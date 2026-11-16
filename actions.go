//go:build !noweb

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// action: uma função com tipos fixos que também vira endpoint para agentes.
//
//	action criar_post
//	    "Cria um post novo"
//	    params[titulo[String], texto[String]]
//	    ...
//
// Em rotas.djo: Route.action:"/posts":"criar_post" (POST, corpo JSON; também Route.put_action:, patch_action: e delete_action:). A entrada é
// conferida contra os tipos, e o manifesto descreve a ação com um JSON Schema.

const manifestPath = "/.well-known/ai-manifest.json"

const schemaDepth = 6

// ---- JSON Schema a partir dos tipos ----

func (in *Interp) schemaOf(typ string, depth int) map[string]any {
	switch typ {
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
	}
	sd, ok := in.structs[typ]
	if !ok || depth > schemaDepth {
		return map[string]any{"type": "object"}
	}
	in.link(sd, sd.Line)
	props := map[string]any{}
	var required []string
	for _, f := range sd.Fields {
		if f.NoCall {
			continue
		}
		if f.Type == "Any" {
			props[f.Name] = map[string]any{}
		} else {
			props[f.Name] = in.schemaOf(f.Type, depth+1)
		}
		if f.Default == nil {
			required = append(required, f.Name)
		}
	}
	out := map[string]any{"type": "object", "title": typ, "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// checkAction confere que os parâmetros da action têm tipos que dá para descrever e receber.
func (in *Interp) checkAction(fd *FuncDecl, line int) {
	for _, p := range fd.Params {
		switch {
		case p.Type == "Any":
			fail(line, "a action '%s' precisa de um tipo em cada parâmetro (o '%s' não tem), para o agente saber o que enviar", fd.Name, p.Name)
		case validTypes[p.Type]:
		case in.structs[p.Type] != nil:
		default:
			fail(line, "a action '%s': o tipo '%s' do parâmetro '%s' não é aceito (use Int, Float, String, Bool, Date, DateTime, Arr, Map ou uma struct)", fd.Name, p.Type, p.Name)
		}
	}
}

// inputSchema é o JSON Schema do corpo da action (sem os parâmetros que vêm da URL).
func (in *Interp) inputSchema(fd *FuncDecl, inPath map[string]bool) map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range fd.Params {
		if inPath[p.Name] {
			continue
		}
		props[p.Name] = in.schemaOf(p.Type, 0)
		required = append(required, p.Name)
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

// ---- manifesto ----

func (t *routeTable) buildManifest(in *Interp) {
	var routes, actions []any
	for _, r := range t.list {
		if r.static != "" {
			continue
		}
		var params []any
		inPath := map[string]bool{}
		for _, s := range r.segs {
			if s.kind != 'l' {
				schema := map[string]any{"type": "string"}
				if r.action != "" {
					for _, p := range in.funcs[r.action].Params {
						if p.Name == s.text {
							schema = in.schemaOf(p.Type, 0)
						}
					}
				}
				schema["name"], schema["in"] = s.text, "path"
				params = append(params, schema)
				inPath[s.text] = true
			}
		}
		if r.action != "" {
			fd := in.funcs[r.action]
			a := map[string]any{
				"name": r.action, "method": r.method, "path": r.raw,
				"input": in.inputSchema(fd, inPath),
			}
			if fd.Doc != "" {
				a["description"] = fd.Doc
			}
			if r.returns != nil {
				a["returns"] = r.returns.schema()
			}
			if len(params) > 0 {
				a["path_params"] = params
			}
			actions = append(actions, a)
			continue
		}
		if r.agent == "" {
			continue
		}
		e := map[string]any{"method": r.method, "path": r.raw}
		if r.about != "" {
			e["description"] = r.about
		}
		if len(params) > 0 {
			e["path_params"] = params
		}
		var q []any
		if r.returns != nil {
			e["returns"] = r.returns.schema()
			q = append(q, map[string]any{"name": "campos", "type": "string", "description": "campos desejados, separados por vírgula (ex.: id,titulo); aninhados com ponto"})
			if r.returns.list {
				q = append(q, map[string]any{"name": "tabela", "type": "flag", "description": "devolve a lista como CSV (text/csv): cabeçalho uma vez, uma linha por item; poupa tokens"})
			}
		}
		if r.page {
			q = append(q,
				map[string]any{"name": "pagina", "type": "integer", "description": "número da página, a partir de 1; há mais páginas quando a resposta traz Link rel=\"next\""},
				map[string]any{"name": "limite", "type": "integer", "description": "itens por página (o servidor limita o máximo)"})
		}
		if len(q) > 0 {
			e["query"] = q
		}
		routes = append(routes, e)
	}
	if len(routes) == 0 && len(actions) == 0 {
		return
	}
	m := map[string]any{"format": "djo-manifest/1", "routes": routes, "actions": actions}
	if t.site != nil {
		m["site"] = t.site
	}
	if routes == nil {
		m["routes"] = []any{}
	}
	if actions == nil {
		m["actions"] = []any{}
	}
	t.manifest, _ = json.MarshalIndent(m, "", "  ")
	t.home = buildHome(t.site, routes, actions)
	t.llms = buildLLMS(t.site, routes, actions)
}

// buildLLMS monta o /llms.txt: o mesmo resumo da home, em Markdown, no formato que os agentes
// procuram primeiro (título, citação com o resumo, listas de links). O detalhe fica no manifesto.
func buildLLMS(site map[string]any, routes, actions []any) []byte {
	var b strings.Builder
	nome, _ := site["nome"].(string)
	if nome == "" {
		nome = "Site"
	}
	fmt.Fprintf(&b, "# %s\n\n", oneLine(nome))
	if about, _ := site["about"].(string); about != "" {
		fmt.Fprintf(&b, "> %s\n\n", oneLine(about))
	}
	fmt.Fprintf(&b, "Este site tem duas faces. As páginas em HTML são para humanos. Para agentes, peça a mesma URL com `Accept: application/json`, ou leia o manifesto, que descreve cada rota, ação e tipo.\n\n")
	fmt.Fprintf(&b, "## Comece por aqui\n\n- [Manifesto](%s): rotas, ações e tipos (JSON Schema)\n", manifestPath)
	line := func(path, desc string) {
		if desc != "" {
			fmt.Fprintf(&b, "- [%s](%s): %s\n", path, path, oneLine(desc))
		} else {
			fmt.Fprintf(&b, "- [%s](%s)\n", path, path)
		}
	}
	if len(routes) > 0 {
		b.WriteString("\n## Leituras (GET com Accept: application/json)\n\n")
		for _, it := range routes {
			m := it.(map[string]any)
			p, _ := m["path"].(string)
			d, _ := m["description"].(string)
			line(p, d)
		}
	}
	if len(actions) > 0 {
		b.WriteString("\n## Ações (corpo application/json)\n\n")
		for _, it := range actions {
			m := it.(map[string]any)
			p, _ := m["path"].(string)
			meth, _ := m["method"].(string)
			name, _ := m["name"].(string)
			d, _ := m["description"].(string)
			if d != "" {
				d = name + ": " + d
			} else {
				d = name
			}
			line(p, meth+" "+d)
		}
	}
	return []byte(b.String())
}

// oneLine deixa o texto numa linha só, para não quebrar a lista do Markdown.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// buildHome monta o que um agente recebe em "/": saudação e um resumo do que o site oferece.
// O detalhe (tipos, schemas) fica no manifesto; aqui só o que ajuda a escolher o próximo passo.
func buildHome(site map[string]any, routes, actions []any) []byte {
	pick := func(list []any, keys ...string) []any {
		out := []any{}
		for _, it := range list {
			m := it.(map[string]any)
			e := map[string]any{}
			for _, k := range keys {
				if v, ok := m[k]; ok {
					e[k] = v
				}
			}
			out = append(out, e)
		}
		return out
	}
	hello := "Olá! Este site atende agentes. Veja o que ele oferece em \"rotas\" e \"acoes\"."
	if nome, ok := site["nome"].(string); ok {
		hello = "Olá! Este é o site " + nome + "."
		if about, ok := site["about"].(string); ok {
			hello += " " + about
		}
	} else if about, ok := site["about"].(string); ok {
		hello = "Olá! " + about
	}
	doc := map[string]any{
		"saudacao":  hello,
		"manifesto": manifestPath,
		"rotas":     pick(routes, "method", "path", "description"),
		"acoes":     pick(actions, "name", "method", "path", "description"),
	}
	if site != nil {
		doc["site"] = site
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

func (s *Server) serveManifest(w http.ResponseWriter, r *http.Request, t *routeTable) {
	serveJSONDoc(w, r, t.manifest)
}

// serveJSONDoc serve um documento JSON estático com ETag e cache curto.
func serveJSONDoc(w http.ResponseWriter, r *http.Request, body []byte) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	etag := etagOf(body)
	h.Set("ETag", etag)
	h.Set("Cache-Control", "public, max-age=60")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// ---- receber a chamada de uma action ----

func jsonError(w http.ResponseWriter, code int, msg string, fields map[string]string) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	body := map[string]any{"erro": msg}
	if len(fields) > 0 {
		body["campos"] = fields
	}
	b, _ := json.Marshal(body)
	w.WriteHeader(code)
	w.Write(b)
}

type inputErrs map[string]string

func (e inputErrs) add(path, msg string) {
	if _, dup := e[path]; !dup {
		e[path] = msg
	}
}

// convertIn confere v (vindo do JSON) contra o tipo e devolve o valor da djo.
func (in *Interp) convertIn(typ string, v any, path string, errs inputErrs, depth int) any {
	bad := func(want string) any {
		errs.add(path, fmt.Sprintf("deve ser %s, mas veio %s", want, typeName(v)))
		return nil
	}
	if depth > 10 {
		errs.add(path, "estrutura funda demais")
		return nil
	}
	switch typ {
	case "Int":
		if x, ok := v.(int64); ok {
			return x
		}
		return bad("Int")
	case "Float":
		switch x := v.(type) {
		case int64:
			return float64(x)
		case float64:
			return x
		}
		return bad("Float")
	case "String":
		if x, ok := v.(string); ok {
			return x
		}
		return bad("String")
	case "Bool":
		if x, ok := v.(bool); ok {
			return x
		}
		return bad("Bool")
	case "Arr":
		if x, ok := v.(*Array); ok {
			return x
		}
		return bad("Arr")
	case "Map":
		if x, ok := v.(*Map); ok {
			return x
		}
		return bad("Map")
	case "Any":
		return v
	case "Date":
		if s, ok := v.(string); ok {
			if d, ok := parseDate(s); ok {
				return d
			}
			errs.add(path, "não é uma data válida; use AAAA-MM-DD, como 2025-01-31")
			return nil
		}
		return bad("Date (texto AAAA-MM-DD)")
	case "DateTime":
		if s, ok := v.(string); ok {
			if t, ok := parseDateTime(s); ok {
				return t
			}
			errs.add(path, "não é uma data e hora válida; use AAAA-MM-DDTHH:MM:SSZ, como 2025-01-31T14:05:00Z")
			return nil
		}
		return bad("DateTime (texto AAAA-MM-DDTHH:MM:SSZ)")
	}
	sd := in.structs[typ]
	m, ok := v.(*Map)
	if sd == nil || !ok {
		return bad("um objeto " + typ)
	}
	in.link(sd, sd.Line)
	inst := &Instance{Def: sd, Vals: make([]any, len(sd.Fields))}
	known := map[string]bool{}
	for i, f := range sd.Fields {
		if f.NoCall {
			inst.Vals[i] = in.zeroOrDefault(f, sd)
			continue
		}
		known[f.Name] = true
		raw, present := m.get(f.Name)
		if !present {
			if f.Default == nil {
				errs.add(path+"."+f.Name, "obrigatório")
				continue
			}
			inst.Vals[i] = in.zeroOrDefault(f, sd)
			continue
		}
		inst.Vals[i] = in.convertIn(f.Type, raw, path+"."+f.Name, errs, depth+1)
	}
	for _, k := range m.Keys {
		if ks, _ := k.(string); !known[ks] {
			errs.add(path+"."+fmt.Sprint(k), "campo desconhecido")
		}
	}
	return inst
}

func (in *Interp) zeroOrDefault(f FieldDecl, sd *StructDecl) any {
	if f.Default != nil {
		return in.eval(f.Default, in.global)
	}
	return in.zeroValue(f, sd.Name, sd.Line)
}

// fromPath converte o texto da URL para o tipo do parâmetro.
func fromPath(typ, s string) (any, error) {
	switch typ {
	case "String":
		return s, nil
	case "Int":
		return strconv.ParseInt(s, 10, 64)
	case "Float":
		return strconv.ParseFloat(s, 64)
	case "Bool":
		return strconv.ParseBool(s)
	case "Date":
		if d, ok := parseDate(s); ok {
			return d, nil
		}
		return nil, errors.New("não é uma data AAAA-MM-DD")
	case "DateTime":
		if t, ok := parseDateTime(s); ok {
			return t, nil
		}
		return nil, errors.New("não é uma data e hora AAAA-MM-DDTHH:MM:SSZ")
	}
	return nil, errors.New("o tipo " + typ + " não pode vir da URL")
}

func (s *Server) runAction(w http.ResponseWriter, r *http.Request, t *routeTable, rt *route, pathVals map[string]string) {
	w.Header().Set("Vary", "Accept")
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	hasBody := r.ContentLength != 0
	if hasBody && ct != "application/json" {
		jsonError(w, http.StatusUnsupportedMediaType, "envie o corpo como application/json", nil)
		return
	}
	var body *Map = newMap()
	if hasBody {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			jsonError(w, http.StatusRequestEntityTooLarge, "corpo grande demais", nil)
			return
		}
		if len(strings.TrimSpace(string(raw))) > 0 {
			v, err := jsonDecode(string(raw))
			m, ok := v.(*Map)
			if err != nil || !ok {
				jsonError(w, http.StatusBadRequest, "o corpo precisa ser um objeto JSON", nil)
				return
			}
			body = m
		}
	}

	out := &limitWriter{max: maxResponse}
	in := NewInterp(out)
	in.stdin = bufio.NewReader(strings.NewReader(""))
	in.root = s.root
	in.dbRoot = s.dbRoot
	in.req = r
	in.params = pathVals
	in.resp = &response{status: http.StatusOK, header: http.Header{}}
	in.maxSteps = s.maxSteps
	in.served = !s.allowPrivate
	if err := in.loadCore(s.root); err != nil {
		s.agentError(w, r, err)
		return
	}
	fd := in.funcs[rt.action]
	if fd == nil || !fd.Action {
		s.agentError(w, r, fmt.Errorf("a action '%s' não existe mais", rt.action))
		return
	}

	errs := inputErrs{}
	args := make([]Expr, len(fd.Params))
	declared := map[string]bool{}
	for i, p := range fd.Params {
		declared[p.Name] = true
		var val any
		if sv, fromURL := pathVals[p.Name]; fromURL {
			if _, dup := body.get(p.Name); dup {
				errs.add(p.Name, "vem da URL e não pode ir no corpo")
			}
			v, err := fromPath(p.Type, sv)
			if err != nil {
				errs.add(p.Name, "na URL deve ser "+p.Type)
			}
			val = v
		} else if raw, present := body.get(p.Name); present {
			val = in.convertIn(p.Type, raw, p.Name, errs, 0)
		} else {
			errs.add(p.Name, "obrigatório")
		}
		args[i] = &ValLit{val}
	}
	for _, k := range body.Keys {
		if ks, _ := k.(string); !declared[ks] {
			errs.add(fmt.Sprint(k), "campo desconhecido")
		}
	}
	if len(errs) > 0 {
		keys := make([]string, 0, len(errs))
		for k := range errs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields := map[string]string{}
		for _, k := range keys {
			fields[k] = errs[k]
		}
		jsonError(w, http.StatusUnprocessableEntity, "entrada inválida", fields)
		return
	}

	prog := []Stmt{&VarStmt{Name: "resultado", Init: &Call{Name: rt.action, Args: args, Line: fd.Line}, Line: fd.Line}}
	if err := in.Run(prog); err != nil {
		s.agentError(w, r, err)
		return
	}
	res, _ := in.global.get("resultado")
	payload, err := encodeResult(res)
	if err != nil {
		s.agentError(w, r, err)
		return
	}
	h := w.Header()
	for k, v := range in.resp.header {
		h[k] = v
	}
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	for _, c := range in.resp.cookies {
		http.SetCookie(w, c)
	}
	w.WriteHeader(in.resp.status)
	w.Write(payload)
}

// encodeResult: o retorno da action vira o corpo; sem retorno, {"ok":true}.
func encodeResult(v any) (b []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			if de, ok := r.(*djoError); ok {
				err = de
				return
			}
			panic(r)
		}
	}()
	if v == nil {
		return []byte(`{"ok":true}`), nil
	}
	return []byte(jsonEncode(v, "", 0)), nil
}

func (s *Server) agentError(w http.ResponseWriter, r *http.Request, err error) {
	if s.logger != nil {
		s.logger.Printf("ERRO em %s: %v", r.URL.Path, err)
	}
	msg := "erro interno"
	if s.dev {
		msg = err.Error()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	b, _ := json.Marshal(map[string]string{"erro": msg})
	w.WriteHeader(http.StatusInternalServerError)
	w.Write(b)
}
