package main

import (
	"bufio"
	"fmt"
	"html"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

type Env struct {
	vars   map[string]any
	locked map[string]string // variável -> tipo travado (vem de params tipados)
	parent *Env
	// promise: uma visão "de fora". Lê (e guarda uma cópia funda) as variáveis de src;
	// escrever nelas é erro (o promise devolve o resultado só pelo return).
	outer bool
	src   *Env
}

func newEnv(parent *Env) *Env {
	return &Env{vars: map[string]any{}, locked: map[string]string{}, parent: parent}
}

func (e *Env) get(name string) (any, bool) {
	for c := e; c != nil; c = c.parent {
		if v, ok := c.vars[name]; ok {
			return v, true
		}
		if c.src != nil {
			if v, ok := c.src.get(name); ok {
				v = deepCopy(v)
				c.vars[name] = v
				return v, true
			}
		}
	}
	return nil, false
}

// isOuter diz se o nome resolve para uma variável de fora de um promise.
func (e *Env) isOuter(name string) bool {
	for c := e; c != nil; c = c.parent {
		if _, ok := c.vars[name]; ok {
			return c.outer
		}
		if c.src != nil {
			if _, ok := c.src.get(name); ok {
				return true
			}
		}
	}
	return false
}

type assignResult int

const (
	assigned assignResult = iota
	notFound
	typeLocked
	outerLocked // variável de fora de um promise
)

// assign respeita o tipo travado; devolve o tipo exigido quando recusa.
func (e *Env) assign(name string, v any) (assignResult, string) {
	for c := e; c != nil; c = c.parent {
		if _, ok := c.vars[name]; !ok && c.src != nil {
			if _, ok := c.src.get(name); ok {
				return outerLocked, ""
			}
		}
		if _, ok := c.vars[name]; ok {
			if c.outer {
				return outerLocked, ""
			}
			if typ, ok := c.locked[name]; ok {
				cv, ok := coerce(typ, v)
				if !ok {
					return typeLocked, typ
				}
				v = cv
			}
			c.vars[name] = v
			return assigned, ""
		}
	}
	return notFound, ""
}

// Valores: int64, float64, string, bool, nil e *Array.
type Interp struct {
	out        io.Writer
	stdin      *bufio.Reader
	global     *Env
	funcs      map[string]*FuncDecl
	structs    map[string]*StructDecl
	interfaces map[string]*InterfaceDecl
	depth      int
	root       string // pasta do site: base do include
	webState
	serial          serialState
	served          bool          // páginas servidas não alcançam endereços internos
	outCalls        int           // chamadas externas feitas nesta página
	outWaited       time.Duration // tempo já esperado em chamadas externas nesta página
	slept           int64         // ms de Time.sleep nesta página (servidor)
	wrote           int           // bytes gravados por File.* nesta página (servidor)
	steps, maxSteps int           // maxSteps>0 limita a execução (servidor)
	Echo            bool          // REPL: mostra o valor de expressões soltas
	dbs             dbState
	ask             *askConfig          // só enquanto o ask.djo é lido
	boosters        map[string]*booster // boosters/ do projeto, pelo nome
	dbRoot          string              // pasta dos bancos SQLite (vazio: a pasta do site); o djo spec usa uma temporária
	deadline        time.Time           // limite de tempo da execução (o djo spec); zero = sem limite
	spec            *specCtx            // só existe sob "djo spec"
	outerLine       int                 // linha, no código do projeto, da última chamada a um booster
	pStructs        map[string]*StructDecl
	pIfaces         map[string]*InterfaceDecl
	cur             *booster         // booster em execução (nil: core ou página)
	job             *jobHost         // Job.run: onde enfileirar (nil fora do servidor e do djo spec)
	sleepMax        int64            // >0: limite de Time.sleep em ms por execução (jobs)
	outMax          int              // >0: limite de chamadas externas por execução (jobs)
	schedLoad       *[]*schedDef     // só enquanto o agenda.djo é lido
	mail            *mailHost        // o que fazer com Mail.send numa página servida (nil: enviar de verdade)
	promOuterTx     bool             // há Db.begin aberto fora do promise em execução
	prom            int              // >0: dentro de um promise (não altera a resposta nem a sessão)
	pend            []*promiseHandle // promises começados e ainda não entregues (fim da função/página)
}

const maxDepth = 2000

func NewInterp(out io.Writer) *Interp {
	return &Interp{out: out, stdin: bufio.NewReader(os.Stdin), root: ".", global: newEnv(nil), funcs: map[string]*FuncDecl{}, structs: map[string]*StructDecl{}, interfaces: map[string]*InterfaceDecl{}}
}

func (in *Interp) Run(prog []Stmt) (err error) {
	defer in.closeTx() // transação esquecida aberta é desfeita
	defer in.closeSerials()
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(haltSignal); ok {
				return // redirect: encerra a página sem erro
			}
			if de, ok := r.(*djoError); ok {
				err = de
				return
			}
			panic(r)
		}
	}()
	in.depth = 0
	in.execBlock(prog, in.global)
	in.awaitPending(0)
	return nil
}

func (in *Interp) execBlock(stmts []Stmt, env *Env) (any, bool) {
	for _, s := range stmts { // funções valem antes da declaração
		switch d := s.(type) {
		case *FuncDecl:
			in.funcs[d.Name] = d
		case *StructDecl:
			in.declare(d.Name, d.Line, d, nil)
		case *InterfaceDecl:
			in.declare(d.Name, d.Line, nil, d)
		}
	}
	for _, s := range stmts {
		if v, returned := in.exec(s, env); returned {
			return v, true
		}
	}
	return nil, false
}

func (in *Interp) tick() {
	in.steps++
	if in.maxSteps > 0 && in.steps > in.maxSteps {
		if in.spec != nil {
			failFatal(0, "o spec passou do limite de passos (um laço sem fim?) e foi interrompido")
		}
		if in.job != nil && in.job.inJob {
			failFatal(0, "o job passou do limite de passos e foi interrompido")
		}
		failFatal(0, "a página demorou demais e foi interrompida (limite de passos)")
	}
	if in.steps&0x3ff == 0 && !in.deadline.IsZero() && time.Now().After(in.deadline) {
		if in.job != nil && in.job.inJob && in.spec == nil {
			failFatal(0, "o job passou do tempo limite (timeout) e foi interrompido")
		}
		failFatal(0, "o spec demorou demais e foi interrompido (limite de tempo)")
	}
}

func (in *Interp) exec(s Stmt, env *Env) (any, bool) {
	in.tick()
	switch n := s.(type) {
	case *FuncDecl, *StructDecl, *InterfaceDecl:
		// já registradas em execBlock
	case *VarStmt:
		v := in.evalValue(n.Init, env)
		if n.IsArr {
			if _, ok := v.(*Array); !ok {
				fail(n.Line, "'arr %s' precisa receber uma lista, mas recebeu %s", n.Name, typeName(v))
			}
			env.locked[n.Name] = "Arr"
		}
		if n.IsMap {
			if _, ok := v.(*Map); !ok {
				fail(n.Line, "'map %s' precisa receber um mapa, mas recebeu %s", n.Name, typeName(v))
			}
			env.locked[n.Name] = "Map"
		}
		env.vars[n.Name] = v
	case *AssignStmt:
		v := in.evalValue(n.Value, env)
		if n.Member != nil {
			in.checkOuterRoot(n.Member.X, env, n.Line)
			in.setField(n.Member, v, env)
			break
		}
		if n.Target != nil {
			in.checkOuterRoot(n.Target.X, env, n.Line)
			if m, ok := in.eval(n.Target.X, env).(*Map); ok {
				m.set(mapKey(in.eval(n.Target.Idx, env), n.Line), v)
				break
			}
			arr, i := in.indexTarget(n.Target, env)
			arr.Items[i] = v
			break
		}
		switch res, typ := env.assign(n.Name, v); res {
		case notFound:
			fail(n.Line, "variável '%s' não existe; declare com 'var %s = ...'", n.Name, n.Name)
		case typeLocked:
			fail(n.Line, "'%s' é %s e não pode receber %s", n.Name, typ, typeName(v))
		case outerLocked:
			fail(n.Line, outerMsg, n.Name)
		}
	case *IncStmt:
		if n.Member != nil {
			cur := in.eval(n.Member, env)
			switch x := cur.(type) {
			case int64:
				in.setField(n.Member, x+n.Delta, env)
			case float64:
				in.setField(n.Member, x+float64(n.Delta), env)
			default:
				fail(n.Line, "'++' e '--' só funcionam com número, e '%s' é %s", n.Member.Name, typeName(cur))
			}
			break
		}
		cur, ok := env.get(n.Name)
		if !ok {
			fail(n.Line, "variável '%s' não existe", n.Name)
		}
		var nv any
		switch x := cur.(type) {
		case int64:
			nv = x + n.Delta
		case float64:
			nv = x + float64(n.Delta)
		default:
			fail(n.Line, "'++' e '--' só funcionam com número, e '%s' é %s", n.Name, typeName(cur))
		}
		if res, typ := env.assign(n.Name, nv); res == typeLocked {
			fail(n.Line, "'%s' é %s e não pode receber %s", n.Name, typ, typeName(nv))
		} else if res == outerLocked {
			fail(n.Line, outerMsg, n.Name)
		}
	case *WhileStmt:
		loop := newEnv(env)
		for _, s := range n.Init {
			in.exec(s, loop)
		}
		for {
			in.tick()
			if !in.condition(n.Cond, loop, n.Line, "while") {
				break
			}
			if v, returned := in.execBlock(n.Body, newEnv(loop)); returned {
				return v, true
			}
		}
	case *ForStmt:
		var rows [][2]any // [chave ou posição, valor]
		coll := in.eval(n.Iter, env)
		switch c := coll.(type) {
		case *Array:
			for i, v := range c.Items {
				rows = append(rows, [2]any{int64(i), v})
			}
		case *Map:
			for i, k := range c.Keys {
				rows = append(rows, [2]any{k, c.Vals[i]})
			}
		case string:
			for i, r := range []rune(c) {
				rows = append(rows, [2]any{int64(i), string(r)})
			}
		default:
			fail(n.Line, "o for percorre Arr, Map ou String, mas isso é %s", typeName(c))
		}
		_, isMap := coll.(*Map)
		for _, row := range rows {
			in.tick()
			loop := newEnv(env)
			switch {
			case len(n.Vars) == 2:
				loop.vars[n.Vars[0]], loop.vars[n.Vars[1]] = row[0], row[1]
			case isMap:
				loop.vars[n.Vars[0]] = row[0] // um nome só em mapa: as chaves
			default:
				loop.vars[n.Vars[0]] = row[1]
			}
			if v, returned := in.execBlock(n.Body, loop); returned {
				return v, true
			}
		}
	case *UntilStmt:
		loop := newEnv(env)
		if a, ok := n.Init.(*AssignStmt); ok && a.Target == nil {
			loop.vars[a.Name] = in.eval(a.Value, loop) // 'i=0' declara i no laço
		} else {
			in.exec(n.Init, loop)
		}
		for !in.condition(n.Cond, loop, n.Line, "until") {
			in.tick()
			if v, returned := in.execBlock(n.Body, newEnv(loop)); returned {
				return v, true
			}
			in.exec(n.Step, loop)
		}
	case *IfStmt:
		for _, br := range n.Branches {
			if in.condition(br.Cond, env, n.Line, "if") {
				return in.execBlock(br.Body, newEnv(env))
			}
		}
		if n.HasElse {
			return in.execBlock(n.Else, newEnv(env))
		}
	case *MatchStmt:
		v := in.eval(n.Val, env)
		for _, c := range n.Cases {
			for _, cv := range c.Vals {
				if equal(v, in.eval(cv, env)) {
					return in.execBlock(c.Body, newEnv(env))
				}
			}
		}
		if n.HasElse {
			return in.execBlock(n.Else, newEnv(env))
		}
	case *EchoStmt:
		var out string
		if v := in.eval(n.Value, env); v != nil {
			out = format(v)
		}
		if !n.Raw {
			out = html.EscapeString(out)
		}
		io.WriteString(in.out, out)
	case *PrintStmt:
		fmt.Fprintln(in.out, format(in.eval(n.Value, env)))
	case *ReturnStmt:
		if n.Value == nil {
			return nil, true
		}
		return in.eval(n.Value, env), true
	case *ExprStmt:
		if pe, ok := n.X.(*PromiseExpr); ok { // instrução: roda agora, entrega no fim da função
			in.runStmtPromise(pe, env)
			break
		}
		v := in.eval(n.X, env)
		if in.Echo && in.depth == 0 && v != nil {
			fmt.Fprintln(in.out, format(v))
		}
	}
	return nil, false
}

func (in *Interp) condition(c Expr, env *Env, line int, what string) bool {
	v := in.eval(c, env)
	b, ok := v.(bool)
	if !ok {
		fail(line, "a condição do %s precisa ser Bool, mas é %s", what, typeName(v))
	}
	return b
}

func (in *Interp) boolean(v any, line int, op string) bool {
	b, ok := v.(bool)
	if !ok {
		fail(line, "'%s' precisa de Bool, mas recebeu %s", op, typeName(v))
	}
	return b
}

// indexTarget valida lista[i] para escrita.
func (in *Interp) indexTarget(t *Index, env *Env) (*Array, int) {
	arr, ok := in.eval(t.X, env).(*Array)
	if !ok {
		fail(t.Line, "só dá para atribuir por índice em uma lista (arr)")
	}
	return arr, in.checkIndex(in.eval(t.Idx, env), len(arr.Items), t.Line)
}

func (in *Interp) checkIndex(v any, n int, line int) int {
	i, ok := v.(int64)
	if !ok {
		fail(line, "o índice precisa ser Int, mas é %s", typeName(v))
	}
	if i < 0 || i >= int64(n) {
		fail(line, "índice %d fora da lista (tamanho %d)", i, n)
	}
	return int(i)
}

// readLine lê uma linha da entrada; ok=false no fim da entrada.
func (in *Interp) readLine() (string, bool) {
	line, err := in.stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimRight(line, "\r\n"), true
}

func (in *Interp) eval(e Expr, env *Env) any {
	switch n := e.(type) {
	case *IntLit:
		return n.Val
	case *FloatLit:
		return n.Val
	case *StrLit:
		return n.Val
	case *BoolLit:
		return n.Val
	case *NilLit:
		return nil
	case *Ident:
		v, ok := env.get(n.Name)
		if !ok {
			if _, isFn := in.funcs[n.Name]; isFn {
				fail(n.Line, "'%s' é uma função; para chamar use '%s:'", n.Name, n.Name)
			}
			fail(n.Line, "variável '%s' não existe", n.Name)
		}
		if h, isP := v.(*promiseHandle); isP {
			return in.await(h)
		}
		return v
	case *PromiseExpr:
		return in.await(in.startPromise(n, env))
	case *Logical:
		if n.Op == Coalesce {
			if l := in.eval(n.L, env); l != nil {
				return l
			}
			return in.eval(n.R, env)
		}
		word := map[Kind]string{KwAnd: "and", KwOr: "or"}[n.Op]
		lb := in.boolean(in.eval(n.L, env), n.Line, word)
		if (n.Op == KwAnd && !lb) || (n.Op == KwOr && lb) {
			return lb // curto-circuito
		}
		return in.boolean(in.eval(n.R, env), n.Line, word)
	case *Pick:
		if in.condition(n.Cond, env, n.Line, "pick") {
			return in.eval(n.A, env)
		}
		return in.eval(n.B, env)
	case *Unary:
		if n.Op == KwNot {
			return !in.boolean(in.eval(n.X, env), n.Line, "not")
		}
		switch v := in.eval(n.X, env).(type) {
		case int64:
			return -v
		case float64:
			return -v
		default:
			fail(n.Line, "não dá para negar %s", typeName(v))
		}
	case *ArrayLit:
		arr := &Array{}
		for _, el := range n.Elems {
			arr.Items = append(arr.Items, in.eval(el, env))
		}
		return arr
	case *MapLit:
		m := newMap()
		for i := range n.Keys {
			k := mapKey(in.eval(n.Keys[i], env), n.Line)
			m.set(k, in.eval(n.Vals[i], env))
		}
		return m
	case *Member:
		return in.member(n, env)
	case *MethodCall:
		return in.methodCall(n, env)
	case *ValLit:
		return n.V
	case *Placeholder:
		fail(n.Line, "o '.' (valor padrão) só vale ao criar uma struct, como em Post:.:\"texto\"")
	case *Index:
		base := in.eval(n.X, env)
		idx := in.eval(n.Idx, env)
		switch b := base.(type) {
		case *Map:
			v, _ := b.get(mapKey(idx, n.Line))
			return v
		case *Array:
			return b.Items[in.checkIndex(idx, len(b.Items), n.Line)]
		case string:
			rs := []rune(b)
			return string(rs[in.checkIndex(idx, len(rs), n.Line)])
		}
		fail(n.Line, "só dá para usar [ ] em Arr, Map ou String, e isso é %s", typeName(base))
	case *Input:
		if n.Prompt != nil {
			fmt.Fprint(in.out, in.eval(n.Prompt, env))
		}
		line, _ := in.readLine()
		return line
	case *Binary:
		return in.binary(n, env)
	case *Call:
		return in.call(n, env)
	}
	panic(fmt.Sprintf("expressão desconhecida: %T", e))
}

func (in *Interp) call(n *Call, env *Env) any {
	if in.cur != nil { // dentro de um booster: só enxerga as próprias funções (o core não)
		if fd, ok := in.cur.funcs[n.Name]; ok {
			return in.invoke(fd, n.Name, n.Args, env, nil, n.Line)
		}
		if fd, ok := in.cur.priv[n.Name]; ok {
			return in.invoke(fd, n.Name, n.Args, env, nil, n.Line)
		}
		if sd, ok := in.structs[n.Name]; ok { // in.structs já é o mundo do booster
			return in.construct(sd, n, env)
		}
		if b, ok := builtins[n.Name]; ok {
			return in.callBuiltin(b, n, env)
		}
		fail(n.Line, "o booster '%s' não tem a função '%s' (um booster não enxerga o core do projeto)", in.cur.name, n.Name)
	}
	if fd, ok := in.funcs[n.Name]; ok {
		return in.invoke(fd, n.Name, n.Args, env, nil, n.Line)
	}
	if sd, ok := in.structs[n.Name]; ok {
		return in.construct(sd, n, env)
	}
	if b, ok := builtins[n.Name]; ok {
		return in.callBuiltin(b, n, env)
	}
	fail(n.Line, "função ou struct '%s' não existe", n.Name)
	return nil
}

func (in *Interp) callBuiltin(b builtin, n *Call, env *Env) any {
	if b.args >= 0 && len(n.Args) != b.args {
		fail(n.Line, "'%s' espera %d argumento(s), recebeu %d", n.Name, b.args, len(n.Args))
	}
	if in.prom > 0 {
		in.promiseGuard(n.Name, n.Line)
	}
	args := make([]any, len(n.Args))
	for i, a := range n.Args {
		args[i] = in.eval(a, env)
	}
	return b.fn(in, env, n.Line, args)
}

// invoke executa uma função ou método; self é a instância (nil em funções).
func (in *Interp) invoke(fd *FuncDecl, label string, args []Expr, env *Env, self *Instance, line int) any {
	if len(args) != len(fd.Params) {
		fail(line, "'%s' espera %d argumento(s), recebeu %d", label, len(fd.Params), len(args))
	}
	local := newEnv(in.global)
	if self != nil {
		local.vars["self"] = self
		if fd.Owner != nil && fd.Owner.ParentSD != nil {
			local.vars["super"] = &superRef{self, fd.Owner.ParentSD}
		}
	}
	for i, p := range fd.Params {
		raw := in.eval(args[i], env)
		v, ok := coerce(p.Type, raw)
		if !ok {
			fail(line, "o parâmetro '%s' de '%s' deve ser %s, mas recebeu %s%s", p.Name, label, p.Type, typeName(raw), hint(p.Type, raw))
		}
		local.vars[p.Name] = v
		if p.Type != "Any" {
			local.locked[p.Name] = p.Type
		}
	}
	in.depth++
	defer func() { in.depth-- }()
	if in.depth > maxDepth {
		failFatal(line, "recursão funda demais em '%s'", label)
	}
	if fd.Booster != nil { // os argumentos já foram avaliados fora do booster
		prev, prevS, prevI := in.cur, in.structs, in.interfaces
		if prev == nil { // vindo do projeto: guarda a linha e o mundo do projeto (o Spec.try volta para ele)
			in.outerLine, in.pStructs, in.pIfaces = line, prevS, prevI
		}
		in.cur, in.structs, in.interfaces = fd.Booster, fd.Booster.structs, fd.Booster.interfaces
		defer func() {
			in.cur, in.structs, in.interfaces = prev, prevS, prevI
			if r := recover(); r != nil { // o erro que nasce no booster diz de onde veio (o "file" do promise)
				if de, ok := r.(*djoError); ok && de.File == "" {
					de.File = boostersDir + "/" + fd.Booster.name
				}
				panic(r)
			}
		}()
	}
	mark := len(in.pend)
	defer func() { // saiu por erro: os promises pendentes desta função se perdem
		if len(in.pend) > mark {
			in.pend = in.pend[:mark]
		}
	}()
	ret, _ := in.execBlock(fd.Body, local)
	in.awaitPending(mark)
	return ret
}

func (in *Interp) binary(n *Binary, env *Env) any {
	l, r := in.eval(n.L, env), in.eval(n.R, env)

	dateMixCheck(l, r, n.Line)
	if res, ok := dateBinary(n.Op, l, r, n.Line); ok {
		return res
	}

	switch n.Op {
	case Eq:
		return equal(l, r)
	case Neq:
		return !equal(l, r)
	}

	// '+' com texto e número: o texto precisa ser um número ("2" + 2 = 4)
	if n.Op == Plus {
		_, lStr := l.(string)
		_, rStr := r.(string)
		if lStr != rStr {
			a, aok := asNumber(l)
			b, bok := asNumber(r)
			if aok && bok {
				if ai, ok := a.(int64); ok {
					if bi, ok := b.(int64); ok {
						return ai + bi
					}
				}
				af, _ := toFloat(a)
				bf, _ := toFloat(b)
				return af + bf
			}
		}
	}

	// string + string
	if ls, ok := l.(string); ok {
		if rs, ok := r.(string); ok {
			switch n.Op {
			case Plus:
				return ls + rs
			case Lt:
				return ls < rs
			case Gt:
				return ls > rs
			case Lte:
				return ls <= rs
			case Gte:
				return ls >= rs
			}
		}
	}

	li, lInt := l.(int64)
	ri, rInt := r.(int64)
	if lInt && rInt {
		switch n.Op {
		case Plus:
			return li + ri
		case Minus:
			return li - ri
		case Star:
			return li * ri
		case Slash:
			if ri == 0 {
				fail(n.Line, "divisão por zero")
			}
			return li / ri
		case Percent:
			if ri == 0 {
				fail(n.Line, "divisão por zero")
			}
			return li % ri
		case Lt:
			return li < ri
		case Gt:
			return li > ri
		case Lte:
			return li <= ri
		case Gte:
			return li >= ri
		}
	}

	lf, lNum := toFloat(l)
	rf, rNum := toFloat(r)
	if lNum && rNum {
		switch n.Op {
		case Plus:
			return lf + rf
		case Minus:
			return lf - rf
		case Star:
			return lf * rf
		case Slash:
			if rf == 0 {
				fail(n.Line, "divisão por zero")
			}
			return lf / rf
		case Lt:
			return lf < rf
		case Gt:
			return lf > rf
		case Lte:
			return lf <= rf
		case Gte:
			return lf >= rf
		}
	}

	fail(n.Line, "não dá para usar '%s' entre %s e %s",
		opText(n.Op), typeName(l), typeName(r))
	return nil
}

// asNumber aceita Int, Float e texto que seja número ("2", "3.5").
func asNumber(v any) (any, bool) {
	switch x := v.(type) {
	case int64, float64:
		return x, true
	case string:
		t := strings.TrimSpace(x)
		if i, err := strconv.ParseInt(t, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, true
		}
	}
	return nil, false
}

func opText(k Kind) string {
	return map[Kind]string{Plus: "+", Minus: "-", Star: "*", Slash: "/", Percent: "%",
		Lt: "<", Gt: ">", Lte: "<=", Gte: ">="}[k]
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func equal(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
		return false
	}
	return a == b
}

func typeName(v any) string {
	switch x := v.(type) {
	case *Instance:
		return x.Def.Name
	case int64:
		return "Int"
	case float64:
		return "Float"
	case string:
		return "String"
	case bool:
		return "Bool"
	case *Array:
		return "Arr"
	case *Map:
		return "Map"
	case Date:
		return "Date"
	case DateTime:
		return "DateTime"
	case nil:
		return "Nil"
	}
	return "?"
}

// coerce confere o tipo declarado em params; Int é aceito onde se pede Float.
func coerce(typ string, v any) (any, bool) {
	switch typ {
	case "Any":
		return v, true
	case "Float":
		if i, ok := v.(int64); ok {
			return float64(i), true
		}
	}
	if v == nil && (!validTypes[typ] || typ == "Date" || typ == "DateTime") {
		return nil, true // campo do tipo de uma struct pode estar vazio
	}
	if inst, ok := v.(*Instance); ok {
		if isA(inst.Def, typ) {
			return v, true
		}
		if w := inst.Def.in; w != nil {
			if id, found := w.interfaces[typ]; found && w.missing(inst.Def, id) == "" {
				return v, true
			}
		}
	}
	return v, typeName(v) == typ
}

func format(v any) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case *Instance:
		parts := make([]string, len(x.Vals))
		for i, f := range x.Def.Fields {
			val := format(x.Vals[i])
			if str, ok := x.Vals[i].(string); ok {
				val = strconv.Quote(str)
			}
			parts[i] = f.Name + ": " + val
		}
		return x.Def.Name + "(" + strings.Join(parts, ", ") + ")"
	case *Map:
		return formatMap(x)
	case *Array:
		parts := make([]string, len(x.Items))
		for i, it := range x.Items {
			if str, ok := it.(string); ok {
				parts[i] = strconv.Quote(str)
			} else {
				parts[i] = format(it)
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case Date:
		return x.String()
	case DateTime:
		return x.String()
	case float64:
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eIN") {
			s += ".0"
		}
		return s
	}
	return fmt.Sprint(v)
}
