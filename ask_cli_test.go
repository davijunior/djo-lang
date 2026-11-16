//go:build !nonet

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runAsk(t *testing.T, stdin string, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := askCLI(args, &out, &errb, strings.NewReader(stdin), func(k string) string { return env[k] })
	return code, out.String(), errb.String()
}

func stub(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL
}

func jsonReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprint(w, body)
}

func TestAskCLISucessoETabela(t *testing.T) {
	var gotAuth, gotCT, gotBody, gotPath string
	url := stub(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotCT, gotPath = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.URL.Path
		b := make([]byte, 1000)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		jsonReply(w, 200, `{"lang":"en","answer":"Showing 2 result(s), page 1. Say \"page 2\" to see more.","type":"post","data":[{"title":"Alpha","views":5,"tags":["go","web"],"nota":null},{"title":"Beta","views":50,"tags":[],"nota":"x"}],"page":1,"limit":2,"more":true}`)
	})
	code, out, errs := runAsk(t, "", nil, url, "list", "posts", "first", "2")
	if code != 0 || errs != "" {
		t.Fatalf("%d %q", code, errs)
	}
	for _, want := range []string{"Showing 2 result(s)", "title", "views", "Alpha", "go, web", "Beta", "--page 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q em:\n%s", want, out)
		}
	}
	if gotPath != "/ask" || gotCT != "application/json" || gotAuth != "" || gotBody != `{"question":"list posts first 2"}` {
		t.Errorf("pedido: %s %s %q %q", gotPath, gotCT, gotAuth, gotBody)
	}
	// ordem das colunas é a do servidor
	if i, j := strings.Index(out, "title"), strings.Index(out, "views"); i > j {
		t.Error("colunas fora de ordem")
	}
	// --page vai no corpo; o idioma PT passa direto
	run := func() (int, string) { c, o, _ := runAsk(t, "", nil, "--page", "2", url, "liste posts"); return c, o }
	if c, _ := run(); c != 0 || !strings.Contains(gotBody, `"page":2`) {
		t.Errorf("--page: %d %q", c, gotBody)
	}
}

func TestAskCLIContagemPT(t *testing.T) {
	url := stub(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, `{"lang":"pt","answer":"São 4.","type":"post","count":4}`)
	})
	code, out, _ := runAsk(t, "", nil, url, "quantos posts")
	if code != 0 || strings.TrimSpace(out) != "São 4." {
		t.Errorf("%d %q", code, out)
	}
}

func TestAskCLIJSONCru(t *testing.T) {
	body := `{"lang":"en","answer":"ok","count":1}`
	url := stub(t, func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, body) })
	code, out, _ := runAsk(t, "", nil, "--json", url, "how many posts")
	if code != 0 || strings.TrimSpace(out) != body {
		t.Errorf("%d %q", code, out)
	}
}

func TestAskCLIToken(t *testing.T) {
	var got string
	url := stub(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		jsonReply(w, 200, `{"answer":"ok","count":1}`)
	})
	// por variável de ambiente
	if c, _, _ := runAsk(t, "", map[string]string{"DJO_ASK_TOKEN": "tok-env"}, url, "q"); c != 0 || got != "Bearer tok-env" {
		t.Errorf("env: %d %q", c, got)
	}
	// por arquivo
	f := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(f, []byte("tok-arq\nlixo\n"), 0o600)
	if c, _, _ := runAsk(t, "", nil, "--token-file", f, url, "q"); c != 0 || got != "Bearer tok-arq" {
		t.Errorf("arquivo: %d %q", c, got)
	}
	// pela entrada padrão
	if c, _, _ := runAsk(t, "tok-stdin\n", nil, "--token-stdin", url, "q"); c != 0 || got != "Bearer tok-stdin" {
		t.Errorf("stdin: %d %q", c, got)
	}
	// não existe flag --token: o segredo nunca vai em argumento
	if c, _, e := runAsk(t, "", nil, "--token", "x", url, "q"); c != 1 || !strings.Contains(e, "flag provided but not defined") {
		t.Errorf("--token não pode existir: %d %q", c, e)
	}
	// token com quebra de linha, espaço ou controle é recusado antes de sair
	for _, bad := range []string{"a b", "a\x1bb", "áb", strings.Repeat("a", 5000)} {
		if c, _, _ := runAsk(t, "", map[string]string{"DJO_ASK_TOKEN": bad}, url, "q"); c != 1 {
			t.Errorf("token ruim %q aceito: %d", bad, c)
		}
	}
	if c, _, _ := runAsk(t, "", nil, "--token-file", f, "--token-stdin", url, "q"); c != 1 {
		t.Error("dois jeitos de token")
	}
	if c, _, _ := runAsk(t, "\n", nil, "--token-stdin", url, "q"); c != 1 {
		t.Error("token vazio")
	}
	if c, _, _ := runAsk(t, "", nil, "--token-file", "/nao/existe", url, "q"); c != 1 {
		t.Error("arquivo inexistente")
	}
}

func TestAskCLICodigosDeSaida(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   int
		msg    string
	}{
		{401, `{"erro":"token inválido ou sem permissão"}`, 4, "token inválido"},
		{429, `{"erro":"muitas perguntas; tente de novo em instantes"}`, 4, "muitas perguntas"},
		{403, `{"erro":"Writing is not accepted.","tipo":"not_accepted"}`, 3, "not accepted"},
		{422, `{"erro":"I did not understand.","tipo":"not_understood"}`, 3, "did not understand"},
		{413, `{"erro":"corpo grande demais"}`, 3, "grande"},
		{503, `{"erro":"Could not answer"}`, 2, "Could not"},
		{504, `{"erro":"The question took too long."}`, 2, "too long"},
		{500, `{"erro":"erro interno"}`, 2, "interno"},
	}
	for _, c := range cases {
		c := c
		url := stub(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "7")
			jsonReply(w, c.status, c.body)
		})
		code, out, errs := runAsk(t, "", nil, url, "q")
		if code != c.code || out != "" || !strings.Contains(errs, c.msg) {
			t.Errorf("%d: código %d (queria %d) out=%q err=%q", c.status, code, c.code, out, errs)
		}
	}
	// 429 informa quando tentar de novo
	url := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		jsonReply(w, 429, `{"erro":"x"}`)
	})
	if _, _, e := runAsk(t, "", nil, url, "q"); !strings.Contains(e, "7 s") {
		t.Errorf("Retry-After: %q", e)
	}
	// --json mantém o código
	url = stub(t, func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 403, `{"erro":"x"}`) })
	if c, out, _ := runAsk(t, "", nil, "--json", url, "q"); c != 3 || !strings.Contains(out, `"erro"`) {
		t.Errorf("--json com 403: %d %q", c, out)
	}
	// site sem /ask (404 com HTML)
	url = stub(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	if c, _, e := runAsk(t, "", nil, url, "q"); c != 3 || !strings.Contains(e, "não publica o /ask") {
		t.Errorf("404: %d %q", c, e)
	}
	// redirecionamento não é seguido (e o token não vai junto)
	var leaked string
	other := stub(t, func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization")
		jsonReply(w, 200, `{"answer":"x"}`)
	})
	url = stub(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other+"/ask", 307) })
	c, _, e := runAsk(t, "", map[string]string{"DJO_ASK_TOKEN": "segredo"}, url, "q")
	if c != 2 || leaked != "" || !strings.Contains(e, "redirecion") {
		t.Errorf("redirect: %d leaked=%q %q", c, leaked, e)
	}
}

func TestAskCLIRedeETempo(t *testing.T) {
	// servidor que não responde: timeout
	url := stub(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) })
	start := time.Now()
	code, _, errs := runAsk(t, "", nil, "--timeout", "200ms", url, "q")
	if code != 2 || !strings.Contains(errs, "tempo esgotado") || time.Since(start) > 1500*time.Millisecond {
		t.Errorf("timeout: %d %q em %v", code, errs, time.Since(start))
	}
	// porta fechada
	if code, _, e := runAsk(t, "", nil, "http://127.0.0.1:1", "q"); code != 2 || !strings.Contains(e, "não consegui falar") {
		t.Errorf("recusada: %d %q", code, e)
	}
	// resposta enorme
	url = stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"answer":"`+strings.Repeat("a", 2<<20)+`"}`)
	})
	if code, out, e := runAsk(t, "", nil, url, "q"); code != 2 || out != "" || !strings.Contains(e, "passa de") {
		t.Errorf("resposta enorme: %d %d bytes %q", code, len(out), e)
	}
	// não é JSON
	url = stub(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>oi</html>") })
	if code, out, _ := runAsk(t, "", nil, url, "q"); code != 2 || out != "" {
		t.Errorf("não-JSON: %d %q", code, out)
	}
}

func TestAskCLIControlesNoTerminal(t *testing.T) {
	evil := `{"answer":"oi \u001b]0;PWNED\u0007\u001b[31mvermelho\u001b[0m \u009b2J ‮abc","erro":"x","data":[{"a\u001b[2J":"v\u001b[1m\nnova linha","b":"\u0007bip"}]}`
	url := stub(t, func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, evil) })
	code, out, _ := runAsk(t, "", nil, url, "q")
	if code != 0 {
		t.Fatal(code)
	}
	for _, bad := range []string{"\x1b", "\x07", "\u009b", "‮"} {
		if strings.Contains(out, bad) {
			t.Errorf("caractere de controle %q chegou ao terminal:\n%q", bad, out)
		}
	}
	if !strings.Contains(out, `\x1b`) || !strings.Contains(out, "\\u202e") {
		t.Errorf("deveria mostrar os escapes visíveis: %q", out)
	}
	// o mesmo na mensagem de erro
	url = stub(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 422, `{"erro":"ruim \u001b[2J\u001b]52;c;ZWNobyBwd25lZA==\u0007"}`)
	})
	_, _, errs := runAsk(t, "", nil, url, "q")
	if strings.ContainsAny(errs, "\x1b\x07") || !strings.Contains(errs, `\x1b`) {
		t.Errorf("erro com controles: %q", errs)
	}
	// e em --json, quando a resposta não é JSON válido
	url = stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "\x1b[31mfalso\x1b[0m")
	})
	_, out, _ = runAsk(t, "", nil, "--json", url, "q")
	if strings.Contains(out, "\x1b") {
		t.Errorf("--json com texto cru: %q", out)
	}
}

func TestAskCLIEndereco(t *testing.T) {
	bad := []string{
		"", "http://exemplo.com", "ftp://exemplo.com", "https://user:senha@exemplo.com", "exemplo.com/outro", "exemplo.com/ask?x=1",
		"exemplo com", "exemplo.com:99999", "exemplo.com:0", "exe\x1bmplo.com", "-exemplo.com", "https://", "//exemplo.com", "exemplo.com#x",
		"http://localhost.exemplo.com", "http://127.0.0.1.exemplo.com", "https://exemplo.com\\@evil.com",
	}
	for _, b := range bad {
		if c, _, _ := runAsk(t, "", nil, b, "q"); c != 1 {
			t.Errorf("%q deveria ser recusado (código %d)", b, c)
		}
	}
	// http só em localhost; --insecure libera
	for _, ok := range []string{"http://localhost:1", "http://127.0.0.1:1", "http://[::1]:1"} {
		if c, _, e := runAsk(t, "", nil, "--timeout", "300ms", ok, "q"); c != 2 {
			t.Errorf("%s: esperava erro de rede (2), veio %d %q", ok, c, e)
		}
	}
	if c, _, e := runAsk(t, "", nil, "--insecure", "--timeout", "300ms", "http://192.0.2.1:1", "q"); c != 2 {
		t.Errorf("--insecure: %d %q", c, e)
	}
	// sem esquema = https (a conexão falha em TLS/porta, mas nunca vai em texto aberto)
	if got, err := askEndpoint("exemplo.com", false); err != nil || got != "https://exemplo.com/ask" {
		t.Errorf("padrão https: %q %v", got, err)
	}
	if got, err := askEndpoint("exemplo.com:8443/ask/", false); err != nil || got != "https://exemplo.com:8443/ask" {
		t.Errorf("porta: %q %v", got, err)
	}
	// uso
	if c, _, _ := runAsk(t, "", nil); c != 1 {
		t.Error("sem argumentos")
	}
	if c, _, _ := runAsk(t, "", nil, "exemplo.com"); c != 1 {
		t.Error("sem pergunta")
	}
	if c, _, _ := runAsk(t, "", nil, "--page", "-1", "exemplo.com", "q"); c != 1 {
		t.Error("página negativa")
	}
}

func TestAskCLIDjoURL(t *testing.T) {
	for _, c := range []struct{ in, host, q string }{
		{`djo://meusite.com/ask='me da a lista de posts'`, "meusite.com", "me da a lista de posts"},
		{`djo://meusite.com/ask="list posts"`, "meusite.com", "list posts"},
		{`djo://meusite.com/ask=list%20posts`, "meusite.com", "list posts"},
		{`djo://meusite.com:8443/ask`, "meusite.com:8443", ""},
		{`djo://meusite.com`, "meusite.com", ""},
	} {
		h, q, err := askParseDjoURL(c.in)
		if err != nil || h != c.host || q != c.q {
			t.Errorf("%s: %q %q %v", c.in, h, q, err)
		}
	}
	for _, bad := range []string{"djo://", "djo:///ask=x", "djo://site/outra=x", "djo://site/ask/x"} {
		if _, _, err := askParseDjoURL(bad); err == nil {
			t.Errorf("%q deveria falhar", bad)
		}
	}
	// vira https (e sem http:// aberto): host remoto com djo:// nunca usa texto aberto
	if c, _, e := runAsk(t, "", nil, `djo://192.0.2.1/ask='x'`, "y"); c != 1 || !strings.Contains(e, "duas vezes") {
		t.Errorf("pergunta duplicada: %d %q", c, e)
	}
	if got, _ := askEndpoint("meusite.com", false); !strings.HasPrefix(got, "https://") {
		t.Error("djo:// precisa virar https")
	}
}
