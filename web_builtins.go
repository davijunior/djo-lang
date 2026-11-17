//go:build !noweb

package main

import (
	"net/http"
	"net/url"
	"strings"
)

const hasWeb = true

// webState é o que a página guarda da requisição HTTP atual.
type webState struct {
	req      *http.Request // nil fora do servidor
	resp     *response
	body     []byte
	bodyRead bool
	sid      string            // sessão do visitante
	routes   *routeTable       // só enquanto o rotas.djo é lido
	params   map[string]string // partes variáveis da rota: posts/[slug].djo
}

// response guarda o que a página quer mudar na resposta HTTP.
type response struct {
	status  int
	header  http.Header
	cookies []*http.Cookie
}

func (in *Interp) needWeb(line int, name string) {
	if in.req == nil {
		fail(line, "'%s' só funciona em páginas servidas por 'djo serve'", name)
	}
}

func init() {
	for k, v := range map[string]builtin{
		"method": {0, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "method")
			return in.req.Method
		}},
		"path": {0, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "path")
			return in.req.URL.Path
		}},
		"query": {1, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "query")
			if v, ok := in.req.URL.Query()[argStr(a, 0, line, "query")]; ok && len(v) > 0 {
				return v[0]
			}
			return nil
		}},
		"param": {1, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "param")
			if v, ok := in.params[argStr(a, 0, line, "param")]; ok {
				return v
			}
			return nil
		}},
		"form": {1, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "form")
			if v, ok := in.req.PostForm[argStr(a, 0, line, "form")]; ok && len(v) > 0 {
				return v[0]
			}
			return nil
		}},
		"header": {1, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "header")
			if v := in.req.Header.Values(argStr(a, 0, line, "header")); len(v) > 0 {
				return v[0]
			}
			return nil
		}},
		// Http.bearer: o token de "Authorization: Bearer <token>", ou nil
		"bearer": {0, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "bearer")
			v := in.req.Header.Get("Authorization")
			if len(v) > 7 && strings.EqualFold(v[:7], "Bearer ") {
				if tok := strings.TrimSpace(v[7:]); tok != "" {
					return tok
				}
			}
			return nil
		}},
		"setheader": {2, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "setheader")
			name, val := argStr(a, 0, line, "setheader"), argStr(a, 1, line, "setheader")
			noCRLF(line, "setheader", name)
			noCRLF(line, "setheader", val)
			in.resp.header.Set(name, val)
			return nil
		}},
		"status": {1, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "status")
			code, ok := a[0].(int64)
			if !ok || code < 100 || code > 599 {
				fail(line, "'status' espera um Int entre 100 e 599")
			}
			in.resp.status = int(code)
			return nil
		}},
		"redirect": {1, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "redirect")
			to := argStr(a, 0, line, "redirect")
			noCRLF(line, "redirect", to)
			in.resp.header.Set("Location", to)
			in.resp.status = http.StatusFound
			panic(haltSignal{})
		}},
		"cookie": {1, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "cookie")
			c, err := in.req.Cookie(argStr(a, 0, line, "cookie"))
			if err != nil {
				return nil
			}
			v, err := url.QueryUnescape(c.Value)
			if err != nil {
				return nil
			}
			return v
		}},
		"setcookie": {2, func(in *Interp, env *Env, line int, a []any) any {
			in.needWeb(line, "setcookie")
			in.resp.cookies = append(in.resp.cookies, &http.Cookie{
				Name:     argStr(a, 0, line, "setcookie"),
				Value:    url.QueryEscape(argStr(a, 1, line, "setcookie")),
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   requestIsHTTPS(in.req),
			})
			return nil
		}},
	} {
		builtins[k] = v
	}
}

// reqQuery devolve um parâmetro ?nome= da requisição atual ("" fora do servidor ou se não veio).
func (in *Interp) reqQuery(name string) string {
	if in.req == nil {
		return ""
	}
	return in.req.URL.Query().Get(name)
}
