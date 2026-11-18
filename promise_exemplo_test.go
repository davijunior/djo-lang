//go:build !noweb && !nodb

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exemplos/promises tem specs de verdade (Db.begin, Job.give_up, uniq...): precisam passar.
func TestSpecExemploPromises(t *testing.T) {
	root := t.TempDir()
	filepath.WalkDir("exemplos/promises", func(p string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel("exemplos/promises", p)
		if err != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(root, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, rel), b, 0o644)
	})
	var o, e strings.Builder
	if code := boosterAdd(root, "boosters-oficiais/expect", "local", &o, &e); code != 0 {
		t.Fatalf("booster add: %s %s", o.String(), e.String())
	}
	code, out, errw := runSpec(t, root)
	if code != 0 || errw != "" || !strings.Contains(out, "10 specs, 0 falhas, 0 erros, 0 pulados") {
		t.Fatalf("%d %q\n%s", code, errw, out)
	}
}
