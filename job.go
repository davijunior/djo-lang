//go:build !nodb && !noweb

package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// A fila de jobs: SQLite em <site>/_dados/jobs.db, workers em goroutines, retentativas com backoff,
// lease para o job de um processo que caiu voltar para a fila, e o disparo da agenda (agenda.djo).
//
// Garantia: at-least-once. Um job pode rodar mais de uma vez (queda no meio, lease vencido):
// escreva-o para ser idempotente, ou use a opção key.

const (
	hasJobs       = true
	jobOffReason  = ""
	jobLease      = 30 * time.Second // folga somada ao timeout para o lease
	jobSweepEvery = 30 * time.Second
	jobHeldKeep   = jobMaxTimeout + jobLease + 5*time.Minute // um filho retido precisa durar mais que o pai mais longo
	jobDoneKeep   = 24 * time.Hour
	jobDeadKeep   = 30 * 24 * time.Hour
	jobErrMax     = 2048
)

const jobSchema = `
CREATE TABLE IF NOT EXISTS jobs(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  fn TEXT NOT NULL,
  args TEXT NOT NULL,
  key TEXT,
  state TEXT NOT NULL,
  run_at INTEGER NOT NULL,
  tries INTEGER NOT NULL DEFAULT 0,
  max_tries INTEGER NOT NULL,
  timeout_s INTEGER NOT NULL,
  lease_until INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0,
  sched TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS jobs_due ON jobs(state, run_at);
CREATE UNIQUE INDEX IF NOT EXISTS jobs_key ON jobs(key) WHERE key IS NOT NULL AND state IN ('held','queued','running');
CREATE TABLE IF NOT EXISTS schedules(id TEXT PRIMARY KEY, last_slot INTEGER NOT NULL);
`

type jobQueue struct {
	db   *sql.DB
	mu   sync.Mutex
	path string
}

type jobRow struct {
	id                          int64
	fn, args, state, err, sched string
	tries, maxTries, timeoutS   int
	runAt, created, finished    int64
	key                         string
}

func jobQueuePath(root string) string { return filepath.Join(root, "_dados", "jobs.db") }

func openJobQueue(path string, create bool) (*jobQueue, error) {
	if !create {
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(jobSchema); err != nil {
		return nil, err
	}
	// colunas da trava (filas criadas por versões antigas não as têm; o erro de "coluna duplicada" é esperado)
	db.Exec(`ALTER TABLE jobs ADD COLUMN lock_resched INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE jobs ADD COLUMN lock_key TEXT NOT NULL DEFAULT ''`)
	return &jobQueue{db: db, path: path}, nil
}

func jobClip(s string) string {
	if len(s) > jobErrMax {
		s = s[:jobErrMax]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

func (q *jobQueue) enqueue(r *jobReq, held bool, now time.Time) (id int64, existed, full bool, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if r.Key != "" {
		var ex int64
		if q.db.QueryRow(`SELECT id FROM jobs WHERE key=? AND state IN ('held','queued','running')`, r.Key).Scan(&ex) == nil {
			return ex, true, false, nil
		}
	}
	var n int
	if err = q.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE state IN ('held','queued')`).Scan(&n); err != nil {
		return
	}
	if n >= jobMaxPending && r.Sched == "" {
		return 0, false, true, nil
	}
	runAt := now.Add(r.In)
	if !r.At.IsZero() {
		runAt = r.At
	}
	st := "queued"
	if held {
		st = "held"
	}
	var key any
	if r.Key != "" {
		key = r.Key
	}
	res, err := q.db.Exec(`INSERT INTO jobs(fn,args,key,state,run_at,max_tries,timeout_s,created_at,sched) VALUES(?,?,?,?,?,?,?,?,?)`,
		r.Fn, r.Args, key, st, runAt.Unix(), r.Tries, int(r.Timeout/time.Second), now.Unix(), r.Sched)
	if err != nil {
		if r.Key != "" { // outro processo enfileirou a mesma chave agora
			var ex int64
			if q.db.QueryRow(`SELECT id FROM jobs WHERE key=? AND state IN ('held','queued','running')`, r.Key).Scan(&ex) == nil {
				return ex, true, false, nil
			}
		}
		return 0, false, false, err
	}
	id, _ = res.LastInsertId()
	return id, false, false, nil
}

func (q *jobQueue) release(ids []int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range ids {
		q.db.Exec(`UPDATE jobs SET state='queued' WHERE id=? AND state='held'`, id)
	}
}

func (q *jobQueue) discard(ids []int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range ids {
		q.db.Exec(`DELETE FROM jobs WHERE id=? AND state='held'`, id)
	}
}

// claim pega o próximo job vencido e o marca como running (atômico).
func (q *jobQueue) claim(now time.Time) (*jobRow, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t := now.Unix()
	j := &jobRow{state: "running"}
	err := q.db.QueryRow(`UPDATE jobs SET state='running', tries=tries+1, lock_key='', lease_until=?+timeout_s+?
		WHERE id=(SELECT id FROM jobs WHERE state='queued' AND run_at<=? ORDER BY run_at, id LIMIT 1)
		RETURNING id, fn, args, tries, max_tries, timeout_s, sched`, t, int(jobLease/time.Second), t).
		Scan(&j.id, &j.fn, &j.args, &j.tries, &j.maxTries, &j.timeoutS, &j.sched)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

// done marca a execução como feita. Com tries, só vale se a tentativa ainda é essa (um worker antigo,
// cujo lease venceu, não marca a execução nova de outro worker).
func (q *jobQueue) done(id int64, now time.Time, tries ...int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	want := 0
	if len(tries) > 0 {
		want = tries[0]
	}
	q.db.Exec(`UPDATE jobs SET state='done', finished_at=?, lease_until=0, error='' WHERE id=? AND state='running' AND (?=0 OR tries=?)`, now.Unix(), id, want, want)
}

var jobJitter = func(d time.Duration) time.Duration { return d + time.Duration(rand.Int63n(int64(d)/10+1)) }

// jobBackoff: 15s, 1min, 4min, 16min, 1h (teto), com até 10% de variação.
func jobBackoff(tries int) time.Duration {
	d := 15 * time.Second
	for i := 1; i < tries && d < time.Hour; i++ {
		d *= 4
	}
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// fail registra a falha de uma tentativa: volta para a fila com backoff ou vai para dead.
func (q *jobQueue) fail(id int64, msg string, now time.Time, permanent bool, attempt ...int) (dead bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	want := 0
	if len(attempt) > 0 {
		want = attempt[0]
	}
	var tries, max int
	if q.db.QueryRow(`SELECT tries, max_tries FROM jobs WHERE id=? AND state='running' AND (?=0 OR tries=?)`, id, want, want).Scan(&tries, &max) != nil {
		return false
	}
	msg = jobClip(msg)
	if permanent || tries >= max {
		q.db.Exec(`UPDATE jobs SET state='dead', error=?, finished_at=?, lease_until=0 WHERE id=?`, msg, now.Unix(), id)
		return true
	}
	q.db.Exec(`UPDATE jobs SET state='queued', error=?, run_at=?, lease_until=0 WHERE id=?`, msg, now.Add(jobJitter(jobBackoff(tries))).Unix(), id)
	return false
}

const jobMaxLockResched = 20

// deferLock reagenda um job que não conseguiu a trava, SEM gastar a tentativa (com backoff de 5 s a 60 s).
// Devolve false quando já foram reagendamentos demais (aí vale a falha normal).
func (q *jobQueue) deferLock(id int64, key, msg string, now time.Time, attempt int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	var n int
	if q.db.QueryRow(`SELECT lock_resched FROM jobs WHERE id=? AND state='running' AND tries=?`, id, attempt).Scan(&n) != nil || n >= jobMaxLockResched {
		return false
	}
	delay := 5 * time.Second << min(n, 4) // 5, 10, 20, 40, 80 -> teto de 60
	if delay > time.Minute {
		delay = time.Minute
	}
	q.db.Exec(`UPDATE jobs SET state='queued', tries=tries-1, lock_resched=lock_resched+1, lock_key=?, error=?, run_at=?, lease_until=0 WHERE id=?`,
		jobClip(key), jobClip(msg), now.Add(jobJitter(delay)).Unix(), id)
	return true
}

// lockInfo: quantos reagendamentos por trava o job já teve e a chave pela qual espera ("" se não espera).
func (q *jobQueue) lockInfo(id int64) (resched int, key string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.db.QueryRow(`SELECT lock_resched, lock_key FROM jobs WHERE id=?`, id).Scan(&resched, &key)
	return
}

// sweep devolve à fila o que ficou running com o lease vencido e limpa o que já passou da retenção.
func (q *jobQueue) sweep(now time.Time) (recovered int) {
	q.mu.Lock()
	rows, err := q.db.Query(`SELECT id FROM jobs WHERE state='running' AND lease_until>0 AND lease_until<?`, now.Unix())
	var ids []int64
	if err == nil {
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
	}
	q.mu.Unlock()
	for _, id := range ids {
		q.fail(id, "o processo parou durante a tentativa (o lease venceu)", now, false)
		recovered++
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.db.Exec(`DELETE FROM jobs WHERE state='held' AND created_at<?`, now.Add(-jobHeldKeep).Unix())
	q.db.Exec(`DELETE FROM jobs WHERE state IN ('done','canceled') AND finished_at>0 AND finished_at<?`, now.Add(-jobDoneKeep).Unix())
	q.db.Exec(`DELETE FROM jobs WHERE state='dead' AND finished_at<?`, now.Add(-jobDeadKeep).Unix())
	return recovered
}

func (q *jobQueue) get(id int64) *jobRow {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.getLocked(id)
}

const jobCols = `id, fn, args, state, tries, max_tries, timeout_s, run_at, created_at, finished_at, error, sched, COALESCE(key,'')`

func scanJob(sc interface{ Scan(...any) error }) (*jobRow, error) {
	j := &jobRow{}
	err := sc.Scan(&j.id, &j.fn, &j.args, &j.state, &j.tries, &j.maxTries, &j.timeoutS, &j.runAt, &j.created, &j.finished, &j.err, &j.sched, &j.key)
	return j, err
}

func (q *jobQueue) getLocked(id int64) *jobRow {
	j, err := scanJob(q.db.QueryRow(`SELECT `+jobCols+` FROM jobs WHERE id=?`, id))
	if err != nil {
		return nil
	}
	return j
}

func (q *jobQueue) cancel(id int64, now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	res, err := q.db.Exec(`UPDATE jobs SET state='canceled', finished_at=? WHERE id=? AND state IN ('queued','held')`, now.Unix(), id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (q *jobQueue) retry(id int64, now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	res, err := q.db.Exec(`UPDATE jobs SET state='queued', tries=0, run_at=?, error='', finished_at=0 WHERE id=? AND state='dead'`, now.Unix(), id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (q *jobQueue) retryAllDead(now time.Time) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	res, err := q.db.Exec(`UPDATE jobs SET state='queued', tries=0, run_at=?, error='', finished_at=0 WHERE state='dead'`, now.Unix())
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

func (q *jobQueue) list(state string, n int) ([]*jobRow, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	where, args := "", []any{}
	if state != "" {
		where, args = " WHERE state=?", append(args, state)
	}
	rows, err := q.db.Query(`SELECT `+jobCols+` FROM jobs`+where+` ORDER BY id DESC LIMIT ?`, append(args, n)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*jobRow
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

func (q *jobQueue) counts() map[string]int {
	q.mu.Lock()
	defer q.mu.Unlock()
	m := map[string]int{}
	rows, err := q.db.Query(`SELECT state, COUNT(*) FROM jobs GROUP BY state`)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int
		rows.Scan(&s, &n)
		m[s] = n
	}
	return m
}

func (q *jobQueue) purge(olderThan time.Duration, now time.Time) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	res, err := q.db.Exec(`DELETE FROM jobs WHERE state IN ('done','canceled','dead') AND finished_at<=?`, now.Add(-olderThan).Unix())
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

func (q *jobQueue) schedGet(id string) (int64, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var v int64
	if q.db.QueryRow(`SELECT last_slot FROM schedules WHERE id=?`, id).Scan(&v) != nil {
		return 0, false
	}
	return v, true
}

func (q *jobQueue) schedSet(id string, slot int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.db.Exec(`INSERT INTO schedules(id,last_slot) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET last_slot=excluded.last_slot`, id, slot)
}

func (q *jobQueue) schedActive(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	var n int
	q.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE sched=? AND state IN ('held','queued','running')`, id).Scan(&n)
	return n > 0
}

// ---- o motor: workers, manutenção e agenda ----

type jobEngine struct {
	q    *jobQueue
	srv  *Server
	now  func() time.Time
	poll time.Duration
	run  func(j *jobRow) (giveUp bool, err error)
	logf func(format string, a ...any)

	wake     chan struct{}
	stop     chan struct{}
	wg       sync.WaitGroup
	stopping atomic.Bool

	mu        sync.Mutex
	rate      []time.Time
	lastSweep time.Time

	amu        sync.Mutex
	agenda     []*schedDef
	agendaMod  time.Time
	agendaSize int64
	agendaOK   bool // já tentou carregar
}

func newJobEngine(q *jobQueue, s *Server) *jobEngine {
	e := &jobEngine{q: q, srv: s, now: time.Now, poll: time.Second, wake: make(chan struct{}, 1), stop: make(chan struct{})}
	e.logf = func(f string, a ...any) {
		if s != nil {
			s.logf(f, a...)
		}
	}
	e.run = e.execute
	return e
}

func (e *jobEngine) notify() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *jobEngine) allow(now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	keep := e.rate[:0]
	for _, t := range e.rate {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	e.rate = keep
	if len(e.rate) >= jobPerMinute {
		return false
	}
	e.rate = append(e.rate, now)
	return true
}

func (e *jobEngine) enqueue(r *jobReq, held bool) (int64, bool, bool, error) {
	now := e.now()
	if r.Sched == "" && !e.allow(now) {
		return 0, false, true, nil
	}
	id, existed, full, err := e.q.enqueue(r, held, now)
	if err == nil && !held && !existed && !full {
		e.notify()
	}
	return id, existed, full, err
}

func (e *jobEngine) status(id int64) *Map {
	j := e.q.get(id)
	if j == nil {
		return nil
	}
	st := j.state
	if st == "held" {
		st = "queued"
	}
	m := jobStatusMap(j.id, j.fn, st, j.tries, j.maxTries, j.runAt, j.created, j.finished, j.err)
	resched, key := e.q.lockInfo(j.id)
	m.set("lock_reschedules", int64(resched))
	if st == "queued" && key != "" {
		m.set("state", "waiting_lock")
		m.set("lock_key", key)
	}
	return m
}

func (e *jobEngine) cancel(id int64) bool { return e.q.cancel(id, e.now()) }

func (e *jobEngine) release(ids []int64) {
	e.q.release(ids)
	e.notify()
}

func (e *jobEngine) discard(ids []int64) { e.q.discard(ids) }

// startWorkers liga n workers e a manutenção (leases, retenção e agenda). Com n = 0 nada roda:
// a fila só recebe jobs (uma cópia que só atende páginas).
func (e *jobEngine) startWorkers(n int) {
	if n == 0 {
		return
	}
	for i := 0; i < n; i++ {
		e.wg.Add(1)
		go e.worker()
	}
	e.wg.Add(1)
	go e.maintenance()
}

func (e *jobEngine) worker() {
	defer e.wg.Done()
	for !e.stopping.Load() {
		j, err := e.q.claim(e.now())
		if err != nil {
			e.logf("job: não consegui pegar da fila: %v", err)
		}
		if j == nil {
			select {
			case <-e.wake:
			case <-time.After(e.poll):
			case <-e.stop:
				return
			}
			continue
		}
		e.runOne(j)
	}
}

func (e *jobEngine) runOne(j *jobRow) {
	t0 := time.Now()
	var giveUp bool
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("erro interno: %v", r)
			}
		}()
		giveUp, err = e.run(j)
	}()
	now := e.now()
	if err == nil {
		e.q.done(j.id, now, j.tries)
		e.logf("job %d %s: ok (%s)", j.id, j.fn, time.Since(t0).Round(time.Millisecond))
		return
	}
	var de *djoError
	if errors.As(err, &de) && de.Code == "lock_busy" && !giveUp {
		key := ""
		if m, ok := de.Data.(*Map); ok {
			if v, ok := m.get("key"); ok {
				key, _ = v.(string)
			}
		}
		if e.q.deferLock(j.id, key, err.Error(), now, j.tries) {
			e.logf("job %d %s: esperando a trava %q, reagendado sem gastar tentativa: %v", j.id, j.fn, key, err)
			return
		}
	}
	dead := e.q.fail(j.id, err.Error(), now, giveUp, j.tries)
	if dead {
		e.logf("job %d %s: FALHOU de vez (tentativa %d/%d): %v", j.id, j.fn, j.tries, j.maxTries, err)
	} else {
		e.logf("job %d %s: falhou (tentativa %d/%d), vai tentar de novo: %v", j.id, j.fn, j.tries, j.maxTries, err)
	}
}

// execute roda a função do job num Interp novo (como uma requisição, mas sem requisição).
func (e *jobEngine) execute(j *jobRow) (bool, error) {
	s := e.srv
	host := &jobHost{back: func() (jobBackend, error) { return e, nil }, hold: true, inJob: true}
	out := &jobOut{}
	in := newJobInterp(s.root, s.dbRoot, out, time.Duration(j.timeoutS)*time.Second, host)
	in.served = !s.allowPrivate
	in.mail = s.mailHost()
	in.httpMock = s.httpMock
	in.limiter = s.limiter
	if err := in.loadCore(s.root); err != nil {
		return false, err
	}
	err, _, gaveUp := jobInvoke(in, j.fn, j.args)
	if err != nil {
		host.discard()
	} else {
		host.release()
	}
	if len(out.buf) > 0 {
		e.logf("job %d %s: saída: %s", j.id, j.fn, strings.TrimSpace(string(out.buf)))
	}
	return gaveUp, err
}

func (e *jobEngine) maintenance() {
	defer e.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-t.C:
			e.tick(e.now())
		}
	}
}

// tick faz a manutenção de um instante: varredura de leases e retenção, e os disparos da agenda.
func (e *jobEngine) tick(now time.Time) {
	if e.lastSweep.IsZero() || now.Sub(e.lastSweep) >= jobSweepEvery {
		e.lastSweep = now
		if n := e.q.sweep(now); n > 0 {
			e.logf("job: %d job(s) de um processo que parou voltaram para a fila", n)
			e.notify()
		}
	}
	e.reloadAgenda()
	e.fireSchedules(now)
}

func (e *jobEngine) reloadAgenda() {
	if e.srv == nil {
		return
	}
	fi, err := os.Stat(filepath.Join(e.srv.root, agendaFile))
	e.amu.Lock()
	defer e.amu.Unlock()
	if err != nil {
		e.agenda, e.agendaOK = nil, true
		return
	}
	if e.agendaOK && fi.ModTime().Equal(e.agendaMod) && fi.Size() == e.agendaSize {
		return
	}
	e.agendaOK, e.agendaMod, e.agendaSize = true, fi.ModTime(), fi.Size()
	defs, err := loadAgenda(e.srv.root)
	if err != nil {
		e.logf("agenda.djo: %v (a agenda anterior continua valendo)", err)
		return
	}
	e.agenda = defs
}

func (e *jobEngine) fireSchedules(now time.Time) {
	e.amu.Lock()
	defs := append([]*schedDef(nil), e.agenda...)
	e.amu.Unlock()
	for _, d := range defs {
		lastU, ok := e.q.schedGet(d.id)
		if !ok {
			e.q.schedSet(d.id, now.Unix()) // primeira vez: conta a partir de agora
			continue
		}
		last := time.Unix(lastU, 0).UTC()
		var slot time.Time
		if d.cron == nil {
			if now.Before(last.Add(d.every)) {
				continue
			}
			k := now.Sub(last) / d.every
			slot = last.Add(k * d.every)
		} else {
			// o último horário <= agora, achado de uma vez só (e um disparo só, mesmo depois de uma parada longa).
			// Se o laço fica longo demais (cron de todo minuto e dias parado), pula para as últimas 25 horas:
			// um cron assim tem horários em qualquer dia.
			cur, jumped := last, false
			for i := 0; ; i++ {
				if i == 100000 && !jumped {
					jumped = true
					if j := now.Add(-25 * time.Hour); j.After(cur) {
						cur = j
					}
					i = 0
				}
				if i >= 100000 {
					break
				}
				nx := d.cron.next(cur)
				if nx.IsZero() || nx.After(now) {
					break
				}
				slot, cur = nx, nx
			}
			if slot.IsZero() {
				continue
			}
		}
		if now.Sub(slot) > schedFreshWindow && !d.catchUp {
			e.q.schedSet(d.id, slot.Unix())
			continue
		}
		if e.q.schedActive(d.id) {
			e.logf("agenda: %s %q ainda está na fila ou rodando; este disparo foi pulado", d.kind, d.fn)
			e.q.schedSet(d.id, slot.Unix())
			continue
		}
		r := &jobReq{Fn: d.fn, Args: d.args, Tries: d.tries, Timeout: d.timeout, Key: fmt.Sprintf("sched:%s:%d", d.id, slot.Unix()), Sched: d.id}
		if _, _, _, err := e.q.enqueue(r, false, now); err != nil {
			e.logf("agenda: não consegui enfileirar %s: %v", d.fn, err)
			continue
		}
		e.q.schedSet(d.id, slot.Unix())
		e.notify()
	}
}

// shutdown para de pegar jobs e espera os em andamento até d; o que sobrar volta pelo lease no próximo start.
func (e *jobEngine) shutdown(d time.Duration) bool {
	if e.stopping.Swap(true) {
		return true
	}
	close(e.stop)
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func init() {
	jobEngineFactory = func(st *jobState) (jobBackend, error) {
		s, ok := st.owner.(*Server)
		if !ok {
			return nil, fmt.Errorf("a fila de jobs precisa do servidor")
		}
		base := s.root
		if s.dbRoot != "" {
			base = s.dbRoot
		}
		q, err := openJobQueue(jobQueuePath(base), true)
		if err != nil {
			return nil, fmt.Errorf("não consegui abrir a fila de jobs: %v", err)
		}
		e := newJobEngine(q, s)
		n := st.workers
		if n < 0 {
			n = jobDefWorkers
		}
		e.startWorkers(n)
		s.logf("jobs: fila em %s (%d worker(s))", q.path, n)
		return e, nil
	}
}

// ---- integração com o Server ----

func (s *Server) jobHost() *jobHost {
	return &jobHost{back: s.jobs.backend, hold: true}
}

// jobsBoot liga a fila no começo se o projeto a usa (agenda.djo ou jobs.db com histórico).
func (s *Server) jobsBoot(workers int) {
	s.jobs.mu.Lock()
	s.jobs.workers = workers
	s.jobs.mu.Unlock()
	base := s.root
	if s.dbRoot != "" {
		base = s.dbRoot
	}
	if isFile(filepath.Join(s.root, agendaFile)) {
		if _, err := loadAgenda(s.root); err != nil {
			s.logf("%v", err)
		}
	}
	if isFile(filepath.Join(s.root, agendaFile)) || isFile(jobQueuePath(base)) {
		if _, err := s.jobs.backend(); err != nil {
			s.logf("jobs: %v", err)
		}
	}
}

func (s *Server) jobsShutdown(d time.Duration) {
	s.jobs.mu.Lock()
	be := s.jobs.be
	s.jobs.mu.Unlock()
	if e, ok := be.(*jobEngine); ok {
		if !e.shutdown(d) {
			s.logf("jobs: ainda há jobs rodando depois de %s; eles voltam para a fila no próximo start (pelo lease)", d)
		}
	}
}

// ---- djo jobs ----

func jobsMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("jobs", flag.ContinueOnError)
	fs.SetOutput(errw)
	state := fs.String("state", "", "só este estado (queued, running, done, dead, canceled)")
	n := fs.Int("n", 20, "quantos mostrar")
	asJSON := fs.Bool("json", false, "saída em JSON")
	allDead := fs.Bool("all-dead", false, "retry: todos os jobs que falharam de vez")
	older := fs.String("older", "0s", "purge: só os terminados há mais que isto (30s, 10m, 2h, 7d)")
	sub := "summary"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "list", "show", "retry", "cancel", "purge":
			sub, args = args[0], args[1:]
		}
	}
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	var id int64
	if sub == "show" || sub == "cancel" || (sub == "retry" && !*allDead) {
		if len(pos) == 0 {
			fmt.Fprintf(errw, "uso: djo jobs %s ID [pasta]\n", sub)
			return 2
		}
		v, err := strconv.ParseInt(pos[0], 10, 64)
		if err != nil || v < 1 {
			fmt.Fprintf(errw, "o id do job é um número (recebi %q)\n", pos[0])
			return 2
		}
		id, pos = v, pos[1:]
	}
	if len(pos) > 1 {
		fmt.Fprintln(errw, "uso: djo jobs [list|show ID|retry ID|retry --all-dead|cancel ID|purge] [-state s] [-n N] [--json] [pasta]")
		return 2
	}
	root := "."
	if len(pos) == 1 {
		root = pos[0]
	}
	path := jobQueuePath(root)
	q, err := openJobQueue(path, false)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "nenhuma fila de jobs em %s (ainda não existe)\n", path)
			if sub == "summary" || sub == "list" || sub == "purge" {
				return 0
			}
			return 1
		}
		fmt.Fprintf(errw, "não consegui abrir %s: %v\n", path, err)
		return 1
	}
	now := time.Now()
	switch sub {
	case "summary":
		c := q.counts()
		if *asJSON {
			b, _ := json.Marshal(c)
			fmt.Fprintln(out, string(b))
			return 0
		}
		fmt.Fprintf(out, "fila: %s\n", path)
		for _, st := range []string{"queued", "running", "done", "dead", "canceled", "held"} {
			fmt.Fprintf(out, "  %-9s %d\n", st, c[st])
		}
	case "list":
		rows, err := q.list(*state, *n)
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		if *asJSON {
			arr := []map[string]any{}
			for _, j := range rows {
				arr = append(arr, jobJSON(j))
			}
			b, _ := json.Marshal(arr)
			fmt.Fprintln(out, string(b))
			return 0
		}
		for _, j := range rows {
			fmt.Fprintf(out, "%-6d %-9s %-24s tentativa %d/%d  %s", j.id, j.state, j.fn, j.tries, j.maxTries, time.Unix(j.runAt, 0).UTC().Format("2006-01-02 15:04:05Z"))
			if j.err != "" {
				fmt.Fprintf(out, "  erro: %s", jobOneLine(j.err, 80))
			}
			fmt.Fprintln(out)
		}
		if len(rows) == 0 {
			fmt.Fprintln(out, "nenhum job")
		}
	case "show":
		j := q.get(id)
		if j == nil {
			fmt.Fprintf(errw, "não existe o job %d\n", id)
			return 1
		}
		if *asJSON {
			b, _ := json.Marshal(jobJSON(j))
			fmt.Fprintln(out, string(b))
			return 0
		}
		f := func(k string, v any) { fmt.Fprintf(out, "%s: %v\n", k, v) }
		f("id", j.id)
		f("função", j.fn)
		f("estado", j.state)
		f("tentativas", fmt.Sprintf("%d de %d", j.tries, j.maxTries))
		f("executa em", time.Unix(j.runAt, 0).UTC().Format(time.RFC3339))
		f("criado em", time.Unix(j.created, 0).UTC().Format(time.RFC3339))
		f("timeout", fmt.Sprintf("%ds", j.timeoutS))
		if j.key != "" {
			f("chave", j.key)
		}
		if j.sched != "" {
			f("agenda", j.sched)
		}
		f("argumentos", j.args)
		if j.err != "" {
			f("erro", j.err)
		}
	case "retry":
		if *allDead {
			fmt.Fprintf(out, "%d job(s) voltaram para a fila\n", q.retryAllDead(now))
			return 0
		}
		if !q.retry(id, now) {
			fmt.Fprintf(errw, "o job %d não existe ou não está em dead\n", id)
			return 1
		}
		fmt.Fprintf(out, "o job %d voltou para a fila\n", id)
	case "cancel":
		if !q.cancel(id, now) {
			fmt.Fprintf(errw, "o job %d não existe ou não está mais na fila\n", id)
			return 1
		}
		fmt.Fprintf(out, "o job %d foi cancelado\n", id)
	case "purge":
		d, ok := parseJobDur(*older)
		if !ok {
			fmt.Fprintf(errw, "-older: %q não é uma duração (30s, 10m, 2h, 7d)\n", *older)
			return 2
		}
		fmt.Fprintf(out, "%d job(s) apagados\n", q.purge(d, now))
	}
	return 0
}

func jobOneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

func jobJSON(j *jobRow) map[string]any {
	m := map[string]any{"id": j.id, "fn": j.fn, "state": j.state, "tries": j.tries, "max_tries": j.maxTries,
		"run_at": time.Unix(j.runAt, 0).UTC().Format(time.RFC3339), "timeout": j.timeoutS, "args": json.RawMessage(j.args)}
	if j.err != "" {
		m["error"] = j.err
	}
	if j.key != "" {
		m["key"] = j.key
	}
	if j.sched != "" {
		m["sched"] = j.sched
	}
	return m
}
