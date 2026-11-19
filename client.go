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

	// timeout por chamada (último argumento opcional do Http.*): piso e tetos por contexto
	clientTimeoutMin    = 1 * time.Second
	clientTimeoutJob    = 60 * time.Second // job e djo spec/script
	clientTimeoutPage   = 20 * time.Second // página e action servidas
	clientPageWaitTotal = 25 * time.Second // soma das esperas de Http de uma página (o servidor corta em 30 s)
)

// fmtSecs: 8 s, 1,5 s.
func fmtSecs(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
	return strings.Replace(fmt.Sprintf("%.1f s", d.Seconds()), ".", ",", 1)
}

// httpTimeout escolhe o prazo desta chamada: o pedido (ou 8 s), dentro do teto do contexto, do que
// resta do prazo do job e do que resta da cota de espera da página.
func (in *Interp) httpTimeout(fn string, v any, given bool, line int) time.Duration {
	isJob := in.job != nil && in.job.inJob && in.spec == nil
	page := in.served && !isJob
	d := clientTimeout
	if given {
		switch x := v.(type) {
		case int64:
			d = time.Duration(x) * time.Second
		case string:
			var ok bool
			if d, ok = parseJobDur(x); !ok {
				fail(line, "%s: o timeout é um número de segundos ou um texto como \"30s\" ou \"2m\", mas recebeu %q", fn, x)
			}
		default:
			fail(line, "%s: o timeout é um número de segundos ou um texto como \"30s\", mas recebeu %s", fn, typeName(v))
		}
		max := clientTimeoutJob
		if page {
			max = clientTimeoutPage
		}
		if d < clientTimeoutMin {
			fail(line, "%s: o timeout mínimo é %s", fn, fmtSecs(clientTimeoutMin))
		}
		if d > max {
			fail(line, "%s: o timeout máximo aqui é %s", fn, fmtSecs(max))
		}
	}
	if !in.deadline.IsZero() && isJob {
		left := time.Until(in.deadline)
		if left <= 0 {
			failFatal(line, "o job passou do tempo limite (timeout) e foi interrompido")
		}
		if left < d {
			d = left
		}
	}
	if page {
		left := clientPageWaitTotal - in.outWaited
		if left <= 0 {
			failFatal(line, "%s: a página já esperou %s por chamadas externas (o máximo da soma)", fn, fmtSecs(clientPageWaitTotal))
		}
		if left < d {
			d = left
		}
	}
	return d
}

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

func newHTTPClient(restrict bool, timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: timeout}
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
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: timeout, MaxIdleConns: 0, DisableKeepAlives: true}
	if !restrict {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
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
	bodyStr, contentType := "", ""
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
			body, bodyStr, contentType = strings.NewReader(b), b, "text/plain; charset=utf-8"
		case *Map, *Array, *Instance:
			bodyStr = jsonEncode(b, "", line)
			body, contentType = strings.NewReader(bodyStr), "application/json"
		default:
			fail(line, "%s: o corpo é String ou dados (Map, Arr), mas recebeu %s", fn, typeName(a[1]))
		}
	}
	if len(a) > argHeaders+2 {
		fail(line, "%s recebeu argumentos demais", fn)
	}
	timeout := in.httpTimeout(fn, func() any {
		if len(a) > argHeaders+1 {
			return a[argHeaders+1]
		}
		return nil
	}(), len(a) > argHeaders+1, line)

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
	if len(a) > argHeaders && a[argHeaders] != nil {
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

	if in.httpMock.isArmed() { // djo spec com Spec.http_mock: nada sai pela rede
		return in.httpMockServe(in.httpMock, method, fn, rawURL, u, req.Header, bodyStr, timeout, line)
	}
	started := time.Now()
	resp, err := newHTTPClient(in.served, timeout).Do(req)
	elapsed := time.Since(started)
	if in.served {
		in.outWaited += elapsed
	}
	if err != nil {
		if in.job != nil && in.job.inJob && in.spec == nil && !in.deadline.IsZero() && !time.Now().Before(in.deadline) {
			failFatal(line, "o job passou do tempo limite (timeout) e foi interrompido (no meio de %s)", fn)
		}
		msg := err.Error()
		if e, ok := err.(*url.Error); ok {
			msg = e.Err.Error()
			if e.Timeout() {
				msg = "tempo esgotado (" + fmtSecs(timeout) + ")"
			}
		}
		if i := strings.Index(msg, "endereço interno bloqueado"); i >= 0 {
			msg = msg[i:]
			if in.httpMock != nil {
				msg += "; no djo spec use Spec.http_mock para simular a resposta"
			}
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
