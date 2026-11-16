//go:build !nodb

package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type askFx struct {
	cfg *askConfig
	db  *sql.DB
}

func askFixture(t *testing.T, djo string) *askFx {
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
	ep := func(s string) int64 { d, _ := parseDateTime(s); return d.sec }
	for _, q := range []string{
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, titulo TEXT, texto TEXT, status TEXT, publicar_em TEXT, criado INTEGER, views INTEGER, secreto TEXT)`,
		`CREATE TABLE post_tags (post_id INTEGER, tag TEXT)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT, password TEXT, criado TEXT)`,
		`INSERT INTO posts VALUES (1, 'Alpha', 'primeiro', 'published', '2025-01-10 09:00:00', ` + strconv.Itoa(int(ep("2025-01-10 09:00:00"))) + `, 5, 'S-1')`,
		`INSERT INTO posts VALUES (2, 'Beta 100% sure', 'segundo', 'published', '2025-03-30 23:30:00', ` + strconv.Itoa(int(ep("2025-03-30 23:30:00"))) + `, 50, 'S-2')`,
		`INSERT INTO posts VALUES (3, 'Gamma', 'terceiro', 'published', '2025-03-31 00:00:00', ` + strconv.Itoa(int(ep("2025-03-31 00:00:00"))) + `, 7, 'S-3')`,
		`INSERT INTO posts VALUES (4, 'Draft one', 'rascunho', 'draft', '2025-04-01 08:00:00', ` + strconv.Itoa(int(ep("2025-04-01 08:00:00"))) + `, 0, 'TOPSECRET')`,
		`INSERT INTO posts VALUES (5, 'Delta_x', 'quinto', 'published', '2025-06-15 12:00:00', ` + strconv.Itoa(int(ep("2025-06-15 12:00:00"))) + `, 300, 'S-5')`,
		`INSERT INTO posts VALUES (6, 'Epsilon', 'sexto', 'published', '2024-12-31 23:59:59', ` + strconv.Itoa(int(ep("2024-12-31 23:59:59"))) + `, 1, 'S-6')`,
		`INSERT INTO post_tags VALUES (1, 'go'), (1, 'web'), (2, 'go'), (3, 'web'), (5, 'Go'), (4, 'internal')`,
		`INSERT INTO users VALUES (1, 'Ana', 'ana@x.com', 'hash-ana', '2025-01-01 00:00:00')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return &askFx{cfg, db}
}

func (fx *askFx) run(level string, p askPlan) (*askResult, error) {
	return fx.cfg.run(fx.db, kindSQLite, level, p)
}

func titles(r *askResult) string {
	var out []string
	for _, m := range r.Items {
		v, _ := m.get("title")
		out = append(out, format(v))
	}
	return strings.Join(out, "|")
}

func mustRun(t *testing.T, fx *askFx, level string, p askPlan) *askResult {
	t.Helper()
	r, err := fx.run(level, p)
	if err != nil {
		t.Fatalf("%+v: %v", p, err)
	}
	return r
}

func d(s string) Date                           { v, _ := parseDate(s); return v }
func dt(s string) DateTime                      { v, _ := parseDateTime(s); return v }
func f(field, op string, vals ...any) askFilter { return askFilter{field, op, vals} }

func TestAskVisibilidade(t *testing.T) {
	fx := askFixture(t, askDjo)
	c := fx.cfg
	// público: só os campos da regra; o status, o secret e o user não existem para ele
	v := c.view("public", "post")
	if got := strings.Join(v.fields, ","); got != "title,body,published_at,created_at,views,tags" {
		t.Errorf("campos do público = %s", got)
	}
	if c.view("public", "user") != nil || c.view("agent", "user") != nil {
		t.Error("user não deveria ser visível abaixo de admin")
	}
	// agent: a regra dele troca a do público (sem where, todos os campos menos o negado)
	if got := strings.Join(c.view("agent", "post").fields, ","); got != "title,body,status,published_at,created_at,views,tags" || len(c.view("agent", "post").where) != 0 {
		t.Errorf("campos do agent = %s", got)
	}
	// admin herda o post do agent e ganha user, sem email e senha
	if c.view("admin", "post") == nil {
		t.Error("admin herda post")
	}
	if got := strings.Join(c.view("admin", "user").fields, ","); got != "name,created_at" {
		t.Errorf("campos de user para admin = %s", got)
	}
	if c.view("ninguem", "post") != nil || c.view("public", "nada") != nil {
		t.Error("nível ou tipo inexistente deve dar nil")
	}
	if got := strings.Join(c.typeNames("public"), ","); got != "post" {
		t.Errorf("tipos do público = %s", got)
	}
	if got := strings.Join(c.typeNames("admin"), ","); got != "post,user" {
		t.Errorf("tipos do admin = %s", got)
	}
}

func TestAskConsultas(t *testing.T) {
	fx := askFixture(t, askDjo)

	// o público só vê publicados, na ordem do id, com os tipos certos
	r := mustRun(t, fx, "public", askPlan{Type: "post"})
	if titles(r) != "Alpha|Beta 100% sure|Gamma|Delta_x|Epsilon" || r.More {
		t.Fatalf("público = %s more=%v", titles(r), r.More)
	}
	first := r.Items[0]
	if v, _ := first.get("published_at"); v != dt("2025-01-10 09:00:00") {
		t.Errorf("published_at = %#v", v)
	}
	if v, _ := first.get("created_at"); v != dt("2025-01-10 09:00:00") { // coluna Int epoch -> DateTime
		t.Errorf("created_at (epoch) = %#v", v)
	}
	if v, _ := first.get("views"); v != int64(5) {
		t.Errorf("views = %#v", v)
	}
	if v, _ := first.get("tags"); format(v) != `["go", "web"]` {
		t.Errorf("tags = %s", format(v))
	}
	if _, has := first.get("status"); has {
		t.Error("status não é visível ao público e não pode vir")
	}
	if _, has := first.get("secret"); has {
		t.Error("secret é negado e não pode vir")
	}

	// agent vê o rascunho; secret segue negado
	r = mustRun(t, fx, "agent", askPlan{Type: "post", Filters: []askFilter{f("status", "eq", "draft")}})
	if titles(r) != "Draft one" {
		t.Errorf("rascunhos = %s", titles(r))
	}

	// página e limite; busca uma a mais para saber se há próxima
	r = mustRun(t, fx, "public", askPlan{Type: "post", Limit: 2})
	if titles(r) != "Alpha|Beta 100% sure" || !r.More || r.Page != 1 || r.Limit != 2 {
		t.Errorf("página 1 = %s %+v", titles(r), r)
	}
	r = mustRun(t, fx, "public", askPlan{Type: "post", Limit: 2, Page: 3})
	if titles(r) != "Epsilon" || r.More {
		t.Errorf("página 3 = %s more=%v", titles(r), r.More)
	}

	// contar e contar com o total
	r = mustRun(t, fx, "public", askPlan{Type: "post", CountOnly: true})
	if !r.HasCount || r.Count != 5 {
		t.Errorf("count = %+v", r)
	}
	r = mustRun(t, fx, "public", askPlan{Type: "post", Limit: 1, Total: true})
	if r.Count != 5 || len(r.Items) != 1 {
		t.Errorf("total = %+v", r)
	}

	// ordenação (com desempate pelo id)
	r = mustRun(t, fx, "public", askPlan{Type: "post", Order: &askOrder{"views", true}})
	if titles(r) != "Delta_x|Beta 100% sure|Gamma|Alpha|Epsilon" {
		t.Errorf("por views = %s", titles(r))
	}
	r = mustRun(t, fx, "public", askPlan{Type: "post", Order: &askOrder{"published_at", false}, Fields: []string{"title"}})
	if titles(r) != "Epsilon|Alpha|Beta 100% sure|Gamma|Delta_x" || len(r.Items[0].Keys) != 1 {
		t.Errorf("por data = %s", titles(r))
	}

	// datas: "até" inclusivo com Date num campo datetime (o dia inteiro), e o dia seguinte fica de fora
	for _, c := range []struct {
		name string
		fl   askFilter
		want string
	}{
		{"de/até no mesmo dia", f("published_at", "between", d("2025-03-30"), d("2025-03-30")), "Beta 100% sure"},
		{"de 30 até 31 inclusive", f("published_at", "between", d("2025-03-30"), d("2025-03-31")), "Beta 100% sure|Gamma"},
		{"from", f("published_at", "from", d("2025-03-31")), "Gamma|Delta_x"},
		{"to inclusivo", f("published_at", "to", d("2025-03-30")), "Alpha|Beta 100% sure|Epsilon"},
		{"after (depois do dia)", f("published_at", "after", d("2025-03-30")), "Gamma|Delta_x"},
		{"before (antes do dia)", f("published_at", "before", d("2025-01-10")), "Epsilon"},
		{"eq no dia", f("published_at", "eq", d("2025-03-30")), "Beta 100% sure"},
		{"ano 2025", f("published_at", "year", int64(2025)), "Alpha|Beta 100% sure|Gamma|Delta_x"},
		{"ano 2024", f("published_at", "year", int64(2024)), "Epsilon"},
		{"instante exato (DateTime)", f("published_at", "to", dt("2025-03-30 23:30:00")), "Alpha|Beta 100% sure|Epsilon"},
		{"instante: antes de", f("published_at", "before", dt("2025-03-30 23:30:00")), "Alpha|Epsilon"},
		{"coluna epoch", f("created_at", "between", d("2025-03-30"), d("2025-03-31")), "Beta 100% sure|Gamma"},
		{"views gt", f("views", "gt", int64(6)), "Beta 100% sure|Gamma|Delta_x"},
		{"views between", f("views", "between", int64(5), int64(50)), "Alpha|Beta 100% sure|Gamma"},
		{"texto contém (sem caixa)", f("title", "contains", "ALPHA"), "Alpha"},
		{"texto começa", f("title", "starts", "ga"), "Gamma"},
		{"texto igual", f("title", "eq", "Gamma"), "Gamma"},
		{"tag (sem caixa)", f("tags", "has", "GO"), "Alpha|Beta 100% sure|Delta_x"},
	} {
		r, err := fx.run("public", askPlan{Type: "post", Filters: []askFilter{c.fl}})
		if err != nil || titles(r) != c.want {
			t.Errorf("%s: %v %v, queria %s", c.name, titles(r), err, c.want)
		}
	}
	// mais de um filtro: E
	r = mustRun(t, fx, "public", askPlan{Type: "post", Filters: []askFilter{f("tags", "has", "go"), f("views", "gt", int64(10))}})
	if titles(r) != "Beta 100% sure|Delta_x" {
		t.Errorf("E de filtros = %s", titles(r))
	}
	// o tag "internal" do rascunho nunca aparece ao público
	r = mustRun(t, fx, "public", askPlan{Type: "post", Filters: []askFilter{f("tags", "has", "internal")}})
	if len(r.Items) != 0 {
		t.Errorf("tag do rascunho vazou: %s", titles(r))
	}
}

// um nome escondido e um que não existe têm de responder IGUAL, só trocando o nome.
func sameErr(t *testing.T, what string, a, b error, nameA, nameB string) {
	t.Helper()
	ea, oka := a.(askErr)
	eb, okb := b.(askErr)
	if !oka || !okb {
		t.Errorf("%s: esperava askErr nos dois, veio %v e %v", what, a, b)
		return
	}
	if ea.Kind != eb.Kind || strings.ReplaceAll(ea.Msg, nameA, "X") != strings.ReplaceAll(eb.Msg, nameB, "X") {
		t.Errorf("%s: respostas diferentes\n  escondido:   %q\n  inexistente: %q", what, ea.Msg, eb.Msg)
	}
}

func TestAskCampoEscondidoIgualAInexistente(t *testing.T) {
	fx := askFixture(t, askDjo)
	// para o público: status e secret (escondidos) vs um nome que não existe; para o agent: secret (negado)
	cases := []struct{ level, hidden string }{{"public", "status"}, {"public", "secret"}, {"agent", "secret"}}
	for _, c := range cases {
		plans := func(field string) map[string]askPlan {
			return map[string]askPlan{
				"filtrar":  {Type: "post", Filters: []askFilter{f(field, "eq", "x")}},
				"ordenar":  {Type: "post", Order: &askOrder{field, false}},
				"ler":      {Type: "post", Fields: []string{field}},
				"contar":   {Type: "post", CountOnly: true, Filters: []askFilter{f(field, "contains", "S")}},
				"total":    {Type: "post", Total: true, Filters: []askFilter{f(field, "eq", "S-1")}},
				"ordenarD": {Type: "post", Order: &askOrder{field, true}, Fields: []string{"title"}},
			}
		}
		ghost := plans("inexistente_xyz")
		for name, p := range plans(c.hidden) {
			_, ea := fx.run(c.level, p)
			_, eb := fx.run(c.level, ghost[name])
			if ea == nil || eb == nil {
				t.Errorf("%s/%s/%s: deveria dar erro (%v, %v)", c.level, c.hidden, name, ea, eb)
				continue
			}
			sameErr(t, c.level+"/"+c.hidden+"/"+name, ea, eb, c.hidden, "inexistente_xyz")
		}
	}
	// tipo escondido (user para o público/agent) = tipo que não existe
	for _, level := range []string{"public", "agent"} {
		_, ea := fx.run(level, askPlan{Type: "user"})
		_, eb := fx.run(level, askPlan{Type: "nada"})
		sameErr(t, level+"/tipo", ea, eb, "user", "nada")
		_, ea = fx.run(level, askPlan{Type: "user", CountOnly: true, Filters: []askFilter{f("name", "eq", "Ana")}})
		_, eb = fx.run(level, askPlan{Type: "nada", CountOnly: true, Filters: []askFilter{f("name", "eq", "Ana")}})
		sameErr(t, level+"/tipo contar", ea, eb, "user", "nada")
	}
	// admin: email e password negados = inexistentes, em ler, filtrar, ordenar e contar
	for _, hidden := range []string{"email", "password"} {
		for name, mk := range map[string]func(string) askPlan{
			"filtrar": func(x string) askPlan { return askPlan{Type: "user", Filters: []askFilter{f(x, "contains", "a")}} },
			"ordenar": func(x string) askPlan { return askPlan{Type: "user", Order: &askOrder{x, false}} },
			"contar": func(x string) askPlan {
				return askPlan{Type: "user", CountOnly: true, Filters: []askFilter{f(x, "eq", "hash-ana")}}
			},
			"ler": func(x string) askPlan { return askPlan{Type: "user", Fields: []string{x}} },
		} {
			_, ea := fx.run("admin", mk(hidden))
			_, eb := fx.run("admin", mk("inexistente_xyz"))
			sameErr(t, "admin/"+hidden+"/"+name, ea, eb, hidden, "inexistente_xyz")
		}
	}
	// e o que é visível ao admin continua funcionando, sem trazer o negado
	r := mustRun(t, fx, "admin", askPlan{Type: "user"})
	if len(r.Items) != 1 || len(r.Items[0].Keys) != 2 {
		t.Errorf("user para admin = %+v", r.Items)
	}
}

func TestAskWhereNaoSeAfrouxa(t *testing.T) {
	// com o status visível ao público, nenhum filtro consegue trazer rascunho
	djo := strings.Replace(askDjo, `"fields": ["title", "body", "published_at", "created_at", "views", "tags"]`, `"fields": ["title", "status", "views", "tags"]`, 1)
	fx := askFixture(t, djo)
	for _, p := range []askPlan{
		{Type: "post", Filters: []askFilter{f("status", "eq", "draft")}},
		{Type: "post", Filters: []askFilter{f("status", "in", "draft", "published"), f("title", "contains", "Draft")}},
		{Type: "post", Filters: []askFilter{f("tags", "has", "internal")}},
		{Type: "post", Filters: []askFilter{f("views", "lte", int64(0))}},
		{Type: "post", CountOnly: true, Filters: []askFilter{f("status", "eq", "draft")}},
		{Type: "post", Total: true, Filters: []askFilter{f("title", "starts", "draft")}},
	} {
		r, err := fx.run("public", p)
		if err != nil {
			t.Errorf("%+v: %v", p, err)
			continue
		}
		if len(r.Items) != 0 || (r.HasCount && r.Count != 0) {
			t.Errorf("%+v trouxe rascunho: %s count=%d", p, titles(r), r.Count)
		}
	}
	// contar tudo pelo público dá só os publicados
	if r := mustRun(t, fx, "public", askPlan{Type: "post", CountOnly: true}); r.Count != 5 {
		t.Errorf("count público = %d, queria 5", r.Count)
	}
}

func TestAskInjecao(t *testing.T) {
	fx := askFixture(t, askDjo)
	evil := []string{
		"'; DROP TABLE posts; --", "' OR '1'='1", `" OR 1=1 --`, "x') OR 1=1 --", "%' OR title LIKE '%", "\\", "\x00", "Robert'); DROP TABLE posts;--",
	}
	rows := func() int {
		var n int
		fx.db.QueryRow("SELECT COUNT(*) FROM posts").Scan(&n)
		return n
	}
	for _, v := range evil {
		for _, fl := range []askFilter{f("title", "eq", v), f("title", "contains", v), f("title", "starts", v), f("tags", "has", v), f("body", "contains", v)} {
			r, err := fx.run("public", askPlan{Type: "post", Filters: []askFilter{fl}})
			if strings.ContainsRune(v, 0) {
				if err == nil {
					t.Errorf("%s: NUL deveria ser recusado", fl.Field)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s %q: %v", fl.Field, v, err)
				continue
			}
			if len(r.Items) != 0 {
				t.Errorf("%s %s %q devolveu linhas: %s", fl.Field, fl.Op, v, titles(r))
			}
		}
		// enum e número recusam valor que não é do campo, em vez de repassar
		if _, err := fx.run("agent", askPlan{Type: "post", Filters: []askFilter{f("status", "eq", v)}}); err == nil {
			t.Errorf("enum aceitou %q", v)
		}
		if _, err := fx.run("public", askPlan{Type: "post", Filters: []askFilter{f("views", "eq", v)}}); err == nil {
			t.Errorf("número aceitou texto %q", v)
		}
		if _, err := fx.run("public", askPlan{Type: "post", Filters: []askFilter{f("published_at", "from", v)}}); err == nil {
			t.Errorf("data aceitou texto %q", v)
		}
	}
	if rows() != 6 {
		t.Fatalf("a tabela mudou: %d linhas", rows())
	}
	// o nome de campo, o operador, o tipo e a ordem nunca viram SQL
	for _, name := range []string{"title; DROP TABLE posts", `title" OR 1=1 --`, "title) OR (1=1", "1", "", "t0.titulo", "titulo", "post_tags.tag", "title,views"} {
		if _, err := fx.run("public", askPlan{Type: "post", Filters: []askFilter{f(name, "eq", "x")}}); err == nil || err.(askErr).Kind != "unknown_field" {
			t.Errorf("campo %q: %v", name, err)
		}
		if _, err := fx.run("public", askPlan{Type: "post", Order: &askOrder{name, false}}); err == nil {
			t.Errorf("ordem por %q foi aceita", name)
		}
		if _, err := fx.run("public", askPlan{Type: "post", Fields: []string{name}}); err == nil {
			t.Errorf("campo %q foi aceito em Fields", name)
		}
	}
	for _, op := range []string{"eq OR 1=1", "= 'x' OR '1'='1", "EQ", "", "like", "contains; --"} {
		if _, err := fx.run("public", askPlan{Type: "post", Filters: []askFilter{f("title", op, "x")}}); err == nil || err.(askErr).Kind != "bad_op" {
			t.Errorf("operador %q: %v", op, err)
		}
	}
	for _, typ := range []string{"posts", "post; DROP TABLE posts", "POST", "", "users"} {
		if _, err := fx.run("public", askPlan{Type: typ}); err == nil || err.(askErr).Kind != "unknown_type" {
			t.Errorf("tipo %q: %v", typ, err)
		}
	}
	if _, err := fx.run("public' OR '1'='1", askPlan{Type: "post"}); err == nil {
		t.Error("nível inventado foi aceito")
	}
	if rows() != 6 {
		t.Fatalf("a tabela mudou: %d linhas", rows())
	}
}

func TestAskLikeEscapa(t *testing.T) {
	fx := askFixture(t, askDjo)
	for _, c := range []struct{ val, want string }{
		{"%", "Beta 100% sure"}, // só o título que tem o % de verdade
		{"100%", "Beta 100% sure"},
		{"_", "Delta_x"}, // só o que tem o _ de verdade (e não "qualquer caractere")
		{"a_p", ""},      // "Alpha" casaria se o _ fosse curinga
		{"!", ""},
		{"%%", ""},
	} {
		r := mustRun(t, fx, "public", askPlan{Type: "post", Filters: []askFilter{f("title", "contains", c.val)}})
		if titles(r) != c.want {
			t.Errorf("contains %q = %q, queria %q", c.val, titles(r), c.want)
		}
	}
	if r := mustRun(t, fx, "public", askPlan{Type: "post", Filters: []askFilter{f("title", "starts", "_")}}); len(r.Items) != 0 {
		t.Errorf("starts '_' casou: %s", titles(r))
	}
}

func TestAskValoresInvalidos(t *testing.T) {
	fx := askFixture(t, askDjo)
	cases := map[string]askPlan{
		"texto longo demais":       {Type: "post", Filters: []askFilter{f("title", "eq", strings.Repeat("a", 201))}},
		"texto com número":         {Type: "post", Filters: []askFilter{f("title", "eq", int64(5))}},
		"between com 1 valor":      {Type: "post", Filters: []askFilter{f("views", "between", int64(1))}},
		"contains em número":       {Type: "post", Filters: []askFilter{f("views", "contains", "1")}},
		"enum fora da lista":       {Type: "post", Filters: []askFilter{f("status", "eq", "arquivado")}},
		"enum in vazio":            {Type: "post", Filters: []askFilter{f("status", "in")}},
		"ano inválido":             {Type: "post", Filters: []askFilter{f("published_at", "year", int64(0))}},
		"ano texto":                {Type: "post", Filters: []askFilter{f("published_at", "year", "2025")}},
		"data com operador errado": {Type: "post", Filters: []askFilter{f("published_at", "contains", "2025")}},
		"tags com eq":              {Type: "post", Filters: []askFilter{f("tags", "eq", "go")}},
		"ordenar por tags":         {Type: "post", Order: &askOrder{"tags", false}},
		"sem campos":               {Type: "post", Fields: []string{}},
		"Date em campo texto":      {Type: "post", Filters: []askFilter{f("title", "eq", d("2025-01-01"))}},
		"9999-12-31 depois":        {Type: "post", Filters: []askFilter{f("published_at", "to", d("9999-12-31"))}},
	}
	for name, p := range cases {
		p := p
		if name == "sem campos" {
			p.Fields = []string{}
		}
		if _, err := fx.run("agent", p); err == nil {
			t.Errorf("%s: deveria dar erro", name)
		}
	}
}

func TestAskLimiteTempoEErros(t *testing.T) {
	fx := askFixture(t, strings.Replace(askDjo, `"max_results": 50`, `"max_results": 3`, 1))
	// max_results é teto, venha o limite que vier
	for _, lim := range []int64{0, 3, 4, 1000, 1 << 40, -5} {
		r := mustRun(t, fx, "public", askPlan{Type: "post", Limit: lim})
		if len(r.Items) > 3 || r.Limit > 3 {
			t.Errorf("limite %d trouxe %d itens (limite %d)", lim, len(r.Items), r.Limit)
		}
	}
	if r := mustRun(t, fx, "public", askPlan{Type: "post", Limit: 1 << 40}); !r.More || len(r.Items) != 3 {
		t.Errorf("com teto 3: %d itens, more=%v", len(r.Items), r.More)
	}
	// a página não estoura
	if _, err := fx.run("public", askPlan{Type: "post", Limit: 2, Page: 1 << 60}); err != nil {
		t.Errorf("página enorme: %v", err)
	}

	// o tempo: uma consulta que não termina é cortada em timeout_ms (3000 no fixture; aqui 100)
	slow := askFixture(t, strings.Replace(askDjo, `"timeout_ms": 3000`, `"timeout_ms": 100`, 1))
	start := time.Now()
	_, err := slow.cfg.run(blockingRunner{}, kindSQLite, "public", askPlan{Type: "post"})
	if err == nil || err.(askErr).Kind != "timeout" || time.Since(start) > 2*time.Second {
		t.Errorf("timeout: %v em %v", err, time.Since(start))
	}
	// um erro do banco não vaza detalhe na mensagem
	_, err = fx.cfg.run(failingRunner{}, kindSQLite, "public", askPlan{Type: "post"})
	ae, ok := err.(askErr)
	if !ok || ae.Kind != "db" || strings.Contains(ae.Msg, "/var/secret") || !strings.Contains(ae.Cause, "/var/secret") {
		t.Errorf("erro do banco: %+v", err)
	}
}

type blockingRunner struct{}

func (blockingRunner) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (blockingRunner) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type failingRunner struct{}

func (failingRunner) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	return nil, errors.New("falhou em /var/secret/banco.db")
}
func (failingRunner) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	return nil, errors.New("falhou em /var/secret/banco.db")
}

type captureRunner struct {
	sqls   []string
	params [][]any
}

func (c *captureRunner) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	return nil, errors.New("captura")
}
func (c *captureRunner) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	c.sqls, c.params = append(c.sqls, q), append(c.params, a)
	return nil, errors.New("captura")
}

// O SQL para PostgreSQL e MySQL só é conferido como TEXTO aqui (aspas, marcadores, parâmetros);
// NÃO foi executado contra servidores reais.
func TestAskSQLPorDialeto(t *testing.T) {
	fx := askFixture(t, askDjo)
	plan := askPlan{Type: "post", Filters: []askFilter{
		f("title", "contains", "a_b"), f("published_at", "to", d("2025-03-30")), f("tags", "has", "Go"),
	}, Order: &askOrder{"views", true}, Limit: 5, Page: 2}
	for kind, want := range map[string][]string{
		kindSQLite:   {`FROM "posts" AS t0`, `t0."titulo"`, `LOWER(t0."titulo") LIKE ? ESCAPE '!'`, `"publicar_em" < ?`, `FROM "post_tags" AS g`, `ORDER BY t0."views" DESC, t0."id" LIMIT 6 OFFSET 5`},
		kindPostgres: {`FROM "posts" AS t0`, `LIKE $2 ESCAPE '!'`, `"publicar_em" < $3`, `LOWER(g."tag") = $4`, `LIMIT 6 OFFSET 5`},
		kindMySQL:    {"FROM `posts` AS t0", "t0.`titulo`", "LIKE ? ESCAPE '!'", "`publicar_em` < ?", "FROM `post_tags` AS g", "ORDER BY t0.`views` DESC, t0.`id` LIMIT 6 OFFSET 5"},
	} {
		cap := &captureRunner{}
		fx.cfg.run(cap, kind, "public", plan)
		if len(cap.sqls) == 0 {
			t.Fatalf("%s: nenhum SQL", kind)
		}
		for _, w := range want {
			if !strings.Contains(cap.sqls[0], w) {
				t.Errorf("%s: falta %q em\n%s", kind, w, cap.sqls[0])
			}
		}
		if strings.Contains(cap.sqls[0], "a_b") || strings.Contains(cap.sqls[0], "2025-03") {
			t.Errorf("%s: um valor foi para dentro do SQL:\n%s", kind, cap.sqls[0])
		}
	}
	// Date num servidor remoto vai como time.Time (UTC), no SQLite como texto
	cap := &captureRunner{}
	fx.cfg.run(cap, kindPostgres, "public", askPlan{Type: "post", Filters: []askFilter{f("published_at", "from", d("2025-03-30"))}})
	var found bool
	for _, p := range cap.params[0] {
		if tm, ok := p.(time.Time); ok && tm.Location() == time.UTC && tm.Format("2006-01-02") == "2025-03-30" {
			found = true
		}
	}
	if !found {
		t.Errorf("parâmetro de data no PostgreSQL: %v", cap.params[0])
	}
}
