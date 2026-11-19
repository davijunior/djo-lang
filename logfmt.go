//go:build !noweb

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"
)

// Log do servidor: formato (text ou json), IP (off, trunc, full), ID de requisição e depurador de segredos.
//
// O log JSON só tem campos de uma lista fechada (reqLogLine): não aceita mapa livre, então cabeçalho, cookie,
// corpo, query e valores de sessão nunca entram. O texto livre (a mensagem de erro, a saída dos jobs) passa
// pelo depurador (scrubLog), que tira chaves, senhas de URL, "Bearer ..." e valores de variáveis secretas.
// O que o código do usuário imprime com `print` NÃO é depurado.

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

func newRequestID() string { return "rq_" + randHex(8) }

// reqLogLine é a linha de log de uma requisição (campos fixos).
type reqLogLine struct {
	Ts        string `json:"ts"`
	Level     string `json:"level"`
	RequestID string `json:"request_id,omitempty"`
	Method    string `json:"method,omitempty"`
	Route     string `json:"route,omitempty"`
	Status    int    `json:"status,omitempty"`
	MS        int64  `json:"ms,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	IP        string `json:"ip,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	Msg       string `json:"msg,omitempty"`
	Stack     string `json:"stack,omitempty"`
}

func (s *Server) logJSON(l reqLogLine) {
	if s.logger == nil {
		return
	}
	l.Ts = time.Now().UTC().Format(time.RFC3339Nano)
	if l.Level == "" {
		l.Level = "info"
	}
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	s.logger.Print(string(b))
}

// logIPOf devolve o IP do log conforme -log-ip ("" se off). trunc zera o último byte (IPv4) ou tudo depois do /48 (IPv6).
func logIPOf(ip, mode string) string {
	switch mode {
	case "off":
		return ""
	case "full":
		return ip
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	a = a.Unmap()
	bits := 24
	if a.Is6() {
		bits = 48
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.Addr().String()
}

// ---- depurador ----

var scrubRes = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`), "Bearer ***"},
	{regexp.MustCompile(`djo1\.[0-9a-f]{8}\.[A-Za-z0-9_-]+`), "djo1.***"},
	{regexp.MustCompile(`(://[^/\s:@]+):[^/\s@]+@`), "$1:***@"},
	{regexp.MustCompile(`(?i)\b(token|password|senha|secret|api[_-]?key|app_secret|app_key|authorization)(\\?["']?\s*[:=]\s*\\?["']?)[^\s"',&}\\]+`), "$1$2***"},
}

var secretEnvRe = regexp.MustCompile(`(?i)(KEY|SECRET|TOKEN|PASS|PWD|DSN|CREDENTIAL)`)

// secretValues lê, do ambiente, os valores que nunca devem aparecer no log.
func secretValues() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if len(v) >= 6 && (strings.HasPrefix(k, cryptoPrefix) || secretEnvRe.MatchString(k)) {
			out = append(out, v)
		}
	}
	return out
}

func scrubLogReal(s string) string {
	for _, v := range secretValues() {
		s = strings.ReplaceAll(s, v, "***")
	}
	for _, r := range scrubRes {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// scrubLog é trocável só para o teste de mutação (que prova que o teste enxerga um vazamento).
var scrubLog = scrubLogReal

// scrubWriter passa cada linha de log pelo depurador.
type scrubWriter struct{ w io.Writer }

func (s scrubWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(s.w, scrubLog(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// logConfig junta flag e ambiente (o flag vence) e confere os valores.
func logConfig(format, ip string) (string, string, error) {
	if format == "" {
		format = os.Getenv("DJO_LOG_FORMAT")
	}
	if ip == "" {
		ip = os.Getenv("DJO_LOG_IP")
	}
	format, ip = strings.TrimSpace(format), strings.TrimSpace(ip)
	if format == "" {
		format = "text"
	}
	if ip == "" {
		ip = "trunc"
	}
	if format != "text" && format != "json" {
		return "", "", fmt.Errorf("-log-format é text ou json (recebeu %q)", format)
	}
	if ip != "off" && ip != "trunc" && ip != "full" {
		return "", "", fmt.Errorf("-log-ip é off, trunc ou full (recebeu %q)", ip)
	}
	return format, ip, nil
}
