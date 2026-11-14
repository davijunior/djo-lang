package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A pasta core/ do projeto guarda as structs e funções do sistema (o "core do
// sistema", diferente do core da linguagem). Tudo que está nela vale em todas
// as páginas, sem require nem include. Ela só declara: código solto é erro.

// loadCore registra as declarações de root/core/**/*.djo no interpretador.
func (in *Interp) loadCore(root string) error {
	if err := in.loadBoosters(root); err != nil {
		return err
	}
	dir := filepath.Join(root, "core")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil // projeto sem core
	}
	var files []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && strings.HasSuffix(p, ".djo") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	for _, f := range files {
		prog, err := loadProgram(f)
		if err != nil {
			return coreError(root, f, err)
		}
		for _, s := range prog {
			switch d := s.(type) {
			case *FuncDecl:
				in.funcs[d.Name] = d
			case *StructDecl:
				in.declare(d.Name, d.Line, d, nil)
			case *InterfaceDecl:
				in.declare(d.Name, d.Line, nil, d)
			default:
				return coreError(root, f, &djoError{lineOf(s), "o core só declara funções, structs e interfaces (f, struct e interface); código solto não é permitido"})
			}
		}
	}
	return nil
}

func coreError(root, file string, err error) error {
	rel, _ := filepath.Rel(root, file)
	if de, ok := err.(*djoError); ok {
		if de.Line == 0 {
			return &djoError{0, rel + ": " + de.Msg}
		}
		return &djoError{de.Line, rel + ": " + de.Msg}
	}
	return &djoError{0, rel + ": " + err.Error()}
}

func lineOf(s Stmt) int {
	switch n := s.(type) {
	case *VarStmt:
		return n.Line
	case *AssignStmt:
		return n.Line
	case *IfStmt:
		return n.Line
	case *MatchStmt:
		return n.Line
	case *WhileStmt:
		return n.Line
	case *UntilStmt:
		return n.Line
	}
	return 0
}
