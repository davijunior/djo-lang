//go:build !nodb

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// Bancos de rede (PostgreSQL e MySQL/MariaDB): o mesmo Db.open, com um endereço no lugar do arquivo.
//
//	Db.open:"postgres://app:${DB_SENHA}@localhost:5432/loja?sslmode=require"
//	Db.open:"mysql://app:${DB_SENHA}@localhost:3306/loja?tls=true"
//
// O SQL continua com "?" nos dois (no PostgreSQL ele vira $1, $2... aqui dentro).

const (
	kindSQLite   = "sqlite"
	kindPostgres = "postgres"
	kindMySQL    = "mysql"
)

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// remoteScheme diz se o argumento do Db.open é um endereço de banco de rede.
func remoteScheme(s string) (kind string, ok bool) {
	i := strings.Index(s, "://")
	if i < 0 {
		return "", false
	}
	switch strings.ToLower(s[:i]) {
	case "postgres", "postgresql":
		return kindPostgres, true
	case "mysql", "mariadb":
		return kindMySQL, true
	}
	return "", false
}

// expandEnv troca ${VAR} pelo valor da variável de ambiente, para a senha não ficar no código.
// Dentro de um endereço o valor é escapado (uma senha com @ ou / não quebra a URL); se o
// texto inteiro é só ${VAR}, o valor entra como está (a variável já traz o endereço completo).
func expandEnv(s string) (string, error) {
	var missing []string
	whole := envRef.MatchString(s) && envRef.FindString(s) == s
	out := envRef.ReplaceAllStringFunc(s, func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok || v == "" {
			missing = append(missing, name)
			return ""
		}
		if whole {
			return v
		}
		return url.PathEscape(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("a variável de ambiente %s não está definida", strings.Join(missing, ", "))
	}
	return out, nil
}

// redact tira a senha de um texto de erro.
func redact(msg, rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.User != nil {
		if p, ok := u.User.Password(); ok && p != "" {
			msg = strings.ReplaceAll(msg, p, "***")
			msg = strings.ReplaceAll(msg, url.PathEscape(p), "***")
		}
	}
	return msg
}

// remoteDSN converte o endereço para o que o driver espera.
func remoteDSN(kind, raw string) (driver, dsn string, err error) {
	switch kind {
	case kindPostgres:
		if !hasPG {
			return "", "", fmt.Errorf("PostgreSQL não está incluído neste build (compilado com -tags nopg)")
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", "", fmt.Errorf("endereço inválido; use postgres://usuario:senha@servidor:5432/banco")
		}
		u.Scheme = "postgres"
		return "postgres", u.String(), nil
	default:
		dsn, err := mysqlDSN(raw)
		return "mysql", dsn, err
	}
}

// openRemote abre (ou reaproveita) o pool de conexões de um banco de rede.
func openRemote(kind, raw string) (*sql.DB, error) {
	full, err := expandEnv(raw)
	if err != nil {
		return nil, err
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db, ok := dbConns[full]; ok {
		return db, nil
	}
	driver, dsn, err := remoteDSN(kind, full)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("%s", redact(err.Error(), full))
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryLimit)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("não consegui conectar: %s", redact(err.Error(), full))
	}
	dbConns[full] = db
	return db, nil
}

// pgPlaceholders troca cada "?" por $1, $2... no SQL do PostgreSQL. Ignora o que está
// dentro de texto '...', identificador "...", comentário (-- e /* */) e $$...$$.
// "??" vira um "?" de verdade (o operador ? do jsonb).
func pgPlaceholders(q string) string {
	if !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(q) {
				if q[j] == c {
					if j+1 < len(q) && q[j+1] == c { // '' dentro do texto
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(q) {
				j = len(q) - 1
			}
			b.WriteString(q[i : j+1])
			i = j + 1
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			j := strings.IndexByte(q[i:], '\n')
			if j < 0 {
				j = len(q) - i
			}
			b.WriteString(q[i : i+j])
			i += j
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			j := strings.Index(q[i+2:], "*/")
			if j < 0 {
				b.WriteString(q[i:])
				i = len(q)
			} else {
				b.WriteString(q[i : i+2+j+2])
				i += 2 + j + 2
			}
		case c == '$':
			j := i + 1
			for j < len(q) && (q[j] == '_' || q[j] >= 'a' && q[j] <= 'z' || q[j] >= 'A' && q[j] <= 'Z' || (j > i+1 && q[j] >= '0' && q[j] <= '9')) {
				j++
			}
			if j < len(q) && q[j] == '$' { // $tag$ ... $tag$
				tag := q[i : j+1]
				end := strings.Index(q[j+1:], tag)
				if end >= 0 {
					b.WriteString(q[i : j+1+end+len(tag)])
					i = j + 1 + end + len(tag)
					continue
				}
			}
			b.WriteByte(c)
			i++
		case c == '?':
			if i+1 < len(q) && q[i+1] == '?' {
				b.WriteByte('?')
				i += 2
				continue
			}
			n++
			fmt.Fprintf(&b, "$%d", n)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}
