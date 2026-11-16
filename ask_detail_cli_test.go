//go:build !nonet && !noweb && !nodb

package main

import (
	"strings"
	"testing"
)

func TestAskCLIDetailItem(t *testing.T) {
	a := askDetailSite(t, askDetailAll)
	code, out, errs := runAsk(t, "", nil, a.ts.URL, `show post "bem-vindo"`)
	want := "slug: bem-vindo\ntitle: Bem-vindo ao djo\nviews: 10\n\nbody:\nLINHA1\n<script>alert(1)</script>\n"
	if code != 0 || !strings.Contains(out, want) || strings.Contains(out, "---") {
		t.Fatalf("item: %d\n%q\n%s", code, out, errs)
	}
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "</script>") {
		t.Errorf("o texto longo vem por último, em bloco: %q", out)
	}
	// não achou: sai 0 com a mensagem, sem o texto do rascunho
	code, out, _ = runAsk(t, "", nil, a.ts.URL, `show post "rascunho"`)
	if code != 0 || !strings.Contains(out, "I did not find it.") || strings.Contains(out, "CORPO-DO-RASCUNHO") {
		t.Errorf("não achei: %d %q", code, out)
	}
	// lista: tabela, sem o texto
	code, out, _ = runAsk(t, "", nil, a.ts.URL, "list posts")
	if code != 0 || strings.Contains(out, "CORPO") || strings.Contains(out, "body") {
		t.Errorf("lista: %d %q", code, out)
	}
}
