//go:build !nonet

package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const hasBoosterNet = true

// loopback: endereços da própria máquina, onde o http puro é aceito (testes e registro local).
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// httpGetLimited baixa um endereço https (ou http só na própria máquina), com limite de tamanho.
func httpGetLimited(raw string, limit int64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("endereço inválido: %s", raw)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname())) {
		return nil, fmt.Errorf("%s: só endereços https são aceitos (http só em localhost)", raw)
	}
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("redirecionamentos demais")
		}
		if r.URL.Scheme != "https" && !(r.URL.Scheme == "http" && loopback(r.URL.Hostname())) {
			return fmt.Errorf("o servidor redirecionou para um endereço sem https")
		}
		return nil
	}}
	req, _ := http.NewRequest(http.MethodGet, raw, nil)
	req.Header.Set("User-Agent", "djo-booster")
	res, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("não consegui baixar %s: %v", raw, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s respondeu %s", raw, res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: maior que o limite de %d MB", raw, limit>>20)
	}
	return data, nil
}
