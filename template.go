package main

import (
	"fmt"
	"strings"
)

// ParseTemplate compila um template (HTML com código) no mesmo AST dos
// programas. Marcas:
//
//	{{ expr }}   escreve o valor, com escape de HTML
//	{{! expr }}  escreve sem escape
//	{% código %} executa código (blocos com chaves: {% if{ cond[x] %} ... {% } %})
//	{# nota #}   comentário
//
// Uma linha que termina em '%}' ou '#}' não deixa linha em branco na saída.
func ParseTemplate(src string) ([]Stmt, error) {
	var toks []Token
	line := 1
	textStart, i := 0, 0

	emitText := func(s string, l int) {
		if s != "" {
			toks = append(toks, Token{Kind: TplText, Text: s, Line: l, First: true})
		}
	}

	for i < len(src) {
		if src[i] != '{' || i+1 >= len(src) {
			i++
			continue
		}
		var open, closer string
		switch src[i+1] {
		case '{':
			open, closer = "{{", "}}"
		case '%':
			open, closer = "{%", "%}"
		case '#':
			open, closer = "{#", "#}"
		default:
			i++
			continue
		}

		text := src[textStart:i]
		emitText(text, line)
		line += strings.Count(text, "\n")

		end := strings.Index(src[i+2:], closer)
		if end < 0 {
			return nil, &djoError{Line: line, Msg: fmt.Sprintf("'%s' sem '%s' para fechar", open, closer)}
		}
		inner := src[i+2 : i+2+end]
		startLine := line
		line += strings.Count(inner, "\n")
		i += 2 + end + 2

		switch open {
		case "{{":
			kind := ""
			if strings.HasPrefix(inner, "!") {
				kind, inner = "raw", inner[1:]
			}
			ptoks, err := lexPiece(inner, startLine)
			if err != nil {
				return nil, err
			}
			if len(ptoks) == 0 {
				return nil, &djoError{Line: startLine, Msg: "'{{ }}' vazio"}
			}
			toks = append(toks, Token{Kind: TplEcho, Text: kind, Line: startLine, First: true})
			ptoks[0].First = false
			toks = append(toks, ptoks...)
			toks = append(toks, Token{Kind: TplEnd, Text: "}}", Line: line})
		case "{%", "{#":
			if open == "{%" {
				ptoks, err := lexPiece(inner, startLine)
				if err != nil {
					return nil, err
				}
				if len(ptoks) > 0 {
					ptoks[0].First = true
					toks = append(toks, ptoks...)
				}
			}
			if strings.HasPrefix(src[i:], "\r\n") {
				i += 2
				line++
			} else if strings.HasPrefix(src[i:], "\n") {
				i++
				line++
			}
		}
		textStart = i
	}
	emitText(src[textStart:], line)
	toks = append(toks, Token{Kind: EOF, Line: line, First: true})
	return runParser(&Parser{toks: toks, tpl: true})
}

// lexPiece tokeniza um pedaço de código mantendo os números de linha do arquivo.
func lexPiece(src string, startLine int) ([]Token, error) {
	toks, err := Lex(src)
	if err != nil {
		if de, ok := err.(*djoError); ok {
			return nil, &djoError{Line: de.Line + startLine - 1, Msg: de.Msg}
		}
		return nil, err
	}
	toks = toks[:len(toks)-1] // tira o EOF
	for i := range toks {
		toks[i].Line += startLine - 1
	}
	return toks, nil
}
