package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// djo spec [caminho] [-e filtro] [--json] [--lang en] [--seed N]
//
// Roda os specs de um projeto: arquivos *_spec.djo com funções it_*, e as opcionais antes e depois.
// Cada it_* roda num interpretador novo, com uma pasta temporária só para os bancos SQLite. O
// namespace Spec (try, fail, skip, lang, get/post/put/patch/delete) só funciona aqui dentro.

const (
	specMaxSteps = 5_000_000
	specTimeout  = 30 * time.Second
	specMaxOut   = 4096 // do que o spec imprimiu, o que sobra no relatório
)

// specCtx é o estado de um spec em execução; só o djo spec cria um.
type specCtx struct {
	root   string
	file   string // caminho relativo do arquivo de spec
	lang   string
	dbRoot string
	mails  *mailBox
	jobs   *specJobBox
	http   *httpMockSet
	holds  map[string]*lockHold // Spec.lock_hold: travas seguradas "por outra execução" até o fim do teste
	lim    *limiter
	server any // *Server, criado na primeira chamada de rota (spec_web.go)
}

// specSignal interrompe o spec: falha (Spec.fail) ou pulado (Spec.skip).
type specSignal struct {
	skip                     bool
	msg, expected, received  string
	hasExpected, hasReceived bool
	line                     int
}

type specResult struct {
	File     string `json:"arquivo"`
	Name     string `json:"nome"`
	Status   string `json:"status"` // passou, falhou, erro, pulado
	Message  string `json:"mensagem,omitempty"`
	Expected string `json:"esperado,omitempty"`
	Received string `json:"recebido,omitempty"`
	Line     int    `json:"linha,omitempty"`
	MS       int64  `json:"duracao_ms"`
	Output   string `json:"saida,omitempty"`
	hasExp   bool
	hasRec   bool
}

type specSummary struct {
	Total   int   `json:"total"`
	Passed  int   `json:"passaram"`
	Failed  int   `json:"falharam"`
	Errors  int   `json:"erros"`
	Skipped int   `json:"pulados"`
	MS      int64 `json:"duracao_ms"`
}

func init() {
	ns := nativeNS("Spec")
	need := func(in *Interp, fn string, line int) *specCtx {
		if in.spec == nil {
			fail(line, "Spec.%s só existe quando os testes rodam com 'djo spec'", fn)
		}
		return in.spec
	}
	// Spec.fail:"mensagem"  ou  Spec.fail:{"mensagem": ..., "esperado": ..., "recebido": ...}
	ns["fail"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		need(in, "fail", line)
		sg := &specSignal{line: line}
		if in.cur != nil && in.outerLine > 0 {
			sg.line = in.outerLine // a linha do spec que chamou o booster
		}
		switch x := a[0].(type) {
		case string:
			sg.msg = x
		case *Map:
			get := func(k string) (string, bool) {
				v, ok := x.get(k)
				if !ok || v == nil {
					return "", false
				}
				return format(v), true
			}
			sg.msg, _ = get("mensagem")
			sg.expected, sg.hasExpected = get("esperado")
			sg.received, sg.hasReceived = get("recebido")
		default:
			fail(line, "Spec.fail espera um texto ou um mapa com mensagem, esperado e recebido")
		}
		if sg.msg == "" {
			sg.msg = "falhou"
		}
		panic(sg)
	}}
	// Spec.emails devolve os e-mails que o Mail.send "enviou" neste spec (nada sai de verdade).
	ns["emails"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		return need(in, "emails", line).mails.list()
	}}
	// Spec.jobs devolve os jobs que o Job.run enfileirou neste spec (nada roda sozinho).
	ns["jobs"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		return need(in, "jobs", line).jobs.list()
	}}
	// Spec.run_jobs executa, em ordem e de forma síncrona, os jobs enfileirados (inclusive os que eles criarem).
	ns["run_jobs"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		return specRunJobs(in, need(in, "run_jobs", line), 0, line)
	}}
	// Spec.run_job:id executa só esse job.
	ns["run_job"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		id, ok := a[0].(int64)
		if !ok {
			fail(line, "Spec.run_job espera o id (Int) que o Job.run devolveu")
		}
		return specRunJobs(in, need(in, "run_job", line), id, line)
	}}
	// Spec.agenda devolve a agenda (agenda.djo) do projeto.
	ns["agenda"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		c := need(in, "agenda", line)
		defs, err := loadAgenda(c.root)
		if err != nil {
			fail(line, "Spec.agenda: %v", err)
		}
		out := &Array{}
		for _, d := range defs {
			m := newMap()
			m.set("kind", d.kind)
			m.set("expr", d.expr)
			m.set("fn", d.fn)
			m.set("tries", int64(d.tries))
			m.set("timeout", int64(d.timeout/time.Second))
			m.set("catch_up", d.catchUp)
			var args any = newMap()
			if v, err := jsonDecode(d.args); err == nil {
				args = v
			}
			m.set("args", args)
			out.Items = append(out.Items, m)
		}
		return out
	}}
	// Spec.skip:"motivo" marca o spec como pulado e para.
	ns["skip"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		need(in, "skip", line)
		panic(&specSignal{skip: true, msg: argStr(a, 0, line, "Spec.skip"), line: line})
	}}
	// Spec.lang: "pt" ou "en" (para os matchers escreverem na língua do relatório).
	ns["lang"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		return need(in, "lang", line).lang
	}}
	// Spec.try:"funcao":[args] chama uma função do projeto e devolve {ok, value, error, line}.
	ns["try"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		need(in, "try", line)
		name := argStr(a, 0, line, "Spec.try")
		args, ok := a[1].(*Array)
		if !ok {
			fail(line, "Spec.try espera o nome da função e uma lista de argumentos")
		}
		fd, found := in.funcs[name]
		if !found {
			fail(line, "Spec.try: a função '%s' não existe no projeto (só as funções do djo valem, sem as nativas)", name)
		}
		exprs := make([]Expr, len(args.Items))
		for i, v := range args.Items {
			exprs[i] = &ValLit{V: v}
		}
		res := newMap()
		in.specTry(func() { // o booster expect chama daqui: volta para o mundo do projeto
			res.set("value", in.invoke(fd, name, exprs, in.global, nil, line))
			res.set("ok", true)
		}, res)
		for _, k := range []string{"ok", "value", "error", "line"} {
			if _, has := res.get(k); !has {
				res.set(k, nil)
			}
		}
		return res
	}}
	for _, m := range []string{"get", "post", "put", "patch", "delete"} {
		method := strings.ToUpper(m)
		name := m
		ns[name] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
			c := need(in, name, line)
			hasBody := method == "POST" || method == "PUT" || method == "PATCH"
			want := 1
			if hasBody {
				want = 2
			}
			if len(a) < want || len(a) > want+1 {
				fail(line, "Spec.%s espera o caminho%s e, se quiser, um mapa de cabeçalhos", name, map[bool]string{true: ", o corpo", false: ""}[hasBody])
			}
			path := argStr(a, 0, line, "Spec."+name)
			var body any
			if hasBody {
				body = a[1]
			}
			var hdr *Map
			if len(a) > want {
				h, ok := a[want].(*Map)
				if !ok {
					fail(line, "Spec.%s: os cabeçalhos são um mapa", name)
				}
				hdr = h
			}
			if specRequestHook == nil {
				fail(line, "Spec.%s não existe neste build (sem servidor web)", name)
			}
			return specRequestHook(in, c, method, path, body, hasBody, hdr, line)
		}}
	}
}

// specRequestHook chama uma rota ou action do site em processo; spec_web.go o define.
var specRequestHook func(in *Interp, c *specCtx, method, path string, body any, hasBody bool, hdr *Map, line int) any

// specTry roda f e transforma o erro de execução em {ok:false, error, line}. Falha e skip passam.
func (in *Interp) specTry(f func(), res *Map) {
	prev, prevS, prevI, prevD := in.cur, in.structs, in.interfaces, in.depth
	if in.cur != nil && in.pStructs != nil {
		in.cur, in.structs, in.interfaces = nil, in.pStructs, in.pIfaces
	}
	defer func() {
		in.cur, in.structs, in.interfaces, in.depth = prev, prevS, prevI, prevD
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *djoError:
				res.set("ok", false)
				res.set("error", e.Msg)
				res.set("line", int64(e.Line))
				em := errMap(e)
				for _, k := range []string{"code", "data", "file"} {
					v, _ := em.get(k)
					res.set(k, v)
				}
			case haltSignal:
				res.set("ok", true)
			default:
				panic(r)
			}
		}
	}()
	f()
}

// ---------- o comando ----------

type specList []string

func (s *specList) String() string     { return strings.Join(*s, ",") }
func (s *specList) Set(v string) error { *s = append(*s, v); return nil }

func specMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("spec", flag.ContinueOnError)
	fs.SetOutput(errw)
	var only specList
	fs.Var(&only, "e", "só os specs cujo nome contém o texto (pode repetir)")
	fs.Var(&only, "only", "o mesmo que -e")
	asJSON := fs.Bool("json", false, "saída em JSON")
	lang := fs.String("lang", "", "idioma das mensagens: pt (padrão) ou en (ou DJO_LANG)")
	seed := fs.Int64("seed", 0, "embaralha a ordem com esta semente (0: ordem do arquivo)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 1 {
		fmt.Fprintln(errw, "uso: djo spec [-e filtro] [--json] [--lang en] [--seed N] [arquivo_spec.djo | pasta]")
		return 2
	}
	l := *lang
	if l == "" {
		l = os.Getenv("DJO_LANG")
	}
	switch strings.ToLower(strings.TrimSpace(l)) {
	case "", "pt", "pt-br", "pt_br":
		l = "pt"
	case "en":
		l = "en"
	default:
		fmt.Fprintf(errw, "idioma '%s' desconhecido (valem: pt, en)\n", l)
		return 2
	}
	target := "."
	if len(pos) == 1 {
		target = pos[0]
	}
	root, files, err := specDiscover(target)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 2
	}
	if err := checkAgenda(root); err != nil {
		fmt.Fprintln(errw, err)
		return 2
	}
	if len(files) == 0 {
		fmt.Fprintln(errw, specT(l, "nenhum arquivo *_spec.djo em "+target, "no *_spec.djo files in "+target))
		return 2
	}

	type job struct {
		file string
		prog []Stmt
		fn   string
		desc string
	}
	var jobs []job
	var results []specResult
	for _, f := range files {
		rel := specRel(root, f)
		src, err := os.ReadFile(f)
		var prog []Stmt
		if err == nil {
			prog, err = Parse(string(src))
		}
		if err != nil {
			results = append(results, specResult{File: rel, Name: rel, Status: "erro", Message: err.Error()})
			continue
		}
		for _, s := range prog {
			if fd, ok := s.(*FuncDecl); ok && strings.HasPrefix(fd.Name, "it_") && len(fd.Name) > 3 {
				d := strings.ReplaceAll(fd.Name[3:], "_", " ")
				if specMatch(only, rel, d, fd.Name) {
					jobs = append(jobs, job{rel, prog, fd.Name, d})
				}
			}
		}
	}
	if *seed != 0 {
		r := rand.New(rand.NewSource(*seed))
		r.Shuffle(len(jobs), func(i, j int) { jobs[i], jobs[j] = jobs[j], jobs[i] })
	}

	start := time.Now()
	var live io.Writer = out
	if *asJSON {
		live = nil
	}
	for _, r := range results { // arquivos que nem leram
		specMark(live, r)
	}
	for _, j := range jobs {
		r := specRun(root, j.file, j.prog, j.fn, j.desc, l)
		results = append(results, r)
		specMark(live, r)
	}
	sum := specSummary{Total: len(results), MS: time.Since(start).Milliseconds()}
	for _, r := range results {
		switch r.Status {
		case "passou":
			sum.Passed++
		case "falhou":
			sum.Failed++
		case "erro":
			sum.Errors++
		case "pulado":
			sum.Skipped++
		}
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		doc := map[string]any{"resumo": sum, "specs": results}
		if results == nil {
			doc["specs"] = []specResult{}
		}
		if *seed != 0 {
			doc["semente"] = *seed
		}
		enc.Encode(doc)
	} else {
		specReport(out, results, sum, l, *seed)
	}
	if sum.Failed+sum.Errors > 0 {
		return 1
	}
	return 0
}

func specT(lang, pt, en string) string {
	if lang == "en" {
		return en
	}
	return pt
}

func specMatch(only specList, file, desc, fn string) bool {
	if len(only) == 0 {
		return true
	}
	hay := strings.ToLower(file + " " + desc + " " + fn)
	for _, o := range only {
		if strings.Contains(hay, strings.ToLower(o)) {
			return true
		}
	}
	return false
}

func specRel(root, f string) string {
	if r, err := filepath.Rel(root, f); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(f)
}

func specMark(w io.Writer, r specResult) {
	if w == nil {
		return
	}
	c := map[string]string{"passou": ".", "falhou": "F", "erro": "E", "pulado": "*"}[r.Status]
	fmt.Fprint(w, c)
}

func specReport(w io.Writer, res []specResult, s specSummary, lang string, seed int64) {
	fmt.Fprintln(w)
	n := 0
	var skipped []specResult
	for _, r := range res {
		switch r.Status {
		case "falhou", "erro":
			if n == 0 {
				fmt.Fprintln(w, "\n"+specT(lang, "Problemas:", "Problems:"))
			}
			n++
			where := r.File
			if r.Line > 0 {
				where = fmt.Sprintf("%s:%d", r.File, r.Line)
			}
			kind := ""
			if r.Status == "erro" {
				kind = specT(lang, " [erro]", " [error]")
			}
			fmt.Fprintf(w, "\n  %d) %s%s\n     %s\n", n, r.Name, kind, where)
			for _, ln := range strings.Split(r.Message, "\n") {
				fmt.Fprintf(w, "     %s\n", ln)
			}
			if r.hasExp {
				fmt.Fprintf(w, "     %s %s\n", specT(lang, "esperado:", "expected:"), r.Expected)
			}
			if r.hasRec {
				fmt.Fprintf(w, "     %s %s\n", specT(lang, "recebido:", "received:"), r.Received)
			}
			if r.Output != "" {
				fmt.Fprintf(w, "     %s\n", specT(lang, "saída do spec:", "spec output:"))
				for _, ln := range strings.Split(strings.TrimRight(r.Output, "\n"), "\n") {
					fmt.Fprintf(w, "       %s\n", ln)
				}
			}
		case "pulado":
			skipped = append(skipped, r)
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintln(w, "\n"+specT(lang, "Pulados:", "Skipped:"))
		for _, r := range skipped {
			fmt.Fprintf(w, "  * %s (%s)", r.Name, r.File)
			if r.Message != "" {
				fmt.Fprintf(w, ": %s", r.Message)
			}
			fmt.Fprintln(w)
		}
	}
	secs := float64(s.MS) / 1000
	pl := func(n int, one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	if lang == "en" {
		fmt.Fprintf(w, "\n%s, %s, %s, %d skipped, in %.2fs\n", pl(s.Total, "spec", "specs"), pl(s.Failed, "failure", "failures"), pl(s.Errors, "error", "errors"), s.Skipped, secs)
	} else {
		fmt.Fprintf(w, "\n%s, %s, %s, %s, em %.2fs\n", pl(s.Total, "spec", "specs"), pl(s.Failed, "falha", "falhas"), pl(s.Errors, "erro", "erros"), pl(s.Skipped, "pulado", "pulados"), secs)
	}
	if seed != 0 {
		fmt.Fprintf(w, "%s %d\n", specT(lang, "semente:", "seed:"), seed)
	}
}

// specDiscover acha a raiz do projeto e os arquivos *_spec.djo (em ordem alfabética).
func specDiscover(target string) (root string, files []string, err error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", nil, fmt.Errorf("%s não existe", target)
	}
	if !fi.IsDir() {
		if !strings.HasSuffix(abs, "_spec.djo") {
			return "", nil, fmt.Errorf("%s: um spec termina em _spec.djo", target)
		}
		return specRoot(filepath.Dir(abs)), []string{abs}, nil
	}
	base := abs
	if specIsDir(filepath.Join(abs, "spec")) {
		base = filepath.Join(abs, "spec") // na raiz do projeto: só a pasta spec/
	}
	filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != base && (strings.HasPrefix(name, ".") || name == "boosters" || name == "core" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && strings.HasSuffix(name, "_spec.djo") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return specRoot(abs), files, nil
}

// specRoot sobe a partir de d até achar a raiz do projeto (a pasta com spec/, core/ ou boosters/).
func specRoot(d string) string {
	cur := d
	for i := 0; i < 12; i++ {
		if filepath.Base(cur) == "spec" {
			return filepath.Dir(cur)
		}
		if specIsDir(filepath.Join(cur, "spec")) || specIsDir(filepath.Join(cur, "core")) || specIsDir(filepath.Join(cur, "boosters")) {
			return cur
		}
		up := filepath.Dir(cur)
		if up == cur {
			break
		}
		cur = up
	}
	return d
}

// specRun roda um it_* num interpretador novo, com antes e depois, e a pasta de bancos própria.
func specRun(root, file string, prog []Stmt, fn, desc, lang string) (res specResult) {
	t0 := time.Now()
	res = specResult{File: file, Name: desc, Status: "passou"}
	defer func() { res.MS = time.Since(t0).Milliseconds() }()

	tmp, err := os.MkdirTemp("", "djo-spec-")
	if err != nil {
		res.Status, res.Message = "erro", "não consegui criar a pasta temporária: "+err.Error()
		return
	}
	defer func() {
		dbCloseUnder(tmp)
		os.RemoveAll(tmp)
	}()
	tmp, _ = filepath.EvalSymlinks(tmp)

	var buf limitedBuf
	in := NewInterp(&buf)
	in.stdin = bufio.NewReader(strings.NewReader(""))
	in.root = root
	in.dbRoot = tmp
	in.maxSteps = specMaxSteps
	in.deadline = time.Now().Add(specTimeout)
	in.served = true
	in.spec = &specCtx{root: root, file: file, lang: lang, dbRoot: tmp, mails: &mailBox{}, jobs: &specJobBox{}, http: &httpMockSet{}, lim: &limiter{}}
	in.httpMock = in.spec.http
	in.limiter = in.spec.lim
	defer in.closeTx()
	defer func() {
		if in.spec != nil {
			specCloseServer(in.spec)
			in.spec.releaseHolds()
		}
	}()

	set := func(sg *specSignal, e error) {
		switch {
		case sg != nil && sg.skip:
			res.Status, res.Message, res.Line = "pulado", sg.msg, sg.line
		case sg != nil:
			res.Status, res.Message, res.Line = "falhou", sg.msg, sg.line
			res.Expected, res.hasExp, res.Received, res.hasRec = sg.expected, sg.hasExpected, sg.received, sg.hasReceived
		default:
			res.Status, res.Message = "erro", e.Error()
			if de, ok := e.(*djoError); ok {
				res.Line, res.Message = de.Line, de.Msg
			}
		}
	}
	// step roda uma etapa; devolve false se ela interrompeu o spec
	step := func(f func()) (ok bool) {
		defer func() {
			if r := recover(); r != nil {
				switch e := r.(type) {
				case *specSignal:
					set(e, nil)
				case *djoError:
					set(nil, e)
				case haltSignal:
					ok = true
				case error:
					set(nil, e)
				default:
					set(nil, fmt.Errorf("falha interna: %v", r))
				}
			}
		}()
		f()
		return true
	}
	call := func(name string) func() {
		return func() {
			fd, ok := in.funcs[name]
			if !ok {
				return
			}
			in.call(&Call{Name: name, Line: fd.Line}, in.global)
		}
	}
	finish := func() {
		res.Output = strings.TrimSpace(buf.String())
		if res.Status == "passou" || res.Status == "pulado" {
			res.Output = ""
		}
	}
	defer finish()

	if !step(func() {
		if err := in.loadCore(root); err != nil {
			panic(err)
		}
	}) {
		return
	}
	if !step(func() {
		if err := in.Run(prog); err != nil {
			panic(err)
		}
	}) {
		return
	}
	ranBefore := step(call("antes"))
	if ranBefore {
		step(call(fn))
	}
	if _, has := in.funcs["depois"]; has {
		first := res
		in.steps = 0 // o depois tem o orçamento dele
		in.deadline = time.Now().Add(specTimeout)
		step(call("depois"))
		if first.Status == "falhou" || first.Status == "erro" {
			res = first // o primeiro problema é o que importa
		}
	}
	return
}

// limitedBuf guarda o que o spec imprimiu, com teto.
type limitedBuf struct{ b bytes.Buffer }

func (l *limitedBuf) Write(p []byte) (int, error) {
	if l.b.Len() < specMaxOut {
		room := specMaxOut - l.b.Len()
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

func (l *limitedBuf) String() string { return l.b.String() }

// dbBase é a pasta onde o Db.open acha os arquivos .db: a do site, ou a temporária do djo spec.
func (in *Interp) dbBase() string {
	if in.dbRoot != "" {
		return in.dbRoot
	}
	return in.root
}

func specIsDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
