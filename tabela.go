//go:build !noweb

package main

import (
	"strings"
)

// ?tabela: uma lista de objetos como CSV (RFC 4180) em vez de JSON. O cabeçalho aparece
// uma vez, em vez de repetir o nome de cada campo em cada item, o que poupa tokens em
// listas longas. Só vale para rotas com "returns": ["Tipo"].
//
// As colunas seguem a ordem do tipo; campos de structs aninhadas viram colunas com
// ponto (autor.nome); Arr e Map viram uma célula com JSON compacto; nil fica vazio.

// colunas lista os caminhos das colunas. s == nil quer dizer "todos os campos".
func (n *tnode) colunas(s sel, prefix string) []string {
	var out []string
	for _, k := range n.order {
		sub := sel(nil)
		if s != nil {
			var want bool
			if sub, want = s[k]; !want {
				continue
			}
		}
		f := n.fields[k]
		if f.kind == "obj" && !f.open {
			out = append(out, f.colunas(sub, prefix+k+".")...)
		} else {
			out = append(out, prefix+k)
		}
	}
	return out
}

func celula(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case *Array, *Map:
		return jsonEncode(x, "", 0)
	}
	return jsonEncode(v, "", 0)
}

func csvQuote(s string) string {
	if !strings.ContainsAny(s, ",\"\r\n") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func pegar(v any, path string) any {
	for _, part := range strings.Split(path, ".") {
		m, ok := v.(*Map)
		if !ok {
			return nil
		}
		v, _ = m.get(part)
	}
	return v
}

// tabela monta o CSV de uma lista já decodificada.
func tabela(a *Array, n *tnode, s sel) string {
	cols := n.colunas(s, "")
	var sb strings.Builder
	for i, c := range cols {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(csvQuote(c))
	}
	sb.WriteString("\r\n")
	for _, it := range a.Items {
		for i, c := range cols {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(csvQuote(celula(pegar(it, c))))
		}
		sb.WriteString("\r\n")
	}
	return sb.String()
}
