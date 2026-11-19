//go:build !nonet

package main

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// httpMockServe atende uma chamada Http.* pelos mocks. Devolve o resultado (como o Http de verdade).
func (in *Interp) httpMockServe(set *httpMockSet, method, fn, rawURL string, u *url.URL, hdr http.Header, body string, timeout time.Duration, line int) any {
	var names []string
	for k := range hdr {
		names = append(names, k)
	}
	sort.Strings(names)
	var hl [][2]string
	for _, k := range names {
		hl = append(hl, [2]string{strings.ToLower(k), strings.Join(hdr[k], ", ")})
	}
	full := u.String()

	set.mu.Lock()
	if len(set.calls) < httpMockMaxCalls {
		set.calls = append(set.calls, httpCallRec{method: method, url: full, body: body, headers: hl})
	}
	var resp *httpMockResp
	for _, r := range set.rules {
		if r.method != method || !r.matchesBody(body) {
			continue
		}
		if (r.prefix && (strings.HasPrefix(full, r.url) || strings.HasPrefix(rawURL, r.url))) || (!r.prefix && r.url == full) {
			i := r.next
			if i >= len(r.resp) {
				i = len(r.resp) - 1
			}
			r.next++
			resp = r.resp[i]
			break
		}
	}
	set.mu.Unlock()
	if resp == nil {
		fail(line, "%s: sem mock para %s %s neste spec (a chamada não sai pela rede; use Spec.http_mock)", fn, method, redactURL(full))
	}

	elapsed := resp.delay
	timedOut := resp.timeout
	if elapsed >= timeout && !timedOut {
		timedOut = true // o atraso simulado passou do timeout desta chamada
	}
	if timedOut {
		elapsed = timeout
	}
	if in.served {
		in.outWaited += elapsed
	}
	switch {
	case timedOut:
		return clientResult(false, 0, "", newMap(), "tempo esgotado ("+fmtSecs(timeout)+")")
	case resp.errMsg != "":
		return clientResult(false, 0, "", newMap(), resp.errMsg)
	}
	return clientResult(resp.status >= 200 && resp.status < 300, resp.status, resp.body, maskedHeaders(resp.headers, false), "")
}
