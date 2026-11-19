//go:build !noweb

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

// Spec.get/post/put/patch/delete: chama uma rota, action ou página do site em processo, sem abrir porta.

func init() { specRequestHook = specRequest }

func specCloseServer(c *specCtx) { c.server = nil }

func specServer(c *specCtx, line int) *Server {
	if s, ok := c.server.(*Server); ok {
		return s
	}
	s, err := NewServer(c.root, true)
	if err != nil {
		fail(line, "Spec: não consegui montar o site para o teste: %v", err)
	}
	s.dbRoot = c.dbRoot
	s.mailBox = c.mails
	s.httpMock = c.http
	s.limiter = c.lim
	s.jobs.be = c.jobs
	s.maxSteps = specMaxSteps
	c.server = s
	return s
}

func specRequest(in *Interp, c *specCtx, method, path string, body any, hasBody bool, hdr *Map, line int) any {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "://") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, " \r\n\x00") {
		fail(line, "Spec.%s: o caminho deve ser local e começar com / (ex.: \"/posts?pagina=2\")", strings.ToLower(method))
	}
	var rd io.Reader
	ctype := ""
	if hasBody {
		switch b := body.(type) {
		case nil:
		case string:
			rd = strings.NewReader(b)
			ctype = "application/x-www-form-urlencoded"
		case *Map, *Array:
			rd = strings.NewReader(jsonEncode(b, "", line))
			ctype = "application/json"
		default:
			fail(line, "Spec.%s: o corpo é um mapa, uma lista ou um texto", strings.ToLower(method))
		}
	}
	req := httptest.NewRequest(method, path, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if hdr != nil {
		for i, k := range hdr.Keys {
			ks, ok := k.(string)
			if !ok {
				fail(line, "Spec.%s: o nome de um cabeçalho é texto", strings.ToLower(method))
			}
			req.Header.Set(ks, format(hdr.Vals[i]))
		}
	}
	rec := httptest.NewRecorder()
	specServer(c, line).ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	out := newMap()
	out.set("status", int64(res.StatusCode))
	out.set("body", string(data))
	h := newMap()
	for k, v := range res.Header {
		h.set(http.CanonicalHeaderKey(k), strings.Join(v, ", "))
	}
	out.set("headers", h)
	return out
}
