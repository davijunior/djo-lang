//go:build !nodb && !noweb

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func runJobs(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := jobsMain(args, &o, &e)
	return code, o.String(), e.String()
}

func TestJobsCLI(t *testing.T) {
	root := t.TempDir()
	// sem fila ainda
	if code, out, _ := runJobs(t, root); code != 0 || !strings.Contains(out, "nenhuma fila") {
		t.Errorf("%d %q", code, out)
	}
	if code, _, _ := runJobs(t, "show", "1", root); code != 1 {
		t.Errorf("show sem fila: %d", code)
	}
	q, err := openJobQueue(jobQueuePath(root), true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	a := mustEnq(t, q, jreq("boa"), false, now)
	b := mustEnq(t, q, jreq("ruim"), false, now)
	d := mustEnq(t, q, jreq("outra_ruim"), false, now)
	q.claim(now)
	q.done(a, now)
	q.claim(now)
	q.fail(b, "deu ruim\ncom duas linhas", now, true)
	q.claim(now)
	q.fail(d, "outro erro", now, true)
	mustEnq(t, q, jreq("pendente"), false, now) // id 4

	_, out, _ := runJobs(t, root)
	for _, want := range []string{"queued    1", "done      1", "dead      2", "running   0"} {
		if !strings.Contains(out, want) {
			t.Errorf("resumo sem %q:\n%s", want, out)
		}
	}
	_, out, _ = runJobs(t, "list", "-state", "dead", root)
	if !strings.Contains(out, "ruim") || !strings.Contains(out, "outra_ruim") || strings.Contains(out, "boa") || !strings.Contains(out, "erro: deu ruim com duas linhas") {
		t.Errorf("list dead:\n%s", out)
	}
	_, out, _ = runJobs(t, "list", "-n", "1", root)
	if strings.Count(out, "\n") != 1 {
		t.Errorf("-n 1:\n%s", out)
	}
	code, out, _ := runJobs(t, "list", "--json", "-state", "dead", root)
	var arr []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &arr) != nil || len(arr) != 2 || arr[0]["state"] != "dead" {
		t.Errorf("json: %d %s", code, out)
	}
	_, out, _ = runJobs(t, "show", "2", root)
	if !strings.Contains(out, "função: ruim") || !strings.Contains(out, "estado: dead") || !strings.Contains(out, "erro: deu ruim") || !strings.Contains(out, `argumentos: {"a":1}`) {
		t.Errorf("show:\n%s", out)
	}
	if code, _, errw := runJobs(t, "show", "99", root); code != 1 || !strings.Contains(errw, "não existe o job 99") {
		t.Errorf("%d %q", code, errw)
	}
	// retry
	if code, out, _ := runJobs(t, "retry", "2", root); code != 0 || !strings.Contains(out, "voltou para a fila") {
		t.Errorf("retry: %d %q", code, out)
	}
	if code, _, _ := runJobs(t, "retry", "1", root); code != 1 {
		t.Error("retry de um job done devia falhar")
	}
	if _, out, _ := runJobs(t, "retry", "--all-dead", root); !strings.Contains(out, "1 job(s)") {
		t.Errorf("--all-dead: %q", out)
	}
	// cancel
	if code, _, _ := runJobs(t, "cancel", "4", root); code != 0 {
		t.Error("cancelar o pendente")
	}
	if code, _, _ := runJobs(t, "cancel", "4", root); code != 1 {
		t.Error("cancelar de novo devia falhar")
	}
	// purge
	if _, out, _ := runJobs(t, "purge", "-older", "1h", root); !strings.Contains(out, "0 job(s)") {
		t.Errorf("purge recente: %q", out)
	}
	if _, out, _ := runJobs(t, "purge", root); !strings.Contains(out, "2 job(s)") { // done + canceled
		t.Errorf("purge: %q", out)
	}
	// uso errado
	for _, args := range [][]string{{"show"}, {"show", "x"}, {"cancel", "-1"}, {"list", "a", "b"}, {"purge", "-older", "ontem"}} {
		if code, _, _ := runJobs(t, append(args, root)...); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
}
