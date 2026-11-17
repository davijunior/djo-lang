//go:build !noweb

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ---- pista dos agentes: limites para o tráfego de máquinas não atrapalhar as pessoas ----
//
// Só as respostas de agente e as actions passam por aqui. A tela de quem usa o navegador
// nunca espera atrás de um agente.

type lane struct {
	sem   chan struct{}
	wait  time.Duration // quanto uma chamada espera por vaga antes de levar 429
	rate  float64       // requisições por segundo por cliente (0 = sem limite)
	burst float64

	mu      sync.Mutex
	clients map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLane(conc int, rate float64) *lane {
	if conc <= 0 {
		conc = 2 * runtime.NumCPU()
	}
	return &lane{sem: make(chan struct{}, conc), wait: 250 * time.Millisecond, rate: rate, burst: 2 * rate, clients: map[string]*bucket{}}
}

// connIP é o endereço da conexão (o RemoteAddr sem a porta), sem olhar cabeçalho nenhum.
func connIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *lane) allow(ip string, now time.Time) bool {
	if l.rate <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.clients[ip]
	if b == nil {
		if len(l.clients) >= 10000 { // limpa quem está parado há mais de 5 minutos
			for k, v := range l.clients {
				if now.Sub(v.last) > 5*time.Minute {
					delete(l.clients, k)
				}
			}
			if len(l.clients) >= 10000 { // ainda cheia: descarta o mais antigo (não recusa quem chega)
				oldest, ot := "", now
				for k, v := range l.clients {
					if v.last.Before(ot) || oldest == "" {
						oldest, ot = k, v.last
					}
				}
				delete(l.clients, oldest)
			}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.clients[ip] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// acquire reserva uma vaga; devolve nil (e já respondeu 429) se não der.
func (l *lane) acquire(w http.ResponseWriter, r *http.Request) func() {
	if !l.allow(clientIP(r), time.Now()) {
		w.Header().Set("Retry-After", "1")
		jsonError(w, http.StatusTooManyRequests, "muitas requisições deste cliente; tente de novo em instantes", nil)
		return nil
	}
	t := time.NewTimer(l.wait)
	defer t.Stop()
	select {
	case l.sem <- struct{}{}:
		return func() { <-l.sem }
	case <-t.C:
		w.Header().Set("Retry-After", "1")
		jsonError(w, http.StatusTooManyRequests, "servidor ocupado com outros agentes; tente de novo em instantes", nil)
	case <-r.Context().Done():
	}
	return nil
}

// ---- resposta capturada: ETag, 304 e cache ----

type captured struct {
	h      http.Header
	status int
	body   bytes.Buffer
}

func (c *captured) Header() http.Header { return c.h }
func (c *captured) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
}
func (c *captured) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(p)
}

func (c *captured) flush(w http.ResponseWriter) {
	h := w.Header()
	for k, v := range c.h {
		h[k] = v
	}
	if c.status == 0 {
		c.status = http.StatusOK
	}
	w.WriteHeader(c.status)
	w.Write(c.body.Bytes())
}

func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

// etagMatches confere o If-None-Match (aceita lista, W/ e *).
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if p == "*" || p == etag {
			return true
		}
	}
	return false
}

type cachedResp struct {
	exp    time.Time
	status int
	h      http.Header
	body   []byte
	etag   string
}

type respCache struct {
	mu sync.Mutex
	m  map[string]*cachedResp
}

const (
	cacheMaxEntries = 500
	cacheMaxBody    = 256 << 10
)

func (c *respCache) get(key string) *cachedResp {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[key]
	if e != nil && time.Now().After(e.exp) {
		delete(c.m, key)
		return nil
	}
	return e
}

func (c *respCache) put(key string, e *cachedResp) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*cachedResp{}
	}
	if len(c.m) >= cacheMaxEntries {
		now := time.Now()
		for k, v := range c.m {
			if now.After(v.exp) {
				delete(c.m, k)
			}
		}
		for k := range c.m { // ainda cheio: tira uma qualquer
			if len(c.m) < cacheMaxEntries {
				break
			}
			delete(c.m, k)
		}
	}
	c.m[key] = e
}

// finish envia a resposta capturada: acrescenta ETag, atende If-None-Match com 304 e,
// quando a rota pede, guarda em memória por 'secs' segundos.
func (s *Server) finish(w http.ResponseWriter, r *http.Request, c *captured, key string, secs int, cacheable bool) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if c.status != http.StatusOK || c.h.Get("Set-Cookie") != "" {
		c.h.Set("Cache-Control", "private, no-store")
		c.flush(w)
		return
	}
	etag := etagOf(c.body.Bytes())
	c.h.Set("ETag", etag)
	if cacheable && secs > 0 {
		c.h.Set("Cache-Control", "public, max-age="+itoa(secs))
		if c.body.Len() <= cacheMaxBody {
			h := c.h.Clone()
			s.rcache.put(key, &cachedResp{exp: time.Now().Add(time.Duration(secs) * time.Second), status: c.status, h: h, body: append([]byte(nil), c.body.Bytes()...), etag: etag})
		}
	} else {
		c.h.Set("Cache-Control", "private, no-cache")
	}
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		h := w.Header()
		for _, k := range []string{"ETag", "Cache-Control", "Vary", "Link"} {
			if v := c.h.Values(k); len(v) > 0 {
				h[k] = v
			}
		}
		w.WriteHeader(http.StatusNotModified)
		return
	}
	c.flush(w)
}

func (s *Server) writeCached(w http.ResponseWriter, r *http.Request, e *cachedResp) {
	h := w.Header()
	for k, v := range e.h {
		h[k] = v
	}
	if etagMatches(r.Header.Get("If-None-Match"), e.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(e.status)
	w.Write(e.body)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
