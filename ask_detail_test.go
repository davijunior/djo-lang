//go:build !nodb

package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Campos detail (o texto longo): nunca em lista, só ao pedir UM item.

const askDetailTypes = `Ask.type:"post":{"table": "posts", "show_by": ["slug", "title"], "fields": {
    "slug": {"kind": "text"},
    "title": {"kind": "text"},
    "body": {"kind": "text", "detail": true},
    "status": {"kind": "enum", "values": ["draft", "published"]},
    "views": {"kind": "number"}
}}
Ask.alias:"post":["posts", "artigos"]
Ask.db:"t.db"
`

func askDetailDjo(public string, extra string) string {
	return askDetailTypes +
		`Ask.level:"public":{"allow": [{"type": "post", "where": {"status": "published"}, "fields": ` + public + `}]}` + "\n" +
		`Ask.level:"agent":{"allow": [{"type": "post"}]}` + "\n" + extra
}

const askDetailAll = `["slug", "title", "body", "views"]`

func askDetailRows(t *testing.T, db *sql.DB) {
	t.Helper()
	long := strings.Repeat("é", 3000) // 6000 bytes
	for _, q := range []string{
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, slug TEXT, title TEXT, body TEXT, status TEXT, views INTEGER)`,
		`INSERT INTO posts VALUES (1, 'bem-vindo', 'Bem-vindo ao djo', 'LINHA1' || char(10) || '<script>alert(1)</script>', 'published', 10)`,
		`INSERT INTO posts VALUES (2, 'go-tips', 'Go tips', 'CORPO-DOIS', 'published', 5)`,
		`INSERT INTO posts VALUES (3, 'rascunho', 'Rascunho secreto', 'CORPO-DO-RASCUNHO', 'draft', 0)`,
		`INSERT INTO posts VALUES (4, 'go-tips-2', 'Go tips', 'CORPO-QUATRO', 'published', 7)`,
		`INSERT INTO posts VALUES (5, 'enorme', 'Enorme', '` + long + `', 'published', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func askDetailFx(t *testing.T, djo string) *askFx {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "core"), 0o755)
	os.WriteFile(filepath.Join(root, "core", "c.djo"), []byte(askCore), 0o644)
	os.WriteFile(filepath.Join(root, "ask.djo"), []byte(djo), 0o644)
	cfg, err := loadAsk(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDB(filepath.Join(root, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	askDetailRows(t, db)
	return &askFx{cfg, db}
}

func hasKey(m *Map, k string) bool { _, ok := m.get(k); return ok }

func TestAskDetailConfigErros(t *testing.T) {
	lvl := `Ask.level:"public":{"allow": [{"type": "post"}]}` + "\n"
	typ := func(fields, extra string) string {
		return `Ask.type:"post":{"table": "posts", ` + extra + `"fields": {` + fields + `}}` + "\n" + lvl
	}
	cases := map[string]struct{ ask, want string }{
		"detail em número":    {typ(`"n": {"kind": "number", "detail": true}`, ""), "detail"},
		"detail não é bool":   {typ(`"b": {"kind": "text", "detail": "sim"}`, ""), "true ou false"},
		"show_by sem campo":   {typ(`"t": {"kind": "text"}`, `"show_by": ["zzz"], `), "zzz"},
		"show_by em detail":   {typ(`"t": {"kind": "text", "detail": true}`, `"show_by": ["t"], `), "detail"},
		"show_by em número":   {typ(`"n": {"kind": "number"}`, `"show_by": ["n"], `), "text ou enum"},
		"show_by repetido":    {typ(`"t": {"kind": "text"}`, `"show_by": ["t", "t"], `), "repete"},
		"show_by demais":      {typ(`"a": {"kind": "text"}, "b": {"kind": "text"}, "c": {"kind": "text"}, "d": {"kind": "text"}, "e": {"kind": "text"}`, `"show_by": ["a", "b", "c", "d", "e"], `), "até 4"},
		"detail_max pequeno":  {typ(`"t": {"kind": "text"}`, "") + "Ask.limits:{\"detail_max_bytes\": 10}\n", "detail_max_bytes"},
		"detail_max enorme":   {typ(`"t": {"kind": "text"}`, "") + "Ask.limits:{\"detail_max_bytes\": 99999999}\n", "detail_max_bytes"},
		"detail como main":    {typ(`"t": {"kind": "text", "detail": true, "main": true}`, ""), "main"},
		"show_by não é lista": {typ(`"t": {"kind": "text"}`, `"show_by": 5, `), "show_by"},
		"show_by em tags":     {typ(`"t": {"kind": "tags", "from": "x", "key": "a", "value": "b"}`, `"show_by": ["t"], `), "text ou enum"},
	}
	for name, c := range cases {
		err := askLoad(t, askCore, c.ask+"Ask.db:\"t.db\"\n")
		if err == nil {
			t.Errorf("%s: deveria falhar", name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: erro %q não menciona %q", name, err, c.want)
		}
	}
	if err := askLoad(t, askCore, askDetailDjo(askDetailAll, "")); err != nil {
		t.Fatalf("a configuração boa falhou: %v", err)
	}
}

func TestAskDetailNuncaEmListaNemContagem(t *testing.T) {
	fx := askDetailFx(t, askDetailDjo(askDetailAll, ""))
	for _, lv := range []string{"public", "agent"} {
		r := mustRun(t, fx, lv, askPlan{Type: "post"})
		if len(r.Items) == 0 {
			t.Fatalf("%s: lista vazia", lv)
		}
		for _, m := range r.Items {
			if hasKey(m, "body") {
				t.Errorf("%s: o texto longo veio em lista: %v", lv, m.Keys)
			}
		}
		if !hasKey(r.Items[0], "title") {
			t.Errorf("%s: os campos curtos precisam continuar vindo", lv)
		}
	}
	if n := mustRun(t, fx, "public", askPlan{Type: "post", CountOnly: true}).Count; n != 4 {
		t.Errorf("contagem = %d (o rascunho não conta para o público)", n)
	}
	// o filtro pelo texto longo continua valendo (e continua sem trazer o texto)
	r := mustRun(t, fx, "public", askPlan{Type: "post", Filters: []askFilter{f("body", "contains", "corpo-dois")}})
	if titles(r) != "Go tips" || hasKey(r.Items[0], "body") {
		t.Errorf("filtro por detail: %s %v", titles(r), r.Items[0].Keys)
	}
	// o rascunho não aparece nem por filtro no detail
	if r := mustRun(t, fx, "public", askPlan{Type: "post", Filters: []askFilter{f("body", "contains", "rascunho")}}); len(r.Items) != 0 {
		t.Errorf("o público achou o rascunho pelo texto: %v", r.Items)
	}
	// não dá para pedir o detail em lista, nem para ordenar por ele
	if _, err := fx.run("public", askPlan{Type: "post", Fields: []string{"title", "body"}}); err == nil {
		t.Error("campos explícitos com detail em lista deveriam falhar")
	}
	if _, err := fx.run("public", askPlan{Type: "post", Order: &askOrder{Field: "body"}}); err == nil {
		t.Error("ordenar por detail deveria falhar")
	}
}

func TestAskShowUmItem(t *testing.T) {
	fx := askDetailFx(t, askDetailDjo(askDetailAll, ""))
	show := func(level, v string) *askResult {
		return mustRun(t, fx, level, askPlan{Type: "post", Show: true, ShowValue: v})
	}
	// pelo slug
	r := show("public", "bem-vindo")
	if !r.Item || len(r.Items) != 1 || !r.Searched || r.Ambiguous {
		t.Fatalf("slug: %+v", r)
	}
	body, _ := r.Items[0].get("body")
	if !strings.HasPrefix(format(body), "LINHA1\n<script>") {
		t.Errorf("faltou o texto: %q", body)
	}
	if strings.Join(r.DetailFields, ",") != "body" || hasKey(r.Items[0], "status") {
		t.Errorf("detail %v, chaves %v (o status é escondido do público)", r.DetailFields, r.Items[0].Keys)
	}
	// pelo título, sem olhar maiúsculas
	if r := show("public", "bem-vindo ao DJO"); !r.Item || titles(r) != "Bem-vindo ao djo" {
		t.Errorf("título: %+v", r)
	}
	// o slug vem antes do título: "go-tips" é o slug do 2, mesmo que "Go tips" seja título de dois
	if r := show("public", "go-tips"); !r.Item || titles(r) != "Go tips" {
		t.Errorf("slug antes do título: %+v", r)
	}
	// dois com o mesmo título: lista curta, SEM o texto longo
	r = show("public", "Go tips")
	if r.Item || !r.Ambiguous || len(r.Items) != 2 {
		t.Fatalf("ambíguo: %+v", r)
	}
	for _, m := range r.Items {
		if hasKey(m, "body") {
			t.Error("a lista curta não pode ter o texto longo")
		}
	}
	// o rascunho: o público não o acha, e a resposta é igual à de um que não existe
	hidden, none := show("public", "rascunho"), show("public", "nao-existe-mesmo")
	if hidden.Item || hidden.Ambiguous || len(hidden.Items) != 0 || !hidden.Searched {
		t.Errorf("rascunho para o público: %+v", hidden)
	}
	if hidden.Item != none.Item || hidden.Ambiguous != none.Ambiguous || len(hidden.Items) != len(none.Items) || hidden.Page != none.Page || hidden.Limit != none.Limit {
		t.Errorf("escondido x inexistente diferem: %+v x %+v", hidden, none)
	}
	// o agent vê o rascunho, com o status
	a := show("agent", "rascunho")
	if !a.Item || !hasKey(a.Items[0], "status") {
		t.Errorf("agent: %+v", a)
	}
	// valor com curingas do SQL não vira padrão
	for _, v := range []string{"%", "_", "bem%", "' OR 1=1 --", "bem-vindo' --"} {
		if r := show("public", v); r.Item || len(r.Items) != 0 {
			t.Errorf("%q achou algo: %+v", v, r)
		}
	}
}

// show_by só usa campos que o nível enxerga; detail só vem se estiver na lista "fields" do nível; deny vence.
func TestAskShowRespeitaONivel(t *testing.T) {
	// o público não enxerga o slug: o show por slug não acha, e por título acha
	fx := askDetailFx(t, askDetailDjo(`["title", "body", "views"]`, ""))
	if r := mustRun(t, fx, "public", askPlan{Type: "post", Show: true, ShowValue: "bem-vindo"}); r.Item || len(r.Items) != 0 {
		t.Errorf("o slug não é visível, mas o show achou por ele: %+v", r)
	}
	if r := mustRun(t, fx, "public", askPlan{Type: "post", Show: true, ShowValue: "Bem-vindo ao djo"}); !r.Item {
		t.Errorf("por título deveria achar: %+v", r)
	}
	// o texto longo fora da lista "fields" do nível não vem nem no item
	fx = askDetailFx(t, askDetailDjo(`["slug", "title", "views"]`, ""))
	r := mustRun(t, fx, "public", askPlan{Type: "post", Show: true, ShowValue: "bem-vindo"})
	if !r.Item || hasKey(r.Items[0], "body") || len(r.DetailFields) != 0 {
		t.Errorf("body fora do nível: %+v %v", r, r.Items[0].Keys)
	}
	if _, err := fx.run("public", askPlan{Type: "post", Filters: []askFilter{f("body", "contains", "x")}}); err == nil {
		t.Error("filtrar por campo fora do nível deveria falhar")
	}
	// deny vence, até para o agent
	fx = askDetailFx(t, askDetailDjo(askDetailAll, "Ask.deny:[\"post.body\"]\n"))
	for _, lv := range []string{"public", "agent"} {
		r := mustRun(t, fx, lv, askPlan{Type: "post", Show: true, ShowValue: "bem-vindo"})
		if !r.Item || hasKey(r.Items[0], "body") {
			t.Errorf("%s com deny: %+v", lv, r.Items)
		}
	}
	// nível sem nenhum campo de show_by: o show não acha nada, nem existe para o parser
	fx = askDetailFx(t, askDetailDjo(`["body", "views"]`, ""))
	if r := mustRun(t, fx, "public", askPlan{Type: "post", Show: true, ShowValue: "bem-vindo"}); r.Item || len(r.Items) != 0 {
		t.Errorf("sem show_by visível: %+v", r)
	}
}

func TestAskDetailTruncado(t *testing.T) {
	fx := askDetailFx(t, askDetailDjo(askDetailAll, "Ask.limits:{\"detail_max_bytes\": 1025}\n"))
	r := mustRun(t, fx, "public", askPlan{Type: "post", Show: true, ShowValue: "enorme"})
	b, _ := r.Items[0].get("body")
	s := format(b)
	if !r.Truncated || len(s) > 1025 || len(s) < 1000 || !utf8.ValidString(s) {
		t.Errorf("truncado=%v, %d bytes, UTF-8 válido=%v", r.Truncated, len(s), utf8.ValidString(s))
	}
	if r := mustRun(t, fx, "public", askPlan{Type: "post", Show: true, ShowValue: "bem-vindo"}); r.Truncated {
		t.Error("um texto curto não é cortado")
	}
}

func TestAskShowFrases(t *testing.T) {
	fx := askDetailFx(t, askDetailDjo(askDetailAll, ""))
	show := map[string]string{
		`show post bem-vindo`:          "bem-vindo",
		`show post "Bem-vindo ao djo"`: "Bem-vindo ao djo",
		`show me the post "go-tips"`:   "go-tips",
		`mostre o post bem-vindo`:      "bem-vindo",
		`mostra o post "bem-vindo"`:    "bem-vindo",
		`me mostre o post "Go tips"`:   "Go tips",
		`o post bem-vindo`:             "bem-vindo",
		`the post "go-tips"`:           "go-tips",
		`mostre artigos "bem-vindo"`:   "bem-vindo",
		`SHOW POST "Bem-vindo ao djo"`: "Bem-vindo ao djo",
		`show post "a b c  d"`:         "a b c  d",
	}
	for phrase, want := range show {
		got, err := fx.cfg.parsePhrase("public", phrase)
		if err != nil || !got.Plan.Show || got.Plan.ShowValue != want || got.Plan.Type != "post" {
			t.Errorf("%q: %+v %v", phrase, got, err)
		}
	}
	notShow := []string{
		`list posts`, `show posts`, `show 3 posts`, `liste posts`, `count posts`, `quantos posts`,
		`list posts with title go`, `show post with title go`, `liste posts onde title contem go`,
		`list posts first 2`, `show posts "bem-vindo"`, `mostre artigos bem-vindo`,
	}
	for _, phrase := range notShow {
		got, err := fx.cfg.parsePhrase("public", phrase)
		if err == nil && got.Plan.Show {
			t.Errorf("%q não é um show: %+v", phrase, got.Plan)
		}
	}
	// sem show_by visível para o nível, "show post X" é uma frase comum (e não entendida)
	fx2 := askDetailFx(t, askDetailDjo(`["body", "views"]`, ""))
	if got, err := fx2.cfg.parsePhrase("public", `show post bem-vindo`); err == nil && got.Plan.Show {
		t.Errorf("sem show_by visível não há show: %+v", got.Plan)
	}
	// valor grande demais
	if _, err := fx.cfg.parsePhrase("public", `show post "`+strings.Repeat("x", 300)+`"`); err == nil {
		t.Error("valor de 300 letras deveria ser recusado")
	} else if got, _ := fx.cfg.parsePhrase("public", `show post "`+strings.Repeat("x", 300)+`"`); got != nil && got.Plan.Show {
		t.Error("valor enorme virou show")
	}
}

// O valor do show nunca vira SQL nem sai da consulta parametrizada: uma carga de ataques não acha nada nem quebra.
func TestAskShowFuzz(t *testing.T) {
	fx := askDetailFx(t, askDetailDjo(askDetailAll, ""))
	base := []string{"", " ", "%", "_", "!", "\\", "'", "\"", "`", ";", "--", "/*", "*/", "\x00", "bem", "vindo", "' OR '1'='1", "\") OR (1=1", "é", "‮", "🙂", strings.Repeat("a", 199), strings.Repeat("%", 50)}
	count := 0
	for _, a := range base {
		for _, b := range base {
			v := a + b
			if v == "" || len(v) > 200 {
				continue
			}
			r, err := fx.run("public", askPlan{Type: "post", Show: true, ShowValue: v})
			count++
			if err != nil {
				if !strings.Contains(err.Error(), "texto") && !strings.Contains(err.Error(), "valor") {
					t.Errorf("%q: erro inesperado %v", v, err)
				}
				continue
			}
			if r.Item || r.Ambiguous || len(r.Items) != 0 {
				t.Errorf("%q achou algo que não deveria: %+v", v, r.Items)
			}
		}
	}
	if count < 200 {
		t.Errorf("só %d casos", count)
	}
	// e a tabela continua inteira
	if r := mustRun(t, fx, "agent", askPlan{Type: "post", CountOnly: true}); r.Count != 5 {
		t.Errorf("a tabela mudou: %d", r.Count)
	}
}

const askPageDjo = `Ask.type:"page":{"table": "posts", "show_by": ["slug", "title"], "fields": {
    "slug": {"kind": "text"},
    "title": {"kind": "text"},
    "body": {"kind": "text", "detail": true}
}}
Ask.alias:"page":["páginas", "página"]
Ask.db:"t.db"
Ask.level:"public":{"allow": [{"type": "page"}]}
`

func TestAskShowAliasSingularSemAspas(t *testing.T) {
	fx := askDetailFx(t, askPageDjo)
	ok := map[string]string{
		`mostre a página sobre`:            "sobre",
		`mostre a página "sobre"`:          "sobre",
		`mostre a página bem-vindo ao djo`: "bem-vindo ao djo",
		`mostre a page sobre`:              "sobre",
		`show page sobre`:                  "sobre",
		`mostre as páginas "sobre"`:        "sobre",
	}
	for phrase, want := range ok {
		got, err := fx.cfg.parsePhrase("public", phrase)
		if err != nil || !got.Plan.Show || got.Plan.ShowValue != want {
			t.Errorf("%q: %+v %v", phrase, got, err)
		}
	}
	// plural sem aspas continua sendo lista (ou erro), nunca um item
	for _, phrase := range []string{`mostre as páginas sobre`, `show pages sobre`} {
		got, err := fx.cfg.parsePhrase("public", phrase)
		if err == nil && got.Plan.Show {
			t.Errorf("%q virou item: %+v", phrase, got.Plan)
		}
	}
	// apelido repetido entre tipos já é recusado na configuração (ask.go), então não há ambiguidade a tratar
}

func TestAskSugestaoNoIdiomaDaPergunta(t *testing.T) {
	fx := askDetailFx(t, askPageDjo)
	_, err := fx.cfg.parsePhrase("public", `liste páginas foo bar`)
	if err == nil || !strings.Contains(err.Error(), "liste páginas ") || strings.Contains(err.Error(), "pages") {
		t.Errorf("PT deveria sugerir 'páginas': %v", err)
	}
	_, err = fx.cfg.parsePhrase("public", `list pages foo bar`)
	if err == nil || !strings.Contains(err.Error(), "list pages") || strings.Contains(err.Error(), "páginas") {
		t.Errorf("EN deveria sugerir 'pages': %v", err)
	}
}
