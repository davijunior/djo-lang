//go:build !nodb && !noweb

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func runPromiseDB(t *testing.T, src string) (string, error) {
	t.Helper()
	root := t.TempDir()
	var out bytes.Buffer
	in := NewInterp(&out)
	in.root = root
	in.dbRoot = root
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	err = in.Run(prog)
	_ = filepath.Join
	return out.String(), err
}

const dbSetup = `Db.open:"p.db"
Db.exec:"CREATE TABLE IF NOT EXISTS c(id INTEGER, v INTEGER)"
Db.exec:"DELETE FROM c"
Db.exec:"INSERT INTO c VALUES (1, 100)"
`

func TestPromiseDbBeginDentroEDesfeito(t *testing.T) {
	out, err := runPromiseDB(t, dbSetup+`
uniq promise{
    Db.begin:
    Db.exec:"UPDATE c SET v = 0 WHERE id = 1"
    reject:"falhou":"no meio"
}error{
    [e]
    print "erro: " + e["msg"]
}
print (Db.one:"SELECT v FROM c WHERE id = 1")["v"]
Db.begin:
Db.exec:"UPDATE c SET v = 5 WHERE id = 1"
Db.commit:
print (Db.one:"SELECT v FROM c WHERE id = 1")["v"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "erro: no meio\n100\n5\n" {
		t.Fatalf("saída %q", out)
	}
}

func TestPromiseDbBeginDentroEConfirmado(t *testing.T) {
	out, err := runPromiseDB(t, dbSetup+`
uniq promise{
    Db.begin:
    Db.exec:"UPDATE c SET v = 7 WHERE id = 1"
    Db.commit:
}success{
    print "ok"
}
print (Db.one:"SELECT v FROM c WHERE id = 1")["v"]
`)
	if err != nil || out != "ok\n7\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestPromiseDbTransacaoDeForaFicaAberta(t *testing.T) {
	out, err := runPromiseDB(t, dbSetup+`
Db.begin:
Db.exec:"UPDATE c SET v = 1 WHERE id = 1"
uniq promise{
    var x = 1 / 0
}error{
    [e]
    print "capturado"
}
Db.rollback:
print (Db.one:"SELECT v FROM c WHERE id = 1")["v"]
`)
	if err != nil || out != "capturado\n100\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestPromiseDbRecusadoComTransacaoAbertaFora(t *testing.T) {
	out, err := runPromiseDB(t, dbSetup+`
Db.begin:
uniq promise{
    Db.exec:"UPDATE c SET v = 2"
}error{
    [e]
    print e["msg"]
}
Db.rollback:
`)
	if err != nil || !strings.Contains(out, "há uma transação aberta") {
		t.Fatalf("%q %v", out, err)
	}
}
