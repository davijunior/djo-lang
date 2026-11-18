//go:build !nonet

package main

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestConfigureJobsHTTP(t *testing.T) {
	t.Cleanup(func() { jobHTTPLimit = jobHTTPCalls })
	cases := []struct {
		n       int
		set     bool
		env     string
		want    int
		wantErr string
	}{
		{20, false, "", 20, ""},
		{20, false, "50", 50, ""},
		{20, false, " 50 ", 50, ""},
		{100, true, "50", 100, ""}, // o flag vence o ambiente
		{1, true, "", 1, ""},
		{200, true, "", 200, ""},
		{0, true, "", 20, "-jobs-http vai de 1 a 200"},
		{201, true, "", 20, "-jobs-http vai de 1 a 200"},
		{-3, true, "", 20, "-jobs-http vai de 1 a 200"},
		{20, false, "abc", 20, "DJO_JOBS_HTTP precisa de um número"},
		{20, false, "0", 20, "DJO_JOBS_HTTP vai de 1 a 200"},
		{20, false, "9999", 20, "DJO_JOBS_HTTP vai de 1 a 200"},
	}
	for _, c := range cases {
		jobHTTPLimit = jobHTTPCalls
		err := configureJobsHTTP(c.n, c.set, c.env)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%+v: erro %v", c, err)
			}
		} else if err != nil {
			t.Errorf("%+v: erro inesperado %v", c, err)
		}
		if jobHTTPLimit != c.want {
			t.Errorf("%+v: limite %d, esperado %d", c, jobHTTPLimit, c.want)
		}
	}
}

// O job faz tantas chamadas Http quantas o -jobs-http deixa; a seguinte é um erro fatal que o promise não pega.
func TestJobsHTTPLimiteDoJob(t *testing.T) {
	t.Cleanup(func() { jobHTTPLimit = jobHTTPCalls })
	src := `f chama
    var i = 0
    while{
        cond[i < 6]
        Http.get:"http://127.0.0.1:1/x"
        i++
    }

f chama_em_promise
    uniq promise{
        var i = 0
        while{
            cond[i < 6]
            Http.get:"http://127.0.0.1:1/x"
            i++
        }
    }error{
        print "NAO"
    }finally{
        print "NAO"
    }
`
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		limit int
		fn    string
		fail  bool
	}{
		{6, "chama", false},
		{5, "chama", true},
		{3, "chama_em_promise", true},
	} {
		jobHTTPLimit = c.limit
		var out strings.Builder
		in := newJobInterp(t.TempDir(), t.TempDir(), &out, time.Minute, nil)
		if err := in.Run(prog); err != nil {
			t.Fatal(err)
		}
		in.outCalls = 0
		err, _, _ := jobInvoke(in, c.fn, "{}")
		if c.fail {
			if err == nil || !strings.Contains(err.Error(), "no máximo") || !strings.Contains(err.Error(), "por job") || !strings.Contains(err.Error(), "-jobs-http") {
				t.Errorf("%+v: esperava o erro do limite, veio %v", c, err)
			}
			if out.String() != "" {
				t.Errorf("%+v: o error e o finally não rodam num limite fatal: %q", c, out.String())
			}
		} else if err != nil {
			t.Errorf("%+v: não devia falhar: %v", c, err)
		}
	}
}

// A página tem o limite dela (5), que o -jobs-http não muda.
func TestJobsHTTPNaoMudaALimitePagina(t *testing.T) {
	t.Cleanup(func() { jobHTTPLimit = jobHTTPCalls })
	jobHTTPLimit = 100
	in := NewInterp(io.Discard)
	in.served = true
	prog, err := Parse("var i = 0\nwhile{\n    cond[i < 6]\n    Http.get:\"http://127.0.0.1:1/x\"\n    i++\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	err = in.Run(prog)
	if err == nil || !strings.Contains(err.Error(), "no máximo 5 chamadas externas por página") {
		t.Fatalf("%v", err)
	}
}
