//go:build !nonet && !noweb && !nodb

package main

import (
	"strings"
	"testing"
)

// ponta a ponta: o cliente fala com o servidor real do /ask
func TestAskCLIContraServidorReal(t *testing.T) {
	a := askHTTPSite(t, askDjoWith(""), nil)
	// EN, público
	code, out, errs := runAsk(t, "", nil, a.ts.URL, "list posts first 2")
	if code != 0 || !strings.Contains(out, "Alpha") || !strings.Contains(out, "page 2") || !strings.Contains(out, "--page 2") || strings.Contains(out, "Draft one") {
		t.Fatalf("EN: %d\n%s\n%s", code, out, errs)
	}
	// página 2 pela flag
	code, out, _ = runAsk(t, "", nil, "--page", "2", a.ts.URL, "list posts first 2")
	if code != 0 || !strings.Contains(out, "Gamma") || strings.Contains(out, "Alpha") {
		t.Errorf("página 2: %d\n%s", code, out)
	}
	// PT
	code, out, _ = runAsk(t, "", nil, a.ts.URL, "quantos posts")
	if code != 0 || !strings.HasPrefix(out, "São 4.") {
		t.Errorf("PT: %d %q", code, out)
	}
	// nível agent por token, vindo do ambiente
	code, out, _ = runAsk(t, "", map[string]string{"DJO_ASK_TOKEN": "tok-agent"}, a.ts.URL, "list posts with status draft")
	if code != 0 || !strings.Contains(out, "Draft one") || strings.Contains(out, "TOPSECRET") {
		t.Errorf("agent: %d %s", code, out)
	}
	// token errado = 4; escrita = 3; não entendi = 3
	if code, _, _ := runAsk(t, "", map[string]string{"DJO_ASK_TOKEN": "errado"}, a.ts.URL, "list posts"); code != 4 {
		t.Errorf("401: %d", code)
	}
	if code, _, e := runAsk(t, "", nil, a.ts.URL, "delete all posts"); code != 3 || !strings.Contains(e, "I can't answer that.") {
		t.Errorf("escrita: %d %q", code, e)
	}
	if code, _, _ := runAsk(t, "", nil, a.ts.URL, "list users"); code != 3 {
		t.Errorf("tipo escondido: %d", code)
	}
}
