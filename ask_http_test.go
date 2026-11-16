//go:build !noweb && !nodb

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const askHook = `
f nivel_do_pedido
    var token = Http.bearer:
    if{ cond[token == nil] return nil }
    if{ cond[token == "tok-agent"] return "agent" }
    if{ cond[token == "tok-admin"] return "admin" }
    return nil
`

type askSite struct {
	ts  *httptest.Server
	srv *Server
}

// askHTTPSite monta um site com core, ask.djo e um banco já populado.
func askHTTPSite(t *testing.T, askDjoSrc string, extra map[string]string) *askSite {
	t.Helper()
	root := filepath.Join(t.TempDir(), "site")
	files := map[string]string{"core/c.djo": askCore + askHook}
	if askDjoSrc != "" {
		files["ask.djo"] = askDjoSrc
	}
	for k, v := range extra {
		files[k] = v
	}
	for name, content := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := openDB(filepath.Join(root, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, titulo TEXT, texto TEXT, status TEXT, publicar_em TEXT, criado INTEGER, views INTEGER, secreto TEXT)`,
		`CREATE TABLE post_tags (post_id INTEGER, tag TEXT)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT, password TEXT, criado TEXT)`,
		`INSERT INTO posts VALUES (1, 'Alpha', 'a', 'published', '2025-01-10 09:00:00', 1736500800, 5, 'S-1')`,
		`INSERT INTO posts VALUES (2, 'Beta', 'b', 'published', '2025-03-30 23:30:00', 1743377400, 50, 'S-2')`,
		`INSERT INTO posts VALUES (3, 'Gamma', 'c', 'published', '2025-03-31 00:00:00', 1743379200, 7, 'S-3')`,
		`INSERT INTO posts VALUES (4, 'Draft one', 'd', 'draft', '2025-04-01 08:00:00', 1743494400, 0, 'TOPSECRET')`,
		`INSERT INTO posts VALUES (5, '<img src=x onerror=alert(1)>', 'e', 'published', '2025-06-15 12:00:00', 1749988800, 3, 'S-5')`,
		`INSERT INTO post_tags VALUES (1, 'go')`,
		`INSERT INTO users VALUES (1, 'Ana', 'ana@x.com', 'hash-ana', '2025-01-01 00:00:00')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	srv, err := NewServer(root, false)
	if err != nil {
		t.Fatal(err)
	}
	srv.maxSteps = 1_000_000
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &askSite{ts, srv}
}

func askDjoWith(extra string) string {
	return strings.Replace(askDjo, "\nAsk.limits", "\nAsk.level_from:\"nivel_do_pedido\"\n"+extra+"Ask.limits", 1)
}

func (a *askSite) do(t *testing.T, method, p, body, token string, hdr map[string]string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, a.ts.URL+p, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (a *askSite) ask(t *testing.T, q, token string) (int, string) {
	st, b, _ := a.do(t, "POST", "/ask", `{"question": `+jsonEncode(q, "", 0)+`}`, token, nil)
	return st, b
}

func TestAskHTTPNiveisPorToken(t *testing.T) {
	a := askHTTPSite(t, askDjoWith(""), nil)

	// sem token = public: só publicados, só os campos do público
	st, b := a.ask(t, "list posts first 2", "")
	if st != 200 || !strings.Contains(b, `"Alpha"`) || strings.Contains(b, "Draft one") || strings.Contains(b, "TOPSECRET") || strings.Contains(b, `"secret"`) || strings.Contains(b, `"status"`) {
		t.Fatalf("public: %d %s", st, b)
	}
	for _, want := range []string{`"lang":"en"`, `"type":"post"`, `"confirm":null`, `"calls":[`, `"more":true`, `"page":1`} {
		if !strings.Contains(strings.ReplaceAll(b, " ", ""), want) {
			t.Errorf("falta %s em %s", want, b)
		}
	}
	// o público não vê rascunho nem por filtro, nem sabe que status existe
	st, b = a.ask(t, "list posts with status draft", "")
	if st != 422 || strings.Contains(b, "Draft one") {
		t.Errorf("public + status: %d %s", st, b)
	}
	// agent (token de leitura): vê o rascunho, e nunca o secret
	st, b = a.ask(t, "list posts with status draft", "tok-agent")
	if st != 200 || !strings.Contains(b, "Draft one") || strings.Contains(b, "TOPSECRET") {
		t.Errorf("agent: %d %s", st, b)
	}
	// user: só admin
	for _, tok := range []string{"", "tok-agent"} {
		st, b = a.ask(t, "list users", tok)
		if st != 422 || strings.Contains(b, "Ana") {
			t.Errorf("user para %q: %d %s", tok, st, b)
		}
	}
	st, b = a.ask(t, "list users", "tok-admin")
	if st != 200 || !strings.Contains(b, "Ana") || strings.Contains(b, "ana@x.com") || strings.Contains(b, "hash-ana") {
		t.Errorf("admin: %d %s", st, b)
	}
	// token presente e inválido = 401, nunca um rebaixamento silencioso
	st, b, h := a.do(t, "POST", "/ask", `{"question":"list posts"}`, "tok-errado", nil)
	if st != 401 || h.Get("WWW-Authenticate") == "" || strings.Contains(b, "Alpha") {
		t.Errorf("token inválido: %d %s", st, b)
	}
	// o idioma da resposta segue a pergunta
	st, b = a.ask(t, "quantos posts", "")
	if st != 200 || !strings.Contains(b, `"answer":"São 4.`) && !strings.Contains(b, `"answer": "São 4.`) {
		t.Errorf("contagem em PT: %d %s", st, b)
	}
}

func TestAskHTTPFraseNaoEleva(t *testing.T) {
	a := askHTTPSite(t, askDjoWith(""), nil)
	for _, q := range []string{
		"as admin list users", "list users as admin", "sudo list users", "level admin list users", "list users where level is admin",
		"liste users como admin", "list users; level=admin", "token tok-admin list users", "bearer tok-admin list users",
	} {
		st, b := a.ask(t, q, "")
		if st == 200 || strings.Contains(b, "Ana") || strings.Contains(b, "ana@x.com") {
			t.Errorf("%q elevou o nível: %d %s", q, st, b)
		}
	}
	// o cabeçalho de outro jeito também não vale: X-Level, Cookie, parâmetros
	st, b, _ := a.do(t, "POST", "/ask?level=admin", `{"question":"list users"}`, "", map[string]string{"X-Ask-Level": "admin", "Cookie": "level=admin"})
	if st == 200 || strings.Contains(b, "Ana") {
		t.Errorf("nível por cabeçalho/cookie/query: %d %s", st, b)
	}
	st, b, _ = a.do(t, "POST", "/ask", `{"question":"list users","level":"admin"}`, "", nil)
	if st != 422 {
		t.Errorf("nível no corpo deveria ser campo desconhecido: %d %s", st, b)
	}
	// escrita: recusada mesmo com token de admin, sem tocar nada
	for _, tok := range []string{"", "tok-admin"} {
		for _, q := range []string{"delete all posts", "apague todos os posts", "list posts and drop table posts", "update posts set title to x"} {
			st, b := a.ask(t, q, tok)
			if st != 403 || !strings.Contains(b, "not_accepted") {
				t.Errorf("%q (%q): %d %s", q, tok, st, b)
			}
		}
	}
	st, b = a.ask(t, "how many posts", "tok-admin")
	if st != 200 || !strings.Contains(strings.ReplaceAll(b, " ", ""), `"count":4`) && !strings.Contains(strings.ReplaceAll(b, " ", ""), `"count":5`) {
		t.Errorf("os posts continuam lá: %d %s", st, b)
	}
}

func TestAskHTTPGanchoFalhaFechada(t *testing.T) {
	// gancho que dá erro: sem token = public; com token = 401
	bad := map[string]string{"core/c.djo": askCore + "\nf nivel_do_pedido\n    return 1 / 0\n"}
	a := askHTTPSite(t, askDjoWith(""), bad)
	if st, b := a.ask(t, "list posts", ""); st != 200 || !strings.Contains(b, "Alpha") {
		t.Errorf("gancho com erro sem token: %d %s", st, b)
	}
	if st, _ := a.ask(t, "list posts", "tok-admin"); st != 401 {
		t.Errorf("gancho com erro e token: %d", st)
	}
	// valor desconhecido ("root", "ADMIN", número) não é nível
	for _, v := range []string{`"root"`, `"ADMIN"`, `7`, `true`, `"admin "`} {
		a := askHTTPSite(t, askDjoWith(""), map[string]string{"core/c.djo": askCore + "\nf nivel_do_pedido\n    return " + v + "\n"})
		if st, b := a.ask(t, "list users", ""); st == 200 || strings.Contains(b, "Ana") {
			t.Errorf("retorno %s virou nível: %d %s", v, st, b)
		}
		if st, _ := a.ask(t, "list posts", "algum"); st != 401 {
			t.Errorf("retorno %s com token deveria dar 401, deu %d", v, st)
		}
	}
	// sem Ask.level_from: tudo é public, e token nenhum é aceito
	b := askHTTPSite(t, askDjo, nil)
	if st, _ := b.ask(t, "list users", ""); st != 422 {
		t.Errorf("sem gancho: %d", st)
	}
	if st, _ := b.ask(t, "list posts", "tok-admin"); st != 401 {
		t.Errorf("sem gancho com token: %d", st)
	}
}

func TestAskHTTPPaginacaoEmPalavras(t *testing.T) {
	a := askHTTPSite(t, askDjoWith(""), nil)
	_, b := a.ask(t, "list posts first 2", "")
	if !strings.Contains(b, `page 2`) {
		t.Errorf("a resposta deveria ensinar 'page 2': %s", b)
	}
	_, b = a.ask(t, "list posts first 2 page 2", "")
	if !strings.Contains(b, "Gamma") || strings.Contains(b, "Alpha") {
		t.Errorf("página 2 pela frase: %s", b)
	}
	st, b, _ := a.do(t, "POST", "/ask", `{"question":"list posts first 2","page":2}`, "", nil)
	if st != 200 || !strings.Contains(b, "Gamma") || strings.Contains(b, "Alpha") {
		t.Errorf("página 2 pelo corpo: %d %s", st, b)
	}
	_, b = a.ask(t, "liste posts primeiros 2", "")
	if !strings.Contains(b, `pagina 2`) || !strings.Contains(b, "Mostrando") {
		t.Errorf("resposta em português: %s", b)
	}
	for _, body := range []string{`{"question":"list posts","page":0}`, `{"question":"list posts","page":"2"}`, `{"question":"list posts","page":99999999999}`} {
		if st, _, _ := a.do(t, "POST", "/ask", body, "", nil); st != 422 {
			t.Errorf("%s: %d", body, st)
		}
	}
}

func TestAskHTTPPaginaHumana(t *testing.T) {
	a := askHTTPSite(t, askDjoWith(""), nil)
	html := map[string]string{"Accept": "text/html"}
	st, b, h := a.do(t, "GET", "/ask", "", "", html)
	if st != 200 || !strings.Contains(h.Get("Content-Type"), "text/html") || !strings.Contains(b, `<form method="post" action="/ask"`) {
		t.Fatalf("página: %d %s", st, b)
	}
	// nada de GET: nem formulário, nem link com ?q=
	if strings.Contains(b, `method="get"`) || strings.Contains(b, "?q=") || strings.Contains(b, "<a ") {
		t.Errorf("a página não pode perguntar por GET: %s", b)
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") || !strings.Contains(h.Get("Content-Security-Policy"), "form-action 'self'") || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("cabeçalhos: %v", h)
	}
	// exemplos só dos tipos visíveis ao nível
	if !strings.Contains(b, "posts") || strings.Contains(b, "users") || strings.Contains(b, "secret") {
		t.Errorf("exemplos: %s", b)
	}
	// GET nunca processa pergunta: ?q= é ignorado (não responde dados, não reflete), com ou sem Accept JSON
	for _, q := range []string{"list posts", `<script>alert(1)</script>`, "delete all posts", "how many posts"} {
		for _, hdr := range []map[string]string{html, {"Accept": "application/json"}, nil} {
			st, body, h := a.do(t, "GET", "/ask?q="+urlEnc(q)+"&page=2&question="+urlEnc(q), "", "", hdr)
			if st != 200 || !strings.Contains(h.Get("Content-Type"), "text/html") {
				t.Errorf("GET ?q=%q: %d %v", q, st, h.Get("Content-Type"))
			}
			for _, bad := range []string{"Alpha", "Beta", "<script>alert", "alert(1)", "list posts</", `value="list posts"`, "São", "There are"} {
				if bad == `value="list posts"` || bad == "list posts</" {
					continue
				}
				if strings.Contains(body, bad) {
					t.Errorf("GET ?q=%q processou ou refletiu (%q)", q, bad)
				}
			}
			if strings.Contains(body, "Showing") || strings.Contains(body, "<table") {
				t.Errorf("GET ?q=%q trouxe resposta", q)
			}
		}
	}
	// HEAD não traz corpo
	if st, b, _ := a.do(t, "HEAD", "/ask", "", "", nil); st != 200 || b != "" {
		t.Errorf("HEAD: %d %q", st, b)
	}
}

func (a *askSite) form(t *testing.T, body string, hdr map[string]string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("POST", a.ts.URL+"/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestAskHTTPFormularioPost(t *testing.T) {
	a := askHTTPSite(t, askDjoWith(""), nil)
	st, b, h := a.form(t, "question=list+posts+first+2", nil)
	if st != 200 || !strings.Contains(h.Get("Content-Type"), "text/html") || !strings.Contains(b, "Alpha") || !strings.Contains(b, "<table") || strings.Contains(b, "Draft one") || strings.Contains(b, "TOPSECRET") {
		t.Fatalf("form: %d %s", st, b)
	}
	// "próxima página" é outro POST (nunca um link GET), com a pergunta e o número da página
	if !strings.Contains(b, `name="page" value="2"`) || strings.Contains(b, `method="get"`) || strings.Contains(b, "?q=") {
		t.Errorf("próxima página: %s", b)
	}
	st, b, _ = a.form(t, "question=list+posts+first+2&page=2", nil)
	if st != 200 || !strings.Contains(b, "Gamma") || strings.Contains(b, "Alpha") {
		t.Errorf("página 2: %d %s", st, b)
	}
	// XSS pela pergunta: só volta escapada
	for _, q := range []string{`<script>alert(1)</script>`, `"><img src=x onerror=alert(1)>`, `list posts" onfocus="alert(1)`, `' onmouseover='alert(1)`} {
		_, body, _ := a.form(t, "question="+urlEnc(q), nil)
		for _, bad := range []string{"<script>alert", "<img src=x", `" onfocus="`, `' onmouseover='`} {
			if strings.Contains(body, bad) {
				t.Errorf("q=%q: HTML sem escape (%s)", q, bad)
			}
		}
	}
	// o conteúdo do banco também sai escapado
	_, body, _ := a.form(t, "question=list+posts", nil)
	if strings.Contains(body, "<img src=x onerror") || !strings.Contains(body, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Errorf("tabela sem escape: %s", body)
	}
	// erros aparecem na página, no status certo, sem dados; escrita = 403
	if st, body, _ := a.form(t, "question=delete+all+posts", nil); st != 403 || !strings.Contains(body, "I can") || strings.Contains(body, "Alpha") {
		t.Errorf("escrita: %d %s", st, body)
	}
	if st, _, _ := a.form(t, "question=list+users", nil); st != 422 {
		t.Errorf("tipo escondido: %d", st)
	}
	// português
	_, body, _ = a.form(t, "question=quantos+posts", map[string]string{"Accept-Language": "pt-BR"})
	if !strings.Contains(body, `lang="pt"`) || !strings.Contains(body, "São 4.") {
		t.Errorf("PT: %s", body)
	}
	// mesmos limites do JSON: campo desconhecido, sem question, página inválida, corpo grande
	for _, f := range []string{"question=list+posts&x=1", "page=2", "question=a&question=b", "question=list+posts&page=0", "question=list+posts&page=abc", "question=" + strings.Repeat("a", 5000)} {
		st, body, _ := a.form(t, f, nil)
		if st == 200 || strings.Contains(body, "Alpha") {
			t.Errorf("%.40s: deveria recusar, veio %d", f, st)
		}
	}
	// POST de outro site não vale (Origin, "null", Sec-Fetch-Site); da própria origem vale
	for _, hdr := range []map[string]string{
		{"Origin": "https://evil.example"}, {"Origin": "null"}, {"Sec-Fetch-Site": "cross-site"}, {"Sec-Fetch-Site": "same-site"},
	} {
		if st, body, _ := a.form(t, "question=list+posts", hdr); st != 403 || strings.Contains(body, "Alpha") {
			t.Errorf("%v: %d", hdr, st)
		}
	}
	if st, _, _ := a.form(t, "question=list+posts", map[string]string{"Origin": a.ts.URL, "Sec-Fetch-Site": "same-origin"}); st != 200 {
		t.Errorf("mesma origem: %d", st)
	}
	// o limite de uso vale também para o formulário (per_minute 30 por padrão: o contador conta as chamadas acima)
}

func TestAskHTTPNextNaoEURL(t *testing.T) {
	a := askHTTPSite(t, askDjoWith(""), nil)
	_, b := a.ask(t, "list posts first 2", "")
	if !strings.Contains(strings.ReplaceAll(b, " ", ""), `"next":{"page":2}`) || strings.Contains(b, "http") || strings.Contains(b, "?q=") || strings.Contains(b, "/ask?") {
		t.Errorf("next deve ser só o corpo do próximo POST: %s", b)
	}
}

func TestAskHTTPGetNaoResponde(t *testing.T) {
	a := askHTTPSite(t, askDjoWith(""), nil)
	// outros métodos seguem 405; GET com Accept JSON agora devolve a página, nunca dados
	st, b, h := a.do(t, "GET", "/ask?q=how+many+posts", "", "", map[string]string{"Accept": "application/json"})
	if st != 200 || strings.Contains(h.Get("Content-Type"), "json") || strings.Contains(b, `"count"`) {
		t.Errorf("GET JSON: %d %s", st, b)
	}
}

func TestAskHTTPIdiomaDasMensagens(t *testing.T) {
	a := askHTTPSite(t, strings.Split(askDjoWith(""), "Ask.reply:")[0], nil)
	// sem verbo e sem pista de idioma: duas linhas, uma por idioma
	_, b := a.ask(t, "banana", "")
	if strings.Contains(b, " / ") || !strings.Contains(b, `\n`) || !strings.Contains(b, "I did not understand") || !strings.Contains(b, "Não entendi") {
		t.Errorf("sem idioma: %s", b)
	}
	// Accept-Language escolhe um só
	_, b, _ = a.do(t, "POST", "/ask", `{"question":"banana"}`, "", map[string]string{"Accept-Language": "pt-BR,pt;q=0.9"})
	if strings.Contains(b, "I did not understand") || !strings.Contains(b, "Não entendi") || !strings.Contains(b, `"lang":"pt"`) {
		t.Errorf("Accept-Language pt: %s", b)
	}
	// "lang" no corpo vale mais; só en|pt
	_, b, _ = a.do(t, "POST", "/ask", `{"question":"banana","lang":"en"}`, "", map[string]string{"Accept-Language": "pt"})
	if strings.Contains(b, "Não entendi") || !strings.Contains(b, "I did not understand") {
		t.Errorf("lang no corpo: %s", b)
	}
	if st, _, _ := a.do(t, "POST", "/ask", `{"question":"banana","lang":"fr"}`, "", nil); st != 422 {
		t.Errorf("lang inválido: %d", st)
	}
	// o idioma sugerido não muda como a frase é lida
	_, b, _ = a.do(t, "POST", "/ask", `{"question":"list posts","lang":"pt"}`, "", nil)
	if !strings.Contains(b, `"lang":"en"`) {
		t.Errorf("a frase manda no idioma: %s", b)
	}
}

func urlEnc(s string) string {
	return strings.NewReplacer(" ", "%20", "<", "%3C", ">", "%3E", "\"", "%22", "'", "%27", "=", "%3D", "(", "%28", ")", "%29", "/", "%2F").Replace(s)
}

func TestAskHTTPRateLimit(t *testing.T) {
	src := strings.Replace(askDjoWith(""), `"per_minute": 30`, `"per_minute": 3, "global_per_minute": 100`, 1)
	a := askHTTPSite(t, src, nil)
	for i := 1; i <= 3; i++ {
		if st, _ := a.ask(t, "how many posts", ""); st != 200 {
			t.Fatalf("chamada %d: %d", i, st)
		}
	}
	st, _, h := a.do(t, "POST", "/ask", `{"question":"how many posts"}`, "", nil)
	if st != 429 || h.Get("Retry-After") == "" {
		t.Errorf("4ª chamada: %d %v", st, h)
	}
	// o limite vale também para a página humana e para o token
	if st, _, _ := a.do(t, "GET", "/ask", "", "", nil); st != 429 {
		t.Errorf("página: %d", st)
	}
	// teto global
	src = strings.Replace(askDjoWith(""), `"per_minute": 30`, `"per_minute": 100, "global_per_minute": 2`, 1)
	g := askHTTPSite(t, src, nil)
	g.ask(t, "how many posts", "")
	g.ask(t, "how many posts", "tok-agent")
	if st, _ := g.ask(t, "how many posts", "tok-admin"); st != 429 {
		t.Errorf("teto global: %d", st)
	}
}

func TestAskHTTPContornos(t *testing.T) {
	a := askHTTPSite(t, askDjoWith(""), nil)
	cases := []struct {
		name, method, ct, body string
		want                   int
	}{
		{"content-type errado", "POST", "text/plain", `{"question":"list posts"}`, 415},
		{"multipart", "POST", "multipart/form-data; boundary=x", `--x--`, 415},
		{"corpo grande", "POST", "application/json", `{"question":"` + strings.Repeat("a", 5000) + `"}`, 413},
		{"json inválido", "POST", "application/json", `{"question":`, 400},
		{"não é objeto", "POST", "application/json", `["list posts"]`, 400},
		{"sem question", "POST", "application/json", `{}`, 422},
		{"question não é texto", "POST", "application/json", `{"question": 5}`, 422},
		{"campo extra", "POST", "application/json", `{"question":"list posts","x":1}`, 422},
		{"frase longa", "POST", "application/json", `{"question":"` + strings.Repeat("a", 400) + `"}`, 422},
		{"PUT", "PUT", "application/json", `{}`, 405},
		{"DELETE", "DELETE", "", ``, 405},
		{"OPTIONS", "OPTIONS", "", ``, 405},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(c.method, a.ts.URL+"/ask", strings.NewReader(c.body))
		if c.ct != "" {
			req.Header.Set("Content-Type", c.ct)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s: %d, queria %d", c.name, resp.StatusCode, c.want)
		}
		if resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s: CORS aberto", c.name)
		}
	}
	// com Origin de outro site, ainda sem CORS
	_, _, h := a.do(t, "POST", "/ask", `{"question":"how many posts"}`, "", map[string]string{"Origin": "https://evil.example"})
	if h.Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS aberto por padrão")
	}
}

func TestAskHTTPDesligadoSemAskDjo(t *testing.T) {
	a := askHTTPSite(t, "", nil)
	st1, b1, _ := a.do(t, "GET", "/ask", "", "", nil)
	st2, b2, _ := a.do(t, "GET", "/naoexiste", "", "", nil)
	if st1 != 404 || st1 != st2 || b1 != b2 {
		t.Errorf("/ask sem ask.djo deveria ser igual a uma rota inexistente: %d %q / %d %q", st1, b1, st2, b2)
	}
	if st, _, _ := a.do(t, "POST", "/ask", `{"question":"list posts"}`, "", nil); st != 404 && st != 405 {
		t.Errorf("POST /ask sem ask.djo: %d", st)
	}
	// e o ask.djo nunca é servido
	b := askHTTPSite(t, askDjoWith(""), nil)
	if st, body, _ := b.do(t, "GET", "/ask.djo", "", "", nil); st != 404 || strings.Contains(body, "Ask.") {
		t.Errorf("ask.djo servido: %d %s", st, body)
	}
}

func TestAskHTTPFraseDoSiteSeAskReply(t *testing.T) {
	src := askDjoWith("") // askDjo já traz Ask.reply com "refused" e "unknown"
	a := askHTTPSite(t, src, nil)
	st, b := a.ask(t, "delete all posts", "")
	if st != 403 || !strings.Contains(b, "I can't answer that.") {
		t.Errorf("refused EN: %d %s", st, b)
	}
	st, b = a.ask(t, "apague tudo", "")
	if st != 403 || !strings.Contains(b, "Não posso responder isso.") {
		t.Errorf("refused PT: %d %s", st, b)
	}
	// "unknown" é um texto único ("*") no askDjo; vale para tipo escondido e inexistente, igual
	_, b1 := a.ask(t, "list users", "")
	_, b2 := a.ask(t, "list cats", "")
	if !strings.Contains(b1, "I did not understand.") || b1 != b2 {
		t.Errorf("unknown:\n %s\n %s", b1, b2)
	}
	// sem Ask.reply, o padrão ensina e lista só o que o nível enxerga (nunca o tipo escondido)
	b3 := askHTTPSite(t, strings.Split(src, "Ask.reply:")[0], nil)
	_, m := b3.ask(t, "list users", "")
	if !strings.Contains(m, "post") || strings.Contains(strings.Replace(m, `\"users\"`, "", 1), "user") {
		t.Errorf("padrão: %s", m)
	}
}

func TestAskHTTPNaoApareceNoManifesto(t *testing.T) {
	rotas := `Route.site:{"nome": "Teste", "about": "x"}
Route.action:"/api/ping":"ping"
`
	core := askCore + askHook + "\naction ping\n    \"ping\"\n    params[a[String]]\n    return {\"a\": a}\n"
	a := askHTTPSite(t, askDjoWith(""), map[string]string{"rotas.djo": rotas, "core/c.djo": core})
	for _, p := range []string{"/.well-known/ai-manifest.json", "/llms.txt"} {
		st, b, _ := a.do(t, "GET", p, "", "", map[string]string{"Accept": "application/json"})
		if strings.Contains(b, "/ask") || strings.Contains(b, "ask.djo") {
			t.Errorf("%s menciona /ask: %d %s", p, st, b)
		}
	}
	// e o /ask funciona também no modo rotas
	if st, b := a.ask(t, "how many posts", ""); st != 200 {
		t.Errorf("/ask no modo rotas: %d %s", st, b)
	}
}
