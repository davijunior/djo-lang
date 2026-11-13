package main

import (
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// haltSignal encerra a página (usado pelo redirect).
type haltSignal struct{}

type builtin struct {
	args int // número exato de argumentos
	fn   func(in *Interp, env *Env, line int, a []any) any
}

// builtins são as funções que já vêm na djo; funções do usuário com o
// mesmo nome têm prioridade.
var builtins = map[string]builtin{}

func argStr(a []any, i, line int, fn string) string {
	s, ok := a[i].(string)
	if !ok {
		fail(line, "'%s' espera String no argumento %d, mas recebeu %s", fn, i+1, typeName(a[i]))
	}
	return s
}

func noCRLF(line int, fn, s string) {
	if strings.ContainsAny(s, "\r\n") {
		fail(line, "'%s' não aceita quebra de linha no valor", fn)
	}
}

func init() {
	for k, v := range map[string]builtin{
		"len": {1, func(in *Interp, env *Env, line int, a []any) any {
			switch x := a[0].(type) {
			case *Array:
				return int64(len(x.Items))
			case *Map:
				return int64(len(x.Keys))
			case string:
				return int64(len([]rune(x)))
			}
			fail(line, "'len' espera Arr, Map ou String, mas recebeu %s", typeName(a[0]))
			return nil
		}},
		"keys": {1, func(in *Interp, env *Env, line int, a []any) any {
			return &Array{Items: append([]any(nil), mustMap(a[0], "keys", line).Keys...)}
		}},
		"values": {1, func(in *Interp, env *Env, line int, a []any) any {
			return &Array{Items: append([]any(nil), mustMap(a[0], "values", line).Vals...)}
		}},
		"has": {2, func(in *Interp, env *Env, line int, a []any) any {
			_, ok := mustMap(a[0], "has", line).get(mapKey(a[1], line))
			return ok
		}},
		"del": {2, func(in *Interp, env *Env, line int, a []any) any {
			return mustMap(a[0], "del", line).del(mapKey(a[1], line))
		}},
		"range": {2, func(in *Interp, env *Env, line int, a []any) any {
			from, ok1 := a[0].(int64)
			to, ok2 := a[1].(int64)
			if !ok1 || !ok2 {
				fail(line, "'range' espera dois Int (início e fim, sem incluir o fim)")
			}
			if to-from > 1_000_000 {
				fail(line, "'range' grande demais (máximo 1 milhão)")
			}
			arr := &Array{}
			for i := from; i < to; i++ {
				arr.Items = append(arr.Items, i)
			}
			return arr
		}},
		// is:valor:"Tipo" -> o valor é desse tipo, descende dessa struct ou cumpre essa interface?
		"is": {2, func(in *Interp, env *Env, line int, a []any) any {
			t := argStr(a, 1, line, "is")
			if t == "Any" || typeName(a[0]) == t {
				return true
			}
			if inst, ok := a[0].(*Instance); ok {
				if isA(inst.Def, t) {
					return true
				}
				if id, found := in.interfaces[t]; found {
					return in.missing(inst.Def, id) == ""
				}
			}
			return false
		}},
		"push": {2, func(in *Interp, env *Env, line int, a []any) any {
			arr, ok := a[0].(*Array)
			if !ok {
				fail(line, "'push' espera uma lista (Arr), mas recebeu %s", typeName(a[0]))
			}
			arr.Items = append(arr.Items, a[1])
			return nil
		}},
		"str": {1, func(in *Interp, env *Env, line int, a []any) any {
			return format(a[0])
		}},
		"escape": {1, func(in *Interp, env *Env, line int, a []any) any {
			return html.EscapeString(format(a[0]))
		}},
		"include": {1, func(in *Interp, env *Env, line int, a []any) any {
			rel := argStr(a, 0, line, "include")
			full, err := safeJoin(in.root, rel)
			if err != nil {
				fail(line, "include '%s': %v", rel, err)
			}
			prog, err := loadProgram(full)
			if err != nil {
				if de, ok := err.(*djoError); ok {
					fail(line, "include '%s' (linha %d): %s", rel, de.Line, de.Msg)
				}
				fail(line, "include '%s': %v", rel, err)
			}
			in.depth++
			defer func() { in.depth-- }()
			if in.depth > maxDepth {
				fail(line, "include recursivo demais em '%s'", rel)
			}
			in.execBlock(prog, env)
			return nil
		}},
	} {
		builtins[k] = v
	}
}

// withinRoot confere que file (já com symlinks resolvidos) está dentro de realRoot.
func withinRoot(realRoot, file string) bool {
	return file == realRoot || strings.HasPrefix(file, realRoot+string(os.PathSeparator))
}

// safeJoin resolve rel dentro de root sem deixar escapar (nem por symlink).
func safeJoin(root, rel string) (string, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	full := filepath.Join(realRoot, filepath.FromSlash(path.Clean("/"+rel)))
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("arquivo não encontrado")
	}
	if !withinRoot(realRoot, real) {
		return "", fmt.Errorf("fora da pasta do site")
	}
	return real, nil
}

// safeJoinNew é como safeJoin, mas o arquivo pode ainda não existir (a pasta precisa existir).
func safeJoinNew(root, rel string) (string, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	full := filepath.Join(realRoot, filepath.FromSlash(path.Clean("/"+rel)))
	dir, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil {
		return "", fmt.Errorf("a pasta não existe")
	}
	target := filepath.Join(dir, filepath.Base(full))
	if real, err := filepath.EvalSymlinks(target); err == nil {
		target = real
	}
	if !withinRoot(realRoot, target) {
		return "", fmt.Errorf("fora da pasta do site")
	}
	return target, nil
}

func isTemplate(file string) bool { return strings.HasSuffix(file, ".djo.html") }

// loadProgram lê e compila um arquivo .djo (código) ou .djo.html (template).
func parseFile(file string) ([]Stmt, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if isTemplate(file) {
		return ParseTemplate(string(src))
	}
	return Parse(string(src))
}
