//go:build !nodb

package main

import (
	"strings"
	"testing"
)

// O ${VAR} do Db.open não pode ler a chave do Crypto.
func TestCryptoChaveNaoVazaPeloDbOpen(t *testing.T) {
	t.Setenv(cryptoKeyVar, testKeyA)
	// Db.open com ${DJO_CRYPTO_KEY}
	if _, err := expandEnv("postgres://u:${" + cryptoKeyVar + "}@h/db"); err == nil || strings.Contains(err.Error(), testKeyA) {
		t.Errorf("expandEnv: %v", err)
	}
	if v, err := expandEnv("${" + cryptoKeyVar + "}"); err == nil || v != "" {
		t.Errorf("expandEnv inteiro devolveu %q", v)
	}
}
