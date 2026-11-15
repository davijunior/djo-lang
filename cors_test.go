//go:build !noweb

package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rotasCors = `Route.cors:{"origins": ["https://app.exemplo.com", "http://localhost:5173"], "max_age": 120}
Route.get:"/itens/{id}":{"agent": "p/ver.djo", "html": "p/ver.djo", "cache": 30}
Route.delete_action:"/itens/{id}":"apagar_item"
Route.get:"/eu":"p/eu.djo"
`

func TestCors(t *testing.T) {
	ts := site(t, map[string]string{
		"core/m.djo": "action apagar_item\n    params[id[Int]]\n    return {\"apagado\": id}\n",
		"rotas.djo":  rotasCors,
		"p/ver.djo":  `Http.json:{"id": param:"id"}`,
		"p/eu.djo":   `Http.json:{"token": Http.bearer:}`,
	})
	do := func(method, p string, hdr map[string]string) (int, string, http.Header) {
		req, _ := http.NewRequest(method, ts.URL+p, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header
	}
	ok := "https://app.exemplo.com"

	// preflight de uma origem permitida
	c, _, h := do("OPTIONS", "/itens/3", map[string]string{"Origin": ok, "Access-Control-Request-Method": "DELETE"})
	if c != 204 || h.Get("Access-Control-Allow-Origin") != ok || h.Get("Access-Control-Allow-Methods") != "DELETE, GET, OPTIONS" ||
		h.Get("Access-Control-Allow-Headers") != "Content-Type, Authorization" || h.Get("Access-Control-Max-Age") != "120" ||
		h.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("preflight = %d %v", c, h)
	}
	// origem de fora: 403 e nenhuma permissão
	c, _, h = do("OPTIONS", "/itens/3", map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "DELETE"})
	if c != 403 || h.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight de fora = %d %v", c, h)
	}
	// caminho que não existe
	if c, _, _ := do("OPTIONS", "/nada", map[string]string{"Origin": ok, "Access-Control-Request-Method": "GET"}); c == 204 {
		t.Errorf("preflight de rota inexistente = %d", c)
	}
	// OPTIONS comum (sem Origin) continua 405
	if c, _, _ := do("OPTIONS", "/itens/3", nil); c != 405 {
		t.Errorf("OPTIONS comum = %d", c)
	}

	// resposta normal: a permissão vale para a origem que perguntou, mesmo com cache
	for i := 0; i < 2; i++ {
		_, _, h = do("GET", "/itens/3", map[string]string{"Origin": ok, "Accept": "application/json"})
		if h.Get("Access-Control-Allow-Origin") != ok || !strings.Contains(strings.Join(h.Values("Vary"), ","), "Origin") {
			t.Errorf("GET permitido (%d): %v", i, h)
		}
	}
	_, _, h = do("GET", "/itens/3", map[string]string{"Origin": "https://evil.example", "Accept": "application/json"})
	if h.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("a permissão em cache vazou para outra origem: %v", h)
	}
	_, _, h = do("GET", "/itens/3", map[string]string{"Origin": "http://localhost:5173", "Accept": "application/json"})
	if h.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("segunda origem: %v", h)
	}
	// sem Origin, nada de CORS
	if _, _, h = do("GET", "/itens/3", map[string]string{"Accept": "application/json"}); h.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("sem Origin: %v", h)
	}
	// a action também responde com a permissão
	if c, b, h := do("DELETE", "/itens/9", map[string]string{"Origin": ok}); c != 200 || b != `{"apagado":9}` || h.Get("Access-Control-Allow-Origin") != ok {
		t.Errorf("DELETE = %d %s %v", c, b, h)
	}

	// Http.bearer:
	for hdr, want := range map[string]string{
		"Bearer abc123": `{"token":"abc123"}`, "bearer  xyz ": `{"token":"xyz"}`,
		"Basic abc": `{"token":null}`, "Bearer ": `{"token":null}`, "": `{"token":null}`,
	} {
		m := map[string]string{}
		if hdr != "" {
			m["Authorization"] = hdr
		}
		if _, b, _ := do("GET", "/eu", m); b != want {
			t.Errorf("bearer %q = %s, queria %s", hdr, b, want)
		}
	}
}

func TestCorsCredenciaisEQualquerOrigem(t *testing.T) {
	ts := site(t, map[string]string{
		"rotas.djo": `Route.cors:{"origins": ["https://a.com"], "credentials": true, "headers": ["X-Chave"]}
Route.get:"/x":"p/x.djo"
`,
		"p/x.djo": `print "x"`,
	})
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/x", nil)
	req.Header.Set("Origin", "https://a.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Credentials") != "true" || resp.Header.Get("Access-Control-Allow-Headers") != "X-Chave" {
		t.Errorf("credenciais: %v", resp.Header)
	}

	ts2 := site(t, map[string]string{
		"rotas.djo": `Route.cors:{"origins": ["*"]}
Route.get:"/x":"p/x.djo"
`,
		"p/x.djo": `print "x"`,
	})
	req, _ = http.NewRequest("GET", ts2.URL+"/x", nil)
	req.Header.Set("Origin", "https://qualquer.com")
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" || resp.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("qualquer origem: %v", resp.Header)
	}
}

func TestCorsErrosDeDeclaracao(t *testing.T) {
	cases := map[string]string{
		`Route.cors:{}`:                                                        "informe \"origins\"",
		`Route.cors:{"origins": []}`:                                           "ao menos uma origem",
		`Route.cors:{"origins": ["app.com"]}`:                                  "origem inválida",
		`Route.cors:{"origins": ["https://app.com/"]}`:                         "origem inválida",
		`Route.cors:{"origins": ["*"], "credentials": true}`:                   "não pode ser usado com",
		`Route.cors:{"origins": ["https://a.com"], "max_age": -1}`:             "max_age",
		`Route.cors:{"origins": ["https://a.com"], "headers": ["a b"]}`:        "cabeçalho inválido",
		`Route.cors:{"origens": ["https://a.com"]}`:                            "não existe",
		"Route.cors:{\"origins\": [\"*\"]}\nRoute.cors:{\"origins\": [\"*\"]}": "só pode aparecer uma vez",
	}
	for src, want := range cases {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "rotas.djo"), []byte(src), 0o644)
		if _, err := NewServer(root, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: erro %v, queria %q", src, err, want)
		}
	}
}
