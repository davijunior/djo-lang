//go:build !nonet

package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHttpCliente(t *testing.T) {
	var gotBody, gotType, gotAuth, gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotType, gotAuth, gotMethod = string(b), r.Header.Get("Content-Type"), r.Header.Get("Authorization"), r.Method
		switch r.URL.Path {
		case "/erro":
			w.WriteHeader(500)
			w.Write([]byte("falhou"))
		case "/grande":
			w.Write(make([]byte, clientMaxBody+10))
		default:
			w.Header().Set("X-Teste", "sim")
			w.Write([]byte(`{"ok": true}`))
		}
	}))
	defer ts.Close()

	out, err := run(t, `var r = Http.get:"`+ts.URL+`/x"
print r["ok"]
print r["status"]
print (Json.decode:r["body"])["ok"]
print r["headers"]["x-teste"]
var e = Http.get:"`+ts.URL+`/erro"
print e["ok"]
print e["status"]
print e["body"]
var p = Http.post:"`+ts.URL+`/x":{"temp": 21.5}:{"Authorization": "Bearer abc"}
print p["ok"]
var q = Http.post:"`+ts.URL+`/x":"texto puro"
print q["status"]
var g = Http.get:"`+ts.URL+`/grande"
print g["ok"]
print g["error"]
var fh = Http.get:"http://127.0.0.1:1/"
print fh["ok"]
print fh["status"]
print fh["error"] != nil
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "true\n200\ntrue\nsim\nfalse\n500\nfalhou\ntrue\n200\nfalse\nresposta grande demais (máximo 5 MB)\nfalse\n0\ntrue\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	// o último POST (texto) e o anterior (JSON)
	if _, err := run(t, `Http.post:"`+ts.URL+`/x":{"a": 1}:{"Authorization": "tok"}`); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotBody != `{"a":1}` || gotType != "application/json" || gotAuth != "tok" {
		t.Fatalf("requisição: %s %q %q %q", gotMethod, gotBody, gotType, gotAuth)
	}
	run(t, `Http.put:"`+ts.URL+`/x":"abc"`)
	if gotMethod != "PUT" || gotBody != "abc" || !strings.HasPrefix(gotType, "text/plain") {
		t.Fatalf("PUT: %s %q %q", gotMethod, gotBody, gotType)
	}
	run(t, `Http.delete:"`+ts.URL+`/x"`)
	if gotMethod != "DELETE" {
		t.Fatalf("DELETE: %s", gotMethod)
	}
	for _, bad := range []string{`Http.get:"ftp://x"`, `Http.get:5`, `Http.post:"http://x"`, `Http.get:"http://x":{"A": 1}`, `Http.get:"http://x":{"A": "b\nc"}`, `Http.get:`} {
		if _, err := run(t, bad); err == nil {
			t.Fatalf("esperava erro em %q", bad)
		}
	}
}

func TestHttpClienteBloqueiaRedeInterna(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("segredo")) }))
	defer ts.Close()
	prog, err := Parse(`var r = Http.get:"` + ts.URL + `"
print r["ok"]
print r["body"]
print r["error"]
`)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	in := NewInterp(&sb)
	in.served = true // como no djo serve
	if err := in.Run(prog); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sb.String(), "false\n\nendereço interno bloqueado") {
		t.Fatalf("deveria bloquear: %q", sb.String())
	}
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.0.9", "172.16.0.1", "169.254.169.254", "100.64.0.1", "::1", "fe80::1", "0.0.0.0", "fd00::1"} {
		if !blockedIP(parseIP(ip)) {
			t.Errorf("%s deveria ser bloqueado", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"} {
		if blockedIP(parseIP(ip)) {
			t.Errorf("%s não deveria ser bloqueado", ip)
		}
	}
}

func parseIP(s string) net.IP { return net.ParseIP(s) }
