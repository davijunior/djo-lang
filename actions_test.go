//go:build !noweb

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const coreAcoes = `struct Autor
    nome[String]
    idade[Int] = 18

action criar_post
    "Cria um post novo"
    params[titulo[String], nota[Float], publico[Bool], autor[Autor]]
    if{ cond[len:titulo == 0]
        Http.status:400
        return {"erro": "titulo vazio"}
    }
    Http.status:201
    return {"titulo": titulo, "nota": nota, "publico": publico, "autor": autor.nome, "idade": autor.idade}

action publicar
    "Publica um post"
    params[id[Int]]
    return {"id": id, "ok": true}

action limpar
    params[]
    print "isto nao vai para o corpo"

f dobro
    params[n[Int]]
    return n * 2
`

const rotasAcoes = `Route.get:"/posts":{"html": "p/posts.djo", "agent": "p/api.djo", "about": "Lista os posts"}
Route.get:"/sobre":"p/sobre.djo"
Route.action:"/posts":"criar_post"
Route.action:"/posts/{id}/publicar":"publicar"
Route.action:"/limpar":"limpar"
`

func acoesSite(t *testing.T) func(method, path, ctype, body string) (int, string, http.Header) {
	ts := site(t, map[string]string{
		"core/m.djo":  coreAcoes,
		"rotas.djo":   rotasAcoes,
		"p/posts.djo": `print "tela"`,
		"p/api.djo":   `print "{}"`,
		"p/sobre.djo": `print "sobre"`,
	})
	return func(method, p, ctype, body string) (int, string, http.Header) {
		req, _ := http.NewRequest(method, ts.URL+p, strings.NewReader(body))
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header
	}
}

func TestAcaoChamada(t *testing.T) {
	do := acoesSite(t)
	code, body, h := do("POST", "/posts", "application/json", `{"titulo":"Oi","nota":4,"publico":true,"autor":{"nome":"Ana"}}`)
	if code != 201 || body != `{"titulo":"Oi","nota":4,"publico":true,"autor":"Ana","idade":18}` {
		t.Errorf("sucesso = %d %s", code, body)
	}
	if !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		t.Errorf("content-type %q", h.Get("Content-Type"))
	}
	// o parâmetro vem da URL e é convertido para Int
	if code, body, _ := do("POST", "/posts/42/publicar", "", ""); code != 200 || body != `{"id":42,"ok":true}` {
		t.Errorf("path = %d %s", code, body)
	}
	if code, body, _ := do("POST", "/posts/abc/publicar", "", ""); code != 422 || !strings.Contains(body, `"id":"na URL deve ser Int"`) {
		t.Errorf("path inválido = %d %s", code, body)
	}
	// o corpo não pode repetir o que já está na URL
	if code, _, _ := do("POST", "/posts/1/publicar", "application/json", `{"id": 2}`); code != 422 {
		t.Errorf("id duplicado = %d", code)
	}
	// sem retorno: {"ok":true}; o que a action imprime não vaza
	if code, body, _ := do("POST", "/limpar", "", ""); code != 200 || body != `{"ok":true}` {
		t.Errorf("sem retorno = %d %s", code, body)
	}
}

func TestAcaoValidaEntrada(t *testing.T) {
	do := acoesSite(t)
	type c struct {
		ctype, body, want string
		code              int
	}
	for _, k := range []c{
		{"application/json", `{}`, `"titulo":"obrigatório"`, 422},
		{"application/json", `{"titulo":1,"nota":1,"publico":true,"autor":{"nome":"a"}}`, `"titulo":"deve ser String, mas veio Int"`, 422},
		{"application/json", `{"titulo":"a","nota":"x","publico":true,"autor":{"nome":"a"}}`, `"nota":"deve ser Float`, 422},
		{"application/json", `{"titulo":"a","nota":1,"publico":"sim","autor":{"nome":"a"}}`, `"publico":"deve ser Bool`, 422},
		{"application/json", `{"titulo":"a","nota":1,"publico":true,"autor":{}}`, `"autor.nome":"obrigatório"`, 422},
		{"application/json", `{"titulo":"a","nota":1,"publico":true,"autor":{"nome":"a","x":1}}`, `"autor.x":"campo desconhecido"`, 422},
		{"application/json", `{"titulo":"a","nota":1,"publico":true,"autor":{"nome":"a"},"extra":1}`, `"extra":"campo desconhecido"`, 422},
		{"application/json", `{"titulo":"a","nota":1.5,"publico":true,"autor":{"nome":"a","idade":1.5}}`, `"autor.idade":"deve ser Int`, 422},
		{"application/json", `[1]`, "objeto JSON", 400},
		{"application/json", `{nao json`, "objeto JSON", 400},
		{"application/x-www-form-urlencoded", `titulo=a`, "application/json", 415},
		{"text/plain", `x`, "application/json", 415},
	} {
		code, body, _ := do("POST", "/posts", k.ctype, k.body)
		if code != k.code || !strings.Contains(body, k.want) {
			t.Errorf("%s %s = %d %s, queria %d com %s", k.ctype, k.body, code, body, k.code, k.want)
		}
	}
	// a action vira erro da própria action (400) sem passar por 422
	code, body, _ := do("POST", "/posts", "application/json", `{"titulo":"","nota":1,"publico":true,"autor":{"nome":"a"}}`)
	if code != 400 || !strings.Contains(body, "titulo vazio") {
		t.Errorf("erro da action = %d %s", code, body)
	}
	// só POST
	if code, _, h := do("GET", "/limpar", "", ""); code != 405 || h.Get("Allow") != "POST" {
		t.Errorf("GET em action = %d %q", code, h.Get("Allow"))
	}
}

func TestManifesto(t *testing.T) {
	do := acoesSite(t)
	code, body, h := do("GET", manifestPath, "", "")
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		t.Fatalf("manifesto = %d %s", code, h.Get("Content-Type"))
	}
	var m struct {
		Format string
		Routes []struct {
			Method, Path, Description string
		}
		Actions []struct {
			Name, Method, Path, Description string
			Input                           struct {
				Properties map[string]map[string]any
				Required   []string
			}
			PathParams []struct{ Name, In, Type string } `json:"path_params"`
		}
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err, body)
	}
	if m.Format != "djo-manifest/1" {
		t.Errorf("format %q", m.Format)
	}
	// só rotas com face de agente; /sobre (só tela) não aparece
	if len(m.Routes) != 1 || m.Routes[0].Path != "/posts" || m.Routes[0].Description != "Lista os posts" {
		t.Errorf("rotas %+v", m.Routes)
	}
	if len(m.Actions) != 3 {
		t.Fatalf("ações %+v", m.Actions)
	}
	cp := m.Actions[0]
	if cp.Name != "criar_post" || cp.Method != "POST" || cp.Description != "Cria um post novo" {
		t.Errorf("criar_post %+v", cp)
	}
	if cp.Input.Properties["nota"]["type"] != "number" || cp.Input.Properties["publico"]["type"] != "boolean" || cp.Input.Properties["titulo"]["type"] != "string" {
		t.Errorf("schema %+v", cp.Input.Properties)
	}
	aut := cp.Input.Properties["autor"]
	if aut["type"] != "object" || aut["additionalProperties"] != false {
		t.Errorf("autor %+v", aut)
	}
	props := aut["properties"].(map[string]any)
	if props["idade"].(map[string]any)["type"] != "integer" {
		t.Errorf("props %+v", props)
	}
	if req := aut["required"].([]any); len(req) != 1 || req[0] != "nome" { // idade tem padrão
		t.Errorf("required %+v", req)
	}
	pub := m.Actions[1]
	if pub.Name != "publicar" || len(pub.Input.Required) != 0 || len(pub.PathParams) != 1 || pub.PathParams[0].Name != "id" || pub.PathParams[0].Type != "integer" {
		t.Errorf("publicar %+v", pub)
	}
	// descoberta
	if _, _, h := do("GET", "/sobre", "", ""); h.Get("Link") != `</.well-known/ai-manifest.json>; rel="ai-manifest"` {
		t.Errorf("Link = %q", h.Get("Link"))
	}
}

func TestSemManifestoSemAgentes(t *testing.T) {
	ts := site(t, map[string]string{"rotas.djo": `Route.get:"/":"p.djo"`, "p.djo": `print "x"`})
	code, _, h := get(t, ts, manifestPath)
	if code != 404 || h.Get("Link") != "" {
		t.Errorf("manifesto sem agentes = %d Link=%q", code, h.Get("Link"))
	}
}

func TestAcaoErrosDeDeclaracao(t *testing.T) {
	core := "action sem_tipo\n    params[x]\n    return x\n\naction boa\n    params[x[Int]]\n    return x\n\nf comum\n    params[x[Int]]\n    return x\n"
	cases := map[string]string{
		`Route.action:"/a":"sem_tipo"`:   "precisa de um tipo",
		`Route.action:"/a":"comum"`:      "não é uma action",
		`Route.action:"/a":"nao_existe"`: "não é uma action",
		`Route.action:"/a/{y}":"boa"`:    "não é parâmetro",
		`Route.action:"/a/{...y}":"boa"`: "não pode usar",
	}
	for src, want := range cases {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "core"), 0o755)
		os.WriteFile(filepath.Join(root, "core", "c.djo"), []byte(core), 0o644)
		os.WriteFile(filepath.Join(root, "rotas.djo"), []byte(src), 0o644)
		if _, err := NewServer(root, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: erro %v, queria %q", src, err, want)
		}
	}
}

func TestActionTambemEFuncao(t *testing.T) {
	out, err := runIn(t, t.TempDir(), "action dobrar\n    \"Dobra\"\n    params[n[Int]]\n    return n * 2\nprint dobrar:4\n", false)
	if err != nil || strings.TrimSpace(out) != "8" {
		t.Errorf("out=%q err=%v", out, err)
	}
}

const coreAcoesMetodos = `action trocar_post
    "Troca um post"
    params[id[Int], titulo[String]]
    return {"id": id, "titulo": titulo, "metodo": Http.method:}

action mudar_post
    params[id[Int], titulo[String]]
    return {"id": id, "titulo": titulo}

action apagar_post
    "Apaga um post"
    params[id[Int]]
    Http.status:200
    return {"apagado": id}
`

func TestAcoesComOutrosMetodos(t *testing.T) {
	ts := site(t, map[string]string{
		"core/m.djo": coreAcoesMetodos,
		"rotas.djo": `Route.put_action:"/posts/{id}":"trocar_post"
Route.patch_action:"/posts/{id}":"mudar_post"
Route.delete_action:"/posts/{id}":"apagar_post"
Route.get:"/posts/{id}":{"agent": "p/ver.djo"}
`,
		"p/ver.djo": `Http.json:{"id": param:"id"}`,
	})
	do := func(method, p, ctype, body string) (int, string, http.Header) {
		req, _ := http.NewRequest(method, ts.URL+p, strings.NewReader(body))
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header
	}
	if c, b, _ := do("PUT", "/posts/3", "application/json", `{"titulo":"Novo"}`); c != 200 || b != `{"id":3,"titulo":"Novo","metodo":"PUT"}` {
		t.Errorf("PUT = %d %s", c, b)
	}
	if c, b, _ := do("PATCH", "/posts/3", "application/json", `{"titulo":"Parcial"}`); c != 200 || b != `{"id":3,"titulo":"Parcial"}` {
		t.Errorf("PATCH = %d %s", c, b)
	}
	// DELETE sem corpo: só o que vem na URL
	if c, b, _ := do("DELETE", "/posts/9", "", ""); c != 200 || b != `{"apagado":9}` {
		t.Errorf("DELETE = %d %s", c, b)
	}
	// as mesmas regras da action POST: tipos conferidos, corpo precisa ser JSON
	if c, b, _ := do("PUT", "/posts/3", "application/json", `{"titulo": 5}`); c != 422 || !strings.Contains(b, `"titulo":"deve ser String, mas veio Int"`) {
		t.Errorf("PUT inválido = %d %s", c, b)
	}
	if c, _, _ := do("PUT", "/posts/3", "application/x-www-form-urlencoded", "titulo=x"); c != 415 {
		t.Errorf("PUT de formulário = %d, queria 415", c)
	}
	if c, _, _ := do("DELETE", "/posts/abc", "", ""); c != 422 {
		t.Errorf("DELETE com id inválido = %d, queria 422", c)
	}
	// o método errado dá 405 com o Allow certo
	c, _, h := do("POST", "/posts/3", "application/json", `{}`)
	if c != 405 || h.Get("Allow") != "DELETE, GET, PATCH, PUT" {
		t.Errorf("POST = %d Allow=%q", c, h.Get("Allow"))
	}

	// o manifesto traz cada ação com o seu método
	_, body, _ := do("GET", "/.well-known/ai-manifest.json", "", "")
	var m struct {
		Actions []struct{ Name, Method, Path string } `json:"actions"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range m.Actions {
		got[a.Name] = a.Method + " " + a.Path
	}
	for name, want := range map[string]string{
		"trocar_post": "PUT /posts/{id}", "mudar_post": "PATCH /posts/{id}", "apagar_post": "DELETE /posts/{id}",
	} {
		if got[name] != want {
			t.Errorf("manifesto: %s = %q, queria %q", name, got[name], want)
		}
	}
}

func TestAcoesOutrosMetodosErros(t *testing.T) {
	core := "action boa\n    params[x[Int]]\n    return x\n\nf comum\n    params[x[Int]]\n    return x\n"
	cases := map[string]string{
		`Route.put_action:"/a/{x}":"comum"`:                                        "Route.put_action: 'comum' não é uma action",
		`Route.delete_action:"/a/{y}":"boa"`:                                       "Route.delete_action: {y} na rota não é parâmetro",
		`Route.patch_action:"/a":1`:                                                "Route.patch_action espera",
		"Route.put_action:\"/a/{x}\":\"boa\"\nRoute.put_action:\"/a/{x}\":\"boa\"": "já foi declarada",
	}
	for src, want := range cases {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "core"), 0o755)
		os.WriteFile(filepath.Join(root, "core", "c.djo"), []byte(core), 0o644)
		os.WriteFile(filepath.Join(root, "rotas.djo"), []byte(src), 0o644)
		if _, err := NewServer(root, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: erro %v, queria %q", src, err, want)
		}
	}
}
