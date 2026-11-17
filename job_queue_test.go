//go:build !nodb && !noweb

package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var jt0 = time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

func newTestQueue(t *testing.T) *jobQueue {
	t.Helper()
	jobJitter = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() {
		jobJitter = func(d time.Duration) time.Duration { return d + time.Duration(int64(d)/10) }
	})
	q, err := openJobQueue(filepath.Join(t.TempDir(), "_dados", "jobs.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func jreq(fn string) *jobReq {
	return &jobReq{Fn: fn, Args: `{"a":1}`, Tries: 3, Timeout: 60 * time.Second}
}

func mustEnq(t *testing.T, q *jobQueue, r *jobReq, held bool, now time.Time) int64 {
	t.Helper()
	id, _, full, err := q.enqueue(r, held, now)
	if err != nil || full {
		t.Fatalf("enqueue: %v full=%v", err, full)
	}
	return id
}

func TestJobFilaOrdemEAtraso(t *testing.T) {
	q := newTestQueue(t)
	a := mustEnq(t, q, jreq("a"), false, jt0)
	late := jreq("b")
	late.In = 10 * time.Minute
	b := mustEnq(t, q, late, false, jt0)
	c := mustEnq(t, q, jreq("c"), false, jt0)
	at := jreq("d")
	at.At = jt0.Add(time.Hour)
	d := mustEnq(t, q, at, false, jt0)

	var got []int64
	for {
		j, err := q.claim(jt0)
		if err != nil {
			t.Fatal(err)
		}
		if j == nil {
			break
		}
		got = append(got, j.id)
		if j.tries != 1 || j.maxTries != 3 || j.timeoutS != 60 || j.args != `{"a":1}` {
			t.Errorf("job %+v", j)
		}
		q.done(j.id, jt0)
	}
	if len(got) != 2 || got[0] != a || got[1] != c {
		t.Fatalf("antes do atraso: %v (a=%d c=%d)", got, a, c)
	}
	if j, _ := q.claim(jt0.Add(9 * time.Minute)); j != nil {
		t.Error("o atraso de 10m ainda não venceu")
	}
	if j, _ := q.claim(jt0.Add(10 * time.Minute)); j == nil || j.id != b {
		t.Errorf("b devia vencer aos 10m: %v", j)
	}
	if j, _ := q.claim(jt0.Add(59 * time.Minute)); j != nil {
		t.Error("'at' ainda não venceu")
	}
	if j, _ := q.claim(jt0.Add(time.Hour)); j == nil || j.id != d {
		t.Errorf("d devia vencer em 'at': %v", j)
	}
}

func TestJobRetentativasBackoffEDead(t *testing.T) {
	q := newTestQueue(t)
	id := mustEnq(t, q, jreq("x"), false, jt0)
	now := jt0
	wants := []time.Duration{15 * time.Second, time.Minute, 0}
	for i, w := range wants {
		j, _ := q.claim(now)
		if j == nil || j.id != id || j.tries != i+1 {
			t.Fatalf("tentativa %d: %+v", i+1, j)
		}
		dead := q.fail(id, "falhou "+string(rune('a'+i)), now, false)
		if i < 2 {
			if dead {
				t.Fatalf("tentativa %d não devia ser a última", i+1)
			}
			if x, _ := q.claim(now.Add(w - time.Second)); x != nil {
				t.Errorf("backoff de %v não respeitado", w)
			}
			now = now.Add(w)
		} else if !dead {
			t.Fatal("a 3ª falha devia ir para dead")
		}
	}
	j := q.get(id)
	if j.state != "dead" || j.err != "falhou c" || j.finished != now.Unix() || j.tries != 3 {
		t.Errorf("%+v", j)
	}
	if x, _ := q.claim(now.Add(100 * time.Hour)); x != nil {
		t.Error("dead não volta sozinho")
	}
	// retry manual
	if !q.retry(id, now) {
		t.Fatal("retry")
	}
	if j := q.get(id); j.state != "queued" || j.tries != 0 || j.err != "" {
		t.Errorf("%+v", j)
	}
}

func TestJobBackoffValores(t *testing.T) {
	want := []time.Duration{15 * time.Second, time.Minute, 4 * time.Minute, 16 * time.Minute, time.Hour, time.Hour}
	for i, w := range want {
		if got := jobBackoff(i + 1); got != w {
			t.Errorf("tentativa %d: %v, esperava %v", i+1, got, w)
		}
	}
}

func TestJobGiveUpEhPermanente(t *testing.T) {
	q := newTestQueue(t)
	id := mustEnq(t, q, jreq("x"), false, jt0)
	q.claim(jt0)
	if !q.fail(id, "desisti", jt0, true) {
		t.Fatal("permanent vai direto para dead")
	}
	if j := q.get(id); j.state != "dead" || j.tries != 1 {
		t.Errorf("%+v", j)
	}
}

func TestJobLeaseVencidoVoltaParaAFila(t *testing.T) {
	q := newTestQueue(t)
	id := mustEnq(t, q, jreq("x"), false, jt0)
	j, _ := q.claim(jt0)
	if j == nil {
		t.Fatal("claim")
	}
	// lease = timeout (60s) + 30s
	if n := q.sweep(jt0.Add(89 * time.Second)); n != 0 {
		t.Errorf("lease ainda valia: %d", n)
	}
	if n := q.sweep(jt0.Add(91 * time.Second)); n != 1 {
		t.Fatalf("recuperados: %d", n)
	}
	row := q.get(id)
	if row.state != "queued" || row.tries != 1 || !strings.Contains(row.err, "lease") {
		t.Errorf("%+v", row)
	}
	// a nova tentativa conta
	if x, _ := q.claim(jt0.Add(91*time.Second + 20*time.Second)); x == nil || x.tries != 2 {
		t.Errorf("segunda tentativa: %+v", x)
	}
	// um job que já esgotou as tentativas e perde o lease vai para dead
	one := jreq("y")
	one.Tries = 1
	id2 := mustEnq(t, q, one, false, jt0)
	q.claim(jt0.Add(200 * time.Second))
	q.sweep(jt0.Add(400 * time.Second))
	if j := q.get(id2); j.state != "dead" {
		t.Errorf("%+v", j)
	}
}

func TestJobChaveDeIdempotencia(t *testing.T) {
	q := newTestQueue(t)
	r := jreq("x")
	r.Key = "k1"
	id, existed, _, _ := q.enqueue(r, false, jt0)
	id2, existed2, _, _ := q.enqueue(r, false, jt0)
	if existed || !existed2 || id != id2 {
		t.Errorf("%d %v / %d %v", id, existed, id2, existed2)
	}
	// rodando também conta
	q.claim(jt0)
	if id3, ex, _, _ := q.enqueue(r, false, jt0); !ex || id3 != id {
		t.Errorf("rodando: %d %v", id3, ex)
	}
	// terminado: a chave fica livre
	q.done(id, jt0)
	if id4, ex, _, _ := q.enqueue(r, false, jt0); ex || id4 == id {
		t.Errorf("depois de done: %d %v", id4, ex)
	}
}

func TestJobRetidosHeldReleaseDiscard(t *testing.T) {
	q := newTestQueue(t)
	a := mustEnq(t, q, jreq("a"), true, jt0)
	b := mustEnq(t, q, jreq("b"), true, jt0)
	if j, _ := q.claim(jt0); j != nil {
		t.Fatal("held não roda")
	}
	q.release([]int64{a})
	q.discard([]int64{b})
	if q.get(b) != nil {
		t.Error("discard apaga")
	}
	if j, _ := q.claim(jt0); j == nil || j.id != a {
		t.Errorf("release liberou a: %v", j)
	}
	// discard não apaga um job que já foi liberado
	q.discard([]int64{a})
	if q.get(a) == nil {
		t.Error("discard só vale para held")
	}
	// held órfão só some depois de mais que o job pai mais longo (15 min de timeout + lease + folga)
	c := mustEnq(t, q, jreq("c"), true, jt0)
	q.sweep(jt0.Add(16 * time.Minute))
	if q.get(c) == nil {
		t.Error("um pai de 15 min ainda pode estar rodando: o filho retido não pode sumir")
	}
	q.sweep(jt0.Add(21 * time.Minute))
	if q.get(c) != nil {
		t.Error("held órfão devia sumir")
	}
}

func TestJobCancelar(t *testing.T) {
	q := newTestQueue(t)
	id := mustEnq(t, q, jreq("a"), false, jt0)
	if !q.cancel(id, jt0) {
		t.Fatal("cancelar na fila")
	}
	if q.cancel(id, jt0) {
		t.Error("já cancelado")
	}
	if j, _ := q.claim(jt0); j != nil {
		t.Error("cancelado não roda")
	}
	id2 := mustEnq(t, q, jreq("b"), false, jt0)
	q.claim(jt0)
	if q.cancel(id2, jt0) {
		t.Error("rodando não cancela")
	}
}

func TestJobRetencaoELimpeza(t *testing.T) {
	q := newTestQueue(t)
	a := mustEnq(t, q, jreq("a"), false, jt0)
	q.claim(jt0)
	q.done(a, jt0)
	b := mustEnq(t, q, jreq("b"), false, jt0)
	q.claim(jt0)
	q.fail(b, "x", jt0, true)
	q.sweep(jt0.Add(23 * time.Hour))
	if q.get(a) == nil || q.get(b) == nil {
		t.Fatal("ainda dentro da retenção")
	}
	q.sweep(jt0.Add(25 * time.Hour))
	if q.get(a) != nil {
		t.Error("done some depois de 24h")
	}
	if q.get(b) == nil {
		t.Error("dead fica 30 dias")
	}
	q.sweep(jt0.Add(31 * 24 * time.Hour))
	if q.get(b) != nil {
		t.Error("dead some depois de 30 dias")
	}
	// purge manual
	c := mustEnq(t, q, jreq("c"), false, jt0)
	q.claim(jt0)
	q.done(c, jt0)
	if n := q.purge(time.Hour, jt0.Add(30*time.Minute)); n != 0 {
		t.Errorf("purge de mais velhos que 1h: %d", n)
	}
	if n := q.purge(0, jt0.Add(time.Second)); n != 1 {
		t.Errorf("purge de tudo: %d", n)
	}
}

func TestJobLimiteDaFila(t *testing.T) {
	q := newTestQueue(t)
	if _, err := q.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO jobs(fn,args,state,run_at,max_tries,timeout_s,created_at) SELECT 'a','{}','queued',0,3,60,0 FROM n`, jobMaxPending); err != nil {
		t.Fatal(err)
	}
	if _, _, full, _ := q.enqueue(jreq("a"), false, jt0); !full {
		t.Error("a fila devia estar cheia")
	}
	// a agenda não é barrada pelo limite
	s := jreq("a")
	s.Sched = "x"
	if _, _, full, _ := q.enqueue(s, false, jt0); full {
		t.Error("a agenda passa do limite")
	}
}

func TestJobErroLongoEhCortadoEmUTF8(t *testing.T) {
	q := newTestQueue(t)
	id := mustEnq(t, q, jreq("a"), false, jt0)
	q.claim(jt0)
	q.fail(id, strings.Repeat("é", 3000), jt0, true)
	e := q.get(id).err
	if len(e) > jobErrMax || len(e) < jobErrMax-2 {
		t.Errorf("%d bytes", len(e))
	}
	for _, r := range e {
		if r == '�' {
			t.Fatal("cortou no meio de uma letra")
		}
	}
}

func TestJobClaimConcorrente(t *testing.T) {
	q := newTestQueue(t)
	const n = 40
	for i := 0; i < n; i++ {
		mustEnq(t, q, jreq("a"), false, jt0)
	}
	got := make(chan int64, n*2)
	done := make(chan struct{})
	for w := 0; w < 4; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				j, _ := q.claim(jt0)
				if j == nil {
					return
				}
				got <- j.id
			}
		}()
	}
	for w := 0; w < 4; w++ {
		<-done
	}
	close(got)
	seen := map[int64]bool{}
	for id := range got {
		if seen[id] {
			t.Fatalf("o job %d foi entregue duas vezes", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Errorf("%d de %d", len(seen), n)
	}
}

func TestJobTentativaAntigaNaoMarcaAExecucaoNova(t *testing.T) {
	q := newTestQueue(t)
	id := mustEnq(t, q, jreq("x"), false, jt0)
	old, _ := q.claim(jt0) // tentativa 1
	q.sweep(jt0.Add(2 * time.Minute))
	now2 := jt0.Add(3 * time.Minute)
	cur, _ := q.claim(now2) // outro worker pega: tentativa 2
	if cur == nil || cur.tries != 2 {
		t.Fatalf("%+v", cur)
	}
	// o worker antigo termina tarde: nem done nem fail valem para a tentativa 2
	q.done(id, now2, old.tries)
	if q.fail(id, "tardio", now2, true, old.tries) {
		t.Error("o worker antigo não pode matar a execução nova")
	}
	if row := q.get(id); row.state != "running" || row.tries != 2 {
		t.Fatalf("%+v", row)
	}
	q.done(id, now2, cur.tries)
	if q.get(id).state != "done" {
		t.Error("a tentativa atual marca normalmente")
	}
}
