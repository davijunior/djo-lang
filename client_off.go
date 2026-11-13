//go:build nonet

package main

const hasNet = false

func init() {
	ns := nativeNS("Http")
	for _, m := range []string{"get", "post", "put", "delete"} {
		name := "Http." + m
		ns[m] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
			fail(line, "%s não está incluído neste build (compilado com -tags nonet)", name)
			return nil
		}}
	}
}
