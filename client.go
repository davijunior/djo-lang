//go:build !nonet

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Cliente HTTP (saída): Http.get, Http.post, Http.put, Http.delete.
//
//	var r = Http.get:"https://api.exemplo.com/x"
//	r["ok"]  r["status"]  r["body"]  r["headers"]  r["error"]
//
// Nunca dá erro por falha de rede: devolve {ok: false, status: 0, error: "..."}.

const hasNet = true

const (
	clientTimeout   = 8 * time.Second
	clientMaxBody   = 5 << 20
	clientMaxServer = 5 // chamadas por página dentro do 'djo serve'
)

// blockedIP: endereços internos, que uma página servida não deve alcançar (SSRF).
func blockedIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
		if ip4[0] == 100 && ip4[1]&0xC0 == 64 { // 100.64.0.0/10
			return true
		}
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

func newHTTPClient(restrict bool) *http.Client {
	d := &net.Dialer{Timeout: clientTimeout}
	if restrict {
		d.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || blockedIP(ip) {
				return fmt.Errorf("endereço interno bloqueado (use djo serve -allow-private para permitir)")
			}
			return nil
		}
	}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: clientTimeout, MaxIdleConns: 0, DisableKeepAlives: true}
	if !restrict {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Transport: tr,
		Timeout:   clientTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("redirecionamentos demais")
			}
			return nil
		},
	}
}

func clientResult(ok bool, status int, body string, headers *Map, errMsg string) *Map {
	m := newMap()
	m.set("ok", ok)
	m.set("status", int64(status))
	m.set("body", body)
	m.set("headers", headers)
	if errMsg != "" {
		m.set("error", errMsg)
	}
	return m
}

// httpDo faz a requisição. body: nil, String (enviado como está) ou dados (viram JSON).
func (in *Interp) httpDo(method, fn string, a []any, line int) any {
	rawURL, ok := a[0].(string)
	if !ok {
		fail(line, "%s espera a URL como String, mas recebeu %s", fn, typeName(a[0]))
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		fail(line, "%s: a URL precisa começar com http:// ou https://", fn)
	}

	var body io.Reader
	contentType := ""
	hasBody := method == "POST" || method == "PUT"
	argHeaders := 1
	if hasBody {
		argHeaders = 2
		if len(a) < 2 {
			fail(line, "%s espera (url, corpo) ou (url, corpo, cabeçalhos)", fn)
		}
		switch b := a[1].(type) {
		case nil:
		case string:
			body, contentType = strings.NewReader(b), "text/plain; charset=utf-8"
		case *Map, *Array, *Instance:
			body, contentType = strings.NewReader(jsonEncode(b, "", line)), "application/json"
		default:
			fail(line, "%s: o corpo é String ou dados (Map, Arr), mas recebeu %s", fn, typeName(a[1]))
		}
	}
	if len(a) > argHeaders+1 {
		fail(line, "%s recebeu argumentos demais", fn)
	}

	if in.served {
		max := clientMaxServer
		if in.outMax > 0 {
			max = in.outMax
		}
		if in.outCalls >= max {
			if in.outMax > 0 {
				failFatal(line, "%s: no máximo %d chamadas externas por job (quem administra o servidor muda isso com djo serve -jobs-http N, até %d)", fn, max, jobHTTPMax)
			}
			failFatal(line, "%s: no máximo %d chamadas externas por página", fn, max)
		}
		in.outCalls++
	}

	req, err := http.NewRequestWithContext(context.Background(), method, u.String(), body)
	if err != nil {
		fail(line, "%s: %v", fn, err)
	}
	req.Header.Set("User-Agent", "djo")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if len(a) > argHeaders {
		hm, ok := a[argHeaders].(*Map)
		if !ok {
			fail(line, "%s: os cabeçalhos vão num Map, como {\"Authorization\": \"...\"}", fn)
		}
		for i, k := range hm.Keys {
			name, ok1 := k.(string)
			val, ok2 := hm.Vals[i].(string)
			if !ok1 || !ok2 {
				fail(line, "%s: cabeçalhos são sempre texto", fn)
			}
			noCRLF(line, fn, name)
			noCRLF(line, fn, val)
			req.Header.Set(name, val)
		}
	}

	resp, err := newHTTPClient(in.served).Do(req)
	if err != nil {
		msg := err.Error()
		if e, ok := err.(*url.Error); ok {
			msg = e.Err.Error()
		}
		if i := strings.Index(msg, "endereço interno bloqueado"); i >= 0 {
			msg = msg[i:]
		}
		return clientResult(false, 0, "", newMap(), msg)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, clientMaxBody+1))
	if err != nil {
		return clientResult(false, resp.StatusCode, "", newMap(), err.Error())
	}
	if len(data) > clientMaxBody {
		return clientResult(false, resp.StatusCode, "", newMap(), "resposta grande demais (máximo 5 MB)")
	}
	headers := newMap()
	for k, v := range resp.Header {
		headers.set(strings.ToLower(k), strings.Join(v, ", "))
	}
	return clientResult(resp.StatusCode >= 200 && resp.StatusCode < 300, resp.StatusCode, string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))), headers, "")
}

func init() {
	ns := nativeNS("Http")
	for _, m := range []string{"get", "post", "put", "delete"} {
		method, fn := strings.ToUpper(m), "Http."+m
		ns[m] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
			if len(a) == 0 {
				fail(line, "%s espera a URL", fn)
			}
			return in.httpDo(method, fn, a, line)
		}}
	}
}
