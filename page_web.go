//go:build !noweb

package main

import (
	"net/url"
	"strconv"
	"strings"
)

// Page.headers:resultado  põe na resposta o que uma API paginada informa pelos cabeçalhos:
//
//	Link: </tarefas?pagina=3&limite=20>; rel="next", </tarefas?pagina=1&limite=20>; rel="prev"
//	X-Total-Count: 134            (só se o resultado tem "total")
//
// Assim o corpo continua sendo só a lista (o que mantém ?campos= e ?tabela funcionando), e quem
// chama sabe se há mais páginas sem ter de ler o corpo.
func init() {
	nativeNS("Page")["headers"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		in.needWeb(line, "Page.headers")
		m, ok := a[0].(*Map)
		if !ok {
			fail(line, "Page.headers espera o resultado de Db.page")
		}
		get := func(k string) (int64, bool) {
			v, _ := m.get(k)
			n, ok := v.(int64)
			return n, ok
		}
		limit, ok := get("limite")
		if !ok {
			fail(line, "Page.headers espera o resultado de Db.page")
		}
		link := func(page int64, rel string) string {
			q := in.req.URL.Query()
			q.Set("pagina", strconv.FormatInt(page, 10))
			q.Set("limite", strconv.FormatInt(limit, 10))
			u := url.URL{Path: in.req.URL.Path, RawQuery: q.Encode()}
			return "<" + u.String() + `>; rel="` + rel + `"`
		}
		var links []string
		if n, ok := get("proxima"); ok {
			links = append(links, link(n, "next"))
		}
		if n, ok := get("anterior"); ok {
			links = append(links, link(n, "prev"))
		}
		if len(links) > 0 {
			in.resp.header.Add("Link", strings.Join(links, ", "))
		}
		if n, ok := get("total"); ok {
			in.resp.header.Set("X-Total-Count", strconv.FormatInt(n, 10))
		}
		return nil
	}}
}
