package main

import "sync"

// Tipos do Mail que existem em todos os perfis (o Interp, o Server e o djo spec os usam).

// mailBox guarda os e-mails "enviados" quando eles são capturados (djo spec): nada sai da máquina.
type mailBox struct {
	mu    sync.Mutex
	items []*Map
}

func (b *mailBox) add(m *Map) {
	b.mu.Lock()
	b.items = append(b.items, m)
	b.mu.Unlock()
}

func (b *mailBox) list() *Array {
	b.mu.Lock()
	defer b.mu.Unlock()
	return &Array{Items: append([]any(nil), anySlice(b.items)...)}
}

func anySlice(ms []*Map) []any {
	out := make([]any, len(ms))
	for i, m := range ms {
		out[i] = m
	}
	return out
}

// mailHost diz ao Mail.send o que fazer com um e-mail dentro de uma página servida:
// capturar (box), gravar um .eml em _saida/emails (dev) ou enviar de verdade (nenhum dos dois).
type mailHost struct {
	dev bool
	box *mailBox
}
