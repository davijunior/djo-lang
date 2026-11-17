//go:build nodb || noweb

package main

import (
	"strings"
	"testing"
)

func TestJobNaoExisteNestePerfil(t *testing.T) {
	_, err := run(t, "f x\n    print 1\nvar i = Job.run:\"x\":{}")
	if err == nil || !strings.Contains(err.Error(), "Job não existe neste perfil") {
		t.Fatalf("%v", err)
	}
	if code, _, errw := runJobsOff(t); code != 1 || !strings.Contains(errw, "djo jobs não existe neste perfil") {
		t.Errorf("%d %q", code, errw)
	}
}

func runJobsOff(t *testing.T) (int, string, string) {
	var o, e strings.Builder
	code := jobsMain([]string{"list"}, &o, &e)
	return code, o.String(), e.String()
}
