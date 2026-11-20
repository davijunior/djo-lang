package main

import (
	"net/url"
	"strings"
	"sync"
	"time"
)

// Mock nativo de Http para o 'djo spec' (nada sai pela rede).
//
//	Spec.http_mock:"GET":"https://api.x.com/itens*":[{"status": 500}, {"json": {"ok": 1}}]
//	Spec.http_calls  Spec.http_calls_raw  Spec.http_mock_clear
//
// Depois do primeiro Spec.http_mock, toda chamada Http.* do spec (inclusive páginas, jobs e boosters)
// que não casa com um mock é um erro: nunca vai para a rede.

const (
	httpMockMaxRules = 200
	httpMockMaxSeq   = 50
	httpMockMaxCalls = 1000
)

type httpMockResp struct {
	status  int
	body    string
	headers [][2]string
	timeout bool
	errMsg  string
	delay   time.Duration
}

type httpMockRule struct {
	method       string
	url          string
	prefix       bool
	bodyContains string // só casa se o corpo enviado tem este trecho
	jsonHas      *Map   // só casa se o corpo (JSON) contém estes campos (subconjunto)
	resp         []*httpMockResp
	next         int
}

type httpCallRec struct {
	method, url, body string
	headers           [][2]string
}

type httpMockSet struct {
	mu    sync.Mutex
	rules []*httpMockRule
	calls []httpCallRec
	armed bool
}

func (m *httpMockSet) isArmed() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.armed
}

func (m *httpMockSet) clear() {
	m.mu.Lock()
	m.rules, m.calls, m.armed = nil, nil, false
	m.mu.Unlock()
}

// sensitiveName: nomes de cabeçalho ou de parâmetro que carregam segredo.
func sensitiveName(n string) bool {
	n = strings.ToLower(n)
	for _, p := range []string{"authorization", "cookie", "token", "secret", "password", "senha", "api-key", "api_key", "apikey", "signature", "x-auth"} {
		if strings.Contains(n, p) {
			return true
		}
	}
	return n == "key" || n == "sig" || n == "auth"
}

// redactURL tira a senha (user:senha@) e os valores de parâmetros sensíveis (?token=...) de uma URL,
// para ela poder aparecer em mensagens e relatórios.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(URL inválida)"
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), "***")
		}
	}
	if u.RawQuery != "" {
		parts := strings.Split(u.RawQuery, "&")
		for i, p := range parts {
			k, _, has := strings.Cut(p, "=")
			if name, err := url.QueryUnescape(k); has && err == nil && sensitiveName(name) {
				parts[i] = k + "=***"
			}
		}
		u.RawQuery = strings.Join(parts, "&")
	}
	return strings.ReplaceAll(u.String(), "%2A%2A%2A", "***")
}

func needSpecHTTP(in *Interp, fn string, line int) *httpMockSet {
	if in.spec == nil {
		fail(line, "Spec.%s só existe quando os testes rodam com 'djo spec'", fn)
	}
	if !hasNet {
		fail(line, "Spec.%s: este build não tem Http (compilado com -tags nonet)", fn)
	}
	if in.spec.http == nil {
		in.spec.http = &httpMockSet{}
	}
	return in.spec.http
}

func parseHTTPMockResp(v any, line int) *httpMockResp {
	m, ok := v.(*Map)
	if !ok {
		fail(line, "Spec.http_mock: cada resposta é um mapa como {\"status\": 200, \"json\": {...}}, mas recebeu %s", typeName(v))
	}
	r := &httpMockResp{}
	for _, k := range m.Keys {
		switch k {
		case "status", "body", "json", "headers", "timeout", "error", "delay_ms":
		default:
			fail(line, "Spec.http_mock: o campo %v não existe (use status, body, json, headers, timeout, error, delay_ms)", k)
		}
	}
	has := func(k string) bool { _, ok := m.get(k); return ok }
	if has("body") && has("json") {
		fail(line, "Spec.http_mock: use body OU json, não os dois")
	}
	if has("timeout") || has("error") {
		if has("status") || has("body") || has("json") || has("headers") {
			fail(line, "Spec.http_mock: timeout e error são falhas de rede e não têm status, body, json nem headers")
		}
		if has("timeout") && has("error") {
			fail(line, "Spec.http_mock: use timeout OU error, não os dois")
		}
		if t, _ := m.get("timeout"); has("timeout") {
			if b, ok := t.(bool); !ok {
				fail(line, "Spec.http_mock: timeout é true ou false")
			} else {
				r.timeout = b
			}
		}
		if e, _ := m.get("error"); has("error") {
			s, ok := e.(string)
			if !ok || s == "" {
				fail(line, "Spec.http_mock: error é um texto não vazio")
			}
			r.errMsg = s
		}
	} else {
		r.status = 200
		if s, ok := m.get("status"); ok {
			n, isInt := s.(int64)
			if !isInt || n < 100 || n > 599 {
				fail(line, "Spec.http_mock: status vai de 100 a 599")
			}
			r.status = int(n)
		}
		if b, ok := m.get("body"); ok {
			s, isStr := b.(string)
			if !isStr {
				fail(line, "Spec.http_mock: body é um texto (para dados use json)")
			}
			r.body = s
		}
		ct := ""
		if j, ok := m.get("json"); ok {
			r.body, ct = jsonEncode(j, "", line), "application/json"
		}
		if h, ok := m.get("headers"); ok {
			hm, isMap := h.(*Map)
			if !isMap {
				fail(line, "Spec.http_mock: headers é um mapa de textos")
			}
			for i, k := range hm.Keys {
				ks, ok1 := k.(string)
				vs, ok2 := hm.Vals[i].(string)
				if !ok1 || !ok2 {
					fail(line, "Spec.http_mock: headers é um mapa de textos")
				}
				r.headers = append(r.headers, [2]string{strings.ToLower(ks), vs})
			}
		}
		if ct != "" {
			set := false
			for _, h := range r.headers {
				if h[0] == "content-type" {
					set = true
				}
			}
			if !set {
				r.headers = append(r.headers, [2]string{"content-type", ct})
			}
		}
	}
	if d, ok := m.get("delay_ms"); ok {
		n, isInt := d.(int64)
		if !isInt || n < 0 || n > 600000 {
			fail(line, "Spec.http_mock: delay_ms vai de 0 a 600000 (é um atraso simulado, o teste não espera)")
		}
		r.delay = time.Duration(n) * time.Millisecond
	}
	return r
}

// maskBody esconde, num corpo JSON, o valor dos campos sensíveis (em qualquer profundidade). Corpo que não é JSON fica como está.
func maskBody(body string) string {
	v, err := jsonDecode(body)
	if err != nil {
		return body
	}
	var walk func(x any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case *Map:
			for i, k := range t.Keys {
				if ks, ok := k.(string); ok && (sensitiveName(ks) || sensitiveBodyName(ks)) {
					t.Vals[i] = "***"
				} else {
					t.Vals[i] = walk(t.Vals[i])
				}
			}
		case *Array:
			for i := range t.Items {
				t.Items[i] = walk(t.Items[i])
			}
		}
		return x
	}
	return jsonEncode(walk(v), "", 0)
}

// sensitiveBodyName: campos de corpo que levam credencial e não casam com os nomes de cabeçalho (app_key do Omie).
func sensitiveBodyName(n string) bool {
	n = strings.ToLower(n)
	return n == "app_key" || n == "app_secret" || n == "client_secret" || n == "private_key"
}

func maskedHeaders(h [][2]string, mask bool) *Map {
	m := newMap()
	for _, kv := range h {
		v := kv[1]
		if mask && sensitiveName(kv[0]) {
			v = "***"
		}
		m.set(kv[0], v)
	}
	return m
}

func (m *httpMockSet) callList(raw bool) *Array {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := &Array{}
	for _, c := range m.calls {
		e := newMap()
		e.set("method", c.method)
		if raw {
			e.set("url", c.url)
		} else {
			e.set("url", redactURL(c.url))
		}
		e.set("headers", maskedHeaders(c.headers, !raw))
		if raw {
			e.set("body", c.body)
		} else {
			e.set("body", maskBody(c.body))
		}
		out.Items = append(out.Items, e)
	}
	return out
}

func init() {
	ns := nativeNS("Spec")
	// Spec.http_mock:método:url:resposta (ou lista de respostas, uma por chamada; a última se repete)
	// Spec.http_mock:método:url:{"body_contains": "...", "json_has": {...}}:resposta (só casa com esse corpo)
	ns["http_mock"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		set := needSpecHTTP(in, "http_mock", line)
		if len(a) != 3 && len(a) != 4 {
			fail(line, "Spec.http_mock espera (método, url, resposta) ou (método, url, condição, resposta)")
		}
		var bodyContains string
		var jsonHas *Map
		if len(a) == 4 {
			cm, ok := a[2].(*Map)
			if !ok {
				fail(line, "Spec.http_mock: a condição é um mapa com body_contains e/ou json_has")
			}
			for i, k := range cm.Keys {
				switch k {
				case "body_contains":
					s, isStr := cm.Vals[i].(string)
					if !isStr || s == "" {
						fail(line, "Spec.http_mock: body_contains é um texto não vazio")
					}
					bodyContains = s
				case "json_has":
					jm, isMap := cm.Vals[i].(*Map)
					if !isMap {
						fail(line, "Spec.http_mock: json_has é um mapa com os campos que o corpo JSON deve ter")
					}
					jsonHas = jm
				default:
					fail(line, "Spec.http_mock: a condição só tem body_contains e json_has")
				}
			}
			a = []any{a[0], a[1], a[3]}
		}
		method := strings.ToUpper(argStr(a, 0, line, "Spec.http_mock"))
		switch method {
		case "GET", "POST", "PUT", "DELETE":
		default:
			fail(line, "Spec.http_mock: o método é GET, POST, PUT ou DELETE")
		}
		raw := argStr(a, 1, line, "Spec.http_mock")
		prefix := strings.HasSuffix(raw, "*")
		check := strings.TrimSuffix(raw, "*")
		u, err := url.Parse(check)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fail(line, "Spec.http_mock: a URL precisa começar com http:// ou https:// (recebeu %s)", redactURL(check))
		}
		rule := &httpMockRule{method: method, url: check, prefix: prefix, bodyContains: bodyContains, jsonHas: jsonHas}
		if !prefix {
			rule.url = u.String()
		}
		if arr, ok := a[2].(*Array); ok {
			if len(arr.Items) == 0 || len(arr.Items) > httpMockMaxSeq {
				fail(line, "Spec.http_mock: a lista de respostas vai de 1 a %d", httpMockMaxSeq)
			}
			for _, it := range arr.Items {
				rule.resp = append(rule.resp, parseHTTPMockResp(it, line))
			}
		} else {
			rule.resp = []*httpMockResp{parseHTTPMockResp(a[2], line)}
		}
		set.mu.Lock()
		defer set.mu.Unlock()
		if len(set.rules) >= httpMockMaxRules {
			fail(line, "Spec.http_mock: no máximo %d mocks por spec", httpMockMaxRules)
		}
		set.rules = append(set.rules, rule)
		set.armed = true
		return nil
	}}
	// Spec.http_calls: o que o spec chamou (Authorization/Cookie e afins mascarados; URL sem senha nem ?token=)
	ns["http_calls"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		return needSpecHTTP(in, "http_calls", line).callList(false)
	}}
	// Spec.http_calls_raw: igual, sem máscara (para conferir o cabeçalho de verdade)
	ns["http_calls_raw"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		return needSpecHTTP(in, "http_calls_raw", line).callList(true)
	}}
	ns["http_mock_clear"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		needSpecHTTP(in, "http_mock_clear", line).clear()
		return nil
	}}
}

// jsonSubset diz se got contém tudo o que want tem (mapas: campo a campo; listas e valores: iguais).
func jsonSubset(want, got any) bool {
	if wm, ok := want.(*Map); ok {
		gm, ok := got.(*Map)
		if !ok {
			return false
		}
		for i, k := range wm.Keys {
			ks, isStr := k.(string)
			if !isStr {
				return false
			}
			gv, has := gm.get(ks)
			if !has || !jsonSubset(wm.Vals[i], gv) {
				return false
			}
		}
		return true
	}
	return equal(want, got)
}

// matchesBody confere as condições de corpo do mock.
func (r *httpMockRule) matchesBody(body string) bool {
	if r.bodyContains != "" && !strings.Contains(body, r.bodyContains) {
		return false
	}
	if r.jsonHas != nil {
		v, err := jsonDecode(body)
		if err != nil || !jsonSubset(r.jsonHas, v) {
			return false
		}
	}
	return true
}
