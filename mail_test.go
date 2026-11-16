//go:build !nonet

package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- servidor SMTP falso em loopback ----

type fakeMail struct {
	mu                       sync.Mutex
	from                     string
	rcpt                     []string
	data                     string
	auth                     string
	conns                    int
	tlsOK                    bool
	offerTLS, implicit, hang bool
	user, pass               string
	rejectRcpt               string
	loginOnly                bool
	ln                       net.Listener
	cfg                      *tls.Config
	addr                     string
	port                     string
}

func testCert(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "djo-test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, pool
}

func startFake(t *testing.T, f *fakeMail) *fakeMail {
	t.Helper()
	cfg, pool := testCert(t)
	f.cfg = cfg
	mailRoots = pool
	t.Cleanup(func() { mailRoots = nil })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	f.addr = ln.Addr().String()
	_, f.port, _ = net.SplitHostPort(f.addr)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeMail) serve(c net.Conn) {
	defer c.Close()
	f.mu.Lock()
	f.conns++
	f.mu.Unlock()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if f.implicit {
		tc := tls.Server(c, f.cfg)
		if tc.Handshake() != nil {
			return
		}
		c = tc
		f.mu.Lock()
		f.tlsOK = true
		f.mu.Unlock()
	}
	r := bufio.NewReader(c)
	say := func(s string) { c.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	secure := f.implicit
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case f.hang && strings.HasPrefix(up, "MAIL"):
			time.Sleep(3 * time.Second)
			return
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			say("250-fake")
			if f.offerTLS && !secure {
				say("250-STARTTLS")
			}
			if f.user != "" && (secure || !f.offerTLS) {
				if f.loginOnly {
					say("250-AUTH LOGIN")
				} else {
					say("250-AUTH PLAIN LOGIN")
				}
			}
			say("250 8BITMIME")
		case up == "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(c, f.cfg)
			if tc.Handshake() != nil {
				return
			}
			c, r, secure = tc, bufio.NewReader(tc), true
			f.mu.Lock()
			f.tlsOK = true
			f.mu.Unlock()
			say = func(s string) { c.Write([]byte(s + "\r\n")) }
		case strings.HasPrefix(up, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			f.mu.Lock()
			f.auth = strings.ReplaceAll(string(raw), "\x00", "|")
			f.mu.Unlock()
			if string(raw) == "\x00"+f.user+"\x00"+f.pass {
				say("235 ok")
			} else {
				say("535 5.7.8 credenciais ruins")
			}
		case up == "AUTH LOGIN":
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, _ := r.ReadString('\n')
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			p, _ := r.ReadString('\n')
			ub, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
			pb, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
			f.mu.Lock()
			f.auth = "login|" + string(ub) + "|" + string(pb)
			f.mu.Unlock()
			if string(ub) == f.user && string(pb) == f.pass {
				say("235 ok")
			} else {
				say("535 5.7.8 credenciais ruins")
			}
		case strings.HasPrefix(up, "MAIL FROM:"):
			f.mu.Lock()
			f.from = strings.Trim(strings.Fields(line[len("MAIL FROM:"):])[0], "<> ")
			f.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			a := strings.Trim(line[len("RCPT TO:"):], "<> ")
			if f.rejectRcpt != "" && a == f.rejectRcpt {
				say("550 5.1.1 sem caixa")
				continue
			}
			f.mu.Lock()
			f.rcpt = append(f.rcpt, a)
			f.mu.Unlock()
			say("250 ok")
		case up == "DATA":
			say("354 go")
			var b bytes.Buffer
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			say("250 queued")
		case up == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

type fakeSnap struct {
	from, data, auth string
	rcpt             []string
	conns            int
	tlsOK            bool
}

func (f *fakeMail) get() fakeSnap {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeSnap{f.from, f.data, f.auth, append([]string(nil), f.rcpt...), f.conns, f.tlsOK}
}

func mailEnvSet(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"DJO_SMTP_HOST", "DJO_SMTP_PORT", "DJO_SMTP_USER", "DJO_SMTP_PASS", "DJO_SMTP_FROM", "DJO_SMTP_SECURE"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func mailRunSrc(t *testing.T, root string, host *mailHost, src string) (string, error) {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	in := NewInterp(&sb)
	in.root = root
	in.mail = host
	err = in.Run(prog)
	return sb.String(), err
}

const mailSimple = `var r = Mail.send:{"to": "ana@x.com", "subject": "Oi", "text": "Olá, Ana"}
print r["ok"]
print r["sent"]
print r["error"] ?? ""
`

func TestMailSTARTTLSeAuth(t *testing.T) {
	f := startFake(t, &fakeMail{offerTLS: true, user: "u1", pass: "segredo-123"})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_USER": "u1", "DJO_SMTP_PASS": "segredo-123", "DJO_SMTP_FROM": "Site <site@x.com>"})
	out, err := mailRunSrc(t, t.TempDir(), nil, `var r = Mail.send:{"to": ["ana@x.com", "Bia <bia@x.com>"], "cc": "cc@x.com", "bcc": "oculto@x.com", "subject": "Olá — ação", "text": "Corpo em texto\nlinha 2 é", "html": "<p>Corpo <b>html</b></p>", "reply_to": "resp@x.com", "from": "Equipe do Site"}
print r["ok"]
print r["sent"]
print r["id"] != nil
`)
	if err != nil || out != "true\ntrue\ntrue\n" {
		t.Fatalf("%q %v", out, err)
	}
	if !f.get().tlsOK || f.get().from != "site@x.com" || strings.Join(f.get().rcpt, ",") != "ana@x.com,bia@x.com,cc@x.com,oculto@x.com" {
		t.Errorf("tls=%v from=%q rcpt=%v", f.get().tlsOK, f.get().from, f.get().rcpt)
	}
	if f.get().auth != "|u1|segredo-123" {
		t.Errorf("auth %q", f.get().auth)
	}
	d := f.get().data
	for _, want := range []string{"From: \"Equipe do Site\" <site@x.com>\r\n", "To: <ana@x.com>, \"Bia\" <bia@x.com>\r\n", "Cc: <cc@x.com>\r\n", "Reply-To: <resp@x.com>\r\n",
		"Subject: =?utf-8?q?Ol=C3=A1_=E2=80=94_a=C3=A7=C3=A3o?=\r\n", "Message-ID: <", "@x.com>\r\n", "Date: ", "MIME-Version: 1.0\r\n", "multipart/alternative; boundary=",
		"Content-Type: text/plain; charset=\"utf-8\"", "Content-Type: text/html; charset=\"utf-8\"", "<p>Corpo <b>html</b></p>", "linha 2 =C3=A9"} {
		if !strings.Contains(d, want) {
			t.Errorf("falta %q em:\n%s", want, d)
		}
	}
	if strings.Contains(d, "oculto@x.com") || strings.Contains(strings.ToLower(d), "bcc") {
		t.Errorf("o bcc vazou nos cabeçalhos:\n%s", d)
	}
	if strings.Index(d, "text/plain") > strings.Index(d, "text/html") {
		t.Error("o texto vem antes do html")
	}
}

func TestMailTextoSimplesSemMultipart(t *testing.T) {
	f := startFake(t, &fakeMail{offerTLS: true})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_FROM": "site@x.com"})
	out, err := mailRunSrc(t, t.TempDir(), nil, mailSimple)
	if err != nil || out != "true\ntrue\n\n" {
		t.Fatalf("%q %v", out, err)
	}
	if strings.Contains(f.get().data, "multipart") || !strings.Contains(f.get().data, "Content-Type: text/plain; charset=\"utf-8\"") || !strings.Contains(f.get().data, "Ol=C3=A1, Ana") {
		t.Errorf("%s", f.get().data)
	}
}

func TestMailTLSImplicito(t *testing.T) {
	f := startFake(t, &fakeMail{implicit: true, user: "u", pass: "pass1234"})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_SECURE": "tls", "DJO_SMTP_USER": "u", "DJO_SMTP_PASS": "pass1234", "DJO_SMTP_FROM": "site@x.com"})
	out, err := mailRunSrc(t, t.TempDir(), nil, mailSimple)
	if err != nil || out != "true\ntrue\n\n" || !f.get().tlsOK {
		t.Fatalf("%q %v tls=%v", out, err, f.get().tlsOK)
	}
}

func TestMailAuthLoginQuandoSoHaLogin(t *testing.T) {
	f := startFake(t, &fakeMail{offerTLS: true, user: "u", pass: "pass1234", loginOnly: true})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_USER": "u", "DJO_SMTP_PASS": "pass1234", "DJO_SMTP_FROM": "site@x.com"})
	out, err := mailRunSrc(t, t.TempDir(), nil, mailSimple)
	if err != nil || out != "true\ntrue\n\n" || f.get().auth != "login|u|pass1234" {
		t.Fatalf("%q %v auth=%q", out, err, f.get().auth)
	}
}

func TestMailFalhasNaoDerrubamEnaoVazamSenha(t *testing.T) {
	// senha errada
	f := startFake(t, &fakeMail{offerTLS: true, user: "u", pass: "certa-123"})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_USER": "u", "DJO_SMTP_PASS": "errada-999", "DJO_SMTP_FROM": "site@x.com"})
	out, err := mailRunSrc(t, t.TempDir(), nil, mailSimple)
	if err != nil || !strings.HasPrefix(out, "false\nfalse\nsmtp: autenticação recusada") || strings.Contains(out, "errada-999") || strings.Contains(out, "certa-123") {
		t.Fatalf("%q %v", out, err)
	}
	// destinatário recusado
	f2 := startFake(t, &fakeMail{offerTLS: true, rejectRcpt: "ana@x.com"})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f2.port, "DJO_SMTP_FROM": "site@x.com"})
	if out, err = mailRunSrc(t, t.TempDir(), nil, mailSimple); err != nil || !strings.Contains(out, "destinatário recusado") {
		t.Fatalf("%q %v", out, err)
	}
	// ninguém ouvindo
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": port, "DJO_SMTP_FROM": "site@x.com"})
	if out, err = mailRunSrc(t, t.TempDir(), nil, mailSimple); err != nil || !strings.HasPrefix(out, "false\nfalse\nsmtp: não consegui conectar") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestMailSemSTARTTLSNaoEnviaTextoPuro(t *testing.T) {
	f := startFake(t, &fakeMail{offerTLS: false})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_FROM": "site@x.com"})
	out, err := mailRunSrc(t, t.TempDir(), nil, mailSimple)
	if err != nil || !strings.Contains(out, "recusei enviar sem criptografia") || f.get().data != "" || f.get().from != "" {
		t.Fatalf("%q %v data=%q", out, err, f.get().data)
	}
}

func TestMailCertificadoNaoConfiavelEhRecusado(t *testing.T) {
	f := startFake(t, &fakeMail{offerTLS: true})
	mailRoots = nil // sem a CA de teste: o certificado não é de confiança
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_FROM": "site@x.com"})
	out, err := mailRunSrc(t, t.TempDir(), nil, mailSimple)
	if err != nil || !strings.HasPrefix(out, "false\nfalse\nsmtp: STARTTLS") || f.get().data != "" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestMailSecureNoneSoEmLoopback(t *testing.T) {
	f := startFake(t, &fakeMail{offerTLS: false})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_SECURE": "none", "DJO_SMTP_FROM": "site@x.com"})
	out, err := mailRunSrc(t, t.TempDir(), nil, mailSimple)
	if err != nil || out != "true\ntrue\n\n" || f.get().tlsOK {
		t.Fatalf("loopback: %q %v", out, err)
	}
	for _, h := range []string{"smtp.exemplo.com", "10.0.0.5", "mail.localhost.evil.com"} {
		mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": h, "DJO_SMTP_SECURE": "none", "DJO_SMTP_FROM": "site@x.com"})
		if _, err := mailRunSrc(t, t.TempDir(), nil, mailSimple); err == nil || !strings.Contains(err.Error(), "só vale para o próprio computador") {
			t.Errorf("%s com none deveria ser erro: %v", h, err)
		}
	}
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_SECURE": "talvez", "DJO_SMTP_FROM": "site@x.com"})
	if _, err := mailRunSrc(t, t.TempDir(), nil, mailSimple); err == nil || !strings.Contains(err.Error(), "DJO_SMTP_SECURE") {
		t.Errorf("secure inválido: %v", err)
	}
}

func TestMailTimeout(t *testing.T) {
	old := mailTotalTime
	mailTotalTime = 700 * time.Millisecond
	defer func() { mailTotalTime = old }()
	f := startFake(t, &fakeMail{offerTLS: true, hang: true})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_FROM": "site@x.com"})
	t0 := time.Now()
	out, err := mailRunSrc(t, t.TempDir(), nil, mailSimple)
	if err != nil || !strings.HasPrefix(out, "false\nfalse\nsmtp:") || time.Since(t0) > 3*time.Second {
		t.Fatalf("%q %v em %v", out, err, time.Since(t0))
	}
}

func TestMailConfigFaltando(t *testing.T) {
	mailEnvSet(t, nil)
	if _, err := mailRunSrc(t, t.TempDir(), nil, mailSimple); err == nil || !strings.Contains(err.Error(), "DJO_SMTP_FROM") {
		t.Errorf("sem from: %v", err)
	}
	mailEnvSet(t, map[string]string{"DJO_SMTP_FROM": "site@x.com"})
	if _, err := mailRunSrc(t, t.TempDir(), nil, mailSimple); err == nil || !strings.Contains(err.Error(), "DJO_SMTP_HOST") {
		t.Errorf("sem host: %v", err)
	}
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "h", "DJO_SMTP_FROM": "site@x.com", "DJO_SMTP_PORT": "abc"})
	if _, err := mailRunSrc(t, t.TempDir(), nil, mailSimple); err == nil || !strings.Contains(err.Error(), "DJO_SMTP_PORT") {
		t.Errorf("porta: %v", err)
	}
}

func TestMailValidacaoEInjecao(t *testing.T) {
	f := startFake(t, &fakeMail{offerTLS: true})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_FROM": "site@x.com"})
	bad := map[string]string{
		"crlf no subject":     `{"to": "a@x.com", "subject": "Oi\nBcc: x@y.com", "text": "t"}`,
		"lf no subject":       `{"to": "a@x.com", "subject": "Oi\nX: y", "text": "t"}`,
		"crlf no to":          `{"to": "a@x.com\nBcc: x@y.com", "subject": "s", "text": "t"}`,
		"crlf em item do to":  `{"to": ["a@x.com", "b@x.com\nBcc: z@y.com"], "subject": "s", "text": "t"}`,
		"crlf no reply_to":    `{"to": "a@x.com", "reply_to": "r@x.com\nX: y", "subject": "s", "text": "t"}`,
		"crlf no from":        `{"to": "a@x.com", "from": "Eu\nBcc: z@y.com", "subject": "s", "text": "t"}`,
		"tab no subject":      `{"to": "a@x.com", "subject": "a\tb", "text": "t"}`,
		"endereço inválido":   `{"to": "isto não é e-mail", "subject": "s", "text": "t"}`,
		"dois endereços":      `{"to": "a@x.com, b@x.com", "subject": "s", "text": "t"}`,
		"endereço não ASCII":  `{"to": "ação@x.com", "subject": "s", "text": "t"}`,
		"sem to":              `{"subject": "s", "text": "t"}`,
		"to vazio":            `{"to": [], "subject": "s", "text": "t"}`,
		"sem subject":         `{"to": "a@x.com", "text": "t"}`,
		"sem text":            `{"to": "a@x.com", "subject": "s"}`,
		"só html":             `{"to": "a@x.com", "subject": "s", "html": "<p>x</p>"}`,
		"text número":         `{"to": "a@x.com", "subject": "s", "text": 5}`,
		"chave desconhecida":  `{"to": "a@x.com", "subject": "s", "text": "t", "attachments": []}`,
		"from com outro end.": `{"to": "a@x.com", "from": "Fulano <ceo@outro.com>", "subject": "s", "text": "t"}`,
		"subject enorme":      `{"to": "a@x.com", "subject": "` + strings.Repeat("a", 201) + `", "text": "t"}`,
		"corpo enorme":        `{"to": "a@x.com", "subject": "s", "text": "` + strings.Repeat("a", 1<<20+1) + `"}`,
	}
	for name, m := range bad {
		out, err := mailRunSrc(t, t.TempDir(), nil, "var r = Mail.send:"+m+"\nprint r[\"ok\"]\n")
		if err == nil {
			t.Errorf("%s: deveria dar erro (saída %q)", name, out)
		}
	}
	// destinatários demais
	var many []string
	for i := 0; i < 21; i++ {
		many = append(many, `"u`+string(rune('a'+i))+`@x.com"`)
	}
	if _, err := mailRunSrc(t, t.TempDir(), nil, `var r = Mail.send:{"to": [`+strings.Join(many, ",")+`], "subject": "s", "text": "t"}`); err == nil || !strings.Contains(err.Error(), "20 destinat") {
		t.Errorf("21 destinatários: %v", err)
	}
	if _, err := mailRunSrc(t, t.TempDir(), nil, `var r = Mail.send:5`); err == nil {
		t.Error("argumento que não é mapa")
	}
	if f.get().conns != 0 {
		t.Errorf("nada disso devia ter conectado no servidor (%d conexões)", f.get().conns)
	}
	// from com o mesmo endereço só troca o nome
	f2 := startFake(t, &fakeMail{offerTLS: true})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f2.port, "DJO_SMTP_FROM": "Site <site@x.com>"})
	out, err := mailRunSrc(t, t.TempDir(), nil, `var r = Mail.send:{"to": "a@x.com", "from": "Time <SITE@x.com>", "subject": "s", "text": "t"}
print r["ok"]`)
	if err != nil || out != "true\n" || !strings.Contains(f2.get().data, "From: \"Time\" <site@x.com>") || f2.get().from != "site@x.com" {
		t.Errorf("%q %v\n%s", out, err, f2.get().data)
	}
}

func TestMailLimitePorMinuto(t *testing.T) {
	old := mailPerMinute
	mailPerMinute = 2
	mailLimMu.Lock()
	mailSent = nil
	mailLimMu.Unlock()
	defer func() { mailPerMinute = old }()
	f := startFake(t, &fakeMail{offerTLS: true})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_FROM": "site@x.com"})
	var got []string
	for i := 0; i < 3; i++ {
		out, err := mailRunSrc(t, t.TempDir(), nil, mailSimple)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.SplitN(out, "\n", 2)[0])
	}
	if strings.Join(got, ",") != "true,true,false" {
		t.Errorf("%v", got)
	}
	mailLimMu.Lock()
	mailSent = nil
	mailLimMu.Unlock()
}

func TestMailDevGravaEml(t *testing.T) {
	mailEnvSet(t, nil) // sem nenhuma configuração: em dev funciona
	root := t.TempDir()
	out, err := mailRunSrc(t, root, &mailHost{dev: true}, `var r = Mail.send:{"to": "a@x.com", "bcc": "o@x.com", "subject": "Dev", "text": "oi"}
print r["ok"]
print r["sent"]
print r["file"]
`)
	if err != nil {
		t.Fatal(err)
	}
	ls := strings.Split(strings.TrimSpace(out), "\n")
	if len(ls) != 3 || ls[0] != "true" || ls[1] != "false" || !strings.HasPrefix(ls[2], "_saida/emails/") || !strings.HasSuffix(ls[2], ".eml") {
		t.Fatalf("%q", out)
	}
	b, err := os.ReadFile(filepath.Join(root, ls[2]))
	if err != nil || !strings.Contains(string(b), "Subject: Dev\r\n") || !strings.Contains(string(b), "To: <a@x.com>") {
		t.Fatalf("%v\n%s", err, b)
	}
	// e o djo build não empacota
	if !buildExcluded("_saida", dirEntryFor{isDir: true}, nil) {
		t.Error("o build não pode levar a pasta _saida")
	}
}

func TestMailDevNaoAbreConexao(t *testing.T) {
	f := startFake(t, &fakeMail{offerTLS: true})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": f.port, "DJO_SMTP_FROM": "site@x.com"})
	if _, err := mailRunSrc(t, t.TempDir(), &mailHost{dev: true}, mailSimple); err != nil {
		t.Fatal(err)
	}
	if f.get().conns != 0 {
		t.Error("em dev o e-mail não é enviado")
	}
	// com -mail-send (dev=false) envia de verdade
	if out, err := mailRunSrc(t, t.TempDir(), &mailHost{dev: false}, mailSimple); err != nil || out != "true\ntrue\n\n" || f.get().conns != 1 {
		t.Errorf("%q %v %d", out, err, f.get().conns)
	}
}

func TestMailCaptura(t *testing.T) {
	mailEnvSet(t, nil)
	box := &mailBox{}
	if _, err := mailRunSrc(t, t.TempDir(), &mailHost{box: box}, mailSimple); err != nil {
		t.Fatal(err)
	}
	l := box.list()
	if len(l.Items) != 1 {
		t.Fatalf("%d", len(l.Items))
	}
	m := l.Items[0].(*Map)
	if s, _ := m.get("subject"); s != "Oi" {
		t.Errorf("%v", s)
	}
}

// ---- dentro do djo spec ----

func TestMailNoSpec(t *testing.T) {
	startFake(t, &fakeMail{offerTLS: true})
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": "1", "DJO_SMTP_FROM": "site@x.com"})
	root := specProject(t, map[string]string{
		"core/envio.djo": `f avisar
    params[quem[String]]
    return Mail.send:{"to": quem, "subject": "Aviso", "text": "Olá " + quem, "html": "<b>oi</b>"}
`,
		"spec/envio_spec.djo": `f it_captura
    expect.eq:(Spec.emails:):[]
    var r = avisar:"ana@x.com"
    expect.be_true:r["ok"]
    expect.be_false:r["sent"]
    var es = Spec.emails:
    expect.len:es:1
    expect.eq:es[0]["to"]:["ana@x.com"]
    expect.eq:es[0]["subject"]:"Aviso"
    expect.eq:es[0]["html"]:"<b>oi</b>"
    expect.include:es[0]["raw"]:"multipart/alternative"

f it_zera_a_cada_spec
    expect.eq:(Spec.emails:):[]
    avisar:"bia@x.com"
    avisar:"cid@x.com"
    expect.len:(Spec.emails:):2
`,
	})
	code, out, errw := runSpec(t, root)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errw)
	}
	if !strings.Contains(out, "2 passaram") && !strings.Contains(out, "2 passed") && !strings.Contains(out, "..") {
		t.Errorf("%s", out)
	}
}

func TestMailControlesCRLFNUL(t *testing.T) {
	for _, s := range []string{"a\rb", "a\nb", "a\x00b", "a\x7fb", "a\x1bb", "a\r\nBcc: x@y.com"} {
		if !mailControl(s) {
			t.Errorf("%q deveria ser controle", s)
		}
		if _, err := mailAddr("a@x.com" + s); err == nil {
			t.Errorf("endereço com %q passou", s)
		}
	}
	if mailControl("Olá, ação — ok") {
		t.Error("acentos não são controle")
	}
}

func TestMailPermissaoDeBooster(t *testing.T) {
	if !restrictedNS["Mail"] {
		t.Fatal("Mail precisa ser uma permissão de booster")
	}
}
