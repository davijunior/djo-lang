//go:build !nodb && nomysql

package main

import "fmt"

const hasMySQL = false

func mysqlDSN(raw string) (string, error) {
	return "", fmt.Errorf("MySQL não está incluído neste build (compilado com -tags nomysql)")
}
