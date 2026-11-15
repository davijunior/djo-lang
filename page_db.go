//go:build !nodb

package main

import (
	"strconv"
	"strings"
)

func init() {
	natives["Db"]["page"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		if len(a) < 1 || len(a) > 3 {
			fail(line, "Db.page espera (sql), (sql, [parâmetros]) ou (sql, [parâmetros], opções)")
		}
		sql, ok := a[0].(string)
		if !ok {
			fail(line, "Db.page espera o SQL como String, mas recebeu %s", typeName(a[0]))
		}
		sql = strings.TrimRight(strings.TrimSpace(sql), "; \t\r\n")
		words := sqlTopWords(sql)
		if len(words) == 0 || (words[0] != "select" && words[0] != "with") {
			fail(line, "Db.page só lê: o comando precisa começar com SELECT")
		}
		hasOrder := false
		for i, w := range words {
			switch {
			case w == "limit" || w == "offset":
				fail(line, "Db.page acrescenta LIMIT e OFFSET sozinho; tire o %s do seu SQL", strings.ToUpper(w))
			case w == "order" && i+1 < len(words) && words[i+1] == "by":
				hasOrder = true
			}
		}
		if !hasOrder {
			fail(line, "Db.page precisa de ORDER BY: sem uma ordem fixa, a mesma linha pode aparecer em duas páginas")
		}

		def, max, force, total := int64(pageDefault), int64(pageMax), int64(0), false
		if len(a) == 3 {
			opts, ok := a[2].(*Map)
			if !ok {
				fail(line, "Db.page: as opções vão num mapa, como {\"limite\": 20, \"total\": true}")
			}
			for i, k := range opts.Keys {
				v := opts.Vals[i]
				switch k {
				case "limite":
					def = pageInt(v, "Db.page: \"limite\"", line)
				case "max":
					max = pageInt(v, "Db.page: \"max\"", line)
				case "pagina":
					force = pageInt2(v, "Db.page: \"pagina\"", line)
				case "total":
					b, ok := v.(bool)
					if !ok {
						fail(line, "Db.page: \"total\" precisa ser Bool")
					}
					total = b
				default:
					fail(line, "Db.page: a opção %v não existe (use \"limite\", \"max\", \"pagina\" e \"total\")", k)
				}
			}
		}
		if def > max {
			if len(a) == 3 && !optsHas(a[2], "max") {
				max = def // só mudou o limite padrão: o teto acompanha
			} else {
				fail(line, "Db.page: o \"limite\" (%d) não pode passar do \"max\" (%d)", def, max)
			}
		}
		p := in.pageRead(def, max, force)
		pv, _ := p.get("pagina")
		lv, _ := p.get("limite")
		ov, _ := p.get("deslocamento")
		pagina, limite, off := pv.(int64), lv.(int64), ov.(int64)

		var params []any
		if len(a) >= 2 {
			params = []any{a[1]}
		}
		q := append([]any{sql + " LIMIT " + strconv.FormatInt(limite+1, 10) + " OFFSET " + strconv.FormatInt(off, 10)}, params...)
		rows := in.dbRows(q, "Db.page", line, int(limite)+1)
		more := int64(len(rows)) > limite
		if more {
			rows = rows[:limite]
		}
		items := &Array{Items: make([]any, len(rows))}
		for i, r := range rows {
			items.Items[i] = r
		}

		res := newMap()
		res.set("itens", items)
		res.set("pagina", pagina)
		res.set("limite", limite)
		if more {
			res.set("proxima", pagina+1)
		} else {
			res.set("proxima", nil)
		}
		if pagina > 1 {
			res.set("anterior", pagina-1)
		} else {
			res.set("anterior", nil)
		}
		if total {
			cq := append([]any{"SELECT COUNT(*) AS n FROM (" + sql + ") AS djo_pagina"}, params...)
			var n int64
			if cr := in.dbRows(cq, "Db.page", line, 1); len(cr) == 1 {
				v, _ := cr[0].get("n")
				switch x := v.(type) {
				case int64:
					n = x
				case string: // alguns drivers devolvem o COUNT como texto
					n, _ = strconv.ParseInt(x, 10, 64)
				}
			}
			res.set("total", n)
			res.set("paginas", (n+limite-1)/limite)
		}
		return res
	}}
}

func optsHas(v any, key string) bool {
	m, ok := v.(*Map)
	if !ok {
		return false
	}
	_, has := m.get(key)
	return has
}

// pageInt2 aceita páginas até pageLast.
func pageInt2(v any, what string, line int) int64 {
	n, ok := v.(int64)
	if !ok || n < 1 || n > pageLast {
		fail(line, "%s precisa ser um Int de 1 a %d", what, pageLast)
	}
	return n
}
