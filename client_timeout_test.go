//go:build !nonet

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func slowServer(d time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lento" {
			time.Sleep(d)
		}
		w.Write([]byte("ok"))
	}))
}

func TestHttpTimeoutPorChamada(t *testing.T) {
	ts := slowServer(2500 * time.Millisecond)
	defer ts.Close()
	// os quatro métodos, com nil nos cabeçalhos, Int e texto de duração; estouro não é erro
	out, err := run(t, `
var a = Http.get:"`+ts.URL+`/lento":nil:1
print a["ok"]
print a["error"]
var b = Http.delete:"`+ts.URL+`/lento":{"X": "1"}:"1s"
print b["error"]
var c = Http.post:"`+ts.URL+`/lento":"x":nil:1
print c["error"]
var d = Http.put:"`+ts.URL+`/lento":{"a": 1}:{"X": "1"}:1
print d["error"]
var e = Http.get:"`+ts.URL+`/rapido":nil:5
print e["ok"]
var g = Http.get:"`+ts.URL+`/lento":nil:"4s"
print g["body"]
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "false\ntempo esgotado (1 s)\ntempo esgotado (1 s)\ntempo esgotado (1 s)\ntempo esgotado (1 s)\ntrue\nok\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestHttpTimeoutArgumentosInvalidos(t *testing.T) {
	for _, c := range []struct{ src, err string }{
		{`Http.get:"http://127.0.0.1:1/":nil:"abc"`, "o timeout é um número de segundos"},
		{`Http.get:"http://127.0.0.1:1/":nil:true`, "o timeout é um número de segundos"},
		{`Http.get:"http://127.0.0.1:1/":nil:0`, "o timeout mínimo é 1 s"},
		{`Http.get:"http://127.0.0.1:1/":nil:61`, "o timeout máximo aqui é 60 s"},
		{`Http.get:"http://127.0.0.1:1/":nil:"2m"`, "o timeout máximo aqui é 60 s"},
		{`Http.post:"http://127.0.0.1:1/":"x":nil:5:6`, "argumentos demais"},
		{`Http.get:"http://127.0.0.1:1/":nil:5:6`, "argumentos demais"},
		{`Http.get:"http://127.0.0.1:1/":"x"`, "os cabeçalhos vão num Map"},
	} {
		if _, err := run(t, c.src+"\n"); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: esperava %q, veio %v", c.src, c.err, err)
		}
	}
	// o piso e o teto valem nas bordas
	if _, err := run(t, `var r = Http.get:"http://127.0.0.1:1/":nil:60`+"\n"+`var s = Http.get:"http://127.0.0.1:1/":nil:1`+"\n"); err != nil {
		t.Fatalf("1 s e 60 s valem: %v", err)
	}
}

func TestHttpTimeoutPadraoContinuaOito(t *testing.T) {
	in := NewInterp(&strings.Builder{})
	if d := in.httpTimeout("Http.get", nil, false, 1); d != 8*time.Second {
		t.Fatalf("padrão %v", d)
	}
}

func TestHttpTimeoutTetosDaPagina(t *testing.T) {
	in := NewInterp(&strings.Builder{})
	in.served = true
	mustFail := func(v any, given bool, want string) {
		t.Helper()
		defer func() {
			r := recover()
			e, ok := r.(*djoError)
			if !ok || !strings.Contains(e.Msg, want) {
				t.Fatalf("esperava %q, veio %v", want, r)
			}
		}()
		in.httpTimeout("Http.get", v, given, 1)
	}
	mustFail(int64(21), true, "o timeout máximo aqui é 20 s")
	mustFail("30s", true, "o timeout máximo aqui é 20 s")
	if d := in.httpTimeout("Http.get", int64(20), true, 1); d != 20*time.Second {
		t.Fatalf("20 s valem na página: %v", d)
	}
	// a soma das esperas: o que sobra da cota de 25 s corta a chamada
	in.outWaited = 20 * time.Second
	if d := in.httpTimeout("Http.get", int64(20), true, 1); d != 5*time.Second {
		t.Fatalf("sobram 5 s: %v", d)
	}
	if d := in.httpTimeout("Http.get", nil, false, 1); d != 5*time.Second {
		t.Fatalf("o padrão de 8 s também respeita a cota: %v", d)
	}
	in.outWaited = 25 * time.Second
	mustFail(nil, false, "a página já esperou 25 s")
}

func TestHttpTimeoutDoJob(t *testing.T) {
	in := NewInterp(&strings.Builder{})
	in.job = &jobHost{inJob: true}
	in.deadline = time.Now().Add(3 * time.Second)
	if d := in.httpTimeout("Http.get", int64(60), true, 1); d > 3*time.Second || d < 2*time.Second {
		t.Fatalf("o prazo do job limita a chamada: %v", d)
	}
	in.deadline = time.Now().Add(10 * time.Minute)
	if d := in.httpTimeout("Http.get", int64(60), true, 1); d != 60*time.Second {
		t.Fatalf("teto do job %v", d)
	}
	if d := in.httpTimeout("Http.get", nil, false, 1); d != 8*time.Second {
		t.Fatalf("padrão no job %v", d)
	}
}

// O prazo do job estoura NO MEIO da chamada: erro fatal (o promise não pega, o finally não roda).
func TestHttpPrazoDoJobNoMeioDaChamadaEFatal(t *testing.T) {
	ts := slowServer(3 * time.Second)
	defer ts.Close()
	src := `
f chama
    uniq promise{
        var r = Http.get:"` + ts.URL + `/lento":nil:30
        print "nao chegou"
    }error{
        [e]
        print "pegou"
    }finally{
        print "finally"
    }
`
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	in := NewInterp(&out)
	in.job = &jobHost{inJob: true}
	in.deadline = time.Now().Add(1200 * time.Millisecond)
	if err := in.Run(prog); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	callErr, _, _ := jobInvoke(in, "chama", "{}")
	if callErr == nil || !strings.Contains(callErr.Error(), "o job passou do tempo limite") {
		t.Fatalf("esperava o erro fatal do prazo do job, veio %v (saída %q)", callErr, out.String())
	}
	if out.String() != "" {
		t.Fatalf("o error e o finally não rodam num fatal: %q", out.String())
	}
	if time.Since(started) > 2500*time.Millisecond {
		t.Fatalf("a chamada deveria ser cortada pelo prazo do job (%v)", time.Since(started))
	}
}
