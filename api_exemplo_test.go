//go:build !noweb && !nodb

package main

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exemploSite lê uma pasta de exemplos/ para o formato que o helper site() espera.
func exemploSite(t *testing.T, dir string) map[string]string {
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestExemploAPI(t *testing.T) {
	files := exemploSite(t, "exemplos/api")
	// só no teste: uma rota que grava o token "segredo" (no exemplo de verdade, é o criar_token.djo)
	files["rotas.djo"] += "Route.post:\"/_token\":\"p/token.djo\"\n"
	files["p/token.djo"] = "abrir:\nDb.exec:\"INSERT OR IGNORE INTO tokens (hash) VALUES (?)\":[Hash.sha256:\"segredo\"]\nprint \"ok\""
	ts := site(t, files)

	do := func(method, p, body, token string) (int, string, http.Header) {
		req, _ := http.NewRequest(method, ts.URL+p, strings.NewReader(body))
		if p != "/_token" {
			req.Header.Set("Accept", "application/json")
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header
	}
	if c, _, _ := do("POST", "/_token", "", ""); c != 200 {
		t.Fatalf("preparar o token: %d", c)
	}
	const tk = "segredo"

	// sem token, ou com token errado: 401 em tudo
	for _, c := range []struct{ m, p, b string }{
		{"GET", "/tarefas", ""}, {"GET", "/tarefas/1", ""}, {"POST", "/tarefas", `{"titulo":"x"}`},
		{"PUT", "/tarefas/1", `{"titulo":"x","feita":false}`}, {"PATCH", "/tarefas/1/feita", `{"feita":true}`}, {"DELETE", "/tarefas/1", ""},
	} {
		for _, tok := range []string{"", "errado"} {
			code, _, h := do(c.m, c.p, c.b, tok)
			if code != 401 || h.Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("%s %s com token %q = %d, queria 401", c.m, c.p, tok, code)
			}
		}
	}

	if c, b, _ := do("POST", "/tarefas", `{"titulo":"Comprar pão"}`, tk); c != 201 || b != `{"id":1,"titulo":"Comprar pão","feita":false}` {
		t.Errorf("criar = %d %s", c, b)
	}
	if c, _, _ := do("POST", "/tarefas", `{"titulo":""}`, tk); c != 400 {
		t.Errorf("título vazio = %d", c)
	}
	if c, _, _ := do("POST", "/tarefas", `{"titulo":5}`, tk); c != 422 {
		t.Errorf("tipo errado = %d", c)
	}
	if c, b, _ := do("PUT", "/tarefas/1", `{"titulo":"Comprar leite","feita":false}`, tk); c != 200 || b != `{"id":1,"titulo":"Comprar leite","feita":false}` {
		t.Errorf("PUT = %d %s", c, b)
	}
	if c, b, _ := do("PATCH", "/tarefas/1/feita", `{"feita":true}`, tk); c != 200 || !strings.Contains(b, `"feita":true`) {
		t.Errorf("PATCH = %d %s", c, b)
	}
	do("POST", "/tarefas", `{"titulo":"Segunda"}`, tk)
	if c, b, _ := do("GET", "/tarefas", "", tk); c != 200 || !strings.HasPrefix(b, `[{"id":1,`) || !strings.Contains(b, `"titulo":"Segunda"`) {
		t.Errorf("lista = %d %s", c, b)
	}
	if c, b, _ := do("GET", "/tarefas/2?campos=titulo", "", tk); c != 200 || b != `{"titulo":"Segunda"}` {
		t.Errorf("?campos = %d %s", c, b)
	}
	if c, _, _ := do("GET", "/tarefas/99", "", tk); c != 404 {
		t.Errorf("tarefa que não existe = %d", c)
	}
	if c, b, _ := do("DELETE", "/tarefas/1", "", tk); c != 200 || b != `{"apagada":1}` {
		t.Errorf("DELETE = %d %s", c, b)
	}
	if c, _, _ := do("DELETE", "/tarefas/1", "", tk); c != 404 {
		t.Errorf("DELETE de novo = %d, queria 404", c)
	}
	// CORS: só a origem listada
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/tarefas/2", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("preflight = %d %v", resp.StatusCode, resp.Header)
	}
}
