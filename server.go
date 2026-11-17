//go:build !noweb

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxBody     = 1 << 20  // 1 MB de formulário
	maxUpload   = 10 << 20 // 10 MB num formulário com arquivos
	maxResponse = 10 << 20 // 10 MB de saída por página
)

// Server serve uma pasta como o PHP: cada URL vira um arquivo.
//
//	/            -> index.djo.html, index.djo ou index.html
//	/sobre       -> sobre.djo.html ou sobre.djo
//	/estilo.css  -> arquivo estático
//
// Arquivos e pastas começando com '.' ou '_' nunca são servidos direto
// (use '_topo.djo.html' para partes que só o include enxerga).
type Server struct {
	root         string
	realRoot     string
	dev          bool // mostra o erro na página
	maxSteps     int
	allowPrivate bool // páginas podem chamar endereços internos (Http.get)
	logger       *log.Logger
	rmu          sync.Mutex // rotas.djo
	rotas        *routeTable
	rmod         time.Time
	rsize        int64
	lane         *lane // limites do tráfego de agentes
	rcache       respCache
	askx         askState
	mailSend     bool     // -dev: Mail.send envia de verdade em vez de gravar o .eml
	mailBox      *mailBox // o djo spec: captura os e-mails
	dbRoot       string   // pasta dos bancos; vazio = a do site (o djo spec usa uma temporária)
}

func NewServer(dir string, dev bool) (*Server, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s não é uma pasta", dir)
	}
	if p := verifyBoosters(real); len(p) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(p, "\n"))
	}
	if err := checkAsk(real); err != nil {
		return nil, err
	}
	srv := &Server{root: real, realRoot: real, dev: dev, maxSteps: 10_000_000, lane: newLane(0, 50)}
	if _, err := srv.table(); err != nil {
		return nil, err
	}
	return srv, nil
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	start := time.Now()
	s.route(sw, r)
	if s.logger != nil {
		s.logger.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	}
}

func (s *Server) mailHost() *mailHost {
	return &mailHost{dev: s.dev && !s.mailSend, box: s.mailBox}
}

func hidden(urlPath string) bool {
	for _, ext := range []string{".db", ".sqlite", ".sqlite3", ".db-wal", ".db-shm", ".db-journal", ".sqlite-wal", ".sqlite-shm"} {
		if strings.HasSuffix(strings.ToLower(urlPath), ext) {
			return true // bancos de dados nunca são servidos
		}
	}
	if strings.EqualFold(urlPath, "/ask.djo") {
		return true // a política do /ask nunca é servida
	}
	if urlPath == "/spec" || strings.HasPrefix(urlPath, "/spec/") {
		return true // os testes (djo spec) nunca são servidos
	}
	if urlPath == "/core" || strings.HasPrefix(urlPath, "/core/") || urlPath == "/boosters" || strings.HasPrefix(urlPath, "/boosters/") {
		return true // o core do sistema e os boosters nunca são servidos
	}
	for _, seg := range strings.Split(urlPath, "/") {
		if strings.HasPrefix(seg, ".") || strings.HasPrefix(seg, "_") || strings.HasPrefix(seg, "[") {
			return true
		}
	}
	return false
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if s.serveAsk(w, r) {
		return
	}
	t, err := s.table()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if t != nil {
		s.serveRoutes(w, r, t)
		return
	}
	urlPath := path.Clean("/" + r.URL.Path)
	if hidden(urlPath) {
		s.notFound(w, r)
		return
	}
	full := filepath.Join(s.root, filepath.FromSlash(urlPath))

	var file string
	if fi, err := os.Stat(full); err == nil {
		if fi.IsDir() {
			if urlPath != "/" && !strings.HasSuffix(r.URL.Path, "/") {
				to := urlPath + "/"
				if r.URL.RawQuery != "" {
					to += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, to, http.StatusMovedPermanently)
				return
			}
			for _, idx := range []string{"index.djo.html", "index.djo", "index.html"} {
				if c := filepath.Join(full, idx); isFile(c) {
					file = c
					break
				}
			}
		} else if fi.Mode().IsRegular() {
			file = full
		}
	} else {
		for _, ext := range []string{".djo.html", ".djo"} {
			if isFile(full + ext) {
				file = full + ext
				break
			}
		}
	}
	var params map[string]string
	if file == "" {
		file, params = s.dynamic(urlPath)
	}
	if file == "" {
		s.notFound(w, r)
		return
	}
	real, err := filepath.EvalSymlinks(file)
	if err != nil || !withinRoot(s.realRoot, real) {
		s.notFound(w, r)
		return
	}

	if isTemplate(file) || strings.HasSuffix(file, ".djo") {
		s.runPage(w, r, file, http.StatusOK, params, "")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
		return
	}
	f, err := os.Open(file)
	if err != nil {
		s.notFound(w, r)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(file), fi.ModTime(), f)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{"404.djo.html", "404.djo"} {
		if p := filepath.Join(s.root, name); isFile(p) {
			s.runPage(w, r, p, http.StatusNotFound, nil, "")
			return
		}
	}
	http.Error(w, "404 não encontrado", http.StatusNotFound)
}

// limitWriter guarda a saída da página e recusa passar do limite.
type limitWriter struct {
	buf bytes.Buffer
	max int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		panic(&djoError{0, "a página gerou saída grande demais"})
	}
	return l.buf.Write(p)
}

func (s *Server) runPage(w http.ResponseWriter, r *http.Request, file string, status int, params map[string]string, ctype string) {
	prog, err := loadProgram(file)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
			if err := r.ParseMultipartForm(maxBody); err != nil {
				http.Error(w, "formulário inválido ou grande demais", http.StatusRequestEntityTooLarge)
				return
			}
			defer r.MultipartForm.RemoveAll() // apaga os temporários
		} else {
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulário inválido ou grande demais", http.StatusRequestEntityTooLarge)
				return
			}
		}
	}

	out := &limitWriter{max: maxResponse}
	in := NewInterp(out)
	in.stdin = bufio.NewReader(strings.NewReader(""))
	in.root = s.root
	in.dbRoot = s.dbRoot
	in.req = r
	in.params = params
	in.resp = &response{status: status, header: http.Header{}}
	in.maxSteps = s.maxSteps
	in.served = !s.allowPrivate
	in.mail = s.mailHost()
	if err := in.loadCore(s.root); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := in.Run(prog); err != nil {
		s.serverError(w, r, err)
		return
	}

	h := w.Header()
	for k, v := range in.resp.header {
		if k == "Link" {
			h["Link"] = append(h["Link"], v...) // soma ao Link do manifesto, em vez de trocar
			continue
		}
		h[k] = v
	}
	if h.Get("Content-Type") == "" {
		if ctype == "" {
			ctype = "text/html; charset=utf-8"
		}
		h.Set("Content-Type", ctype)
	}
	if h.Get("X-Content-Type-Options") == "" {
		h.Set("X-Content-Type-Options", "nosniff")
	}
	for _, c := range in.resp.cookies {
		http.SetCookie(w, c)
	}
	body := out.buf.Bytes()
	if r.Context().Value(announceKey{}) != nil && strings.HasPrefix(h.Get("Content-Type"), "text/html") {
		body = announceAgents(body)
	}
	w.WriteHeader(in.resp.status)
	w.Write(body)
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	if s.logger != nil {
		s.logger.Printf("ERRO em %s: %v", r.URL.Path, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if s.dev {
		fmt.Fprintf(w, "<h1>Erro na página</h1><pre>%s</pre>", html.EscapeString(err.Error()))
		return
	}
	fmt.Fprint(w, "500 erro interno")
}

// serveCmd implementa: djo serve [-addr host:porta] [-dev] [pasta]
func serveCmd(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "localhost:8080", "endereço (use :80 para aceitar de fora)")
	dev := fs.Bool("dev", false, "mostra os erros na própria página")
	agentConc := fs.Int("agent-conc", 0, "máximo de requisições de agente em execução ao mesmo tempo (0 = 2 por núcleo)")
	agentRate := fs.Float64("agent-rate", 50, "requisições de agente por segundo, por cliente (0 = sem limite)")
	mailSend := fs.Bool("mail-send", false, "com -dev, Mail.send envia de verdade (por padrão grava o .eml em _saida/emails)")
	allowPrivate := fs.Bool("allow-private", false, "deixa as páginas chamarem endereços internos (rede local) com Http.get/post")
	trusted := fs.String("trusted-proxy", "", "proxies confiáveis para X-Forwarded-Proto: IPs/CIDRs separados por vírgula, ou none (padrão: loopback; também DJO_TRUSTED_PROXY)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	trustedSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "trusted-proxy" {
			trustedSet = true
		}
	})
	if err := configureTrustedProxy(*trusted, trustedSet); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	srv, err := NewServer(dir, *dev)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	srv.allowPrivate = *allowPrivate
	srv.mailSend = *mailSend
	srv.lane = newLane(*agentConc, *agentRate)
	srv.logger = log.New(os.Stderr, "", log.LstdFlags)

	hs := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	fmt.Fprintf(os.Stderr, "djo servindo %s em http://%s (ctrl+c para parar)\n", srv.root, *addr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		if errors.Is(err, syscall.EADDRINUSE) {
			fmt.Fprintf(os.Stderr, "a porta de %s já está em uso por outro programa.\ntente outra: djo serve -addr localhost:8081 %s\n", *addr, dir)
			return 1
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// dynamic acha páginas de rota amigável: [nome].djo casa com um trecho da URL e
// [...nome].djo com o resto dela. Um arquivo ou pasta de nome exato sempre vence.
//
//	posts/[slug].djo         /posts/ola-mundo        slug = "ola-mundo"
//	posts/[id]/editar.djo    /posts/7/editar         id   = "7"
//	docs/[...caminho].djo    /docs/a/b/c             caminho = "a/b/c"
func (s *Server) dynamic(urlPath string) (string, map[string]string) {
	segs := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(segs) == 0 || segs[0] == "" {
		return "", nil
	}
	params := map[string]string{}
	if f := s.resolve(s.root, segs, params); f != "" {
		return f, params
	}
	return "", nil
}

func pageIn(dir, name string) string {
	for _, ext := range []string{".djo.html", ".djo"} {
		if p := filepath.Join(dir, name+ext); isFile(p) {
			return p
		}
	}
	return ""
}

// resolve procura segs a partir de dir; ordem: nome exato, [nome], [...nome].
func (s *Server) resolve(dir string, segs []string, params map[string]string) string {
	if len(segs) == 0 {
		return pageIn(dir, "index")
	}
	seg, rest := segs[0], segs[1:]
	if seg == "" || (seg == "core" || seg == "boosters") && dir == s.root {
		return ""
	}
	// 1. nome exato (pasta; o arquivo exato de um segmento só é a última parte)
	if len(rest) == 0 {
		if p := pageIn(dir, seg); p != "" {
			return p
		}
	}
	if sub := filepath.Join(dir, seg); isDir(sub) {
		if f := s.resolve(sub, rest, params); f != "" {
			return f
		}
	}
	// 2. [nome]
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "[") || strings.HasPrefix(n, "[...") {
			continue
		}
		if e.IsDir() && strings.HasSuffix(n, "]") {
			name := n[1 : len(n)-1]
			if f := s.resolve(filepath.Join(dir, n), rest, params); f != "" {
				params[name] = seg
				return f
			}
		}
		if len(rest) == 0 {
			if name, ok := dynName(n, "["); ok {
				params[name] = seg
				return filepath.Join(dir, n)
			}
		}
	}
	// 3. [...nome]: leva o resto da URL
	for _, e := range entries {
		if name, ok := dynName(e.Name(), "[..."); ok && !e.IsDir() {
			params[name] = strings.Join(segs, "/")
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// dynName extrai "slug" de "[slug].djo" / "[slug].djo.html".
func dynName(file, open string) (string, bool) {
	for _, ext := range []string{".djo.html", ".djo"} {
		if strings.HasSuffix(file, "]"+ext) && strings.HasPrefix(file, open) {
			name := file[len(open) : len(file)-len(ext)-1]
			if name != "" && !strings.ContainsAny(name, "[]/.") {
				return name, true
			}
		}
	}
	return "", false
}
