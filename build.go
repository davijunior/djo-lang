package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// djo build [-o app] [-runtime caminho-do-djo] [pasta]
//
// Empacota o projeto num executável único: o binário do djo com o site dentro.

const buildMaxFiles = 20000

// buildExcluded diz se um arquivo não entra no app: ocultos, bancos de dados (são dados, não
// código), a pasta dist/ e o que o .djoignore listar.
func buildExcluded(rel string, d fs.DirEntry, ignore []string) bool {
	base := path.Base(rel)
	if strings.HasPrefix(base, ".") && rel != "." {
		return true
	}
	if d.IsDir() {
		if rel == "dist" || rel == "spec" || rel == "_saida" || base == "node_modules" {
			return true
		}
	} else {
		lower := strings.ToLower(base)
		for _, suf := range []string{".db", ".sqlite", ".sqlite3", ".db-wal", ".db-shm", ".djo-new"} {
			if strings.HasSuffix(lower, suf) {
				return true
			}
		}
	}
	for _, pat := range ignore {
		pat = strings.TrimSuffix(pat, "/")
		if m, _ := path.Match(pat, rel); m {
			return true
		}
		if m, _ := path.Match(pat, base); m {
			return true
		}
	}
	return false
}

func readIgnore(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, ".djoignore"))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// checkProject lê todo o código e acusa erros de sintaxe antes de empacotar.
func checkProject(dir string, files []string) []string {
	var problems []string
	for _, rel := range files {
		var err error
		switch {
		case strings.HasSuffix(rel, ".djo.html"):
			src, rerr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if rerr != nil {
				err = rerr
			} else {
				_, err = ParseTemplate(string(src))
			}
		case strings.HasSuffix(rel, ".djo"):
			src, rerr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if rerr != nil {
				err = rerr
			} else {
				_, err = Parse(string(src))
			}
		}
		if err != nil {
			problems = append(problems, rel+": "+err.Error())
		}
	}
	if err := NewInterp(io.Discard).loadCore(dir); err != nil {
		problems = append(problems, err.Error())
	}
	problems = append(problems, verifyBoosters(dir)...)
	if err := checkRoutes(dir); err != nil {
		problems = append(problems, err.Error())
	}
	if err := checkAsk(dir); err != nil {
		problems = append(problems, err.Error())
	}
	if err := checkAgenda(dir); err != nil {
		problems = append(problems, err.Error())
	}
	return problems
}

func buildCmd(args []string) int {
	fset := flag.NewFlagSet("build", flag.ContinueOnError)
	out := fset.String("o", "", "nome do executável (padrão: o nome da pasta)")
	rt := fset.String("runtime", "", "binário do djo que vira a base (padrão: este; use outro para compilar para outra máquina)")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	dir := "."
	if fset.NArg() > 0 {
		dir = fset.Arg(0)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "djo build: '%s' não é uma pasta\n", dir)
		return 1
	}

	// 1. base: o binário do djo (sem app colado)
	runtimePath := *rt
	if runtimePath == "" {
		if runtimePath, err = os.Executable(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	base, err := os.ReadFile(runtimePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "djo build: não consegui ler o runtime: %v\n", err)
		return 1
	}
	if f, err := os.Open(runtimePath); err == nil {
		if off, _, ok := payloadOf(f); ok {
			base = base[:off] // o runtime já era um app: tira o site velho
		}
		f.Close()
	}
	if len(base) < 1000 {
		fmt.Fprintln(os.Stderr, "djo build: o runtime não parece um binário do djo")
		return 1
	}
	winTarget := bytes.HasPrefix(base, []byte("MZ")) || strings.HasSuffix(strings.ToLower(runtimePath), ".exe")

	// 2. arquivos do projeto
	ignore := readIgnore(abs)
	outPath := *out
	if outPath == "" {
		outPath = filepath.Join(abs, "dist", filepath.Base(abs))
		if winTarget {
			outPath += ".exe"
		}
	}
	if outAbs, err := filepath.Abs(outPath); err == nil {
		outPath = outAbs
	}
	var files []string
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(abs, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if p == outPath || buildExcluded(rel, d, ignore) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // links ficam de fora: o app não deve apontar para fora do projeto
		}
		if d.Type().IsRegular() {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "djo build: %v\n", err)
		return 1
	}
	sort.Strings(files)
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "djo build: a pasta não tem arquivos para empacotar")
		return 1
	}
	if len(files) > buildMaxFiles {
		fmt.Fprintf(os.Stderr, "djo build: arquivos demais (%d); use .djoignore\n", len(files))
		return 1
	}

	// 3. confere o código
	if problems := checkProject(abs, files); len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "djo build: %d erro(s) no código, nada foi gerado:\n", len(problems))
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  "+p)
		}
		return 1
	}

	// 4. modo: main.djo roda como programa; senão o site é servido
	mode := "serve"
	for _, f := range files {
		if f == "main.djo" {
			mode = "run"
		}
	}
	if mode == "serve" {
		hasPage := false
		for _, f := range files {
			if strings.HasSuffix(f, ".djo") || strings.HasSuffix(f, ".djo.html") || strings.HasSuffix(f, ".html") {
				hasPage = true
			}
		}
		if !hasPage {
			fmt.Fprintln(os.Stderr, "djo build: não achei nem main.djo nem páginas (.djo, .djo.html, .html)")
			return 1
		}
	}

	// 5. zip do site
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(abs, filepath.FromSlash(rel)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "djo build: %v\n", err)
			return 1
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: rel, Method: zip.Deflate})
		if err == nil {
			_, err = w.Write(data)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "djo build: %v\n", err)
			return 1
		}
	}
	// a versão depende do conteúdo dos arquivos (não de datas), então builds iguais dão a mesma versão
	version := appVersion(append(append([]byte(mode), '\n'), zipContentKey(abs, files)...))
	manifest, _ := json.Marshal(appInfo{Name: filepath.Base(abs), Mode: mode, Files: len(files), Version: version})
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: appManifest, Method: zip.Store})
	w.Write(manifest)
	if err := zw.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "djo build: %v\n", err)
		return 1
	}

	// 6. junta: binário + zip + tamanho + marca
	var tail [16]byte
	binary.LittleEndian.PutUint64(tail[:8], uint64(zbuf.Len()))
	copy(tail[8:], appMagic)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "djo build: %v\n", err)
		return 1
	}
	tmp := outPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err == nil {
		for _, part := range [][]byte{base, zbuf.Bytes(), tail[:]} {
			if _, err = f.Write(part); err != nil {
				break
			}
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err == nil {
		err = os.Rename(tmp, outPath)
	}
	if err != nil {
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "djo build: %v\n", err)
		return 1
	}
	rel, _ := filepath.Rel(mustGetwd(), outPath)
	if strings.HasPrefix(rel, "..") {
		rel = outPath
	}
	fmt.Printf("app gerado: %s (%.1f MB, %d arquivos, modo %s)\n", rel, float64(len(base)+zbuf.Len()+16)/1048576, len(files), mode)
	if mode == "serve" {
		fmt.Printf("rode com: ./%s -addr :8080\n", filepath.Base(outPath))
	} else {
		fmt.Printf("rode com: ./%s\n", filepath.Base(outPath))
	}
	return 0
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// zipContentKey junta nomes e conteúdos dos arquivos, em ordem, para calcular a versão.
func zipContentKey(abs string, files []string) []byte {
	var b bytes.Buffer
	for _, rel := range files {
		data, _ := os.ReadFile(filepath.Join(abs, filepath.FromSlash(rel)))
		fmt.Fprintf(&b, "%s\x00%d\x00", rel, len(data))
		b.Write(data)
	}
	return b.Bytes()
}
