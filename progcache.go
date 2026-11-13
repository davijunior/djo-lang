package main

import (
	"os"
	"sync"
	"time"
)

// Cache dos programas já interpretados (lexer + parser), por arquivo. Um arquivo
// só é lido de novo quando o horário de modificação ou o tamanho mudam, então
// editar com 'djo serve' continua valendo na hora, e o servidor não refaz o
// mesmo trabalho a cada requisição.
//
// A árvore é compartilhada entre requisições. Só a struct muda depois de
// carregada (o 'link' liga pai, campos e métodos), então cada uso recebe uma cópia
// das structs; o resto da árvore é somente leitura.

type progEntry struct {
	mod  time.Time
	size int64
	prog []Stmt
}

var progCache sync.Map // caminho -> *progEntry

// loadProgram lê e compila um arquivo .djo (código) ou .djo.html (template).
func loadProgram(file string) ([]Stmt, error) {
	fi, err := os.Stat(file)
	if err != nil || !fi.Mode().IsRegular() {
		return parseFile(file) // devolve o erro de sempre
	}
	if v, ok := progCache.Load(file); ok {
		if e := v.(*progEntry); e.size == fi.Size() && e.mod.Equal(fi.ModTime()) {
			return cloneProg(e.prog), nil
		}
	}
	prog, err := parseFile(file)
	if err != nil {
		progCache.Delete(file)
		return nil, err
	}
	progCache.Store(file, &progEntry{fi.ModTime(), fi.Size(), prog})
	return cloneProg(prog), nil
}

// cloneProg copia a lista de instruções e as structs (que o interpretador altera).
func cloneProg(prog []Stmt) []Stmt {
	out := make([]Stmt, len(prog))
	for i, s := range prog {
		if sd, ok := s.(*StructDecl); ok {
			s = cloneStruct(sd)
		}
		out[i] = s
	}
	return out
}

func cloneStruct(sd *StructDecl) *StructDecl {
	c := *sd
	c.linked, c.linking, c.in, c.ParentSD = false, false, nil, nil
	c.Methods = make([]*FuncDecl, len(sd.Methods))
	c.methods = make(map[string]*FuncDecl, len(sd.methods))
	for i, m := range sd.Methods {
		mc := *m
		mc.Owner = &c
		c.Methods[i] = &mc
		c.methods[mc.Name] = &mc
	}
	return &c
}
