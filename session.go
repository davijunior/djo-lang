//go:build !noweb

package main

import (
	"crypto/subtle"
	"net/http"
	"sync"
	"time"
)

// Session: dados do visitante guardados no servidor, ligados a um cookie.
// Hash: senhas e valores aleatórios.

const (
	sessCookie  = "djo_sid"
	sessTTL     = 24 * time.Hour
	sessMax     = 10000
	sessMaxData = 64 * 1024 // tamanho máximo dos dados de uma sessão (em JSON)
)

type session struct {
	data *Map
	csrf string
	seen time.Time
}

var sessions = struct {
	sync.Mutex
	m map[string]*session
}{m: map[string]*session{}}

// sessionCopy copia só dados simples; objetos (structs) ficam de fora.
func sessionCopy(v any, depth, line int) any {
	if depth > 20 {
		fail(line, "Session: dado fundo demais")
	}
	switch x := v.(type) {
	case nil, bool, int64, float64, string:
		return x
	case *Array:
		out := &Array{Items: make([]any, len(x.Items))}
		for i, it := range x.Items {
			out.Items[i] = sessionCopy(it, depth+1, line)
		}
		return out
	case *Map:
		out := newMap()
		for i, k := range x.Keys {
			out.set(k, sessionCopy(x.Vals[i], depth+1, line))
		}
		return out
	}
	fail(line, "Session guarda só dados simples (texto, número, Bool, Arr, Map), e isso é %s", typeName(v))
	return nil
}

// current devolve a sessão do visitante, criando uma nova (e o cookie) se preciso.
// O chamador precisa estar com sessions.Lock().
func (in *Interp) current(line int) *session {
	in.needWeb(line, "Session")
	now := time.Now()
	if in.sid != "" {
		if s, ok := sessions.m[in.sid]; ok {
			s.seen = now
			return s
		}
	}
	if c, err := in.req.Cookie(sessCookie); err == nil {
		if s, ok := sessions.m[c.Value]; ok && now.Sub(s.seen) < sessTTL {
			s.seen = now
			in.sid = c.Value
			return s
		}
	}
	return in.newSession(now, line)
}

func (in *Interp) newSession(now time.Time, line int) *session {
	if len(sessions.m) >= sessMax {
		for id, s := range sessions.m { // limpa as vencidas
			if now.Sub(s.seen) >= sessTTL {
				delete(sessions.m, id)
			}
		}
		if len(sessions.m) >= sessMax {
			fail(line, "Session: servidor com sessões demais")
		}
	}
	id := randHex(16)
	s := &session{data: newMap(), csrf: randHex(16), seen: now}
	sessions.m[id] = s
	in.sid = id
	in.setSessionCookie(id, 0)
	return s
}

func (in *Interp) setSessionCookie(value string, maxAge int) {
	kept := in.resp.cookies[:0]
	for _, c := range in.resp.cookies {
		if c.Name != sessCookie {
			kept = append(kept, c)
		}
	}
	in.resp.cookies = append(kept, &http.Cookie{
		Name: sessCookie, Value: value, Path: "/", HttpOnly: true, MaxAge: maxAge,
		SameSite: http.SameSiteLaxMode, Secure: in.req.TLS != nil,
	})
}

func sessKey(a []any, line int, fn string) any {
	k, ok := a[0].(string)
	if !ok {
		fail(line, "%s espera o nome como String, mas recebeu %s", fn, typeName(a[0]))
	}
	return k
}

func init() {
	locked := func(f func(in *Interp, line int, a []any) any) func(*Interp, *Env, int, []any) any {
		return func(in *Interp, env *Env, line int, a []any) any {
			sessions.Lock()
			defer sessions.Unlock()
			return f(in, line, a)
		}
	}
	natives["Session"] = map[string]builtin{
		"get": {1, locked(func(in *Interp, line int, a []any) any {
			v, _ := in.current(line).data.get(sessKey(a, line, "Session.get"))
			return sessionCopy(v, 0, line)
		})},
		"set": {2, locked(func(in *Interp, line int, a []any) any {
			s := in.current(line)
			k := sessKey(a, line, "Session.set")
			old, had := s.data.get(k)
			s.data.set(k, sessionCopy(a[1], 0, line))
			if len(jsonEncode(s.data, "", line)) > sessMaxData {
				if had {
					s.data.set(k, old)
				} else {
					s.data.del(k)
				}
				fail(line, "Session: dados grandes demais (máximo %d KB)", sessMaxData/1024)
			}
			return nil
		})},
		"del": {1, locked(func(in *Interp, line int, a []any) any {
			return in.current(line).data.del(sessKey(a, line, "Session.del"))
		})},
		"clear": {0, locked(func(in *Interp, line int, a []any) any {
			in.current(line).data = newMap()
			return nil
		})},
		// Session.destroy: encerra a sessão (logout)
		"destroy": {0, locked(func(in *Interp, line int, a []any) any {
			in.needWeb(line, "Session")
			if c, err := in.req.Cookie(sessCookie); err == nil {
				delete(sessions.m, c.Value)
			}
			delete(sessions.m, in.sid)
			in.sid = ""
			in.setSessionCookie("", -1)
			return nil
		})},
		// Session.renew: novo identificador, mesmos dados. Chame ao fazer login.
		"renew": {0, locked(func(in *Interp, line int, a []any) any {
			s := in.current(line)
			delete(sessions.m, in.sid)
			in.sid = randHex(16)
			s.csrf = randHex(16)
			sessions.m[in.sid] = s
			in.setSessionCookie(in.sid, 0)
			return nil
		})},
		// Session.token: valor para pôr em formulários (proteção CSRF)
		"token": {0, locked(func(in *Interp, line int, a []any) any {
			return in.current(line).csrf
		})},
		// Session.verify:valor -> o valor enviado bate com o token?
		"verify": {1, locked(func(in *Interp, line int, a []any) any {
			s := in.current(line)
			v, ok := a[0].(string)
			return ok && subtle.ConstantTimeCompare([]byte(v), []byte(s.csrf)) == 1
		})},
	}
}
