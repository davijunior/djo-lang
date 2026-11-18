package main

import (
	"fmt"
	"strconv"
	"unicode"
)

type Parser struct {
	toks   []Token
	pos    int
	noCall int  // >0: dentro de argumento, 'nome:' não vira chamada
	tpl    bool // lendo um template: blocos só com chaves
	// dentro do finally{} de um promise o return não vale
	noReturn bool
}

var validTypes = map[string]bool{"Map": true, "Int": true, "Float": true, "String": true, "Bool": true, "Arr": true, "Any": true, "Date": true, "DateTime": true}

// validType: tipos da linguagem ou nome de struct (inicial maiúscula).
func validType(s string) bool {
	if validTypes[s] {
		return true
	}
	r := []rune(s)
	return len(r) > 0 && unicode.IsUpper(r[0])
}

func Parse(src string) (prog []Stmt, err error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	return runParser(&Parser{toks: toks})
}

func runParser(p *Parser) (prog []Stmt, err error) {
	defer func() {
		if r := recover(); r != nil {
			if de, ok := r.(*djoError); ok {
				prog, err = nil, de
				return
			}
			panic(r)
		}
	}()
	for p.peek().Kind != EOF {
		prog = append(prog, p.parseStmt())
	}
	return prog, nil
}

func (p *Parser) peek() Token { return p.toks[p.pos] }
func (p *Parser) next() Token {
	t := p.toks[p.pos]
	if t.Kind != EOF {
		p.pos++
	}
	return t
}

func (p *Parser) expect(k Kind, what string) Token {
	t := p.peek()
	if t.Kind != k {
		fail(t.Line, "esperava %s, encontrei %s", what, describe(t))
	}
	return p.next()
}

func describe(t Token) string {
	if t.Kind == EOF {
		return "fim do arquivo"
	}
	return fmt.Sprintf("'%s'", t.Text)
}

// ---- blocos ----

// block lê um corpo: '{ ... }' ou linhas indentadas mais que a palavra
// que abriu o bloco (head).
type block struct {
	p     *Parser
	head  Token
	brace bool
}

func (p *Parser) openBlock(head Token) *block {
	b := &block{p: p, head: head, brace: p.peek().Kind == LBrace}
	if p.tpl && !b.brace {
		fail(head.Line, "em templates, o bloco '%s' precisa de chaves { }", head.Text)
	}
	if b.brace {
		p.next()
	}
	return b
}

func (b *block) more() bool {
	t := b.p.peek()
	if t.Kind == EOF {
		return false
	}
	if b.brace {
		return t.Kind != RBrace
	}
	return t.First && t.Col > b.head.Col
}

func (b *block) close(what string) {
	if b.brace {
		b.p.expect(RBrace, "'}' para fechar "+what)
	}
}

func (b *block) stmts() []Stmt {
	var out []Stmt
	for b.more() {
		out = append(out, b.p.parseStmt())
	}
	return out
}

// ---- instruções ----

func (p *Parser) parseStmt() Stmt {
	switch t := p.peek(); t.Kind {
	case IDENT:
		if p.promiseAt(p.pos, true) {
			return &ExprStmt{p.parsePromise()}
		}
		p.strayClause()
	case TplText:
		p.next()
		return &EchoStmt{&StrLit{t.Text}, true}
	case TplEcho:
		p.next()
		e := p.parseExpr()
		p.expect(TplEnd, "'}}' para fechar a expressão")
		return &EchoStmt{e, t.Text == "raw"}
	case KwF, KwAction:
		return p.parseFunc()
	case KwWhile:
		return p.parseWhile()
	case KwFor:
		return p.parseFor()
	case KwUntil:
		return p.parseUntil()
	case KwIf:
		return p.parseIf()
	case KwStruct:
		return p.parseStruct()
	case KwInterface:
		return p.parseInterface()
	case KwMatch:
		return p.parseMatch()
	case KwElif, KwElse:
		fail(t.Line, "'%s' sem um 'if' antes", t.Text)
	case KwParams:
		fail(t.Line, "'params' só pode ser o primeiro item de uma função ou de um while")
	case KwCond:
		fail(t.Line, "'cond' só pode ser usado dentro de um while")
	case KwEng:
		fail(t.Line, "'eng' só pode ser usado dentro de um until")
	}
	s := p.parseSimple()
	if nt := p.peek(); nt.Kind != EOF && nt.Kind != RBrace && !nt.First {
		fail(nt.Line, "esperava fim de linha, encontrei %s", describe(nt))
	}
	return s
}

// parseSimple lê uma instrução de uma linha (sem checar o fim dela).
func (p *Parser) parseSimple() Stmt {
	t := p.peek()
	switch t.Kind {
	case KwVar, KwArr, KwMap:
		p.next()
		name := p.expect(IDENT, "nome da variável")
		p.expect(Assign, "'='")
		return &VarStmt{Name: name.Text, Init: p.parseExpr(), IsArr: t.Kind == KwArr, IsMap: t.Kind == KwMap, Line: name.Line}
	case KwPrint:
		p.next()
		if nt := p.peek(); nt.First || nt.Kind == EOF || nt.Kind == RBrace {
			fail(t.Line, "'print' precisa de um valor")
		}
		return &PrintStmt{p.parseExpr()}
	case KwReturn:
		if p.noReturn {
			fail(t.Line, "o return não vale dentro do finally (ele serve para limpar, não para decidir o valor)")
		}
		p.next()
		nt := p.peek()
		if nt.First || nt.Kind == EOF || nt.Kind == RBrace {
			return &ReturnStmt{nil}
		}
		return &ReturnStmt{p.parseExpr()}
	}

	x := p.parseExpr()
	if nt := p.peek(); !nt.First {
		switch nt.Kind {
		case Assign:
			p.next()
			val := p.parseExpr()
			switch tg := x.(type) {
			case *Ident:
				return &AssignStmt{Name: tg.Name, Value: val, Line: tg.Line}
			case *Index:
				return &AssignStmt{Target: tg, Value: val, Line: tg.Line}
			case *Member:
				return &AssignStmt{Member: tg, Value: val, Line: tg.Line}
			}
			fail(nt.Line, "do lado esquerdo do '=' precisa haver uma variável")
		case PlusPlus, MinusMinus:
			p.next()
			delta := int64(1)
			if nt.Kind == MinusMinus {
				delta = -1
			}
			switch tg := x.(type) {
			case *Ident:
				return &IncStmt{Name: tg.Name, Delta: delta, Line: tg.Line}
			case *Member:
				return &IncStmt{Member: tg, Delta: delta, Line: tg.Line}
			}
			fail(nt.Line, "'%s' só funciona em variáveis e campos", nt.Text)
		}
	}
	return &ExprStmt{x}
}

func (p *Parser) parseFunc() Stmt {
	ft := p.next()
	name := p.expect(IDENT, "nome da função")
	fn := &FuncDecl{Name: name.Text, Line: ft.Line}
	savedNR := p.noReturn
	p.noReturn = false
	defer func() { p.noReturn = savedNR }()
	b := p.openBlock(ft)
	if ft.Kind == KwAction {
		fn.Action = true
		if b.more() && p.peek().Kind == STRING { // descrição: primeira linha, antes do params
			fn.Doc = p.next().Text
		}
	}
	if b.more() && p.peek().Kind == KwParams {
		fn.Params = p.parseParams()
	}
	fn.Body = b.stmts()
	b.close("a função '" + name.Text + "'")
	return fn
}

func (p *Parser) parseParams() []Param {
	p.next() // params
	p.expect(LBrack, "'['")
	var params []Param
	for p.peek().Kind != RBrack {
		name := p.expect(IDENT, "nome do parâmetro")
		typ := "Any" // sem tipo: aceita qualquer valor e não trava o tipo
		if p.peek().Kind == LBrack {
			p.next()
			tt := p.expect(IDENT, "tipo do parâmetro")
			if !validType(tt.Text) {
				fail(tt.Line, "tipo desconhecido '%s' (use Int, Float, String, Bool, Arr, Any ou o nome de uma struct)", tt.Text)
			}
			typ = tt.Text
			p.expect(RBrack, "']'")
		}
		params = append(params, Param{name.Text, typ})
		if p.peek().Kind == Comma {
			p.next()
		} else {
			break
		}
	}
	p.expect(RBrack, "']' para fechar params")
	return params
}

// while{ cond[...]  params[ var/arr ... ]  corpo }
func (p *Parser) parseWhile() Stmt {
	wt := p.next()
	w := &WhileStmt{Line: wt.Line}
	b := p.openBlock(wt)
	for b.more() {
		t := p.peek()
		if t.Kind == KwCond {
			if w.Cond != nil {
				fail(t.Line, "o while só pode ter um 'cond'")
			}
			p.next()
			p.expect(LBrack, "'['")
			w.Cond = p.parseExpr()
			p.expect(RBrack, "']' para fechar cond")
		} else if t.Kind == KwParams {
			p.next()
			p.expect(LBrack, "'['")
			for p.peek().Kind != RBrack {
				st := p.peek()
				s := p.parseSimple()
				if _, ok := s.(*VarStmt); !ok {
					fail(st.Line, "dentro do params do while só cabem 'var' e 'arr'")
				}
				w.Init = append(w.Init, s)
				if p.peek().Kind == Comma {
					p.next()
				}
				if p.peek().Kind == EOF {
					fail(st.Line, "params do while sem ']'")
				}
			}
			p.next()
		} else {
			break
		}
	}
	if w.Cond == nil {
		fail(wt.Line, "o while precisa de um cond[...]")
	}
	w.Body = b.stmts()
	b.close("o while")
	return w
}

// if{ cond[...] corpo } elif{ cond[...] corpo } else{ corpo }
// Sem chaves, o corpo é o que estiver indentado; 'elif'/'else' ficam na
// mesma coluna do 'if'.
func (p *Parser) parseIf() Stmt {
	it := p.next()
	s := &IfStmt{Line: it.Line}
	head := it
	for {
		b := p.openBlock(head)
		var cond Expr
		if head.Kind != KwElse {
			ct := p.peek()
			if ct.Kind != KwCond || !(ct.Line == head.Line || b.more()) {
				fail(head.Line, "'%s' precisa começar com cond[...]", head.Text)
			}
			p.next()
			p.expect(LBrack, "'['")
			cond = p.parseExpr()
			p.expect(RBrack, "']' para fechar cond")
			if nt := p.peek(); !b.brace && !nt.First && nt.Kind != EOF {
				fail(nt.Line, "sem '{', o corpo do %s começa na linha de baixo, indentado", head.Text)
			}
		}
		body := b.stmts()
		b.close("o " + head.Text)
		if head.Kind == KwElse {
			s.Else, s.HasElse = body, true
			return s
		}
		s.Branches = append(s.Branches, IfBranch{cond, body})
		if nt := p.peek(); (nt.Kind == KwElif || nt.Kind == KwElse) && (!nt.First || nt.Col == it.Col) {
			head = p.next()
			continue
		}
		return s
	}
}

// reconhece 'val[' e 'case[' (palavras só especiais dentro do match)
func (p *Parser) isWord(w string) bool {
	t := p.peek()
	return t.Kind == IDENT && t.Text == w && p.toks[p.pos+1].Kind == LBrack
}

// match{ val[x]  case[a, b] corpo  else corpo }
func (p *Parser) parseMatch() Stmt {
	mt := p.next()
	m := &MatchStmt{Line: mt.Line}
	b := p.openBlock(mt)
	if !b.more() || !p.isWord("val") {
		fail(mt.Line, "o match precisa começar com val[...]")
	}
	p.next()
	p.next()
	m.Val = p.parseExpr()
	p.expect(RBrack, "']' para fechar val")
	caseBody := func() []Stmt {
		var out []Stmt
		if t := p.peek(); !t.First && t.Kind != EOF && t.Kind != RBrace {
			out = append(out, p.parseStmt()) // corpo na mesma linha do case/else
		}
		for b.more() && !p.isWord("case") && p.peek().Kind != KwElse {
			out = append(out, p.parseStmt())
		}
		return out
	}
	for b.more() {
		t := p.peek()
		switch {
		case p.isWord("case"):
			p.next()
			p.next()
			c := MatchCase{}
			for p.peek().Kind != RBrack {
				c.Vals = append(c.Vals, p.parseExpr())
				if p.peek().Kind != Comma {
					break
				}
				p.next()
			}
			p.expect(RBrack, "']' para fechar case")
			if len(c.Vals) == 0 {
				fail(t.Line, "case[] precisa de pelo menos um valor")
			}
			c.Body = caseBody()
			m.Cases = append(m.Cases, c)
		case t.Kind == KwElse:
			if m.HasElse {
				fail(t.Line, "o match só pode ter um else")
			}
			p.next()
			m.Else, m.HasElse = caseBody(), true
		default:
			fail(t.Line, "dentro do match só cabem case[...] e else, encontrei %s", describe(t))
		}
	}
	b.close("o match")
	return m
}

// interface Nome{ f metodo  params[...] ... }: só as assinaturas, sem corpo.
func (p *Parser) parseInterface() Stmt {
	it := p.next()
	name := p.expect(IDENT, "nome da interface")
	if r := []rune(name.Text)[0]; !unicode.IsUpper(r) {
		fail(name.Line, "nomes de interface começam com letra maiúscula: '%s'", name.Text)
	}
	id := &InterfaceDecl{Name: name.Text, Line: it.Line}
	b := p.openBlock(it)
	seen := map[string]bool{}
	for b.more() {
		t := p.peek()
		if t.Kind != KwF {
			fail(t.Line, "dentro de uma interface só cabem métodos (f nome), encontrei %s", describe(t))
		}
		fd := p.parseFunc().(*FuncDecl)
		if len(fd.Body) > 0 {
			fail(fd.Line, "o método '%s' da interface %s não pode ter corpo: só o nome e o params", fd.Name, id.Name)
		}
		if seen[fd.Name] {
			fail(fd.Line, "o método '%s' aparece duas vezes em %s", fd.Name, id.Name)
		}
		seen[fd.Name] = true
		id.Methods = append(id.Methods, fd)
	}
	b.close("a interface '" + name.Text + "'")
	return id
}

// struct Nome{ campo[Tipo] nocall = padrão ...  f metodo ... }
func (p *Parser) parseStruct() Stmt {
	st := p.next()
	name := p.expect(IDENT, "nome da struct")
	if r := []rune(name.Text)[0]; !unicode.IsUpper(r) {
		fail(name.Line, "nomes de struct começam com letra maiúscula: '%s'", name.Text)
	}
	sd := &StructDecl{Name: name.Text, Line: st.Line, fieldIdx: map[string]int{}, methods: map[string]*FuncDecl{}}
	if p.peek().Kind == KwSonof {
		p.next()
		par := p.expect(IDENT, "nome da struct pai depois de sonof")
		if !unicode.IsUpper([]rune(par.Text)[0]) {
			fail(par.Line, "nomes de struct começam com letra maiúscula: '%s'", par.Text)
		}
		if par.Text == name.Text {
			fail(par.Line, "%s não pode ser filha de si mesma", name.Text)
		}
		sd.Parent = par.Text
	}
	b := p.openBlock(st)
	for b.more() {
		t := p.peek()
		switch {
		case t.Kind == KwF:
			fd := p.parseFunc().(*FuncDecl)
			if _, dup := sd.methods[fd.Name]; dup {
				fail(fd.Line, "o método '%s' aparece duas vezes em %s", fd.Name, sd.Name)
			}
			fd.Owner = sd
			sd.methods[fd.Name] = fd
			sd.Methods = append(sd.Methods, fd)
		case t.Kind == IDENT && p.toks[p.pos+1].Kind == LBrack:
			p.next()
			p.next()
			typ := p.expect(IDENT, "tipo do campo")
			if !validType(typ.Text) {
				fail(typ.Line, "tipo desconhecido '%s'", typ.Text)
			}
			p.expect(RBrack, "']'")
			f := FieldDecl{Name: t.Text, Type: typ.Text}
			if c := p.peek(); c.Kind == IDENT && c.Text == "nocall" && !c.First {
				p.next()
				f.NoCall = true
			}
			if c := p.peek(); c.Kind == Assign && !c.First {
				p.next()
				f.Default = p.parseExpr()
			}
			if nt := p.peek(); !nt.First && nt.Kind != RBrace && nt.Kind != EOF {
				fail(nt.Line, "esperava fim de linha depois do campo '%s', encontrei %s", f.Name, describe(nt))
			}
			if _, dup := sd.fieldIdx[f.Name]; dup {
				fail(t.Line, "o campo '%s' aparece duas vezes em %s", f.Name, sd.Name)
			}
			sd.fieldIdx[f.Name] = len(sd.Fields)
			sd.Fields = append(sd.Fields, f)
		case t.Kind == IDENT && unicode.IsUpper([]rune(t.Text)[0]):
			// composição: uma linha só com o nome de outra struct
			p.next()
			if nt := p.peek(); !nt.First && nt.Kind != RBrace && nt.Kind != EOF {
				fail(nt.Line, "esperava fim de linha depois de '%s', encontrei %s", t.Text, describe(nt))
			}
			if _, dup := sd.fieldIdx[t.Text]; dup {
				fail(t.Line, "%s já está incluída em %s", t.Text, sd.Name)
			}
			sd.fieldIdx[t.Text] = len(sd.Fields)
			sd.Fields = append(sd.Fields, FieldDecl{Name: t.Text, Type: t.Text, Embed: true, Default: &Call{Name: t.Text, Line: t.Line}})
		default:
			fail(t.Line, "dentro de uma struct só cabem campos (nome[Tipo]), métodos (f nome) e outras structs; encontrei %s", describe(t))
		}
	}
	b.close("a struct '" + name.Text + "'")
	sd.own = append([]FieldDecl(nil), sd.Fields...)
	for _, m := range sd.Methods {
		if _, clash := sd.fieldIdx[m.Name]; clash {
			fail(m.Line, "'%s' é campo e método ao mesmo tempo em %s", m.Name, sd.Name)
		}
	}
	return sd
}

// until{ eng[init ; cond ; passo]  corpo }
// for{ each[x in lista]  corpo }   |   for{ each[chave, valor in mapa]  corpo }
func (p *Parser) parseFor() Stmt {
	ft := p.next()
	f := &ForStmt{Line: ft.Line}
	b := p.openBlock(ft)
	if e := p.peek(); !b.more() || e.Kind != IDENT || e.Text != "each" {
		fail(ft.Line, "o for precisa começar com each[item in lista]")
	}
	p.next()
	p.expect(LBrack, "'[' depois de each")
	for {
		f.Vars = append(f.Vars, p.expect(IDENT, "nome da variável do for").Text)
		if p.peek().Kind != Comma {
			break
		}
		p.next()
	}
	if len(f.Vars) > 2 {
		fail(ft.Line, "o each aceita no máximo duas variáveis: [chave, valor in mapa]")
	}
	if in := p.peek(); in.Kind != IDENT || in.Text != "in" {
		fail(in.Line, "esperava 'in' no each, encontrei %s", describe(in))
	}
	p.next()
	f.Iter = p.parseExpr()
	p.expect(RBrack, "']' para fechar each")
	f.Body = b.stmts()
	b.close("o for")
	return f
}

func (p *Parser) parseUntil() Stmt {
	ut := p.next()
	u := &UntilStmt{Line: ut.Line}
	b := p.openBlock(ut)
	if !b.more() || p.peek().Kind != KwEng {
		fail(ut.Line, "o until precisa começar com eng[início ; condição ; passo]")
	}
	p.next()
	p.expect(LBrack, "'[' depois de eng")
	u.Init = p.parseSimple()
	p.expect(Semi, "';' depois do início")
	u.Cond = p.parseExpr()
	p.expect(Semi, "';' depois da condição")
	u.Step = p.parseSimple()
	p.expect(RBrack, "']' para fechar eng")
	u.Body = b.stmts()
	b.close("o until")
	return u
}

// ---- expressões ----
// comparação < soma < produto < unário < postfixo [i] < primário.
// Um operador numa linha nova encerra a expressão.

func (p *Parser) continues(kinds ...Kind) (Token, bool) {
	t := p.peek()
	if t.First {
		return t, false
	}
	for _, k := range kinds {
		if t.Kind == k {
			return t, true
		}
	}
	return t, false
}

// a ?? b: usa b quando a for nil
func (p *Parser) parseExpr() Expr {
	l := p.parseOr()
	for {
		t, ok := p.continues(Coalesce)
		if !ok {
			return l
		}
		p.next()
		l = &Logical{Coalesce, l, p.parseOr(), t.Line}
	}
}

func (p *Parser) parseOr() Expr {
	l := p.parseAnd()
	for {
		t, ok := p.continues(KwOr)
		if !ok {
			return l
		}
		p.next()
		l = &Logical{t.Kind, l, p.parseAnd(), t.Line}
	}
}

func (p *Parser) parseAnd() Expr {
	l := p.parseNot()
	for {
		t, ok := p.continues(KwAnd)
		if !ok {
			return l
		}
		p.next()
		l = &Logical{t.Kind, l, p.parseNot(), t.Line}
	}
}

func (p *Parser) parseNot() Expr {
	if t := p.peek(); t.Kind == KwNot {
		p.next()
		return &Unary{KwNot, p.parseNot(), t.Line}
	}
	return p.parseCmp()
}

func (p *Parser) parseCmp() Expr {
	l := p.parseAdd()
	for {
		t, ok := p.continues(Eq, Neq, Lt, Gt, Lte, Gte)
		if !ok {
			return l
		}
		p.next()
		l = &Binary{t.Kind, l, p.parseAdd(), t.Line}
	}
}

func (p *Parser) parseAdd() Expr {
	l := p.parseMul()
	for {
		t, ok := p.continues(Plus, Minus)
		if !ok {
			return l
		}
		p.next()
		l = &Binary{t.Kind, l, p.parseMul(), t.Line}
	}
}

func (p *Parser) parseMul() Expr {
	l := p.parseUnary()
	for {
		t, ok := p.continues(Star, Slash, Percent)
		if !ok {
			return l
		}
		p.next()
		l = &Binary{t.Kind, l, p.parseUnary(), t.Line}
	}
}

func (p *Parser) parseUnary() Expr {
	if t := p.peek(); t.Kind == Minus {
		p.next()
		return &Unary{Minus, p.parseUnary(), t.Line}
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() Expr {
	x := p.parsePrimary()
	for {
		if d, ok := p.continues(Dot); ok { // obj.campo  |  obj.metodo:args
			p.next()
			name := p.next()
			if _, kw := keywords[name.Text]; name.Kind != IDENT && !(kw && !name.First) {
				fail(name.Line, "esperava nome do campo ou método depois do '.', encontrei %s", describe(name))
			}
			if c := p.peek(); c.Kind == Colon && !c.First && p.noCall == 0 {
				p.next()
				x = &MethodCall{x, name.Text, p.parseCallArgs(), d.Line}
			} else {
				x = &Member{x, name.Text, d.Line}
			}
			continue
		}
		t, ok := p.continues(LBrack)
		if !ok {
			return x
		}
		p.next()
		saved := p.noCall
		p.noCall = 0
		idx := p.parseExpr()
		p.noCall = saved
		p.expect(RBrack, "']'")
		x = &Index{x, idx, t.Line}
	}
}

func (p *Parser) startsArg() bool {
	t := p.peek()
	if t.First {
		return false
	}
	switch t.Kind {
	case INT, FLOAT, STRING, IDENT, KwTrue, KwFalse, KwNil, LParen, LBrack, LBrace, Minus, Dot:
		return true
	}
	return false
}

func (p *Parser) parsePrimary() Expr {
	if p.promiseAt(p.pos, false) {
		return p.parsePromise()
	}
	t := p.next()
	switch t.Kind {
	case INT:
		v, err := strconv.ParseInt(t.Text, 10, 64)
		if err != nil {
			fail(t.Line, "número inteiro grande demais: %s", t.Text)
		}
		return &IntLit{v}
	case FLOAT:
		v, _ := strconv.ParseFloat(t.Text, 64)
		return &FloatLit{v}
	case STRING:
		return &StrLit{t.Text}
	case KwTrue:
		return &BoolLit{true}
	case KwFalse:
		return &BoolLit{false}
	case KwNil:
		return &NilLit{}
	case KwPick:
		p.expect(LBrack, "'[' depois de pick")
		saved := p.noCall
		p.noCall = 0
		pk := &Pick{Line: t.Line}
		pk.Cond = p.parseExpr()
		p.expect(Semi, "';' depois da condição")
		pk.A = p.parseExpr()
		p.expect(Semi, "';' depois do primeiro valor")
		pk.B = p.parseExpr()
		p.noCall = saved
		p.expect(RBrack, "']' para fechar pick")
		return pk
	case KwInput:
		if n := p.peek(); n.Kind == STRING && !n.First {
			p.next()
			return &Input{&StrLit{n.Text}}
		}
		return &Input{}
	case LBrack:
		saved := p.noCall
		p.noCall = 0
		arr := &ArrayLit{}
		for p.peek().Kind != RBrack {
			arr.Elems = append(arr.Elems, p.parseExpr())
			if p.peek().Kind == Comma {
				p.next()
			} else {
				break
			}
		}
		p.noCall = saved
		p.expect(RBrack, "']' para fechar a lista")
		return arr
	case LBrace:
		saved := p.noCall
		m := &MapLit{Line: t.Line}
		for p.peek().Kind != RBrace {
			p.noCall = 1 // na chave, 'nome:' não é chamada
			m.Keys = append(m.Keys, p.parseExpr())
			p.noCall = saved
			p.expect(Colon, "':' depois da chave do map")
			p.noCall = 0
			m.Vals = append(m.Vals, p.parseExpr())
			p.noCall = saved
			if p.peek().Kind == Comma {
				p.next()
			} else {
				break
			}
		}
		p.noCall = saved
		p.expect(RBrace, "'}' para fechar o map")
		return m
	case LParen:
		saved := p.noCall
		p.noCall = 0
		e := p.parseExpr()
		p.noCall = saved
		p.expect(RParen, "')'")
		return e
	case IDENT:
		if c := p.peek(); c.Kind == Colon && !c.First && p.noCall == 0 {
			return p.parseCall(t)
		}
		return &Ident{t.Text, t.Line}
	}
	fail(t.Line, "expressão inválida: encontrei %s", describe(t))
	return nil
}

// nome:  |  nome:a  |  nome:a:b
func (p *Parser) parseCall(name Token) Expr {
	p.next() // ':'
	return &Call{Name: name.Text, Line: name.Line, Args: p.parseCallArgs()}
}

// parseCallArgs lê os argumentos depois de um ':' já consumido.
// Um '.' no lugar do argumento pede o valor padrão (Post:.:"texto").
func (p *Parser) parseCallArgs() []Expr {
	if !p.startsArg() {
		return nil
	}
	p.noCall++
	defer func() { p.noCall-- }()
	var args []Expr
	for {
		if d := p.peek(); d.Kind == Dot {
			p.next()
			args = append(args, &Placeholder{d.Line})
		} else {
			args = append(args, p.parseAdd())
		}
		if c := p.peek(); c.Kind == Colon && !c.First {
			p.next()
			if !p.startsArg() {
				fail(c.Line, "esperava um argumento depois de ':'")
			}
			continue
		}
		return args
	}
}
