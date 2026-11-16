//go:build !noweb && !nodb

package main

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// /ask: a porta em linguagem natural para HUMANOS (agentes usam o manifesto e as actions).
//
//	POST /ask  {"question": "...", "page": 2}   -> JSON {lang, answer, type, data|count, page, limit, more, calls, confirm}
//	GET  /ask                                   -> página com caixa de pergunta (?q= responde na própria página)
//
// Só lê. O nível (public, agent, admin) vem do gancho Ask.level_from do projeto, é decidido ANTES de ler a
// pergunta e nunca depende dela. Sem ask.djo o /ask não existe (cai nas rotas normais, como qualquer outro caminho).

const (
	askMaxBody  = 4 << 10
	askLevelPub = "public"
)

func (s *Server) serveAsk(w http.ResponseWriter, r *http.Request) bool {
	if path.Clean("/"+r.URL.Path) != "/ask" {
		return false
	}
	cfg, err := s.askx.config(s.root, s.dev)
	if err != nil {
		s.logf("ask.djo: %v", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return true
	}
	if cfg == nil {
		return false
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Vary", "Authorization, Cookie, Accept")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost:
	default:
		h.Set("Allow", "GET, HEAD, POST")
		jsonError(w, http.StatusMethodNotAllowed, "método não permitido", nil)
		return true
	}
	now := time.Now()
	// 1. limites, do mais barato ao mais caro: teto global, depois por IP (antes de qualquer gancho)
	if ok, retry := s.askx.rl.allow("global", cfg.limits.globalPerMinute, now); !ok {
		askTooMany(w, retry)
		return true
	}
	if ok, retry := s.askx.rl.allow("ip:"+askClientIP(r, cfg.proxies), cfg.limits.perMinute, now); !ok {
		askTooMany(w, retry)
		return true
	}
	// 2. o nível: decidido pelo projeto, antes de olhar a pergunta
	level, token, err := s.askLevel(r, cfg)
	if err != nil {
		h.Set("WWW-Authenticate", `Bearer realm="ask"`)
		jsonError(w, http.StatusUnauthorized, "token inválido ou sem permissão", nil)
		return true
	}
	if token != "" {
		sum := sha256.Sum256([]byte(token))
		if ok, retry := s.askx.rl.allow("tok:"+hex.EncodeToString(sum[:8]), cfg.limits.perMinute, now); !ok {
			askTooMany(w, retry)
			return true
		}
	}
	// 3. a pergunta: SEMPRE por POST. O GET só mostra a página com a caixa (nunca lê nem devolve ?q=).
	if r.Method == http.MethodPost {
		s.askPost(w, r, cfg, level)
		return true
	}
	s.askPage(w, r, cfg, level, "", 0, nil, nil)
	return true
}

func askTooMany(w http.ResponseWriter, retry int) {
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	jsonError(w, http.StatusTooManyRequests, "muitas perguntas; tente de novo em instantes", nil)
}

// ---- nível ----

// askBearer devolve o token de "Authorization: Bearer ..." (como Http.bearer), ou "".
func askBearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if len(v) > 7 && strings.EqualFold(v[:7], "Bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}

// askLevel chama o gancho do projeto. Falha fechada: sem gancho, erro, nil ou valor desconhecido = public.
// Se havia token e o gancho não o aceitou, o erro é devolvido (a resposta é 401, nunca um rebaixamento silencioso).
func (s *Server) askLevel(r *http.Request, cfg *askConfig) (level, token string, err error) {
	token = askBearer(r)
	if cfg.levelFrom == "" {
		if token != "" {
			return "", "", errors.New("sem Ask.level_from, tokens não são aceitos")
		}
		return askLevelPub, "", nil
	}
	out := &limitWriter{max: 1 << 16}
	in := NewInterp(out)
	in.stdin = bufio.NewReader(strings.NewReader(""))
	in.root = s.root
	in.req = r
	in.resp = &response{status: http.StatusOK, header: http.Header{}}
	in.maxSteps = s.maxSteps
	in.served = !s.allowPrivate
	var got any
	rerr := in.loadCore(s.root)
	if rerr == nil {
		fd := in.funcs[cfg.levelFrom]
		if fd == nil || fd.Action || len(fd.Params) != 0 {
			rerr = fmt.Errorf("a função %s não existe mais", cfg.levelFrom)
		} else {
			prog := []Stmt{&VarStmt{Name: "nivel", Init: &Call{Name: cfg.levelFrom, Line: fd.Line}, Line: fd.Line}}
			if rerr = in.Run(prog); rerr == nil {
				got, _ = in.global.get("nivel")
			}
		}
	}
	if rerr != nil {
		s.logf("ask: Ask.level_from %s: %v", cfg.levelFrom, rerr)
	}
	lv, _ := got.(string)
	if rerr == nil && levelIndex(lv) >= 0 {
		if lv == askLevelPub {
			token = ""
		}
		return lv, token, nil
	}
	if token != "" {
		return "", "", errors.New("token recusado")
	}
	return askLevelPub, "", nil
}

func (s *Server) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

// ---- POST ----

func (s *Server) askPost(w http.ResponseWriter, r *http.Request, cfg *askConfig, level string) {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/json":
	case "application/x-www-form-urlencoded":
		s.askPostForm(w, r, cfg, level)
		return
	default:
		jsonError(w, http.StatusUnsupportedMediaType, "envie o corpo como application/json", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, askMaxBody)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, http.StatusRequestEntityTooLarge, "corpo grande demais", nil)
		return
	}
	v, err := jsonDecode(string(raw))
	body, ok := v.(*Map)
	if err != nil || !ok {
		jsonError(w, http.StatusBadRequest, "o corpo precisa ser um objeto JSON como {\"question\": \"...\"}", nil)
		return
	}
	var q, lang string
	var page int64
	for i, k := range body.Keys {
		switch k {
		case "question":
			s, ok := body.Vals[i].(string)
			if !ok {
				jsonError(w, http.StatusUnprocessableEntity, "entrada inválida", map[string]string{"question": "deve ser texto"})
				return
			}
			q = s
		case "page":
			n, ok := body.Vals[i].(int64)
			if !ok || n < 1 || n > 1_000_000 {
				jsonError(w, http.StatusUnprocessableEntity, "entrada inválida", map[string]string{"page": "deve ser um Int de 1 a 1000000"})
				return
			}
			page = n
		case "lang":
			l, ok := body.Vals[i].(string)
			if !ok || (l != "en" && l != "pt") {
				jsonError(w, http.StatusUnprocessableEntity, "entrada inválida", map[string]string{"lang": "deve ser \"en\" ou \"pt\""})
				return
			}
			lang = l
		default:
			jsonError(w, http.StatusUnprocessableEntity, "entrada inválida", map[string]string{fmt.Sprint(askSafeWord(askNorm(fmt.Sprint(k)))): "campo desconhecido"})
			return
		}
	}
	if _, has := body.get("question"); !has {
		jsonError(w, http.StatusUnprocessableEntity, "entrada inválida", map[string]string{"question": "obrigatório"})
		return
	}
	if lang == "" {
		lang = askAcceptLang(r)
	}
	res, status, aerr := s.askAnswer(cfg, level, q, page, lang)
	if aerr != nil {
		askJSONErr(w, cfg, aerr)
		return
	}
	askJSON(w, status, res)
}

// askAcceptLang: o idioma pedido pelo navegador/cliente ("en", "pt" ou ""), só para as mensagens de erro.
func askAcceptLang(r *http.Request) string {
	a := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language")))
	switch {
	case strings.HasPrefix(a, "pt"):
		return "pt"
	case strings.HasPrefix(a, "en"):
		return "en"
	}
	return ""
}

// askPostForm: a página humana envia a pergunta por POST (formulário). Mesmos limites do JSON; resposta em HTML.
// Um POST de outro site não consegue ler a resposta (não há CORS) e, vindo de outra origem, é recusado.
func (s *Server) askPostForm(w http.ResponseWriter, r *http.Request, cfg *askConfig, level string) {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			jsonError(w, http.StatusForbidden, "origem não aceita", nil)
			return
		}
	} else if o == "null" {
		jsonError(w, http.StatusForbidden, "origem não aceita", nil)
		return
	}
	if sf := r.Header.Get("Sec-Fetch-Site"); sf != "" && sf != "same-origin" && sf != "none" {
		jsonError(w, http.StatusForbidden, "origem não aceita", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, askMaxBody)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, http.StatusRequestEntityTooLarge, "corpo grande demais", nil)
		return
	}
	vals, err := url.ParseQuery(string(raw))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "formulário inválido", nil)
		return
	}
	var q string
	var page int64
	bad := ""
	for k, v := range vals {
		switch k {
		case "question":
			if len(v) == 1 {
				q = v[0]
			} else {
				bad = "question"
			}
		case "page":
			if len(v) == 1 {
				page = int64(0)
				n, perr := strconv.ParseInt(v[0], 10, 64)
				if perr != nil || n < 1 || n > 1_000_000 {
					bad = "page"
				} else {
					page = n
				}
			} else {
				bad = "page"
			}
		default:
			bad = "x"
		}
	}
	if _, has := vals["question"]; !has && bad == "" {
		bad = "question"
	}
	if bad != "" {
		s.askPage(w, r, cfg, level, "", 0, nil, askErr{Kind: "bad_value", Msg: askRunMsgs[askUILang(r)]["cond"], Lang: askUILang(r)})
		return
	}
	res, _, aerr := s.askAnswer(cfg, level, q, page, askUILang(r))
	s.askPage(w, r, cfg, level, q, page, res, aerr)
}

func askJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(jsonEncode(v, "", 0)))
}

// ---- a resposta ----

var askStatus = map[string]int{
	"not_accepted": http.StatusForbidden, "timeout": http.StatusGatewayTimeout, "db": http.StatusServiceUnavailable,
}

var askRunMsgs = map[string]map[string]string{
	"en": {"cond": "That condition does not apply to that field.", "timeout": "The question took too long. Try a narrower one.", "db": "I could not answer right now. Try again later."},
	"pt": {"cond": "Essa condição não vale para esse campo.", "timeout": "A pergunta demorou demais. Tente uma mais específica.", "db": "Não consegui responder agora. Tente de novo mais tarde."},
}

// askText escolhe a mensagem final: o Ask.reply do site (refused / unknown) ou o texto padrão no idioma da pergunta.
func (c *askConfig) askText(e askErr) (int, string) {
	status := http.StatusUnprocessableEntity
	if st, ok := askStatus[e.Kind]; ok {
		status = st
	}
	lang := e.Lang
	msg := e.Msg
	key := ""
	switch e.Kind {
	case "not_accepted":
		key = "refused"
	case "not_understood":
		key = "unknown"
	}
	if key != "" {
		if m := c.reply[key]; m != nil {
			if t := m[lang]; t != "" {
				return status, t
			}
			if t := m["*"]; t != "" {
				return status, t
			}
		}
	}
	return status, msg
}

func askJSONErr(w http.ResponseWriter, c *askConfig, e error) {
	ae, ok := e.(askErr)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "erro interno", nil)
		return
	}
	status, msg := c.askText(ae)
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	b := newMap()
	b.set("erro", msg)
	b.set("tipo", ae.Kind)
	if ae.Lang != "" {
		b.set("lang", ae.Lang)
	}
	w.Write([]byte(jsonEncode(b, "", 0)))
}

func askOpenDB(root, name string) (*sql.DB, string, error) {
	if kind, ok := remoteScheme(name); ok {
		db, err := openRemote(kind, name)
		return db, kind, err
	}
	p, err := safeJoinNew(root, name)
	if err != nil {
		return nil, "", err
	}
	db, err := openDB(p)
	return db, kindSQLite, err
}

// askAnswer: frase -> plano -> consulta -> resposta estruturada. O nível já veio decidido.
func (s *Server) askAnswer(cfg *askConfig, level, question string, page int64, hint string) (*Map, int, error) {
	parsed, err := cfg.parsePhraseLang(level, question, hint)
	if err != nil {
		return nil, 0, err
	}
	plan := parsed.Plan
	if page > 0 {
		plan.Page = page
	}
	lang := parsed.Lang
	db, kind, err := askOpenDB(s.root, cfg.dbName)
	if err != nil {
		s.logf("ask: banco: %v", err)
		return nil, 0, askErr{Kind: "db", Msg: askRunMsgs[lang]["db"], Lang: lang}
	}
	res, err := cfg.run(db, kind, level, plan)
	if err != nil {
		ae, ok := err.(askErr)
		if !ok {
			s.logf("ask: %v", err)
			return nil, 0, askErr{Kind: "db", Msg: askRunMsgs[lang]["db"], Lang: lang}
		}
		if ae.Cause != "" {
			s.logf("ask: %s: %s", ae.Kind, ae.Cause)
		}
		ae.Lang = lang
		switch ae.Kind {
		case "timeout", "db":
			ae.Msg = askRunMsgs[lang][ae.Kind]
		default:
			ae.Kind, ae.Msg = "bad_value", askRunMsgs[lang]["cond"]
		}
		return nil, 0, ae
	}
	return askBuildResponse(lang, plan, res, askSingularLabel(cfg, lang, plan.Type)), http.StatusOK, nil
}

var askSay = map[string]map[string]string{
	"en": {
		"count": "There are %d.", "none": "No results.", "some": "Showing %d result(s), page %d.",
		"more":  ` Say "page %d" to see more.`,
		"found": "Here is the %s.", "several": "Found %d matches; say which one (use the exact title).", "notfound": "I did not find it.",
	},
	"pt": {
		"count": "São %d.", "none": "Nenhum resultado.", "some": "Mostrando %d resultado(s), página %d.",
		"more":  ` Diga "pagina %d" para ver mais.`,
		"found": "Encontrei: %s.", "several": "Achei %d; diga qual (use o título exato).", "notfound": "Não encontrei.",
	},
}

// askSingularLabel é o nome do tipo, no singular, para frases como "Encontrei: página.": em português
// o primeiro apelido declarado que não termina em "s"; senão o nome do tipo.
func askSingularLabel(cfg *askConfig, lang, typ string) string {
	if lang == "pt" {
		if t := cfg.types[typ]; t != nil {
			for _, a := range t.aliases {
				if !strings.HasSuffix(a, "s") {
					return strings.ReplaceAll(a, "_", " ")
				}
			}
		}
	}
	return typ
}

func askBuildResponse(lang string, plan askPlan, res *askResult, label string) *Map {
	say := askSay[lang]
	m := newMap()
	m.set("lang", lang)
	var text string
	switch {
	case plan.CountOnly:
		text = askSprintf(say["count"], int(res.Count))
	case plan.Show && res.Item:
		text = askSprintf(say["found"], label)
	case plan.Show && res.Ambiguous:
		text = askSprintf(say["several"], len(res.Items))
	case plan.Show:
		text = say["notfound"]
	case len(res.Items) == 0:
		text = say["none"]
	default:
		text = askSprintf(say["some"], len(res.Items), int(res.Page))
		if res.More {
			text += askSprintf(say["more"], int(res.Page)+1)
		}
	}
	m.set("answer", text)
	m.set("type", plan.Type)
	if plan.CountOnly {
		m.set("count", res.Count)
	} else if plan.Show && res.Item {
		// um item só: todos os campos que o nível enxerga, com o texto longo (detail)
		m.set("item", res.Items[0])
		df := &Array{}
		for _, f := range res.DetailFields {
			df.Items = append(df.Items, f)
		}
		m.set("detail_fields", df)
		m.set("truncated", res.Truncated)
	} else {
		if plan.Show {
			m.set("item", nil)
			m.set("ambiguous", res.Ambiguous)
		}
		arr := &Array{}
		for _, it := range res.Items {
			arr.Items = append(arr.Items, it)
		}
		m.set("data", arr)
		m.set("page", res.Page)
		m.set("limit", res.Limit)
		m.set("more", res.More)
		if res.HasCount {
			m.set("count", res.Count)
		}
		if res.More {
			next := newMap()
			next.set("page", res.Page+1)
			m.set("next", next)
		}
	}
	// calls: o que foi consultado, em forma estruturada (transparência: nada acontece que a pessoa não possa ver)
	call := newMap()
	call.set("op", "query")
	if plan.Show {
		call.set("op", "show")
		call.set("value", plan.ShowValue)
	}
	call.set("type", plan.Type)
	fs := &Array{}
	for _, f := range plan.Filters {
		fm := newMap()
		fm.set("field", f.Field)
		fm.set("op", f.Op)
		vs := &Array{}
		for _, v := range f.Vals {
			vs.Items = append(vs.Items, v)
		}
		fm.set("values", vs)
		fs.Items = append(fs.Items, fm)
	}
	call.set("filters", fs)
	if plan.Order != nil {
		om := newMap()
		om.set("field", plan.Order.Field)
		om.set("desc", plan.Order.Desc)
		call.set("order", om)
	}
	m.set("calls", &Array{Items: []any{call}})
	m.set("confirm", nil)
	return m
}

// ---- a página humana ----

var askPageText = map[string]map[string]string{
	"en": {
		"title": "Ask", "h": "Ask a question", "ph": "for example: list posts from 2025-01-01 to 2025-12-31", "go": "Ask",
		"ex": "Examples", "res": "Answer", "more": "Next page", "cut": "The text was cut at the size limit.", "hint": "This page only reads. Questions are answered in English or Portuguese.",
	},
	"pt": {
		"title": "Perguntar", "h": "Faça uma pergunta", "ph": "por exemplo: liste posts de 2025-01-01 até 2025-12-31", "go": "Perguntar",
		"ex": "Exemplos", "res": "Resposta", "more": "Próxima página", "cut": "O texto foi cortado no limite de tamanho.", "hint": "Esta página só lê. As perguntas valem em português ou inglês.",
	},
}

func askUILang(r *http.Request) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language"))), "pt") {
		return "pt"
	}
	return "en"
}

// askClean limita o que a pessoa digitou antes de devolver à caixa (sempre escapado ao escrever).
func askClean(q string) string {
	if len(q) > askMaxPhrase {
		q = q[:askMaxPhrase]
	}
	if !utf8.ValidString(q) {
		q = strings.ToValidUTF8(q, "")
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, q)
}

func askCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case *Array:
		parts := make([]string, len(x.Items))
		for i, it := range x.Items {
			parts[i] = askCell(it)
		}
		return strings.Join(parts, ", ")
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return format(v)
}

// askPage mostra a página. No GET ela é só a caixa de pergunta e os exemplos; depois de um POST de formulário
// traz também a resposta (status 200, ou o do erro). Nunca lê nem devolve ?q= da URL.
func (s *Server) askPage(w http.ResponseWriter, r *http.Request, cfg *askConfig, level, q string, page int64, res *Map, aerr error) {
	ui := askUILang(r)
	tx := askPageText[ui]
	esc := html.EscapeString
	status := http.StatusOK
	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"" + ui + "\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	b.WriteString("<title>" + esc(tx["title"]) + "</title><meta name=\"robots\" content=\"noindex\">")
	b.WriteString("<style>body{font:16px/1.5 system-ui,sans-serif;max-width:46rem;margin:2rem auto;padding:0 1rem}input[type=text]{width:100%;padding:.5rem;box-sizing:border-box}button{margin-top:.5rem;padding:.4rem 1rem}table{border-collapse:collapse;margin:1rem 0;width:100%}td,th{border:1px solid #8884;padding:.25rem .5rem;text-align:left;vertical-align:top}code{background:#8882;padding:0 .25rem}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#8881;padding:.5rem;border:1px solid #8884}dt{font-weight:bold}.err{color:#b00020}</style>")
	b.WriteString("</head><body><main><h1>" + esc(tx["h"]) + "</h1>")
	qc := askClean(q)
	b.WriteString("<form method=\"post\" action=\"/ask\" enctype=\"application/x-www-form-urlencoded\"><label for=\"q\" hidden>" + esc(tx["h"]) + "</label>")
	b.WriteString("<input type=\"text\" id=\"q\" name=\"question\" maxlength=\"" + strconv.Itoa(askMaxPhrase) + "\" autocomplete=\"off\" placeholder=\"" + esc(tx["ph"]) + "\" value=\"" + esc(qc) + "\">")
	b.WriteString("<button type=\"submit\">" + esc(tx["go"]) + "</button></form>")
	if aerr != nil || res != nil {
		b.WriteString("<section><h2>" + esc(tx["res"]) + "</h2>")
		if aerr != nil {
			msg := "error"
			status = http.StatusInternalServerError
			if ae, ok := aerr.(askErr); ok {
				status, msg = cfg.askText(ae)
			}
			b.WriteString("<p class=\"err\" role=\"alert\">" + esc(msg) + "</p>")
		} else {
			ans, _ := res.get("answer")
			b.WriteString("<p>" + esc(askCell(ans)) + "</p>")
			if it, ok := res.get("item"); ok && it != nil {
				df, _ := res.get("detail_fields")
				tr, _ := res.get("truncated")
				askItem(&b, it.(*Map), df, tr == true, tx)
			}
			if d, ok := res.get("data"); ok {
				askTable(&b, d.(*Array))
			}
			if more, _ := res.get("more"); more == true {
				pg, _ := res.get("page")
				np := pg.(int64) + 1
				b.WriteString("<form method=\"post\" action=\"/ask\"><input type=\"hidden\" name=\"question\" value=\"" + esc(qc) + "\"><input type=\"hidden\" name=\"page\" value=\"" + strconv.FormatInt(np, 10) + "\"><button type=\"submit\">" + esc(tx["more"]) + "</button></form>")
			}
		}
		b.WriteString("</section>")
	}
	// exemplos: só tipos que este nível enxerga; cada um é um formulário POST
	b.WriteString("<section><h2>" + esc(tx["ex"]) + "</h2><ul>")
	for _, name := range cfg.typeNames(level) {
		for _, lang := range []string{"en", "pt"} {
			ex := askSprintf(askExample[lang], name+"s")
			b.WriteString("<li><form method=\"post\" action=\"/ask\"><input type=\"hidden\" name=\"question\" value=\"" + esc(ex) + "\"><button type=\"submit\"><code>" + esc(ex) + "</code></button></form></li>")
		}
	}
	b.WriteString("</ul></section><p><small>" + esc(tx["hint"]) + "</small></p></main></body></html>")

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		io.WriteString(w, b.String())
	}
}

// askItem mostra um item: os campos curtos numa lista e o texto longo (detail) em <pre>, sempre escapado
// (nada de Markdown nem de HTML do site vira marcação aqui).
func askItem(b *strings.Builder, m *Map, detailFields any, truncated bool, tx map[string]string) {
	esc := html.EscapeString
	long := map[string]bool{}
	if arr, ok := detailFields.(*Array); ok {
		for _, f := range arr.Items {
			long[askCell(f)] = true
		}
	}
	b.WriteString("<dl>")
	for i, k := range m.Keys {
		name := fmt.Sprint(k)
		if !long[name] {
			b.WriteString("<dt>" + esc(name) + "</dt><dd>" + esc(askCell(m.Vals[i])) + "</dd>")
		}
	}
	b.WriteString("</dl>")
	for i, k := range m.Keys {
		name := fmt.Sprint(k)
		if long[name] {
			b.WriteString("<h3>" + esc(name) + "</h3><pre>" + esc(askCell(m.Vals[i])) + "</pre>")
		}
	}
	if truncated {
		b.WriteString("<p><small>" + esc(tx["cut"]) + "</small></p>")
	}
}

func askTable(b *strings.Builder, items *Array) {
	if len(items.Items) == 0 {
		return
	}
	first, ok := items.Items[0].(*Map)
	if !ok {
		return
	}
	esc := html.EscapeString
	b.WriteString("<table><thead><tr>")
	for _, k := range first.Keys {
		b.WriteString("<th>" + esc(fmt.Sprint(k)) + "</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, it := range items.Items {
		m, ok := it.(*Map)
		if !ok {
			continue
		}
		b.WriteString("<tr>")
		for _, k := range first.Keys {
			v, _ := m.get(k)
			b.WriteString("<td>" + esc(askCell(v)) + "</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
}

// askClientIP é o endereço de quem perguntou. Por padrão é o da conexão. Só se a conexão vem de um proxy
// listado em Ask.proxy, o X-Forwarded-For é lido: da direita para a esquerda, pula os proxies de confiança
// e usa o primeiro que não é (o último IP não confiável da cadeia). Cadeia inválida = o IP da conexão.
func askClientIP(r *http.Request, proxies []*net.IPNet) string {
	conn := clientIP(r)
	if len(proxies) == 0 {
		return conn
	}
	trusted := func(ip net.IP) bool {
		for _, n := range proxies {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	cip := net.ParseIP(conn)
	if cip == nil || !trusted(cip) {
		return conn
	}
	var chain []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, p := range strings.Split(h, ",") {
			chain = append(chain, strings.TrimSpace(p))
		}
	}
	if len(chain) == 0 || len(chain) > 50 {
		return conn
	}
	for i := len(chain) - 1; i >= 0; i-- {
		ip := net.ParseIP(chain[i])
		if ip == nil {
			return conn
		}
		if !trusted(ip) {
			return ip.String()
		}
	}
	return conn
}
