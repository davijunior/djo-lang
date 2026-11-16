//go:build !nodb

package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// O parser de frases do /ask: função pura (frase + nível + política -> plano de consulta, ou erro que ensina).
//
// Linguagem controlada, sem modelo: inglês e português, reconhecida por tabelas (askLexicons) que dá para
// ampliar sem mexer na lógica. A gramática nasce SEM verbos de escrita: eles são recusados.
//
//	FRASE  = VERBO [artigos] TIPO { CLÁUSULA }
//	VERBO  = list | show | give me | find | count | how many ...   /   liste | mostre | me dê | conte | quantos ...
//	CLÁUSULA (datas) = from D [to D] | since D | until D | before D | after D | on D | in AAAA | between D and D
//	CLÁUSULA (campos) = CAMPO [OPERADOR] VALOR | with|where CAMPO OPERADOR VALOR | tagged X
//	CLÁUSULA (resto)  = sorted by CAMPO [desc] | newest first | oldest first | first N | page N
//
// Garantias:
//   - o idioma vem do primeiro verbo; palavras do outro idioma = 422 que ensina (kind "mixed_language");
//   - tipo ou campo que o nível não enxerga é tratado EXATAMENTE como um que não existe;
//   - datas: ISO sempre; dd/mm/aaaa só em português; sem expressões relativas;
//   - a frase digitada nunca é devolvida: só uma palavra reduzida a [a-z0-9_-] pode aparecer numa mensagem;
//   - o parser só devolve nomes de campo que o nível enxerga (o motor confere de novo).

const (
	askMaxPhrase = 300 // bytes
	askMaxToks   = 60
	askMaxFilter = 6
)

type askKW struct {
	words string // palavras separadas por espaço, sem acento e em minúsculas
	act   string
}

type askLex struct {
	code     string
	verbs    []askKW
	fillers  []string
	clauses  []askKW
	ops      []askKW
	rangeEnd []string
	and      []string
	dirAsc   []string
	dirDesc  []string
	yes, no  []string
	skipN    []string // palavras depois de "first 5 ..."
	glue     []string // palavras de ligação que podem ser ignoradas entre as cláusulas ("que", "o", "the"...)
	nouns    []string // "contém a palavra X", "contains the word X": palavras antes do valor de texto
	firstN   []string // "os 5 primeiros posts": palavra depois do número
}

var askLexicons = []*askLex{
	{
		code: "en",
		verbs: []askKW{
			{"list", "list"}, {"show me", "list"}, {"show", "list"}, {"give me", "list"}, {"get", "list"},
			{"find", "list"}, {"search", "list"}, {"what are", "list"}, {"count", "count"},
			{"how many", "count"}, {"number of", "count"}, {"display", "list"}, {"see", "list"}, {"fetch", "list"},
			{"tell me", "list"}, {"i want to see", "list"}, {"i would like to see", "list"}, {"i want", "list"},
			{"let me see", "list"}, {"bring me", "list"},
		},
		fillers: []string{"a", "an", "the", "all", "list", "of", "me", "some", "my", "number", "total", "listing"},
		clauses: []askKW{
			{"from", "from"}, {"since", "since"}, {"starting from", "since"}, {"until", "until"}, {"up to", "until"},
			{"before", "before"}, {"after", "after"}, {"on", "on"}, {"in", "in"}, {"between", "between"},
			{"with tag", "tagged"}, {"with the tag", "tagged"}, {"tagged", "tagged"},
			{"with", "where"}, {"where", "where"}, {"whose", "where"},
			{"sorted by", "sort"}, {"ordered by", "sort"}, {"order by", "sort"}, {"sort by", "sort"},
			{"newest first", "newest"}, {"latest first", "newest"}, {"oldest first", "oldest"},
			{"first", "limit"}, {"top", "limit"}, {"limit", "limit"}, {"page", "page"},
			{"and", "and"}, {"also", "and"}, {"etiqueta", "tagged"},
			{"tagged with", "tagged"}, {"tag", "tagged"}, {"newest", "newest"}, {"latest", "newest"}, {"most recent", "newest"},
			{"oldest", "oldest"},
		},
		ops: []askKW{
			{"is", "eq"}, {"equals", "eq"}, {"equal to", "eq"}, {"is equal to", "eq"}, {"equal", "eq"}, {"=", "eq"},
			{"contains", "contains"}, {"contain", "contains"}, {"containing", "contains"}, {"includes", "contains"},
			{"start with", "starts"}, {"begin with", "starts"}, {"starts with", "starts"}, {"starting with", "starts"}, {"begins with", "starts"},
			{"greater than", "gt"}, {"more than", "gt"}, {">", "gt"}, {"at least", "gte"}, {">=", "gte"},
			{"less than", "lt"}, {"fewer than", "lt"}, {"<", "lt"}, {"at most", "lte"}, {"<=", "lte"},
			{"between", "between"},
		},
		rangeEnd: []string{"to", "until", "through", "thru"},
		and:      []string{"and"},
		dirAsc:   []string{"asc", "ascending"},
		dirDesc:  []string{"desc", "descending"},
		yes:      []string{"true", "yes"},
		no:       []string{"false", "no"},
		skipN:    []string{"results", "items", "result", "item"},
		glue:     []string{"the", "a", "an", "that", "which", "who", "are", "is", "have", "has", "having", "also", "too"},
		nouns:    []string{"word", "text", "term", "the", "a"},
		firstN:   []string{"first", "top"},
	},
	{
		code: "pt",
		verbs: []askKW{
			{"liste", "list"}, {"listar", "list"}, {"mostre", "list"}, {"mostrar", "list"}, {"me de", "list"},
			{"me da", "list"}, {"me mostre", "list"}, {"busque", "list"}, {"buscar", "list"}, {"encontre", "list"},
			{"quais", "list"}, {"conte", "count"}, {"contar", "count"}, {"quantos", "count"}, {"quantas", "count"},
			{"lista", "list"}, {"mostra", "list"}, {"quero ver", "list"}, {"quero", "list"}, {"traz", "list"}, {"traga", "list"},
			{"me traz", "list"}, {"me traga", "list"}, {"me mostra", "list"}, {"me passa", "list"}, {"me passe", "list"},
			{"gostaria de ver", "list"}, {"veja", "list"}, {"ver", "list"}, {"quantidade de", "count"}, {"numero de", "count"},
		},
		fillers: []string{"a", "o", "as", "os", "um", "uma", "uns", "umas", "todos", "todas", "lista", "listagem", "de", "do", "da", "dos", "das", "me", "numero", "total", "sao"},
		clauses: []askKW{
			{"de", "from"}, {"desde", "since"}, {"a partir de", "since"}, {"ate", "until"}, {"ate o dia", "until"},
			{"antes de", "before"}, {"depois de", "after"}, {"no dia", "on"}, {"em", "in"}, {"entre", "between"},
			{"com a tag", "tagged"}, {"com tag", "tagged"}, {"com a etiqueta", "tagged"}, {"marcados com", "tagged"},
			{"com", "where"}, {"onde", "where"}, {"cujo", "where"}, {"cuja", "where"},
			{"ordenado por", "sort"}, {"ordenados por", "sort"}, {"ordenadas por", "sort"}, {"ordene por", "sort"}, {"ordem de", "sort"},
			{"mais recentes primeiro", "newest"}, {"mais novos primeiro", "newest"}, {"mais antigos primeiro", "oldest"},
			{"primeiros", "limit"}, {"primeiras", "limit"}, {"top", "limit"}, {"limite", "limit"}, {"pagina", "page"},
			{"e", "and"}, {"tambem", "and"}, {"etiqueta", "tagged"}, {"etiquetas", "tagged"}, {"com etiqueta", "tagged"},
			{"marcado com", "tagged"}, {"marcada com", "tagged"}, {"marcadas com", "tagged"}, {"marcado como", "tagged"},
			{"marcados como", "tagged"}, {"marcada como", "tagged"}, {"marcadas como", "tagged"}, {"tag", "tagged"},
			{"mais recentes", "newest"}, {"mais recente", "newest"}, {"mais novos", "newest"}, {"mais novas", "newest"},
			{"mais antigos", "oldest"}, {"mais antigas", "oldest"},
		},
		ops: []askKW{
			{"e", "eq"}, {"e igual a", "eq"}, {"igual a", "eq"}, {"igual", "eq"}, {"seja", "eq"}, {"=", "eq"},
			{"contem", "contains"}, {"contendo", "contains"}, {"contenha", "contains"}, {"inclui", "contains"},
			{"comece com", "starts"}, {"inicia com", "starts"}, {"iniciando com", "starts"},
			{"comeca com", "starts"}, {"comecando com", "starts"}, {"comeca por", "starts"}, {"comecando por", "starts"},
			{"maior que", "gt"}, {">", "gt"}, {"pelo menos", "gte"}, {">=", "gte"}, {"menor que", "lt"}, {"<", "lt"},
			{"no maximo", "lte"}, {"<=", "lte"}, {"entre", "between"},
		},
		rangeEnd: []string{"ate", "a"},
		and:      []string{"e"},
		dirAsc:   []string{"asc", "crescente"},
		dirDesc:  []string{"desc", "decrescente"},
		yes:      []string{"true", "sim", "verdadeiro"},
		no:       []string{"false", "nao", "falso"},
		skipN:    []string{"resultados", "itens", "resultado", "item"},
		glue:     []string{"o", "a", "os", "as", "um", "uma", "uns", "umas", "que", "quais", "qual", "tenha", "tenham", "tem", "tiver", "tiverem", "possua", "possuem", "seja", "sejam", "esta", "estao", "sao", "ainda"},
		nouns:    []string{"palavra", "texto", "termo", "o", "a"},
		firstN:   []string{"primeiros", "primeiras"},
	},
}

// verbos de escrita: reconhecidos nos dois idiomas só para recusar com clareza.
var askWriteWords = []string{
	"delete", "remove", "drop", "update", "insert", "create", "add", "change", "edit", "truncate", "grant", "revoke", "alter", "destroy",
	"apague", "apagar", "remova", "remover", "exclua", "excluir", "atualize", "atualizar", "altere", "alterar",
	"insira", "inserir", "crie", "criar", "adicione", "adicionar", "edite", "editar", "modifique", "modificar", "destrua",
}

// ---- mensagens (por idioma) ----

var askMsgs = map[string]map[string]string{
	"en": {
		"empty":          "Ask a question, for example: %s",
		"too_long":       "The question is too long (at most %d characters).",
		"bad_chars":      "The question has characters that are not accepted.",
		"bad_quote":      "A quotation mark was opened and not closed.",
		"mixed_language": "Please use one language per question: this one started in English, but has Portuguese words. Example: %s",
		"not_accepted":   "Only questions that read data are accepted here. Writing is not accepted.",
		"not_understood": "I did not understand%s. You can ask about: %s. Example: %s",
		"unknown_field":  "I did not understand%s. Fields you can use for %s: %s.",
		"need_iso":       "Please write dates as YYYY-MM-DD, for example 2025-03-30.",
		"bad_date":       "That date does not exist. Write dates as YYYY-MM-DD.",
		"need_date":      "A date was expected here (YYYY-MM-DD).",
		"need_main":      "Which date field? Name it first, for example: %s.",
		"need_value":     "A value was expected here. Text goes in quotes, for example: title contains \"news\".",
		"bad_number":     "A number was expected here.",
		"too_many":       "Too many conditions in one question.",
		"understood":     "I understood: %s.",
		"stopped":        "I did not understand from \"%s\".",
		"stopped_nw":     "I did not understand the rest.",
		"try":            "Try: %s",
		"fields":         "Fields: %s.",
		"unsup_or":       "OR is not supported: conditions are combined with AND only. Ask two separate questions instead.",
		"unsup_not":      "Negation (not, without, except) is not supported yet. Describe what you want to include instead.",
		"unsup_month":    "Month names are not supported. Write dates as YYYY-MM-DD, or use a year (in 2025).",
		"unsup_relative": "Relative dates (today, last week...) are not supported. Write dates as YYYY-MM-DD.",
	},
	"pt": {
		"empty":          "Faça uma pergunta, por exemplo: %s",
		"too_long":       "A pergunta é longa demais (no máximo %d caracteres).",
		"bad_chars":      "A pergunta tem caracteres que não são aceitos.",
		"bad_quote":      "Uma aspa foi aberta e não foi fechada.",
		"mixed_language": "Use um idioma só por pergunta: esta começou em português, mas tem palavras em inglês. Exemplo: %s",
		"not_accepted":   "Aqui só são aceitas perguntas que leem dados. Escrever não é aceito.",
		"not_understood": "Não entendi%s. Você pode perguntar sobre: %s. Exemplo: %s",
		"unknown_field":  "Não entendi%s. Campos que você pode usar em %s: %s.",
		"need_iso":       "Escreva as datas como AAAA-MM-DD, por exemplo 2025-03-30.",
		"bad_date":       "Essa data não existe. Escreva as datas como AAAA-MM-DD ou dd/mm/aaaa.",
		"need_date":      "Era esperada uma data aqui (AAAA-MM-DD ou dd/mm/aaaa).",
		"need_main":      "Qual campo de data? Diga o nome antes, por exemplo: %s.",
		"need_value":     "Era esperado um valor aqui. Texto vai entre aspas, por exemplo: titulo contem \"novidade\".",
		"bad_number":     "Era esperado um número aqui.",
		"too_many":       "Condições demais em uma pergunta.",
		"understood":     "Entendi: %s.",
		"stopped":        "Não entendi a partir de \"%s\".",
		"stopped_nw":     "Não entendi o resto.",
		"try":            "Tente: %s",
		"fields":         "Campos: %s.",
		"unsup_or":       "O OU não existe: as condições se combinam só com E. Faça duas perguntas separadas.",
		"unsup_not":      "A negação (não, sem, exceto) não é aceita ainda. Descreva o que você quer incluir.",
		"unsup_month":    "Nomes de meses por extenso não são aceitos (meses por extenso). Escreva as datas como AAAA-MM-DD ou dd/mm/aaaa, ou use um ano (em 2025).",
		"unsup_relative": "Não aceito datas relativas (hoje, semana passada...). Escreva as datas como AAAA-MM-DD ou dd/mm/aaaa.",
	},
}

var askExample = map[string]string{
	"en": "list %s from 2025-01-01 to 2025-12-31",
	"pt": "liste %s de 2025-01-01 até 2025-12-31",
}

// ---- normalização e tokens ----

var askFold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "ç", "c", "ñ", "n",
)

func askNorm(s string) string { return askFold.Replace(strings.ToLower(s)) }

type askTok struct {
	text   string // normalizado (minúsculas, sem acento); vazio se entre aspas
	raw    string // como foi digitado (só entre aspas)
	quoted bool
}

var askQuotes = map[rune]rune{'"': '"', '\'': '\'', '“': '”', '‘': '’', '«': '»'}

func askTokenize(s string) ([]askTok, string) {
	rs := []rune(s)
	var toks []askTok
	for i := 0; i < len(rs); {
		r := rs[i]
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			i++
			continue
		}
		if closeQ, ok := askQuotes[r]; ok {
			j := i + 1
			for j < len(rs) && rs[j] != closeQ {
				j++
			}
			if j >= len(rs) {
				return nil, "bad_quote"
			}
			toks = append(toks, askTok{raw: string(rs[i+1 : j]), quoted: true})
			i = j + 1
			continue
		}
		j := i
		for j < len(rs) && rs[j] != ' ' && rs[j] != '\t' && rs[j] != '\n' && rs[j] != '\r' {
			j++
		}
		w := string(rs[i:j])
		i = j
		hadComma := false
		for strings.HasSuffix(w, ",") || strings.HasSuffix(w, ";") || strings.HasSuffix(w, "?") || strings.HasSuffix(w, "!") || strings.HasSuffix(w, ".") || strings.HasSuffix(w, ":") {
			hadComma = hadComma || strings.HasSuffix(w, ",") || strings.HasSuffix(w, ";")
			w = w[:len(w)-1]
		}
		w = strings.TrimPrefix(w, "(")
		w = strings.TrimSuffix(w, ")")
		if w != "" {
			toks = append(toks, askTok{text: askNorm(w), raw: w})
		}
		if hadComma {
			toks = append(toks, askTok{text: ","})
		}
		if len(toks) > askMaxToks {
			return nil, "too_long"
		}
	}
	return toks, ""
}

// ---- vocabulário do nível ----

type askVocab struct {
	c      *askConfig
	level  string
	types  map[string]string            // palavras (normalizadas) -> nome do tipo
	fields map[string]map[string]string // tipo -> palavras -> campo (só os que o nível enxerga)
	words  map[string]bool              // todas as palavras do site (nunca contam como "do outro idioma")
	names  []string                     // tipos visíveis, ordenados
}

func (c *askConfig) vocabFor(level string) *askVocab {
	c.vmu.Lock()
	defer c.vmu.Unlock()
	if v, ok := c.vocabs[level].(*askVocab); ok {
		return v
	}
	if c.vocabs == nil {
		c.vocabs = map[string]any{}
	}
	v := c.vocab(level)
	c.vocabs[level] = v
	return v
}

func (c *askConfig) vocab(level string) *askVocab {
	v := &askVocab{c: c, level: level, types: map[string]string{}, fields: map[string]map[string]string{}, words: map[string]bool{}}
	add := func(m map[string]string, phrase, target string) {
		p := strings.Join(strings.Fields(askNorm(phrase)), " ")
		if p == "" {
			return
		}
		if _, dup := m[p]; !dup {
			m[p] = target
		}
		for _, w := range strings.Fields(p) {
			v.words[w] = true
		}
	}
	for _, name := range c.typeNames(level) {
		t := c.types[name]
		vw := c.view(level, name)
		v.names = append(v.names, name)
		for _, n := range append([]string{name, name + "s"}, t.aliases...) {
			add(v.types, strings.ReplaceAll(n, "_", " "), name)
			add(v.types, n, name)
		}
		fm := map[string]string{}
		for _, f := range vw.fields {
			add(fm, f, f)
			add(fm, strings.ReplaceAll(f, "_", " "), f)
		}
		for _, a := range t.fieldAliases {
			if vw.has(a.field) {
				add(fm, a.name, a.field)
			}
		}
		// nomes comuns em português, só para campos que o nível enxerga; o que o ask.djo declarou tem prioridade
		for _, f := range vw.fields {
			for _, w := range askDefaultFieldAliases[f] {
				add(fm, w, f)
			}
		}
		v.fields[name] = fm
	}
	return v
}

// askDefaultFieldAliases: apelidos em português para nomes de campo comuns (valem só se o campo existe e o nível o enxerga;
// um nome ou apelido declarado no ask.djo nunca é trocado por estes). Não há dicionário para tipos.
var askDefaultFieldAliases = map[string][]string{
	"title": {"titulo"}, "text": {"texto"}, "body": {"corpo", "conteudo"}, "content": {"conteudo"}, "tags": {"etiquetas"},
	"status": {"situacao"}, "name": {"nome"}, "description": {"descricao"}, "views": {"visualizacoes"}, "author": {"autor"},
	"category": {"categoria"}, "price": {"preco"}, "date": {"data"},
	"created_at": {"criado em", "criado"}, "updated_at": {"atualizado em", "atualizado"}, "published_at": {"publicado em", "publicado"},
}

// longest devolve o alvo da frase mais longa de m que começa em toks[i] (e quantos tokens usou).
func askLongest(m map[string]string, toks []askTok, i int) (string, int) {
	best, bn := "", 0
	var sb []string
	for k := i; k < len(toks) && k-i < 5; k++ {
		if toks[k].quoted {
			break
		}
		sb = append(sb, toks[k].text)
		if t, ok := m[strings.Join(sb, " ")]; ok {
			best, bn = t, k-i+1
		}
	}
	return best, bn
}

func askMatchKW(list []askKW, toks []askTok, i int) (string, int) {
	best, bn := "", 0
	for _, kw := range list {
		ws := strings.Fields(kw.words)
		if len(ws) <= bn || i+len(ws) > len(toks) {
			continue
		}
		ok := true
		for k, w := range ws {
			if toks[i+k].quoted || toks[i+k].text != w {
				ok = false
				break
			}
		}
		if ok {
			best, bn = kw.act, len(ws)
		}
	}
	return best, bn
}

func askIn(list []string, t askTok) bool {
	if t.quoted {
		return false
	}
	for _, w := range list {
		if t.text == w {
			return true
		}
	}
	return false
}

// palavras que só existem em um idioma (para detectar mistura)
var askOnly map[string]map[string]bool

func init() {
	sets := map[string]map[string]bool{}
	for _, lx := range askLexicons {
		s := map[string]bool{}
		for _, l := range [][]askKW{lx.verbs, lx.clauses, lx.ops} {
			for _, kw := range l {
				for _, w := range strings.Fields(kw.words) {
					s[w] = true
				}
			}
		}
		for _, l := range [][]string{lx.fillers, lx.rangeEnd, lx.and, lx.dirAsc, lx.dirDesc, lx.yes, lx.no, lx.skipN} {
			for _, w := range l {
				s[w] = true
			}
		}
		sets[lx.code] = s
	}
	askOnly = map[string]map[string]bool{}
	for code, s := range sets {
		only := map[string]bool{}
		for w := range s {
			shared := false
			for oc, os := range sets {
				if oc != code && os[w] {
					shared = true
				}
			}
			if !shared {
				only[w] = true
			}
		}
		askOnly[code] = only
	}
}

// ---- o parser ----

type askParsed struct {
	Lang string
	Plan askPlan
}

type askP struct {
	lx   *askLex
	voc  *askVocab
	toks []askTok
	i    int
	typ  string
	typW string // o tipo como a pessoa o escreveu, quando está no plural
	fm   map[string]string
	view *askView
	plan askPlan
	lang string
	nf   int

	ctx        string // o tipo de cláusula em andamento: date, text, tag, number, enum, order, limit
	stop       int    // índice do token onde o erro aconteceu
	lastKey    string // a mensagem (chave) do último erro
	afterWhere bool   // acabou de ler "onde/where": o próximo precisa ser um campo
}

func (p *askP) msg(key string, args ...any) string {
	return askSprintf(askMsgs[p.lang][key], args...)
}

func askSprintf(f string, args ...any) string {
	out := f
	for _, a := range args {
		switch v := a.(type) {
		case string:
			out = strings.Replace(out, "%s", v, 1)
		case int:
			out = strings.Replace(out, "%d", strconv.Itoa(v), 1)
		}
	}
	return out
}

func (p *askP) err(kind, key string, args ...any) error {
	p.lastKey, p.stop = key, p.i
	return askErr{Kind: kind, Msg: p.msg(key, args...), Lang: p.lang}
}

// quanto da palavra digitada pode aparecer numa mensagem: só [a-z0-9_-], até 30 caracteres.
func askSafeWord(s string) string {
	if s == "" || len(s) > 30 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return ""
		}
	}
	return s
}

func (p *askP) cur() (askTok, bool) {
	if p.i < len(p.toks) {
		return p.toks[p.i], true
	}
	return askTok{}, false
}

func (p *askP) exampleType() string {
	if len(p.voc.names) > 0 {
		return p.typeLabel(p.voc.names[0])
	}
	return "items"
}

// typeLabel é como o tipo aparece em exemplos e sugestões: em português, o primeiro apelido
// declarado no plural (termina em "s"), senão o nome do tipo no plural; em inglês, o nome no plural.
func (p *askP) typeLabel(name string) string {
	if p.typW != "" && name == p.typ {
		return p.typW
	}
	if p.lang == "pt" {
		for _, a := range p.voc.c.types[name].aliases {
			if strings.HasSuffix(a, "s") && a != name+"s" {
				return strings.ReplaceAll(a, "_", " ")
			}
		}
	}
	return name + "s"
}

// singularAlias diz se a frase do tipo é um apelido no singular (não termina em "s") que só este tipo usa.
func (p *askP) singularAlias(phrase string) bool {
	if phrase == "" || strings.HasSuffix(phrase, "s") {
		return false
	}
	owner := ""
	for _, n := range p.voc.names {
		for _, a := range p.voc.c.types[n].aliases {
			if strings.Join(strings.Fields(askNorm(strings.ReplaceAll(a, "_", " "))), " ") == phrase {
				if owner != "" && owner != n {
					return false
				}
				owner = n
			}
		}
	}
	return owner == p.typ
}

// notUnderstood é a resposta única para tudo que não existe OU que o nível não enxerga.
func (p *askP) notUnderstood() error {
	p.lastKey, p.stop = "not_understood", p.i
	where := ""
	if t, ok := p.cur(); ok && !t.quoted {
		if w := askSafeWord(t.text); w != "" {
			where = " \"" + w + "\""
		}
	}
	types := strings.Join(p.voc.names, ", ")
	ex := askSprintf(askExample[p.lang], p.exampleType())
	return p.err("not_understood", "not_understood", where, types, ex)
}

func (p *askP) unknownField() error {
	p.lastKey, p.stop = "unknown_field", p.i
	where := ""
	if t, ok := p.cur(); ok && !t.quoted {
		if w := askSafeWord(t.text); w != "" {
			where = " \"" + w + "\""
		}
	}
	var fs []string
	if p.view != nil {
		fs = p.view.fields
	}
	return p.err("not_understood", "unknown_field", where, p.typ, strings.Join(fs, ", "))
}

var (
	askReISO   = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	askReSlash = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)
	askReShort = regexp.MustCompile(`^\d{1,2}/\d{1,2}$`)
	askReYear  = regexp.MustCompile(`^\d{4}$`)
	askReNum   = regexp.MustCompile(`^-?\d{1,15}(\.\d{1,6})?$`)
)

// parsePhrase transforma a frase em plano. level é o nível já decidido pelo chamador (token/sessão).
func (c *askConfig) parsePhrase(level, phrase string) (*askParsed, error) {
	return c.parsePhraseLang(level, phrase, "")
}

// parsePhraseLang é o parsePhrase com um idioma sugerido ("en", "pt" ou ""): ele NÃO muda como a frase é lida
// (o idioma da frase vem do verbo), só em que idioma saem as mensagens quando não dá para saber.
func (c *askConfig) parsePhraseLang(level, phrase, hint string) (*askParsed, error) {
	voc := c.vocabFor(level)
	if hint != "en" && hint != "pt" {
		hint = ""
	}
	p := &askP{voc: voc, lang: "en"}
	if hint != "" {
		p.lang = hint
	}
	if len(voc.names) == 0 {
		return nil, p.err("not_understood", "empty", askSprintf(askExample["en"], "items"))
	}
	if len(phrase) > askMaxPhrase {
		return nil, p.err("too_long", "too_long", askMaxPhrase)
	}
	if !utf8.ValidString(phrase) {
		return nil, p.err("bad_phrase", "bad_chars")
	}
	for _, r := range phrase {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0xfffd {
			return nil, p.err("bad_phrase", "bad_chars")
		}
	}
	toks, why := askTokenize(phrase)
	if why != "" {
		return nil, p.err("bad_phrase", why, askMaxToks)
	}
	if len(toks) == 0 {
		return nil, p.err("not_understood", "empty", askSprintf(askExample["en"], p.exampleType()))
	}
	p.toks = toks
	// verbo: define o idioma
	var kind string
	for _, lx := range askLexicons {
		if act, n := askMatchKW(lx.verbs, toks, 0); n > 0 {
			p.lx, p.lang, kind, p.i = lx, lx.code, act, n
			break
		}
	}
	// escrita, em qualquer idioma, antes de qualquer outra coisa
	for _, t := range toks {
		if !t.quoted && !voc.words[t.text] {
			for _, w := range askWriteWords {
				if t.text == w {
					if p.lx != nil {
						return nil, p.err("not_accepted", "not_accepted")
					}
					p.lang = "pt"
					if askIsEnglishWrite(w) {
						p.lang = "en"
					}
					return nil, p.err("not_accepted", "not_accepted")
				}
			}
		}
	}
	var kindV string
	if p.lx == nil {
		kindV = "list"
		p.lx = askVerbless(toks, voc)
		if p.lx != nil {
			p.lang, p.i = p.lx.code, 0
		}
	} else {
		kindV = kind
	}
	if p.lx == nil {
		ex := func(l string) string { return askSprintf(askExample[l], p.exampleType()) }
		types := strings.Join(voc.names, ", ")
		if hint != "" {
			return nil, askErr{Kind: "not_understood", Lang: hint, Msg: askSprintf(askMsgs[hint]["not_understood"], "", types, ex(hint))}
		}
		return nil, askErr{Kind: "not_understood", Msg: askSprintf(askMsgs["en"]["not_understood"], "", types, ex("en")) +
			"\n" + askSprintf(askMsgs["pt"]["not_understood"], "", types, ex("pt"))}
	}
	kind = kindV
	// mistura de idiomas
	other := "pt"
	if p.lang == "pt" {
		other = "en"
	}
	for _, t := range toks {
		if !t.quoted && askOnly[other][t.text] && !voc.words[t.text] {
			return nil, p.err("mixed_language", "mixed_language", askSprintf(askExample[p.lang], p.exampleType()))
		}
	}
	// artigos e enchimento, depois o tipo
	var pre askPlan
	var preOrder string
	for p.i < len(toks) {
		if _, n := askLongest(voc.types, toks, p.i); n > 0 {
			break
		}
		if askIn(p.lx.fillers, toks[p.i]) || askIn(p.lx.glue, toks[p.i]) {
			p.i++
			continue
		}
		act, n := askMatchKW(p.lx.clauses, toks, p.i)
		// "first 5 posts": o limite pode vir antes do tipo
		if act == "limit" && n > 0 && p.i+n < len(toks) {
			p.i += n
			v, e := p.number()
			if e != nil {
				return nil, e
			}
			if v < 1 || v > 1_000_000 {
				return nil, p.err("bad_value", "bad_number")
			}
			pre.Limit = v
			continue
		}
		// "os 5 primeiros posts"
		if t := toks[p.i]; !t.quoted && askReNum.MatchString(t.text) && !strings.Contains(t.text, ".") && p.i+1 < len(toks) && askIn(p.lx.firstN, toks[p.i+1]) {
			v, e := p.number()
			if e != nil || v < 1 || v > 1_000_000 {
				return nil, p.err("bad_value", "bad_number")
			}
			p.i++
			pre.Limit = v
			continue
		}
		// "os posts mais recentes" / "list the newest posts"
		if (act == "newest" || act == "oldest") && n > 0 {
			preOrder = act
			p.i += n
			continue
		}
		break
	}
	tn, n := askLongest(voc.types, toks, p.i)
	if n == 0 {
		return nil, p.notUnderstood()
	}
	p.i += n
	p.typ, p.fm, p.view = tn, voc.fields[tn], c.view(level, tn)
	if w := toks[p.i-n : p.i]; len(w) > 0 {
		var ws []string
		for _, tk := range w {
			ws = append(ws, tk.text)
		}
		if j := strings.Join(ws, " "); strings.HasSuffix(j, "s") && !w[0].quoted {
			var rs []string
			for _, tk := range w {
				rs = append(rs, strings.ToLower(tk.raw))
			}
			p.typW = strings.Join(rs, " ")
		}
	}
	if p.view == nil {
		return nil, p.notUnderstood()
	}
	p.plan = askPlan{Type: tn, CountOnly: kind == "count", Limit: pre.Limit}
	if preOrder != "" {
		m := p.mainField()
		if m == "" {
			return nil, p.decorate(p.err("not_understood", "need_main", p.view.fields[0]))
		}
		p.plan.Order = &askOrder{m, preOrder == "newest"}
	}
	// "mostre o post X", "show post \"X\"": um item específico (o texto longo só vem assim)
	if kind != "count" && pre.Limit == 0 && preOrder == "" {
		if v, ok := p.showValue(toks[p.i-n : p.i]); ok {
			p.plan = askPlan{Type: tn, Show: true, ShowValue: v}
			p.i = len(toks)
			return &askParsed{Lang: p.lang, Plan: p.plan}, nil
		}
	}
	if err := p.clauses(); err != nil {
		return nil, p.decorate(err)
	}
	return &askParsed{Lang: p.lang, Plan: p.plan}, nil
}

// showValue diz se o que sobra depois do tipo é o nome de UM item ("show post welcome", "mostre o post \"bem-vindo\""):
// o tipo precisa ter show_by que o nível enxergue e vir no singular, e o resto não pode ser uma cláusula.
// Entre aspas, qualquer texto; sem aspas, só se o tipo foi escrito no singular e as palavras não são do vocabulário do site.
func (p *askP) showValue(typToks []askTok) (string, bool) {
	t := p.voc.c.types[p.typ]
	capable := false
	for _, f := range t.showBy {
		capable = capable || p.view.has(f)
	}
	if !capable || p.i >= len(p.toks) {
		return "", false
	}
	var words []string
	for _, tk := range typToks {
		words = append(words, tk.text)
	}
	phrase := strings.Join(words, " ")
	base := askNorm(strings.ReplaceAll(p.typ, "_", " "))
	if phrase == base+"s" {
		return "", false // "show posts X" é lista
	}
	singular := phrase == base || p.singularAlias(phrase)
	rest := p.toks[p.i:]
	if rest[0].quoted {
		if len(rest) != 1 || rest[0].raw == "" || len(rest[0].raw) > 200 {
			return "", false
		}
		return rest[0].raw, true
	}
	if !singular || !p.bareOK(rest[0]) || len(rest) > 12 {
		return "", false
	}
	if _, n := askMatchKW(p.lx.clauses, p.toks, p.i); n > 0 {
		return "", false
	}
	if _, n := askMatchKW(p.lx.ops, p.toks, p.i); n > 0 {
		return "", false
	}
	var parts []string
	for _, tk := range rest {
		if tk.quoted || tk.text == "," || tk.raw == "" {
			return "", false
		}
		parts = append(parts, tk.raw)
	}
	v := strings.Join(parts, " ")
	if len(v) > 200 {
		return "", false
	}
	return v, true
}

// askVerbless aceita frases sem verbo quando não há dúvida do idioma: começam por "first N"/"primeiros N"
// ou pelo nome de um tipo, e o resto tem alguma palavra que só existe em um dos idiomas.
func askVerbless(toks []askTok, voc *askVocab) *askLex {
	if len(toks) == 0 || toks[0].quoted {
		return nil
	}
	starts := false
	for _, lx := range askLexicons {
		if act, n := askMatchKW(lx.clauses, toks, 0); act == "limit" && n > 0 && n < len(toks) && !toks[n].quoted && askReNum.MatchString(toks[n].text) {
			if lx.code == "en" || toks[0].text != "top" {
				return lxOnly(toks, voc, lx, true)
			}
		}
	}
	if _, n := askLongest(voc.types, toks, 0); n > 0 {
		starts = true
	}
	// "o post X", "the post X": artigos antes do tipo também valem
	if !starts {
		i := 0
		for i < len(toks) && !toks[i].quoted && askFillerAny(toks[i]) {
			i++
		}
		if i > 0 && i < len(toks) {
			if _, n := askLongest(voc.types, toks, i); n > 0 {
				starts = true
			}
		}
	}
	if !starts {
		return nil
	}
	return lxOnly(toks, voc, nil, false)
}

// lxOnly escolhe o idioma: o do cue (first/primeiros) ou, sem cue, o único idioma que tem palavras exclusivas na frase.
func lxOnly(toks []askTok, voc *askVocab, cue *askLex, cueIsLimit bool) *askLex {
	found := map[string]bool{}
	for _, t := range toks {
		if t.quoted || voc.words[t.text] {
			continue
		}
		for code, only := range askOnly {
			if only[t.text] {
				found[code] = true
			}
		}
	}
	if cueIsLimit && cue != nil {
		if cue.code == "en" && (found["pt"] || (found["en"] && found["pt"])) {
			return nil
		}
		if len(found) == 0 || found[cue.code] && len(found) == 1 {
			return cue
		}
		return nil
	}
	if len(found) != 1 {
		return nil
	}
	for _, lx := range askLexicons {
		if found[lx.code] {
			return lx
		}
	}
	return nil
}

func askIsEnglishWrite(w string) bool {
	for _, x := range askWriteWords[:14] {
		if x == w {
			return true
		}
	}
	return false
}

func (p *askP) mainField() string {
	for _, name := range p.view.fields {
		if f := p.voc.c.types[p.typ].fields[name]; f != nil && f.main {
			return name
		}
	}
	return ""
}

func (p *askP) fieldKind(name string) string {
	if f := p.voc.c.types[p.typ].fields[name]; f != nil {
		return f.kind
	}
	return ""
}

func (p *askP) clauses() error {
	for p.i < len(p.toks) {
		p.ctx = ""
		t := p.toks[p.i]
		if !t.quoted && (t.text == "," || askIn(p.lx.and, t)) {
			p.i++
			continue
		}
		if !t.quoted {
			if fname, n := askLongest(p.fm, p.toks, p.i); n > 0 {
				p.i += n
				p.afterWhere = false
				if err := p.fieldClause(fname); err != nil {
					return err
				}
				continue
			}
		}
		act, n := "", 0
		if !t.quoted {
			act, n = askMatchKW(p.lx.clauses, p.toks, p.i)
		}
		if n == 0 {
			if !t.quoted && askIn(p.lx.glue, t) { // "que", "o", "the", "tenha"...: só ligação
				p.i++
				continue
			}
			if p.afterWhere {
				return p.unknownField()
			}
			return p.notUnderstood()
		}
		p.i += n
		if act != "where" {
			p.afterWhere = false
		}
		if act == "and" { // "também", "also"
			continue
		}
		var err error
		switch act {
		case "from", "since", "until", "before", "after", "on", "in", "between":
			p.ctx = "date"
			m := p.mainField()
			if m == "" {
				return p.err("not_understood", "need_main", p.view.fields[0])
			}
			err = p.dateClause(m, act)
		case "where":
			p.afterWhere = true // "onde": o que vem depois é um campo (ou "tag", "o", "que"...)
		case "tagged":
			p.ctx = "tag"
			f := ""
			for _, name := range p.view.fields {
				if p.fieldKind(name) == "tags" {
					f = name
					break
				}
			}
			if f == "" {
				p.i--
				return p.notUnderstood()
			}
			err = p.addFilter(f, "has", 1)
		case "sort":
			p.ctx = "order"
			f, k := askLongest(p.fm, p.toks, p.i)
			if k == 0 {
				return p.unknownField()
			}
			p.i += k
			desc := false
			if c, ok := p.cur(); ok {
				if askIn(p.lx.dirDesc, c) {
					desc, p.i = true, p.i+1
				} else if askIn(p.lx.dirAsc, c) {
					p.i++
				}
			}
			p.plan.Order = &askOrder{f, desc}
		case "newest", "oldest":
			m := p.mainField()
			if m == "" {
				return p.err("not_understood", "need_main", p.view.fields[0])
			}
			p.plan.Order = &askOrder{m, act == "newest"}
		case "limit":
			p.ctx = "limit"
			v, e := p.number()
			if e != nil {
				return e
			}
			if v < 1 || v > 1_000_000 {
				return p.err("bad_value", "bad_number")
			}
			p.plan.Limit = v
			if c, ok := p.cur(); ok && (askIn(p.lx.skipN, c) || c.text == p.typ || c.text == p.typ+"s") {
				p.i++
			}
		case "page":
			p.ctx = "limit"
			v, e := p.number()
			if e != nil {
				return e
			}
			if v < 1 || v > 1_000_000 {
				return p.err("bad_value", "bad_number")
			}
			p.plan.Page = v
		default:
			return p.notUnderstood()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// bareOK: uma palavra solta (sem aspas) pode ser valor de texto/tag? Não, se for palavra da gramática,
// do vocabulário do site, "e/ou", negação ou pontuação.
func (p *askP) bareOK(t askTok) bool {
	if t.quoted || t.text == "" || t.text == "," || len(t.raw) > 60 || t.raw == "" {
		return false
	}
	if p.voc.words[t.text] || askIn(p.lx.and, t) || askIn(p.lx.glue, t) || askIn(p.lx.fillers, t) {
		return false
	}
	if askRefusalOf(t.text) != "" {
		return false
	}
	for _, lx := range askLexicons {
		for _, kw := range lx.clauses {
			switch kw.act {
			case "tagged", "where", "sort", "and", "page":
				if strings.Fields(kw.words)[0] == t.text {
					return false
				}
			}
		}
	}
	return true
}

func (p *askP) number() (int64, error) {
	t, ok := p.cur()
	if !ok || t.quoted || !askReNum.MatchString(t.text) || strings.Contains(t.text, ".") {
		return 0, p.err("bad_value", "bad_number")
	}
	v, err := strconv.ParseInt(t.text, 10, 64)
	if err != nil {
		return 0, p.err("bad_value", "bad_number")
	}
	p.i++
	return v, nil
}

// date lê uma data no token atual (ISO sempre; dd/mm/aaaa só em português).
func (p *askP) date() (Date, error) {
	t, ok := p.cur()
	if !ok || t.quoted {
		return Date{}, p.err("bad_value", "need_date")
	}
	if m := askReISO.FindStringSubmatch(t.text); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		dt, ok := makeDate(int64(y), int64(mo), int64(d))
		if !ok {
			return Date{}, p.err("bad_value", "bad_date")
		}
		p.i++
		return dt, nil
	}
	if m := askReSlash.FindStringSubmatch(t.text); m != nil {
		if p.lang != "pt" {
			return Date{}, p.err("bad_value", "need_iso")
		}
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		dt, ok := makeDate(int64(y), int64(mo), int64(d))
		if !ok {
			return Date{}, p.err("bad_value", "bad_date")
		}
		p.i++
		return dt, nil
	}
	if askReShort.MatchString(t.text) {
		return Date{}, p.err("bad_value", "need_iso")
	}
	return Date{}, p.err("bad_value", "need_date")
}

// yearTok: o token atual é um ano de 4 dígitos (1000–9999)?
func (p *askP) yearTok() (int64, bool) {
	t, ok := p.cur()
	if !ok || t.quoted || !askReYear.MatchString(t.text) {
		return 0, false
	}
	y, _ := strconv.ParseInt(t.text, 10, 64)
	if y < 1000 {
		return 0, false
	}
	return y, true
}

func (p *askP) isDateTok() bool {
	t, ok := p.cur()
	return ok && !t.quoted && (askReISO.MatchString(t.text) || askReSlash.MatchString(t.text) || askReShort.MatchString(t.text))
}

func (p *askP) addFilterVals(field, op string, vals ...any) error {
	p.nf++
	if p.nf > askMaxFilter {
		return p.err("bad_value", "too_many")
	}
	p.plan.Filters = append(p.plan.Filters, askFilter{Field: field, Op: op, Vals: vals})
	return nil
}

func (p *askP) dateClause(field, act string) error {
	switch act {
	case "from":
		// "de 2025" / "from 2025" = o ano; "de 2024 ate 2025" = do começo de 2024 ao fim de 2025
		if y1, ok := p.yearTok(); ok {
			p.i++
			if t, ok := p.cur(); ok && askIn(p.lx.rangeEnd, t) {
				p.i++
				y2, ok := p.yearTok()
				if !ok {
					return p.err("bad_value", "need_date")
				}
				p.i++
				a, _ := makeDate(y1, 1, 1)
				z, _ := makeDate(y2, 12, 31)
				return p.addFilterVals(field, "between", a, z)
			}
			return p.addFilterVals(field, "year", y1)
		}
		d1, err := p.date()
		if err != nil {
			return err
		}
		if t, ok := p.cur(); ok && askIn(p.lx.rangeEnd, t) {
			p.i++
			d2, err := p.date()
			if err != nil {
				return err
			}
			return p.addFilterVals(field, "between", d1, d2)
		}
		return p.addFilterVals(field, "from", d1)
	case "since", "until", "before", "after", "on":
		d, err := p.date()
		if err != nil {
			return err
		}
		op := map[string]string{"since": "from", "until": "to", "before": "before", "after": "after", "on": "eq"}[act]
		return p.addFilterVals(field, op, d)
	case "in":
		if t, ok := p.cur(); ok && !t.quoted && askReYear.MatchString(t.text) {
			y, _ := strconv.ParseInt(t.text, 10, 64)
			p.i++
			return p.addFilterVals(field, "year", y)
		}
		d, err := p.date()
		if err != nil {
			return err
		}
		return p.addFilterVals(field, "eq", d)
	case "between":
		d1, err := p.date()
		if err != nil {
			return err
		}
		if t, ok := p.cur(); !ok || !askIn(p.lx.and, t) {
			return p.err("bad_value", "need_date")
		}
		p.i++
		d2, err := p.date()
		if err != nil {
			return err
		}
		return p.addFilterVals(field, "between", d1, d2)
	}
	return p.notUnderstood()
}

// fieldClause: depois do nome de um campo vêm [operador] valor(es).
func (p *askP) fieldClause(field string) error {
	kind := p.fieldKind(field)
	if kind == "date" || kind == "datetime" {
		p.ctx = "date"
		p.skipGlue()
		if act, n := askMatchKW(p.lx.clauses, p.toks, p.i); n > 0 {
			switch act {
			case "from", "since", "until", "before", "after", "on", "in", "between":
				p.i += n
				return p.dateClause(field, act)
			}
		}
		if p.isDateTok() {
			return p.dateClause(field, "in")
		}
		if t, ok := p.cur(); ok && !t.quoted && askReYear.MatchString(t.text) {
			return p.dateClause(field, "in")
		}
		return p.err("bad_value", "need_date")
	}
	p.ctx = map[string]string{"tags": "tag", "text": "text", "number": "number", "enum": "enum", "bool": "enum"}[kind]
	// operador: "contém", "igual a", "maior que"... (palavras de ligação como "que" podem vir antes)
	op := ""
	for p.i < len(p.toks) {
		if act, n := askMatchKW(p.lx.ops, p.toks, p.i); n > 0 {
			op = act
			p.i += n
			break
		}
		if t, _ := p.cur(); askIn(p.lx.glue, t) {
			p.i++
			continue
		}
		break
	}
	switch kind {
	case "tags":
		return p.addFilter(field, "has", 1)
	case "text":
		if op == "" {
			return p.err("bad_value", "need_value")
		}
	case "number":
		if op == "" {
			op = "eq"
		}
	case "enum", "bool":
		if op == "" || op == "eq" {
			op = "eq"
		} else {
			return p.notUnderstood()
		}
	}
	if op == "between" {
		return p.addFilter(field, op, 2)
	}
	return p.addFilter(field, op, 1)
}

// skipGlue pula palavras de ligação ("que", "o", "the"...).
func (p *askP) skipGlue() {
	for {
		t, ok := p.cur()
		if !ok || !askIn(p.lx.glue, t) {
			return
		}
		p.i++
	}
}

func (p *askP) addFilter(field, op string, count int) error {
	var vals []any
	for k := 0; k < count; k++ {
		if k == 1 {
			if t, ok := p.cur(); !ok || !askIn(p.lx.and, t) {
				return p.err("bad_value", "need_value")
			}
			p.i++
		}
		v, err := p.value(field)
		if err != nil {
			return err
		}
		vals = append(vals, v)
	}
	return p.addFilterVals(field, op, vals...)
}

func (p *askP) value(field string) (any, error) {
	t, ok := p.cur()
	if !ok {
		return nil, p.err("bad_value", "need_value")
	}
	f := p.voc.c.types[p.typ].fields[field]
	switch f.kind {
	case "text":
		// "contém a palavra X": pula o substantivo e as palavras de ligação
		for !t.quoted && (askIn(p.lx.nouns, t) || askIn(p.lx.glue, t)) && p.i+1 < len(p.toks) {
			p.i++
			t = p.toks[p.i]
		}
		if !t.quoted && !p.bareOK(t) {
			return nil, p.err("bad_value", "need_value")
		}
		p.i++
		return t.raw, nil
	case "tags":
		for !t.quoted && askIn(p.lx.glue, t) && p.i+1 < len(p.toks) {
			p.i++
			t = p.toks[p.i]
		}
		if !t.quoted && !p.bareOK(t) {
			return nil, p.err("bad_value", "need_value")
		}
		p.i++
		return t.raw, nil
	case "number":
		if t.quoted || !askReNum.MatchString(t.text) {
			return nil, p.err("bad_value", "bad_number")
		}
		p.i++
		if strings.Contains(t.text, ".") {
			v, err := strconv.ParseFloat(t.text, 64)
			if err != nil {
				return nil, p.err("bad_value", "bad_number")
			}
			return v, nil
		}
		v, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return nil, p.err("bad_value", "bad_number")
		}
		return v, nil
	case "bool":
		switch {
		case askIn(p.lx.yes, t):
			p.i++
			return true, nil
		case askIn(p.lx.no, t):
			p.i++
			return false, nil
		}
		return nil, p.err("bad_value", "need_value")
	case "enum":
		w := t.text
		if t.quoted {
			w = askNorm(t.raw)
		}
		for _, v := range f.values {
			if askNorm(v) == w {
				p.i++
				return v, nil
			}
		}
		return nil, p.err("bad_value", "need_value")
	}
	return nil, p.notUnderstood()
}

// ---- recusas de propósito: dizem o motivo ----

var askRefusalWords = map[string][]string{
	"or":  {"or", "ou"},
	"not": {"not", "without", "except", "sem", "exceto", "nao", "nem", "never", "nunca", "diferente", "doesn't", "isn't", "don't", "aren't", "excluding", "excluindo"},
	"month": {"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december",
		"janeiro", "fevereiro", "marco", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro",
		"jan", "feb", "fev", "apr", "abr", "jun", "jul", "aug", "sep", "oct", "nov", "dec", "dez"},
	"relative": {"today", "yesterday", "tomorrow", "hoje", "ontem", "amanha", "last", "next", "this", "recently", "ultimo", "ultimos",
		"ultima", "ultimas", "proximo", "proxima", "passado", "passada", "semana", "semanas", "week", "weeks", "month", "months",
		"mes", "meses", "este", "esta", "esse", "essa", "atual", "current", "ago"},
}

var askRefusalPriority = []string{"or", "not", "month", "relative"}

var askRefusalIdx = func() map[string]string {
	m := map[string]string{}
	for cat, ws := range askRefusalWords {
		for _, w := range ws {
			m[w] = cat
		}
	}
	return m
}()

func askRefusalOf(w string) string { return askRefusalIdx[w] }

// refusalScan procura, na frase toda, uma palavra de algo que não existe (OU, negação, mês, data relativa).
func (p *askP) refusalScan() string {
	found := map[string]bool{}
	for _, t := range p.toks {
		if t.quoted || p.voc.words[t.text] {
			continue
		}
		if cat := askRefusalOf(t.text); cat != "" {
			found[cat] = true
		}
	}
	for _, cat := range askRefusalPriority {
		if found[cat] {
			return cat
		}
	}
	return ""
}

// ---- erros que ajudam ----

// askSafeValue: um valor digitado só aparece na mensagem se for simples (letras, números, espaço, _ - .).
func askSafeValue(s string) string {
	if s == "" || utf8.RuneCountInString(s) > 30 {
		return "…"
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '_' || r == '-' || r == '.') {
			return "…"
		}
	}
	return s
}

var askOpWords = map[string]map[string]string{
	"en": {"eq": "equal to", "contains": "containing", "starts": "starting with", "gt": "greater than", "gte": "at least", "lt": "less than",
		"lte": "at most", "from": "from", "to": "until", "before": "before", "after": "after", "year": "in", "between": "between"},
	"pt": {"eq": "igual a", "contains": "contendo", "starts": "começando com", "gt": "maior que", "gte": "pelo menos", "lt": "menor que",
		"lte": "no máximo", "from": "desde", "to": "até", "before": "antes de", "after": "depois de", "year": "em", "between": "entre"},
}

// understood resume em palavras o que já foi lido, só com valores limpos.
func (p *askP) understood() string {
	pl := p.plan
	var conds []string
	ow := askOpWords[p.lang]
	and := " and "
	with := "with "
	if p.lang == "pt" {
		and, with = " e ", "com "
	}
	val := func(v any) string {
		switch x := v.(type) {
		case string:
			return "\"" + askSafeValue(x) + "\""
		case Date:
			return x.String()
		case DateTime:
			return x.String()
		}
		return askSafeValue(fmt.Sprint(v))
	}
	for _, f := range pl.Filters {
		switch f.Op {
		case "has":
			if p.lang == "pt" {
				conds = append(conds, "a tag "+val(f.Vals[0]))
			} else {
				conds = append(conds, "the tag "+val(f.Vals[0]))
			}
		case "between":
			if len(f.Vals) == 2 {
				conds = append(conds, f.Field+" "+ow["between"]+" "+val(f.Vals[0])+and+val(f.Vals[1]))
			}
		default:
			if len(f.Vals) == 1 {
				conds = append(conds, f.Field+" "+ow[f.Op]+" "+val(f.Vals[0]))
			}
		}
	}
	var out []string
	if len(conds) > 0 {
		out = append(out, with+strings.Join(conds, and))
	}
	if pl.Order != nil {
		dir := map[string]string{"en": "ascending", "pt": "crescente"}[p.lang]
		if pl.Order.Desc {
			dir = map[string]string{"en": "descending", "pt": "decrescente"}[p.lang]
		}
		by := map[string]string{"en": "sorted by ", "pt": "ordenados por "}[p.lang]
		out = append(out, by+pl.Order.Field+" "+dir)
	}
	if pl.Limit > 0 {
		out = append(out, map[string]string{"en": "first ", "pt": "primeiros "}[p.lang]+strconv.FormatInt(pl.Limit, 10))
	}
	if len(out) == 0 && !pl.CountOnly {
		return ""
	}
	head := p.typeLabel(p.typ)
	if pl.CountOnly {
		head = map[string]string{"en": "count of ", "pt": "contagem de "}[p.lang] + head
	}
	return head + func() string {
		if len(out) == 0 {
			return ""
		}
		return " " + strings.Join(out, ", ")
	}()
}

func (p *askP) fieldOfKind(kind string) (string, *askField) {
	t := p.voc.c.types[p.typ]
	for _, name := range p.view.fields {
		if f := t.fields[name]; f != nil && f.kind == kind {
			return name, f
		}
	}
	return "", nil
}

// guessCtx: quando o erro não aconteceu dentro de uma cláusula, adivinha qual o resto da frase queria.
func (p *askP) guessCtx() string {
	from := p.stop
	if from > len(p.toks) {
		from = len(p.toks)
	}
	for _, t := range p.toks[from:] {
		if t.quoted {
			continue
		}
		switch t.text {
		case "tag", "tags", "tagged", "etiqueta", "etiquetas", "marcado", "marcados", "marcada", "marcadas":
			return "tag"
		}
	}
	for i := from; i < len(p.toks); i++ {
		if p.toks[i].quoted {
			continue
		}
		if act, n := askMatchKW(p.lx.ops, p.toks, i); n > 0 && (act == "contains" || act == "starts") {
			return "text"
		}
	}
	for _, t := range p.toks[from:] {
		if t.quoted {
			continue
		}
		if askReISO.MatchString(t.text) || askReSlash.MatchString(t.text) || askReYear.MatchString(t.text) {
			return "date"
		}
		switch t.text {
		case "since", "until", "before", "after", "desde", "antes", "depois":
			return "date"
		}
	}
	for _, t := range p.toks[from:] {
		if !t.quoted && askReNum.MatchString(t.text) {
			return "number"
		}
	}
	return ""
}

// example escolhe um exemplo do tipo de cláusula que falhou, com campos que o nível enxerga.
func (p *askP) example() string {
	ctx := p.ctx
	if ctx == "" {
		ctx = p.guessCtx()
	}
	pt := p.lang == "pt"
	t := p.typeLabel(p.typ)
	pick := func(en, ptT string, args ...string) string {
		f := en
		if pt {
			f = ptT
		}
		for _, a := range args {
			f = strings.Replace(f, "%s", a, 1)
		}
		return f
	}
	tryCtx := func(ctx string) string {
		switch ctx {
		case "tag":
			if f, _ := p.fieldOfKind("tags"); f != "" {
				return pick(`list %s with the tag "go"`, `liste %s com a tag "go"`, t)
			}
		case "text":
			if f, _ := p.fieldOfKind("text"); f != "" {
				return pick(`list %s where %s contains "word"`, `liste %s onde %s contem "palavra"`, t, f)
			}
		case "number":
			if f, _ := p.fieldOfKind("number"); f != "" {
				return pick(`list %s where %s greater than 10`, `liste %s onde %s maior que 10`, t, f)
			}
		case "enum":
			if f, fd := p.fieldOfKind("enum"); f != "" && len(fd.values) > 0 {
				return pick(`list %s where %s is %s`, `liste %s onde %s igual a %s`, t, f, fd.values[0])
			}
		case "order":
			if f, _ := p.fieldOfKind("number"); f != "" {
				return pick(`list %s sorted by %s desc`, `liste %s ordenados por %s decrescente`, t, f)
			}
			if m := p.mainField(); m != "" {
				return pick(`list %s sorted by %s desc`, `liste %s ordenados por %s decrescente`, t, m)
			}
		case "limit":
			return pick(`list the first 5 %s`, `liste os 5 primeiros %s`, t)
		case "date":
			if p.mainField() != "" {
				return askSprintf(askExample[p.lang], t)
			}
		}
		return ""
	}
	if ex := tryCtx(ctx); ex != "" {
		return ex
	}
	for _, c := range []string{"date", "text", "tag"} {
		if ex := tryCtx(c); ex != "" {
			return ex
		}
	}
	return pick(`list %s`, `liste %s`, t)
}

// decorate troca o erro seco por um que diz o que foi entendido, onde parou e o que tentar.
func (p *askP) decorate(err error) error {
	e, ok := err.(askErr)
	if !ok || (e.Kind != "not_understood" && e.Kind != "bad_value") || p.typ == "" {
		return err
	}
	if cat := p.refusalScan(); cat != "" {
		return askErr{Kind: "unsupported", Lang: p.lang, Msg: p.msg("unsup_" + cat)}
	}
	var parts []string
	if und := p.understood(); und != "" {
		parts = append(parts, p.msg("understood", und))
	}
	switch p.lastKey {
	case "not_understood", "unknown_field":
		stop := ""
		if p.stop < len(p.toks) && !p.toks[p.stop].quoted {
			stop = askSafeWord(p.toks[p.stop].text)
		}
		if stop != "" {
			parts = append(parts, p.msg("stopped", stop))
		} else {
			parts = append(parts, p.msg("stopped_nw"))
		}
		if p.lastKey == "unknown_field" && p.view != nil {
			parts = append(parts, p.msg("fields", strings.Join(p.view.fields, ", ")))
		}
	default:
		parts = append(parts, e.Msg)
	}
	parts = append(parts, p.msg("try", p.example()))
	return askErr{Kind: e.Kind, Lang: e.Lang, Msg: strings.Join(parts, " ")}
}

// askFillerAny: palavra de enchimento ou ligação de qualquer idioma ("o", "the"...).
func askFillerAny(t askTok) bool {
	for _, lx := range askLexicons {
		if askIn(lx.fillers, t) || askIn(lx.glue, t) {
			return true
		}
	}
	return false
}
