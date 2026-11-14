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

const rotasOK = `// rotas do site
Route.get:"/":"paginas/inicio.djo"
Route.get:"/posts":{"html": "paginas/posts.djo.html", "agent": "agentes/posts.djo"}
Route.get:"/posts/novo":"paginas/novo.djo"
Route.get:"/posts/{id}":{"html": "paginas/post.djo", "agent": "agentes/post.djo"}
Route.post:"/entrar":"paginas/entrar.djo"
Route.get:"/docs/{...caminho}":"paginas/docs.djo"
Route.static:"/assets":"publico"
`

func TestRotasDeclaradas(t *testing.T) {
	ts := site(t, map[string]string{
		"rotas.djo":              rotasOK,
		"paginas/inicio.djo":     `print "inicio"`,
		"paginas/posts.djo.html": `<ul>posts</ul>`,
		"paginas/novo.djo":       `print "novo"`,
		"paginas/post.djo":       `print "post " + param:"id"`,
		"paginas/entrar.djo":     `print "entrou"`,
		"paginas/docs.djo":       `print Http.param:"caminho"`,
		"agentes/posts.djo":      `print Json.encode:{"posts": [1, 2]}`,
		"agentes/post.djo":       `print Json.encode:{"id": param:"id"}`,
		"publico/a.css":          `body{}`,
		"publico/_oculto.txt":    `x`,
		"publico/pagina.djo":     `print "nao"`,
		"escondida.djo":          `print "vazou"`, // não declarada: não existe
		"posts/[id].djo":         `print "roteamento por arquivo"`,
		"core/lib.djo":           "f x\n  return 1\n",
	})
	do := func(method, p, accept string) (int, string, http.Header) {
		req, _ := http.NewRequest(method, ts.URL+p, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header
	}
	type c struct {
		method, path, accept string
		code                 int
		body                 string
	}
	for _, k := range []c{
		{"GET", "/", "text/html", 200, "inicio"},
		{"GET", "/posts", "text/html,application/xhtml+xml", 200, "<ul>posts</ul>"},
		{"GET", "/posts", "application/json", 200, `{"posts":[1,2]}`},
		{"GET", "/posts", "", 200, "<ul>posts</ul>"},     // sem Accept: a tela
		{"GET", "/posts/novo", "text/html", 200, "novo"}, // fixo vence {id}
		{"GET", "/posts/7", "text/html", 200, "post 7"},
		{"GET", "/posts/7", "application/json", 200, `{"id":"7"}`},
		{"GET", "/docs/a/b", "", 200, "a/b"},
		{"GET", "/assets/a.css", "", 200, "body{}"},
		{"GET", "/assets/_oculto.txt", "", 404, ""},
		{"GET", "/assets/pagina.djo", "", 404, ""},
		{"GET", "/assets/../escondida.djo", "", 404, ""},
		{"GET", "/escondida", "", 404, ""},
		{"GET", "/escondida.djo", "", 404, ""},
		{"GET", "/core/lib.djo", "", 404, ""},
		{"GET", "/a.css", "", 404, ""},
		{"POST", "/entrar", "", 200, "entrou"},
		{"GET", "/entrar", "", 405, ""},
		{"POST", "/posts", "", 405, ""},
		{"GET", "/novo", "", 404, ""},
		{"HEAD", "/", "", 200, ""},
	} {
		code, body, h := do(k.method, k.path, k.accept)
		if code != k.code || (k.body != "" && body != k.body) {
			t.Errorf("%s %s [%s] = %d %q, queria %d %q", k.method, k.path, k.accept, code, body, k.code, k.body)
		}
		if k.path == "/posts" && k.method == "GET" && k.code == 200 && h.Get("Vary") != "Accept" {
			t.Errorf("faltou Vary: Accept em /posts")
		}
	}
	if _, _, h := do("GET", "/posts", "application/json"); !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		t.Errorf("a face de agente deve responder application/json, veio %q", h.Get("Content-Type"))
	}
	if _, _, h := do("GET", "/entrar", ""); h.Get("Allow") != "POST" {
		t.Errorf("Allow = %q", h.Get("Allow"))
	}
}

func TestRotasSoUmaFace(t *testing.T) {
	ts := site(t, map[string]string{
		"rotas.djo":   "Route.get:\"/sobre\":\"p/sobre.djo\"\nRoute.get:\"/api\":{\"agent\": \"p/api.djo\"}\n",
		"p/sobre.djo": `print "sobre"`,
		"p/api.djo":   `print "{}"`,
	})
	req := func(path, accept string) (int, string) {
		r, _ := http.NewRequest("GET", ts.URL+path, nil)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		resp, err := noRedirect.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b))
	}
	if code, _ := req("/sobre", "application/json"); code != 406 {
		t.Errorf("agente em página só de tela deveria dar 406, deu %d", code)
	}
	if code, _ := req("/api", "text/html"); code != 406 {
		t.Errorf("navegador em rota só de agente deveria dar 406, deu %d", code)
	}
	if code, body := req("/api", "*/*"); code != 200 || body != "{}" {
		t.Errorf("*/* deveria servir a face que existe: %d %q", code, body)
	}
}

func TestRotasErros(t *testing.T) {
	cases := map[string]string{
		`Route.get:"/x":"nao/existe.djo"`:                        "não existe",
		`Route.get:"x":"a.djo"`:                                  "começar com /",
		`Route.get:"/x":"../a.djo"`:                              "dentro do site",
		`Route.get:"/x":"core/lib.djo"`:                          "core/",
		`Route.get:"/x":"a.txt"`:                                 "não é uma página",
		`Route.get:"/x":{"agente": "a.djo"}`:                     "desconhecida",
		`Route.get:"/x":{}`:                                      "ao menos",
		"Route.get:\"/x\":\"a.djo\"\nRoute.get:\"/x\":\"a.djo\"": "já foi declarada",
		`Route.get:"/{...a}/b":"a.djo"`:                          "último",
		`Route.static:"/s":"core"`:                               "core",
		`Route.static:"/s":"naoexiste"`:                          "não existe",
		`print "oi"`:                                             "só aceita",
		`var x = 1`:                                              "só aceita",
	}
	for src, want := range cases {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "a.djo"), []byte(`print "a"`), 0o644)
		os.WriteFile(filepath.Join(root, "a.txt"), []byte(`a`), 0o644)
		os.MkdirAll(filepath.Join(root, "core"), 0o755)
		os.WriteFile(filepath.Join(root, "core", "lib.djo"), []byte("f x\n  return 1\n"), 0o644)
		os.WriteFile(filepath.Join(root, "rotas.djo"), []byte(src), 0o644)
		_, err := NewServer(root, false)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: erro = %v, queria conter %q", src, err, want)
		}
	}
}

func TestRouteForaDoRotas(t *testing.T) {
	ts := site(t, map[string]string{"index.djo": `Route.get:"/":"index.djo"`})
	_, body, _ := get(t, ts, "/")
	if !strings.Contains(body, "só pode ser usado dentro de rotas.djo") {
		t.Errorf("body = %q", body)
	}
}

func TestAnunciaPortaDosAgentes(t *testing.T) {
	ts := site(t, map[string]string{
		"rotas.djo": `Route.get:"/":{"html": "paginas/inicio.djo.html"}
Route.get:"/frag":{"html": "paginas/frag.djo.html", "agent": "agentes/frag.djo"}
Route.get:"/pronta":{"html": "paginas/pronta.djo.html"}
Route.get:"/dados":{"agent": "agentes/frag.djo"}
`,
		"paginas/inicio.djo.html": "<!doctype html><html><head><title>x</title></head><BODY class=\"a\"><p>oi</p></body></html>",
		"paginas/frag.djo.html":   `<ul>sem head</ul>`,
		"paginas/pronta.djo.html": `<html><head><meta name="ai-manifest" content="/x"></head><body>ok</body></html>`,
		"agentes/frag.djo":        `print Json.encode:{"a": 1}`,
	})
	get := func(p, accept string) (int, string) {
		req, _ := http.NewRequest("GET", ts.URL+p, nil)
		req.Header.Set("Accept", accept)
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	_, h := get("/", "text/html")
	for _, want := range []string{
		`<link rel="alternate" type="application/json" href="/.well-known/ai-manifest.json">`,
		`<meta name="ai-manifest" content="/.well-known/ai-manifest.json">`,
		"<BODY class=\"a\">\n<!-- Agente de IA:",
		`Accept: application/json. --><p>oi</p>`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("falta %q em:\n%s", want, h)
		}
	}
	if i, j := strings.Index(h, "<meta name=\"ai-manifest\""), strings.Index(h, "</head>"); i < 0 || i > j {
		t.Errorf("o aviso deve ficar dentro do <head>:\n%s", h)
	}
	if _, f := get("/frag", "text/html"); f != `<ul>sem head</ul>` {
		t.Errorf("fragmento não deve mudar: %q", f)
	}
	if _, p := get("/pronta", "text/html"); strings.Count(p, "ai-manifest") != 1 || strings.Contains(p, "Agente de IA") {
		t.Errorf("página que já avisa não deve ganhar de novo: %q", p)
	}
	if _, a := get("/frag", "application/json"); strings.Contains(a, "Agente de IA") || !strings.Contains(a, `"a"`) {
		t.Errorf("a face de agente não leva o aviso: %q", a)
	}

	// 406 que ensina
	code, m := get("/pronta", "application/json")
	if code != 406 || !strings.Contains(m, "só para humanos") || !strings.Contains(m, "/.well-known/ai-manifest.json") {
		t.Errorf("406 de rota só de tela: %d %q", code, m)
	}
	code, m = get("/dados", "text/html")
	if code != 406 || !strings.Contains(m, "só para agentes") || !strings.Contains(m, "Accept: application/json") {
		t.Errorf("406 de rota só de agente: %d %q", code, m)
	}
}

func TestSemAgentesSemAviso(t *testing.T) {
	ts := site(t, map[string]string{
		"rotas.djo":               `Route.get:"/":"paginas/inicio.djo.html"`,
		"paginas/inicio.djo.html": "<html><head></head><body>oi</body></html>",
	})
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "ai-manifest") || strings.Contains(string(b), "Agente de IA") {
		t.Errorf("site sem face de agente não deve anunciar nada: %s", b)
	}
}
