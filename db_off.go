//go:build nodb

package main

// Build sem banco de dados (-tags nodb): o Db existe, mas avisa que não está incluído.

const hasDB = false

type dbState struct{}

func (in *Interp) closeTx() {}

func init() {
	off := builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		fail(line, "Db não está incluído neste build (compilado com -tags nodb)")
		return nil
	}}
	natives["Db"] = map[string]builtin{}
	for _, n := range []string{"open", "exec", "query", "one", "begin", "commit", "rollback"} {
		natives["Db"][n] = off
	}
}
