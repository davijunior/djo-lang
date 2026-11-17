//go:build !noweb

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func laneServer(t *testing.T, rotas string) (*Server, *httptest.Server) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "rotas.djo", rotas)
	writeFile(t, root, "p/lista.djo", `print Json.encode:{"v": Hash.random:8}`)
	writeFile(t, root, "p/tela.djo", `print "tela " + Hash.random:8`)
	writeFile(t, root, "p/sessao.djo", "Http.setcookie:\"a\":\"b\"\nprint Hash.random:8")
	srv, err := NewServer(root, true)
	if err != nil {
		t.Fatal(err)
	}
	srv.maxSteps = 100_000
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, ts
}

func laneSite(t *testing.T, rotas string) (*Server, *httptest.Server) { return laneServer(t, rotas) }

func laneSite2(t *testing.T) (*Server, *httptest.Server) {
	return laneServer(t, `Route.get:"/lista":{"html": "p/tela.djo", "agent": "p/lista.djo"}`)
}

func req(t *testing.T, ts *httptest.Server, path string, h map[string]string) (int, string, http.Header) {
	t.Helper()
	r, _ := http.NewRequest("GET", ts.URL+path, nil)
	for k, v := range h {
		r.Header.Set(k, v)
	}
	resp, err := noRedirect.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

const J = "application/json"

func TestETagEh304(t *testing.T) {
	_, ts := laneSite(t, `Route.get:"/lista":{"html": "p/tela.djo", "agent": "p/lista.djo"}`)
	// a face de agente sempre leva ETag; a cada chamada o corpo muda (random), então não casa
	code, _, h := req(t, ts, "/lista", map[string]string{"Accept": J})
	if code != 200 || h.Get("ETag") == "" || h.Get("Cache-Control") != "private, no-cache" {
		t.Errorf("ETag/Cache-Control: %d %q %q", code, h.Get("ETag"), h.Get("Cache-Control"))
	}
	// a tela não ganha ETag (só agentes e rotas com "cache")
	if _, _, h := req(t, ts, "/lista", map[string]string{"Accept": "text/html"}); h.Get("ETag") != "" {
		t.Errorf("tela não deveria ter ETag")
	}
}

func TestCacheDaRota(t *testing.T) {
	_, ts := laneSite(t, `Route.get:"/lista":{"html": "p/tela.djo", "agent": "p/lista.djo", "cache": 30}
Route.get:"/sessao":{"html": "p/sessao.djo", "cache": 30}`)
	a := map[string]string{"Accept": J}
	code, b1, h1 := req(t, ts, "/lista", a)
	_, b2, h2 := req(t, ts, "/lista", a)
	if code != 200 || b1 != b2 || h1.Get("ETag") != h2.Get("ETag") || h1.Get("Cache-Control") != "public, max-age=30" {
		t.Errorf("cache não guardou: %q %q %q", b1, b2, h1.Get("Cache-Control"))
	}
	// 304 com If-None-Match, sem corpo
	code, body, h := req(t, ts, "/lista", map[string]string{"Accept": J, "If-None-Match": h1.Get("ETag")})
	if code != 304 || body != "" || h.Get("ETag") != h1.Get("ETag") {
		t.Errorf("304: %d %q", code, body)
	}
	// W/ e lista
	if code, _, _ := req(t, ts, "/lista", map[string]string{"Accept": J, "If-None-Match": `"x", W/` + h1.Get("ETag")}); code != 304 {
		t.Errorf("W/ e lista: %d", code)
	}
	// ETag diferente: 200
	if code, _, _ := req(t, ts, "/lista", map[string]string{"Accept": J, "If-None-Match": `"outro"`}); code != 200 {
		t.Errorf("etag diferente: %d", code)
	}
	// a tela e o agente têm entradas separadas
	_, tela, _ := req(t, ts, "/lista", map[string]string{"Accept": "text/html"})
	if !strings.HasPrefix(tela, "tela ") {
		t.Errorf("tela misturada com agente: %q", tela)
	}
	// a query faz parte da chave
	_, q1, _ := req(t, ts, "/lista?x=1", a)
	if q1 == b1 {
		t.Errorf("query diferente devolveu a mesma resposta")
	}
	// quem manda cookie ou credencial nunca recebe resposta guardada, e nada seu é guardado
	_, c1, _ := req(t, ts, "/lista", map[string]string{"Accept": J, "Cookie": "sid=1"})
	if c1 == b1 {
		t.Errorf("requisição com cookie recebeu a resposta guardada")
	}
	_, d1, _ := req(t, ts, "/lista", map[string]string{"Accept": J, "Authorization": "Bearer x"})
	if d1 == b1 {
		t.Errorf("requisição com Authorization recebeu a resposta guardada")
	}
	// página que define cookie não é guardada
	_, s1, h := req(t, ts, "/sessao", nil)
	_, s2, _ := req(t, ts, "/sessao", nil)
	if s1 == s2 || h.Get("Cache-Control") != "" && !strings.Contains(h.Get("Cache-Control"), "no-store") {
		t.Errorf("página com cookie foi guardada: %q %q %q", s1, s2, h.Get("Cache-Control"))
	}
}

func TestCacheExpira(t *testing.T) {
	var c respCache
	c.put("k", &cachedResp{exp: time.Now().Add(-time.Second)})
	if c.get("k") != nil {
		t.Errorf("entrada vencida deveria sumir")
	}
	for i := 0; i < cacheMaxEntries+50; i++ {
		c.put(itoa(i), &cachedResp{exp: time.Now().Add(time.Minute)})
	}
	if len(c.m) > cacheMaxEntries {
		t.Errorf("cache passou do limite: %d", len(c.m))
	}
}

func TestCacheErrosDeDeclaracao(t *testing.T) {
	for src, want := range map[string]string{
		`Route.post:"/a":{"html": "p/tela.djo", "cache": 5}`:  "só vale em Route.get",
		`Route.get:"/a":{"html": "p/tela.djo", "cache": -1}`:  "segundos",
		`Route.get:"/a":{"html": "p/tela.djo", "cache": "x"}`: "segundos",
	} {
		root := t.TempDir()
		writeFile(t, root, "p/tela.djo", `print "x"`)
		writeFile(t, root, "rotas.djo", src)
		if _, err := NewServer(root, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestPistaDeAgentes(t *testing.T) {
	l := newLane(2, 0)
	l.wait = 30 * time.Millisecond
	do := func() (release func(), code int) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		rel := l.acquire(w, r)
		return rel, w.Code
	}
	r1, _ := do()
	r2, _ := do()
	if r1 == nil || r2 == nil {
		t.Fatal("as duas primeiras vagas deveriam existir")
	}
	w := httptest.NewRecorder()
	if rel := l.acquire(w, httptest.NewRequest("GET", "/", nil)); rel != nil || w.Code != 429 || w.Header().Get("Retry-After") != "1" {
		t.Errorf("pista cheia: code=%d retry=%q", w.Code, w.Header().Get("Retry-After"))
	}
	r1() // libera uma vaga: a próxima passa
	if rel, _ := do(); rel == nil {
		t.Errorf("depois de liberar deveria passar")
	}
}

func TestLimiteDeTaxaPorCliente(t *testing.T) {
	l := newLane(4, 5) // 5/s, rajada de 10
	now := time.Now()
	ok := 0
	for i := 0; i < 30; i++ {
		if l.allow("1.1.1.1", now) {
			ok++
		}
	}
	if ok != 10 {
		t.Errorf("rajada: passaram %d, queria 10", ok)
	}
	if !l.allow("2.2.2.2", now) {
		t.Errorf("outro cliente não pode ser afetado")
	}
	if !l.allow("1.1.1.1", now.Add(time.Second)) { // 1 s depois, 5 fichas voltam
		t.Errorf("deveria reabastecer")
	}
}

func TestHumanoNaoEntraNaPista(t *testing.T) {
	srv, ts := laneSite2(t)
	for i := 0; i < cap(srv.lane.sem); i++ {
		srv.lane.sem <- struct{}{} // pista de agentes lotada
	}
	if code, _, _ := req(t, ts, "/lista", map[string]string{"Accept": J}); code != 429 {
		t.Errorf("agente com a pista cheia: %d", code)
	}
	if code, body, _ := req(t, ts, "/lista", map[string]string{"Accept": "text/html"}); code != 200 || !strings.HasPrefix(body, "tela") {
		t.Errorf("a tela não pode esperar pelos agentes: %d %q", code, body)
	}
}

func TestLaneTabelaCheiaDescartaOMaisAntigo(t *testing.T) {
	l := newLane(4, 1)
	now := time.Now()
	for i := 0; i < 10000; i++ {
		l.clients[fmt.Sprintf("ip%d", i)] = &bucket{tokens: 2, last: now.Add(time.Duration(i) * time.Millisecond)}
	}
	if !l.allow("novo", now.Add(time.Minute)) {
		t.Fatal("cliente novo não pode ser recusado só porque a tabela está cheia")
	}
	if len(l.clients) != 10000 {
		t.Errorf("tabela cresceu: %d", len(l.clients))
	}
	if _, ok := l.clients["ip0"]; ok {
		t.Error("devia ter descartado o mais antigo (ip0)")
	}
}
