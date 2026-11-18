package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// ask.djo: a política do /ask. Só existe o que o ask.djo libera; sem o arquivo, o /ask está desligado.
// É um programa djo que só aceita chamadas Ask.*, como o rotas.djo só aceita Route.*:
//
//	Ask.type:"post":{"table": "posts", "fields": {"title": {"kind": "text"}, ...}}
//	Ask.level:"public":{"allow": [{"type": "post", "where": {"status": "published"}}]}
//	Ask.deny:["user.password", "token.*"]
//	Ask.alias:"post.published_at":["published", "publicado em"]
//	Ask.limits:{"max_results": 100, "per_minute": 30, "timeout_ms": 5000}
//	Ask.reply:{"refused": "...", "unknown": {"en": "...", "pt": "..."}}
//
// Os nomes de tabela e de coluna vêm só daqui: nada do que uma pessoa digita chega ao SQL como nome.
// Tudo é conferido ao carregar (falha cedo): tipos, campos, kind contra a struct, deny e aliases.

var (
	askName   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	askIdent  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	askLevels = []string{"public", "agent", "admin"}
)

const (
	askDefaultMax       = 100
	askMaxResultsCap    = 1000
	askDefaultTimeout   = 5000
	askDefaultDetailMax = 200 * 1024
	askMaxDetailMax     = 500_000 // o djo ask lê no máximo 1 MB de resposta
)

type askField struct {
	name, column, kind string
	as                 string   // "epoch": coluna Int com segundos desde 1970 (só em datetime)
	values             []string // enum
	from, key, value   string   // tags: tabela de ligação, coluna da chave e coluna do valor
	main               bool
	detail             bool // texto longo: nunca sai em lista nem em contagem, só ao pedir um item (show_by)
}

type askType struct {
	name, table, idCol string
	fields             map[string]*askField
	order              []string
	aliases            []string
	fieldAliases       []askAlias
	showBy             []string // campos pelos quais "mostre o post X" procura um item, na ordem
}

type askCond struct {
	field string
	vals  []any // um valor (igualdade) ou vários (um deles)
}

type askAllow struct {
	typ    string
	where  []askCond
	fields []string // nil = todos os campos do tipo (menos os negados)
}

type askLimits struct {
	maxResults, perMinute, globalPerMinute, timeoutMS int // globalPerMinute 0 = sem teto global
	detailMax                                         int // bytes de campos detail devolvidos num item
}

type askConfig struct {
	types     map[string]*askType
	typeOrder []string
	levels    map[string][]askAllow
	deny      []string
	limits    askLimits
	reply     map[string]map[string]string
	dbName    string       // Ask.db: o banco que o /ask consulta (arquivo .db/.sqlite do site ou postgres:// / mysql://)
	proxies   []*net.IPNet // Ask.proxy: proxies de confiança (para ler o IP real em X-Forwarded-For)
	levelFrom string       // Ask.level_from: função do projeto que decide o nível (public/agent/admin) a partir do pedido

	vmu    sync.Mutex
	vocabs map[string]any // vocabulário por nível (a política não muda depois de carregada)
}

func newAskConfig() *askConfig {
	return &askConfig{
		types:  map[string]*askType{},
		levels: map[string][]askAllow{},
		limits: askLimits{maxResults: askDefaultMax, perMinute: 60, timeoutMS: askDefaultTimeout, detailMax: askDefaultDetailMax},
		reply:  map[string]map[string]string{},
	}
}

func (in *Interp) needAsk(line int) *askConfig {
	if in.ask == nil {
		fail(line, "Ask só pode ser usado dentro do ask.djo")
	}
	return in.ask
}

func mapKeys(line int, m *Map, ctx string, allowed ...string) {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for _, k := range m.Keys {
		ks, _ := k.(string)
		if !ok[ks] {
			fail(line, "%s: a chave %v não existe (valem: %s)", ctx, k, strings.Join(allowed, ", "))
		}
	}
}

func askStrList(line int, v any, ctx string) []string {
	arr, ok := v.(*Array)
	if !ok {
		fail(line, "%s precisa ser uma lista de textos", ctx)
	}
	out := make([]string, len(arr.Items))
	for i, it := range arr.Items {
		s, ok := it.(string)
		if !ok || s == "" {
			fail(line, "%s: o item %d precisa ser um texto não vazio", ctx, i+1)
		}
		out[i] = s
	}
	return out
}

func identArg(line int, v any, ctx string) string {
	s, ok := v.(string)
	if !ok || !askIdent.MatchString(s) {
		fail(line, "%s precisa ser um nome simples (letras, números e _, como \"posts\"), mas recebeu %v", ctx, v)
	}
	return s
}

func init() {
	ns := nativeNS("Ask")
	ns["type"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		in.addAskType(line, a)
		return nil
	}}
	ns["level"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		in.addAskLevel(line, a)
		return nil
	}}
	ns["deny"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		in.addAskDeny(line, a)
		return nil
	}}
	ns["alias"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		in.addAskAlias(line, a)
		return nil
	}}
	ns["limits"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		in.addAskLimits(line, a)
		return nil
	}}
	ns["db"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		c := in.needAsk(line)
		name, ok := a[0].(string)
		if !ok || name == "" || len(name) > 300 {
			fail(line, "Ask.db espera o banco: um arquivo \"site.db\" ou um endereço postgres:// / mysql://")
		}
		if !askRemoteAddr(name) {
			if !(strings.HasSuffix(name, ".db") || strings.HasSuffix(name, ".sqlite")) || strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
				fail(line, "Ask.db: o arquivo precisa terminar em .db ou .sqlite e ficar dentro da pasta do site")
			}
		}
		c.dbName = name
		return nil
	}}
	ns["proxy"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		c := in.needAsk(line)
		names := askStrList(line, a[0], "Ask.proxy")
		if len(names) == 0 || len(names) > 50 {
			fail(line, "Ask.proxy espera de 1 a 50 endereços ou faixas, como [\"127.0.0.1\", \"10.0.0.0/8\"]")
		}
		c.proxies = nil
		for _, n := range names {
			n = strings.TrimSpace(n)
			if !strings.Contains(n, "/") {
				ip := net.ParseIP(n)
				if ip == nil {
					fail(line, "Ask.proxy: \"%s\" não é um endereço IP nem uma faixa (CIDR)", n)
				}
				bits := 128
				if ip.To4() != nil {
					bits = 32
				}
				c.proxies = append(c.proxies, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
				continue
			}
			_, nw, err := net.ParseCIDR(n)
			if err != nil {
				fail(line, "Ask.proxy: \"%s\" não é uma faixa válida (CIDR)", n)
			}
			if ones, _ := nw.Mask.Size(); ones == 0 {
				fail(line, "Ask.proxy: \"%s\" confiaria em qualquer endereço; liste só os seus proxies", n)
			}
			c.proxies = append(c.proxies, nw)
		}
		return nil
	}}
	ns["level_from"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		c := in.needAsk(line)
		name, ok := a[0].(string)
		if !ok || !askIdent.MatchString(name) {
			fail(line, "Ask.level_from espera o nome de uma função do core/, como \"nivel_do_pedido\"")
		}
		c.levelFrom = name
		return nil
	}}
	ns["reply"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		in.addAskReply(line, a)
		return nil
	}}
}

// kindFromStruct é a natureza que o tipo da struct pede; "" se não há struct para consultar.
var structKinds = map[string][]string{
	"String":   {"text", "enum"},
	"Int":      {"number", "datetime"}, // datetime só com "as": "epoch"
	"Float":    {"number"},
	"Bool":     {"bool"},
	"Date":     {"date"},
	"DateTime": {"datetime"},
	"Arr":      {"tags"},
}

var askKinds = map[string]bool{"text": true, "number": true, "bool": true, "date": true, "datetime": true, "enum": true, "tags": true}

func (in *Interp) addAskType(line int, a []any) {
	c := in.needAsk(line)
	name, ok := a[0].(string)
	if !ok || !askName.MatchString(name) {
		fail(line, "Ask.type: o nome do tipo precisa ser em minúsculas, como \"post\" (letras, números e _), mas recebeu %v", a[0])
	}
	if c.types[name] != nil {
		fail(line, "Ask.type: o tipo '%s' já foi declarado", name)
	}
	m, ok := a[1].(*Map)
	if !ok {
		fail(line, "Ask.type:\"%s\" espera um mapa com \"table\" e \"fields\"", name)
	}
	ctx := "Ask.type:\"" + name + "\""
	mapKeys(line, m, ctx, "table", "id", "struct", "fields", "show_by")
	t := &askType{name: name, idCol: "id", fields: map[string]*askField{}}
	var sd *StructDecl
	for i, k := range m.Keys {
		v := m.Vals[i]
		switch k {
		case "table":
			t.table = identArg(line, v, ctx+": \"table\"")
		case "id":
			t.idCol = identArg(line, v, ctx+": \"id\"")
		case "show_by":
			t.showBy = askStrList(line, v, ctx+": \"show_by\"")
		case "struct":
			sn, _ := v.(string)
			sd = in.structs[sn]
			if sd == nil {
				fail(line, "%s: a struct '%v' não existe no core/", ctx, v)
			}
			in.link(sd, line)
		}
	}
	if t.table == "" {
		fail(line, "%s: falta \"table\"", ctx)
	}
	fm := mapOf(m, "fields")
	if fm == nil || len(fm.Keys) == 0 {
		fail(line, "%s: \"fields\" precisa ser um mapa com ao menos um campo", ctx)
	}
	mains := 0
	for i, k := range fm.Keys {
		fname, _ := k.(string)
		if !askName.MatchString(fname) {
			fail(line, "%s: o nome de campo %v precisa ser em minúsculas, como \"published_at\"", ctx, k)
		}
		spec, ok := fm.Vals[i].(*Map)
		if !ok {
			fail(line, "%s: o campo '%s' espera um mapa, como {\"kind\": \"text\"}", ctx, fname)
		}
		f := in.askField(line, ctx, fname, spec, sd)
		if f.main {
			mains++
		}
		t.fields[fname] = f
		t.order = append(t.order, fname)
	}
	if mains > 1 {
		// uma data "principal" por tipo; o /ask usa quando a frase não diz qual
		fail(line, "%s: só um campo pode ser \"main\"", ctx)
	}
	if len(t.showBy) > 4 {
		fail(line, "%s: \"show_by\" aceita até 4 campos", ctx)
	}
	seen := map[string]bool{}
	for _, sb := range t.showBy {
		f := t.fields[sb]
		switch {
		case f == nil:
			fail(line, "%s: \"show_by\" cita o campo '%s', que não existe", ctx, sb)
		case f.kind != "text" && f.kind != "enum":
			fail(line, "%s: \"show_by\" só aceita campos text ou enum ('%s' é %s)", ctx, sb, f.kind)
		case f.detail:
			fail(line, "%s: o campo '%s' é \"detail\" e não pode ser usado em \"show_by\"", ctx, sb)
		case seen[sb]:
			fail(line, "%s: \"show_by\" repete o campo '%s'", ctx, sb)
		}
		seen[sb] = true
	}
	c.types[name] = t
	c.typeOrder = append(c.typeOrder, name)
}

func mapOf(m *Map, key string) *Map {
	v, _ := m.get(key)
	r, _ := v.(*Map)
	return r
}

func (in *Interp) askField(line int, tctx, fname string, spec *Map, sd *StructDecl) *askField {
	ctx := tctx + " campo '" + fname + "'"
	mapKeys(line, spec, ctx, "kind", "column", "as", "values", "from", "key", "value", "attr", "main", "detail")
	f := &askField{name: fname, column: fname}
	declared := ""
	attr := fname
	for i, k := range spec.Keys {
		v := spec.Vals[i]
		switch k {
		case "kind":
			s, _ := v.(string)
			if !askKinds[s] {
				fail(line, "%s: \"kind\" precisa ser text, number, bool, date, datetime, enum ou tags (veio %v)", ctx, v)
			}
			declared = s
		case "column":
			f.column = identArg(line, v, ctx+": \"column\"")
		case "as":
			if s, _ := v.(string); s != "epoch" {
				fail(line, "%s: \"as\" só aceita \"epoch\" (coluna Int com segundos desde 1970)", ctx)
			}
			f.as = "epoch"
		case "values":
			f.values = askStrList(line, v, ctx+": \"values\"")
		case "from":
			f.from = identArg(line, v, ctx+": \"from\"")
		case "key":
			f.key = identArg(line, v, ctx+": \"key\"")
		case "value":
			f.value = identArg(line, v, ctx+": \"value\"")
		case "attr":
			s, _ := v.(string)
			if s == "" {
				fail(line, "%s: \"attr\" é o nome do campo na struct", ctx)
			}
			attr = s
		case "main":
			b, ok := v.(bool)
			if !ok {
				fail(line, "%s: \"main\" precisa ser true ou false", ctx)
			}
			f.main = b
		case "detail":
			b, ok := v.(bool)
			if !ok {
				fail(line, "%s: \"detail\" precisa ser true ou false", ctx)
			}
			f.detail = b
		}
	}
	kind := declared
	if sd != nil {
		var st string
		for _, sf := range sd.Fields {
			if sf.Name == attr {
				st = sf.Type
			}
		}
		if st == "" {
			fail(line, "%s: a struct %s não tem o campo '%s' (use \"attr\" se o nome é outro)", ctx, sd.Name, attr)
		}
		allowed, known := structKinds[st]
		if !known && st != "Any" {
			fail(line, "%s: o campo '%s' da struct é %s, que o /ask não consulta", ctx, attr, st)
		}
		if st != "Any" {
			if kind == "" {
				kind = allowed[0]
				if st == "Int" && f.as == "epoch" {
					kind = "datetime"
				}
			}
			ok := false
			for _, k := range allowed {
				ok = ok || k == kind
			}
			if kind == "datetime" && st == "Int" && f.as != "epoch" {
				ok = false
			}
			if !ok {
				fail(line, "%s: o kind \"%s\" não combina com o campo '%s' da struct, que é %s", ctx, kind, attr, st)
			}
		}
	}
	if kind == "" {
		fail(line, "%s: falta \"kind\" (não há struct para derivar)", ctx)
	}
	f.kind = kind
	if f.as == "epoch" && kind != "datetime" {
		fail(line, "%s: \"as\": \"epoch\" só vale em campo datetime", ctx)
	}
	if kind == "enum" && len(f.values) == 0 {
		fail(line, "%s: um campo enum precisa de \"values\"", ctx)
	}
	if kind != "enum" && len(f.values) > 0 {
		fail(line, "%s: \"values\" só vale em campo enum", ctx)
	}
	if kind == "tags" {
		if f.from == "" || f.key == "" || f.value == "" {
			fail(line, "%s: um campo tags precisa de \"from\", \"key\" e \"value\" (a tabela de ligação e as duas colunas)", ctx)
		}
	} else if f.from != "" || f.key != "" || f.value != "" {
		fail(line, "%s: \"from\", \"key\" e \"value\" só valem em campo tags", ctx)
	}
	if f.detail && kind != "text" {
		fail(line, "%s: \"detail\" só vale em campo text (o texto longo, como o corpo de um post)", ctx)
	}
	if f.detail && f.main {
		fail(line, "%s: um campo \"detail\" não pode ser \"main\"", ctx)
	}
	if f.main && kind != "date" && kind != "datetime" {
		fail(line, "%s: \"main\" só vale em campo date ou datetime", ctx)
	}
	return f
}

func levelIndex(name string) int {
	for i, l := range askLevels {
		if l == name {
			return i
		}
	}
	return -1
}

func (in *Interp) addAskLevel(line int, a []any) {
	c := in.needAsk(line)
	name, _ := a[0].(string)
	if levelIndex(name) < 0 {
		fail(line, "Ask.level: o nível precisa ser public, agent ou admin (veio %v)", a[0])
	}
	if _, dup := c.levels[name]; dup {
		fail(line, "Ask.level:\"%s\" já foi declarado", name)
	}
	m, ok := a[1].(*Map)
	if !ok {
		fail(line, "Ask.level:\"%s\" espera um mapa com \"allow\"", name)
	}
	ctx := "Ask.level:\"" + name + "\""
	mapKeys(line, m, ctx, "allow")
	arr, ok := mapVal(m, "allow").(*Array)
	if !ok || len(arr.Items) == 0 {
		fail(line, "%s: \"allow\" precisa ser uma lista com ao menos um tipo liberado", ctx)
	}
	seen := map[string]bool{}
	var allows []askAllow
	for i, it := range arr.Items {
		am, ok := it.(*Map)
		if !ok {
			fail(line, "%s: o item %d de \"allow\" precisa ser um mapa, como {\"type\": \"post\"}", ctx, i+1)
		}
		mapKeys(line, am, ctx+" allow", "type", "where", "fields")
		tn, _ := mapVal(am, "type").(string)
		t := c.types[tn]
		if t == nil {
			fail(line, "%s: o tipo '%s' não foi declarado (declare Ask.type antes)", ctx, tn)
		}
		if seen[tn] {
			fail(line, "%s: o tipo '%s' aparece duas vezes", ctx, tn)
		}
		seen[tn] = true
		al := askAllow{typ: tn}
		if wv, has := am.get("where"); has {
			wm, ok := wv.(*Map)
			if !ok || len(wm.Keys) == 0 {
				fail(line, "%s %s: \"where\" precisa ser um mapa, como {\"status\": \"published\"}", ctx, tn)
			}
			for j, k := range wm.Keys {
				fname, _ := k.(string)
				f := t.fields[fname]
				if f == nil {
					fail(line, "%s %s: \"where\" usa o campo '%s', que o tipo não tem", ctx, tn, fname)
				}
				al.where = append(al.where, askCond{fname, askWhereVals(line, ctx+" "+tn+" where "+fname, f, wm.Vals[j])})
			}
		}
		if fv, has := am.get("fields"); has {
			fl := askStrList(line, fv, ctx+" "+tn+": \"fields\"")
			for _, fname := range fl {
				if t.fields[fname] == nil {
					fail(line, "%s %s: \"fields\" lista '%s', que o tipo não tem", ctx, tn, fname)
				}
			}
			al.fields = fl
		}
		allows = append(allows, al)
	}
	c.levels[name] = allows
}

func mapVal(m *Map, key string) any {
	v, _ := m.get(key)
	return v
}

// askWhereVals confere os valores de um "where" da política contra a natureza do campo.
func askWhereVals(line int, ctx string, f *askField, v any) []any {
	var vals []any
	if arr, ok := v.(*Array); ok {
		vals = arr.Items
	} else {
		vals = []any{v}
	}
	if len(vals) == 0 {
		fail(line, "%s: a lista de valores não pode ser vazia", ctx)
	}
	for _, x := range vals {
		switch f.kind {
		case "text", "enum":
			s, ok := x.(string)
			if !ok {
				fail(line, "%s: o valor precisa ser texto", ctx)
			}
			if f.kind == "enum" && !contains(f.values, s) {
				fail(line, "%s: '%s' não está nos values do campo (%s)", ctx, s, strings.Join(f.values, ", "))
			}
		case "number":
			switch x.(type) {
			case int64, float64:
			default:
				fail(line, "%s: o valor precisa ser número", ctx)
			}
		case "bool":
			if _, ok := x.(bool); !ok {
				fail(line, "%s: o valor precisa ser true ou false", ctx)
			}
		default:
			fail(line, "%s: \"where\" só vale em campo text, number, bool ou enum", ctx)
		}
	}
	return vals
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (in *Interp) addAskDeny(line int, a []any) {
	c := in.needAsk(line)
	for _, p := range askStrList(line, a[0], "Ask.deny") {
		typ, field, hasField := strings.Cut(p, ".")
		t := c.types[typ]
		if t == nil {
			fail(line, "Ask.deny: o tipo '%s' de \"%s\" não foi declarado (um deny com erro de digitação não protegeria nada)", typ, p)
		}
		if hasField && field != "*" && t.fields[field] == nil {
			fail(line, "Ask.deny: o tipo '%s' não tem o campo '%s' (\"%s\")", typ, field, p)
		}
		c.deny = append(c.deny, p)
	}
}

func (in *Interp) addAskAlias(line int, a []any) {
	c := in.needAsk(line)
	target, _ := a[0].(string)
	names := askStrList(line, a[1], "Ask.alias \""+target+"\"")
	typ, field, hasField := strings.Cut(target, ".")
	t := c.types[typ]
	if t == nil || (hasField && t.fields[field] == nil) {
		fail(line, "Ask.alias: \"%s\" não existe (declare Ask.type antes)", target)
	}
	for _, n := range names {
		if len(n) > 40 || n != strings.ToLower(n) {
			fail(line, "Ask.alias \"%s\": o apelido \"%s\" precisa estar em minúsculas e ter até 40 caracteres", target, n)
		}
		// um apelido não pode ser outro nome real, nem repetir outro apelido do mesmo escopo
		if !hasField {
			for _, ot := range c.types {
				if ot.name == n || (ot != t && contains(ot.aliases, n)) {
					fail(line, "Ask.alias \"%s\": \"%s\" já é um nome de tipo ou apelido de outro", target, n)
				}
			}
		} else {
			for fn, of := range t.fields {
				_ = of
				if fn == n {
					fail(line, "Ask.alias \"%s\": \"%s\" já é o nome de um campo", target, n)
				}
			}
			for _, other := range t.aliasesOfFields() {
				if other == n {
					fail(line, "Ask.alias \"%s\": \"%s\" já é apelido de outro campo", target, n)
				}
			}
		}
		if hasField {
			t.fieldAliases = append(t.fieldAliases, askAlias{field, n})
		} else {
			t.aliases = append(t.aliases, n)
		}
	}
}

type askAlias struct{ field, name string }

func (t *askType) aliasesOfFields() []string {
	out := make([]string, len(t.fieldAliases))
	for i, a := range t.fieldAliases {
		out[i] = a.name
	}
	return out
}

func (in *Interp) addAskLimits(line int, a []any) {
	c := in.needAsk(line)
	m, ok := a[0].(*Map)
	if !ok {
		fail(line, "Ask.limits espera um mapa, como {\"max_results\": 100}")
	}
	mapKeys(line, m, "Ask.limits", "max_results", "per_minute", "global_per_minute", "timeout_ms", "detail_max_bytes")
	for i, k := range m.Keys {
		n, ok := m.Vals[i].(int64)
		if !ok {
			fail(line, "Ask.limits: \"%v\" precisa ser um Int", k)
		}
		switch k {
		case "max_results":
			if n < 1 || n > askMaxResultsCap {
				fail(line, "Ask.limits: \"max_results\" precisa ficar entre 1 e %d", askMaxResultsCap)
			}
			c.limits.maxResults = int(n)
		case "per_minute":
			if n < 1 || n > 100000 {
				fail(line, "Ask.limits: \"per_minute\" precisa ficar entre 1 e 100000")
			}
			c.limits.perMinute = int(n)
		case "global_per_minute":
			if n < 1 || n > 10000000 {
				fail(line, "Ask.limits: \"global_per_minute\" precisa ficar entre 1 e 10000000")
			}
			c.limits.globalPerMinute = int(n)
		case "detail_max_bytes":
			if n < 1024 || n > askMaxDetailMax {
				fail(line, "Ask.limits: \"detail_max_bytes\" precisa ficar entre 1024 e %d", askMaxDetailMax)
			}
			c.limits.detailMax = int(n)
		case "timeout_ms":
			if n < 100 || n > 10000 {
				fail(line, "Ask.limits: \"timeout_ms\" precisa ficar entre 100 e 10000")
			}
			c.limits.timeoutMS = int(n)
		}
	}
}

func (in *Interp) addAskReply(line int, a []any) {
	c := in.needAsk(line)
	m, ok := a[0].(*Map)
	if !ok {
		fail(line, "Ask.reply espera um mapa, como {\"refused\": \"...\", \"unknown\": \"...\"}")
	}
	mapKeys(line, m, "Ask.reply", "refused", "unknown")
	for i, k := range m.Keys {
		ks := k.(string)
		langs := map[string]string{}
		switch v := m.Vals[i].(type) {
		case string:
			langs["*"] = v
		case *Map:
			mapKeys(line, v, "Ask.reply \""+ks+"\"", "en", "pt")
			for j, lk := range v.Keys {
				s, _ := v.Vals[j].(string)
				langs[lk.(string)] = s
			}
		default:
			fail(line, "Ask.reply \"%s\": use um texto ou {\"en\": \"...\", \"pt\": \"...\"}", ks)
		}
		for _, s := range langs {
			if s == "" || len(s) > 300 {
				fail(line, "Ask.reply \"%s\": o texto precisa ter de 1 a 300 caracteres", ks)
			}
		}
		c.reply[ks] = langs
	}
}

// ---- carga ----

// loadAsk lê o ask.djo da raiz. Devolve nil, nil se o arquivo não existe (/ask desligado).
func loadAsk(root string) (*askConfig, error) {
	path := filepath.Join(root, "ask.djo")
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prog, err := Parse(string(src))
	if err != nil {
		if de, ok := err.(*djoError); ok {
			return nil, &djoError{Line: de.Line, Msg: "ask.djo: " + de.Msg}
		}
		return nil, err
	}
	for _, s := range prog {
		es, ok := s.(*ExprStmt)
		var mc *MethodCall
		if ok {
			mc, ok = es.X.(*MethodCall)
		}
		if ok {
			id, isID := mc.X.(*Ident)
			ok = isID && id.Name == "Ask"
		}
		if !ok {
			return nil, &djoError{Line: lineOf(s), Msg: "ask.djo: só aceita chamadas Ask.type:, Ask.level:, Ask.deny:, Ask.alias:, Ask.limits: e Ask.reply:"}
		}
	}
	in := NewInterp(io.Discard)
	in.root = root
	if err := in.loadCore(root); err != nil {
		return nil, err
	}
	in.ask = newAskConfig()
	if err := in.Run(prog); err != nil {
		if de, ok := err.(*djoError); ok {
			return nil, &djoError{Line: de.Line, Msg: "ask.djo: " + de.Msg}
		}
		return nil, err
	}
	if len(in.ask.typeOrder) == 0 {
		return nil, &djoError{Line: 1, Msg: "ask.djo: declare ao menos um Ask.type"}
	}
	if in.ask.levelFrom != "" {
		fd := in.funcs[in.ask.levelFrom]
		if fd == nil || fd.Action || len(fd.Params) != 0 {
			return nil, &djoError{Line: 1, Msg: "ask.djo: Ask.level_from \"" + in.ask.levelFrom + "\" precisa ser uma função (não action) do core/ sem parâmetros; ela lê o pedido com Http.bearer: e devolve \"public\", \"agent\" ou \"admin\""}
		}
	}
	if len(in.ask.levels) == 0 {
		return nil, &djoError{Line: 1, Msg: "ask.djo: declare ao menos um Ask.level (sem nível liberado, nada é consultável)"}
	}
	if in.ask.dbName == "" {
		return nil, &djoError{Line: 1, Msg: "ask.djo: declare Ask.db com o banco que o /ask consulta, como Ask.db:\"site.db\""}
	}
	return in.ask, nil
}

// checkAsk é usado ao iniciar o servidor e no djo build: um ask.djo com erro não pode ir ao ar.
func checkAsk(root string) error {
	_, err := loadAsk(root)
	return err
}

// ---- o que cada nível enxerga ----

// askView é o que um nível vê de um tipo: os campos visíveis, na ordem, e as condições de linha.
type askView struct {
	typ    *askType
	fields []string
	where  []askCond
}

func (c *askConfig) denied(typ, field string) bool {
	for _, d := range c.deny {
		t, f, has := strings.Cut(d, ".")
		if t != typ {
			continue
		}
		if !has || f == "*" || f == field {
			return true
		}
	}
	return false
}

// view devolve o que o nível enxerga do tipo, ou nil se o tipo não existe para ele. A regra do nível mais
// alto que cita o tipo (e que não passa do nível pedido) vale inteira, no lugar das de baixo.
func (c *askConfig) view(level, typ string) *askView {
	li := levelIndex(level)
	t := c.types[typ]
	if t == nil || li < 0 || c.denied(typ, "") {
		return nil
	}
	var al *askAllow
	for i := 0; i <= li; i++ {
		for j := range c.levels[askLevels[i]] {
			if c.levels[askLevels[i]][j].typ == typ {
				al = &c.levels[askLevels[i]][j]
			}
		}
	}
	if al == nil {
		return nil
	}
	cand := t.order
	if al.fields != nil {
		cand = al.fields
	}
	v := &askView{typ: t, where: al.where}
	for _, f := range cand {
		if !c.denied(typ, f) {
			v.fields = append(v.fields, f)
		}
	}
	if len(v.fields) == 0 {
		return nil
	}
	return v
}

func (v *askView) has(field string) bool {
	for _, f := range v.fields {
		if f == field {
			return true
		}
	}
	return false
}

// typeNames lista os tipos que o nível enxerga (para a ajuda).
func (c *askConfig) typeNames(level string) []string {
	var out []string
	for _, n := range c.typeOrder {
		if c.view(level, n) != nil {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (e askErr) Error() string { return e.Msg }

type askErr struct {
	Kind  string // unknown_type, unknown_field, bad_op, bad_value, bad_plan, timeout, db
	Msg   string // o que a pessoa vê
	Cause string // detalhe para o log; nunca vai à resposta
	Lang  string // idioma da pergunta ("en", "pt"), quando o parser o conhece
}

func askFail(kind, format string, args ...any) askErr {
	return askErr{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// askRemoteAddr: o nome é um endereço de PostgreSQL/MySQL (e não um arquivo do site)?
func askRemoteAddr(s string) bool {
	i := strings.Index(s, "://")
	if i < 0 {
		return false
	}
	switch strings.ToLower(s[:i]) {
	case "postgres", "postgresql", "mysql", "mariadb":
		return true
	}
	return false
}
