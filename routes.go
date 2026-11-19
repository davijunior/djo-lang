//go:build !noweb

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// rotas.djo: quando existe na raiz do site, só as rotas declaradas nele existem.
// O roteamento por arquivo (posts/[id].djo) fica desligado, e arquivos estáticos
// só saem das pastas declaradas com Route.static.
//
//	Route.get:"/posts/{id}":{"html": "paginas/post.djo.html", "agent": "agentes/post.djo"}
//	Route.get:"/sobre":"paginas/sobre.djo.html"
//	Route.post:"/entrar":"paginas/entrar.djo"
//	Route.static:"/assets":"publico"

type rseg struct {
	kind byte // 'l' texto fixo, 'p' {nome}, 'r' {...nome}
	text string
}

type route struct {
	method  string
	raw     string
	segs    []rseg
	html    string   // página para o navegador (relativa ao site)
	agent   string   // página para agentes
	about   string   // descrição para o manifesto
	action  string   // nome da action (Route.action)
	returns *retSpec // tipo da resposta de agente
	page    bool     // a rota pagina: o manifesto anuncia ?pagina= e ?limite=
	cache   int      // segundos que a resposta de um GET fica em memória (0 = não guarda)
	static  string   // pasta de arquivos estáticos (Route.static)
	line    int
}

type routeTable struct {
	list     []*route
	manifest []byte         // JSON do manifesto; vazio se nenhuma rota fala com agentes
	site     map[string]any // Route.site: nome e about do site
	cors     *corsCfg       // Route.cors; nil = sem CORS
	llms     []byte         // conteúdo gerado do /llms.txt; vazio = não há
	home     []byte         // resposta padrão de agente para "/" (saudação + resumo); vazio = não há
}

var routeMethods = []string{"get", "post", "put", "patch", "delete"}

func init() {
	ns := nativeNS("Route")
	for _, m := range routeMethods {
		method := strings.ToUpper(m)
		ns[m] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
			in.addRoute(line, method, a)
			return nil
		}}
	}
	ns["action"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		in.addAction(line, "POST", "action", a)
		return nil
	}}
	// put_action, patch_action e delete_action: a mesma action, publicada com outro método HTTP
	for _, m := range []string{"put", "patch", "delete"} {
		method, name := strings.ToUpper(m), m+"_action"
		ns[name] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
			in.addAction(line, method, name, a)
			return nil
		}}
	}
	ns["cors"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		in.addCors(line, a)
		return nil
	}}
	ns["site"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		in.addSite(line, a)
		return nil
	}}
	ns["static"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		in.addStatic(line, a)
		return nil
	}}
}

func (in *Interp) needRoutes(line int) *routeTable {
	if in.routes == nil {
		fail(line, "Route só pode ser usado dentro de rotas.djo")
	}
	return in.routes
}

func parsePattern(line int, raw string) []rseg {
	dotdot := false
	for _, seg := range strings.Split(raw, "/") {
		dotdot = dotdot || seg == ".."
	}
	if !strings.HasPrefix(raw, "/") || dotdot || strings.Contains(raw, "//") {
		fail(line, "a rota '%s' precisa começar com / e não ter .. nem //", raw)
	}
	trim := strings.Trim(raw, "/")
	if trim == "" {
		return nil
	}
	parts := strings.Split(trim, "/")
	segs := make([]rseg, len(parts))
	for i, p := range parts {
		switch {
		case strings.HasPrefix(p, "{...") && strings.HasSuffix(p, "}"):
			if i != len(parts)-1 {
				fail(line, "'%s' só pode ser o último trecho da rota", p)
			}
			segs[i] = rseg{'r', validName(line, p[4:len(p)-1])}
		case strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}"):
			segs[i] = rseg{'p', validName(line, p[1:len(p)-1])}
		case strings.ContainsAny(p, "{}"):
			fail(line, "trecho de rota inválido: '%s' (use {nome} ou {...nome} no trecho inteiro)", p)
		default:
			segs[i] = rseg{'l', p}
		}
	}
	return segs
}

func validName(line int, n string) string {
	ok := n != ""
	for _, r := range n {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			ok = false
		}
	}
	if !ok {
		fail(line, "nome de parte da rota inválido: '%s'", n)
	}
	return n
}

// pageFile confere que o caminho é uma página .djo/.djo.html dentro do site.
func (in *Interp) pageFile(line int, what string, v any) string {
	rel, ok := v.(string)
	if !ok {
		fail(line, "%s deve ser o caminho de uma página (String), mas é %s", what, typeName(v))
	}
	clean := path.Clean(filepath.ToSlash(rel))
	if rel == "" || strings.HasPrefix(rel, "/") || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(rel, "..") {
		fail(line, "%s: '%s' precisa ser um caminho dentro do site, como \"paginas/posts.djo.html\"", what, rel)
	}
	if clean == "core" || strings.HasPrefix(clean, "core/") {
		fail(line, "%s: o core/ não pode ser usado como página", what)
	}
	if !strings.HasSuffix(clean, ".djo") && !strings.HasSuffix(clean, ".djo.html") {
		fail(line, "%s: '%s' não é uma página .djo nem .djo.html", what, rel)
	}
	if !isFile(filepath.Join(in.root, filepath.FromSlash(clean))) {
		fail(line, "%s: o arquivo '%s' não existe", what, rel)
	}
	return clean
}

func (in *Interp) addRoute(line int, method string, a []any) {
	t := in.needRoutes(line)
	pat, ok := a[0].(string)
	if !ok {
		fail(line, "Route.%s espera o caminho como String", strings.ToLower(method))
	}
	r := &route{method: method, raw: pat, segs: parsePattern(line, pat), line: line}
	switch target := a[1].(type) {
	case string:
		r.html = in.pageFile(line, "Route "+pat, target)
	case *Map:
		for i, k := range target.Keys {
			switch k {
			case "html":
				r.html = in.pageFile(line, "Route "+pat+" (html)", target.Vals[i])
			case "agent":
				r.agent = in.pageFile(line, "Route "+pat+" (agent)", target.Vals[i])
			case "returns":
				r.returns = in.parseReturns(line, "Route "+pat, target.Vals[i])
			case "cache":
				n, ok := target.Vals[i].(int64)
				if !ok || n < 0 || n > 86400 {
					fail(line, "Route %s: \"cache\" são segundos, um Int de 0 a 86400", pat)
				}
				if method != "GET" {
					fail(line, "Route %s: \"cache\" só vale em Route.get", pat)
				}
				r.cache = int(n)
			case "page":
				b, ok := target.Vals[i].(bool)
				if !ok {
					fail(line, "Route %s: \"page\" deve ser true ou false", pat)
				}
				if method != "GET" {
					fail(line, "Route %s: \"page\" só vale em Route.get", pat)
				}
				r.page = b
			case "about":
				about, ok := target.Vals[i].(string)
				if !ok {
					fail(line, "Route %s: \"about\" deve ser um texto", pat)
				}
				r.about = about
			default:
				fail(line, "Route %s: chave '%v' desconhecida; use \"html\", \"agent\", \"about\", \"returns\" ou \"cache\"", pat, k)
			}
		}
	default:
		fail(line, "Route %s: o destino é uma página (String) ou {\"html\": ..., \"agent\": ...}, não %s", pat, typeName(a[1]))
	}
	if r.html == "" && r.agent == "" {
		fail(line, "Route %s: declare ao menos \"html\" ou \"agent\"", pat)
	}
	t.add(line, r)
}

func (in *Interp) addAction(line int, method, fn string, a []any) {
	t := in.needRoutes(line)
	if len(a) < 2 || len(a) > 3 {
		fail(line, "Route.%s espera (caminho, nome da action) e, se quiser, o tipo da resposta", fn)
	}
	pat, ok1 := a[0].(string)
	name, ok2 := a[1].(string)
	if !ok1 || !ok2 {
		fail(line, "Route.%s espera (caminho, nome da action), os dois String", fn)
	}
	var ret *retSpec
	if len(a) == 3 {
		ret = in.parseReturns(line, "Route."+fn+" "+pat, a[2])
	}
	fd := in.funcs[name]
	if fd == nil || !fd.Action {
		fail(line, "Route.%s: '%s' não é uma action (declare com \"action %s\" no core/)", fn, name, name)
	}
	in.checkAction(fd, line)
	segs := parsePattern(line, pat)
	for _, s := range segs {
		if s.kind == 'r' {
			fail(line, "Route.%s: o caminho '%s' não pode usar {...}", fn, pat)
		}
		if s.kind == 'p' {
			found := false
			for _, p := range fd.Params {
				found = found || p.Name == s.text
			}
			if !found {
				fail(line, "Route.%s: {%s} na rota não é parâmetro da action '%s'", fn, s.text, name)
			}
		}
	}
	t.add(line, &route{method: method, raw: pat, segs: segs, action: name, returns: ret, line: line})
}

// Route.site:{"nome": "djo", "about": "A linguagem djo"}: quem é o site, para a saudação de
// agentes em "/" e para o manifesto.
func (in *Interp) addSite(line int, a []any) {
	t := in.needRoutes(line)
	if t.site != nil {
		fail(line, "Route.site só pode aparecer uma vez")
	}
	m, ok := a[0].(*Map)
	if !ok {
		fail(line, "Route.site espera um mapa: {\"nome\": \"...\", \"about\": \"...\"}")
	}
	site := map[string]any{}
	for i, k := range m.Keys {
		ks, _ := k.(string)
		if ks != "nome" && ks != "about" {
			fail(line, "Route.site: a chave %v não existe (use \"nome\" e \"about\")", k)
		}
		v, isStr := m.Vals[i].(string)
		if !isStr || strings.TrimSpace(v) == "" || len(v) > 300 {
			fail(line, "Route.site: \"%s\" precisa ser um texto de até 300 caracteres", ks)
		}
		site[ks] = v
	}
	if len(site) == 0 {
		fail(line, "Route.site: informe \"nome\" e/ou \"about\"")
	}
	t.site = site
}

func (in *Interp) addStatic(line int, a []any) {
	t := in.needRoutes(line)
	pat, ok1 := a[0].(string)
	dir, ok2 := a[1].(string)
	if !ok1 || !ok2 {
		fail(line, "Route.static espera (caminho, pasta), os dois String")
	}
	clean := path.Clean(filepath.ToSlash(dir))
	if dir == "" || strings.HasPrefix(dir, "/") || strings.Contains(dir, "..") || clean == "." || clean == "core" || strings.HasPrefix(clean, "core/") {
		fail(line, "Route.static: '%s' precisa ser uma pasta do site (e não o core/ nem a raiz)", dir)
	}
	if !isDir(filepath.Join(in.root, filepath.FromSlash(clean))) {
		fail(line, "Route.static: a pasta '%s' não existe", dir)
	}
	segs := parsePattern(line, pat)
	for _, s := range segs {
		if s.kind != 'l' {
			fail(line, "Route.static: o caminho '%s' não pode ter partes variáveis", pat)
		}
	}
	segs = append(segs, rseg{'r', "_arquivo"})
	t.add(line, &route{method: "GET", raw: pat, segs: segs, static: clean, line: line})
}

func (t *routeTable) add(line int, r *route) {
	key := r.method + " " + sigOf(r.segs)
	for _, o := range t.list {
		if o.method+" "+sigOf(o.segs) == key {
			fail(line, "a rota %s %s já foi declarada na linha %d", r.method, r.raw, o.line)
		}
	}
	t.list = append(t.list, r)
}

// sigOf ignora os nomes das partes: /posts/{id} e /posts/{slug} são a mesma rota.
func sigOf(segs []rseg) string {
	var b strings.Builder
	for _, s := range segs {
		switch s.kind {
		case 'l':
			b.WriteString("/" + s.text)
		case 'p':
			b.WriteString("/{}")
		default:
			b.WriteString("/{...}")
		}
	}
	return b.String()
}

func rank(k byte) int {
	switch k {
	case 'l':
		return 0
	case 'p':
		return 1
	}
	return 2
}

// sortRoutes põe as mais específicas primeiro: texto fixo, depois {x}, depois {...x}.
func (t *routeTable) sortRoutes() {
	sort.SliceStable(t.list, func(i, j int) bool {
		a, b := t.list[i].segs, t.list[j].segs
		for k := 0; k < len(a) && k < len(b); k++ {
			if ra, rb := rank(a[k].kind), rank(b[k].kind); ra != rb {
				return ra < rb
			}
		}
		return len(a) > len(b)
	})
}

func (r *route) match(parts []string) (map[string]string, bool) {
	params := map[string]string{}
	for i, s := range r.segs {
		if s.kind == 'r' {
			if i >= len(parts) {
				return nil, false
			}
			params[s.text] = strings.Join(parts[i:], "/")
			return params, true
		}
		if i >= len(parts) || parts[i] == "" {
			return nil, false
		}
		if s.kind == 'l' && parts[i] != s.text {
			return nil, false
		}
		if s.kind == 'p' {
			params[s.text] = parts[i]
		}
	}
	return params, len(parts) == len(r.segs)
}

// loadRoutes lê e confere o rotas.djo; devolve nil se o site não tem um.
func loadRoutes(root string) (*routeTable, error) {
	file := filepath.Join(root, "rotas.djo")
	if !isFile(file) {
		return nil, nil
	}
	fail := func(line int, msg string) error { return &djoError{Line: line, Msg: "rotas.djo: " + msg} }
	prog, err := loadProgram(file)
	if err != nil {
		if de, ok := err.(*djoError); ok {
			return nil, &djoError{Line: de.Line, Msg: "rotas.djo: " + de.Msg}
		}
		return nil, err
	}
	for _, s := range prog {
		es, ok := s.(*ExprStmt)
		var mc *MethodCall
		if ok {
			mc, ok = es.X.(*MethodCall)
		}
		if ok {
			id, isID := mc.X.(*Ident)
			ok = isID && id.Name == "Route"
		}
		if !ok {
			return nil, fail(lineOf(s), "só aceita chamadas Route.get:, Route.post:... Route.action:, Route.cors:, Route.site: e Route.static:")
		}
	}
	in := NewInterp(io.Discard)
	in.root = root
	if err := in.loadCore(root); err != nil {
		return nil, err
	}
	in.routes = &routeTable{}
	if err := in.Run(prog); err != nil {
		if de, ok := err.(*djoError); ok {
			return nil, &djoError{Line: de.Line, Msg: "rotas.djo: " + de.Msg}
		}
		return nil, err
	}
	in.routes.buildManifest(in)
	in.routes.sortRoutes()
	return in.routes, nil
}

// checkRoutes é usado pelo djo build.
func checkRoutes(root string) error {
	_, err := loadRoutes(root)
	return err
}

// table devolve as rotas atuais (recarrega se o rotas.djo mudou).
func (s *Server) table() (*routeTable, error) {
	fi, err := os.Stat(filepath.Join(s.root, "rotas.djo")) // fora do mutex: é um syscall por requisição
	s.rmu.Lock()
	defer s.rmu.Unlock()
	if err != nil {
		s.rotas, s.rmod = nil, time.Time{}
		return nil, nil
	}
	if s.rotas != nil && fi.ModTime().Equal(s.rmod) && fi.Size() == s.rsize {
		return s.rotas, nil
	}
	t, err := loadRoutes(s.root)
	if err != nil {
		s.rotas = nil
		return nil, err
	}
	s.rotas, s.rmod, s.rsize = t, fi.ModTime(), fi.Size()
	return t, nil
}

// declares diz se o rotas.djo declara alguma rota com esse caminho (qualquer método).
func (t *routeTable) declares(p string) bool {
	for _, r := range t.list {
		if r.raw == p {
			return true
		}
	}
	return false
}

// rootAgent diz se o rotas.djo já declara uma face de agente para GET /.
func (t *routeTable) rootAgent() bool {
	for _, r := range t.list {
		if r.method == "GET" && r.raw == "/" && r.agent != "" {
			return true
		}
	}
	return false
}

func (s *Server) wantsAgent(r *http.Request) (agent, explicit bool) {
	if s.dev {
		switch r.URL.Query().Get("formato") {
		case "agent":
			return true, true
		case "html":
			return false, true
		}
	}
	acc := strings.ToLower(r.Header.Get("Accept"))
	html, js := strings.Contains(acc, "text/html"), strings.Contains(acc, "application/json")
	return js && !html, html || js
}

func (s *Server) serveRoutes(w http.ResponseWriter, r *http.Request, t *routeTable) {
	urlPath := path.Clean("/" + r.URL.Path)
	var parts []string
	if urlPath != "/" {
		parts = strings.Split(strings.Trim(urlPath, "/"), "/")
	}
	if t.cors != nil {
		if s.corsPreflight(w, r, t, parts) {
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			w = &corsWriter{ResponseWriter: w, cfg: t.cors, origin: origin}
		}
	}
	if len(t.manifest) > 0 {
		w.Header().Set("Link", "<"+manifestPath+">; rel=\"ai-manifest\"")
		get := r.Method == http.MethodGet || r.Method == http.MethodHead
		if urlPath == manifestPath && get {
			s.serveManifest(w, r, t)
			return
		}
		if urlPath == "/llms.txt" && get && len(t.llms) > 0 && !t.declares("/llms.txt") {
			h := w.Header()
			h.Set("Content-Type", "text/markdown; charset=utf-8")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Cache-Control", "public, max-age=60")
			if r.Method != http.MethodHead {
				w.Write(t.llms)
			}
			return
		}
		// "/" para um agente, quando o site não escreveu a sua própria face de agente
		if urlPath == "/" && get && len(t.home) > 0 {
			if agent, _ := s.wantsAgent(r); agent && !t.rootAgent() {
				w.Header().Set("Vary", "Accept")
				serveJSONDoc(w, r, t.home)
				return
			}
		}
	}
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	var allow []string
	for _, rt := range t.list {
		params, ok := rt.match(parts)
		if !ok {
			continue
		}
		if rt.method != method {
			allow = append(allow, rt.method)
			continue
		}
		metaOf(r).route = rt.raw
		if rt.action != "" {
			if release := s.lane.acquire(w, r); release != nil {
				defer release()
				s.runAction(w, r, t, rt, params)
			}
			return
		}
		if rt.static != "" {
			s.serveStatic(w, r, rt, params["_arquivo"])
			return
		}
		agent, explicit := s.wantsAgent(r)
		file, ctype := rt.html, ""
		if agent {
			file, ctype = rt.agent, "application/json; charset=utf-8"
		}
		if file == "" {
			// sem preferência declarada (Accept vazio ou */*), serve a face que existe
			if !explicit && rt.html == "" {
				file, ctype = rt.agent, "application/json; charset=utf-8"
			} else if !explicit && rt.agent == "" {
				file, ctype = rt.html, ""
			} else {
				w.Header().Set("Vary", "Accept")
				http.Error(w, notAcceptableMsg(rt, len(t.manifest) > 0), http.StatusNotAcceptable)
				return
			}
		}
		abs := filepath.Join(s.root, filepath.FromSlash(file))
		real, err := filepath.EvalSymlinks(abs)
		if err != nil || !withinRoot(s.realRoot, real) {
			s.notFound(w, r)
			return
		}
		w.Header().Set("Vary", "Accept")
		isAgent := ctype != ""
		var campos sel
		tab := isAgent && r.URL.Query().Has("tabela")
		if tab && (rt.returns == nil || !rt.returns.list) {
			jsonError(w, http.StatusBadRequest, "?tabela só vale em rotas que devolvem uma lista (\"returns\": [\"Tipo\"])", nil)
			return
		}
		if isAgent && r.URL.Query().Has("campos") {
			if rt.returns == nil {
				jsonError(w, http.StatusBadRequest, "esta rota não declara \"returns\", então não aceita ?campos=", nil)
				return
			}
			var err error
			if campos, err = parseCampos(r.URL.Query().Get("campos"), rt.returns.node); err != nil {
				jsonError(w, http.StatusBadRequest, err.Error(), nil)
				return
			}
		}
		// resposta guardada: só para GET sem cookie nem credencial (nunca mistura usuários)
		key := ""
		cacheable := method == http.MethodGet && rt.cache > 0 && r.Header.Get("Cookie") == "" && r.Header.Get("Authorization") == ""
		if cacheable {
			key = ctype + "|" + r.URL.RequestURI()
			if hit := s.rcache.get(key); hit != nil {
				s.writeCached(w, r, hit)
				return
			}
		}
		if isAgent {
			release := s.lane.acquire(w, r)
			if release == nil {
				return
			}
			defer release()
		}
		if !isAgent && len(t.manifest) > 0 {
			r = r.WithContext(context.WithValue(r.Context(), announceKey{}, true))
		}
		if !isAgent && !cacheable || method != http.MethodGet {
			s.runPage(w, r, abs, http.StatusOK, params, ctype)
			return
		}
		c := &captured{h: w.Header().Clone()}
		s.runPage(c, r, abs, http.StatusOK, params, ctype)
		if isAgent {
			s.reshape(c, rt, campos, tab)
		}
		s.finish(w, r, c, key, rt.cache, cacheable)
		return
	}
	if len(allow) > 0 {
		sort.Strings(allow)
		w.Header().Set("Allow", strings.Join(uniq(allow), ", "))
		http.Error(w, "405 método não permitido", http.StatusMethodNotAllowed)
		return
	}
	s.notFound(w, r)
}

func uniq(in []string) []string {
	var out []string
	for i, v := range in {
		if i == 0 || v != in[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, rt *route, rest string) {
	low := strings.ToLower(rest)
	if hidden("/"+rest) || strings.HasSuffix(low, ".djo") || strings.HasSuffix(low, ".djo.html") {
		s.notFound(w, r)
		return
	}
	file := filepath.Join(s.root, filepath.FromSlash(rt.static), filepath.FromSlash(path.Clean("/"+rest)))
	real, err := filepath.EvalSymlinks(file)
	if err != nil || !withinRoot(s.realRoot, real) || !isFile(real) {
		s.notFound(w, r)
		return
	}
	f, err := os.Open(real)
	if err != nil {
		s.notFound(w, r)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(real), fi.ModTime(), f)
}

// announceKey marca, no contexto da requisição, as páginas de tela de um site que também
// tem face de agente: nelas o djo acrescenta o aviso de onde fica a porta dos agentes.
type announceKey struct{}

const (
	announceHead = "<link rel=\"alternate\" type=\"application/json\" href=\"" + manifestPath + "\">\n" +
		"<meta name=\"ai-manifest\" content=\"" + manifestPath + "\">\n"
	announceBody = "\n<!-- Agente de IA: esta é a face para humanos. A sua porta é " + manifestPath +
		" (o mapa do que existe), ou peça esta mesma URL com Accept: application/json. -->"
)

// announceAgents acrescenta ao HTML o aviso para agentes: um <link> e um <meta> no fim do
// <head> e um comentário logo depois de abrir o <body>. Só mexe em página completa (com
// </head> e <body>); fragmentos, e páginas que já têm o aviso, passam como estão.
func announceAgents(b []byte) []byte {
	low := bytes.ToLower(b)
	if bytes.Contains(low, []byte("ai-manifest")) {
		return b
	}
	hi := bytes.Index(low, []byte("</head>"))
	bi := bytes.Index(low, []byte("<body"))
	if hi < 0 || bi < hi {
		return b
	}
	be := bytes.IndexByte(low[bi:], '>')
	if be < 0 {
		return b
	}
	be += bi + 1
	out := make([]byte, 0, len(b)+len(announceHead)+len(announceBody))
	out = append(out, b[:hi]...)
	out = append(out, announceHead...)
	out = append(out, b[hi:be]...)
	out = append(out, announceBody...)
	out = append(out, b[be:]...)
	return out
}

// notAcceptableMsg é o corpo do 406: diz quais faces a rota tem e para onde um agente deve ir.
func notAcceptableMsg(rt *route, manifest bool) string {
	var b strings.Builder
	b.WriteString("406 esta rota não tem resposta nesse formato\n")
	switch {
	case rt.agent == "" && rt.html != "":
		b.WriteString("Esta rota é só para humanos (tela), sem resposta de agente.")
	case rt.html == "" && rt.agent != "":
		b.WriteString("Esta rota é só para agentes: peça com Accept: application/json.")
	}
	if manifest {
		b.WriteString("\nO que existe para agentes está em " + manifestPath + ".")
	}
	return b.String() + "\n"
}
