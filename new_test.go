package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func criar(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := newCmd(args, &out, &errw)
	return code, out.String(), errw.String()
}

func TestNewScriptRoda(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ferr")
	if code, _, e := criar(t, "-modelo", "script", dir); code != 0 {
		t.Fatal(e)
	}
	var out bytes.Buffer
	prog, err := parseFile(filepath.Join(dir, "principal.djo"))
	if err != nil {
		t.Fatal(err)
	}
	in := NewInterp(&out)
	in.root = dir
	if err := in.Run(prog); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "olá, mundo!" {
		t.Fatalf("saída: %q", out.String())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	if !strings.Contains(string(b), "# ferr") {
		t.Fatalf("README sem o nome: %s", b)
	}
}

func TestNewIotParseia(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sensor")
	if code, _, e := criar(t, "-modelo", "iot", dir); code != 0 {
		t.Fatal(e)
	}
	if _, err := parseFile(filepath.Join(dir, "leitura.djo")); err != nil {
		t.Fatal(err)
	}
}

func TestNewRecusa(t *testing.T) {
	base := t.TempDir()
	cheia := filepath.Join(base, "cheia")
	os.MkdirAll(cheia, 0o755)
	os.WriteFile(filepath.Join(cheia, "x.txt"), []byte("não apague"), 0o644)
	cases := map[string][]string{
		"pasta com arquivos":  {cheia},
		"modelo inexistente":  {"-modelo", "xyz", filepath.Join(base, "a")},
		"nome inválido":       {filepath.Join(base, "a b;c")},
		"sem pasta":           {},
		"nome só ponto-ponto": {filepath.Join(base, "..")},
	}
	for nome, args := range cases {
		if code, _, _ := criar(t, args...); code == 0 {
			t.Errorf("%s: deveria falhar", nome)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(cheia, "x.txt")); string(b) != "não apague" {
		t.Fatal("a pasta existente foi alterada")
	}
	// pasta existente mas vazia vale
	vazia := filepath.Join(base, "vazia")
	os.MkdirAll(vazia, 0o755)
	if code, _, e := criar(t, "-modelo", "script", vazia); code != 0 {
		t.Fatal(e)
	}
}
