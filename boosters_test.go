package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkBooster(t *testing.T, root, nome, manifest string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "boosters", nome)
	os.MkdirAll(dir, 0o755)
	if manifest == "" {
		manifest = `{"nome":"` + nome + `","versao":"1.0.0","descricao":"teste"}`
	}
	os.WriteFile(filepath.Join(dir, "booster.json"), []byte(manifest), 0o644)
	for n, c := range files {
		os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644)
	}
}

func runWith(t *testing.T, root, src string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	in := NewInterp(&out)
	in.root = root
	if err := in.loadCore(root); err != nil {
		return "", err
	}
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	err = in.Run(prog)
	return out.String(), err
}

const dobra = "f dobro\n    params[n[Int]]\n    return n * 2\n\nf quadruplo\n    params[n[Int]]\n    return dobro:(dobro:n)\n"

func TestBoosterChamaPeloNome(t *testing.T) {
	root := t.TempDir()
	mkBooster(t, root, "mat", "", map[string]string{"main.djo": dobra})
	// o core tem uma função com o mesmo nome; a do booster não se mistura
	os.MkdirAll(filepath.Join(root, "core"), 0o755)
	os.WriteFile(filepath.Join(root, "core", "c.djo"), []byte("f dobro\n    params[n[Int]]\n    return 1000\n"), 0o644)
	out, err := runWith(t, root, "print mat.quadruplo:3\nprint dobro:3\n")
	if err != nil || out != "12\n1000\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestBoosterNaoEnxergaOCore(t *testing.T) {
	root := t.TempDir()
	mkBooster(t, root, "mat", "", map[string]string{"main.djo": "f usa\n    return segredo:\n"})
	os.MkdirAll(filepath.Join(root, "core"), 0o755)
	os.WriteFile(filepath.Join(root, "core", "c.djo"), []byte("f segredo\n    return 1\n"), 0o644)
	_, err := runWith(t, root, "print mat.usa:\n")
	if err == nil || !strings.Contains(err.Error(), "não enxerga o core") {
		t.Fatalf("o booster não pode chamar o core: %v", err)
	}
}

func TestBoosterPermissoes(t *testing.T) {
	root := t.TempDir()
	mkBooster(t, root, "ler", "", map[string]string{"main.djo": "f ler\n    return File.read:\"x.txt\"\n\nf json\n    return Json.encode:[1]\n"})
	if out, err := runWith(t, root, "print ler.json:\n"); err != nil || out != "[1]\n" {
		t.Fatalf("namespace puro vale sem permissão: %q %v", out, err)
	}
	_, err := runWith(t, root, "print ler.ler:\n")
	if err == nil || !strings.Contains(err.Error(), "não declarou essa permissão") {
		t.Fatalf("File sem permissão: %v", err)
	}
	os.WriteFile(filepath.Join(root, "x.txt"), []byte("oi"), 0o644)
	root2 := t.TempDir()
	os.WriteFile(filepath.Join(root2, "x.txt"), []byte("oi"), 0o644)
	mkBooster(t, root2, "ler", `{"nome":"ler","versao":"1","permissoes":["File"]}`, map[string]string{"main.djo": "f ler\n    return File.read:\"x.txt\"\n"})
	if out, err := runWith(t, root2, "print ler.ler:\n"); err != nil || out != "oi\n" {
		t.Fatalf("com permissão: %q %v", out, err)
	}
	// o projeto continua usando File sem restrição, mesmo chamando o booster
	if _, err := runWith(t, root, "print File.read:\"x.txt\"\n"); err != nil {
		t.Fatal(err)
	}
}

func TestBoosterSoDeclaraFuncoes(t *testing.T) {
	for nome, src := range map[string]string{"solto": "print 1\n", "estrutura": "struct A\n    x[Int]\n"} {
		root := t.TempDir()
		mkBooster(t, root, nome, "", map[string]string{"main.djo": src})
		if _, err := runWith(t, root, "print 1\n"); err == nil || !strings.Contains(err.Error(), "não é permitido") && !strings.Contains(err.Error(), "só valem funções") {
			t.Errorf("%s: %v", nome, err)
		}
	}
}

func TestBoosterManifestoInvalido(t *testing.T) {
	for manifest, want := range map[string]string{
		`{"nome":"Mat","versao":"1"}`:                      "inválido",
		`{"nome":"mat"}`:                                   "falta a versão",
		`{"nome":"outro","versao":"1"}`:                    "a pasta se chama",
		`{"nome":"mat","versao":"1","permissoes":["Foo"]}`: "desconhecida",
		`nao e json`:                                       "inválido",
	} {
		root := t.TempDir()
		mkBooster(t, root, "mat", manifest, map[string]string{"main.djo": dobra})
		if _, err := runWith(t, root, "print 1\n"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v (queria %q)", manifest, err, want)
		}
	}
}

func TestVariavelSombreiaBooster(t *testing.T) {
	root := t.TempDir()
	mkBooster(t, root, "mat", "", map[string]string{"main.djo": dobra})
	out, err := runWith(t, root, "var mat = {\"dobro\": 7}\nprint mat[\"dobro\"]\n")
	if err != nil || out != "7\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestBoosterCLI(t *testing.T) {
	src := t.TempDir()
	mkBooster(t, src, "mat", "", map[string]string{"main.djo": dobra})
	srcDir := filepath.Join(src, "boosters", "mat")
	root := t.TempDir()
	wd, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(wd)
	var out, errw bytes.Buffer
	if c := boosterCmd([]string{"add", srcDir}, &out, &errw); c != 0 {
		t.Fatalf("add: %d %s", c, errw.String())
	}
	if _, err := os.Stat("boosters.lock"); err != nil {
		t.Fatal("sem lock")
	}
	if p := verifyBoosters(root); len(p) != 0 {
		t.Fatalf("verify: %v", p)
	}
	if c := boosterCmd([]string{"add", srcDir}, &out, &errw); c == 0 || !strings.Contains(errw.String(), "já está instalado") {
		t.Fatalf("add duplicado: %d %s", c, errw.String())
	}
	if res, err := runWith(t, root, "print mat.quadruplo:2\n"); err != nil || res != "8\n" {
		t.Fatalf("%q %v", res, err)
	}
	// mexer no código quebra a conferência
	os.WriteFile(filepath.Join(root, "boosters", "mat", "main.djo"), []byte(dobra+"\nf extra\n    return 1\n"), 0o644)
	if p := verifyBoosters(root); len(p) != 1 || !strings.Contains(p[0], "foi alterado") {
		t.Fatalf("verify alterado: %v", p)
	}
	// pasta solta que não está no lock
	mkBooster(t, root, "intruso", "", map[string]string{"main.djo": dobra})
	if p := verifyBoosters(root); len(p) != 2 {
		t.Fatalf("verify intruso: %v", p)
	}
	out.Reset()
	if c := boosterCmd([]string{"remove", "intruso"}, &out, &errw); c != 0 {
		t.Fatal(errw.String())
	}
	if c := boosterCmd([]string{"remove", "mat"}, &out, &errw); c != 0 {
		t.Fatal(errw.String())
	}
	if _, err := os.Stat("boosters.lock"); err == nil {
		t.Fatal("o lock vazio deveria sumir")
	}
	if c := boosterCmd([]string{"add", t.TempDir()}, &out, &errw); c == 0 {
		t.Fatal("add de pasta sem booster.json deveria falhar")
	}
}

func TestBoosterRecusaLinkSimbolico(t *testing.T) {
	root := t.TempDir()
	mkBooster(t, root, "mat", "", map[string]string{"main.djo": dobra})
	os.Symlink("/etc/passwd", filepath.Join(root, "boosters", "mat", "p.djo"))
	if _, err := runWith(t, root, "print 1\n"); err == nil || !strings.Contains(err.Error(), "link simbólico") {
		t.Fatalf("%v", err)
	}
}

func TestBoosterCreate(t *testing.T) {
	root := t.TempDir()
	var out, errw bytes.Buffer
	wd, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(wd)
	out.Reset()
	errw.Reset()
	if c := boosterCmd([]string{"create", "-perm", "File,Http", "saudar"}, &out, &errw); c != 0 {
		t.Fatalf("create: %d %s", c, errw.String())
	}
	m, err := readBoosterManifest(filepath.Join(root, "saudar"))
	if err != nil || m.Nome != "saudar" || len(m.Permissoes) != 2 {
		t.Fatalf("manifesto gerado: %+v %v", m, err)
	}
	if err := checkBoosterSource(filepath.Join(root, "saudar")); err != nil {
		t.Fatal(err)
	}
	// o que foi gerado instala e funciona
	proj := t.TempDir()
	os.Chdir(proj)
	if c := boosterCmd([]string{"add", filepath.Join(root, "saudar")}, &out, &errw); c != 0 {
		t.Fatalf("add: %s", errw.String())
	}
	if res, err := runWith(t, proj, "print saudar.ola:\"mundo\"\n"); err != nil || res != "Olá, mundo!\n" {
		t.Fatalf("%q %v", res, err)
	}
	os.Chdir(root)
	for _, args := range [][]string{{"create", "saudar"}, {"create", "Ruim"}, {"create", "-perm", "Foo", "x"}, {"create"}} {
		errw.Reset()
		if c := boosterCmd(args, &out, &errw); c == 0 {
			t.Errorf("%v deveria falhar", args)
		}
	}
}

func mkBoosterCore(t *testing.T, root string) {
	t.Helper()
	mkBooster(t, root, "ger", "", map[string]string{"main.djo": "f novo\n    params[nome[String]]\n    var p = Pessoa:nome\n    return p\n\nf saudar\n    params[nome[String]]\n    var p = Pessoa:nome\n    return p.ola:\n\nf interno\n    return ajuda:\n"})
	os.MkdirAll(filepath.Join(root, "boosters", "ger", "core"), 0o755)
	os.WriteFile(filepath.Join(root, "boosters", "ger", "core", "tipos.djo"), []byte("struct Pessoa\n    nome[String]\n    f ola\n        return \"Oi, \" + self.nome\n\nf ajuda\n    return \"ajuda do booster\"\n"), 0o644)
}

func TestBoosterCoreProprio(t *testing.T) {
	root := t.TempDir()
	mkBoosterCore(t, root)
	// o projeto tem uma struct e uma função com os mesmos nomes: não se misturam
	os.MkdirAll(filepath.Join(root, "core"), 0o755)
	os.WriteFile(filepath.Join(root, "core", "p.djo"), []byte("struct Pessoa\n    idade[Int]\n\nf ajuda\n    return \"ajuda do projeto\"\n"), 0o644)
	out, err := runWith(t, root, "print ger.saudar:\"Ana\"\nvar q = ger.novo:\"Bia\"\nprint q.nome\nprint q.ola:\nprint ger.interno:\nprint ajuda:\nvar p = Pessoa:30\nprint p.idade\n")
	if err != nil || out != "Oi, Ana\nBia\nOi, Bia\najuda do booster\najuda do projeto\n30\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestBoosterCoreEPrivado(t *testing.T) {
	root := t.TempDir()
	mkBoosterCore(t, root)
	for src, want := range map[string]string{
		"print ger.ajuda:\n":     "não tem a função 'ajuda'",
		"var x = Pessoa:\"a\"\n": "não existe",
	} {
		if _, err := runWith(t, root, src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v (queria %q)", src, err, want)
		}
	}
}

func TestBoosterCoreErros(t *testing.T) {
	root := t.TempDir()
	mkBoosterCore(t, root)
	os.WriteFile(filepath.Join(root, "boosters", "ger", "core", "solto.djo"), []byte("print 1\n"), 0o644)
	if _, err := runWith(t, root, "print 1\n"); err == nil || !strings.Contains(err.Error(), "não é permitido") {
		t.Fatalf("código solto no core do booster: %v", err)
	}
	root2 := t.TempDir()
	mkBoosterCore(t, root2)
	os.WriteFile(filepath.Join(root2, "boosters", "ger", "core", "dup.djo"), []byte("f novo\n    return 1\n"), 0o644)
	if _, err := runWith(t, root2, "print 1\n"); err == nil || !strings.Contains(err.Error(), "já existe neste booster") {
		t.Fatalf("função repetida entre API e core: %v", err)
	}
}

func TestBoosterCoreNoCLIEHash(t *testing.T) {
	src := t.TempDir()
	mkBoosterCore(t, src)
	srcDir := filepath.Join(src, "boosters", "ger")
	root := t.TempDir()
	wd, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(wd)
	var out, errw bytes.Buffer
	if c := boosterCmd([]string{"add", srcDir}, &out, &errw); c != 0 {
		t.Fatal(errw.String())
	}
	if _, err := os.Stat(filepath.Join(root, "boosters", "ger", "core", "tipos.djo")); err != nil {
		t.Fatal("o core/ do booster deveria ser copiado")
	}
	os.WriteFile(filepath.Join(root, "boosters", "ger", "core", "tipos.djo"), []byte("f ajuda\n    return 2\n"), 0o644)
	if p := verifyBoosters(root); len(p) != 1 {
		t.Fatalf("mexer no core/ do booster tem que quebrar o lock: %v", p)
	}
}

func TestBoosterExigeMainEAPISoNele(t *testing.T) {
	root := t.TempDir()
	mkBooster(t, root, "mat", "", map[string]string{"outro.djo": dobra})
	if _, err := runWith(t, root, "print 1\n"); err == nil || !strings.Contains(err.Error(), "falta o main.djo") {
		t.Fatalf("sem main.djo: %v", err)
	}
	root2 := t.TempDir()
	mkBooster(t, root2, "mat", "", map[string]string{"main.djo": dobra, "extra.djo": "f x\n    return 1\n"})
	if _, err := runWith(t, root2, "print 1\n"); err == nil || !strings.Contains(err.Error(), "só existe o main.djo") {
		t.Fatalf("outro .djo na raiz: %v", err)
	}
	// o mesmo vale ao instalar
	var out, errw bytes.Buffer
	wd, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(wd)
	if c := boosterCmd([]string{"add", filepath.Join(root2, "boosters", "mat")}, &out, &errw); c == 0 || !strings.Contains(errw.String(), "só existe o main.djo") {
		t.Fatalf("add: %d %s", c, errw.String())
	}
}
