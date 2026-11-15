//go:build !noweb

package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// CORS, só quando o site pede com Route.cors no rotas.djo:
//
//	Route.cors:{"origins": ["https://app.exemplo.com"], "headers": ["Content-Type", "Authorization"], "max_age": 600}
//
// Sem Route.cors, nada de CORS: o navegador bloqueia chamadas de outros domínios (o preflight
// OPTIONS dá 405). Com ele, só as origens listadas recebem permissão.

type corsCfg struct {
	any         bool            // "*": qualquer origem (nunca junto com credentials)
	origins     map[string]bool // origens exatas permitidas
	headers     string          // Access-Control-Allow-Headers
	credentials bool
	maxAge      int
}

// allowed devolve o valor de Access-Control-Allow-Origin para a origem da requisição ("" = não permitida).
func (c *corsCfg) allowed(origin string) string {
	if origin == "" {
		return ""
	}
	if c.origins[origin] {
		return origin
	}
	if c.any {
		return "*"
	}
	return ""
}

func (in *Interp) addCors(line int, a []any) {
	t := in.needRoutes(line)
	if t.cors != nil {
		fail(line, "Route.cors só pode aparecer uma vez")
	}
	m, ok := a[0].(*Map)
	if !ok {
		fail(line, "Route.cors espera um mapa: {\"origins\": [\"https://app.exemplo.com\"]}")
	}
	c := &corsCfg{origins: map[string]bool{}, headers: "Content-Type, Authorization", maxAge: 600}
	for i, k := range m.Keys {
		ks, _ := k.(string)
		v := m.Vals[i]
		switch ks {
		case "origins":
			arr, ok := v.(*Array)
			if !ok || len(arr.Items) == 0 {
				fail(line, "Route.cors: \"origins\" precisa ser uma lista com ao menos uma origem")
			}
			for _, it := range arr.Items {
				o, _ := it.(string)
				switch {
				case o == "*":
					c.any = true
				case validOrigin(o):
					c.origins[o] = true
				default:
					fail(line, "Route.cors: origem inválida %v (use \"https://site.com\" ou \"https://site.com:8443\", sem barra no fim, ou \"*\")", it)
				}
			}
		case "headers":
			arr, ok := v.(*Array)
			if !ok {
				fail(line, "Route.cors: \"headers\" precisa ser uma lista de nomes de cabeçalho")
			}
			var hs []string
			for _, it := range arr.Items {
				h, _ := it.(string)
				if !validHeaderName(h) {
					fail(line, "Route.cors: cabeçalho inválido %v", it)
				}
				hs = append(hs, h)
			}
			c.headers = strings.Join(hs, ", ")
		case "credentials":
			b, ok := v.(bool)
			if !ok {
				fail(line, "Route.cors: \"credentials\" precisa ser Bool")
			}
			c.credentials = b
		case "max_age":
			n, ok := v.(int64)
			if !ok || n < 0 || n > 86400 {
				fail(line, "Route.cors: \"max_age\" precisa ser um Int de 0 a 86400 (segundos)")
			}
			c.maxAge = int(n)
		default:
			fail(line, "Route.cors: a chave %v não existe (use \"origins\", \"headers\", \"credentials\" e \"max_age\")", k)
		}
	}
	if len(c.origins) == 0 && !c.any {
		fail(line, "Route.cors: informe \"origins\"")
	}
	if c.any && c.credentials {
		fail(line, "Route.cors: \"*\" não pode ser usado com \"credentials\": true (liste as origens)")
	}
	t.cors = c
}

func validOrigin(o string) bool {
	rest := ""
	switch {
	case strings.HasPrefix(o, "https://"):
		rest = o[len("https://"):]
	case strings.HasPrefix(o, "http://"):
		rest = o[len("http://"):]
	default:
		return false
	}
	if rest == "" || strings.ContainsAny(rest, "/?#@ \r\n\t") {
		return false
	}
	return true
}

func validHeaderName(h string) bool {
	if h == "" || len(h) > 60 {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// corsWriter acrescenta os cabeçalhos de CORS no momento de responder, depois de qualquer cache,
// para que uma resposta guardada para uma origem nunca saia com a permissão de outra.
type corsWriter struct {
	http.ResponseWriter
	cfg    *corsCfg
	origin string
	done   bool
}

func (c *corsWriter) apply() {
	if c.done {
		return
	}
	c.done = true
	h := c.Header()
	h.Add("Vary", "Origin")
	h.Del("Access-Control-Allow-Origin")
	h.Del("Access-Control-Allow-Credentials")
	h.Del("Access-Control-Expose-Headers")
	if v := c.cfg.allowed(c.origin); v != "" {
		h.Set("Access-Control-Allow-Origin", v)
		h.Set("Access-Control-Expose-Headers", "Link, X-Total-Count") // para o front-end ler a paginação
		if c.cfg.credentials && v != "*" {
			h.Set("Access-Control-Allow-Credentials", "true")
		}
	}
}

func (c *corsWriter) WriteHeader(code int) {
	c.apply()
	c.ResponseWriter.WriteHeader(code)
}

func (c *corsWriter) Write(b []byte) (int, error) {
	c.apply()
	return c.ResponseWriter.Write(b)
}

func (c *corsWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// corsPreflight responde ao OPTIONS do navegador. Devolve true se tratou a requisição.
func (s *Server) corsPreflight(w http.ResponseWriter, r *http.Request, t *routeTable, parts []string) bool {
	origin := r.Header.Get("Origin")
	if r.Method != http.MethodOptions || origin == "" || r.Header.Get("Access-Control-Request-Method") == "" {
		return false
	}
	var methods []string
	for _, rt := range t.list {
		if _, ok := rt.match(parts); ok {
			methods = append(methods, rt.method)
		}
	}
	if len(methods) == 0 {
		return false // caminho que não existe: segue o fluxo normal (404)
	}
	h := w.Header()
	h.Add("Vary", "Origin")
	h.Add("Vary", "Access-Control-Request-Method")
	allow := t.cors.allowed(origin)
	if allow == "" {
		http.Error(w, "403 origem não permitida", http.StatusForbidden)
		return true
	}
	sort.Strings(methods)
	methods = append(uniq(methods), "OPTIONS")
	h.Set("Access-Control-Allow-Origin", allow)
	if t.cors.credentials && allow != "*" {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	h.Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))
	h.Set("Access-Control-Allow-Headers", t.cors.headers)
	h.Set("Access-Control-Max-Age", strconv.Itoa(t.cors.maxAge))
	w.WriteHeader(http.StatusNoContent)
	return true
}
