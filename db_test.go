//go:build !nodb && !noweb

package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDb(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "d.djo"), []byte(`Db.open:"site.db"
Db.exec:"CREATE TABLE IF NOT EXISTS posts (id INTEGER PRIMARY KEY, titulo TEXT, nota REAL, ok INTEGER)"
var r = Db.exec:"INSERT INTO posts (titulo, nota, ok) VALUES (?, ?, ?)":["Olá", 4.5, true]
print r["last_id"]
Db.exec:"INSERT INTO posts (titulo) VALUES (?)":["Segundo' ); DROP TABLE posts; --"]
var todos = Db.query:"SELECT * FROM posts ORDER BY id"
print len:todos
print todos[0]
print (Db.one:"SELECT titulo FROM posts WHERE id = ?":[2])["titulo"]
print Db.one:"SELECT * FROM posts WHERE id = 99" ?? "nada"
Db.begin:
Db.exec:"DELETE FROM posts"
Db.rollback:
print len:(Db.query:"SELECT * FROM posts")
Db.begin:
Db.exec:"DELETE FROM posts WHERE id = 1"
Db.commit:
print len:(Db.query:"SELECT * FROM posts")
Db.begin:
`), 0o644)
	os.WriteFile(filepath.Join(dir, "e.djo"), []byte("print Db.query:\"SELECT 1\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "f.djo"), []byte("Db.open:\"../fora.db\"\n"), 0o644)
	srv, _ := NewServer(dir, true)
	get := func(u string) (int, string) {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("GET", u, nil))
		return rec.Code, rec.Body.String()
	}
	c, b := get("/d")
	want := "1\n2\n{\"id\": 1, \"titulo\": \"Olá\", \"nota\": 4.5, \"ok\": 1}\nSegundo' ); DROP TABLE posts; --\nnada\n2\n1\n"
	if c != 200 || b != want {
		t.Fatalf("%d %q", c, b)
	}
	if c, b := get("/d"); c != 200 { // transação esquecida não trava a próxima requisição
		t.Fatalf("segunda execução: %d %q", c, b)
	}
	if c, _ := get("/e"); c != 500 {
		t.Fatalf("sem Db.open deveria dar erro, veio %d", c)
	}
	if c, _ := get("/f"); c != 500 {
		t.Fatalf("caminho fora do site deveria dar erro, veio %d", c)
	}
	if c, _ := get("/site.db"); c != 404 {
		t.Fatalf("o arquivo do banco não pode ser servido, veio %d", c)
	}
}
