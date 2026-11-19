package main

import (
	"fmt"
	"strings"
	"unicode"
)

type Kind int

const (
	EOF Kind = iota
	IDENT
	INT
	FLOAT
	STRING

	// palavras-chave
	KwF
	KwVar
	KwPrint
	KwParams
	KwReturn
	KwTrue
	KwFalse
	KwNil
	KwArr
	KwInput
	KwCond
	KwWhile
	KwUntil
	KwEng
	KwIf
	KwElif
	KwElse
	KwMatch
	KwAnd
	KwOr
	KwNot
	KwPick
	KwStruct
	KwSonof
	KwInterface
	KwFor
	KwMap
	KwAction

	// símbolos
	LBrace
	RBrace
	LBrack
	RBrack
	LParen
	RParen
	Comma
	Colon
	Plus
	Minus
	Star
	Slash
	Percent
	Assign
	Eq
	Neq
	Lt
	Gt
	Lte
	Gte
	PlusPlus
	MinusMinus
	Semi
	Coalesce
	Dot

	// só existem em templates (geradas por ParseTemplate)
	TplText
	TplEcho
	TplEnd
)

var keywords = map[string]Kind{
	"f":         KwF,
	"var":       KwVar,
	"print":     KwPrint,
	"params":    KwParams,
	"return":    KwReturn,
	"true":      KwTrue,
	"false":     KwFalse,
	"nil":       KwNil,
	"arr":       KwArr,
	"input":     KwInput,
	"cond":      KwCond,
	"while":     KwWhile,
	"until":     KwUntil,
	"eng":       KwEng,
	"if":        KwIf,
	"elif":      KwElif,
	"else":      KwElse,
	"match":     KwMatch,
	"and":       KwAnd,
	"or":        KwOr,
	"not":       KwNot,
	"pick":      KwPick,
	"struct":    KwStruct,
	"sonof":     KwSonof,
	"interface": KwInterface,
	"for":       KwFor,
	"map":       KwMap,
	"action":    KwAction,
}

// Token guarda a posição para o parser decidir blocos por indentação
// (Col) e fim de instrução por quebra de linha (First).
type Token struct {
	Kind  Kind
	Text  string
	Line  int
	Col   int
	First bool // primeiro token da linha
}

func Lex(src string) ([]Token, error) {
	rs := []rune(src)
	var toks []Token
	line, col := 1, 1
	first := true
	i := 0

	adv := func() rune {
		r := rs[i]
		i++
		if r == '\n' {
			line++
			col = 1
			first = true
		} else {
			col++
		}
		return r
	}
	push := func(k Kind, text string, l, c int, f bool) {
		toks = append(toks, Token{Kind: k, Text: text, Line: l, Col: c, First: f})
		first = false
	}

	for i < len(rs) {
		r := rs[i]
		l, c, f := line, col, first
		switch {
		case unicode.IsSpace(r):
			adv()

		case r == '/' && i+1 < len(rs) && rs[i+1] == '/':
			for i < len(rs) && rs[i] != '\n' {
				adv()
			}

		case unicode.IsDigit(r):
			st := i
			kind := INT
			for i < len(rs) && unicode.IsDigit(rs[i]) {
				adv()
			}
			if i+1 < len(rs) && rs[i] == '.' && unicode.IsDigit(rs[i+1]) {
				kind = FLOAT
				adv()
				for i < len(rs) && unicode.IsDigit(rs[i]) {
					adv()
				}
			}
			push(kind, string(rs[st:i]), l, c, f)

		case unicode.IsLetter(r) || r == '_':
			st := i
			for i < len(rs) && (unicode.IsLetter(rs[i]) || unicode.IsDigit(rs[i]) || rs[i] == '_') {
				adv()
			}
			text := string(rs[st:i])
			if k, ok := keywords[text]; ok {
				push(k, text, l, c, f)
			} else {
				push(IDENT, text, l, c, f)
			}

		case r == '"':
			adv()
			var sb strings.Builder
			closed := false
			for i < len(rs) {
				ch := adv()
				if ch == '"' {
					closed = true
					break
				}
				if ch == '\\' {
					if i >= len(rs) {
						break
					}
					e := adv()
					switch e {
					case 'n':
						sb.WriteRune('\n')
					case 't':
						sb.WriteRune('\t')
					case '"':
						sb.WriteRune('"')
					case '\\':
						sb.WriteRune('\\')
					default:
						return nil, &djoError{Line: line, Code: codeInvalidEscape, Msg: fmt.Sprintf("escape inválido '\\%c' na string (os válidos são %s)", e, validEscapes)}
					}
					continue
				}
				sb.WriteRune(ch)
			}
			if !closed {
				return nil, &djoError{Line: l, Msg: "string sem fechar"}
			}
			push(STRING, sb.String(), l, c, f)

		default:
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			twoKinds := map[string]Kind{"==": Eq, "!=": Neq, "<=": Lte, ">=": Gte, "++": PlusPlus, "--": MinusMinus, "??": Coalesce}
			if k, ok := twoKinds[two]; ok {
				adv()
				adv()
				push(k, two, l, c, f)
				continue
			}
			oneKinds := map[rune]Kind{
				'{': LBrace, '}': RBrace, '[': LBrack, ']': RBrack,
				'(': LParen, ')': RParen, ',': Comma, ':': Colon,
				'+': Plus, '-': Minus, '*': Star, '/': Slash, '%': Percent,
				'=': Assign, '<': Lt, '>': Gt, ';': Semi, '.': Dot,
			}
			k, ok := oneKinds[r]
			if !ok {
				return nil, &djoError{Line: l, Msg: fmt.Sprintf("caractere inesperado '%c'", r)}
			}
			adv()
			push(k, string(r), l, c, f)
		}
	}
	push(EOF, "", line, col, true)
	return toks, nil
}
