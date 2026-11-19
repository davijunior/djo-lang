package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// lock{ key["omie:42:ListarClientes"]  wait[2]  ...corpo... }
//
// Exclusão mútua por chave dentro DESTE processo (djo serve). Quem não consegue a trava em `wait` segundos
// recebe o erro lock_busy (código estável, capturável). A trava vale por um prazo (lease); ao expirar ela é
// cedida à fila e a dona antiga recebe lock_revoked no PRÓXIMO passo (ninguém mata uma goroutine).

const (
	lockMaxKeyLen  = 200
	lockMaxWaiters = 100   // esperando na mesma chave
	lockMaxKeys    = 10000 // chaves ativas
	lockLeasePage  = 30 * time.Second
	lockLeaseOther = 60 * time.Second // script e djo spec
	lockWaitScript = 60 * time.Second // teto de wait fora de página e de job
)

// lockLeaseOverride: os testes encurtam o prazo (0 = o normal).
var lockLeaseOverride time.Duration

type lockHold struct {
	key      string
	revoked  atomic.Bool
	released bool // sob lockTab.mu
	timer    *time.Timer
}

type lockWaiter struct {
	ch      chan *lockHold
	granted bool // sob lockTab.mu
	lease   time.Duration
}

type lockState struct {
	holder *lockHold
	q      []*lockWaiter
}

var lockTab struct {
	mu sync.Mutex
	m  map[string]*lockState
}

func newHoldLocked(key string, lease time.Duration) *lockHold {
	h := &lockHold{key: key}
	h.timer = time.AfterFunc(lease, func() {
		h.revoked.Store(true) // só marca; a dona se interrompe no próximo passo
		lockRelease(h)
	})
	return h
}

// lockAcquire pega a trava, esperando até `wait` na fila (FIFO). ok=false: ocupada (ou tabela cheia).
func lockAcquire(key string, wait, lease time.Duration) (*lockHold, bool) {
	lockTab.mu.Lock()
	if lockTab.m == nil {
		lockTab.m = map[string]*lockState{}
	}
	st := lockTab.m[key]
	if st == nil {
		if len(lockTab.m) >= lockMaxKeys {
			lockTab.mu.Unlock()
			return nil, false
		}
		st = &lockState{}
		lockTab.m[key] = st
	}
	if st.holder == nil && len(st.q) == 0 {
		h := newHoldLocked(key, lease)
		st.holder = h
		lockTab.mu.Unlock()
		return h, true
	}
	if wait <= 0 || len(st.q) >= lockMaxWaiters {
		lockTab.mu.Unlock()
		return nil, false
	}
	w := &lockWaiter{ch: make(chan *lockHold, 1), lease: lease}
	st.q = append(st.q, w)
	lockTab.mu.Unlock()

	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case h := <-w.ch:
		return h, true
	case <-t.C:
		lockTab.mu.Lock()
		if w.granted { // a vez chegou junto com o fim da espera: vale
			lockTab.mu.Unlock()
			return <-w.ch, true
		}
		if st := lockTab.m[key]; st != nil {
			for i, x := range st.q {
				if x == w {
					st.q = append(st.q[:i], st.q[i+1:]...)
					break
				}
			}
			if st.holder == nil && len(st.q) == 0 {
				delete(lockTab.m, key)
			}
		}
		lockTab.mu.Unlock()
		return nil, false
	}
}

// lockRelease solta a trava (de novo é inofensivo) e passa a vez ao primeiro da fila.
func lockRelease(h *lockHold) {
	lockTab.mu.Lock()
	defer lockTab.mu.Unlock()
	if h.released {
		return
	}
	h.released = true
	h.timer.Stop()
	st := lockTab.m[h.key]
	if st == nil || st.holder != h {
		return
	}
	st.holder = nil
	if len(st.q) > 0 {
		w := st.q[0]
		st.q = st.q[1:]
		w.granted = true
		nh := newHoldLocked(h.key, w.lease)
		st.holder = nh
		w.ch <- nh
		return
	}
	delete(lockTab.m, h.key)
}

// lockCheck: a dona foi revogada (passou do prazo)? Chamado a cada passo da interpretação enquanto ela segura trava.
func (in *Interp) lockCheck() {
	for _, h := range in.locks {
		if h.revoked.Load() {
			panic(&djoError{Code: "lock_revoked", Fatal: true,
				Msg: fmt.Sprintf("a trava %q passou do prazo e foi cedida a outra execução; esta execução foi interrompida (o que ela já gravou não é desfeito)", h.key)})
		}
	}
}

func (in *Interp) isJobRun() bool { return in.job != nil && in.job.inJob && in.spec == nil }

// lockLease: o prazo da trava para esta execução.
func (in *Interp) lockLease() time.Duration {
	if lockLeaseOverride > 0 {
		return lockLeaseOverride
	}
	if in.isJobRun() && !in.deadline.IsZero() {
		if d := time.Until(in.deadline); d > time.Second {
			return d
		}
		return time.Second
	}
	if in.served && !in.isJobRun() && in.spec == nil {
		return lockLeasePage
	}
	return lockLeaseOther
}

func (in *Interp) execLock(n *LockStmt, env *Env) (any, bool) {
	if in.txOpen() {
		failc(n.Line, "lock_in_transaction", "lock: pegue a trava ANTES de Db.begin (esperar por uma trava com a transação aberta prende o banco para quem já tem a trava)")
	}
	kv := in.eval(n.Key, env)
	key, ok := kv.(string)
	if !ok || key == "" || len(key) > lockMaxKeyLen {
		failc(n.Line, "lock_args", "lock: key[...] é um texto de 1 a %d bytes (não ponha segredo na chave: ela aparece no Job.status e no log)", lockMaxKeyLen)
	}
	wait := in.lockWait(n, env)
	for _, h := range in.locks {
		if h.key == key {
			failc(n.Line, "lock_reentrant", "lock: esta execução já segura a trava %q (pegar de novo travaria para sempre)", key)
		}
	}
	started := time.Now()
	h, got := lockAcquire(key, wait, in.lockLease())
	if waited := time.Since(started); waited > 0 && (in.sleepMax > 0 || (in.served && !in.isJobRun())) {
		in.slept += waited.Milliseconds() // a espera da trava entra na mesma cota do Time.sleep
	}
	if !got {
		d := newMap()
		d.set("key", key)
		d.set("wait_s", int64(wait/time.Second))
		panic(&djoError{Line: n.Line, Code: "lock_busy", Data: d,
			Msg: fmt.Sprintf("a trava %q está ocupada (esperou %s)", key, fmtDur(wait))})
	}
	in.locks = append(in.locks, h)
	defer func() {
		lockRelease(h)
		for i, x := range in.locks {
			if x == h {
				in.locks = append(in.locks[:i], in.locks[i+1:]...)
				break
			}
		}
	}()
	return in.execBlock(n.Body, newEnv(env))
}

func fmtDur(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
	return d.String()
}

// lockWait calcula (e confere) o wait[...]: segundos (Int) ou texto como "500ms"/"2s".
func (in *Interp) lockWait(n *LockStmt, env *Env) time.Duration {
	v := in.eval(n.Wait, env)
	var d time.Duration
	switch x := v.(type) {
	case int64:
		d = time.Duration(x) * time.Second
	case string:
		var ok bool
		if d, ok = parseJobDur(x); !ok {
			failc(n.Line, "lock_args", "lock: wait[...] é um número de segundos ou um texto como \"2s\" ou \"1m\", mas recebeu %q", x)
		}
	default:
		failc(n.Line, "lock_args", "lock: wait[...] é um número de segundos ou um texto como \"2s\" ou \"1m\", mas recebeu %s", typeName(v))
	}
	if d < 0 {
		failc(n.Line, "lock_args", "lock: wait[...] não pode ser negativo")
	}
	switch {
	case in.sleepMax > 0: // job: a espera sai da mesma cota do Time.sleep (o timeout do job)
		if left := in.sleepMax - in.slept; d.Milliseconds() > left {
			failc(n.Line, "lock_args", "lock: wait de %s passa do que o job ainda pode esperar (%d s, o timeout dele)", fmtDur(d), left/1000)
		}
	case in.served: // página: 2 s no total, junto com o Time.sleep
		if left := int64(2000) - in.slept; d.Milliseconds() > left {
			failc(n.Line, "lock_args", "lock: numa página a espera máxima é 2 segundos no total (junto com Time.sleep); wait de %s não cabe", fmtDur(d))
		}
	default:
		if d > lockWaitScript {
			failc(n.Line, "lock_args", "lock: wait vai até %s", fmtDur(lockWaitScript))
		}
	}
	return d
}

// ---- parser ----

// lockAt: o token i começa um lock ("lock{" ou "lock" com o corpo indentado)?
func (p *Parser) lockAt(i int) bool {
	if !p.wordAt(i, "lock") || i+1 >= len(p.toks) {
		return false
	}
	nt := p.toks[i+1]
	if nt.Kind == LBrace && !nt.First {
		return true
	}
	return !p.tpl && nt.First && nt.Kind != RBrace && nt.Kind != EOF && nt.Col > p.toks[p.lineStartIdx(i)].Col && p.wordAt(i+1, "key") && i+2 < len(p.toks) && p.toks[i+2].Kind == LBrack
}

func (p *Parser) parseLock() Stmt {
	lt := p.next()
	n := &LockStmt{Line: lt.Line}
	b := p.openBlock(lt)
	for b.more() {
		t := p.peek()
		if t.Kind != IDENT || (t.Text != "key" && t.Text != "wait") || p.pos+1 >= len(p.toks) || p.toks[p.pos+1].Kind != LBrack {
			break
		}
		p.next()
		p.next()
		e := p.parseExpr()
		p.expect(RBrack, "']' para fechar "+t.Text)
		switch {
		case t.Text == "key" && n.Key == nil:
			n.Key = e
		case t.Text == "wait" && n.Wait == nil:
			n.Wait = e
		default:
			fail(t.Line, "o lock só pode ter um '%s'", t.Text)
		}
	}
	if n.Key == nil || n.Wait == nil {
		fail(lt.Line, "o lock precisa de key[...] e wait[...] no começo: lock{ key[\"minha:chave\"] wait[2] ... }")
	}
	n.Body = b.stmts()
	b.close("o lock")
	return n
}

// ---- djo spec ----

func (c *specCtx) releaseHolds() {
	for _, h := range c.holds {
		lockRelease(h)
	}
	c.holds = nil
}

func init() {
	ns := nativeNS("Spec")
	need := func(in *Interp, fn string, line int) *specCtx {
		if in.spec == nil {
			fail(line, "Spec.%s só existe quando os testes rodam com 'djo spec'", fn)
		}
		return in.spec
	}
	// Spec.lock_hold:"chave": simula OUTRA execução segurando a trava, até Spec.lock_release ou o fim do teste
	ns["lock_hold"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		c := need(in, "lock_hold", line)
		key := argStr(a, 0, line, "Spec.lock_hold")
		if _, dup := c.holds[key]; dup {
			fail(line, "Spec.lock_hold: a trava %q já está segurada neste teste", key)
		}
		h, ok := lockAcquire(key, 0, 10*time.Minute)
		if !ok {
			fail(line, "Spec.lock_hold: a trava %q já está ocupada por outra execução", key)
		}
		if c.holds == nil {
			c.holds = map[string]*lockHold{}
		}
		c.holds[key] = h
		return nil
	}}
	// Spec.lock_release:"chave": solta a trava que o Spec.lock_hold segurou
	ns["lock_release"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		c := need(in, "lock_release", line)
		key := argStr(a, 0, line, "Spec.lock_release")
		h, ok := c.holds[key]
		if !ok {
			fail(line, "Spec.lock_release: o teste não segurou a trava %q", key)
		}
		lockRelease(h)
		delete(c.holds, key)
		return nil
	}}
}
