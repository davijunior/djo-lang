//go:build !noweb

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHomeDeAgente(t *testing.T) {
	_, ts := laneServer(t, `Route.site:{"nome": "Loja", "about": "Vende coisas"}
Route.get:"/":"p/tela.djo"
Route.get:"/lista":{"html": "p/tela.djo", "agent": "p/lista.djo", "about": "Lista"}
`)
	a := map[string]string{"Accept": J}
	code, body, h := req(t, ts, "/", a)
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "application/json") || h.Get("Vary") != "Accept" || h.Get("ETag") == "" {
		t.Fatalf("home: %d %q %v", code, body, h)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	if d["saudacao"] != "Olá! Este é o site Loja. Vende coisas" || d["manifesto"] != manifestPath {
		t.Fatalf("saudação: %+v", d)
	}
	rotas := d["rotas"].([]any)
	if len(rotas) != 1 || rotas[0].(map[string]any)["path"] != "/lista" || rotas[0].(map[string]any)["description"] != "Lista" {
		t.Fatalf("rotas: %+v", rotas)
	}
	// o manifesto leva o site
	_, mb, _ := req(t, ts, manifestPath, a)
	if !strings.Contains(mb, `"nome": "Loja"`) {
		t.Fatalf("manifesto sem site: %s", mb)
	}
	// 304 com ETag
	if code, _, _ := req(t, ts, "/", map[string]string{"Accept": J, "If-None-Match": h.Get("ETag")}); code != 304 {
		t.Fatalf("esperava 304, veio %d", code)
	}
	// humano continua vendo a tela; Accept vazio também
	if code, body, _ := req(t, ts, "/", nil); code != 200 || !strings.HasPrefix(body, "tela") {
		t.Fatalf("humano: %d %q", code, body)
	}
}

func TestHomeSemSite(t *testing.T) {
	_, ts := laneServer(t, `Route.get:"/lista":{"html": "p/tela.djo", "agent": "p/lista.djo"}`)
	code, body, _ := req(t, ts, "/", map[string]string{"Accept": J})
	if code != 200 || !strings.Contains(body, "Olá! Este site atende agentes") {
		t.Fatalf("home genérica: %d %q", code, body)
	}
}

func TestHomeDoAutorVence(t *testing.T) {
	_, ts := laneServer(t, `Route.get:"/":{"html": "p/tela.djo", "agent": "p/lista.djo"}`)
	code, body, _ := req(t, ts, "/", map[string]string{"Accept": J})
	if code != 200 || !strings.Contains(body, `"v"`) || strings.Contains(body, "saudacao") {
		t.Fatalf("a face do autor deveria valer: %d %q", code, body)
	}
}

func TestHomeSemAgenteNaoExiste(t *testing.T) {
	// site sem nada para agentes: não há manifesto nem home
	_, ts := laneServer(t, `Route.get:"/":"p/tela.djo"`)
	if code, _, _ := req(t, ts, "/", map[string]string{"Accept": J}); code != 406 {
		t.Fatalf("esperava 406, veio %d", code)
	}
}

func TestRouteSiteInvalido(t *testing.T) {
	for _, r := range []string{
		`Route.site:"oi"`,
		`Route.site:{"titulo": "x"}`,
		`Route.site:{"nome": 3}`,
		`Route.site:{}`,
		"Route.site:{\"nome\": \"a\"}\nRoute.site:{\"nome\": \"b\"}",
	} {
		root := t.TempDir()
		writeFile(t, root, "rotas.djo", r)
		if _, err := NewServer(root, false); err == nil {
			t.Errorf("deveria recusar: %s", r)
		}
	}
}
