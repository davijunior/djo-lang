//go:build !noweb

package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// usarProxies troca a lista de proxies confiáveis durante o teste.
func usarProxies(t *testing.T, v string) {
	t.Helper()
	list, err := parseTrustedProxies(v)
	if err != nil {
		t.Fatal(err)
	}
	old := trustedProxies
	trustedProxies = list
	t.Cleanup(func() { trustedProxies = old })
}

func reqDe(remote, xfp string, comTLS bool) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	if xfp != "" {
		r.Header.Set("X-Forwarded-Proto", xfp)
	}
	if comTLS {
		r.TLS = &tls.ConnectionState{}
	}
	return r
}

func TestRequestIsHTTPS(t *testing.T) {
	usarProxies(t, "")
	casos := []struct {
		nome   string
		remote string
		xfp    string
		tls    bool
		quer   bool
	}{
		{"loopback v4 + https", "127.0.0.1:5000", "https", false, true},
		{"loopback v4 outro endereço do /8", "127.5.6.7:5000", "https", false, true},
		{"loopback v6 + https", "[::1]:5000", "https", false, true},
		{"loopback v4 mapeado em v6", "[::ffff:127.0.0.1]:5000", "https", false, true},
		{"HTTPS maiúsculo e com espaço", "127.0.0.1:5000", "  HTTPS ", false, true},
		{"lista: primeiro https", "127.0.0.1:5000", "https, http", false, true},
		{"lista: primeiro http", "127.0.0.1:5000", "http, https", false, false},
		{"http", "127.0.0.1:5000", "http", false, false},
		{"lixo", "127.0.0.1:5000", "https://x", false, false},
		{"vazio com vírgula", "127.0.0.1:5000", ", https", false, false},
		{"sem cabeçalho", "127.0.0.1:5000", "", false, false},
		{"IP externo + https (spoofing)", "203.0.113.9:5000", "https", false, false},
		{"IP privado de fora do loopback + https", "10.0.0.5:5000", "https", false, false},
		{"IP externo v6 + https", "[2001:db8::1]:5000", "https", false, false},
		{"RemoteAddr sem porta, não é IP", "unix", "https", false, false},
		{"TLS direto", "203.0.113.9:5000", "", true, true},
		{"TLS direto ignora http no cabeçalho", "203.0.113.9:5000", "http", true, true},
	}
	for _, c := range casos {
		if got := requestIsHTTPS(reqDe(c.remote, c.xfp, c.tls)); got != c.quer {
			t.Errorf("%s: requestIsHTTPS = %v, esperado %v", c.nome, got, c.quer)
		}
	}
}

func TestRequestIsHTTPSConfigurado(t *testing.T) {
	usarProxies(t, "none")
	if requestIsHTTPS(reqDe("127.0.0.1:1", "https", false)) {
		t.Error("com none, nem o loopback é confiável")
	}
	if !requestIsHTTPS(reqDe("127.0.0.1:1", "", true)) {
		t.Error("TLS direto vale mesmo com none")
	}
	usarProxies(t, "10.0.0.0/8, 192.168.1.7")
	for remote, quer := range map[string]bool{
		"10.9.8.7:1": true, "192.168.1.7:1": true, "192.168.1.8:1": false, "127.0.0.1:1": false, "11.0.0.1:1": false,
	} {
		if got := requestIsHTTPS(reqDe(remote, "https", false)); got != quer {
			t.Errorf("%s: %v, esperado %v", remote, got, quer)
		}
	}
}

func TestParseTrustedProxies(t *testing.T) {
	for _, ok := range []string{"", "none", "NONE", "127.0.0.1", "::1", "10.0.0.0/8", "10.0.0.1, 2001:db8::/32", "::ffff:10.0.0.0/104", "10.0.0.0/8", "2001::/16"} {
		if _, err := parseTrustedProxies(ok); err != nil {
			t.Errorf("%q deveria valer: %v", ok, err)
		}
	}
	for _, ruim := range []string{"all", "*", "0.0.0.0/0", "::/0", "0.0.0.0/1", "10.0.0.0/7", "::/1", "2000::/15", "10.0.0.0/33", "lixo", "10.0.0.1,,10.0.0.2", "none,10.0.0.1", "10.0.0.1,"} {
		if _, err := parseTrustedProxies(ruim); err == nil {
			t.Errorf("%q deveria ser recusado", ruim)
		}
	}
	if l, _ := parseTrustedProxies("none"); len(l) != 0 {
		t.Errorf("none = %v", l)
	}
	if l, _ := parseTrustedProxies(""); len(l) != 2 {
		t.Errorf("padrão = %v", l)
	}
}

func TestConfigureTrustedProxyFlagVenceEnv(t *testing.T) {
	old := trustedProxies
	t.Cleanup(func() { trustedProxies = old })
	t.Setenv("DJO_TRUSTED_PROXY", "10.0.0.0/8")
	if err := configureTrustedProxy("", false); err != nil || !fromTrustedProxy(reqDe("10.1.1.1:1", "", false)) || fromTrustedProxy(reqDe("127.0.0.1:1", "", false)) {
		t.Errorf("a variável deveria valer sem flag: %v %v", err, trustedProxies)
	}
	if err := configureTrustedProxy("none", true); err != nil || len(trustedProxies) != 0 {
		t.Errorf("a flag deveria vencer a variável: %v %v", err, trustedProxies)
	}
	if err := configureTrustedProxy("", true); err != nil || len(trustedProxies) != 2 {
		t.Errorf("flag vazia explícita = padrão: %v %v", err, trustedProxies)
	}
	t.Setenv("DJO_TRUSTED_PROXY", "*")
	if err := configureTrustedProxy("", false); err == nil {
		t.Error("variável inválida deveria dar erro")
	}
}

// ponta a ponta: o cookie de sessão (djo_sid) e o de Http.setcookie saem com Secure só quando devem.
func TestCookiesSecureAtrasDeProxy(t *testing.T) {
	usarProxies(t, "")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.djo"), []byte("Session.set:\"n\":1\nHttp.setcookie:\"tema\":\"escuro\"\nprint \"ok\"\n"), 0o644)
	srv, err := NewServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	secure := func(remote, xfp string, comTLS bool) map[string]bool {
		rec := httptest.NewRecorder()
		r := reqDe(remote, xfp, comTLS)
		r.URL.Path = "/a"
		srv.ServeHTTP(rec, r)
		m := map[string]bool{}
		for _, c := range rec.Result().Cookies() {
			m[c.Name] = c.Secure
		}
		return m
	}
	for _, c := range []struct {
		nome, remote, xfp string
		tls, quer         bool
	}{
		{"proxy local + https", "127.0.0.1:1", "https", false, true},
		{"IP externo + https", "203.0.113.9:1", "https", false, false},
		{"sem cabeçalho", "127.0.0.1:1", "", false, false},
		{"proxy local + http", "127.0.0.1:1", "http", false, false},
		{"TLS direto", "203.0.113.9:1", "", true, true},
	} {
		m := secure(c.remote, c.xfp, c.tls)
		for _, nome := range []string{sessCookie, "tema"} {
			v, achou := m[nome]
			if !achou {
				t.Errorf("%s: cookie %s não veio (%v)", c.nome, nome, m)
			} else if v != c.quer {
				t.Errorf("%s: cookie %s Secure = %v, esperado %v", c.nome, nome, v, c.quer)
			}
		}
	}
}
