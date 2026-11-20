package main

import (
	"fmt"
	"io"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Job.run, Job.status, Job.cancel, Job.give_up: tarefas em segundo plano.
//
//	var id = Job.run:"enviar_boas_vindas":{"usuario": 7}
//	var id = Job.run:"limpar":{}:{"in": "10m", "tries": 5, "key": "limpar-7", "timeout": "2m"}
//	var j  = Job.status:id     // {id, fn, state, tries, max_tries, run_at, created_at, finished_at, error} ou nil
//	Job.cancel:id              // true se ainda estava na fila
//
// Este arquivo tem o que vale em todos os perfis: a API da linguagem, a captura do djo spec
// e a execução de uma função como job. A fila persistente e os workers estão em job.go
// (só com banco e servidor).

const (
	jobMaxArgs       = 16 << 10
	jobMaxPending    = 10000
	jobDefaultTries  = 3
	jobMaxTries      = 10
	jobDefTimeout    = 60 * time.Second
	jobMaxTimeout    = 15 * time.Minute
	jobMaxDelay      = 365 * 24 * time.Hour
	jobMaxWorkers    = 16
	jobDefWorkers    = 2
	jobMaxPerRun     = 100 // Job.run por página, action ou job
	jobMaxSteps      = 50_000_000
	jobOutMax        = 4 << 10
	jobHTTPCalls     = 20  // chamadas Http por execução de job (o padrão do -jobs-http)
	jobHTTPMax       = 200 // o teto do -jobs-http
	jobMaxSpecRounds = 100
	jobGrace         = 30 * time.Second // espera pelos jobs ao desligar
)

var jobPerMinute = 300 // enfileiramentos por minuto por processo (os testes mudam)

// jobReq é um pedido de job já validado.
type jobReq struct {
	Fn      string
	Args    string // JSON
	In      time.Duration
	At      time.Time // zero: não definido
	Tries   int
	Timeout time.Duration
	Key     string
	Sched   string // o agendamento que o criou
}

// jobBackend é o lugar onde os jobs vão parar: a fila persistente (job.go) ou a captura do djo spec.
type jobBackend interface {
	enqueue(r *jobReq, held bool) (id int64, existed, full bool, err error)
	status(id int64) *Map
	cancel(id int64) bool
	release(ids []int64)
	discard(ids []int64)
}

// jobState fica no Server: cria a fila só no primeiro uso (custo zero sem Job).
type jobState struct {
	mu      sync.Mutex
	be      jobBackend
	owner   any // *Server
	workers int // -jobs; -1: o padrão
}

var jobEngineFactory func(st *jobState) (jobBackend, error)

func (st *jobState) backend() (jobBackend, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.be != nil {
		return st.be, nil
	}
	if jobEngineFactory == nil {
		return nil, fmt.Errorf("Job não existe neste perfil")
	}
	be, err := jobEngineFactory(st)
	if err != nil {
		return nil, err
	}
	st.be = be
	return be, nil
}

// jobHost é o que o Interp sabe sobre jobs: onde enfileirar e o que ficou retido até o fim da página.
type jobHost struct {
	back  func() (jobBackend, error)
	held  []int64 // retidos até a página (ou o job pai) terminar sem erro
	made  []int64 // todos os criados nesta execução (o ponto de salvamento do promise descarta os de dentro)
	hold  bool    // true: página, action ou job; false: o spec chamando direto
	inJob bool    // está executando um job (vale Job.give_up)
	runs  int
}

func (h *jobHost) release() {
	if h == nil || len(h.held) == 0 {
		return
	}
	if be, err := h.back(); err == nil {
		be.release(h.held)
	}
	h.held = nil
}

func (h *jobHost) discard() {
	if h == nil || len(h.held) == 0 {
		return
	}
	if be, err := h.back(); err == nil {
		be.discard(h.held)
	}
	h.held = nil
}

// mark e discardFrom são o ponto de salvamento do promise: descarta os retidos criados depois de mark.
func (h *jobHost) mark() int {
	if h == nil {
		return 0
	}
	return len(h.made)
}

func (h *jobHost) discardFrom(n int) {
	if h == nil || len(h.made) <= n {
		return
	}
	gone := map[int64]bool{}
	for _, id := range h.made[n:] {
		gone[id] = true
	}
	if be, err := h.back(); err == nil {
		be.discard(h.made[n:])
	}
	h.made = h.made[:n]
	keep := h.held[:0]
	for _, id := range h.held {
		if !gone[id] {
			keep = append(keep, id)
		}
	}
	h.held = keep
}

// promiseJobMark / promiseJobDiscard: o ponto de salvamento do promise para os jobs. No djo spec
// (sem retenção) os jobs entram direto na caixa do teste, então o ponto de salvamento é o tamanho dela.
func (in *Interp) promiseJobMark() int {
	if in.job != nil {
		return in.job.mark()
	}
	if in.spec != nil && in.spec.jobs != nil {
		return in.spec.jobs.size()
	}
	return 0
}

func (in *Interp) promiseJobDiscard(n int) {
	if in.job != nil {
		in.job.discardFrom(n)
	} else if in.spec != nil && in.spec.jobs != nil {
		in.spec.jobs.truncate(n)
	}
}

func (b *specJobBox) size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}

func (b *specJobBox) truncate(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) > n {
		b.items = b.items[:n]
	}
}

func (in *Interp) jobRelease() { in.job.release() }
func (in *Interp) jobDiscard() { in.job.discard() }

// jobGiveUp interrompe o job sem nova tentativa (Job.give_up).
type jobGiveUp struct{ msg string }

var (
	jobDurRe     = regexp.MustCompile(`^([0-9]{1,6})(s|m|h|d)$`)
	errJobNoHost = "Job só existe dentro de djo serve (ou de um app do djo build); fora dele chame a função diretamente"
)

func parseJobDur(s string) (time.Duration, bool) {
	m := jobDurRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit, true
}

func jobDurOpt(v any, key string, line int) time.Duration {
	s, ok := v.(string)
	if !ok {
		fail(line, "Job.run: '%s' é um texto como \"30s\", \"10m\", \"2h\" ou \"1d\"", key)
	}
	d, ok := parseJobDur(s)
	if !ok {
		fail(line, "Job.run: '%s' = %q não é uma duração (use 30s, 10m, 2h ou 1d)", key, s)
	}
	return d
}

// jobData confere que o valor é só dados (vira JSON e volta igual).
func jobData(v any, depth, line int) {
	if depth > 20 {
		fail(line, "Job.run: os argumentos são fundos demais")
	}
	switch x := v.(type) {
	case nil, bool, int64, float64, string, Date, DateTime:
	case *Array:
		for _, it := range x.Items {
			jobData(it, depth+1, line)
		}
	case *Map:
		for i, k := range x.Keys {
			if _, ok := k.(string); !ok {
				fail(line, "Job.run: as chaves dos argumentos precisam ser textos")
			}
			jobData(x.Vals[i], depth+1, line)
		}
	default:
		fail(line, "Job.run: os argumentos são só dados (texto, número, Bool, nil, lista e mapa), mas recebeu %s", typeName(v))
	}
}

// jobFuncCheck confere a função do job: do core/, não action, com zero ou um parâmetro (o mapa dos argumentos).
func jobFuncCheck(funcs map[string]*FuncDecl, name, who string, line int) {
	fd, ok := funcs[name]
	if !ok || fd.Booster != nil {
		fail(line, "%s: a função '%s' não existe no core/ do projeto", who, name)
	}
	if fd.Action {
		fail(line, "%s: '%s' é uma action; use uma função comum", who, name)
	}
	if len(fd.Params) > 1 {
		fail(line, "%s: '%s' precisa ter zero ou um parâmetro (o mapa dos argumentos)", who, name)
	}
}

func buildJobReq(in *Interp, a []any, line int) *jobReq {
	if len(a) < 2 || len(a) > 3 {
		fail(line, "Job.run espera o nome da função, o mapa de argumentos e, se quiser, um mapa de opções: Job.run:\"fn\":{...}:{\"in\": \"10m\"}")
	}
	name, ok := a[0].(string)
	if !ok {
		fail(line, "Job.run espera o nome da função como texto, mas recebeu %s", typeName(a[0]))
	}
	args, ok := a[1].(*Map)
	if !ok {
		fail(line, "Job.run: os argumentos são um mapa (use {} se não há), mas recebeu %s", typeName(a[1]))
	}
	jobFuncCheck(in.funcs, name, "Job.run", line)
	jobData(args, 0, line)
	js := jsonEncode(args, "", line)
	if len(js) > jobMaxArgs {
		fail(line, "Job.run: os argumentos passam de %d KB", jobMaxArgs>>10)
	}
	r := &jobReq{Fn: name, Args: js, Tries: jobDefaultTries, Timeout: jobDefTimeout}
	if len(a) == 3 {
		o, ok := a[2].(*Map)
		if !ok {
			fail(line, "Job.run: as opções são um mapa (in, at, tries, timeout, key)")
		}
		hasIn, hasAt := false, false
		for i, k := range o.Keys {
			ks, _ := k.(string)
			v := o.Vals[i]
			switch ks {
			case "in":
				r.In, hasIn = jobDurOpt(v, "in", line), true
				if r.In > jobMaxDelay {
					fail(line, "Job.run: 'in' passa de 365 dias")
				}
			case "at":
				hasAt = true
				switch x := v.(type) {
				case DateTime:
					r.At = x.time()
				case string:
					dt, ok := parseDateTime(x)
					if !ok {
						fail(line, "Job.run: 'at' = %q não é um instante (use 2026-12-01T09:00:00Z)", x)
					}
					r.At = dt.time()
				default:
					fail(line, "Job.run: 'at' é um DateTime ou um texto como \"2026-12-01T09:00:00Z\"")
				}
			case "tries":
				n, ok := v.(int64)
				if !ok || n < 1 || n > jobMaxTries {
					fail(line, "Job.run: 'tries' é um número de 1 a %d", jobMaxTries)
				}
				r.Tries = int(n)
			case "timeout":
				r.Timeout = jobDurOpt(v, "timeout", line)
				if r.Timeout < time.Second || r.Timeout > jobMaxTimeout {
					fail(line, "Job.run: 'timeout' vai de 1s a 15m")
				}
			case "key":
				s, ok := v.(string)
				if !ok || s == "" || len(s) > 200 || jobCtl(s) {
					fail(line, "Job.run: 'key' é um texto de 1 a 200 letras, sem quebra de linha")
				}
				r.Key = s
			default:
				fail(line, "Job.run: a opção '%s' não existe (valem: in, at, tries, timeout, key)", ks)
			}
		}
		if hasAt && r.At.After(time.Now().Add(jobMaxDelay)) {
			fail(line, "Job.run: 'at' passa de 365 dias")
		}
		if hasIn && hasAt {
			fail(line, "Job.run: use 'in' ou 'at', não os dois")
		}
	}
	return r
}

var (
	jobWarnMu   sync.Mutex
	jobWarnLast time.Time
)

// jobWarnFull avisa no log (com data e hora) que o Job.run foi recusado, no máximo uma vez a cada 10 s.
func jobWarnFull() {
	jobWarnMu.Lock()
	defer jobWarnMu.Unlock()
	if time.Since(jobWarnLast) < 10*time.Second {
		return
	}
	jobWarnLast = time.Now()
	log.Printf("djo: Job.run recusado: fila cheia ou limite de enfileiramentos por minuto atingido (os próximos avisos em 10 s)")
}

func jobCtl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// jobHostFor devolve onde o Job.run desta execução enfileira.
func (in *Interp) jobHostFor(line int) *jobHost {
	if !hasJobs {
		fail(line, "Job não existe neste perfil (%s)", jobOffReason)
	}
	if in.job != nil {
		return in.job
	}
	if in.spec != nil {
		return &jobHost{back: func() (jobBackend, error) { return in.spec.jobs, nil }}
	}
	fail(line, "%s", errJobNoHost)
	return nil
}

func jobStatusMap(id int64, fn, state string, tries, maxTries int, runAt, created, finished int64, errMsg string) *Map {
	m := newMap()
	m.set("id", id)
	m.set("fn", fn)
	m.set("state", state)
	m.set("tries", int64(tries))
	m.set("max_tries", int64(maxTries))
	m.set("run_at", DateTime{sec: runAt})
	m.set("created_at", DateTime{sec: created})
	if finished > 0 {
		m.set("finished_at", DateTime{sec: finished})
	} else {
		m.set("finished_at", nil)
	}
	if errMsg != "" {
		m.set("error", errMsg)
	} else {
		m.set("error", nil)
	}
	m.set("lock_reschedules", int64(0))
	m.set("lock_key", nil)
	return m
}

func init() {
	ns := nativeNS("Job")
	ns["run"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		h := in.jobHostFor(line)
		r := buildJobReq(in, a, line)
		if h.runs >= jobMaxPerRun {
			fail(line, "Job.run: no máximo %d jobs por execução", jobMaxPerRun)
		}
		be, err := h.back()
		if err != nil {
			fail(line, "Job.run: %v", err)
		}
		id, existed, full, err := be.enqueue(r, h.hold)
		if err != nil {
			fail(line, "Job.run: %v", err)
		}
		if full {
			jobWarnFull()
			return nil
		}
		h.runs++
		if !existed {
			h.made = append(h.made, id)
		}
		if h.hold && !existed {
			h.held = append(h.held, id)
		}
		return id
	}}
	ns["status"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		h := in.jobHostFor(line)
		id, ok := a[0].(int64)
		if !ok {
			fail(line, "Job.status espera o id (Int) que o Job.run devolveu")
		}
		be, err := h.back()
		if err != nil {
			fail(line, "Job.status: %v", err)
		}
		if m := be.status(id); m != nil {
			return m
		}
		return nil
	}}
	ns["cancel"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		h := in.jobHostFor(line)
		id, ok := a[0].(int64)
		if !ok {
			fail(line, "Job.cancel espera o id (Int) que o Job.run devolveu")
		}
		be, err := h.back()
		if err != nil {
			fail(line, "Job.cancel: %v", err)
		}
		return be.cancel(id)
	}}
	ns["give_up"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		if in.job == nil || !in.job.inJob {
			fail(line, "Job.give_up só vale dentro de um job")
		}
		msg, ok := a[0].(string)
		if !ok {
			fail(line, "Job.give_up espera o motivo como texto")
		}
		panic(&jobGiveUp{msg})
	}}
}

// ---- a captura do djo spec ----

type specJob struct {
	id      int64
	req     jobReq
	state   string // held, queued, done, dead, canceled
	tries   int
	err     string
	created int64
}

type specJobBox struct {
	mu    sync.Mutex
	items []*specJob
	next  int64
}

func (b *specJobBox) enqueue(r *jobReq, held bool) (int64, bool, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.Key != "" {
		for _, j := range b.items {
			if j.req.Key == r.Key && (j.state == "queued" || j.state == "held") {
				return j.id, true, false, nil
			}
		}
	}
	b.next++
	st := "queued"
	if held {
		st = "held"
	}
	b.items = append(b.items, &specJob{id: b.next, req: *r, state: st, created: time.Now().Unix()})
	return b.next, false, false, nil
}

func (b *specJobBox) find(id int64) *specJob {
	for _, j := range b.items {
		if j.id == id {
			return j
		}
	}
	return nil
}

func (b *specJobBox) status(id int64) *Map {
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.find(id)
	if j == nil {
		return nil
	}
	return jobStatusMap(j.id, j.req.Fn, j.state, j.tries, j.req.Tries, j.created, j.created, 0, j.err)
}

func (b *specJobBox) cancel(id int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if j := b.find(id); j != nil && (j.state == "queued" || j.state == "held") {
		j.state = "canceled"
		return true
	}
	return false
}

func (b *specJobBox) release(ids []int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range ids {
		if j := b.find(id); j != nil && j.state == "held" {
			j.state = "queued"
		}
	}
}

func (b *specJobBox) discard(ids []int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	drop := map[int64]bool{}
	for _, id := range ids {
		if j := b.find(id); j != nil && j.state == "held" {
			drop[id] = true
		}
	}
	keep := b.items[:0]
	for _, j := range b.items {
		if !drop[j.id] {
			keep = append(keep, j)
		}
	}
	b.items = keep
}

func (b *specJobBox) list() *Array {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := &Array{}
	for _, j := range b.items {
		if j.state == "held" {
			continue
		}
		m := newMap()
		m.set("id", j.id)
		m.set("fn", j.req.Fn)
		var args any = newMap()
		if v, err := jsonDecode(j.req.Args); err == nil {
			args = v
		}
		m.set("args", args)
		m.set("state", j.state)
		m.set("max_tries", int64(j.req.Tries))
		m.set("timeout", int64(j.req.Timeout/time.Second))
		if j.req.Key != "" {
			m.set("key", j.req.Key)
		} else {
			m.set("key", nil)
		}
		if j.req.In > 0 {
			m.set("in", int64(j.req.In/time.Second))
		} else {
			m.set("in", nil)
		}
		if !j.req.At.IsZero() {
			m.set("at", DateTime{sec: j.req.At.Unix()})
		} else {
			m.set("at", nil)
		}
		if j.err != "" {
			m.set("error", j.err)
		} else {
			m.set("error", nil)
		}
		out.Items = append(out.Items, m)
	}
	return out
}

// ---- executar uma função como job (o worker e o Spec.run_jobs usam) ----

// jobOut guarda só o começo da saída do job (vai para o log).
type jobOut struct {
	buf []byte
}

func (o *jobOut) Write(p []byte) (int, error) {
	if room := jobOutMax - len(o.buf); room > 0 {
		if len(p) > room {
			o.buf = append(o.buf, p[:room]...)
		} else {
			o.buf = append(o.buf, p...)
		}
	}
	return len(p), nil
}

// jobInvoke chama a função do job num Interp já preparado. Devolve o erro (nil = deu certo)
// e, se o job pediu Job.give_up, o motivo.
func jobInvoke(in *Interp, name, argsJSON string) (err error, giveUp string, gaveUp bool) {
	defer in.closeTx()
	defer in.closeSerials()
	defer func() {
		if r := recover(); r != nil {
			switch x := r.(type) {
			case haltSignal:
			case *jobGiveUp:
				gaveUp, giveUp = true, x.msg
				err = fmt.Errorf("%s", x.msg)
			case *djoError:
				err = x
			default:
				err = fmt.Errorf("erro interno: %v", r)
			}
		}
	}()
	fd, ok := in.funcs[name]
	if !ok {
		return fmt.Errorf("a função '%s' não existe mais no core/", name), "", false
	}
	if fd.Action || fd.Booster != nil || len(fd.Params) > 1 {
		return fmt.Errorf("a função '%s' não serve mais como job (action, de booster ou com mais de um parâmetro)", name), "", false
	}
	var args any = newMap()
	if v, jerr := jsonDecode(argsJSON); jerr == nil {
		args = v
	}
	var exprs []Expr
	if len(fd.Params) == 1 {
		exprs = []Expr{&ValLit{V: args}}
	}
	in.depth = 0
	in.invoke(fd, name, exprs, in.global, nil, fd.Line)
	return nil, "", false
}

// newJobInterp prepara o Interp onde um job roda: como uma requisição servida, mas sem requisição.
// jobHTTPLimit é o limite de chamadas Http por execução de job (-jobs-http ou DJO_JOBS_HTTP).
// É do servidor: o job não escolhe, para não aumentar o limite que o protege.
var jobHTTPLimit = jobHTTPCalls

// configureJobsHTTP aplica o -jobs-http: n vale se o flag foi passado; senão vale DJO_JOBS_HTTP; senão 20.
func configureJobsHTTP(n int, flagSet bool, env string) error {
	src := "-jobs-http"
	if !flagSet {
		env = strings.TrimSpace(env)
		if env == "" {
			jobHTTPLimit = jobHTTPCalls
			return nil
		}
		v, err := strconv.Atoi(env)
		if err != nil {
			return fmt.Errorf("DJO_JOBS_HTTP precisa de um número inteiro de 1 a %d (veio %q)", jobHTTPMax, env)
		}
		n, src = v, "DJO_JOBS_HTTP"
	}
	if n < 1 || n > jobHTTPMax {
		return fmt.Errorf("%s vai de 1 a %d chamadas Http por job (o padrão é %d; veio %d)", src, jobHTTPMax, jobHTTPCalls, n)
	}
	jobHTTPLimit = n
	return nil
}

func newJobInterp(root, dbRoot string, w io.Writer, timeout time.Duration, host *jobHost) *Interp {
	in := NewInterp(w)
	in.root = root
	in.dbRoot = dbRoot
	in.served = true
	in.maxSteps = jobMaxSteps
	in.outMax = jobHTTPLimit
	in.sleepMax = timeout.Milliseconds()
	in.deadline = time.Now().Add(timeout)
	in.job = host
	return in
}

var specRunJobs = func(in *Interp, c *specCtx, only int64, line int) any {
	if !hasJobs {
		fail(line, "Job não existe neste perfil (%s)", jobOffReason)
	}
	box := c.jobs
	res := &Array{}
	ran := 0
	for round := 0; round < jobMaxSpecRounds; round++ {
		box.mu.Lock()
		var next *specJob
		for _, j := range box.items {
			if j.state == "queued" && (only == 0 || j.id == only) {
				next = j
				break
			}
		}
		if next != nil {
			next.state = "running"
			next.tries++
		}
		box.mu.Unlock()
		if next == nil {
			break
		}
		ran++
		host := &jobHost{back: func() (jobBackend, error) { return box, nil }, hold: true, inJob: true}
		child := newJobInterp(in.root, in.dbRoot, &jobOut{}, next.req.Timeout, host)
		child.maxSteps = specMaxSteps
		child.spec = c
		child.httpMock = c.http
		child.limiter = c.lim
		var err error
		if lerr := child.loadCore(in.root); lerr != nil {
			err = lerr
		} else {
			err, _, _ = jobInvoke(child, next.req.Fn, next.req.Args)
		}
		m := newMap()
		m.set("id", next.id)
		m.set("fn", next.req.Fn)
		if err != nil {
			box.mu.Lock()
			next.state, next.err = "dead", err.Error()
			box.mu.Unlock()
			host.discard()
			m.set("ok", false)
			m.set("error", err.Error())
		} else {
			box.mu.Lock()
			next.state = "done"
			box.mu.Unlock()
			host.release()
			m.set("ok", true)
			m.set("error", nil)
		}
		res.Items = append(res.Items, m)
		if only != 0 {
			break
		}
	}
	return res
}
