//go:build !noweb

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sessCount() int {
	sessions.Lock()
	defer sessions.Unlock()
	return len(sessions.m)
}

func sessReset() {
	sessions.Lock()
	sessions.m = map[string]*session{}
	sessions.Unlock()
}

func TestSessaoLeituraNaoCria(t *testing.T) {
	sessReset()
	t.Cleanup(sessReset)
	dir := t.TempDir()
	w := func(n, s string) { os.WriteFile(filepath.Join(dir, n), []byte(s), 0o644) }
	w("le.djo", "print Session.get:\"user\" ?? \"anonimo\"\nSession.del:\"x\"\nSession.clear:\nprint Session.verify:\"qualquer\"\n")
	w("grava.djo", "Session.set:\"user\":\"ana\"\nprint \"ok\"\n")
	w("form.djo", "print Session.token:\n")
	srv, _ := NewServer(dir, true)
	do := func(u string, cks []*http.Cookie) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", u, nil)
		for _, c := range cks {
			r.AddCookie(c)
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	// 12 mil requisições sem cookie, só lendo: nada cresce, a página segue 200, sem cookie
	for i := 0; i < 12000; i++ {
		rec := do("/le", nil)
		if rec.Code != 200 || rec.Body.String() != "anonimo\nfalse\n" {
			t.Fatalf("req %d: %d %q", i, rec.Code, rec.Body.String())
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("leitura criou cookie (req %d)", i)
		}
	}
	if n := sessCount(); n != 0 {
		t.Fatalf("leituras criaram %d sessões", n)
	}
	// a escrita ainda cria, e o cookie válido funciona
	rec := do("/grava", nil)
	cks := rec.Result().Cookies()
	if rec.Code != 200 || len(cks) != 1 || cks[0].Name != sessCookie || sessCount() != 1 {
		t.Fatalf("escrita: %d %v %d", rec.Code, cks, sessCount())
	}
	if rec := do("/le", cks); rec.Body.String() != "anonimo\nfalse\n" && !strings.HasPrefix(rec.Body.String(), "ana\n") {
		t.Fatalf("cookie válido: %q", rec.Body.String())
	}
	// leitura com cookie que não existe (ou vencido): nil, sem criar nem renovar cookie
	bad := []*http.Cookie{{Name: sessCookie, Value: "naoexiste"}}
	if rec := do("/le", bad); rec.Code != 200 || len(rec.Result().Cookies()) != 0 || sessCount() != 1 {
		t.Fatalf("cookie inválido: %d %v %d", rec.Code, rec.Result().Cookies(), sessCount())
	}
}

func TestSessaoCSRFContinuaFuncionando(t *testing.T) {
	sessReset()
	t.Cleanup(sessReset)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "form.djo"), []byte("print Session.token:\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "post.djo"), []byte("print Session.verify:(Http.query:\"t\")\n"), 0o644)
	srv, _ := NewServer(dir, true)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/form", nil))
	cks := rec.Result().Cookies()
	token := strings.TrimSpace(rec.Body.String())
	if len(cks) != 1 || token == "" || sessCount() != 1 {
		t.Fatalf("o formulário precisa de sessão: %v %q %d", cks, token, sessCount())
	}
	for _, c := range []struct{ t, want string }{{token, "true\n"}, {"errado", "false\n"}} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/post?t="+c.t, nil)
		r.AddCookie(cks[0])
		srv.ServeHTTP(rec, r)
		if rec.Body.String() != c.want {
			t.Fatalf("verify %q: %q", c.t, rec.Body.String())
		}
	}
	// sem cookie, o verify dá false e não cria sessão
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/post?t="+token, nil))
	if rec.Body.String() != "false\n" || sessCount() != 1 {
		t.Fatalf("verify sem cookie: %q %d", rec.Body.String(), sessCount())
	}
}

func TestSessaoCheiaEvictaEmVezDeFalhar(t *testing.T) {
	sessReset()
	t.Cleanup(sessReset)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "form.djo"), []byte("print Session.token:\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "grava.djo"), []byte("Session.set:\"u\":\"ana\"\nprint \"ok\"\n"), 0o644)
	srv, _ := NewServer(dir, true)
	// uma sessão com dados (a de um usuário), a mais antiga de todas
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/grava", nil))
	user := rec.Result().Cookies()[0]
	sessions.Lock()
	sessions.m[user.Value].seen = time.Now().Add(-2 * time.Hour)
	sessions.Unlock()
	// enche com sessões vazias (só token) e passa do limite
	for i := 0; i < sessMax+500; i++ {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("GET", "/form", nil))
		if rec.Code != 200 {
			t.Fatalf("req %d: %d %q", i, rec.Code, rec.Body.String())
		}
	}
	if n := sessCount(); n > sessMax {
		t.Fatalf("a tabela passou do limite: %d", n)
	}
	sessions.Lock()
	_, kept := sessions.m[user.Value]
	sessions.Unlock()
	if !kept {
		t.Fatal("a sessão com dados foi evictada enquanto havia vazias")
	}
}
