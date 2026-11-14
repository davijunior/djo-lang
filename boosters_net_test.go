//go:build !nonet

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeArchive(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		body := "f x\n    return 1\n"
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		h.Mode = 0o644
		tw.WriteHeader(&h)
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestUnpackRecusaArquivoPerigoso(t *testing.T) {
	for nome, h := range map[string]tar.Header{
		"sobe pasta":  {Name: "../fora.djo"},
		"absoluto":    {Name: "/etc/x.djo"},
		"dentro e ..": {Name: "core/../../x.djo"},
		"link":        {Name: "l.djo", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"barra":       {Name: "a\\b.djo"},
	} {
		dest := t.TempDir()
		if err := unpackBooster(fakeArchive(t, h), dest); err == nil {
			t.Errorf("%s: deveria recusar", nome)
		}
	}
	if err := unpackBooster([]byte("nao e gzip"), t.TempDir()); err == nil {
		t.Error("lixo deveria ser recusado")
	}
	if err := unpackBooster(fakeArchive(t), t.TempDir()); err == nil {
		t.Error("arquivo vazio deveria ser recusado")
	}
}

func TestPackEhIgualSempre(t *testing.T) {
	src := t.TempDir()
	mkBoosterCore(t, src)
	dir := filepath.Join(src, "boosters", "ger")
	a, _, err := packBooster(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := packBooster(dir)
	if sumHex(a) != sumHex(b) {
		t.Fatal("o mesmo booster deveria gerar sempre o mesmo arquivo")
	}
	out := t.TempDir()
	if err := unpackBooster(a, out); err != nil {
		t.Fatal(err)
	}
	h1, _ := hashBooster(dir)
	h2, _ := hashBooster(out)
	if h1 != h2 {
		t.Fatal("empacotar e extrair deveria devolver o mesmo booster")
	}
}

func mkRegistro(t *testing.T) (string, *httptest.Server) {
	t.Helper()
	reg := t.TempDir()
	for _, v := range []string{"1.2.0", "1.10.0", "1.9.0"} {
		src := t.TempDir()
		mkBoosterCore(t, src)
		m := `{"nome":"ger","versao":"` + v + `","descricao":"gera pessoas","permissoes":[]}`
		os.WriteFile(filepath.Join(src, "boosters", "ger", "booster.json"), []byte(m), 0o644)
		var out, errw bytes.Buffer
		if c := boosterPack([]string{"-registro", reg, filepath.Join(src, "boosters", "ger")}, &out, &errw); c != 0 {
			t.Fatalf("pack %s: %s", v, errw.String())
		}
	}
	ts := httptest.NewServer(http.FileServer(http.Dir(reg)))
	t.Cleanup(ts.Close)
	return reg, ts
}

func TestRegistroIndice(t *testing.T) {
	reg, _ := mkRegistro(t)
	data, _ := os.ReadFile(filepath.Join(reg, "indice.json"))
	var ix regIndex
	if err := json.Unmarshal(data, &ix); err != nil {
		t.Fatal(err)
	}
	b := ix.find("ger")
	if ix.Formato != indexFormat || b == nil || b.Ultima != "1.10.0" || len(b.Versoes) != 3 || b.Versoes[0].Versao != "1.2.0" {
		t.Fatalf("índice: %s", data)
	}
	if b.Descricao != "gera pessoas" || b.Permissoes == nil {
		t.Fatalf("descrição/permissões: %+v", b)
	}
	// uma versão publicada não muda
	src := t.TempDir()
	mkBoosterCore(t, src)
	dir := filepath.Join(src, "boosters", "ger")
	os.WriteFile(filepath.Join(dir, "booster.json"), []byte(`{"nome":"ger","versao":"1.2.0","descricao":"x"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "core", "novo.djo"), []byte("f outra\n    return 1\n"), 0o644)
	var out, errw bytes.Buffer
	if c := boosterPack([]string{"-registro", reg, dir}, &out, &errw); c == 0 || !strings.Contains(errw.String(), "já foi publicado") {
		t.Fatalf("republicar com outro conteúdo: %d %s", c, errw.String())
	}
}

func TestAddPeloNomeEPelaVersao(t *testing.T) {
	_, ts := mkRegistro(t)
	for spec, want := range map[string]string{"ger": "1.10.0", "ger@1.2.0": "1.2.0"} {
		proj := t.TempDir()
		var out, errw bytes.Buffer
		if c := boosterAddCmd(proj, []string{spec, "-registro", ts.URL}, &out, &errw); c != 0 {
			t.Fatalf("%s: %s", spec, errw.String())
		}
		lock, _ := readLock(proj)
		if lock["ger"].Versao != want || !strings.HasPrefix(lock["ger"].Origem, ts.URL) {
			t.Fatalf("%s: lock %+v", spec, lock)
		}
		if res, err := runWith(t, proj, "print ger.saudar:\"Ana\"\n"); err != nil || res != "Oi, Ana\n" {
			t.Fatalf("%q %v", res, err)
		}
		if p := verifyBoosters(proj); len(p) != 0 {
			t.Fatal(p)
		}
	}
}

func TestAddErrosDoRegistro(t *testing.T) {
	reg, ts := mkRegistro(t)
	var out, errw bytes.Buffer
	for spec, want := range map[string]string{"nada": "não tem o booster", "ger@9.9": "não tem a versão"} {
		errw.Reset()
		if c := boosterAddCmd(t.TempDir(), []string{spec, "-registro", ts.URL}, &out, &errw); c == 0 || !strings.Contains(errw.String(), want) {
			t.Errorf("%s: %d %s", spec, c, errw.String())
		}
	}
	// arquivo adulterado no servidor: o SHA256 do índice não bate e nada é instalado
	p := filepath.Join(reg, "ger", "1.2.0", "ger-1.2.0.tar.gz")
	os.WriteFile(p, fakeArchive(t, tar.Header{Name: "booster.json"}), 0o644)
	proj := t.TempDir()
	errw.Reset()
	if c := boosterAddCmd(proj, []string{"ger@1.2.0", "-registro", ts.URL}, &out, &errw); c == 0 || !strings.Contains(errw.String(), "SHA256") {
		t.Fatalf("adulterado: %d %s", c, errw.String())
	}
	if _, err := os.Stat(filepath.Join(proj, "boosters")); err == nil {
		t.Fatal("nada deveria ter sido instalado")
	}
	// registro fora do ar / sem https
	errw.Reset()
	if c := boosterAddCmd(proj, []string{"ger", "-registro", "http://exemplo.com/registro"}, &out, &errw); c == 0 || !strings.Contains(errw.String(), "https") {
		t.Fatalf("http: %d %s", c, errw.String())
	}
}

func TestAddPorEnderecoExigeSHA(t *testing.T) {
	reg, ts := mkRegistro(t)
	data, _ := os.ReadFile(filepath.Join(reg, "indice.json"))
	var ix regIndex
	json.Unmarshal(data, &ix)
	v := ix.find("ger").version("1.2.0")
	u := ts.URL + "/" + v.URL
	var out, errw bytes.Buffer
	if c := boosterAddCmd(t.TempDir(), []string{u}, &out, &errw); c != 2 || !strings.Contains(errw.String(), "exige o SHA256") {
		t.Fatalf("sem sha: %d %s", c, errw.String())
	}
	errw.Reset()
	if c := boosterAddCmd(t.TempDir(), []string{u, "-sha256", strings.Repeat("0", 64)}, &out, &errw); c == 0 || !strings.Contains(errw.String(), "não bate") {
		t.Fatalf("sha errado: %d %s", c, errw.String())
	}
	proj := t.TempDir()
	if c := boosterAddCmd(proj, []string{"-sha256", strings.ToUpper(v.SHA256), u}, &out, &errw); c != 0 {
		t.Fatalf("sha certo: %s", errw.String())
	}
	if lock, _ := readLock(proj); lock["ger"].Origem != u {
		t.Fatalf("origem: %+v", lock)
	}
}

func TestBuscaNoRegistro(t *testing.T) {
	_, ts := mkRegistro(t)
	var out, errw bytes.Buffer
	if c := boosterSearch([]string{"-registro", ts.URL, "pessoas"}, &out, &errw); c != 0 || !strings.Contains(out.String(), "ger 1.10.0") {
		t.Fatalf("%d %s %s", c, out.String(), errw.String())
	}
	out.Reset()
	boosterSearch([]string{"-registro", ts.URL, "zzz"}, &out, &errw)
	if !strings.Contains(out.String(), "nenhum booster") {
		t.Fatal(out.String())
	}
}

func TestCmpVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"1.10.0", "1.9.3", 1}, {"1.0", "1.0.0", 0}, {"2.0.0", "10.0.0", -1}, {"1.0.0", "1.0.0", 0}, {"1.0.1", "1.0.0", 1}} {
		if got := cmpVersion(c.a, c.b); got != c.want {
			t.Errorf("cmp(%s,%s)=%d queria %d", c.a, c.b, got, c.want)
		}
	}
}
