package main

import (
	"strconv"
	"strings"
)

// Paginação.
//
//	var p = Page.read:                 {"pagina": 2, "limite": 20, "deslocamento": 20}, de ?pagina= e ?limite=
//	var p = Page.read:20:100           limite padrão 20, no máximo 100
//	var r = Db.page:"SELECT ... ORDER BY id":[args]
//	r["itens"]  r["pagina"]  r["limite"]  r["proxima"]  r["anterior"]   (e "total", "paginas" com {"total": true})
//
// Db.page acrescenta LIMIT/OFFSET sozinho (com inteiros já validados) e busca uma linha a mais para
// saber se há próxima página, sem contar a tabela inteira (o que custa caro em tabela grande).

const (
	pageDefault = 20
	pageMax     = 100
	pageHardMax = 1000    // nem com "max" maior que isso
	pageLast    = 1000000 // maior número de página aceito
)

func init() {
	nativeNS("Page")["read"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		def, max := int64(pageDefault), int64(pageMax)
		if len(a) > 2 {
			fail(line, "Page.read espera () ou (limite padrão) ou (limite padrão, máximo)")
		}
		if len(a) >= 1 {
			def = pageInt(a[0], "Page.read: o limite padrão", line)
		}
		if len(a) == 2 {
			max = pageInt(a[1], "Page.read: o máximo", line)
		}
		if def > max {
			fail(line, "Page.read: o limite padrão (%d) não pode passar do máximo (%d)", def, max)
		}
		return in.pageRead(def, max, 0)
	}}
}

func pageInt(v any, what string, line int) int64 {
	n, ok := v.(int64)
	if !ok || n < 1 || n > pageHardMax {
		fail(line, "%s precisa ser um Int de 1 a %d", what, pageHardMax)
	}
	return n
}

// pageRead lê ?pagina= e ?limite= da requisição e os mantém dentro dos limites: valor ausente,
// inválido ou fora da faixa nunca dá erro, volta para um valor seguro. force > 0 fixa a página.
func (in *Interp) pageRead(def, max, force int64) *Map {
	limit := def
	if s := in.reqQuery("limite"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 1 {
			limit = n
		}
	}
	if limit > max {
		limit = max
	}
	page := int64(1)
	if force > 0 {
		page = force
	} else if s := in.reqQuery("pagina"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 1 {
			page = n
		}
	}
	if page > pageLast {
		page = pageLast
	}
	m := newMap()
	m.set("pagina", page)
	m.set("limite", limit)
	m.set("deslocamento", (page-1)*limit)
	return m
}

// sqlTopWords devolve as palavras do SQL que estão fora de textos, comentários e parênteses,
// em minúsculas. Serve para achar o ORDER BY e o LIMIT do próprio comando, e não de uma subconsulta.
func sqlTopWords(s string) []string {
	var words []string
	depth := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < len(s) {
				if s[j] == c {
					if j+1 < len(s) && s[j+1] == c { // aspas duplicadas dentro do texto
						j += 2
						continue
					}
					break
				}
				j++
			}
			i = j + 1
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return words
			}
			i += j + 4
		case c == '(':
			depth++
			i++
		case c == ')':
			depth--
			i++
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			if depth == 0 {
				words = append(words, strings.ToLower(s[i:j]))
			}
			i = j
		default:
			i++
		}
	}
	return words
}
