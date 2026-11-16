//go:build !nonet

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// djo ask: faz uma pergunta em linguagem natural a um site que publica o /ask.
//
//	djo ask meusite.com "list posts from 2025-01-01 to 2025-03-30"
//	djo ask localhost:8080 "quantos posts"
//	djo ask --page 2 meusite.com "liste posts primeiros 5"
//	djo ask 'djo://meusite.com/ask="me da a lista de posts"'     (só açúcar: vira https://meusite.com/ask)
//
// Segurança: https é obrigatório fora de localhost (ou --insecure); o token (níveis agent/admin) vem de
// DJO_ASK_TOKEN, --token-file ou --token-stdin, nunca de um argumento (ps mostraria); redirecionamentos não são seguidos;
// o que o servidor responde é só TEXTO: nunca é executado e sai com os caracteres de controle do terminal
// (ESC, OSC, C1, bidi) escapados.
//
// Saída: 0 ok · 1 uso · 2 rede/servidor · 3 recusado, não entendi ou site sem /ask · 4 token recusado ou limite (401/429).

const (
	askCLIMaxBody  = 1 << 20
	askCLITokenEnv = "DJO_ASK_TOKEN"
)

var (
	askHostRe  = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:.]+\])(:[0-9]{1,5})?$`)
	askTokenRe = regexp.MustCompile(`^[\x21-\x7e]{1,1000}$`)
)

func askMain(args []string) int {
	return askCLI(args, os.Stdout, os.Stderr, os.Stdin, os.Getenv)
}

func askCLI(args []string, out, errw io.Writer, stdin io.Reader, getenv func(string) string) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(errw)
	asJSON := fs.Bool("json", false, "imprime a resposta do servidor como veio (JSON)")
	tokenFile := fs.String("token-file", "", "arquivo com o token (nível agent/admin)")
	tokenStdin := fs.Bool("token-stdin", false, "lê o token da primeira linha da entrada padrão")
	insecure := fs.Bool("insecure", false, "aceita http:// fora de localhost (o texto e o token viajam abertos)")
	timeout := fs.Duration("timeout", 15*time.Second, "tempo máximo")
	page := fs.Int("page", 0, "página dos resultados")
	fs.Usage = func() {
		fmt.Fprintln(errw, "uso: djo ask [--json] [--page N] [--token-file arq | --token-stdin] [--insecure] [--timeout 15s] site \"pergunta\"\n     djo ask 'djo://site/ask=\"pergunta\"'\n  o token vem de DJO_ASK_TOKEN, --token-file ou --token-stdin (nunca de um argumento)")
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return 1
	}
	target, question := rest[0], strings.TrimSpace(strings.Join(rest[1:], " "))
	if strings.HasPrefix(strings.ToLower(target), "djo://") {
		var q string
		var err error
		target, q, err = askParseDjoURL(target)
		if err != nil {
			fmt.Fprintln(errw, "djo ask:", err)
			return 1
		}
		if q != "" {
			if question != "" {
				fmt.Fprintln(errw, "djo ask: a pergunta veio duas vezes (no endereço e nos argumentos)")
				return 1
			}
			question = q
		}
	}
	if question == "" {
		fmt.Fprintln(errw, "djo ask: faltou a pergunta")
		fs.Usage()
		return 1
	}
	if *page < 0 || *page > 1_000_000 {
		fmt.Fprintln(errw, "djo ask: --page precisa ficar entre 1 e 1000000")
		return 1
	}
	endpoint, err := askEndpoint(target, *insecure)
	if err != nil {
		fmt.Fprintln(errw, "djo ask:", err)
		return 1
	}
	token, err := askReadToken(*tokenFile, *tokenStdin, stdin, getenv)
	if err != nil {
		fmt.Fprintln(errw, "djo ask:", err)
		return 1
	}

	body := map[string]any{"question": question}
	if *page > 0 {
		body["page"] = *page
	}
	payload, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "djo-ask")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{
		Timeout:       *timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, // nunca leva o token a outro lugar
	}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(errw, "djo ask: não consegui falar com %s (%s)\n", askSafe(req.URL.Host), askNetErr(err))
		return 2
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, askCLIMaxBody+1))
	if err != nil {
		fmt.Fprintf(errw, "djo ask: a conexão caiu ao ler a resposta (%s)\n", askNetErr(err))
		return 2
	}
	if len(data) > askCLIMaxBody {
		fmt.Fprintf(errw, "djo ask: a resposta passa de %d bytes; ignorada\n", askCLIMaxBody)
		return 2
	}
	code := askExitFor(res.StatusCode)
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		fmt.Fprintf(errw, "djo ask: o site redirecionou (%d) e o redirecionamento não é seguido; use o endereço final\n", res.StatusCode)
		return 2
	}
	ct := strings.ToLower(res.Header.Get("Content-Type"))
	isJSON := strings.Contains(ct, "json") && json.Valid(data)
	if *asJSON {
		if isJSON {
			fmt.Fprintln(out, strings.TrimRight(string(data), "\n"))
		} else {
			fmt.Fprintln(out, askSafe(strings.TrimRight(string(data), "\n")))
		}
		return code
	}
	if !isJSON {
		if res.StatusCode == http.StatusNotFound {
			fmt.Fprintln(errw, "djo ask: este site não publica o /ask")
			return 3
		}
		fmt.Fprintf(errw, "djo ask: resposta inesperada (%d, não é JSON)\n", res.StatusCode)
		if code == 0 {
			code = 2
		}
		return code
	}
	if res.StatusCode != http.StatusOK {
		msg := askErrText(data)
		fmt.Fprintln(errw, askSafe(msg))
		if res.StatusCode == http.StatusUnauthorized {
			fmt.Fprintln(errw, "(o token vai em "+askCLITokenEnv+", --token-file ou --token-stdin)")
		}
		if res.StatusCode == http.StatusTooManyRequests {
			if ra := res.Header.Get("Retry-After"); ra != "" {
				fmt.Fprintln(errw, "tente de novo em", askSafe(ra), "s")
			}
		}
		return code
	}
	askPrintAnswer(out, data)
	return 0
}

// askExitFor traduz o status HTTP em código de saída.
func askExitFor(status int) int {
	switch {
	case status == http.StatusOK:
		return 0
	case status == http.StatusUnauthorized || status == http.StatusTooManyRequests:
		return 4
	case status >= 500:
		return 2
	case status >= 300 && status < 400:
		return 2
	default:
		return 3
	}
}

func askNetErr(err error) string {
	if ue, ok := err.(*url.Error); ok {
		err = ue.Err
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return "tempo esgotado"
	}
	return askSafe(err.Error())
}

// askParseDjoURL lê djo://host[/ask][='pergunta'] e devolve o host e a pergunta.
func askParseDjoURL(s string) (host, question string, err error) {
	rest := s[len("djo://"):]
	host, tail, _ := strings.Cut(rest, "/")
	if host == "" {
		return "", "", fmt.Errorf("endereço djo:// sem site")
	}
	if tail == "" || tail == "ask" {
		return host, "", nil
	}
	q, ok := strings.CutPrefix(tail, "ask=")
	if !ok {
		return "", "", fmt.Errorf("use djo://site/ask='pergunta' (só o /ask existe)")
	}
	if len(q) >= 2 && (q[0] == '\'' || q[0] == '"') && q[len(q)-1] == q[0] {
		q = q[1 : len(q)-1]
	}
	if un, uerr := url.PathUnescape(q); uerr == nil {
		q = un
	}
	return host, q, nil
}

// askEndpoint valida o destino e devolve a URL do POST. Sem esquema = https (djo:// também vira https).
func askEndpoint(target string, insecure bool) (string, error) {
	scheme, rest := "https", target
	if i := strings.Index(target, "://"); i >= 0 {
		scheme = strings.ToLower(target[:i])
		rest = target[i+3:]
		if scheme != "http" && scheme != "https" {
			return "", fmt.Errorf("esquema %q não aceito (use https://, ou só o nome do site)", askSafe(scheme))
		}
	}
	host, tail, _ := strings.Cut(rest, "/")
	tail = strings.TrimSuffix(tail, "/")
	if strings.ContainsAny(host, "@?#\\ ") || strings.ContainsAny(rest, "?#") {
		return "", fmt.Errorf("endereço inválido: use só site[:porta]; sem usuário, consulta ou fragmento")
	}
	if !askHostRe.MatchString(host) {
		return "", fmt.Errorf("endereço de site inválido")
	}
	if tail != "" && tail != "ask" {
		return "", fmt.Errorf("o endereço é só o site; o caminho /ask é fixo")
	}
	if p := hostPort(host); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("porta inválida")
		}
	}
	if scheme == "http" && !insecure && !askIsLocal(host) {
		return "", fmt.Errorf("http:// só vale para localhost; use https:// (ou --insecure, sabendo que o texto e o token viajam abertos)")
	}
	return scheme + "://" + host + "/ask", nil
}

func hostPort(h string) string {
	if strings.HasPrefix(h, "[") {
		if i := strings.LastIndex(h, "]:"); i >= 0 {
			return h[i+2:]
		}
		return ""
	}
	if i := strings.LastIndex(h, ":"); i >= 0 {
		return h[i+1:]
	}
	return ""
}

func askIsLocal(host string) bool {
	h := host
	if strings.HasPrefix(h, "[") {
		if i := strings.Index(h, "]"); i > 0 {
			h = h[1:i]
		}
	} else if i := strings.LastIndex(h, ":"); i >= 0 {
		h = h[:i]
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func askReadToken(file string, fromStdin bool, stdin io.Reader, getenv func(string) string) (string, error) {
	var tok string
	switch {
	case file != "" && fromStdin:
		return "", fmt.Errorf("use --token-file ou --token-stdin, não os dois")
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("não consegui ler o arquivo do token")
		}
		tok = firstLine(string(b))
	case fromStdin:
		b, err := io.ReadAll(io.LimitReader(stdin, 8192))
		if err != nil {
			return "", fmt.Errorf("não consegui ler o token da entrada")
		}
		tok = firstLine(string(b))
	default:
		tok = strings.TrimSpace(getenv(askCLITokenEnv))
	}
	if tok == "" {
		if file != "" || fromStdin {
			return "", fmt.Errorf("o token está vazio")
		}
		return "", nil
	}
	if !askTokenRe.MatchString(tok) {
		return "", fmt.Errorf("o token tem caracteres que não são aceitos (só ASCII visível, sem espaços)")
	}
	return tok, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(s)
}

// askSafe escapa o que não pode chegar cru ao terminal: controles C0 (menos \n e \t), DEL, C1,
// e os caracteres que reordenam ou escondem texto (bidi e largura zero).
func askSafe(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			fmt.Fprintf(&b, "\\x%02x", r)
		case r == 0x061C || r == 0x200B || r == 0x200C || r == 0x200D || r == 0x200E || r == 0x200F || (r >= 0x202A && r <= 0x202E) || (r >= 0x2060 && r <= 0x2064) || (r >= 0x2066 && r <= 0x2069) || r == 0xFEFF:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func askErrText(data []byte) string {
	var m struct {
		Erro string `json:"erro"`
	}
	if json.Unmarshal(data, &m) == nil && m.Erro != "" {
		return m.Erro
	}
	return "o servidor recusou a pergunta"
}

// ---- resposta ----

type askOrdered struct {
	keys []string
	vals map[string]any
}

// askDecodeOrdered lê um objeto JSON guardando a ordem das chaves.
func askDecodeOrdered(raw json.RawMessage) (*askOrdered, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	t, err := dec.Token()
	if d, ok := t.(json.Delim); err != nil || !ok || d != '{' {
		return nil, false
	}
	o := &askOrdered{vals: map[string]any{}}
	for dec.More() {
		kt, err := dec.Token()
		k, ok := kt.(string)
		if err != nil || !ok {
			return nil, false
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		o.keys = append(o.keys, k)
		o.vals[k] = v
	}
	return o, true
}

func askCellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, it := range x {
			parts[i] = askCellText(it)
		}
		return strings.Join(parts, ", ")
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func askPrintAnswer(out io.Writer, data []byte) {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		fmt.Fprintln(out, "(resposta sem formato conhecido)")
		return
	}
	str := func(k string) string {
		var s string
		json.Unmarshal(top[k], &s)
		return s
	}
	if a := str("answer"); a != "" {
		fmt.Fprintln(out, askSafe(a))
	}
	var items []json.RawMessage
	if json.Unmarshal(top["data"], &items) == nil && len(items) > 0 {
		var rows []*askOrdered
		var cols []string
		seen := map[string]bool{}
		for _, it := range items {
			if o, ok := askDecodeOrdered(it); ok {
				rows = append(rows, o)
				for _, k := range o.keys {
					if !seen[k] {
						seen[k] = true
						cols = append(cols, k)
					}
				}
			}
		}
		if len(cols) > 0 {
			askCLITable(out, cols, rows)
		}
	}
	var more bool
	json.Unmarshal(top["more"], &more)
	if more {
		var pg int
		json.Unmarshal(top["page"], &pg)
		if pg < 1 {
			pg = 1
		}
		fmt.Fprintf(out, "\n(há mais resultados: use --page %d)\n", pg+1)
	}
}

func askCLITable(out io.Writer, cols []string, rows []*askOrdered) {
	const maxW = 50
	clean := func(s string) string {
		s = askSafe(s)
		s = strings.NewReplacer("\n", " ", "\t", " ").Replace(s)
		if utf8.RuneCountInString(s) > maxW {
			s = string([]rune(s)[:maxW-1]) + "…"
		}
		return s
	}
	cells := make([][]string, len(rows))
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = utf8.RuneCountInString(clean(c))
	}
	for r, row := range rows {
		cells[r] = make([]string, len(cols))
		for i, c := range cols {
			s := clean(askCellText(row.vals[c]))
			cells[r][i] = s
			if n := utf8.RuneCountInString(s); n > w[i] {
				w[i] = n
			}
		}
	}
	pad := func(s string, n int) string { return s + strings.Repeat(" ", n-utf8.RuneCountInString(s)) }
	fmt.Fprintln(out)
	var hdr, sep []string
	for i, c := range cols {
		hdr = append(hdr, pad(clean(c), w[i]))
		sep = append(sep, strings.Repeat("-", w[i]))
	}
	fmt.Fprintln(out, strings.TrimRight(strings.Join(hdr, "  "), " "))
	fmt.Fprintln(out, strings.Join(sep, "  "))
	for _, row := range cells {
		var line []string
		for i, s := range row {
			line = append(line, pad(s, w[i]))
		}
		fmt.Fprintln(out, strings.TrimRight(strings.Join(line, "  "), " "))
	}
}
