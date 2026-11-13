//go:build noweb

package main

import (
	"fmt"
	"os"
)

// Build sem servidor web (-tags noweb): só scripts e templates na linha de comando.

const hasWeb = false

type webState struct{}

func (in *Interp) needWeb(line int, name string) {
	fail(line, "'%s' não está incluído neste build (compilado com -tags noweb)", name)
}

func serveCmd(args []string) int {
	fmt.Fprintln(os.Stderr, "djo serve não está incluído neste build (compilado com -tags noweb)")
	return 1
}

func init() {
	names := map[string][]string{
		"Http":    {"method", "path", "query", "param", "args", "form", "header", "setheader", "status", "redirect", "cookie", "setcookie", "body", "json", "file", "upload", "ip"},
		"Route":   {"get", "post", "put", "patch", "delete", "static", "site"},
		"Session": {"get", "set", "del", "clear", "destroy", "renew", "token", "verify"},
	}
	for ns, list := range names {
		for _, n := range list {
			name := ns + "." + n
			nativeNS(ns)[n] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
				in.needWeb(line, name)
				return nil
			}}
		}
	}
}
