//go:build nonet

package main

const hasMail = false

func init() {
	nativeNS("Mail")["send"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		fail(line, "Mail não existe neste perfil (compilado com -tags nonet)")
		return nil
	}}
}
