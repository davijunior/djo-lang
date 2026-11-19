package main

import (
	"fmt"
	"sort"
	"strings"
)

// Mensagens de erro mais úteis: códigos estáveis, "quer dizer X?" e dicas para os tropeços comuns.
// Os textos podem mudar entre versões; o código (err["code"]) é o contrato.

// failc é o fail com um código estável (aparece em err["code"] no promise e no Spec.try).
func failc(line int, code, format string, a ...any) {
	panic(&djoError{Line: line, Code: code, Msg: fmt.Sprintf(format, a...)})
}

// Códigos dos erros comuns.
const (
	codeReservedName   = "reserved_name"
	codeNestedCall     = "nested_call"
	codeNamespaceValue = "namespace_as_value"
	codeInvalidEscape  = "invalid_escape"
	codeOpTypes        = "operator_types"
	codeArity          = "wrong_arity"
	codeUnknownName    = "unknown_name"
	codeUnknownMember  = "unknown_member"
	codeNilIndex       = "nil_index"
	codeSQLNil         = "sql_nil_literal"
	codeBoosterFn      = "booster_function"
)

// editDistance: distância de edição com troca de letras vizinhas (nmoe -> nome vale 1).
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := 0; j <= len(rb); j++ {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+c)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

// didYouMean devolve " (quer dizer 'x'?)" com o nome mais parecido dos que o usuário já pode ver ("" se nenhum).
func didYouMean(name string, cands []string) string {
	sort.Strings(cands)
	n := len([]rune(name))
	if n <= 2 { // nome curto: qualquer letra solta "parece" com outra, a sugestão vira ruído
		return ""
	}
	max := 2
	if n <= 4 {
		max = 1
	}
	best, bestD := "", max+1
	for _, c := range cands {
		if c == name || c == "" {
			continue
		}
		if d := editDistance(strings.ToLower(name), strings.ToLower(c)); d < bestD {
			best, bestD = c, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (quer dizer '%s'?)", best)
}

// visibleVars lista as variáveis e funções que o código daqui enxerga.
func (in *Interp) visibleNames(env *Env) []string {
	var out []string
	for e := env; e != nil; e = e.parent {
		for k := range e.vars {
			out = append(out, k)
		}
		if e.src != nil {
			for k := range e.src.vars {
				out = append(out, k)
			}
		}
	}
	for k := range in.funcs {
		out = append(out, k)
	}
	return out
}

func nsFuncNames(ns string) []string {
	var out []string
	for k := range natives[ns] {
		out = append(out, k)
	}
	return out
}

// exprDesc descreve uma expressão curta para mensagens ("l", `l["valor"]`, "r.corpo").
func exprDesc(e Expr) string {
	switch x := e.(type) {
	case *Ident:
		return x.Name
	case *Member:
		if b := exprDesc(x.X); b != "" {
			return b + "." + x.Name
		}
	case *Index:
		if s, ok := x.Idx.(*StrLit); ok {
			if b := exprDesc(x.X); b != "" {
				return fmt.Sprintf("%s[%q]", b, s.Val)
			}
		}
		if n, ok := x.Idx.(*IntLit); ok {
			if b := exprDesc(x.X); b != "" {
				return fmt.Sprintf("%s[%d]", b, n.Val)
			}
		}
	}
	return ""
}

// reservedHint explica por que uma palavra reservada não serve de nome.
func reservedHint(word string) string {
	if word == "f" {
		return "'f' é palavra reservada da djo (declara função); escolha outro nome para a variável (ex.: fn1)"
	}
	return fmt.Sprintf("'%s' é palavra reservada da djo; escolha outro nome", word)
}

const validEscapes = `\n, \t, \" e \\`

// paramList mostra a assinatura: "a, b".
func paramList(ps []Param) string {
	var names []string
	for _, p := range ps {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

const nestedCallHint = "se um dos argumentos é outra chamada (como trim:x), ponha-a entre parênteses: f:a:(trim:x)"

// opHint dá a dica certa quando um operador não serve para os tipos.
func opHint(op Kind, l, r any) string {
	_, ls := l.(string)
	_, rs := r.(string)
	_, la := l.(*Array)
	_, ra := r.(*Array)
	switch {
	case op == Star && ((ls && isNum(r)) || (rs && isNum(l))):
		return " (para repetir um texto use Text.repeat:texto:vezes)"
	case op == Plus && la && ra:
		return " (para acrescentar itens use push:lista:item)"
	case op == Plus && ((ls && isNum(r)) || (rs && isNum(l))):
		return " (para juntar texto e número converta com str:número)"
	}
	return ""
}

func isNum(v any) bool {
	switch v.(type) {
	case int64, float64:
		return true
	}
	return false
}
