//go:build !nodb && !nomysql

package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const hasMySQL = true

// mysqlDSN converte mysql://usuario:senha@servidor:3306/banco?tls=true para o formato do driver.
func mysqlDSN(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("endereço inválido; use mysql://usuario:senha@servidor:3306/banco")
	}
	cfg := mysql.NewConfig()
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
	}
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	if u.Port() == "" {
		cfg.Addr = u.Host + ":3306"
	}
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Params = map[string]string{}
	for k, v := range u.Query() {
		if len(v) == 0 {
			continue
		}
		switch k {
		case "tls":
			cfg.TLSConfig = v[0]
		case "multiStatements", "allowAllFiles", "allowCleartextPasswords", "allowOldPasswords", "clientFoundRows":
			return "", fmt.Errorf("o parâmetro %s não é aceito no endereço", k)
		default:
			cfg.Params[k] = v[0]
		}
	}
	return cfg.FormatDSN(), nil
}
