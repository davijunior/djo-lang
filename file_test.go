package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runIn(t *testing.T, dir, src string, served bool) (string, error) {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	in := NewInterp(&sb)
	in.root = dir
	in.served = served
	err = in.Run(prog)
	return sb.String(), err
}

func TestFile(t *testing.T) {
	dir := t.TempDir()
	out, err := runIn(t, dir, `print File.exists:"a.txt"
print File.read:"a.txt" ?? "nada"
print File.write:"dados/a.txt":"linha1\nlinha2\n"
File.append:"dados/a.txt":"linha3\n"
var linhas = split:(trim:(File.read:"dados/a.txt")):"\n"
print len:linhas
print join:linhas:","
print File.exists:"dados/a.txt"
print File.list:"dados"
print File.list:"nao-existe" ?? "sem pasta"
File.write:"dados/b.json":(Json.encode:{"x": 1})
print (Json.decode:(File.read:"dados/b.json"))["x"]
print File.list:"dados"
var i = File.info:"dados/a.txt"
print i["size"]
print i["dir"]
print File.delete:"dados/a.txt"
print File.delete:"dados/a.txt"
File.mkdir:"x/y/z"
print File.list:"x"
File.write:"dados/a.txt":"novo"
File.write:"dados/a.txt":"sobrescrito"
print File.read:"dados/a.txt"
`, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "false\nnada\ntrue\n3\nlinha1,linha2,linha3\ntrue\n[\"a.txt\"]\nsem pasta\n1\n[\"a.txt\", \"b.json\"]\n21\nfalse\ntrue\nfalse\n[\"y/\"]\nsobrescrito\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "dados", "a.txt")); string(b) != "sobrescrito" {
		t.Fatalf("arquivo no disco: %q", b)
	}
	if es, _ := os.ReadDir(filepath.Join(dir, "dados")); len(es) != 2 {
		t.Fatalf("sobrou arquivo temporário: %v", es)
	}
}

func TestFileConfinado(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "site")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(parent, "segredo.txt"), []byte("s"), 0o644)
	os.Symlink(parent, filepath.Join(dir, "link"))
	os.Symlink(filepath.Join(parent, "segredo.txt"), filepath.Join(dir, "atalho.txt"))
	for _, src := range []string{
		`print File.read:"../segredo.txt"`,
		`print File.read:"a/../../segredo.txt"`,
		`print File.read:"/etc/passwd"`,
		`print File.read:"link/segredo.txt"`,
		`print File.read:"atalho.txt"`,
		`File.write:"link/novo.txt":"x"`,
		`File.write:"link/pasta/nova/x.txt":"x"`,
		`File.append:"..\\fora.txt":"x"`,
		`File.mkdir:"link/dir"`,
		`print File.write:"a.txt":5`,
		`print File.read:"a\u0000b"`,
	} {
		if _, err := runIn(t, dir, src, false); err == nil {
			t.Errorf("esperava erro em %q", src)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "pasta")); err == nil {
		t.Error("criou pasta fora do projeto")
	}
	if _, err := os.Stat(filepath.Join(parent, "novo.txt")); err == nil {
		t.Error("gravou fora do projeto")
	}
}

func TestFileEmPaginaServida(t *testing.T) {
	dir := t.TempDir()
	for _, src := range []string{
		`File.write:"x.djo":"print 1"`, `File.write:"p.html":"<script>"`, `File.write:"Img.SVG":"<svg>"`,
		`File.append:"a.js":"x"`, `File.write:"core/x.txt":"x"`, `File.write:"./CORE/x.txt":"x"`, `File.delete:"x.djo"`,
	} {
		if _, err := runIn(t, dir, src, true); err == nil {
			t.Errorf("página servida não deveria conseguir: %q", src)
		}
		if _, err := runIn(t, dir, src, false); err != nil && !strings.Contains(src, "delete") {
			t.Errorf("script local deveria conseguir %q: %v", src, err)
		}
	}
	if out, err := runIn(t, dir, `File.write:"_dados/log.txt":"ok"
print File.read:"_dados/log.txt"`, true); err != nil || out != "ok\n" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := runIn(t, dir, `File.write:"g.txt":"`+strings.Repeat("x", 6<<20)+`"
File.write:"h.txt":"`+strings.Repeat("x", 6<<20)+`"`, true); err == nil {
		t.Error("deveria limitar o total gravado por página")
	}
}

func TestTimeSleep(t *testing.T) {
	if _, err := runIn(t, t.TempDir(), "Time.sleep:10", false); err != nil {
		t.Fatal(err)
	}
	if _, err := runIn(t, t.TempDir(), "Time.sleep:1500\nTime.sleep:1500", true); err == nil {
		t.Error("página servida não pode esperar mais de 2 s no total")
	}
	for _, bad := range []string{"Time.sleep:-1", `Time.sleep:"a"`, "Time.sleep:99999999"} {
		if _, err := runIn(t, t.TempDir(), bad, false); err == nil {
			t.Errorf("esperava erro em %q", bad)
		}
	}
}
