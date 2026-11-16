//go:build !nonet

package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mail.send:{to, subject, text, ...}: e-mail por SMTP, sem dependência de fora.
//
//	var r = Mail.send:{"to": "ana@x.com", "subject": "Oi", "text": "Olá", "html": "<p>Olá</p>"}
//	r["ok"]  r["id"]  r["sent"]  r["file"]  r["error"]
//
// A configuração vem só do ambiente do processo e nunca vira valor da linguagem:
// DJO_SMTP_HOST, DJO_SMTP_PORT (587), DJO_SMTP_USER, DJO_SMTP_PASS, DJO_SMTP_FROM e
// DJO_SMTP_SECURE ("" = automático, tls, starttls ou none, este só para localhost).
// Porta 465 usa TLS implícito; as outras exigem STARTTLS; o certificado é sempre verificado.

const hasMail = true

const (
	mailMaxRcpt    = 20
	mailMaxSubject = 200
	mailMaxBody    = 1 << 20
	mailMaxConc    = 4
)

var (
	mailDialTime  = 10 * time.Second // os testes encurtam
	mailTotalTime = 30 * time.Second
)

var (
	mailPerMinute = 30           // envios de verdade por minuto, por processo
	mailRoots     *x509.CertPool // só os testes mudam: confiar numa CA de teste
	mailLimMu     sync.Mutex
	mailSent      []time.Time
	mailSem       = make(chan struct{}, mailMaxConc)
	mailKeys      = map[string]bool{"to": true, "cc": true, "bcc": true, "subject": true, "text": true, "html": true, "reply_to": true, "from": true}
)

type mailConf struct {
	host, port, user, pass, secure string
	from                           mail.Address
}

func mailConfig(forReal bool, line int) *mailConf {
	c := &mailConf{host: os.Getenv("DJO_SMTP_HOST"), port: os.Getenv("DJO_SMTP_PORT"), user: os.Getenv("DJO_SMTP_USER"),
		pass: os.Getenv("DJO_SMTP_PASS"), secure: strings.ToLower(strings.TrimSpace(os.Getenv("DJO_SMTP_SECURE")))}
	if c.port == "" {
		c.port = "587"
	}
	if n, err := strconv.Atoi(c.port); err != nil || n < 1 || n > 65535 {
		fail(line, "Mail.send: DJO_SMTP_PORT inválida")
	}
	switch c.secure {
	case "", "tls", "starttls":
	case "none":
		if !mailLoopback(c.host) {
			fail(line, "Mail.send: DJO_SMTP_SECURE=none só vale para o próprio computador (localhost); para outros servidores o envio precisa de TLS")
		}
	default:
		fail(line, "Mail.send: DJO_SMTP_SECURE precisa ser tls, starttls ou none")
	}
	if f := strings.TrimSpace(os.Getenv("DJO_SMTP_FROM")); f != "" {
		a, err := mailAddr(f)
		if err != nil {
			fail(line, "Mail.send: DJO_SMTP_FROM inválido (%v)", err)
		}
		c.from = *a
	} else if forReal {
		fail(line, "Mail.send: falta DJO_SMTP_FROM no ambiente (o endereço de quem envia)")
	} else {
		c.from = mail.Address{Address: "noreply@localhost"}
	}
	if forReal && c.host == "" {
		fail(line, "Mail.send: falta DJO_SMTP_HOST no ambiente (o servidor SMTP)")
	}
	return c
}

func mailLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func mailControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// mailAddr valida um endereço ("a@b.com" ou "Nome <a@b.com>"). Só ASCII no endereço; o nome pode ter acento.
func mailAddr(s string) (*mail.Address, error) {
	if mailControl(s) {
		return nil, errors.New("tem caractere de controle ou quebra de linha")
	}
	a, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("endereço inválido")
	}
	for i := 0; i < len(a.Address); i++ {
		if a.Address[i] > 0x7e || a.Address[i] <= 0x20 {
			return nil, errors.New("o endereço precisa ser só ASCII, sem espaços")
		}
	}
	if len(a.Address) > 254 || strings.Count(a.Address, "@") != 1 {
		return nil, errors.New("endereço inválido")
	}
	return a, nil
}

func mailList(v any, key string, line int) []*mail.Address {
	if v == nil {
		return nil
	}
	var raw []string
	switch x := v.(type) {
	case string:
		raw = []string{x}
	case *Array:
		for _, it := range x.Items {
			s, ok := it.(string)
			if !ok {
				fail(line, "Mail.send: '%s' é um endereço ou uma lista de endereços (texto)", key)
			}
			raw = append(raw, s)
		}
	default:
		fail(line, "Mail.send: '%s' é um endereço ou uma lista de endereços (texto)", key)
	}
	var out []*mail.Address
	for _, r := range raw {
		a, err := mailAddr(r)
		if err != nil {
			fail(line, "Mail.send: '%s': %v", key, err)
		}
		out = append(out, a)
	}
	return out
}

func mailStr(m *Map, key string, line int, required bool) (string, bool) {
	v, ok := m.get(key)
	if !ok || v == nil {
		if required {
			fail(line, "Mail.send: falta '%s'", key)
		}
		return "", false
	}
	s, isStr := v.(string)
	if !isStr {
		fail(line, "Mail.send: '%s' precisa ser um texto, mas recebeu %s", key, typeName(v))
	}
	return s, true
}

type mailMsg struct {
	to, cc, bcc      []*mail.Address
	subject, text    string
	html, replyTo    string
	hasHTML, hasRepl bool
	from             mail.Address
	id               string
	raw              []byte
}

func mailRandom(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func mailBuild(m *Map, conf *mailConf, line int) *mailMsg {
	for _, k := range m.Keys {
		ks, _ := k.(string)
		if !mailKeys[ks] {
			fail(line, "Mail.send: a chave '%s' não existe (valem: to, cc, bcc, subject, text, html, reply_to, from)", ks)
		}
	}
	g := func(k string) any { v, _ := m.get(k); return v }
	msg := &mailMsg{to: mailList(g("to"), "to", line), cc: mailList(g("cc"), "cc", line), bcc: mailList(g("bcc"), "bcc", line)}
	if len(msg.to) == 0 {
		fail(line, "Mail.send: falta 'to' (um endereço ou uma lista)")
	}
	if n := len(msg.to) + len(msg.cc) + len(msg.bcc); n > mailMaxRcpt {
		fail(line, "Mail.send: no máximo %d destinatários por envio (to, cc e bcc somados)", mailMaxRcpt)
	}
	msg.subject, _ = mailStr(m, "subject", line, true)
	if msg.subject == "" || len(msg.subject) > mailMaxSubject || mailControl(msg.subject) {
		fail(line, "Mail.send: 'subject' precisa ter de 1 a %d bytes, numa linha só, sem caracteres de controle", mailMaxSubject)
	}
	msg.text, _ = mailStr(m, "text", line, true)
	if msg.text == "" {
		fail(line, "Mail.send: 'text' é obrigatório (a versão em texto puro, mesmo quando há 'html')")
	}
	msg.html, msg.hasHTML = mailStr(m, "html", line, false)
	if len(msg.text)+len(msg.html) > mailMaxBody {
		fail(line, "Mail.send: texto e html passam de %d bytes", mailMaxBody)
	}
	if s, ok := mailStr(m, "reply_to", line, false); ok {
		a, err := mailAddr(s)
		if err != nil {
			fail(line, "Mail.send: 'reply_to': %v", err)
		}
		msg.replyTo, msg.hasRepl = a.String(), true
	}
	msg.from = conf.from
	if s, ok := mailStr(m, "from", line, false); ok {
		if mailControl(s) {
			fail(line, "Mail.send: 'from' tem caractere de controle ou quebra de linha")
		}
		name := strings.TrimSpace(s)
		if strings.ContainsAny(s, "<>@") {
			a, err := mailAddr(s)
			if err != nil {
				fail(line, "Mail.send: 'from': %v", err)
			}
			if !strings.EqualFold(a.Address, conf.from.Address) {
				fail(line, "Mail.send: o remetente é sempre o configurado em DJO_SMTP_FROM; 'from' só muda o nome exibido")
			}
			name = a.Name
		}
		msg.from.Name = name
	}
	dom := conf.from.Address[strings.LastIndex(conf.from.Address, "@")+1:]
	msg.id = "<" + mailRandom(16) + "@" + dom + ">"

	var h strings.Builder
	w := func(k, v string) { h.WriteString(k + ": " + v + "\r\n") }
	w("Date", time.Now().Format(time.RFC1123Z))
	w("From", msg.from.String())
	w("To", mailJoin(msg.to))
	if len(msg.cc) > 0 {
		w("Cc", mailJoin(msg.cc))
	}
	if msg.hasRepl {
		w("Reply-To", msg.replyTo)
	}
	w("Subject", mime.QEncoding.Encode("utf-8", msg.subject))
	w("Message-ID", msg.id)
	w("MIME-Version", "1.0")
	if !msg.hasHTML {
		w("Content-Type", `text/plain; charset="utf-8"`)
		w("Content-Transfer-Encoding", "quoted-printable")
		h.WriteString("\r\n")
		h.WriteString(mailQP(msg.text))
	} else {
		bd := "djo-" + mailRandom(12)
		w("Content-Type", `multipart/alternative; boundary="`+bd+`"`)
		h.WriteString("\r\n")
		for _, p := range []struct{ typ, body string }{{"text/plain", msg.text}, {"text/html", msg.html}} {
			h.WriteString("--" + bd + "\r\nContent-Type: " + p.typ + "; charset=\"utf-8\"\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
			h.WriteString(mailQP(p.body))
			h.WriteString("\r\n")
		}
		h.WriteString("--" + bd + "--\r\n")
	}
	msg.raw = []byte(h.String())
	return msg
}

func mailJoin(as []*mail.Address) string {
	var p []string
	for _, a := range as {
		p = append(p, a.String())
	}
	return strings.Join(p, ", ")
}

func mailQP(s string) string {
	var b strings.Builder
	w := quotedprintable.NewWriter(&b)
	w.Write([]byte(s))
	w.Close()
	return b.String()
}

func mailAddrs(as []*mail.Address) *Array {
	out := &Array{}
	for _, a := range as {
		out.Items = append(out.Items, a.Address)
	}
	return out
}

func mailCaptured(msg *mailMsg) *Map {
	m := newMap()
	m.set("to", mailAddrs(msg.to))
	m.set("cc", mailAddrs(msg.cc))
	m.set("bcc", mailAddrs(msg.bcc))
	m.set("subject", msg.subject)
	m.set("text", msg.text)
	if msg.hasHTML {
		m.set("html", msg.html)
	} else {
		m.set("html", nil)
	}
	if msg.hasRepl {
		m.set("reply_to", msg.replyTo)
	} else {
		m.set("reply_to", nil)
	}
	m.set("from", msg.from.String())
	m.set("id", msg.id)
	m.set("raw", string(msg.raw))
	return m
}

func mailResult(ok bool, id string, sent bool, errMsg string) *Map {
	m := newMap()
	m.set("ok", ok)
	if id != "" {
		m.set("id", id)
	}
	m.set("sent", sent)
	if errMsg != "" {
		m.set("error", errMsg)
	}
	return m
}

// mailLimit: no máximo mailPerMinute envios de verdade por minuto e mailMaxConc ao mesmo tempo.
func mailLimit() (release func(), msg string) {
	mailLimMu.Lock()
	now := time.Now()
	keep := mailSent[:0]
	for _, t := range mailSent {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	mailSent = keep
	if len(mailSent) >= mailPerMinute {
		mailLimMu.Unlock()
		return nil, "limite de envios por minuto atingido"
	}
	select {
	case mailSem <- struct{}{}:
	default:
		mailLimMu.Unlock()
		return nil, "envios simultâneos demais, tente de novo"
	}
	mailSent = append(mailSent, now)
	mailLimMu.Unlock()
	return func() { <-mailSem }, ""
}

func (c *mailConf) redact(s string) string {
	for _, secret := range []string{c.pass, c.user} {
		if len(secret) >= 3 {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	return s
}

type loginAuth struct{ user, pass string }

func (a *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }
func (a *loginAuth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(from))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.pass), nil
	}
	return nil, errors.New("resposta inesperada do servidor no AUTH LOGIN")
}

func mailSend(conf *mailConf, msg *mailMsg) (err error) {
	addr := net.JoinHostPort(conf.host, conf.port)
	tlsConf := &tls.Config{ServerName: conf.host, MinVersion: tls.VersionTLS12, RootCAs: mailRoots}
	implicit := conf.secure == "tls" || (conf.secure == "" && conf.port == "465")
	conn, err := (&net.Dialer{Timeout: mailDialTime}).Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("não consegui conectar em %s", addr)
	}
	conn.SetDeadline(time.Now().Add(mailTotalTime))
	if implicit {
		tc := tls.Client(conn, tlsConf)
		if err := tc.Handshake(); err != nil {
			conn.Close()
			return fmt.Errorf("TLS: %v", err)
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, conf.host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("o servidor não respondeu como SMTP: %v", err)
	}
	defer c.Close()
	if !implicit && conf.secure != "none" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("o servidor não oferece STARTTLS; recusei enviar sem criptografia")
		}
		if err := c.StartTLS(tlsConf); err != nil {
			return fmt.Errorf("STARTTLS: %v", err)
		}
	}
	if conf.user != "" {
		ok, mechs := c.Extension("AUTH")
		if !ok {
			return errors.New("o servidor não oferece autenticação (AUTH)")
		}
		var auth smtp.Auth
		if strings.Contains(" "+strings.ToUpper(mechs)+" ", " PLAIN ") || !strings.Contains(strings.ToUpper(mechs), "LOGIN") {
			auth = &plainAuth{conf.user, conf.pass}
		} else {
			auth = &loginAuth{conf.user, conf.pass}
		}
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("autenticação recusada: %v", err)
		}
	}
	if err := c.Mail(conf.from.Address); err != nil {
		return fmt.Errorf("remetente recusado: %v", err)
	}
	for _, grp := range [][]*mail.Address{msg.to, msg.cc, msg.bcc} {
		for _, a := range grp {
			if err := c.Rcpt(a.Address); err != nil {
				return fmt.Errorf("destinatário recusado: %v", err)
			}
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %v", err)
	}
	if _, err := w.Write(msg.raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("o servidor recusou a mensagem: %v", err)
	}
	c.Quit()
	return nil
}

// plainAuth é o AUTH PLAIN sem a trava do net/smtp contra hosts que não são "localhost" (o envio já exige TLS antes daqui).
type plainAuth struct{ user, pass string }

func (a *plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("resposta inesperada do servidor")
	}
	return nil, nil
}

func mailWriteDev(root string, msg *mailMsg) (string, error) {
	dir := filepath.Join(root, "_saida", "emails")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := time.Now().Format("20060102-150405") + "-" + mailRandom(3) + ".eml"
	body := msg.raw
	if len(msg.bcc) > 0 {
		body = append([]byte("X-Djo-Dev-Bcc: "+mailJoin(msg.bcc)+"\r\n"), body...)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		return "", err
	}
	return "_saida/emails/" + name, nil
}

func init() {
	nativeNS("Mail")["send"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		m, ok := a[0].(*Map)
		if !ok {
			fail(line, "Mail.send espera um mapa {to, subject, text, ...}, mas recebeu %s", typeName(a[0]))
		}
		var box *mailBox
		dev := false
		switch {
		case in.spec != nil:
			box = in.spec.mails
		case in.mail != nil && in.mail.box != nil:
			box = in.mail.box
		case in.mail != nil && in.mail.dev:
			dev = true
		}
		real := box == nil && !dev
		conf := mailConfig(real, line)
		msg := mailBuild(m, conf, line)
		switch {
		case box != nil:
			box.add(mailCaptured(msg))
			return mailResult(true, msg.id, false, "")
		case dev:
			file, err := mailWriteDev(in.root, msg)
			if err != nil {
				return mailResult(false, msg.id, false, "não consegui gravar o e-mail de teste: "+err.Error())
			}
			fmt.Fprintf(os.Stderr, "djo: e-mail de desenvolvimento gravado em %s (não enviado; use -mail-send para enviar)\n", file)
			r := mailResult(true, msg.id, false, "")
			r.set("file", file)
			return r
		}
		release, lim := mailLimit()
		if release == nil {
			return mailResult(false, msg.id, false, lim)
		}
		defer release()
		if err := mailSend(conf, msg); err != nil {
			return mailResult(false, msg.id, false, "smtp: "+conf.redact(err.Error()))
		}
		fmt.Fprintf(os.Stderr, "djo: e-mail enviado, id %s, %d destinatário(s)\n", msg.id, len(msg.to)+len(msg.cc)+len(msg.bcc))
		return mailResult(true, msg.id, true, "")
	}}
}
