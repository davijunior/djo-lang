//go:build !noweb

package main

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func site(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "site")
	files["../segredo.txt"] = "segredo"
	for name, content := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := NewServer(root, true)
	if err != nil {
		t.Fatal(err)
	}
	srv.maxSteps = 100_000
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func get(t *testing.T, ts *httptest.Server, p string) (int, string, http.Header) {
	t.Helper()
	resp, err := noRedirect.Get(ts.URL + p)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestTemplateBasico(t *testing.T) {
	out, err := ParseTemplate("<ul>\n{% var i = 0 %}\n{% while{ cond[i < 3] %}\n<li>{{ i }}</li>\n{% i++ } %}\n</ul>\n{# nota #}{{! \"<b>\" }}|{{ \"<b>\" }}\n")
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := NewInterp(&buf).Run(out); err != nil {
		t.Fatal(err)
	}
	want := "<ul>\n<li>0</li>\n<li>1</li>\n<li>2</li>\n</ul>\n<b>|&lt;b&gt;\n"
	if buf.String() != want {
		t.Fatalf("saída %q, esperado %q", buf.String(), want)
	}
}

func TestTemplateIfElseEErros(t *testing.T) {
	run := func(src string) (string, error) {
		prog, err := ParseTemplate(src)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		err = NewInterp(&b).Run(prog)
		return b.String(), err
	}
	out, err := run("{% if{ cond[false] %}A{% } elif{ cond[true] %}B{% } else{ %}C{% } %}")
	if err != nil || out != "B" {
		t.Fatalf("saída %q err %v", out, err)
	}
	casos := map[string]string{
		"a {{ x":                  "sem '}}'",
		"\n\n{% print %}":         "linha 3",
		"{{ }}":                   "vazio",
		"{% if\n cond[true]\n %}": "precisa de chaves",
		"x\n{{ naoexiste }}":      "linha 2",
	}
	for src, want := range casos {
		_, err := run(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: erro %v, esperava conter %q", src, err, want)
		}
	}
}

func TestCoalesce(t *testing.T) {
	out, err := run(t, "var a = nil\nprint a ?? \"padrão\"\nprint 5 ?? 1 / 0\nprint (len:\"olá\") + (len:[1, 2])\n")
	if err != nil || out != "padrão\n5\n5\n" {
		t.Fatalf("saída %q err %v", out, err)
	}
}

func TestServidor(t *testing.T) {
	ts := site(t, map[string]string{
		"index.djo.html":  `{% include:"_topo.djo.html" %}<h1>{{ query:"n" ?? "visitante" }}</h1>`,
		"_topo.djo.html":  "<header>topo</header>",
		"sobre.djo.html":  "sobre",
		"api.djo":         "setheader:\"Content-Type\":\"text/plain\"\nprint method: + \" \" + path:",
		"ir.djo":          "redirect:\"/sobre\"\nprint \"nunca\"",
		"form.djo":        "print form:\"a\" ?? \"sem\"",
		"cookie.djo":      "setcookie:\"k\":\"v w\"\nprint cookie:\"k\" ?? \"nenhum\"",
		"erro.djo":        "print naoexiste",
		"loop.djo":        "while{\n cond[true]\n}",
		"estilo.css":      "body{}",
		"404.djo.html":    "nada em {{ path: }}",
		"dir/index.djo":   "print \"dir\"",
		".oculto.djo":     "print 1",
		"sub/_x.djo.html": "x",
		"status.djo":      "status:201\nprint \"criado\"",
	})

	code, body, _ := get(t, ts, "/?n=%3Cb%3E")
	if code != 200 || body != "<header>topo</header><h1>&lt;b&gt;</h1>" {
		t.Errorf("/: %d %q", code, body)
	}
	if code, body, _ = get(t, ts, "/"); body != "<header>topo</header><h1>visitante</h1>" {
		t.Errorf("/ padrão: %q", body)
	}
	if code, body, _ = get(t, ts, "/sobre"); code != 200 || body != "sobre" {
		t.Errorf("/sobre: %d %q", code, body)
	}
	code, body, h := get(t, ts, "/api")
	if body != "GET /api\n" || h.Get("Content-Type") != "text/plain" {
		t.Errorf("/api: %q %v", body, h)
	}
	if code, _, h = get(t, ts, "/ir"); code != 302 || h.Get("Location") != "/sobre" {
		t.Errorf("/ir: %d %v", code, h)
	}
	if code, body, h = get(t, ts, "/estilo.css"); code != 200 || body != "body{}" || !strings.HasPrefix(h.Get("Content-Type"), "text/css") {
		t.Errorf("css: %d %q %v", code, body, h)
	}
	if code, _, _ = get(t, ts, "/status"); code != 201 {
		t.Errorf("/status: %d", code)
	}
	if code, _, h = get(t, ts, "/dir"); code != 301 || h.Get("Location") != "/dir/" {
		t.Errorf("/dir: %d %v", code, h)
	}
	if _, body, _ = get(t, ts, "/dir/"); body != "dir\n" {
		t.Errorf("/dir/: %q", body)
	}

	// não serve nem executa o que é escondido
	for _, p := range []string{"/_topo.djo.html", "/.oculto.djo", "/sub/_x.djo.html"} {
		if code, _, _ = get(t, ts, p); code != 404 {
			t.Errorf("%s deveria ser 404, foi %d", p, code)
		}
	}
	// 404 personalizado
	if code, body, _ = get(t, ts, "/nada"); code != 404 || body != "nada em /nada" {
		t.Errorf("404: %d %q", code, body)
	}
	// não escapa da pasta
	for _, p := range []string{"/..%2fsegredo.txt", "/%2e%2e/segredo.txt", "/sub/..%2f..%2fsegredo.txt"} {
		if code, body, _ = get(t, ts, p); code == 200 || body == "segredo" {
			t.Errorf("%s vazou: %d %q", p, code, body)
		}
	}
	// erro de execução vira 500 e laço infinito é interrompido
	if code, body, _ = get(t, ts, "/erro"); code != 500 || !strings.Contains(body, "naoexiste") {
		t.Errorf("/erro: %d %q", code, body)
	}
	if code, body, _ = get(t, ts, "/loop"); code != 500 || !strings.Contains(body, "demorou demais") {
		t.Errorf("/loop: %d %q", code, body)
	}

	// formulário
	resp, _ := http.PostForm(ts.URL+"/form", url.Values{"a": {"valor"}})
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "valor\n" {
		t.Errorf("POST form: %q", b)
	}
	// cookie
	resp, _ = noRedirect.Get(ts.URL + "/cookie")
	resp.Body.Close()
	cs := resp.Cookies()
	if len(cs) != 1 || !cs[0].HttpOnly || cs[0].Name != "k" {
		t.Errorf("cookies: %v", cs)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/cookie", nil)
	req.AddCookie(&http.Cookie{Name: "k", Value: url.QueryEscape("v w")})
	resp, _ = noRedirect.Do(req)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "v w\n" {
		t.Errorf("cookie lido: %q", b)
	}
}

func TestIncludeNaoEscapa(t *testing.T) {
	prog, _ := Parse(`include:"../../etc/passwd"`)
	err := NewInterp(io.Discard).Run(prog)
	if err == nil {
		t.Fatal("include fora da pasta deveria falhar")
	}
}

func TestCoreDoSistema(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "core"), 0o755)
	os.WriteFile(filepath.Join(dir, "core", "post.djo"), []byte("struct Post{\n titulo[String]\n f link\n  return \"/p/\" + self.titulo\n}\nf dobro\n params[n[Int]]\n return n * 2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "index.djo"), []byte("var p = Post:\"a\"\nprint p.link:\nprint dobro:4\n"), 0o644)
	srv, _ := NewServer(dir, false)
	get := func(u string) (int, string) {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("GET", u, nil))
		return rec.Code, rec.Body.String()
	}
	if c, b := get("/"); c != 200 || b != "/p/a\n8\n" {
		t.Fatalf("%d %q", c, b)
	}
	if c, _ := get("/core/post.djo"); c != 404 {
		t.Fatalf("core deveria ser oculto, veio %d", c)
	}
	os.WriteFile(filepath.Join(dir, "core", "ruim.djo"), []byte("print 1\n"), 0o644)
	if c, _ := get("/"); c != 500 {
		t.Fatalf("core com código solto deveria dar 500, veio %d", c)
	}
}

func TestHttpETime(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "api.djo"), []byte(`if
    cond[Http.method: == "POST"]
    var d = Http.json:
    Http.json:{"recebido": d["n"] + 1, "q": Http.query:"x"}:201
else
    Http.json:{"erro": "use POST"}:405
`), 0o644)
	os.WriteFile(filepath.Join(dir, "t.djo"), []byte(`print Time.format:86400:"DD/MM/YYYY HH:mm:ss"
print Time.parse:"2025-01-31":"YYYY-MM-DD"
print Time.parse:"lixo":"YYYY-MM-DD" ?? "nil"
print (Time.date:0)["year"]
print Time.now: > 1700000000
`), 0o644)
	srv, _ := NewServer(dir, false)
	do := func(m, u, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(m, u, strings.NewReader(body))
		srv.ServeHTTP(rec, r)
		return rec
	}
	r := do("POST", "/api?x=oi", `{"n": 41}`)
	if r.Code != 201 || r.Body.String() != `{"recebido":42,"q":"oi"}` || !strings.HasPrefix(r.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%d %q %v", r.Code, r.Body.String(), r.Header())
	}
	if r := do("GET", "/api", ""); r.Code != 405 || r.Body.String() != `{"erro":"use POST"}` {
		t.Fatalf("%d %q", r.Code, r.Body.String())
	}
	r = do("GET", "/t", "")
	want := "02/01/1970 00:00:00\n1738281600\nnil\n1970\ntrue\n"
	if r.Body.String() != want {
		t.Fatalf("%q, esperado %q", r.Body.String(), want)
	}
}

func TestSessaoEHash(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.djo"), []byte(`var n = Session.get:"n" ?? 0
Session.set:"n":n + 1
Session.set:"user":{"nome": "Ana", "tags": [1, 2]}
print Session.get:"n"
print (Session.get:"user")["nome"]
print Session.token:
`), 0o644)
	os.WriteFile(filepath.Join(dir, "v.djo"), []byte(`print Session.verify:(Http.query:"t")
`), 0o644)
	os.WriteFile(filepath.Join(dir, "x.djo"), []byte("Session.destroy:\nprint Session.get:\"n\" ?? \"fim\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "r.djo"), []byte("Session.renew:\nprint Session.get:\"n\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.djo"), []byte("struct P{\n a[Int]\n}\nSession.set:\"p\":(P:1)\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "h.djo"), []byte(`var h = Hash.password:"segredo"
print Hash.verify:"segredo":h
print Hash.verify:"errada":h
print Hash.verify:"x":"lixo"
print h == (Hash.password:"segredo")
print len:(Hash.random:8)
print Hash.sha256:"abc"
`), 0o644)
	srv, _ := NewServer(dir, true)
	var cookies []*http.Cookie
	get := func(u string) (int, string, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", u, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		srv.ServeHTTP(rec, r)
		if cs := rec.Result().Cookies(); len(cs) > 0 {
			cookies = cs
		}
		return rec.Code, rec.Body.String(), rec
	}
	c, b, rec := get("/a")
	if c != 200 || !strings.HasPrefix(b, "1\nAna\n") {
		t.Fatalf("%d %q", c, b)
	}
	ck := rec.Result().Cookies()
	if len(ck) != 1 || ck[0].Name != "djo_sid" || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie da sessão: %+v", ck)
	}
	token := strings.TrimSpace(strings.SplitN(b, "\n", 3)[2])
	if _, b2, _ := get("/a"); !strings.HasPrefix(b2, "2\n") || !strings.HasSuffix(b2, token+"\n") {
		t.Fatalf("a sessão não persistiu: %q", b2)
	}
	if _, v, _ := get("/v?t=" + token); v != "true\n" {
		t.Fatalf("token certo: %q", v)
	}
	if _, v, _ := get("/v?t=errado"); v != "false\n" {
		t.Fatalf("token errado: %q", v)
	}
	old := cookies[0].Value
	if _, v, _ := get("/r"); v != "2\n" || cookies[0].Value == old {
		t.Fatalf("renew: %q, cookie igual=%v", v, cookies[0].Value == old)
	}
	if _, v, _ := get("/x"); v != "fim\n" {
		t.Fatalf("destroy: %q", v)
	}
	cookies = nil
	if c, _, _ := get("/bad"); c != 500 {
		t.Fatalf("struct na sessão deveria dar erro, veio %d", c)
	}
	_, v, _ := get("/h")
	if !strings.HasPrefix(v, "true\nfalse\nfalse\nfalse\n16\nba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") {
		t.Fatalf("hash: %q", v)
	}
}

func TestUpload(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "u.djo"), []byte(`var arq = Http.file:"foto"
if
    cond[arq == nil]
    print "sem arquivo"
else
    print arq["name"] + " " + (str:arq["size"])
    print Http.upload:"foto":"fotos/2025"
print Http.form:"legenda"
`), 0o644)
	os.WriteFile(filepath.Join(dir, "u2.djo"), []byte("print Http.upload:\"foto\":\"../fora\"\n"), 0o644)
	srv, _ := NewServer(dir, true)
	post := func(filename, content, page string) (int, string) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		mw.WriteField("legenda", "oi")
		fw, _ := mw.CreateFormFile("foto", filename)
		fw.Write([]byte(content))
		mw.Close()
		rec := httptest.NewRecorder()
		if page == "" {
			page = "/u"
		}
		r := httptest.NewRequest("POST", page, &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		srv.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
	c, b := post("a b.PNG", "12345", "")
	lines := strings.Split(strings.TrimSpace(b), "\n")
	if c != 200 || len(lines) != 3 || lines[0] != "a b.PNG 5" || lines[2] != "oi" || !strings.HasPrefix(lines[1], "fotos/2025/") || !strings.HasSuffix(lines[1], ".png") {
		t.Fatalf("%d %q", c, b)
	}
	saved, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(lines[1])))
	if err != nil || string(saved) != "12345" {
		t.Fatalf("arquivo salvo: %q %v", saved, err)
	}
	if c, _ := post("pagina.html", "<script>", ""); c != 500 {
		t.Fatalf(".html deveria ser recusado, veio %d", c)
	}
	if c, _ := post("x.djo", "print 1", ""); c != 500 {
		t.Fatalf(".djo deveria ser recusado, veio %d", c)
	}
	if c, _ := post("a.png", "1", "/u2"); c != 500 {
		t.Fatalf("pasta com .. deveria ser recusada, veio %d", c)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/u", nil))
	if !strings.HasPrefix(rec.Body.String(), "sem arquivo") {
		t.Fatalf("sem upload: %q", rec.Body.String())
	}
}

func TestRotasAmigaveis(t *testing.T) {
	ts := site(t, map[string]string{
		"index.djo":             `print "home"`,
		"posts/index.djo":       `print "lista"`,
		"posts/novo.djo":        `print "novo"`,
		"blog/[slug].djo":       `print "post " + param:"slug"`,
		"posts/[id]/editar.djo": `print "editar " + Http.param:"id"`,
		"posts/[id]/index.djo":  `print "ver " + param:"id"`,
		"docs/[...caminho].djo": `print Http.param:"caminho"`,
		"u/[nome].djo.html":     `<b>{{ param:"nome" }}</b>{% var a = Http.args: %}{{ a["nome"] }}`,
		"core/lib.djo":          "f x\n  return 1\n",
		"api/[v]/[rec].djo": `var p = Http.args:
print p["v"] + "/" + p["rec"]`,
		"404.djo": `print "nada"`,
	})
	cases := map[string]string{
		"/posts/":               "lista",
		"/posts/novo":           "novo", // nome exato vence
		"/blog/ola-mundo":       "post ola-mundo",
		"/posts/7/editar":       "editar 7",
		"/docs/a/b/c":           "a/b/c",
		"/docs/x":               "x",
		"/api/v1/usuarios":      "v1/usuarios",
		"/u/ana":                "<b>ana</b>ana",
		"/posts/7/desconhecida": "nada",
		"/docs/":                "nada",
		"/[slug]":               "nada",
		"/posts/[slug]":         "nada",
		"/core/x":               "nada",
	}
	for u, want := range cases {
		_, body, _ := get(t, ts, u)
		if strings.TrimSpace(body) != want {
			t.Errorf("%s = %q, queria %q", u, body, want)
		}
	}
	if code, _, _ := get(t, ts, "/posts/7/editar"); code != 200 {
		t.Errorf("status %d", code)
	}
	if code, _, _ := get(t, ts, "/posts/7/zzz"); code != 404 {
		t.Errorf("status %d", code)
	}
}
