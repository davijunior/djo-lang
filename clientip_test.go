//go:build !noweb

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reqIP(remote string, h map[string][]string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for k, vs := range h {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	return r
}

func TestClientIP(t *testing.T) {
	usarProxies(t, "127.0.0.0/8,::1")
	xff := func(v ...string) map[string][]string { return map[string][]string{"X-Forwarded-For": v} }
	real := func(v string) map[string][]string { return map[string][]string{"X-Real-IP": {v}} }
	for _, c := range []struct {
		nome, remote string
		h            map[string][]string
		quer         string
	}{
		{"sem proxy: ignora cabeçalhos forjados", "203.0.113.9:5", xff("1.2.3.4"), "203.0.113.9"},
		{"sem proxy: ignora X-Real-IP", "203.0.113.9:5", real("1.2.3.4"), "203.0.113.9"},
		{"proxy sem cabeçalho", "127.0.0.1:5", nil, "127.0.0.1"},
		{"proxy + XFF simples", "127.0.0.1:5", xff("198.51.100.7"), "198.51.100.7"},
		{"visitante forja a esquerda", "127.0.0.1:5", xff("6.6.6.6, 198.51.100.7"), "198.51.100.7"},
		{"cadeia com proxy confiável no fim", "127.0.0.1:5", xff("198.51.100.7, 127.0.0.1"), "198.51.100.7"},
		{"vários cabeçalhos XFF", "127.0.0.1:5", xff("6.6.6.6", "198.51.100.7"), "198.51.100.7"},
		{"cadeia só de proxies", "127.0.0.1:5", xff("127.0.0.2, 127.0.0.1"), "127.0.0.1"},
		{"direita inválida: não pula para a esquerda", "127.0.0.1:5", xff("6.6.6.6, lixo"), "127.0.0.1"},
		{"XFF vazio", "127.0.0.1:5", xff(""), "127.0.0.1"},
		{"lista mal formada", "127.0.0.1:5", xff(",,, ,"), "127.0.0.1"},
		{"XFF vence o X-Real-IP", "127.0.0.1:5", map[string][]string{"X-Forwarded-For": {"198.51.100.7"}, "X-Real-IP": {"9.9.9.9"}}, "198.51.100.7"},
		{"X-Real-IP sozinho", "127.0.0.1:5", real("198.51.100.8"), "198.51.100.8"},
		{"X-Real-IP inválido", "127.0.0.1:5", real("não sou ip"), "127.0.0.1"},
		{"IPv6 no XFF", "127.0.0.1:5", xff("2001:db8::1"), "2001:db8::1"},
		{"IPv6 com colchetes e porta não é IP", "127.0.0.1:5", xff("[2001:db8::1]:443"), "127.0.0.1"},
		{"IPv4 mapeado vira IPv4", "127.0.0.1:5", xff("::ffff:198.51.100.7"), "198.51.100.7"},
		{"zona IPv6 sai", "127.0.0.1:5", xff("fe80::1%eth0"), "fe80::1"},
		{"conexão IPv6 de proxy", "[::1]:5", xff("198.51.100.7"), "198.51.100.7"},
		{"RemoteAddr sem porta", "203.0.113.9", xff("1.2.3.4"), "203.0.113.9"},
	} {
		if got := clientIP(reqIP(c.remote, c.h)); got != c.quer {
			t.Errorf("%s: %q, esperado %q", c.nome, got, c.quer)
		}
	}
}

func TestClientIPListaConfigurada(t *testing.T) {
	usarProxies(t, "10.0.0.0/8,192.168.1.7")
	h := map[string][]string{"X-Forwarded-For": {"198.51.100.7, 10.1.1.1, 192.168.1.7"}}
	if got := clientIP(reqIP("10.2.2.2:1", h)); got != "198.51.100.7" {
		t.Errorf("cadeia de dois proxies: %q", got)
	}
	// o loopback deixou de ser confiável: ignora o cabeçalho
	if got := clientIP(reqIP("127.0.0.1:1", h)); got != "127.0.0.1" {
		t.Errorf("loopback fora da lista: %q", got)
	}
	usarProxies(t, "none")
	if got := clientIP(reqIP("127.0.0.1:1", h)); got != "127.0.0.1" {
		t.Errorf("none: %q", got)
	}
}

// ponta a ponta: Http.ip numa página e o limite do -agent-rate por IP real.
func TestHttpIPAtrasDeProxy(t *testing.T) {
	usarProxies(t, "127.0.0.0/8,::1")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ip.djo"), []byte("print Http.ip:\n"), 0o644)
	srv, err := NewServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	get := func(remote, xff string) string {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/ip", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		srv.ServeHTTP(rec, r)
		return strings.TrimSpace(rec.Body.String())
	}
	if got := get("127.0.0.1:9", "198.51.100.7"); got != "198.51.100.7" {
		t.Errorf("atrás do proxy: %q", got)
	}
	if got := get("203.0.113.9:9", "1.2.3.4"); got != "203.0.113.9" {
		t.Errorf("visitante forjando: %q", got)
	}
	if got := get("127.0.0.1:9", ""); got != "127.0.0.1" {
		t.Errorf("sem cabeçalho: %q", got)
	}
}

func TestLaneContaPorIPReal(t *testing.T) {
	usarProxies(t, "127.0.0.0/8,::1")
	l := newLane(4, 1) // 1/s, rajada de 2
	via := func(xff string) bool {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		rel := l.acquire(rec, r)
		if rel != nil {
			rel()
		}
		return rel != nil
	}
	for i := 0; i < 2; i++ {
		if !via("198.51.100.1") {
			t.Fatal("a rajada devia passar")
		}
	}
	if via("198.51.100.1") {
		t.Error("o mesmo visitante estourou o limite")
	}
	if !via("198.51.100.2") {
		t.Error("outro visitante, atrás do mesmo proxy, tem o seu próprio balde")
	}
}

// o padrão (loopback, sem configurar) não lê cabeçalho de IP: um nginx sem a config certa os repassaria forjados.
func TestClientIPPadraoNaoLeCabecalho(t *testing.T) {
	usarProxies(t, "")
	h := map[string][]string{"X-Forwarded-For": {"6.6.6.6"}, "X-Real-IP": {"6.6.6.6"}}
	if got := clientIP(reqIP("127.0.0.1:1", h)); got != "127.0.0.1" {
		t.Errorf("padrão leu cabeçalho: %q", got)
	}
	// mas o X-Forwarded-Proto continua valendo no padrão
	if !requestIsHTTPS(reqDe("127.0.0.1:1", "https", false)) {
		t.Error("o Secure do cookie deve seguir funcionando no padrão")
	}
	// lista explícita (mesmo igual ao loopback) lê
	usarProxies(t, "127.0.0.1")
	if got := clientIP(reqIP("127.0.0.1:1", h)); got != "6.6.6.6" {
		t.Errorf("lista explícita devia ler: %q", got)
	}
}

func TestConfigureTrustedProxyMarcaExplicito(t *testing.T) {
	old, oldExp := trustedProxies, trustedExplicit
	defer func() { trustedProxies, trustedExplicit = old, oldExp }()
	t.Setenv("DJO_TRUSTED_PROXY", "")
	for _, c := range []struct {
		flag    string
		set     bool
		env     string
		explico bool
	}{{"", false, "", false}, {"", false, "10.0.0.1", true}, {"127.0.0.1", true, "", true}, {"none", true, "", true}, {"", true, "10.0.0.1", false}} {
		t.Setenv("DJO_TRUSTED_PROXY", c.env)
		if err := configureTrustedProxy(c.flag, c.set); err != nil {
			t.Fatal(err)
		}
		if trustedExplicit != c.explico {
			t.Errorf("%+v: explícito = %v", c, trustedExplicit)
		}
	}
}
