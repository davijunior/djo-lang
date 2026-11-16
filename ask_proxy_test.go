//go:build !noweb && !nodb

package main

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

func nets(t *testing.T, cidrs ...string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range cidrs {
		if !strings.Contains(c, "/") {
			c += "/32"
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestAskClientIP(t *testing.T) {
	mk := func(remote string, xff ...string) *http.Request {
		r, _ := http.NewRequest("GET", "/ask", nil)
		r.RemoteAddr = remote
		for _, x := range xff {
			r.Header.Add("X-Forwarded-For", x)
		}
		return r
	}
	px := nets(t, "10.0.0.1", "192.168.0.0/16")
	cases := []struct {
		name  string
		r     *http.Request
		proxy []*net.IPNet
		want  string
	}{
		{"sem Ask.proxy ignora o header", mk("10.0.0.1:5000", "1.2.3.4"), nil, "10.0.0.1"},
		{"conexão que não é proxy: ignora", mk("8.8.8.8:5000", "1.2.3.4"), px, "8.8.8.8"},
		{"proxy + um cliente", mk("10.0.0.1:5000", "1.2.3.4"), px, "1.2.3.4"},
		{"último não confiável da cadeia", mk("10.0.0.1:5000", "6.6.6.6, 1.2.3.4, 192.168.5.5"), px, "1.2.3.4"},
		{"forjar à esquerda não ajuda", mk("10.0.0.1:5000", "9.9.9.9, 1.2.3.4"), px, "1.2.3.4"},
		{"vários cabeçalhos", mk("10.0.0.1:5000", "9.9.9.9", "1.2.3.4, 10.0.0.1"), px, "1.2.3.4"},
		{"cadeia só de proxies", mk("10.0.0.1:5000", "192.168.1.1"), px, "10.0.0.1"},
		{"sem header", mk("10.0.0.1:5000"), px, "10.0.0.1"},
		{"entrada inválida = IP da conexão", mk("10.0.0.1:5000", "1.2.3.4, lixo"), px, "10.0.0.1"},
		{"entrada vazia", mk("10.0.0.1:5000", "1.2.3.4,,"), px, "10.0.0.1"},
		{"IPv6", mk("10.0.0.1:5000", "2001:db8::1"), px, "2001:db8::1"},
		{"cadeia enorme", mk("10.0.0.1:5000", strings.Repeat("1.1.1.1,", 60)+"2.2.2.2"), px, "10.0.0.1"},
	}
	for _, c := range cases {
		if got := askClientIP(c.r, c.proxy); got != c.want {
			t.Errorf("%s: %s, queria %s", c.name, got, c.want)
		}
	}
}

func TestAskProxyConfig(t *testing.T) {
	ok := askDjo + `Ask.proxy:["127.0.0.1", "10.0.0.0/8", "::1", "fd00::/8"]` + "\n"
	if err := askLoad(t, askCore, ok); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{
		"lixo":            `["abc"]`,
		"cidr ruim":       `["10.0.0.0/99"]`,
		"confia em todos": `["0.0.0.0/0"]`,
		"lista vazia":     `[]`,
		"não é lista":     `"127.0.0.1"`,
	} {
		if err := askLoad(t, askCore, askDjo+"Ask.proxy:"+v+"\n"); err == nil {
			t.Errorf("%s: deveria falhar", name)
		}
	}
}

func TestAskHTTPLimitePorIPAtrasDeProxy(t *testing.T) {
	limit := func(extra string) string {
		return strings.Replace(askDjoWith(extra), `"per_minute": 30`, `"per_minute": 2`, 1)
	}
	xff := func(ip string) map[string]string { return map[string]string{"X-Forwarded-For": ip} }
	call := func(a *askSite, ip string) int {
		st, _, _ := a.do(t, "POST", "/ask", `{"question":"how many posts"}`, "", xff(ip))
		return st
	}
	// sem Ask.proxy: o header é ignorado, todos dividem o IP da conexão (e não dá para escapar forjando)
	a := askHTTPSite(t, limit(""), nil)
	call(a, "1.1.1.1")
	call(a, "2.2.2.2")
	if st := call(a, "3.3.3.3"); st != 429 {
		t.Errorf("sem Ask.proxy, o header forjado escapou do limite: %d", st)
	}
	// com Ask.proxy: cada cliente tem o seu limite; forjar à esquerda não ajuda
	b := askHTTPSite(t, limit(`Ask.proxy:["127.0.0.1"]`+"\n"), nil)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		if st := call(b, ip); st != 200 {
			t.Fatalf("%s: %d", ip, st)
		}
	}
	call(b, "1.1.1.1")
	if st := call(b, "1.1.1.1"); st != 429 {
		t.Errorf("o cliente 1.1.1.1 deveria estourar o próprio limite: %d", st)
	}
	if st := call(b, "2.2.2.2"); st != 200 {
		t.Fatalf("2.2.2.2 ainda tem uma chamada: %d", st)
	}
	if st := call(b, "7.7.7.7, 2.2.2.2"); st != 429 {
		t.Errorf("forjar um IP à esquerda não pode dar limite novo: %d", st)
	}
	if st := call(b, "3.3.3.3"); st != 200 {
		t.Errorf("outro cliente não deveria ser limitado: %d", st)
	}
}
