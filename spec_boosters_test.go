//go:build !noweb && !nodb

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Os specs dos boosters oficiais (boosters-oficiais-specs/<nome>/spec) rodam num projeto de teste
// com o booster e o expect instalados. Ficam fora da pasta do booster porque lá só valem main.djo e core/.
func TestSpecBoosterMarkdown(t *testing.T) {
	root := t.TempDir()
	for _, b := range []string{"expect", "markdown"} {
		var o, e strings.Builder
		if code := boosterAdd(root, filepath.Join("boosters-oficiais", b), "local", &o, &e); code != 0 {
			t.Fatalf("booster add %s: %s %s", b, o.String(), e.String())
		}
	}
	data, err := os.ReadFile("boosters-oficiais-specs/markdown/spec/markdown_spec.djo")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "spec"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "spec", "markdown_spec.djo"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errw := runSpec(t, root)
	if code != 0 || errw != "" || !strings.Contains(out, "0 falhas, 0 erros, 0 pulados") || strings.Contains(out, " 0 specs") {
		t.Fatalf("%d %q\n%s", code, errw, out)
	}
}
