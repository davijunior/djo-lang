//go:build !noweb

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
)

// Proxy confiável: atrás de um proxy reverso (nginx, Caddy...) que termina o HTTPS, a djo vê
// HTTP puro vindo do proxy. Só um proxy da lista pode dizer o que o visitante usou de verdade
// (X-Forwarded-Proto); cabeçalho vindo de qualquer outro endereço é ignorado, porque qualquer
// um pode escrevê-lo. A lista também é o ponto de partida para confiar em X-Forwarded-For / X-Real-IP.

// trustedProxies é definida uma vez na partida (configureTrustedProxy); o padrão é o loopback.
var trustedProxies = defaultTrustedProxies()

func defaultTrustedProxies() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
}

// parseTrustedProxies lê "ip[,cidr...]" ou "none". Vazio é o padrão (loopback).
// Recusa curinga ("all", "*"), prefixo /0 e prefixos muito largos (abaixo de /8 em IPv4 ou /16 em IPv6).
func parseTrustedProxies(s string) ([]netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultTrustedProxies(), nil
	}
	if strings.EqualFold(s, "none") {
		return nil, nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("proxy confiável: item vazio em %q", s)
		}
		if strings.EqualFold(part, "none") {
			return nil, fmt.Errorf("proxy confiável: 'none' não pode vir junto de outros valores")
		}
		var p netip.Prefix
		if strings.Contains(part, "/") {
			var err error
			if p, err = netip.ParsePrefix(part); err != nil {
				return nil, fmt.Errorf("proxy confiável: %q não é um CIDR válido", part)
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), unmappedBits(p)).Masked()
		} else {
			a, err := netip.ParseAddr(part)
			if err != nil {
				return nil, fmt.Errorf("proxy confiável: %q não é um IP nem um CIDR (use 'none' para desligar)", part)
			}
			a = a.Unmap().WithZone("")
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("proxy confiável: %q confiaria na internet inteira", part)
		}
		if (p.Addr().Is4() && p.Bits() < 8) || (p.Addr().Is6() && p.Bits() < 16) {
			return nil, fmt.Errorf("proxy confiável: %q é largo demais (mínimo /8 em IPv4 e /16 em IPv6)", part)
		}
		out = append(out, p)
	}
	return out, nil
}

// unmappedBits ajusta o prefixo de um endereço IPv4 escrito como ::ffff:a.b.c.d/N.
func unmappedBits(p netip.Prefix) int {
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		return p.Bits() - 96
	}
	return p.Bits()
}

// configureTrustedProxy define a lista na partida: a flag vence a variável DJO_TRUSTED_PROXY.
func configureTrustedProxy(flagValue string, flagSet bool) error {
	v := flagValue
	if !flagSet {
		v = os.Getenv("DJO_TRUSTED_PROXY")
	}
	list, err := parseTrustedProxies(v)
	if err != nil {
		return err
	}
	trustedProxies = list
	return nil
}

// fromTrustedProxy: a conexão veio de um proxy da lista? (usa o RemoteAddr, nunca um cabeçalho)
func fromTrustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap().WithZone("")
	for _, p := range trustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// requestIsHTTPS: o visitante falou HTTPS? TLS direto, ou um proxy confiável disse "https"
// em X-Forwarded-Proto. Só o primeiro valor conta e só "https" vale; "http", lixo e vazio não.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !fromTrustedProxy(r) {
		return false
	}
	first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(first), "https")
}
