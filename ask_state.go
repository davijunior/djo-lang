//go:build !noweb

package main

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// askState: o que o servidor guarda do /ask (política carregada e contadores de uso).
type askState struct {
	mu   sync.Mutex
	cfg  *askConfig
	mod  time.Time
	size int64
	rl   askLimiter
}

// config devolve a política do ask.djo da raiz, recarregando quando o arquivo muda.
// nil, nil = não há ask.djo: o /ask está desligado.
func (a *askState) config(root string, dev bool) (*askConfig, error) {
	fi, err := os.Stat(filepath.Join(root, "ask.djo"))
	if err != nil || !fi.Mode().IsRegular() {
		a.mu.Lock()
		a.cfg = nil
		a.mu.Unlock()
		return nil, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg != nil && !dev && fi.ModTime().Equal(a.mod) && fi.Size() == a.size {
		return a.cfg, nil
	}
	cfg, err := loadAsk(root)
	if err != nil {
		return nil, err
	}
	a.cfg, a.mod, a.size = cfg, fi.ModTime(), fi.Size()
	return cfg, nil
}

// askLimiter conta chamadas por chave numa janela de um minuto.
type askLimiter struct {
	mu  sync.Mutex
	win map[string]*askWin
}

type askWin struct {
	start time.Time
	n     int
}

// allow registra uma chamada de key; max <= 0 = sem limite. Se recusar, devolve em quantos segundos tentar de novo.
func (l *askLimiter) allow(key string, max int, now time.Time) (bool, int) {
	if max <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.win == nil {
		l.win = map[string]*askWin{}
	}
	w := l.win[key]
	if w == nil || now.Sub(w.start) >= time.Minute {
		if w == nil && len(l.win) >= 20000 {
			for k, v := range l.win {
				if now.Sub(v.start) >= time.Minute {
					delete(l.win, k)
				}
			}
			if len(l.win) >= 20000 {
				return false, 60
			}
		}
		w = &askWin{start: now}
		l.win[key] = w
	}
	if w.n >= max {
		retry := int(time.Minute.Seconds() - now.Sub(w.start).Seconds())
		if retry < 1 {
			retry = 1
		}
		return false, retry
	}
	w.n++
	return true, 0
}
