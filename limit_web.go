//go:build !noweb

package main

import (
	"fmt"
	"net/http"
	"strconv"
)

func init() {
	ns := nativeNS("Limit")
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
}
