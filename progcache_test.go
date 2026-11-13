//go:build !noweb

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCacheConcorrenteComHeranca(t *testing.T) {
	ts := site(t, map[string]string{
		"core/m.djo": `struct Base
    nome[String]
    f diz
        return "base " + self.nome

struct Filho sonof Base
    extra[Int] = 7
    f diz
        return "filho " + super.diz: + " " + str:self.extra

interface Fala
    f diz
`,
		"index.djo": `var a = Base:"x"
var b = Filho:"y"
print a.diz: + "|" + b.diz:`,
	})
	var wg sync.WaitGroup
	errs := make(chan string, 400)
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_, body, _ := get(t, ts, "/")
				if body != "base x|filho base y 7\n" {
					errs <- body
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("resposta errada sob concorrência: %q", e)
	}
}

func TestCacheNotaEdicao(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "p.djo")
	write := func(s string, mod time.Time) {
		os.WriteFile(file, []byte(s), 0o644)
		os.Chtimes(file, mod, mod)
	}
	now := time.Now()
	write(`print "um"`, now)
	run := func() string {
		prog, err := loadProgram(file)
		if err != nil {
			t.Fatal(err)
		}
		var sb fmtBuilder
		if err := NewInterp(&sb).Run(prog); err != nil {
			t.Fatal(err)
		}
		return sb.String()
	}
	if got := run(); got != "um\n" {
		t.Fatalf("got %q", got)
	}
	write(`print "dois"`, now.Add(2*time.Second)) // mesmo tamanho, horário novo
	if got := run(); got != "dois\n" {
		t.Errorf("depois de editar: %q", got)
	}
	write(`print "tres!"`, now.Add(2*time.Second)) // horário igual, tamanho novo
	if got := run(); got != "tres!\n" {
		t.Errorf("tamanho mudou: %q", got)
	}
	write(`print `, now.Add(4*time.Second)) // erro de sintaxe não fica no cache
	if _, err := loadProgram(file); err == nil {
		t.Errorf("esperava erro de sintaxe")
	}
	write(`print "ok"`, now.Add(6*time.Second))
	if got := run(); got != "ok\n" {
		t.Errorf("depois de corrigir: %q", got)
	}
	_ = fmt.Sprint
}

type fmtBuilder struct{ b []byte }

func (f *fmtBuilder) Write(p []byte) (int, error) { f.b = append(f.b, p...); return len(p), nil }
func (f *fmtBuilder) String() string              { return string(f.b) }
