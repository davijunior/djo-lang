package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func openApp(t *testing.T, path string) (*zip.Reader, *os.File) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	off, size, ok := payloadOf(f)
	if !ok {
		t.Fatal("o executável gerado não tem o site colado")
	}
	zr, err := zip.NewReader(io.NewSectionReader(f, off, size), size)
	if err != nil {
		t.Fatal(err)
	}
	return zr, f
}

func TestBuildEUnpack(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "meusite")
	writeTree(t, proj, map[string]string{
		"index.djo.html": "<h1>{{ 1 + 1 }}</h1>\n",
		"core/post.djo":  "struct Post{\n t[String]\n}\n",
		"estatico/a.css": "body{}\n",
		"dados.db":       "banco",
		".segredo":       "x",
		".git/config":    "x",
		"dist/velho":     "x",
		"rascunho/a.djo": "print 1\n",
		".djoignore":     "rascunho\n*.tmp\n",
		"lixo.tmp":       "x",
	})
	out := filepath.Join(t.TempDir(), "app")
	if code := buildCmd([]string{"-o", out, proj}); code != 0 {
		t.Fatalf("build falhou: %d", code)
	}
	zr, f := openApp(t, out)
	defer f.Close()
	var names []string
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	got := strings.Join(names, ",")
	if got != "core/post.djo,estatico/a.css,index.djo.html,.djo-app.json" {
		t.Fatalf("arquivos no app: %s", got)
	}
	info, ok := readManifest(zr)
	if !ok || info.Mode != "serve" || info.Name != "meusite" || info.Files != 3 || len(info.Version) != 12 {
		t.Fatalf("manifesto: %+v %v", info, ok)
	}

	// mesmo conteúdo, mesma versão
	out2 := filepath.Join(t.TempDir(), "app2")
	buildCmd([]string{"-o", out2, proj})
	zr2, f2 := openApp(t, out2)
	defer f2.Close()
	if info2, _ := readManifest(zr2); info2.Version != info.Version {
		t.Fatalf("versões diferentes para o mesmo conteúdo: %s %s", info.Version, info2.Version)
	}

	// desempacotar
	data := filepath.Join(t.TempDir(), "app.d")
	if err := unpack(zr, data, info.Version); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "core", "post.djo")); !strings.Contains(string(b), "struct Post") {
		t.Fatalf("core não foi desempacotado: %q", b)
	}
	if _, err := os.Stat(filepath.Join(data, ".djo-app.json")); err == nil {
		t.Fatal("o manifesto não deve ir para a pasta de dados")
	}
	// mesma versão: não mexe em nada
	os.WriteFile(filepath.Join(data, "index.djo.html"), []byte("editado"), 0o644)
	os.WriteFile(filepath.Join(data, "meu.db"), []byte("dados do usuário"), 0o644)
	unpack(zr, data, info.Version)
	if b, _ := os.ReadFile(filepath.Join(data, "index.djo.html")); string(b) != "editado" {
		t.Fatal("reabrir a mesma versão não deveria sobrescrever")
	}
	// versão nova: troca os arquivos do app, mantém os dados
	unpack(zr, data, "nova")
	if b, _ := os.ReadFile(filepath.Join(data, "index.djo.html")); !strings.Contains(string(b), "{{ 1 + 1 }}") {
		t.Fatal("versão nova deveria restaurar o código do app")
	}
	if b, _ := os.ReadFile(filepath.Join(data, "meu.db")); string(b) != "dados do usuário" {
		t.Fatal("os dados do usuário foram apagados")
	}
	es, _ := os.ReadDir(data)
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".djo-new") {
			t.Fatalf("sobrou temporário: %s", e.Name())
		}
	}
}

func TestBuildModoRunEErros(t *testing.T) {
	proj := t.TempDir()
	writeTree(t, proj, map[string]string{"main.djo": "print 1\n"})
	out := filepath.Join(t.TempDir(), "iot")
	if buildCmd([]string{"-o", out, proj}) != 0 {
		t.Fatal("build falhou")
	}
	zr, f := openApp(t, out)
	defer f.Close()
	if info, _ := readManifest(zr); info.Mode != "run" {
		t.Fatalf("main.djo deveria dar modo run: %+v", info)
	}

	// construir a partir de um app: o site antigo é tirado
	out2 := filepath.Join(t.TempDir(), "outro")
	proj2 := t.TempDir()
	writeTree(t, proj2, map[string]string{"index.djo": "print 2\n"})
	if buildCmd([]string{"-o", out2, "-runtime", out, proj2}) != 0 {
		t.Fatal("build com runtime falhou")
	}
	zr2, f2 := openApp(t, out2)
	defer f2.Close()
	if info, _ := readManifest(zr2); info.Mode != "serve" || info.Files != 1 {
		t.Fatalf("o app novo deveria ser só o site novo: %+v", info)
	}

	for name, files := range map[string]map[string]string{
		"sintaxe":     {"index.djo": "if\n"},
		"template":    {"index.djo.html": "{{ 1 + }}"},
		"core solto":  {"index.djo": "print 1\n", "core/x.djo": "print 1\n"},
		"sem paginas": {"leia.txt": "oi"},
		"pasta vazia": {},
	} {
		p := t.TempDir()
		writeTree(t, p, files)
		o := filepath.Join(t.TempDir(), "x")
		if buildCmd([]string{"-o", o, p}) == 0 {
			t.Errorf("%s: deveria falhar", name)
		}
		if _, err := os.Stat(o); err == nil {
			t.Errorf("%s: não deveria gerar o arquivo", name)
		}
	}
}

func TestUnpackRecusaCaminhoSuspeito(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "evil.zip")
	zf, _ := os.Create(tmp)
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("../../fora.txt")
	w.Write([]byte("x"))
	zw.Close()
	zf.Close()
	r, _ := zip.OpenReader(tmp)
	defer r.Close()
	dest := filepath.Join(dir, "d")
	if err := unpack(&r.Reader, dest, "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fora.txt")); err == nil {
		t.Fatal("escapou da pasta")
	}
	if _, err := os.Stat(filepath.Join(dest, "fora.txt")); err != nil {
		t.Fatal("o caminho deveria ser contido dentro da pasta de dados")
	}
}
