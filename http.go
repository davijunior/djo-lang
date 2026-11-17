//go:build !noweb

package main

import (
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Http: o lado web do core da linguagem (só funciona em 'djo serve').
// Time: datas e horas. Ambos são namespaces nativos, como o Json.

// viaBuiltin faz Http.nome repassar para o builtin de mesmo nome.
func viaBuiltin(name string) builtin {
	return builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		b := builtins[name]
		if len(a) != b.args {
			fail(line, "Http.%s espera %d argumento(s), recebeu %d", name, b.args, len(a))
		}
		return b.fn(in, env, line, a)
	}}
}

func (in *Interp) readBody(line int) []byte {
	in.needWeb(line, "Http.body")
	if !in.bodyRead {
		in.bodyRead = true
		in.body, _ = io.ReadAll(in.req.Body) // já limitado pelo servidor
	}
	return in.body
}

func init() {
	for k, v := range map[string]builtin{
		"method":    viaBuiltin("method"),
		"path":      viaBuiltin("path"),
		"query":     viaBuiltin("query"),
		"param":     viaBuiltin("param"),
		"form":      viaBuiltin("form"),
		"header":    viaBuiltin("header"),
		"setheader": viaBuiltin("setheader"),
		"bearer":    viaBuiltin("bearer"),
		"status":    viaBuiltin("status"),
		"redirect":  viaBuiltin("redirect"),
		"cookie":    viaBuiltin("cookie"),
		"setcookie": viaBuiltin("setcookie"),
		// Http.body: o corpo da requisição como texto (vazio em formulários comuns)
		"body": {0, func(in *Interp, env *Env, line int, a []any) any {
			return string(in.readBody(line))
		}},
		// Http.json: o corpo da requisição lido como JSON (nil se inválido)
		"json": {-1, func(in *Interp, env *Env, line int, a []any) any {
			if len(a) == 0 {
				v, err := jsonDecode(string(in.readBody(line)))
				if err != nil {
					return nil
				}
				return v
			}
			// Http.json:valor  ou  Http.json:valor:status -> responde JSON
			in.needWeb(line, "Http.json")
			if len(a) > 2 {
				fail(line, "Http.json aceita (valor) ou (valor, status)")
			}
			if len(a) == 2 {
				builtins["status"].fn(in, env, line, a[1:])
			}
			in.resp.header.Set("Content-Type", "application/json; charset=utf-8")
			fmt.Fprint(in.out, jsonEncode(a[0], "", line))
			return nil
		}},
		// Http.file:"campo" -> {name, size, type} do arquivo enviado, ou nil
		"file": {1, func(in *Interp, env *Env, line int, a []any) any {
			fh := in.uploaded(argStr(a, 0, line, "Http.file"), line)
			if fh == nil {
				return nil
			}
			m := newMap()
			m.set("name", filepath.Base(fh.Filename))
			m.set("size", fh.Size)
			m.set("type", fh.Header.Get("Content-Type"))
			return m
		}},
		// Http.upload:"campo":"pasta" -> grava o arquivo com nome novo e devolve o caminho (ou nil)
		"upload": {2, func(in *Interp, env *Env, line int, a []any) any {
			fh := in.uploaded(argStr(a, 0, line, "Http.upload"), line)
			if fh == nil {
				return nil
			}
			return in.saveUpload(fh, argStr(a, 1, line, "Http.upload"), line)
		}},
		// Http.args: todas as partes variáveis da rota, num mapa
		"args": {0, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "Http.args")
			m := newMap()
			keys := make([]string, 0, len(in.params))
			for k := range in.params {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				m.set(k, in.params[k])
			}
			return m
		}},
		// Http.ip: endereço de quem fez a requisição
		"ip": {0, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "Http.ip")
			return clientIP(in.req)
		}},
	} {
		nativeNS("Http")[k] = v
	}
}

// Extensões aceitas em uploads. Nada que o servidor execute ou que o navegador
// trate como página (html, svg, js, djo...) entra na lista.
var uploadExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true, ".ico": true,
	".pdf": true, ".txt": true, ".csv": true, ".json": true, ".md": true,
	".zip": true, ".gz": true, ".mp3": true, ".wav": true, ".ogg": true, ".mp4": true, ".webm": true,
}

func (in *Interp) uploaded(field string, line int) *multipart.FileHeader {
	in.needWeb(line, "Http.file")
	if in.req.MultipartForm == nil {
		return nil
	}
	if fhs := in.req.MultipartForm.File[field]; len(fhs) > 0 {
		return fhs[0]
	}
	return nil
}

func (in *Interp) saveUpload(fh *multipart.FileHeader, dir string, line int) string {
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !uploadExts[ext] {
		fail(line, "Http.upload: arquivos '%s' não são aceitos", ext)
	}
	clean := filepath.ToSlash(filepath.Clean(dir))
	if dir == "" || strings.HasPrefix(dir, "/") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(dir, "..") {
		fail(line, "Http.upload: a pasta precisa ser um caminho dentro do site, como \"uploads\"")
	}
	abs, err := filepath.Abs(in.root)
	if err != nil {
		fail(line, "Http.upload: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(abs, filepath.FromSlash(clean)), 0o755); err != nil {
		fail(line, "Http.upload: não consegui criar a pasta '%s'", clean)
	}
	destDir, err := safeJoin(in.root, clean) // confere symlinks
	if err != nil {
		fail(line, "Http.upload: pasta '%s': %v", clean, err)
	}
	src, err := fh.Open()
	if err != nil {
		fail(line, "Http.upload: %v", err)
	}
	defer src.Close()
	name := randHex(8) + ext
	dst, err := os.OpenFile(filepath.Join(destDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		fail(line, "Http.upload: %v", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(dst.Name())
		fail(line, "Http.upload: %v", err)
	}
	dst.Close()
	return clean + "/" + name
}
