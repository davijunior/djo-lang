package main

import (
	"fmt"
	"sort"
	"strings"
)

// Instance é um objeto criado a partir de uma struct. Tem semântica de
// referência: duas variáveis podem apontar para o mesmo objeto.
type Instance struct {
	Def  *StructDecl
	Vals []any // na ordem de Def.Fields
}

// link junta os campos e métodos do pai (sonof) aos da struct, uma única vez.
func (in *Interp) link(sd *StructDecl, line int) {
	sd.in = in
	if sd.linked || sd.Parent == "" {
		sd.linked = true
		return
	}
	if sd.linking {
		fail(line, "herança circular em %s", sd.Name)
	}
	sd.linking = true
	defer func() { sd.linking = false }()
	pd, ok := in.structs[sd.Parent]
	if !ok {
		fail(sd.Line, "%s é filha de '%s', que não existe", sd.Name, sd.Parent)
	}
	in.link(pd, line)
	sd.ParentSD = pd
	fields := append([]FieldDecl(nil), pd.Fields...)
	idx := map[string]int{}
	for i, f := range fields {
		idx[f.Name] = i
	}
	for _, f := range sd.own {
		if _, dup := idx[f.Name]; dup {
			fail(sd.Line, "o campo '%s' de %s já existe em %s", f.Name, sd.Name, pd.Name)
		}
		idx[f.Name] = len(fields)
		fields = append(fields, f)
	}
	methods := map[string]*FuncDecl{}
	for k, m := range pd.methods {
		methods[k] = m
	}
	for k, m := range sd.methods {
		methods[k] = m
	}
	for k, m := range methods {
		if _, clash := idx[k]; clash {
			fail(m.Line, "'%s' é campo e método ao mesmo tempo em %s", k, sd.Name)
		}
	}
	sd.Fields, sd.fieldIdx, sd.methods = fields, idx, methods
	sd.linked = true
}

// declare registra uma struct ou interface; os nomes são únicos.
func (in *Interp) declare(name string, line int, sd *StructDecl, id *InterfaceDecl) {
	_, isS := in.structs[name]
	_, isI := in.interfaces[name]
	if (isS || isI) && !(sd != nil && in.structs[name] == sd) && !(id != nil && in.interfaces[name] == id) {
		// recarregar o mesmo nome (REPL) é permitido; misturar struct com interface não
		if (sd != nil && isI) || (id != nil && isS) {
			fail(line, "'%s' já existe como %s", name, map[bool]string{true: "interface", false: "struct"}[isI])
		}
	}
	if sd != nil {
		in.structs[name] = sd
	} else {
		in.interfaces[name] = id
	}
}

// methodDef acha a implementação de um método numa struct: própria, herdada ou promovida
// de uma struct incluída.
func (in *Interp) methodDef(sd *StructDecl, name string, depth int) *FuncDecl {
	if depth > 10 {
		return nil
	}
	in.link(sd, sd.Line)
	if m, ok := sd.methods[name]; ok {
		return m
	}
	for _, f := range sd.Fields {
		if f.Embed {
			if esd, ok := in.structs[f.Type]; ok {
				if m := in.methodDef(esd, name, depth+1); m != nil {
					return m
				}
			}
		}
	}
	return nil
}

// missing diz o que falta para sd cumprir a interface ("" = cumpre).
func (in *Interp) missing(sd *StructDecl, id *InterfaceDecl) string {
	for _, want := range id.Methods {
		got := in.methodDef(sd, want.Name, 0)
		if got == nil {
			return fmt.Sprintf("%s não tem o método '%s'", sd.Name, want.Name)
		}
		same := len(got.Params) == len(want.Params)
		for i := 0; same && i < len(got.Params); i++ {
			same = got.Params[i].Type == want.Params[i].Type
		}
		if !same {
			return fmt.Sprintf("o método '%s' de %s tem parâmetros diferentes dos de %s", want.Name, sd.Name, id.Name)
		}
	}
	return ""
}

// hint explica por que um valor não serve para uma interface.
func hint(typ string, v any) string {
	if inst, ok := v.(*Instance); ok && inst.Def.in != nil {
		if id, found := inst.Def.in.interfaces[typ]; found {
			return " (" + inst.Def.in.missing(inst.Def, id) + ")"
		}
	}
	return ""
}

// isA diz se a struct é a de nome 'name' ou descende dela.
func isA(sd *StructDecl, name string) bool {
	for ; sd != nil; sd = sd.ParentSD {
		if sd.Name == name {
			return true
		}
	}
	return false
}

// superRef é o 'super' de dentro de um método: o objeto atual e a struct pai.
type superRef struct {
	self   *Instance
	parent *StructDecl
}

func (in *Interp) zeroValue(f FieldDecl, owner string, line int) any {
	switch f.Type {
	case "Int":
		return int64(0)
	case "Float":
		return float64(0)
	case "String":
		return ""
	case "Bool":
		return false
	case "Arr":
		return &Array{}
	case "Map":
		return newMap()
	case "Any", "Date", "DateTime":
		return nil
	}
	if _, ok := in.structs[f.Type]; ok || in.interfaces[f.Type] != nil {
		return nil // campo do tipo de outra struct começa vazio
	}
	fail(line, "o campo '%s' de %s usa o tipo '%s', que não existe", f.Name, owner, f.Type)
	return nil
}

// construct cria a instância: Post:"titulo":"texto":true
// Os argumentos seguem a ordem dos campos, pulando os 'nocall'. Pode faltar
// argumento no fim, e '.' no meio pede o valor padrão do campo.
func (in *Interp) construct(sd *StructDecl, n *Call, env *Env) any {
	in.link(sd, n.Line)
	var callable []int
	for i, f := range sd.Fields {
		if !f.NoCall {
			callable = append(callable, i)
		}
	}
	if len(n.Args) > len(callable) {
		fail(n.Line, "%s aceita no máximo %d argumento(s), recebeu %d", sd.Name, len(callable), len(n.Args))
	}
	given := make([]any, len(sd.Fields))
	has := make([]bool, len(sd.Fields))
	for k, a := range n.Args {
		if _, empty := a.(*Placeholder); empty {
			continue
		}
		i := callable[k]
		given[i], has[i] = in.eval(a, env), true
	}

	inst := &Instance{Def: sd, Vals: make([]any, len(sd.Fields))}
	for i, f := range sd.Fields {
		var v any
		switch {
		case has[i]:
			v = given[i]
		case f.Default != nil:
			v = in.eval(f.Default, in.global) // avaliado a cada criação
		default:
			v = in.zeroValue(f, sd.Name, n.Line)
		}
		cv, ok := coerce(f.Type, v)
		if !ok {
			fail(n.Line, "o campo '%s' de %s deve ser %s, mas recebeu %s%s", f.Name, sd.Name, f.Type, typeName(v), hint(f.Type, v))
		}
		inst.Vals[i] = cv
	}
	return inst
}

func (in *Interp) instance(v any, line int, what string) *Instance {
	inst, ok := v.(*Instance)
	if !ok {
		fail(line, "só dá para %s em uma struct, mas isso é %s", what, typeName(v))
	}
	return inst
}

// lookupField acha o campo na própria struct ou nas incluídas (a de fora vence).
func lookupField(inst *Instance, name string) (*Instance, int, bool) {
	if i, ok := inst.Def.fieldIdx[name]; ok {
		return inst, i, true
	}
	for i, f := range inst.Def.Fields {
		if sub, ok := inst.Vals[i].(*Instance); f.Embed && ok {
			if o, j, found := lookupField(sub, name); found {
				return o, j, true
			}
		}
	}
	return nil, 0, false
}

// lookupMethod acha o método; self dentro dele é a struct que o declarou.
func lookupMethod(inst *Instance, name string) (*Instance, *FuncDecl, bool) {
	if m, ok := inst.Def.methods[name]; ok {
		return inst, m, true
	}
	for i, f := range inst.Def.Fields {
		if sub, ok := inst.Vals[i].(*Instance); f.Embed && ok {
			if o, m, found := lookupMethod(sub, name); found {
				return o, m, true
			}
		}
	}
	return nil, nil, false
}

func (in *Interp) member(n *Member, env *Env) any {
	v := in.eval(n.X, env)
	if r, ok := dateProp(v, n.Name, n.Line); ok {
		return r
	}
	inst := in.instance(v, n.Line, "acessar o campo '"+n.Name+"'")
	owner, i, ok := lookupField(inst, n.Name)
	if ok {
		return owner.Vals[i]
	}
	if !ok {
		if _, _, isMethod := lookupMethod(inst, n.Name); isMethod {
			fail(n.Line, "'%s' é um método de %s; para chamar use '.%s:'", n.Name, inst.Def.Name, n.Name)
		}
		fail(n.Line, "%s não tem o campo '%s'", inst.Def.Name, n.Name)
	}
	return nil
}

func (in *Interp) setField(m *Member, v any, env *Env) {
	inst := in.instance(in.eval(m.X, env), m.Line, "mudar o campo '"+m.Name+"'")
	owner, i, ok := lookupField(inst, m.Name)
	if !ok {
		fail(m.Line, "%s não tem o campo '%s'", inst.Def.Name, m.Name)
	}
	f := owner.Def.Fields[i]
	cv, ok := coerce(f.Type, v)
	if !ok {
		fail(m.Line, "'%s' é %s e não pode receber %s", m.Name, f.Type, typeName(v))
	}
	owner.Vals[i] = cv
}

func (in *Interp) methodCall(n *MethodCall, env *Env) any {
	if id, ok := n.X.(*Ident); ok {
		if ns, isNative := natives[id.Name]; isNative {
			if _, shadow := env.get(id.Name); !shadow {
				in.checkPerm(id.Name, n.Line)
				b, ok := ns[n.Name]
				if !ok {
					failc(n.Line, codeUnknownMember, "%s não tem a função '%s'%s", id.Name, n.Name, didYouMean(n.Name, nsFuncNames(id.Name)))
				}
				if b.args >= 0 && len(n.Args) != b.args {
					fail(n.Line, "%s.%s espera %d argumento(s), recebeu %d", id.Name, n.Name, b.args, len(n.Args))
				}
				if in.prom > 0 {
					in.promiseGuard(id.Name+"."+n.Name, n.Line)
				}
				args := make([]any, len(n.Args))
				for i, a := range n.Args {
					args[i] = in.eval(a, env)
				}
				return b.fn(in, env, n.Line, args)
			}
		}
	}
	if id, ok := n.X.(*Ident); ok && in.cur == nil {
		if bo, isBooster := in.boosters[id.Name]; isBooster {
			if _, shadow := env.get(id.Name); !shadow {
				fd, ok := bo.funcs[n.Name]
				if !ok {
					var exposed []string
					for k := range bo.funcs {
						exposed = append(exposed, k)
					}
					sort.Strings(exposed)
					failc(n.Line, codeBoosterFn, "o booster %s não tem a função '%s'; ele expõe: %s%s", id.Name, n.Name, strings.Join(exposed, ", "), didYouMean(n.Name, exposed))
				}
				return in.invoke(fd, id.Name+"."+n.Name, n.Args, env, nil, n.Line)
			}
		}
	}
	if id, ok := n.X.(*Ident); ok && id.Name == "super" {
		v, _ := env.get("super")
		sr, ok := v.(*superRef)
		if !ok {
			fail(n.Line, "'super' só existe dentro de método de uma struct com sonof")
		}
		m, ok := sr.parent.methods[n.Name]
		if !ok {
			fail(n.Line, "%s não tem o método '%s' para o super chamar", sr.parent.Name, n.Name)
		}
		return in.invoke(m, sr.parent.Name+"."+n.Name, n.Args, env, sr.self, n.Line)
	}
	recv := in.eval(n.X, env)
	if r, ok := in.dateMethod(recv, n, env); ok {
		return r
	}
	inst := in.instance(recv, n.Line, "chamar o método '"+n.Name+"'")
	owner, m, ok := lookupMethod(inst, n.Name)
	if !ok {
		if _, _, isField := lookupField(inst, n.Name); isField {
			fail(n.Line, "'%s' é um campo de %s, não um método", n.Name, inst.Def.Name)
		}
		fail(n.Line, "%s não tem o método '%s'", inst.Def.Name, n.Name)
	}
	return in.invoke(m, owner.Def.Name+"."+n.Name, n.Args, env, owner, n.Line)
}
