//go:build !nodb

package main

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Db: banco SQLite embutido (arquivo único, sem servidor).
//
//	Db.open:"site.db"                       abre (ou cria) dentro da pasta do site
//	Db.exec:"INSERT ... VALUES (?, ?)":[a, b]   -> {changes, last_id}
//	Db.query:"SELECT ... WHERE x = ?":[v]       -> lista de mapas
//	Db.one:"SELECT ...":[v]                     -> um mapa, ou nil
//	Db.begin: / Db.commit: / Db.rollback:       transação

const hasDB = true

const (
	dbMaxRows    = 100000
	dbQueryLimit = 10 * time.Second
)

var (
	dbMu    sync.Mutex
	dbConns = map[string]*sql.DB{} // uma conexão por arquivo, compartilhada entre requisições
)

func openDB(path string) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db, ok := dbConns[path]; ok {
		return db, nil
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: path}).EscapedPath()+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite aceita um escritor por vez; uma conexão evita "database is locked"
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	dbConns[path] = db
	return db, nil
}

// dbState é o banco aberto pela página atual.
type dbState struct {
	db *sql.DB
	tx *sql.Tx
}

type dbRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (in *Interp) runner(fn string, line int) dbRunner {
	if in.dbs.tx != nil {
		return in.dbs.tx
	}
	if in.dbs.db == nil {
		fail(line, "%s: abra o banco antes com Db.open:\"arquivo.db\"", fn)
	}
	return in.dbs.db
}

// closeTx desfaz uma transação esquecida aberta (fim da página ou erro).
func (in *Interp) closeTx() {
	if in.dbs.tx != nil {
		in.dbs.tx.Rollback()
		in.dbs.tx = nil
	}
}

func dbParams(a []any, i int, fn string, line int) []any {
	if len(a) <= i {
		return nil
	}
	arr, ok := a[i].(*Array)
	if !ok {
		fail(line, "%s: os parâmetros vão numa lista, como [nome, 3], mas recebeu %s", fn, typeName(a[i]))
	}
	out := make([]any, len(arr.Items))
	for j, v := range arr.Items {
		switch x := v.(type) {
		case nil, int64, float64, string, bool:
			out[j] = x
		default:
			fail(line, "%s: parâmetro %d: %s não pode ir para o banco", fn, j+1, typeName(v))
		}
	}
	return out
}

func dbSQL(a []any, fn string, line int) string {
	if len(a) == 0 || len(a) > 2 {
		fail(line, "%s espera (sql) ou (sql, [parâmetros])", fn)
	}
	s, ok := a[0].(string)
	if !ok {
		fail(line, "%s espera o SQL como String, mas recebeu %s", fn, typeName(a[0]))
	}
	return s
}

func dbFail(fn string, line int, err error) {
	fail(line, "%s: %v", fn, err)
}

func dbValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.Unix()
	}
	return v
}

func (in *Interp) dbRows(a []any, fn string, line int, limit int) []*Map {
	q, params := dbSQL(a, fn, line), dbParams(a, 1, fn, line)
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryLimit)
	defer cancel()
	rows, err := in.runner(fn, line).QueryContext(ctx, q, params...)
	if err != nil {
		dbFail(fn, line, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []*Map
	for rows.Next() {
		if len(out) >= limit {
			if limit == dbMaxRows {
				fail(line, "%s: mais de %d linhas; use LIMIT", fn, dbMaxRows)
			}
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			dbFail(fn, line, err)
		}
		m := newMap()
		for i, c := range cols {
			m.set(c, dbValue(vals[i]))
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		dbFail(fn, line, err)
	}
	return out
}

func init() {
	natives["Db"] = map[string]builtin{
		"open": {1, func(in *Interp, env *Env, line int, a []any) any {
			name := argStr(a, 0, line, "Db.open")
			if !strings.HasSuffix(name, ".db") && !strings.HasSuffix(name, ".sqlite") {
				fail(line, "Db.open: o arquivo precisa terminar em .db ou .sqlite")
			}
			if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
				fail(line, "Db.open: o caminho precisa ficar dentro da pasta do site")
			}
			path, err := safeJoinNew(in.root, name)
			if err != nil {
				fail(line, "Db.open '%s': %v", name, err)
			}
			db, err := openDB(path)
			if err != nil {
				dbFail("Db.open", line, err)
			}
			in.closeTx()
			in.dbs.db = db
			return nil
		}},
		"exec": {-1, func(in *Interp, env *Env, line int, a []any) any {
			q, params := dbSQL(a, "Db.exec", line), dbParams(a, 1, "Db.exec", line)
			ctx, cancel := context.WithTimeout(context.Background(), dbQueryLimit)
			defer cancel()
			res, err := in.runner("Db.exec", line).ExecContext(ctx, q, params...)
			if err != nil {
				dbFail("Db.exec", line, err)
			}
			m := newMap()
			n, _ := res.RowsAffected()
			id, _ := res.LastInsertId()
			m.set("changes", n)
			m.set("last_id", id)
			return m
		}},
		"query": {-1, func(in *Interp, env *Env, line int, a []any) any {
			rows := in.dbRows(a, "Db.query", line, dbMaxRows)
			arr := &Array{Items: make([]any, len(rows))}
			for i, r := range rows {
				arr.Items[i] = r
			}
			return arr
		}},
		"one": {-1, func(in *Interp, env *Env, line int, a []any) any {
			rows := in.dbRows(a, "Db.one", line, 1)
			if len(rows) == 0 {
				return nil
			}
			return rows[0]
		}},
		"begin": {0, func(in *Interp, env *Env, line int, a []any) any {
			if in.dbs.db == nil {
				fail(line, "Db.begin: abra o banco antes com Db.open:\"arquivo.db\"")
			}
			if in.dbs.tx != nil {
				fail(line, "Db.begin: já existe uma transação aberta")
			}
			tx, err := in.dbs.db.BeginTx(context.Background(), nil)
			if err != nil {
				dbFail("Db.begin", line, err)
			}
			in.dbs.tx = tx
			return nil
		}},
		"commit": {0, func(in *Interp, env *Env, line int, a []any) any {
			if in.dbs.tx == nil {
				fail(line, "Db.commit: não há transação aberta")
			}
			tx := in.dbs.tx
			in.dbs.tx = nil
			if err := tx.Commit(); err != nil {
				dbFail("Db.commit", line, err)
			}
			return nil
		}},
		"rollback": {0, func(in *Interp, env *Env, line int, a []any) any {
			if in.dbs.tx == nil {
				fail(line, "Db.rollback: não há transação aberta")
			}
			tx := in.dbs.tx
			in.dbs.tx = nil
			tx.Rollback()
			return nil
		}},
	}
}
