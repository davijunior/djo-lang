//go:build !noweb

package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// returns: o tipo da resposta de uma rota (ou action) de agente.
//
//	"returns": "Post"        um objeto Post
//	"returns": ["Post"]      uma lista de Post
//
// O tipo vai para o manifesto como JSON Schema e permite o ?campos=, que devolve só os
// campos pedidos. Em 'djo serve -dev' a resposta é conferida contra o tipo.

// tnode é a descrição do tipo, montada uma vez ao ler o rotas.djo (nunca toca nas
// structs da linguagem durante as requisições).
type tnode struct {
	kind   string // Int, Float, String, Bool, Arr, Map, Any ou obj
	title  string
	fields map[string]*tnode
	order  []string
	open   bool // estrutura funda demais: não confere nem lista os campos
}

type retSpec struct {
	list bool
	node *tnode
}

const maxNodeDepth = 4

func (in *Interp) buildNode(typ string, depth int) *tnode {
	if validTypes[typ] {
		return &tnode{kind: typ}
	}
	sd := in.structs[typ]
	if sd == nil {
		return &tnode{kind: "Any"}
	}
	n := &tnode{kind: "obj", title: typ, fields: map[string]*tnode{}}
	if depth >= maxNodeDepth {
		n.open = true
		return n
	}
	in.link(sd, sd.Line)
	for _, f := range sd.Fields {
		n.fields[f.Name] = in.buildNode(f.Type, depth+1)
		n.order = append(n.order, f.Name)
	}
	return n
}

func (in *Interp) parseReturns(line int, ctx string, v any) *retSpec {
	spec := &retSpec{}
	name := ""
	switch x := v.(type) {
	case string:
		name = x
	case *Array:
		if len(x.Items) == 1 {
			if s, ok := x.Items[0].(string); ok {
				name, spec.list = s, true
			}
		}
	}
	if name == "" {
		fail(line, "%s: \"returns\" é o nome de um tipo (\"Post\") ou uma lista com um tipo ([\"Post\"])", ctx)
	}
	if name == "Any" || (!validTypes[name] && in.structs[name] == nil) {
		fail(line, "%s: \"returns\": o tipo '%s' não existe (use Int, Float, String, Bool, Date, DateTime, Arr, Map ou uma struct do core/)", ctx, name)
	}
	spec.node = in.buildNode(name, 0)
	return spec
}

func (n *tnode) schema() map[string]any {
	switch n.kind {
	case "Int":
		return map[string]any{"type": "integer"}
	case "Float":
		return map[string]any{"type": "number"}
	case "String":
		return map[string]any{"type": "string"}
	case "Bool":
		return map[string]any{"type": "boolean"}
	case "Arr":
		return map[string]any{"type": "array"}
	case "Map":
		return map[string]any{"type": "object"}
	case "Date":
		return map[string]any{"type": "string", "format": "date"}
	case "DateTime":
		return map[string]any{"type": "string", "format": "date-time"}
	case "obj":
		props := map[string]any{}
		for k, f := range n.fields {
			props[k] = f.schema()
		}
		return map[string]any{"type": "object", "title": n.title, "properties": props}
	}
	return map[string]any{}
}

func (r *retSpec) schema() map[string]any {
	if r.list {
		return map[string]any{"type": "array", "items": r.node.schema()}
	}
	return r.node.schema()
}

// paths lista os campos que o ?campos= aceita (com ponto para os aninhados).
func (n *tnode) paths(prefix string) []string {
	var out []string
	for _, k := range n.order {
		out = append(out, prefix+k)
		if f := n.fields[k]; f.kind == "obj" {
			out = append(out, f.paths(prefix+k+".")...)
		}
	}
	return out
}

// ---- ?campos= ----

type sel map[string]sel // árvore dos campos pedidos; nil = o campo inteiro

func parseCampos(raw string, root *tnode) (sel, error) {
	tree := sel{}
	valid := map[string]bool{}
	for _, p := range root.paths("") {
		valid[p] = true
	}
	seen := 0
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		seen++
		if seen > 50 {
			return nil, fmt.Errorf("campos demais (máximo 50)")
		}
		if !valid[p] {
			all := root.paths("")
			sort.Strings(all)
			return nil, fmt.Errorf("campo desconhecido '%s'. Campos válidos: %s", p, strings.Join(all, ", "))
		}
		cur := tree
		parts := strings.Split(p, ".")
		for i, part := range parts {
			next, exists := cur[part]
			switch {
			case i == len(parts)-1:
				cur[part] = nil // o campo inteiro vale mais que partes dele
			case exists && next == nil:
				cur = nil // o campo inteiro já foi pedido: o resto é redundante
			case !exists:
				cur[part] = sel{}
				cur = cur[part]
			default:
				cur = next
			}
			if cur == nil {
				break
			}
		}
	}
	if seen == 0 {
		return nil, fmt.Errorf("?campos= está vazio")
	}
	return tree, nil
}

func project(v any, s sel) any {
	switch x := v.(type) {
	case *Array:
		out := &Array{Items: make([]any, len(x.Items))}
		for i, it := range x.Items {
			out.Items[i] = project(it, s)
		}
		return out
	case *Map:
		out := newMap()
		for i, k := range x.Keys {
			ks, _ := k.(string)
			sub, want := s[ks]
			if !want {
				continue
			}
			if sub == nil {
				out.set(k, x.Vals[i])
			} else {
				out.set(k, project(x.Vals[i], sub))
			}
		}
		return out
	}
	return v
}

// ---- conferência em -dev ----

func conform(v any, n *tnode, path string, errs *[]string) {
	bad := func(want string) {
		*errs = append(*errs, fmt.Sprintf("%s deveria ser %s, mas veio %s", path, want, typeName(v)))
	}
	if len(*errs) >= 5 {
		return
	}
	switch n.kind {
	case "Int":
		if _, ok := v.(int64); !ok {
			bad("Int")
		}
	case "Float":
		switch v.(type) {
		case int64, float64:
		default:
			bad("Float")
		}
	case "String":
		if _, ok := v.(string); !ok {
			bad("String")
		}
	case "Bool":
		if _, ok := v.(bool); !ok {
			bad("Bool")
		}
	case "Arr":
		if _, ok := v.(*Array); !ok {
			bad("Arr")
		}
	case "Map":
		if _, ok := v.(*Map); !ok {
			bad("Map")
		}
	case "Date":
		if s, isStr := v.(string); v != nil {
			if _, ok := parseDate(s); !isStr || !ok {
				bad("Date (AAAA-MM-DD)")
			}
		}
	case "DateTime":
		if s, isStr := v.(string); v != nil {
			if _, ok := parseDateTime(s); !isStr || !ok {
				bad("DateTime (AAAA-MM-DDTHH:MM:SSZ)")
			}
		}
	case "obj":
		if v == nil || n.open {
			return
		}
		m, ok := v.(*Map)
		if !ok {
			bad("um objeto " + n.title)
			return
		}
		for i, k := range m.Keys {
			ks := fmt.Sprint(k)
			f, known := n.fields[ks]
			if !known {
				*errs = append(*errs, fmt.Sprintf("%s.%s não é um campo de %s", path, ks, n.title))
				continue
			}
			conform(m.Vals[i], f, path+"."+ks, errs)
		}
	}
}

func (r *retSpec) conformTo(v any) []string {
	var errs []string
	if r.list {
		a, ok := v.(*Array)
		if !ok {
			return []string{"a resposta deveria ser uma lista, mas veio " + typeName(v)}
		}
		for i, it := range a.Items {
			conform(it, r.node, fmt.Sprintf("[%d]", i), &errs)
		}
		return errs
	}
	conform(v, r.node, "resposta", &errs)
	return errs
}

// reshape confere (em -dev) e recorta (?campos=) o corpo JSON de uma resposta 200 de agente.
func (s *Server) reshape(c *captured, rt *route, campos sel, tab bool) {
	if rt.returns == nil || c.status != http.StatusOK || !strings.Contains(c.h.Get("Content-Type"), "json") {
		return
	}
	v, err := jsonDecode(c.body.String())
	if err != nil {
		return
	}
	if s.dev {
		if errs := rt.returns.conformTo(v); len(errs) > 0 {
			c.status = http.StatusInternalServerError
			c.body.Reset()
			c.h.Set("Content-Type", "application/json; charset=utf-8")
			msg := "a resposta não bate com o \"returns\" da rota: " + strings.Join(errs, "; ")
			fmt.Fprintf(&c.body, `{"erro":%s}`, jsonEncode(msg, "", 0))
			return
		}
	}
	if tab {
		if a, ok := v.(*Array); ok {
			c.body.Reset()
			c.body.WriteString(tabela(a, rt.returns.node, campos))
			c.h.Set("Content-Type", "text/csv; charset=utf-8")
		}
		return
	}
	if campos == nil {
		return
	}
	c.body.Reset()
	c.body.WriteString(jsonEncode(project(v, campos), "", 0))
}
