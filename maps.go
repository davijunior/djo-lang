package main

import (
	"strconv"
	"strings"
)

// Map é o mapa da djo (map): chaves String ou Int, na ordem em que entraram.
// Tem semântica de referência, como a lista.
type Map struct {
	Keys []any
	Vals []any
	idx  map[any]int
}

func newMap() *Map { return &Map{idx: map[any]int{}} }

func mapKey(v any, line int) any {
	switch v.(type) {
	case string, int64:
		return v
	}
	fail(line, "a chave de um map precisa ser String ou Int, mas é %s", typeName(v))
	return nil
}

func (m *Map) get(k any) (any, bool) {
	i, ok := m.idx[k]
	if !ok {
		return nil, false
	}
	return m.Vals[i], true
}

func (m *Map) set(k, v any) {
	if i, ok := m.idx[k]; ok {
		m.Vals[i] = v
		return
	}
	m.idx[k] = len(m.Keys)
	m.Keys = append(m.Keys, k)
	m.Vals = append(m.Vals, v)
}

func (m *Map) del(k any) bool {
	i, ok := m.idx[k]
	if !ok {
		return false
	}
	m.Keys = append(m.Keys[:i], m.Keys[i+1:]...)
	m.Vals = append(m.Vals[:i], m.Vals[i+1:]...)
	delete(m.idx, k)
	for j := i; j < len(m.Keys); j++ {
		m.idx[m.Keys[j]] = j
	}
	return true
}

func formatMap(m *Map) string {
	parts := make([]string, len(m.Keys))
	for i, k := range m.Keys {
		parts[i] = formatItem(k) + ": " + formatItem(m.Vals[i])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func mustMap(v any, fn string, line int) *Map {
	m, ok := v.(*Map)
	if !ok {
		fail(line, "'%s' espera um map, mas recebeu %s", fn, typeName(v))
	}
	return m
}

// formatItem mostra um valor dentro de uma lista, mapa ou objeto: texto entre aspas.
func formatItem(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	return format(v)
}
