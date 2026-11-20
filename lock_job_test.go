//go:build !nodb && !noweb

package main

import (
	"strings"
	"testing"
	"time"
)

func TestJobLockBusyReagendaSemGastarTentativa(t *testing.T) {
	f := newEngineFx(t, "")
	f.e.run = func(j *jobRow) (bool, error) {
		d := newMap()
		d.set("key", "omie:42:Listar")
		return false, &djoError{Code: "lock_busy", Data: d, Msg: "a trava está ocupada"}
	}
	r := jreq("a")
	r.Tries = 2
	id, _, _, _ := f.e.enqueue(r, false)
	for i := 1; i <= jobMaxLockResched; i++ {
		j, err := f.e.q.claim(time.Now().Add(24 * time.Hour))
		if err != nil || j == nil {
			t.Fatalf("%d: sem job para pegar: %v", i, err)
		}
		f.e.runOne(j)
		st := f.e.status(id)
		if s, _ := st.get("state"); s != "waiting_lock" {
			t.Fatalf("%d: state %v", i, s)
		}
		if tr, _ := st.get("tries"); tr != int64(0) {
			t.Fatalf("%d: a tentativa foi gasta: %v", i, tr)
		}
		if n, _ := st.get("lock_reschedules"); n != int64(i) {
			t.Fatalf("%d: reagendamentos %v", i, n)
		}
		if k, _ := st.get("lock_key"); k != "omie:42:Listar" {
			t.Fatalf("lock_key %v", k)
		}
	}
	// reagendamentos demais: vira falha normal e gasta tentativas
	j, _ := f.e.q.claim(time.Now().Add(48 * time.Hour))
	f.e.runOne(j)
	st := f.e.status(id)
	if tr, _ := st.get("tries"); tr != int64(1) {
		t.Errorf("depois de %d reagendamentos devia gastar a tentativa: %v", jobMaxLockResched, tr)
	}
	if s, _ := st.get("state"); s == "waiting_lock" {
		t.Errorf("state %v", s)
	}
}

// O job reexecuta do começo: o que ele fez antes do lock se repete a cada reagendamento (por isso a doc manda
// pôr o lock no início da função).
func TestJobLockReexecutaDoComecoEfeitoRepetido(t *testing.T) {
	h, _ := lockAcquire("k", 0, time.Minute)
	defer lockRelease(h)
	prog, err := Parse("f job\n    print \"efeito\"\n    lock{\n        key[\"k\"]\n        wait[0]\n        print \"dentro\"\n    }\n")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for i := 0; i < 3; i++ {
		in := newJobInterp(t.TempDir(), t.TempDir(), &out, time.Minute, nil)
		if err := in.Run(prog); err != nil {
			t.Fatal(err)
		}
		err, _, _ := jobInvoke(in, "job", "{}")
		var de *djoError
		if err == nil || !strings.Contains(err.Error(), "ocupada") {
			t.Fatalf("%d: %v", i, err)
		}
		_ = de
	}
	if strings.Count(out.String(), "efeito") != 3 || strings.Contains(out.String(), "dentro") {
		t.Errorf("%q", out.String())
	}
}
