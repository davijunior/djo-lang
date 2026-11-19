//go:build !noweb

package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogIPOf(t *testing.T) {
	for _, c := range []struct{ ip, mode, want string }{
		{"203.0.113.77", "trunc", "203.0.113.0"},
		{"203.0.113.77", "full", "203.0.113.77"},
		{"203.0.113.77", "off", ""},
		{"2001:db8:abcd:1234::5", "trunc", "2001:db8:abcd::"},
		{"::ffff:10.1.2.3", "trunc", "10.1.2.0"},
		{"lixo", "trunc", ""},
	} {
		if got := logIPOf(c.ip, c.mode); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}

func TestLogConfig(t *testing.T) {
	t.Setenv("DJO_LOG_FORMAT", "")
	t.Setenv("DJO_LOG_IP", "")
	if f, i, err := logConfig("", ""); err != nil || f != "text" || i != "trunc" {
		t.Errorf("%s %s %v", f, i, err)
	}
	t.Setenv("DJO_LOG_FORMAT", "json")
	t.Setenv("DJO_LOG_IP", "off")
	if f, i, err := logConfig("", ""); err != nil || f != "json" || i != "off" {
		t.Errorf("%s %s %v", f, i, err)
	}
	if f, i, _ := logConfig("text", "full"); f != "text" || i != "full" { // o flag vence o ambiente
		t.Errorf("%s %s", f, i)
	}
	if _, _, err := logConfig("xml", ""); err == nil || !strings.Contains(err.Error(), "-log-format") {
		t.Error(err)
	}
	if _, _, err := logConfig("", "meio"); err == nil || !strings.Contains(err.Error(), "-log-ip") {
		t.Error(err)
	}
}

const logSiteRotas = "Route.get:\"/cliente/{id}\":{\"agent\": \"agentes/c.djo\", \"about\": \"c\"}\nRoute.get:\"/falha/{id}\":{\"agent\": \"agentes/f.djo\", \"about\": \"f\"}\n"

func logServer(t *testing.T, format string) (*Server, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	for name, c := range map[string]string{
		"rotas.djo":     logSiteRotas,
		"agentes/c.djo": "Http.json:{\"rid\": Http.request_id:}\n",
		"agentes/f.djo": "var x = 1 / 0\n",
		"core/a.djo":    "f util\n    return 1\n",
	} {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	s, err := NewServer(root, false)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	s.logFormat, s.logIP = format, "trunc"
	s.logger = log.New(scrubWriter{&buf}, "", 0)
	return s, &buf
}

const (
	segTokenURL  = "tok-na-url-9f8e7d6c"
	segBearer    = "bearer-secreto-123456"
	segCookie    = "sessao-secreta-abcdef"
	segSenha     = "senha-do-login-xyz987"
	segQuery     = "query-secreta-55aa"
	segEnvSecret = "valor-da-chave-de-ambiente"
)

// dispara requisições com segredos em todos os lugares e devolve tudo o que foi logado
func logScenario(t *testing.T) (string, *httptest.ResponseRecorder) {
	t.Helper()
	t.Setenv("DJO_ALGUMA_SECRET", segEnvSecret)
	s, buf := logServer(t, "json")
	req := httptest.NewRequest("GET", "/cliente/"+segTokenURL+"?api_key="+segQuery, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+segBearer)
	req.Header.Set("Cookie", "sid="+segCookie)
	req.RemoteAddr = "203.0.113.77:5555"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	// login com corpo e uma página que falha com segredo na mensagem
	req2 := httptest.NewRequest("POST", "/login", strings.NewReader(`{"senha":"`+segSenha+`"}`))
	s.ServeHTTP(httptest.NewRecorder(), req2)
	req3 := httptest.NewRequest("GET", "/falha/1", nil)
	req3.Header.Set("Accept", "application/json")
	s.ServeHTTP(httptest.NewRecorder(), req3)
	// o erro carrega segredos no texto, como uma mensagem de erro de banco ou de API faria
	s.serverError(w, httptest.NewRequest("GET", "/x", nil),
		&djoError{Line: 1, Code: "erro_api", Msg: "falhou: Authorization: Bearer " + segBearer + " e " + segEnvSecret +
			` e {"password":"` + segSenha + `"} em postgres://usuario:` + segSenha + `@db/x`})
	return buf.String(), w
}

func TestLogJSONSemSegredos(t *testing.T) {
	out, _ := logScenario(t)
	for _, seg := range []string{segTokenURL, segBearer, segCookie, segSenha, segQuery, segEnvSecret, "203.0.113.77"} {
		if strings.Contains(out, seg) {
			t.Errorf("o segredo %q apareceu no log:\n%s", seg, out)
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		t.Fatalf("linhas: %d\n%s", len(lines), out)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("não é JSON: %v\n%s", err, lines[0])
	}
	if first["route"] != "/cliente/{id}" || first["method"] != "GET" || first["status"] != float64(200) || first["ip"] != "203.0.113.0" || first["level"] != "info" {
		t.Errorf("%v", first)
	}
	if rid, _ := first["request_id"].(string); !requestIDRe.MatchString(rid) {
		t.Errorf("request_id %v", first["request_id"])
	}
	for _, k := range []string{"ts", "ms", "bytes"} {
		if _, ok := first[k]; !ok && k == "ts" {
			t.Errorf("faltou %s", k)
		}
	}
	// só campos da lista fechada
	allowed := map[string]bool{"ts": true, "level": true, "request_id": true, "method": true, "route": true, "status": true, "ms": true, "bytes": true, "ip": true, "error_code": true, "msg": true, "stack": true}
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("linha que não é JSON: %q", ln)
		}
		for k := range m {
			if !allowed[k] {
				t.Errorf("campo fora da lista: %s", k)
			}
		}
	}
	// o erro 500 saiu com error_code e a rota (padrão)
	if !strings.Contains(out, `"error_code":"erro"`) && !strings.Contains(out, `"error_code":"internal_error"`) {
		t.Errorf("sem error_code:\n%s", out)
	}
}

// Teste de mutação: sem o depurador os segredos do texto livre APARECEM. Prova que o teste acima enxerga o vazamento.
func TestLogDepuradorDesligadoVaza(t *testing.T) {
	prev := scrubLog
	scrubLog = func(s string) string { return s }
	t.Cleanup(func() { scrubLog = prev })
	out, _ := logScenario(t)
	vazou := 0
	for _, seg := range []string{segBearer, segSenha, segEnvSecret} {
		if strings.Contains(out, seg) {
			vazou++
		}
	}
	if vazou == 0 {
		t.Fatal("com o depurador desligado nada vazou: o teste não detecta vazamento")
	}
}

func TestRequestIDHeaderERoute(t *testing.T) {
	s, _ := logServer(t, "text")
	req := httptest.NewRequest("GET", "/cliente/1", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Request-Id", "meu-id-12345")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Header().Get("X-Request-Id") != "meu-id-12345" || !strings.Contains(w.Body.String(), `"meu-id-12345"`) {
		t.Errorf("id aceito do proxy: %v %s", w.Header(), w.Body.String())
	}
	// id inválido (curto, com espaço, injeção) é trocado por um novo
	for _, bad := range []string{"x", "com espaco no id", "a\"b<script>12345678"} {
		req := httptest.NewRequest("GET", "/cliente/1", nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Request-Id", bad)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		got := w.Header().Get("X-Request-Id")
		if got == bad || !strings.HasPrefix(got, "rq_") || !strings.Contains(w.Body.String(), got) {
			t.Errorf("%q -> %q", bad, got)
		}
	}
	var _ http.Handler = s
}

func TestLogTextoContinuaIgual(t *testing.T) {
	s, buf := logServer(t, "text")
	req := httptest.NewRequest("GET", "/cliente/9?x=1", nil)
	req.Header.Set("Accept", "application/json")
	s.ServeHTTP(httptest.NewRecorder(), req)
	if got := buf.String(); !strings.HasPrefix(got, "GET /cliente/9 200 ") || strings.Contains(got, "x=1") {
		t.Errorf("%q", got)
	}
}

// Sem rotas.djo (roteamento por arquivo) o log mostra o arquivo que atendeu, não a URL.
func TestLogRoteamentoPorArquivo(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "blog"), 0o755)
	os.WriteFile(filepath.Join(root, "blog", "post.djo"), []byte("print \"oi\"\n"), 0o644)
	s, err := NewServer(root, false)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	s.logFormat, s.logIP = "json", "off"
	s.logger = log.New(scrubWriter{&buf}, "", 0)
	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/blog/post?token=abc123xyz", nil))
	if !strings.Contains(buf.String(), `"route":"blog/post.djo"`) || strings.Contains(buf.String(), "abc123xyz") {
		t.Errorf("%s", buf.String())
	}
}
