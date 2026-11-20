//go:build !noweb

package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Em produção a resposta 500 não mostra a pilha nem caminhos; o modo dev e o log mostram.
func TestPilhaSoEmDevELog(t *testing.T) {
	err := runDjo(t, map[string]string{"core/a.djo": errCoreA}, "meio:3\n")
	for _, dev := range []bool{false, true} {
		s := &Server{dev: dev}
		w := httptest.NewRecorder()
		s.serverError(w, httptest.NewRequest("GET", "/x", nil), err)
		body := w.Body.String()
		if w.Code != 500 {
			t.Errorf("status %d", w.Code)
		}
		if got := strings.Contains(body, "core/a.djo"); got != dev {
			t.Errorf("dev=%v: caminho na resposta = %v: %q", dev, got, body)
		}
	}
}
