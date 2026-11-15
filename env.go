package main

import (
	"os"
	"regexp"
)

// Env.get:"NOME"            o valor da variável de ambiente, ou nil se não existe
// Env.get:"NOME":"padrão"   o mesmo, com um valor para quando ela não existe
//
// É o lugar certo para segredos (senhas, tokens): ficam fora do código e do executável.
// Uma variável que existe e está vazia devolve "" (e não o padrão).

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func init() {
	nativeNS("Env")["get"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		if len(a) < 1 || len(a) > 2 {
			fail(line, "Env.get espera (nome) ou (nome, padrão)")
		}
		name, ok := a[0].(string)
		if !ok || !envName.MatchString(name) {
			fail(line, "Env.get: o nome da variável precisa ser um texto como \"DB_SENHA\" (letras, números e _)")
		}
		if v, found := os.LookupEnv(name); found {
			return v
		}
		if len(a) == 2 {
			def, ok := a[1].(string)
			if !ok {
				fail(line, "Env.get: o valor padrão precisa ser String, mas recebeu %s", typeName(a[1]))
			}
			return def
		}
		return nil
	}}
}
