package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
)

// Hash: senhas e valores aleatórios.

const pwIter = 600000

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("sem aleatoriedade do sistema: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func init() {
	natives["Hash"] = map[string]builtin{
		// Hash.password:senha -> texto para guardar no banco
		"password": {1, func(in *Interp, env *Env, line int, a []any) any {
			p := argStr(a, 0, line, "Hash.password")
			salt := make([]byte, 16)
			rand.Read(salt)
			k, err := pbkdf2.Key(sha256.New, p, salt, pwIter, 32)
			if err != nil {
				fail(line, "Hash.password: %v", err)
			}
			return "pbkdf2$" + strconv.Itoa(pwIter) + "$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(k)
		}},
		// Hash.verify:senha:guardado -> Bool
		"verify": {2, func(in *Interp, env *Env, line int, a []any) any {
			p, ok1 := a[0].(string)
			stored, ok2 := a[1].(string)
			if !ok1 || !ok2 {
				return false
			}
			parts := strings.Split(stored, "$")
			if len(parts) != 4 || parts[0] != "pbkdf2" {
				return false
			}
			iter, err := strconv.Atoi(parts[1])
			if err != nil || iter < 1000 || iter > 10_000_000 {
				return false
			}
			salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
			want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
			if err1 != nil || err2 != nil || len(want) == 0 {
				return false
			}
			got, err := pbkdf2.Key(sha256.New, p, salt, iter, len(want))
			return err == nil && subtle.ConstantTimeCompare(got, want) == 1
		}},
		"sha256": {1, func(in *Interp, env *Env, line int, a []any) any {
			sum := sha256.Sum256([]byte(argStr(a, 0, line, "Hash.sha256")))
			return hex.EncodeToString(sum[:])
		}},
		// Hash.random:n -> texto aleatório seguro com n bytes (2n caracteres hex)
		"random": {1, func(in *Interp, env *Env, line int, a []any) any {
			n, ok := a[0].(int64)
			if !ok || n < 1 || n > 64 {
				fail(line, "Hash.random espera um Int de 1 a 64")
			}
			return randHex(int(n))
		}},
	}
}
