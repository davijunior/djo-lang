//go:build !nodb && !noweb

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// engineFx monta um site com agenda e um motor com relógio e executor falsos (nada de goroutines).
type engineFx struct {
	root string
	srv  *Server
	e    *jobEngine
	logs []string
	mu   sync.Mutex
}

func newEngineFx(t *testing.T, agenda string) *engineFx {
	t.Helper()
	root := agendaRoot(t, agenda, agendaCore)
	srv, err := NewServer(root, false)
	if err != nil {
		t.Fatal(err)
	}
	jobJitter = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() { jobJitter = func(d time.Duration) time.Duration { return d + time.Duration(int64(d)/10) } })
	q, err := openJobQueue(jobQueuePath(root), true)
	if err != nil {
		t.Fatal(err)
	}
	f := &engineFx{root: root, srv: srv}
	f.e = newJobEngine(q, srv)
	f.e.logf = func(format string, a ...any) {
		f.mu.Lock()
		f.logs = append(f.logs, fmt.Sprintf(format, a...))
		f.mu.Unlock()
	}
	return f
}

func (f *engineFx) log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.logs, "\n")
}

// queued lista as funções dos jobs na fila (em ordem de id).
func (f *engineFx) queued(t *testing.T) []string {
	rows, err := f.e.q.list("queued", 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := len(rows) - 1; i >= 0; i-- {
		out = append(out, rows[i].fn)
	}
	return out
}

func (f *engineFx) finishAll(now time.Time) {
	for {
		j, _ := f.e.q.claim(now)
		if j == nil {
			return
		}
		f.e.q.done(j.id, now)
	}
}

func TestAgendaEveryDisparaESemSobreposicao(t *testing.T) {
	f := newEngineFx(t, "Schedule.every:\"10m\":\"limpar\"\n")
	e := f.e
	e.tick(jt0) // primeira vez: só marca o ponto de partida
	if q := f.queued(t); len(q) != 0 {
		t.Fatalf("não dispara ao subir: %v", q)
	}
	e.tick(jt0.Add(9*time.Minute + 59*time.Second))
	if q := f.queued(t); len(q) != 0 {
		t.Fatalf("cedo demais: %v", q)
	}
	e.tick(jt0.Add(10 * time.Minute))
	if q := f.queued(t); len(q) != 1 || q[0] != "limpar" {
		t.Fatalf("devia disparar aos 10m: %v", q)
	}
	e.tick(jt0.Add(10*time.Minute + 5*time.Second)) // o mesmo slot não dispara duas vezes
	if q := f.queued(t); len(q) != 1 {
		t.Fatalf("duplicou: %v", q)
	}
	// o job anterior ainda está na fila: o próximo disparo é pulado e logado
	e.tick(jt0.Add(20 * time.Minute))
	if q := f.queued(t); len(q) != 1 {
		t.Fatalf("sobreposição: %v", q)
	}
	if !strings.Contains(f.log(), "ainda está na fila") {
		t.Errorf("faltou o log: %s", f.log())
	}
	f.finishAll(jt0.Add(21 * time.Minute))
	e.tick(jt0.Add(30 * time.Minute))
	if q := f.queued(t); len(q) != 1 {
		t.Fatalf("depois de terminar volta a disparar: %v", q)
	}
	// o job criado leva a chave do slot e a agenda de origem
	rows, _ := e.q.list("queued", 5)
	if !strings.HasPrefix(rows[0].key, "sched:every:10m:limpar:") || rows[0].sched != "every:10m:limpar" {
		t.Errorf("%+v", rows[0])
	}
}

func TestAgendaCronECatchUp(t *testing.T) {
	f := newEngineFx(t, "Schedule.cron:\"20 3 * * *\":\"limpar\"\nSchedule.cron:\"20 3 * * *\":\"recebe\":{\"catch_up\": false}\n")
	e := f.e
	start := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	e.tick(start)
	e.tick(time.Date(2026, 1, 11, 3, 19, 59, 0, time.UTC))
	if q := f.queued(t); len(q) != 0 {
		t.Fatalf("cedo: %v", q)
	}
	e.tick(time.Date(2026, 1, 11, 3, 20, 0, 0, time.UTC))
	if q := f.queued(t); len(q) != 2 {
		t.Fatalf("às 03:20 os dois disparam: %v", q)
	}
	f.finishAll(time.Date(2026, 1, 11, 3, 21, 0, 0, time.UTC))

	// o servidor ficou parado 3 dias: com catch_up roda UMA vez; sem catch_up, pula
	e.tick(time.Date(2026, 1, 14, 9, 0, 0, 0, time.UTC))
	q := f.queued(t)
	if len(q) != 1 || q[0] != "limpar" {
		t.Fatalf("catch_up uma vez, e o outro pula: %v", q)
	}
	rows, _ := e.q.list("queued", 5)
	if !strings.HasSuffix(rows[0].key, fmt.Sprint(time.Date(2026, 1, 14, 3, 20, 0, 0, time.UTC).Unix())) {
		t.Errorf("devia usar o slot mais recente: %s", rows[0].key)
	}
	f.finishAll(time.Date(2026, 1, 14, 9, 1, 0, 0, time.UTC))
	// e o próximo slot normal volta a disparar os dois
	e.tick(time.Date(2026, 1, 15, 3, 20, 0, 0, time.UTC))
	if q := f.queued(t); len(q) != 2 {
		t.Fatalf("%v", q)
	}
}

func TestAgendaChaveDoSlotImpedeDuplicarEntreMotores(t *testing.T) {
	f := newEngineFx(t, "Schedule.every:\"1m\":\"limpar\"\n")
	f.e.tick(jt0)
	other := newJobEngine(f.e.q, f.srv) // outro processo no mesmo arquivo
	other.logf = func(string, ...any) {}
	now := jt0.Add(time.Minute)
	f.e.tick(now)
	other.tick(now)
	if q := f.queued(t); len(q) != 1 {
		t.Fatalf("dois motores, um disparo: %v", q)
	}
}

func TestAgendaCronNaoPerdeDisparoRecenteSemCatchUp(t *testing.T) {
	f := newEngineFx(t, "Schedule.every:\"30s\":\"limpar\":{\"catch_up\": false}\n")
	f.e.tick(jt0)
	f.e.tick(jt0.Add(31 * time.Second)) // 1s depois do slot: ainda é recente
	if q := f.queued(t); len(q) != 1 {
		t.Fatalf("%v", q)
	}
}

func TestAgendaRecarregaEMantemAnteriorSeErrar(t *testing.T) {
	f := newEngineFx(t, "Schedule.every:\"1m\":\"limpar\"\n")
	f.e.tick(jt0)
	f.e.tick(jt0.Add(time.Minute))
	if q := f.queued(t); len(q) != 1 {
		t.Fatal(q)
	}
	f.finishAll(jt0.Add(2 * time.Minute))
	path := filepath.Join(f.root, "agenda.djo")
	os.WriteFile(path, []byte("Schedule.every:\"1m\":\"nao_existe\"\n"), 0o644)
	future := time.Now().Add(time.Hour)
	os.Chtimes(path, future, future)
	f.e.tick(jt0.Add(3 * time.Minute))
	if !strings.Contains(f.log(), "a agenda anterior continua valendo") {
		t.Errorf("faltou o aviso: %s", f.log())
	}
	if q := f.queued(t); len(q) != 1 {
		t.Errorf("a agenda antiga continua disparando: %v", q)
	}
	f.finishAll(jt0.Add(4 * time.Minute))
	// uma agenda nova válida passa a valer
	os.WriteFile(path, []byte("Schedule.every:\"1m\":\"recebe\"\n"), 0o644)
	os.Chtimes(path, future.Add(time.Hour), future.Add(time.Hour))
	f.e.tick(jt0.Add(5 * time.Minute)) // recarrega; o recebe começa a contar daqui
	f.e.tick(jt0.Add(6 * time.Minute))
	if q := f.queued(t); len(q) != 1 || q[0] != "recebe" {
		t.Errorf("%v", q)
	}
	// agenda apagada: nada mais dispara
	f.finishAll(jt0.Add(7 * time.Minute))
	os.Remove(path)
	f.e.tick(jt0.Add(8 * time.Minute))
	f.e.tick(jt0.Add(20 * time.Minute))
	if q := f.queued(t); len(q) != 0 {
		t.Errorf("%v", q)
	}
}

func TestMotorWorkersDeVerdadeProcessamERetentam(t *testing.T) {
	f := newEngineFx(t, "")
	var ran, failOnce atomic.Int32
	f.e.poll = 10 * time.Millisecond
	f.e.run = func(j *jobRow) (bool, error) {
		ran.Add(1)
		if j.fn == "falha_uma_vez" && failOnce.Add(1) == 1 {
			return false, fmt.Errorf("primeira vez falha")
		}
		return false, nil
	}
	clock := &fakeClock{t: jt0}
	f.e.now = clock.now
	f.e.startWorkers(3)
	defer f.e.shutdown(2 * time.Second)
	for i := 0; i < 20; i++ {
		f.e.enqueue(jreq("ok"), false)
	}
	id, _, _, _ := f.e.enqueue(jreq("falha_uma_vez"), false)
	waitFor(t, func() bool { return ran.Load() >= 21 }, "os 20 + a primeira tentativa")
	if j := f.e.q.get(id); j.state != "queued" || j.tries != 1 {
		t.Fatalf("depois da falha: %+v", j)
	}
	clock.advance(20 * time.Second) // passa o backoff de 15s
	f.e.notify()
	waitFor(t, func() bool { return f.e.q.get(id).state == "done" }, "a segunda tentativa")
	if n := f.e.q.counts()["done"]; n != 21 {
		t.Errorf("done = %d", n)
	}
}

func TestMotorLimiteDeEnfileiramentoPorMinuto(t *testing.T) {
	f := newEngineFx(t, "")
	old := jobPerMinute
	jobPerMinute = 3
	defer func() { jobPerMinute = old }()
	clock := &fakeClock{t: jt0}
	f.e.now = clock.now
	for i := 0; i < 3; i++ {
		if _, _, full, _ := f.e.enqueue(jreq("a"), false); full {
			t.Fatalf("%d recusado", i)
		}
	}
	if _, _, full, _ := f.e.enqueue(jreq("a"), false); !full {
		t.Error("o 4º no mesmo minuto devia ser recusado")
	}
	clock.advance(61 * time.Second)
	if _, _, full, _ := f.e.enqueue(jreq("a"), false); full {
		t.Error("depois de um minuto volta a aceitar")
	}
}

func TestMotorDesligamentoEsperaOsRapidosEAbandonaOsLentos(t *testing.T) {
	f := newEngineFx(t, "")
	f.e.poll = 5 * time.Millisecond
	release := make(chan struct{})
	var started atomic.Int32
	f.e.run = func(j *jobRow) (bool, error) {
		started.Add(1)
		if j.fn == "lento" {
			<-release
		} else {
			time.Sleep(80 * time.Millisecond)
		}
		return false, nil
	}
	f.e.startWorkers(2)
	fast, _, _, _ := f.e.enqueue(jreq("rapido"), false)
	slow, _, _, _ := f.e.enqueue(jreq("lento"), false)
	waitFor(t, func() bool { return started.Load() == 2 }, "os dois começarem")
	f.e.enqueue(jreq("depois"), false) // chega durante o desligamento: não deve ser pego
	if f.e.shutdown(300 * time.Millisecond) {
		t.Fatal("o job lento ainda roda: o desligamento devia estourar o prazo")
	}
	if f.e.q.get(fast).state != "done" {
		t.Errorf("o rápido devia terminar: %+v", f.e.q.get(fast))
	}
	if j := f.e.q.get(slow); j.state != "running" {
		t.Errorf("o lento fica running (o lease o recupera no próximo start): %+v", j)
	}
	if n := f.e.q.counts()["queued"]; n != 1 {
		t.Errorf("o job novo não devia ter sido pego: %d", n)
	}
	// "próximo start": o lease vence e o job volta
	f.e.q.sweep(jt0.Add(time.Hour).Add(time.Duration(time.Now().Unix()-jt0.Unix()) * time.Second))
	if j := f.e.q.get(slow); j.state != "queued" {
		t.Errorf("o lease devia devolver o job: %+v", j)
	}
	close(release)
}

func TestMotorPanicNoJobViraFalha(t *testing.T) {
	f := newEngineFx(t, "")
	f.e.run = func(j *jobRow) (bool, error) { panic("boom") }
	id, _, _, _ := f.e.enqueue(jreq("a"), false)
	j, _ := f.e.q.claim(time.Now())
	f.e.runOne(j)
	row := f.e.q.get(id)
	if row.state != "queued" || !strings.Contains(row.err, "erro interno: boom") {
		t.Errorf("%+v", row)
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("esperei 5s por: %s", what)
}

func TestAgendaCronDepoisDeParadaLongaDisparaUmaVez(t *testing.T) {
	f := newEngineFx(t, "Schedule.cron:\"* * * * *\":\"limpar\"\n")
	f.e.tick(jt0) // marca o ponto de partida
	later := jt0.Add(8*24*time.Hour + 30*time.Second)
	f.e.tick(later)
	f.e.tick(later.Add(time.Second)) // o tick seguinte não pode disparar de novo
	f.e.tick(later.Add(2 * time.Second))
	rows, _ := f.e.q.list("queued", 10)
	if len(rows) != 1 {
		t.Fatalf("depois de 8 dias parado: %d jobs, devia ser 1", len(rows))
	}
	want := later.Truncate(time.Minute).Unix()
	if !strings.HasSuffix(rows[0].key, fmt.Sprint(want)) {
		t.Errorf("devia usar o último minuto (%d): %s", want, rows[0].key)
	}
	// o horário mensal fica longe de 25h e ainda assim é achado (sem pular)
	g := newEngineFx(t, "Schedule.cron:\"0 0 1 * *\":\"limpar\"\n")
	g.e.tick(jt0)
	g.e.tick(time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)) // parado de janeiro a março: perdeu 1/fev e 1/mar
	rows, _ = g.e.q.list("queued", 10)
	if len(rows) != 1 || !strings.HasSuffix(rows[0].key, fmt.Sprint(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Unix())) {
		t.Errorf("mensal: %+v", rows)
	}
}

func TestJobInvokeRecusaFuncaoDeBooster(t *testing.T) {
	in := NewInterp(io.Discard)
	in.funcs["de_booster"] = &FuncDecl{Name: "de_booster", Booster: &booster{name: "x"}}
	err, _, _ := jobInvoke(in, "de_booster", "{}")
	if err == nil || !strings.Contains(err.Error(), "de booster") {
		t.Errorf("%v", err)
	}
}

func TestJobAvisoDeFilaCheiaLimitado(t *testing.T) {
	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	jobWarnMu.Lock()
	jobWarnLast = time.Time{}
	jobWarnMu.Unlock()
	for i := 0; i < 50; i++ {
		jobWarnFull()
	}
	if n := strings.Count(buf.String(), "Job.run recusado"); n != 1 {
		t.Errorf("%d avisos para 50 recusas", n)
	}
	if !strings.Contains(buf.String(), "20") { // o log padrão traz data e hora
		t.Errorf("sem timestamp: %q", buf.String())
	}
}
