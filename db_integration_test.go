//go:build !nodb

package main

import (
	"fmt"
	"os"
	"testing"
)

// Testes contra servidores de verdade. Só rodam se você apontar um:
//
//	DJO_TEST_PG='postgres://usuario:senha@localhost:5432/teste?sslmode=disable' \
//	DJO_TEST_MYSQL='mysql://usuario:senha@localhost:3306/teste' go test -run Integracao .
//
// Eles criam e apagam uma tabela djo_teste_<pid>; use um banco de testes.

func integracao(t *testing.T, env, kind string) {
	url := os.Getenv(env)
	if url == "" {
		t.Skipf("defina %s para testar contra um servidor %s", env, kind)
	}
	tabela := fmt.Sprintf("djo_teste_%d", os.Getpid())
	create := "CREATE TABLE " + tabela + " (id INTEGER PRIMARY KEY, titulo TEXT, nota DOUBLE PRECISION, ok INTEGER)"
	if kind == kindMySQL {
		create = "CREATE TABLE " + tabela + " (id INTEGER PRIMARY KEY, titulo TEXT, nota DOUBLE, ok INTEGER)"
	}
	src := `Db.open:"` + url + `"
Db.exec:"DROP TABLE IF EXISTS ` + tabela + `"
Db.exec:"` + create + `"
var r = Db.exec:"INSERT INTO ` + tabela + ` (id, titulo, nota, ok) VALUES (?, ?, ?, ?)":[1, "Olá", 4.5, 1]
print r["changes"]
Db.exec:"INSERT INTO ` + tabela + ` (id, titulo) VALUES (?, ?)":[2, "Segundo' ); DROP TABLE ` + tabela + `; --"]
Db.exec:"INSERT INTO ` + tabela + ` (id, titulo) VALUES (?, ?)":[3, "isto é ? um 'talvez?'"]
var todos = Db.query:"SELECT * FROM ` + tabela + ` ORDER BY id"
print len:todos
print todos[0]["titulo"]
print todos[0]["nota"]
print todos[0]["id"]
print todos[1]["titulo"]
print todos[2]["titulo"]
print (Db.one:"SELECT nota FROM ` + tabela + ` WHERE id = ?":[2]) ["nota"] ?? "nada"
print Db.one:"SELECT * FROM ` + tabela + ` WHERE id = 99" ?? "nada"
print (Db.one:"SELECT titulo FROM ` + tabela + ` WHERE id = ? AND nota > ?":[1, 4]) ["titulo"]
Db.begin:
Db.exec:"DELETE FROM ` + tabela + `"
Db.rollback:
print len:(Db.query:"SELECT * FROM ` + tabela + `")
Db.begin:
Db.exec:"DELETE FROM ` + tabela + ` WHERE id = ?":[1]
Db.commit:
print len:(Db.query:"SELECT * FROM ` + tabela + `")
Db.begin:
Db.exec:"DELETE FROM ` + tabela + `"
`
	// a transação esquecida aberta no fim é desfeita: as 2 linhas continuam lá no próximo passo
	out, err := runWith(t, t.TempDir(), src)
	if err != nil {
		t.Fatalf("%v\nsaída até aqui:\n%s", err, out)
	}
	want := "1\n3\nOlá\n4.5\n1\nSegundo' ); DROP TABLE " + tabela + "; --\nisto é ? um 'talvez?'\nnada\nnada\nOlá\n3\n2\n"
	if out != want {
		t.Errorf("saída diferente:\n%q\nqueria\n%q", out, want)
	}
	// limpeza e checagem do que a página anterior deixou
	out, err = runWith(t, t.TempDir(), `Db.open:"`+url+`"
print len:(Db.query:"SELECT * FROM `+tabela+`")
Db.exec:"DROP TABLE `+tabela+`"
`)
	if err != nil || out != "2\n" {
		t.Errorf("a transação esquecida deveria ter sido desfeita: %q %v", out, err)
	}
}

func TestIntegracaoPostgres(t *testing.T) { integracao(t, "DJO_TEST_PG", kindPostgres) }
func TestIntegracaoMySQL(t *testing.T)    { integracao(t, "DJO_TEST_MYSQL", kindMySQL) }

func TestIntegracaoPostgresRetornaID(t *testing.T) {
	url := os.Getenv("DJO_TEST_PG")
	if url == "" {
		t.Skip("defina DJO_TEST_PG")
	}
	tabela := fmt.Sprintf("djo_teste_serial_%d", os.Getpid())
	out, err := runWith(t, t.TempDir(), `Db.open:"`+url+`"
Db.exec:"CREATE TABLE `+tabela+` (id SERIAL PRIMARY KEY, titulo TEXT, dados JSONB, criado TIMESTAMP DEFAULT now())"
var r = Db.exec:"INSERT INTO `+tabela+` (titulo) VALUES (?)":["a"]
print r["last_id"] ?? "sem last_id"
var n = Db.one:"INSERT INTO `+tabela+` (titulo, dados) VALUES (?, '{\"k\": 1}') RETURNING id":["b"]
print n["id"]
print (Db.one:"SELECT count(*) AS n FROM `+tabela+` WHERE dados ?? 'k'")["n"]
print (Db.one:"SELECT criado FROM `+tabela+` WHERE id = 1")["criado"] > 1000000000
Db.exec:"DROP TABLE `+tabela+`"
`)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := "sem last_id\n2\n1\ntrue\n"; out != want {
		t.Errorf("%q, queria %q", out, want)
	}
}
