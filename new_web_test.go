//go:build !noweb

package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSiteFunciona(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "meu-site")
	if code, _, e := criar(t, dir); code != 0 {
		t.Fatal(e)
	}
	if err := checkRoutes(dir); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(dir, true) // -dev: confere as respostas contra o "returns"
	if err != nil {
		t.Fatal(err)
	}
	srv.lane = newLane(64, 0)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	a := map[string]string{"Accept": J}

	if code, body, _ := req(t, ts, "/", nil); code != 200 || !strings.Contains(body, "<h1>meu-site</h1>") {
		t.Fatalf("/ humano: %d %q", code, body)
	}
	if code, body, _ := req(t, ts, "/", a); code != 200 || !strings.Contains(body, "Olá! Este é o site meu-site") {
		t.Fatalf("/ agente: %d %q", code, body)
	}
	if code, body, _ := req(t, ts, "/notas", a); code != 200 || !strings.Contains(body, `"titulo":"Primeira nota"`) {
		t.Fatalf("/notas agente: %d %q", code, body)
	}
	if code, body, _ := req(t, ts, "/notas?tabela&campos=id,titulo", a); code != 200 || !strings.HasPrefix(body, "id,titulo\r\n1,Primeira nota") {
		t.Fatalf("tabela: %d %q", code, body)
	}
	if code, body, _ := req(t, ts, "/notas/2", a); code != 200 || !strings.Contains(body, "Segunda nota") {
		t.Fatalf("/notas/2: %d %q", code, body)
	}
	if code, _, _ := req(t, ts, "/notas/9", a); code != 404 {
		t.Fatalf("/notas/9 deveria ser 404, veio %d", code)
	}
	if code, body, _ := req(t, ts, "/notas", nil); code != 200 || !strings.Contains(body, "Primeira nota") {
		t.Fatalf("/notas humano: %d %q", code, body)
	}
	if code, body, _ := req(t, ts, "/notas/1", nil); code != 200 || !strings.Contains(body, "<h1>Primeira nota</h1>") {
		t.Fatalf("/notas/1 humano: %d %q", code, body)
	}
	_, mb, _ := req(t, ts, manifestPath, a)
	for _, want := range []string{"deixar_recado", "/notas/{id}/recados", `"nome": "meu-site"`} {
		if !strings.Contains(mb, want) {
			t.Errorf("manifesto sem %q", want)
		}
	}
}
