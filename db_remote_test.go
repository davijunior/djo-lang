//go:build !nodb

package main

import (
	"os"
	"strings"
	"testing"
)

func TestPgPlaceholders(t *testing.T) {
	for in, want := range map[string]string{
		"SELECT 1":                                        "SELECT 1",
		"SELECT * FROM t WHERE a = ? AND b > ?":           "SELECT * FROM t WHERE a = $1 AND b > $2",
		"SELECT '?' , ?":                                  "SELECT '?' , $1",
		"SELECT 'it''s ?', ?":                             "SELECT 'it''s ?', $1",
		`SELECT "col?" FROM t WHERE x = ?`:                `SELECT "col?" FROM t WHERE x = $1`,
		"SELECT ? -- e se for ?\nWHERE y = ?":             "SELECT $1 -- e se for ?\nWHERE y = $2",
		"SELECT /* ? */ ?":                                "SELECT /* ? */ $1",
		"SELECT $$ ? $$, ?":                               "SELECT $$ ? $$, $1",
		"SELECT $f$ ? $f$, ?":                             "SELECT $f$ ? $f$, $1",
		"SELECT data ?? 'k' FROM t WHERE id = ?":          "SELECT data ? 'k' FROM t WHERE id = $1",
		"UPDATE t SET a = $1":                             "UPDATE t SET a = $1",
		"SELECT 'aberto ?":                                "SELECT 'aberto ?",
		"INSERT INTO t (a, b) VALUES (?, ?) RETURNING id": "INSERT INTO t (a, b) VALUES ($1, $2) RETURNING id",
		"SELECT 1 /* sem fim ?":                           "SELECT 1 /* sem fim ?",
	} {
		if got := pgPlaceholders(in); got != want {
			t.Errorf("pgPlaceholders(%q)\n  = %q\n  queria %q", in, got, want)
		}
	}
}

func TestRemoteScheme(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://u@h/db": kindPostgres, "postgresql://u@h/db": kindPostgres, "POSTGRES://u@h/db": kindPostgres,
		"mysql://u@h/db": kindMySQL, "mariadb://u@h/db": kindMySQL,
		"site.db": "", "http://x/y.db": "", "ftp://x": "",
	} {
		kind, ok := remoteScheme(in)
		if kind != want || ok != (want != "") {
			t.Errorf("%q: %q %v", in, kind, ok)
		}
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("DJO_T_SENHA", "p@ss/w:rd")
	t.Setenv("DJO_T_URL", "postgres://u:x@h/db?sslmode=disable")
	got, err := expandEnv("postgres://app:${DJO_T_SENHA}@h/db")
	if err != nil || got != "postgres://app:p@ss%2Fw:rd@h/db" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := expandEnv("${DJO_T_URL}"); err != nil || got != "postgres://u:x@h/db?sslmode=disable" {
		t.Fatalf("variável com o endereço inteiro: %q %v", got, err)
	}
	if _, err := expandEnv("postgres://app:${DJO_T_NAO_EXISTE}@h/db"); err == nil || !strings.Contains(err.Error(), "DJO_T_NAO_EXISTE") {
		t.Fatalf("variável ausente: %v", err)
	}
	if got, _ := expandEnv("mysql://u@h/db"); got != "mysql://u@h/db" {
		t.Fatalf("sem variável: %q", got)
	}
}

func TestRedact(t *testing.T) {
	msg := redact(`dial tcp: senha "s3gr#do" recusada (s3gr%23do)`, "postgres://u:s3gr%23do@h/db")
	if strings.Contains(msg, "s3gr") {
		t.Fatalf("a senha vazou: %s", msg)
	}
}

func TestRemoteDSN(t *testing.T) {
	if hasMySQL {
		dsn, err := mysqlDSN("mysql://app:s%40nha@db.local/loja?tls=skip-verify&charset=utf8mb4")
		if err != nil || !strings.HasPrefix(dsn, "app:s@nha@tcp(db.local:3306)/loja?") || !strings.Contains(dsn, "parseTime=true") || !strings.Contains(dsn, "tls=skip-verify") || !strings.Contains(dsn, "charset=utf8mb4") {
			t.Fatalf("%q %v", dsn, err)
		}
		if _, err := mysqlDSN("mysql://u@h/db?multiStatements=true"); err == nil {
			t.Fatal("multiStatements não pode ser aceito")
		}
		if _, err := mysqlDSN("mysql:///db"); err == nil {
			t.Fatal("sem servidor deveria falhar")
		}
	}
	if hasPG {
		if _, dsn, err := remoteDSN(kindPostgres, "postgresql://app:x@h:5433/loja?sslmode=disable"); err != nil || !strings.HasPrefix(dsn, "postgres://app:x@h:5433/loja") {
			t.Fatalf("%q %v", dsn, err)
		}
		if _, _, err := remoteDSN(kindPostgres, "postgres:///db"); err == nil {
			t.Fatal("sem servidor deveria falhar")
		}
	}
}

func TestDbOpenRemotoSemServidorNaoVazaSenha(t *testing.T) {
	root := t.TempDir()
	for _, u := range []string{"postgres://app:SegredoGrande@127.0.0.1:1/loja?sslmode=disable&connect_timeout=2", "mysql://app:SegredoGrande@127.0.0.1:1/loja"} {
		_, err := runWith(t, root, "Db.open:\""+u+"\"\n")
		if err == nil {
			t.Fatalf("%s: deveria falhar sem servidor", u)
		}
		if strings.Contains(err.Error(), "SegredoGrande") {
			t.Fatalf("a senha vazou no erro: %v", err)
		}
	}
	os.Unsetenv("DJO_T_AUSENTE")
	if _, err := runWith(t, root, "Db.open:\"postgres://app:${DJO_T_AUSENTE}@127.0.0.1:1/x\"\n"); err == nil || !strings.Contains(err.Error(), "DJO_T_AUSENTE") {
		t.Fatalf("variável ausente: %v", err)
	}
}
