package main

import (
	"bytes"
	"io"
	"strings"
)

// promise{ corpo }success{ [x] ... }error{ [e] ... }finally{ ... }
//
// Fase 1: tudo roda em sequência, mas com a semântica pensada para a concorrência (fase 2):
//   - o promise roda no ponto em que aparece; a saída (print) entra na página nesse lugar, e
//     se um erro vai subir dela, a do corpo, do success e do error é descartada (só a do finally fica);
//   - o resultado (valor e erro) só é ENTREGUE na espera: o primeiro uso da variável
//     ou o fim da função (da página, da action, do job) que o criou;
//   - o bloco enxerga uma cópia das variáveis de fora e não escreve nelas (Env.outer/src);
//   - não altera a resposta nem a sessão da página (promiseGuard);
//   - o return termina o BLOCO, com o valor.
//
// As palavras promise, uniq, success, error e finally são contextuais: nada é reservado.

// Clause é o success{}, o error{} ou o finally{}.
type Clause struct {
	Param string // nome em [x], ou ""
	Body  []Stmt
}

// PromiseExpr é o promise (expressão ou instrução).
type PromiseExpr struct {
	Uniq bool // uniq: espera o fim antes de continuar
	Body []Stmt
	Succ *Clause
	Err  *Clause
	Fin  *Clause
	Line int
}

// promiseHandle é um promise já executado e ainda não entregue.
type promiseHandle struct {
	in        *Interp
	val       any
	sig       any // erro (*djoError) ou sinal (give_up, Spec.fail...) que sobe na espera
	delivered bool
}

const outerMsg = "o promise não altera variáveis de fora ('%s'); devolva o valor com return"

// ---- parser ----

func (p *Parser) wordAt(i int, w string) bool {
	return i < len(p.toks) && p.toks[i].Kind == IDENT && p.toks[i].Text == w
}

func (p *Parser) lineStartIdx(i int) int {
	for i > 0 && !p.toks[i].First {
		i--
	}
	return i
}

// promiseAt: o token i começa um promise ("promise{", "uniq promise{" ou, só no começo de
// uma instrução, "promise" com o corpo indentado na linha de baixo)?
func (p *Parser) promiseAt(i int, indent bool) bool {
	if p.wordAt(i, "uniq") {
		i++
	}
	if !p.wordAt(i, "promise") || i+1 >= len(p.toks) {
		return false
	}
	nt := p.toks[i+1]
	if nt.Kind == LBrace && !nt.First {
		return true
	}
	return indent && !p.tpl && nt.First && nt.Kind != RBrace && nt.Kind != EOF && nt.Col > p.toks[p.lineStartIdx(i)].Col
}

// clauseAhead: o próximo token é a cláusula 'name' deste promise?
func (p *Parser) clauseAhead(name string, brace bool, headCol int) bool {
	t := p.peek()
	if t.Kind != IDENT || t.Text != name || p.pos+1 >= len(p.toks) {
		return false
	}
	nt := p.toks[p.pos+1]
	if brace {
		if nt.Kind != LBrace || nt.First {
			return false
		}
		return !t.First || t.Col == headCol
	}
	return t.First && t.Col == headCol && nt.First && nt.Col > headCol
}

// takeParam lê o "[nome]" sozinho na primeira linha do bloco.
func (p *Parser) takeParam(b *block, what string, allowed bool) string {
	if !b.more() || p.peek().Kind != LBrack || p.pos+3 >= len(p.toks) {
		return ""
	}
	n, c, e := p.toks[p.pos+1], p.toks[p.pos+2], p.toks[p.pos+3]
	if n.Kind != IDENT || c.Kind != RBrack {
		return ""
	}
	switch e.Kind { // sozinho na linha, ou (em uma linha só, como nos templates) antes de uma instrução
	case RBrace, EOF, TplText, TplEnd, KwReturn, KwPrint, KwVar, KwArr, KwMap, KwIf, KwWhile, KwUntil, KwFor, KwMatch, IDENT:
	default:
		if !e.First {
			return ""
		}
	}
	if !allowed {
		fail(n.Line, "o %s não tem parâmetro; só o success{ [x] } e o error{ [e] } recebem um valor", what)
	}
	p.pos += 3
	return n.Text
}

func (p *Parser) parsePromise() *PromiseExpr {
	first := p.toks[p.lineStartIdx(p.pos)]
	pe := &PromiseExpr{Line: p.peek().Line}
	if p.wordAt(p.pos, "uniq") {
		pe.Uniq = true
		p.next()
	}
	p.next() // promise
	brace := p.peek().Kind == LBrace
	savedNR := p.noReturn
	defer func() { p.noReturn = savedNR }()

	p.noReturn = false
	b := p.openBlock(first)
	p.takeParam(b, "promise", false)
	pe.Body = b.stmts()
	b.close("o promise")

	clause := func(name string) *Clause {
		if !p.clauseAhead(name, brace, first.Col) {
			return nil
		}
		p.next()
		cb := p.openBlock(first)
		p.noReturn = name == "finally"
		c := &Clause{Param: p.takeParam(cb, name, name != "finally")}
		c.Body = cb.stmts()
		cb.close("o " + name)
		return c
	}
	pe.Succ = clause("success")
	pe.Err = clause("error")
	pe.Fin = clause("finally")
	if pe.Succ == nil && pe.Err == nil && pe.Fin == nil {
		fail(pe.Line, "um promise precisa de success, error ou finally depois do corpo (a ordem é promise, success, error, finally)")
	}
	for _, n := range []string{"success", "error", "finally"} {
		if p.clauseAhead(n, brace, first.Col) {
			fail(p.peek().Line, "'%s' fora de ordem ou repetido: a ordem é promise, success, error, finally", n)
		}
	}
	return pe
}

// strayClause: success/error/finally { sem um promise antes.
func (p *Parser) strayClause() {
	t := p.peek()
	if t.Kind == IDENT && (t.Text == "success" || t.Text == "error" || t.Text == "finally") && p.pos+1 < len(p.toks) {
		if nt := p.toks[p.pos+1]; nt.Kind == LBrace && !nt.First && t.First {
			fail(t.Line, "'%s{' sem um promise antes (a ordem é promise, success, error, finally)", t.Text)
		}
	}
}

// ---- execução ----

// guard roda f e devolve o que a interrompeu: um erro capturável (*djoError) ou um sinal
// que o promise só repassa (haltSignal, give_up, Spec.fail/skip). Os erros fatais (limites)
// e os pânicos do próprio Go passam direto.
func (in *Interp) guard(f func()) (sig any) {
	defer func() {
		if r := recover(); r != nil {
			switch x := r.(type) {
			case *djoError:
				if x.Fatal {
					panic(r)
				}
				sig = x
			case haltSignal, *jobGiveUp, *specSignal:
				sig = r
			default:
				panic(r)
			}
		}
	}()
	f()
	return nil
}

func errMap(e *djoError) *Map {
	m := newMap()
	m.set("msg", e.Msg)
	m.set("line", int64(e.Line))
	code := e.Code
	if code == "" {
		code = "erro"
	}
	m.set("code", code)
	m.set("data", e.Data)
	st := &Array{}
	for _, f := range e.Stack {
		fm := newMap()
		fm.set("fn", f.Fn)
		fm.set("file", f.File)
		fm.set("line", int64(f.Line))
		st.Items = append(st.Items, fm)
	}
	m.set("stack", st)
	if e.File == "" {
		m.set("file", nil)
	} else {
		m.set("file", e.File)
	}
	return m
}

func (in *Interp) startPromise(pe *PromiseExpr, env *Env) *promiseHandle {
	h := &promiseHandle{in: in}
	savedOut, savedGlobal, savedTx := in.out, in.global, in.promOuterTx
	view := &Env{vars: map[string]any{}, locked: map[string]string{}, outer: true, src: env}
	in.global = &Env{vars: map[string]any{}, locked: map[string]string{}, outer: true, src: savedGlobal}
	in.prom++
	in.promOuterTx = in.promOuterTx || in.txOpen()
	defer func() {
		in.out, in.global, in.promOuterTx = savedOut, savedGlobal, savedTx
		in.prom--
	}()

	txBefore := in.txOpen()
	heldMark := in.promiseJobMark()
	pendMark := len(in.pend)
	var val any

	runBlock := func(body []Stmt, e *Env) (v any) {
		mark := len(in.pend)
		r, _ := in.execBlock(body, e)
		in.awaitPending(mark)
		return r
	}

	// A saída de cada parte vai para um buffer; no fim, o que vale entra na página, no lugar
	// onde o promise foi escrito (veja o fim desta função).
	capture := func(f func()) string {
		var b bytes.Buffer
		prev := in.out
		in.out = &b
		defer func() { in.out = prev }()
		f()
		return b.String()
	}
	var bodyOut, succOut, errOut, finOut string
	var sig any
	bodyOut = capture(func() { sig = in.guard(func() { val = runBlock(pe.Body, newEnv(view)) }) })
	bodyOK := sig == nil
	if sig != nil { // ponto de salvamento: desfaz o que o bloco abriu
		if !txBefore && in.txOpen() {
			in.closeTx()
		}
		in.promiseJobDiscard(heldMark)
		if len(in.pend) > pendMark {
			in.pend = in.pend[:pendMark]
		}
	}
	runClause := func(c *Clause, arg any) string {
		val = nil
		return capture(func() {
			sig = in.guard(func() {
				e := newEnv(view)
				if c.Param != "" {
					e.vars[c.Param] = arg
				}
				val = runBlock(c.Body, e)
			})
		})
	}
	if sig == nil && pe.Succ != nil {
		succOut = runClause(pe.Succ, val)
	} else if de, ok := sig.(*djoError); ok && pe.Err != nil {
		errOut = runClause(pe.Err, errMap(de))
	}
	if pe.Fin != nil {
		finOut = capture(func() {
			if fsig := in.guard(func() { runBlock(pe.Fin.Body, newEnv(view)) }); fsig != nil {
				sig = fsig // o erro do finally vence o que estava pendente
			}
		})
	}
	h.val, h.sig = val, sig
	// A saída entra na página AGORA, que é o lugar onde o promise foi escrito (na fase 2 esta
	// posição será reservada no começo e preenchida aqui). Se um erro vai subir do promise, a
	// saída do corpo, do success e do error é descartada; só a do finally fica. Se o corpo
	// falhou e o error tratou, a saída do corpo se perde e a do error aparece.
	var final string
	if sig == nil {
		if bodyOK {
			final += bodyOut
		}
		final += succOut + errOut
	}
	final += finOut
	if final != "" {
		in.out = savedOut
		io.WriteString(savedOut, final)
	}
	in.pend = append(in.pend, h)
	return h
}

// await entrega o resultado: a saída entra na página, o erro (ou sinal) sobe.
func (in *Interp) await(h *promiseHandle) any {
	if !h.delivered {
		h.delivered = true
		if n := len(in.pend); n > 0 && in.pend[n-1] == h {
			in.pend = in.pend[:n-1]
		}
	}
	if h.sig != nil {
		panic(h.sig)
	}
	return h.val
}

// awaitPending entrega, na ordem, os promises começados desde 'mark' que ninguém esperou.
func (in *Interp) awaitPending(mark int) {
	for len(in.pend) > mark {
		h := in.pend[mark]
		in.pend = append(in.pend[:mark], in.pend[mark+1:]...)
		if h.delivered {
			continue
		}
		h.delivered = true
		if h.sig != nil {
			panic(h.sig)
		}
	}
}

// evalValue avalia uma expressão; um promise sem uniq vira um handle (entregue no primeiro uso).
func (in *Interp) evalValue(e Expr, env *Env) any {
	if pe, ok := e.(*PromiseExpr); ok && !pe.Uniq {
		return in.startPromise(pe, env)
	}
	return in.eval(e, env)
}

// runStmtPromise: o promise como instrução; sem uniq, a entrega fica para o fim da função.
func (in *Interp) runStmtPromise(pe *PromiseExpr, env *Env) {
	h := in.startPromise(pe, env)
	if pe.Uniq {
		in.await(h)
	}
}

// checkOuterRoot recusa "lista[0] = x" e "obj.campo = x" quando a raiz é variável de fora.
func (in *Interp) checkOuterRoot(root Expr, env *Env, line int) {
	if in.prom == 0 {
		return
	}
	for {
		switch x := root.(type) {
		case *Ident:
			if env.isOuter(x.Name) {
				fail(line, outerMsg, x.Name)
			}
			return
		case *Index:
			root = x.X
		case *Member:
			root = x.X
		default:
			return
		}
	}
}

var promiseBlocked = map[string]bool{
	"setheader": true, "status": true, "redirect": true, "setcookie": true,
	"Session.set": true, "Session.del": true, "Session.clear": true, "Session.destroy": true, "Session.renew": true,
	"Http.json": true, "Page.headers": true,
}

// promiseGuard: dentro de um promise, o que altera a resposta ou a sessão é recusado, e o Db
// não pode ser usado enquanto há uma transação aberta fora (a conexão é do pai).
func (in *Interp) promiseGuard(name string, line int) {
	if promiseBlocked[name] {
		fail(line, "'%s' altera a resposta ou a sessão da página; faça isso fora do promise", name)
	}
	if in.promOuterTx && strings.HasPrefix(name, "Db.") {
		fail(line, "'%s': há uma transação aberta (Db.begin) fora do promise; use o Db antes do begin ou depois do commit", name)
	}
}

// deepCopy copia listas, mapas e structs em profundidade (o promise trabalha com cópias).
func deepCopy(v any) any { return deepCopyM(v, map[any]any{}) }

func deepCopyM(v any, seen map[any]any) any {
	switch x := v.(type) {
	case *Array:
		if c, ok := seen[x]; ok {
			return c
		}
		n := &Array{Items: make([]any, len(x.Items))}
		seen[x] = n
		for i, it := range x.Items {
			n.Items[i] = deepCopyM(it, seen)
		}
		return n
	case *Map:
		if c, ok := seen[x]; ok {
			return c
		}
		n := newMap()
		seen[x] = n
		for i, k := range x.Keys {
			n.set(k, deepCopyM(x.Vals[i], seen))
		}
		return n
	case *Instance:
		if c, ok := seen[x]; ok {
			return c
		}
		n := &Instance{Def: x.Def, Vals: make([]any, len(x.Vals))}
		seen[x] = n
		for i, it := range x.Vals {
			n.Vals[i] = deepCopyM(it, seen)
		}
		return n
	case *promiseHandle: // usar um promise de fora é o "primeiro uso": espera e copia o valor
		return deepCopyM(x.in.await(x), seen)
	}
	return v
}

// ---- reject: ----

func init() {
	builtins["reject"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		switch len(a) {
		case 1:
			switch x := a[0].(type) {
			case string:
				panic(&djoError{Line: line, Msg: x})
			case *Map: // reject:e relança o erro de um error{ [e] }
				e := &djoError{Line: line}
				if v, ok := x.get("msg"); ok {
					e.Msg, _ = v.(string)
				}
				if v, ok := x.get("line"); ok {
					if n, ok := v.(int64); ok {
						e.Line = int(n)
					}
				}
				if v, ok := x.get("code"); ok {
					if s, ok := v.(string); ok && s != "erro" {
						e.Code = s
					}
				}
				e.Data, _ = x.get("data")
				if v, ok := x.get("file"); ok {
					e.File, _ = v.(string)
				}
				if e.Msg == "" {
					fail(line, "reject: o mapa não tem 'msg' (use o erro que o error{ [e] } recebe)")
				}
				panic(e)
			}
			fail(line, "reject: espera uma mensagem (texto) ou o erro de um error{ [e] }")
		case 2, 3:
			code, ok1 := a[0].(string)
			msg, ok2 := a[1].(string)
			if !ok1 || !ok2 {
				fail(line, "reject: o código e a mensagem são texto: reject:\"codigo\":\"mensagem\":dados")
			}
			if code == "" || code == "erro" {
				fail(line, "reject: o código %q é reservado aos erros da própria linguagem; escolha outro", code)
			}
			e := &djoError{Line: line, Msg: msg, Code: code}
			if len(a) == 3 {
				e.Data = a[2]
			}
			panic(e)
		}
		fail(line, "reject: use reject:\"mensagem\", reject:\"codigo\":\"mensagem\":dados ou reject:e")
		return nil
	}}
}
