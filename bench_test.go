//go:build !noweb

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// benchSite monta um site parecido com o exemplos/rotas: core com struct e funções,
// uma tela (template) e uma face de agente (JSON).
func benchSite(b *testing.B) *Server { return benchSiteWith(b, "") }

func benchSiteWith(b *testing.B, extra string) *Server {
	b.Helper()
	root := b.TempDir()
	files := map[string]string{
		"core/posts.djo": `struct Post
    id[Int]
    titulo[String]
    texto[String]

struct Destaque sonof Post
    cor[String] = "azul"

f todos
    var lista = []
    var i = 1
    while{ cond[i <= 20]
        push:lista:(Post:i:"Titulo":"Texto do post")
        i++
    }
    return lista
`,
		"rotas.djo": `Route.get:"/posts":{"html": "p/posts.djo.html", "agent": "p/posts.djo"` + extra + `}`,
		"p/posts.djo.html": `<h1>Posts</h1>
{% var lista = todos: %}
{% for{ each[p in lista] %}<p><a href="/posts/{{ p.id }}">{{ p.titulo }}</a> {{ p.texto }}</p>
{% } %}`,
		"p/posts.djo": `var lista = todos:
var itens = []
for{ each[p in lista]
    push:itens:{"id": p.id, "titulo": p.titulo}
}
Http.json:{"posts": itens}`,
	}
	for name, content := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	srv, err := NewServer(root, false)
	if err != nil {
		b.Fatal(err)
	}
	srv.lane = newLane(1024, 0)
	return srv
}

func benchGet(b *testing.B, srv *Server, accept string) {
	req := httptest.NewRequest("GET", "/posts", nil)
	req.Header.Set("Accept", accept)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		b.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r := httptest.NewRequest("GET", "/posts", nil)
			r.Header.Set("Accept", accept)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
		}
	})
}

func BenchmarkTela(b *testing.B)   { benchGet(b, benchSite(b), "text/html") }
func BenchmarkAgente(b *testing.B) { benchGet(b, benchSite(b), "application/json") }

// com "cache": 30 a página roda uma vez e o resto vem da memória
func BenchmarkAgenteComCache(b *testing.B) {
	benchGet(b, benchSiteWith(b, `, "cache": 30`), "application/json")
}
