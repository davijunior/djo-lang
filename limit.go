package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limit: limite de uso por chave (por exemplo, por token já validado), em janela deslizante.
//
//	Limit.enforce:("tok:" + id):100:60      // 100 por 60 s; passou: responde 429 e PARA a página
//	var r = Limit.take:chave:100:60         // {allowed, remaining, retry_after_s, reason}
//
// Os contadores ficam na memória deste processo: somem no reinício e não valem entre várias instâncias.
// A chave é guardada só como SHA-256. Use com chave de quem já foi autenticado: com chaves que o visitante
// escolhe (IP, e-mail digitado), um atacante enche a tabela e o limite passa a recusar os outros ("cheio").

const (
	limitMaxKeys   = 50000
	limitMaxN      = 1000000
	limitMaxWindow = 86400
	limitMaxKeyLen = 512
)

var limitNow = time.Now // os testes trocam o relógio

type limBucket struct {
	win       int64 // tamanho da janela, em segundos
	idx       int64 // número da janela atual (agora / win)
	cur, prev int64
}

type limiter struct {
	mu        sync.Mutex
	m         map[[32]byte]*limBucket
	lastSweep time.Time
}

var globalLimiter = &limiter{}

func (in *Interp) limiterFor() *limiter {
	if in.limiter != nil {
		return in.limiter
	}
	return globalLimiter
}

type limResult struct {
	allowed   bool
	remaining int64
	retry     int64 // segundos até poder tentar de novo (0 se liberado)
	full      bool
}

// take conta uma tentativa na chave e diz se ela passa.
func (l *limiter) take(key string, n, win int64) limResult {
	now := limitNow()
	sec := now.Unix()
	idx := sec / win
	frac := float64(sec%win) / float64(win)
	h := sha256.Sum256([]byte(key))

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[[32]byte]*limBucket{}
	}
	b := l.m[h]
	if b == nil {
		if len(l.m) >= limitMaxKeys {
			l.sweep(now)
			if len(l.m) >= limitMaxKeys { // cheio de chaves vivas: recusa, nunca expulsa uma viva
				return limResult{allowed: false, retry: 1, full: true}
			}
		}
		b = &limBucket{win: win, idx: idx}
		l.m[h] = b
	}
	if b.win != win { // mesma chave com outra janela: recomeça
		*b = limBucket{win: win, idx: idx}
	}
	switch {
	case idx == b.idx:
	case idx == b.idx+1:
		b.prev, b.cur, b.idx = b.cur, 0, idx
	default:
		b.prev, b.cur, b.idx = 0, 0, idx
	}
	est := float64(b.prev)*(1-frac) + float64(b.cur)
	if est+1 > float64(n) {
		retry := win - sec%win // até a próxima janela (conservador)
		if retry < 1 {
			retry = 1
		}
		rem := int64(float64(n) - est)
		if rem < 0 {
			rem = 0
		}
		return limResult{allowed: false, remaining: rem, retry: retry}
	}
	b.cur++
	rem := int64(float64(n) - est - 1)
	if rem < 0 {
		rem = 0
	}
	return limResult{allowed: true, remaining: rem}
}

// sweep tira as chaves cujas duas janelas já passaram (no máximo uma varredura por segundo).
func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Second {
		return
	}
	l.lastSweep = now
	sec := now.Unix()
	for k, b := range l.m {
		if sec/b.win > b.idx+1 {
			delete(l.m, k)
		}
	}
}

func (l *limiter) reset() {
	l.mu.Lock()
	l.m = nil
	l.mu.Unlock()
}

func limitArgs(a []any, line int, fn string) (key string, n, win int64) {
	if len(a) < 3 {
		failc(line, "limit_args", "%s espera (chave, quantas, janela_em_segundos)", fn)
	}
	key, ok := a[0].(string)
	if !ok || key == "" || len(key) > limitMaxKeyLen {
		failc(line, "limit_args", "%s: a chave é um texto de 1 a %d bytes", fn, limitMaxKeyLen)
	}
	n, ok1 := a[1].(int64)
	win, ok2 := a[2].(int64)
	if !ok1 || n < 1 || n > limitMaxN {
		failc(line, "limit_args", "%s: 'quantas' vai de 1 a %d", fn, limitMaxN)
	}
	if !ok2 || win < 1 || win > limitMaxWindow {
		failc(line, "limit_args", "%s: a janela vai de 1 a %d segundos", fn, limitMaxWindow)
	}
	return
}

func limitMap(r limResult) *Map {
	m := newMap()
	m.set("allowed", r.allowed)
	m.set("remaining", r.remaining)
	m.set("retry_after_s", r.retry)
	if r.full {
		m.set("reason", "cheio")
	} else {
		m.set("reason", nil)
	}
	return m
}

func init() {
	ns := nativeNS("Limit")
	// Limit.take:chave:quantas:janela -> {allowed, remaining, retry_after_s, reason}
	ns["take"] = builtin{3, func(in *Interp, env *Env, line int, a []any) any {
		key, n, win := limitArgs(a, line, "Limit.take")
		return limitMap(in.limiterFor().take(key, n, win))
	}}
	// Limit.enforce:chave:quantas:janela[:{"body": ..., "headers": {...}}]
	// Passou do limite: 429 (ou 503 se a tabela está cheia) com Retry-After, e a página termina.
	ns["enforce"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		in.needWeb(line, "Limit.enforce")
		if len(a) < 3 || len(a) > 4 {
			failc(line, "limit_args", "Limit.enforce espera (chave, quantas, janela) e, se quiser, um mapa {\"body\": ..., \"headers\": {...}}")
		}
		key, n, win := limitArgs(a[:3], line, "Limit.enforce")
		var opt *Map
		if len(a) == 4 {
			var ok bool
			if opt, ok = a[3].(*Map); !ok {
				failc(line, "limit_args", "Limit.enforce: o 4º argumento é um mapa com body e/ou headers")
			}
		}
		r := in.limiterFor().take(key, n, win)
		if r.allowed {
			in.resp.header.Set("RateLimit-Remaining", strconv.FormatInt(r.remaining, 10))
			return nil
		}
		in.resp.status = http.StatusTooManyRequests
		code := "muitas_requisicoes"
		if r.full {
			in.resp.status = http.StatusServiceUnavailable
			code = "limite_cheio"
		}
		in.resp.header.Set("Retry-After", strconv.FormatInt(r.retry, 10))
		var body any
		if opt != nil {
			if h, ok := opt.get("headers"); ok {
				hm, isMap := h.(*Map)
				if !isMap {
					failc(line, "limit_args", "Limit.enforce: headers é um mapa de textos")
				}
				for i, k := range hm.Keys {
					ks, ok1 := k.(string)
					vs, ok2 := hm.Vals[i].(string)
					if !ok1 || !ok2 {
						failc(line, "limit_args", "Limit.enforce: headers é um mapa de textos")
					}
					noCRLF(line, "Limit.enforce", ks)
					noCRLF(line, "Limit.enforce", vs)
					in.resp.header.Set(ks, vs)
				}
			}
			body, _ = opt.get("body")
		}
		switch b := body.(type) {
		case nil:
			e := newMap()
			e.set("error", code)
			e.set("retry_after_s", r.retry)
			in.resp.header.Set("Content-Type", "application/json; charset=utf-8")
			fmt.Fprint(in.out, jsonEncode(e, "", line))
		case string:
			if in.resp.header.Get("Content-Type") == "" {
				in.resp.header.Set("Content-Type", "text/plain; charset=utf-8")
			}
			fmt.Fprint(in.out, b)
		default:
			in.resp.header.Set("Content-Type", "application/json; charset=utf-8")
			fmt.Fprint(in.out, jsonEncode(b, "", line))
		}
		panic(haltSignal{})
	}}
	// Spec.limit_reset: zera os contadores do Limit neste teste (cada teste já começa limpo)
	nativeNS("Spec")["limit_reset"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		if in.spec == nil {
			fail(line, "Spec.limit_reset só existe quando os testes rodam com 'djo spec'")
		}
		in.limiterFor().reset()
		return nil
	}}
}
