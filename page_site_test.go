//go:build !noweb && !nodb

package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const corePaginas = `f abrir
    Db.open:"p.db"
    Db.exec:"CREATE TABLE IF NOT EXISTS itens (id INTEGER PRIMARY KEY, nome TEXT)"
    if{ cond[(Db.one:"SELECT COUNT(*) AS n FROM itens")["n"] == 0]
        Db.begin:
        for{ each[i in range:1:46]
            Db.exec:"INSERT INTO itens (nome) VALUES (?)":["item " + str:i]
        }
        Db.commit:
    }
`

func paginasSite(t *testing.T) func(p string) (int, string, http.Header) {
	ts := site(t, map[string]string{
		"core/c.djo": corePaginas,
		"rotas.djo": `Route.get:"/itens":{"agent": "p/itens.djo", "returns": ["Item"], "page": true, "about": "Lista"}
Route.get:"/total":{"agent": "p/total.djo"}
Route.get:"/lido":{"agent": "p/lido.djo"}
Route.get:"/busca":{"agent": "p/busca.djo"}
`,
		"core/t.djo": "struct Item\n    id[Int]\n    nome[String]\n",
		"p/itens.djo": `abrir:
var r = Db.page:"SELECT id, nome FROM itens ORDER BY id"
Page.headers:r
Http.json:r["itens"]`,
		"p/total.djo": `abrir:
var r = Db.page:"SELECT id FROM itens ORDER BY id":[]:{"limite": 10, "total": true}
Page.headers:r
Http.json:{"pagina": r["pagina"], "paginas": r["paginas"], "total": r["total"], "proxima": r["proxima"], "anterior": r["anterior"], "n": len:r["itens"]}`,
		"p/lido.djo": `Http.json:(Page.read:5:50)`,
		"p/busca.djo": `abrir:
var r = Db.page:"SELECT id FROM itens WHERE nome LIKE ? ORDER BY id DESC":["%1%"]:{"limite": 3, "total": true}
Http.json:{"ids": r["itens"], "total": r["total"]}`,
	})
	return func(p string) (int, string, http.Header) {
		req, _ := http.NewRequest("GET", ts.URL+p, nil)
		req.Header.Set("Accept", "application/json")
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header
	}
}

func TestPaginaDb(t *testing.T) {
	get := paginasSite(t)
	links := func(h http.Header) string { return strings.Join(h.Values("Link"), " | ") }

	// padrão: 20 por página, 45 itens
	c, b, h := get("/itens")
	if c != 200 || !strings.HasPrefix(b, `[{"id":1,"nome":"item 1"}`) || strings.Count(b, `"id"`) != 20 {
		t.Fatalf("página 1 = %d %.80s", c, b)
	}
	if l := links(h); !strings.Contains(l, `</itens?limite=20&pagina=2>; rel="next"`) || strings.Contains(l, `rel="prev"`) || !strings.Contains(l, "ai-manifest") {
		t.Errorf("Link da página 1: %q", l)
	}
	// última página (3): 5 itens, sem próxima, com anterior
	c, b, h = get("/itens?pagina=3")
	if c != 200 || strings.Count(b, `"id"`) != 5 || !strings.HasPrefix(b, `[{"id":41,`) {
		t.Fatalf("página 3 = %d %s", c, b)
	}
	if l := links(h); strings.Contains(l, `rel="next"`) || !strings.Contains(l, `</itens?limite=20&pagina=2>; rel="prev"`) {
		t.Errorf("Link da página 3: %q", l)
	}
	// limite do cliente, e o teto de 100
	if _, b, _ := get("/itens?limite=5&pagina=2"); strings.Count(b, `"id"`) != 5 || !strings.HasPrefix(b, `[{"id":6,`) {
		t.Errorf("limite=5: %s", b)
	}
	if _, b, _ := get("/itens?limite=100000"); strings.Count(b, `"id"`) != 45 {
		t.Errorf("limite acima do teto deve virar o teto (100): %d itens (45 existem)", strings.Count(b, `"id"`))
	}
	// valores ruins nunca dão erro: voltam para o padrão
	for _, q := range []string{"?pagina=abc", "?pagina=-3", "?pagina=0", "?limite=x", "?limite=0", "?limite=-1"} {
		if c, b, _ := get("/itens" + q); c != 200 || !strings.HasPrefix(b, `[{"id":1,`) {
			t.Errorf("%s = %d %.60s", q, c, b)
		}
	}
	// além do fim: lista vazia, sem erro
	if c, b, _ := get("/itens?pagina=99"); c != 200 || b != `[]` {
		t.Errorf("página 99 = %d %s", c, b)
	}
	// ?campos continua valendo com a lista paginada
	if _, b, _ := get("/itens?limite=2&campos=nome"); b != `[{"nome":"item 1"},{"nome":"item 2"}]` {
		t.Errorf("?campos = %s", b)
	}

	// total e páginas
	c, b, h = get("/total?pagina=5")
	if c != 200 || b != `{"pagina":5,"paginas":5,"total":45,"proxima":null,"anterior":4,"n":5}` || h.Get("X-Total-Count") != "45" {
		t.Errorf("total = %d %s %v", c, b, h.Get("X-Total-Count"))
	}
	// parâmetros do SQL e contagem coerente com o filtro: 1, 10..19, 21, 31, 41 = 14
	if _, b, _ := get("/busca"); b != `{"ids":[{"id":41},{"id":31},{"id":21}],"total":14}` {
		t.Errorf("busca = %s", b)
	}
	// Page.read
	if _, b, _ := get("/lido?limite=500&pagina=3"); b != `{"pagina":3,"limite":50,"deslocamento":100}` {
		t.Errorf("Page.read = %s", b)
	}
	if _, b, _ := get("/lido"); b != `{"pagina":1,"limite":5,"deslocamento":0}` {
		t.Errorf("Page.read padrão = %s", b)
	}

	// o manifesto anuncia a paginação
	_, m, _ := get("/.well-known/ai-manifest.json")
	if !strings.Contains(m, `"name": "pagina"`) || !strings.Contains(m, `"name": "limite"`) {
		t.Errorf("manifesto sem pagina/limite:\n%s", m)
	}
}

func TestDbPageErros(t *testing.T) {
	cases := map[string]string{
		`Db.page:"SELECT 1"`:                                         "precisa de ORDER BY",
		`Db.page:"SELECT * FROM t ORDER BY id LIMIT 5"`:              "acrescenta LIMIT e OFFSET",
		`Db.page:"SELECT * FROM t ORDER BY id OFFSET 5"`:             "acrescenta LIMIT e OFFSET",
		`Db.page:"DELETE FROM t"`:                                    "só lê",
		`Db.page:5`:                                                  "espera o SQL como String",
		`Db.page:"SELECT 1 ORDER BY 1":[]:5`:                         "mapa",
		`Db.page:"SELECT 1 ORDER BY 1":[]:{"x": 1}`:                  "não existe",
		`Db.page:"SELECT 1 ORDER BY 1":[]:{"limite": 0}`:             "de 1 a",
		`Db.page:"SELECT 1 ORDER BY 1":[]:{"limite": 50, "max": 10}`: "não pode passar",
		`Db.page:"SELECT 1 ORDER BY 1":[]:{"total": 1}`:              "Bool",
	}
	for src, want := range cases {
		if _, err := run(t, src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: erro %v, queria %q", src, err, want)
		}
	}
}
