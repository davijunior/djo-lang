package main

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// File: ler e gravar arquivos dentro da pasta do projeto (a do script, ou o site).
//
//	File.read:"dados.txt"            texto, ou nil se não existe
//	File.write:"dados.txt":texto     grava (cria pastas que faltarem); true
//	File.append:"log.txt":linha      acrescenta no fim; true
//	File.exists:"x" File.delete:"x" File.list:"pasta" File.info:"x"  File.mkdir:"pasta"
//
// Nenhum caminho sai da pasta do projeto (".." e links simbólicos são barrados).

const (
	fileMaxRead   = 10 << 20
	fileMaxWrite  = 10 << 20 // por chamada
	fileMaxServed = 10 << 20 // total gravado por página, em 'djo serve'
	fileMaxList   = 10000
)

// Extensões que uma página servida não pode gravar: código da djo e tudo que o
// navegador trata como página ou script (para um upload ou texto de visitante
// nunca virar XSS ou código executado).
var fileDeniedWrite = map[string]bool{
	".djo": true, ".html": true, ".htm": true, ".xhtml": true, ".svg": true,
	".js": true, ".mjs": true, ".xml": true,
}

// filePath resolve rel dentro da pasta do projeto. create=true cria as pastas que faltam.
func (in *Interp) filePath(fn, rel string, line int, create bool) string {
	if strings.ContainsRune(rel, 0) || path.IsAbs(rel) || strings.HasPrefix(rel, "\\") {
		fail(line, "%s: o caminho precisa ser relativo à pasta do projeto", fn)
	}
	for _, seg := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			fail(line, "%s: o caminho não pode usar '..'", fn)
		}
	}
	root := in.root
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		fail(line, "%s: %v", fn, err)
	}
	clean := path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	full := filepath.Join(abs, filepath.FromSlash(clean))

	// o ancestral mais fundo que já existe precisa estar dentro do projeto
	anc := full
	for {
		if _, err := os.Lstat(anc); err == nil {
			break
		}
		next := filepath.Dir(anc)
		if next == anc {
			break
		}
		anc = next
	}
	real, err := filepath.EvalSymlinks(anc)
	if err != nil || !withinRoot(abs, real) {
		fail(line, "%s: '%s' fica fora da pasta do projeto", fn, rel)
	}
	if rest, err := filepath.Rel(anc, full); err == nil && rest != "." {
		full = filepath.Join(real, rest)
	} else {
		full = real
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			fail(line, "%s: não consegui criar a pasta: %v", fn, err)
		}
	}
	return full
}

// checkWrite barra gravações perigosas em páginas servidas.
func (in *Interp) checkWrite(fn, rel string, n int, line int) {
	if !in.served {
		return
	}
	lower := strings.ToLower(rel)
	if fileDeniedWrite[filepath.Ext(lower)] {
		fail(line, "%s: páginas servidas não gravam arquivos '%s' (código ou página web)", fn, filepath.Ext(lower))
	}
	first := strings.SplitN(strings.TrimLeft(path.Clean("/"+strings.ReplaceAll(lower, "\\", "/")), "/"), "/", 2)[0]
	if first == "core" {
		fail(line, "%s: páginas servidas não gravam na pasta core/", fn)
	}
	in.wrote += n
	if in.wrote > fileMaxServed {
		fail(line, "%s: esta página já gravou arquivos demais (máximo %d MB)", fn, fileMaxServed>>20)
	}
}

func fileText(a []any, i int, fn string, line int) string {
	s, ok := a[i].(string)
	if !ok {
		fail(line, "%s espera String no argumento %d, mas recebeu %s (use Json.encode: para dados)", fn, i+1, typeName(a[i]))
	}
	if len(s) > fileMaxWrite {
		fail(line, "%s: texto grande demais (máximo %d MB por chamada)", fn, fileMaxWrite>>20)
	}
	return s
}

func fileFail(fn string, line int, err error) {
	if pe, ok := err.(*os.PathError); ok {
		err = pe.Err // sem repetir o caminho absoluto
	}
	fail(line, "%s: %v", fn, err)
}

func init() {
	ns := nativeNS("File")
	ns["read"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		p := in.filePath("File.read", argStr(a, 0, line, "File.read"), line, false)
		fi, err := os.Stat(p)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			fileFail("File.read", line, err)
		}
		if fi.IsDir() {
			fail(line, "File.read: '%s' é uma pasta (use File.list:)", a[0])
		}
		if fi.Size() > fileMaxRead {
			fail(line, "File.read: arquivo grande demais (máximo %d MB)", fileMaxRead>>20)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			fileFail("File.read", line, err)
		}
		return string(data)
	}}
	ns["write"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		rel, text := argStr(a, 0, line, "File.write"), fileText(a, 1, "File.write", line)
		in.checkWrite("File.write", rel, len(text), line)
		p := in.filePath("File.write", rel, line, true)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			fail(line, "File.write: '%s' é uma pasta", rel)
		}
		// grava num temporário e renomeia: quem lê nunca vê o arquivo pela metade
		tmp := filepath.Join(filepath.Dir(p), ".djo-tmp-"+randHex(6))
		if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
			fileFail("File.write", line, err)
		}
		if err := os.Rename(tmp, p); err != nil {
			os.Remove(tmp)
			fileFail("File.write", line, err)
		}
		return true
	}}
	ns["append"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		rel, text := argStr(a, 0, line, "File.append"), fileText(a, 1, "File.append", line)
		in.checkWrite("File.append", rel, len(text), line)
		p := in.filePath("File.append", rel, line, true)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			fileFail("File.append", line, err)
		}
		defer f.Close()
		if _, err := f.WriteString(text); err != nil {
			fileFail("File.append", line, err)
		}
		return true
	}}
	ns["exists"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		_, err := os.Stat(in.filePath("File.exists", argStr(a, 0, line, "File.exists"), line, false))
		return err == nil
	}}
	ns["delete"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		rel := argStr(a, 0, line, "File.delete")
		in.checkWrite("File.delete", rel, 0, line)
		err := os.Remove(in.filePath("File.delete", rel, line, false))
		if os.IsNotExist(err) {
			return false
		}
		if err != nil {
			fileFail("File.delete", line, err)
		}
		return true
	}}
	ns["mkdir"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		rel := argStr(a, 0, line, "File.mkdir")
		in.checkWrite("File.mkdir", rel, 0, line)
		if err := os.MkdirAll(in.filePath("File.mkdir", rel, line, false), 0o755); err != nil {
			fileFail("File.mkdir", line, err)
		}
		return true
	}}
	ns["list"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		entries, err := os.ReadDir(in.filePath("File.list", argStr(a, 0, line, "File.list"), line, false))
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			fileFail("File.list", line, err)
		}
		if len(entries) > fileMaxList {
			fail(line, "File.list: mais de %d itens", fileMaxList)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, ".djo-tmp-") {
				continue
			}
			if e.IsDir() {
				n += "/"
			}
			names = append(names, n)
		}
		sort.Strings(names)
		arr := &Array{Items: make([]any, len(names))}
		for i, n := range names {
			arr.Items[i] = n
		}
		return arr
	}}
	ns["info"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		fi, err := os.Stat(in.filePath("File.info", argStr(a, 0, line, "File.info"), line, false))
		if err != nil {
			return nil
		}
		m := newMap()
		m.set("size", fi.Size())
		m.set("modified", fi.ModTime().Unix())
		m.set("dir", fi.IsDir())
		return m
	}}
}
