//go:build !noweb

package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

const coreCampos = `struct Autor
    nome[String]
    pais[String] = "BR"

struct Post
    id[Int] nocall
    titulo[String]
    texto[String]
    autor[Autor]

f todos
    var a = Autor:"Ana"
    var p1 = Post:"Ola":"primeiro":a
    p1.id = 1
    var p2 = Post:"Rotas":"segundo":a
    p2.id = 2
    return [p1, p2]

action comentar
    "Comenta"
    params[id[Int], texto[String]]
    return {"id": id, "texto": texto}
`

const rotasCampos = `Route.get:"/posts":{"html": "p/tela.djo", "agent": "p/lista.djo", "returns": ["Post"], "cache": 30}
Route.get:"/posts/primeiro":{"html": "p/tela.djo", "agent": "p/um.djo", "returns": "Post"}
Route.get:"/semtipo":{"html": "p/tela.djo", "agent": "p/lista.djo"}
Route.get:"/torto":{"html": "p/tela.djo", "agent": "p/torto.djo", "returns": "Post"}
Route.action:"/posts/{id}/comentarios":"comentar":"Comentario"
`

func camposServer(t *testing.T, dev bool) *httptest.Server {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "core/m.djo", coreCampos+"\nstruct Comentario\n    id[Int]\n    texto[String]\n")
	writeFile(t, root, "rotas.djo", rotasCampos)
	writeFile(t, root, "p/tela.djo", `print "tela"`)
	writeFile(t, root, "p/lista.djo", "var l = todos:\nHttp.json:l")
	writeFile(t, root, "p/um.djo", "var l = todos:\nHttp.json:l[0]")
	writeFile(t, root, "p/torto.djo", `Http.json:{"id": "nao e int", "inventado": 1}`)
	srv, err := NewServer(root, dev)
	if err != nil {
		t.Fatal(err)
	}
	srv.lane = newLane(64, 0)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func TestCamposProjeta(t *testing.T) {
	ts := camposServer(t, false)
	a := map[string]string{"Accept": J}
	type c struct {
		path, want string
		code       int
	}
	for _, k := range []c{
		{"/posts?campos=id,titulo", `[{"id":1,"titulo":"Ola"},{"id":2,"titulo":"Rotas"}]`, 200},
		{"/posts?campos=titulo", `[{"titulo":"Ola"},{"titulo":"Rotas"}]`, 200},
		{"/posts?campos=autor.nome", `[{"autor":{"nome":"Ana"}},{"autor":{"nome":"Ana"}}]`, 200},
		{"/posts?campos=id,autor", `[{"id":1,"autor":{"nome":"Ana","pais":"BR"}},{"id":2,"autor":{"nome":"Ana","pais":"BR"}}]`, 200},
		{"/posts?campos=autor.nome,autor", `[{"autor":{"nome":"Ana","pais":"BR"}},{"autor":{"nome":"Ana","pais":"BR"}}]`, 200},
		{"/posts?campos=autor,autor.nome", `[{"autor":{"nome":"Ana","pais":"BR"}},{"autor":{"nome":"Ana","pais":"BR"}}]`, 200},
		{"/posts?campos=%20id%20,%20titulo%20", `[{"id":1,"titulo":"Ola"},{"id":2,"titulo":"Rotas"}]`, 200},
		{"/posts/primeiro?campos=titulo", `{"titulo":"Ola"}`, 200},
	} {
		code, body, _ := req(t, ts, k.path, a)
		if code != k.code || strings.TrimSpace(body) != k.want {
			t.Errorf("%s = %d %s\n  queria %s", k.path, code, body, k.want)
		}
	}
	// sem ?campos= vem tudo
	_, all, _ := req(t, ts, "/posts/primeiro", a)
	if !strings.Contains(all, `"texto":"primeiro"`) || !strings.Contains(all, `"pais":"BR"`) {
		t.Errorf("sem campos: %s", all)
	}
}

func TestCamposInvalidos(t *testing.T) {
	ts := camposServer(t, false)
	a := map[string]string{"Accept": J}
	for path, want := range map[string]string{
		"/posts?campos=nada":         "campo desconhecido 'nada'",
		"/posts?campos=id,autor.xx":  "Campos válidos: autor, autor.nome, autor.pais, id, texto, titulo",
		"/posts?campos=":             "está vazio",
		"/posts?campos=,":            "está vazio",
		"/semtipo?campos=id":         `não declara \"returns\"`,
		"/posts?campos=autor.nome.x": "campo desconhecido",
	} {
		code, body, _ := req(t, ts, path, a)
		if code != 400 || !strings.Contains(body, want) {
			t.Errorf("%s = %d %s, queria 400 com %q", path, code, body, want)
		}
	}
	// na tela o ?campos= é ignorado
	if code, body, _ := req(t, ts, "/posts?campos=nada", map[string]string{"Accept": "text/html"}); code != 200 || body != "tela\n" {
		t.Errorf("tela: %d %q", code, body)
	}
}

func TestCamposECache(t *testing.T) {
	ts := camposServer(t, false)
	a := map[string]string{"Accept": J}
	_, x, h1 := req(t, ts, "/posts?campos=id", a)
	_, y, h2 := req(t, ts, "/posts?campos=titulo", a)
	_, z, h3 := req(t, ts, "/posts?campos=id", a)
	if x == y || x != z || h1.Get("ETag") == h2.Get("ETag") || h1.Get("ETag") != h3.Get("ETag") {
		t.Errorf("a query tem que fazer parte do cache e do ETag: %q %q %q", x, y, z)
	}
	if code, _, _ := req(t, ts, "/posts?campos=id", map[string]string{"Accept": J, "If-None-Match": h1.Get("ETag")}); code != 304 {
		t.Errorf("304 com campos: %d", code)
	}
}

func TestDevConfereReturns(t *testing.T) {
	a := map[string]string{"Accept": J}
	dev := camposServer(t, true)
	code, body, _ := req(t, dev, "/torto", a)
	if code != 500 || !strings.Contains(body, "não bate com o") || !strings.Contains(body, "resposta.id deveria ser Int") || !strings.Contains(body, "inventado não é um campo de Post") {
		t.Errorf("-dev deveria acusar: %d %s", code, body)
	}
	if code, _, _ := req(t, dev, "/posts", a); code != 200 {
		t.Errorf("-dev em resposta certa: %d", code)
	}
	// sem -dev não paga o custo da conferência
	prod := camposServer(t, false)
	if code, _, _ := req(t, prod, "/torto", a); code != 200 {
		t.Errorf("produção não confere: %d", code)
	}
}

func TestManifestoTemReturns(t *testing.T) {
	ts := camposServer(t, false)
	_, body, _ := req(t, ts, manifestPath, nil)
	var m struct {
		Routes []struct {
			Path    string
			Returns map[string]any
			Query   []map[string]any
		}
		Actions []struct {
			Name    string
			Returns map[string]any
		}
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err, body)
	}
	var posts, semtipo *struct {
		Path    string
		Returns map[string]any
		Query   []map[string]any
	}
	for i := range m.Routes {
		switch m.Routes[i].Path {
		case "/posts":
			posts = &m.Routes[i]
		case "/semtipo":
			semtipo = &m.Routes[i]
		}
	}
	if posts == nil || posts.Returns["type"] != "array" {
		t.Fatalf("returns de /posts: %+v", posts)
	}
	items := posts.Returns["items"].(map[string]any)
	props := items["properties"].(map[string]any)
	if items["title"] != "Post" || props["id"].(map[string]any)["type"] != "integer" {
		t.Errorf("items %+v", items)
	}
	autor := props["autor"].(map[string]any)
	if autor["title"] != "Autor" || autor["properties"].(map[string]any)["nome"].(map[string]any)["type"] != "string" {
		t.Errorf("autor %+v", autor)
	}
	if len(posts.Query) != 2 || posts.Query[0]["name"] != "campos" || posts.Query[1]["name"] != "tabela" {
		t.Errorf("query %+v", posts.Query)
	}
	if semtipo.Returns != nil || semtipo.Query != nil {
		t.Errorf("rota sem returns não anuncia campos: %+v", semtipo)
	}
	if len(m.Actions) != 1 || m.Actions[0].Returns["title"] != "Comentario" {
		t.Errorf("action: %+v", m.Actions)
	}
}

func TestReturnsErrosDeDeclaracao(t *testing.T) {
	for src, want := range map[string]string{
		`Route.get:"/a":{"agent": "p/tela.djo", "returns": "Fantasma"}`: "não existe",
		`Route.get:"/a":{"agent": "p/tela.djo", "returns": 5}`:          "nome de um tipo",
		`Route.get:"/a":{"agent": "p/tela.djo", "returns": ["A", "B"]}`: "nome de um tipo",
		`Route.get:"/a":{"agent": "p/tela.djo", "returns": "Any"}`:      "não existe",
		`Route.action:"/a":"comentar":"Fantasma"`:                       "não existe",
		`Route.action:"/a/{id}":"comentar":["Comentario"]:1`:            "espera",
	} {
		root := t.TempDir()
		writeFile(t, root, "core/m.djo", coreCampos)
		writeFile(t, root, "p/tela.djo", `print "x"`)
		writeFile(t, root, "rotas.djo", src)
		if _, err := NewServer(root, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestTabela(t *testing.T) {
	ts := camposServer(t, false)
	a := map[string]string{"Accept": J}
	code, body, h := req(t, ts, "/posts?tabela", a)
	want := "id,titulo,texto,autor.nome,autor.pais\r\n1,Ola,primeiro,Ana,BR\r\n2,Rotas,segundo,Ana,BR\r\n"
	if code != 200 || body != want || !strings.HasPrefix(h.Get("Content-Type"), "text/csv") {
		t.Fatalf("tabela: %d %q %q", code, body, h.Get("Content-Type"))
	}
	_, body, _ = req(t, ts, "/posts?tabela&campos=id,autor.nome", a)
	if body != "id,autor.nome\r\n1,Ana\r\n2,Ana\r\n" {
		t.Fatalf("tabela com campos: %q", body)
	}
	// objeto único e rota sem lista não têm tabela
	for _, p := range []string{"/posts/primeiro?tabela", "/semtipo?tabela"} {
		if code, _, _ := req(t, ts, p, a); code != 400 {
			t.Fatalf("%s deveria dar 400, deu %d", p, code)
		}
	}
	// a face humana ignora ?tabela
	if code, body, _ := req(t, ts, "/posts?tabela", nil); code != 200 || strings.TrimSpace(body) != "tela" {
		t.Fatalf("humano: %d %q", code, body)
	}
}

func TestCsvAspas(t *testing.T) {
	for in, want := range map[string]string{"a": "a", "a,b": `"a,b"`, `diz "oi"`: `"diz ""oi"""`, "x\ny": "\"x\ny\""} {
		if got := csvQuote(in); got != want {
			t.Errorf("csvQuote(%q) = %q, quer %q", in, got, want)
		}
	}
}
