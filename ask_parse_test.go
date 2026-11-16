//go:build !nodb

package main

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

func parseFx(t *testing.T) *askFx { return askFixture(t, askDjo) }

func planStr(p *askParsed) string {
	var parts []string
	pl := p.Plan
	parts = append(parts, p.Lang+":"+pl.Type)
	for _, f := range pl.Filters {
		vs := make([]string, len(f.Vals))
		for i, v := range f.Vals {
			vs[i] = fmt.Sprint(v)
		}
		parts = append(parts, f.Field+" "+f.Op+" "+strings.Join(vs, "|"))
	}
	if pl.Order != nil {
		d := "asc"
		if pl.Order.Desc {
			d = "desc"
		}
		parts = append(parts, "order "+pl.Order.Field+" "+d)
	}
	if pl.Limit > 0 {
		parts = append(parts, fmt.Sprint("limit ", pl.Limit))
	}
	if pl.Page > 0 {
		parts = append(parts, fmt.Sprint("page ", pl.Page))
	}
	if pl.CountOnly {
		parts = append(parts, "count")
	}
	return strings.Join(parts, "; ")
}

func TestAskParseValidas(t *testing.T) {
	fx := parseFx(t)
	cases := []struct{ level, phrase, want string }{
		// o exemplo do pedido, nos dois idiomas
		{"public", "give me a list of posts from 2025-01-01 to 2026-03-30", "en:post; published_at between 2025-01-01|2026-03-30"},
		{"public", "me dê uma lista de posts de 2025-01-01 até 2026-03-30", "pt:post; published_at between 2025-01-01|2026-03-30"},
		{"public", "Me da uma lista de artigos de 01/01/2025 ate 30/03/2026", "pt:post; published_at between 2025-01-01|2026-03-30"},
		{"public", "LIST POSTS", "en:post"},
		{"public", "liste os posts.", "pt:post"},
		{"public", "show me the posts since 2025-03-01", "en:post; published_at from 2025-03-01"},
		{"public", "list posts until 2025-03-30", "en:post; published_at to 2025-03-30"},
		{"public", "list posts before 2025-03-30", "en:post; published_at before 2025-03-30"},
		{"public", "liste posts depois de 2025-03-30", "pt:post; published_at after 2025-03-30"},
		{"public", "list posts on 2025-03-30", "en:post; published_at eq 2025-03-30"},
		{"public", "list posts in 2025", "en:post; published_at year 2025"},
		{"public", "liste posts em 2024", "pt:post; published_at year 2024"},
		{"public", "list posts between 2025-01-01 and 2025-02-01", "en:post; published_at between 2025-01-01|2025-02-01"},
		{"public", "liste posts entre 2025-01-01 e 2025-02-01", "pt:post; published_at between 2025-01-01|2025-02-01"},
		{"public", "list posts published from 2025-01-01 to 2025-02-01", "en:post; published_at between 2025-01-01|2025-02-01"},
		{"public", "liste artigos publicado em 2025", "pt:post; published_at year 2025"},
		{"public", "list posts created_at from 2025-03-30 to 2025-03-31", "en:post; created_at between 2025-03-30|2025-03-31"},
		// sinônimos e jeitos de falar
		{"public", "lista de posts de 2025", "pt:post; published_at year 2025"},
		{"public", "lista posts em 2025", "pt:post; published_at year 2025"},
		{"public", "mostra os posts", "pt:post"},
		{"public", "quero ver os artigos de 2025", "pt:post; published_at year 2025"},
		{"public", "traz posts", "pt:post"},
		{"public", "me traz os artigos primeiros 3", "pt:post; limit 3"},
		{"public", "me mostra os posts", "pt:post"},
		{"public", "veja os posts", "pt:post"},
		{"public", "quantidade de posts", "pt:post; count"},
		{"public", "display posts", "en:post"},
		{"public", "I want to see the posts from 2025-01-01 to 2025-02-01", "en:post; published_at between 2025-01-01|2025-02-01"},
		{"public", "tell me the posts", "en:post"},
		{"public", "liste posts de 2024 ate 2025", "pt:post; published_at between 2024-01-01|2025-12-31"},
		{"public", "list posts from 2025", "en:post; published_at year 2025"},
		// sem verbo, quando não há dúvida
		{"public", "first 2 posts", "en:post; limit 2"},
		{"public", "primeiros 2 posts", "pt:post; limit 2"},
		{"public", "primeiras 3 artigos", "pt:post; limit 3"},
		{"public", "posts de 2025", "pt:post; published_at year 2025"},
		{"public", "posts from 2025-01-01 to 2025-03-30", "en:post; published_at between 2025-01-01|2025-03-30"},
		// contar
		{"public", "how many posts", "en:post; count"},
		{"public", "quantos posts em 2025", "pt:post; published_at year 2025; count"},
		{"public", "count posts with views greater than 10", "en:post; views gt 10; count"},
		{"public", "conte os artigos", "pt:post; count"},
		// campos
		{"public", `list posts where title contains "alpha"`, "en:post; title contains alpha"},
		{"public", `liste posts com titulo contem "Alpha"`, "pt:post; title contains Alpha"},
		{"public", `list posts title starts with "Ga"`, "en:post; title starts Ga"},
		{"public", `liste posts título começa com "Ga"`, "pt:post; title starts Ga"},
		{"public", `list posts with title is "Gamma"`, "en:post; title eq Gamma"},
		{"public", "list posts with views at least 7", "en:post; views gte 7"},
		{"public", "liste posts com views maior que 7", "pt:post; views gt 7"},
		{"public", "list posts views between 5 and 50", "en:post; views between 5|50"},
		{"public", "liste posts views entre 5 e 50", "pt:post; views between 5|50"},
		{"agent", "list posts with status published", "en:post; status eq published"},
		{"agent", "liste posts com status draft", "pt:post; status eq draft"},
		{"public", "list posts tagged go", "en:post; tags has go"},
		{"public", `liste posts com a tag "web"`, "pt:post; tags has web"},
		{"public", "list posts with tag go and views more than 10", "en:post; tags has go; views gt 10"},
		// ordem, limite, página
		{"public", "list posts sorted by views desc", "en:post; order views desc"},
		{"public", "liste posts ordenados por views decrescente", "pt:post; order views desc"},
		{"public", "list posts newest first", "en:post; order published_at desc"},
		{"public", "liste posts mais antigos primeiro", "pt:post; order published_at asc"},
		{"public", "list the first 5 posts", "en:post; limit 5"},
		{"public", "list posts first 5 results page 2", "en:post; limit 5; page 2"},
		{"public", "liste posts primeiros 3 resultados pagina 2", "pt:post; limit 3; page 2"},
		{"public", "list posts, newest first, first 3", "en:post; order published_at desc; limit 3"},
		// admin vê user; ?, pontuação e espaços
		{"admin", "list users", "en:user"},
		{"admin", "  liste   users  ?? ", "pt:user"},
		{"admin", "list users created_at in 2025", "en:user; created_at year 2025"},
	}
	for _, c := range cases {
		got, err := fx.cfg.parsePhrase(c.level, c.phrase)
		if err != nil {
			t.Errorf("%q: %v", c.phrase, err)
			continue
		}
		if s := planStr(got); s != c.want {
			t.Errorf("%q:\n  veio    %s\n  queria  %s", c.phrase, s, c.want)
		}
	}
}

// toda frase válida tem de rodar no motor sem erro de plano
func TestAskParseRodaNoMotor(t *testing.T) {
	fx := parseFx(t)
	r0, err := fx.cfg.parsePhrase("public", "give me a list of posts from 2025-01-01 to 2025-03-30")
	if err != nil {
		t.Fatal(err)
	}
	r := mustRun(t, fx, "public", r0.Plan)
	if titles(r) != "Alpha|Beta 100% sure" {
		t.Errorf("resultado = %s", titles(r))
	}
	r0, _ = fx.cfg.parsePhrase("public", "liste artigos de 30/03/2025 ate 31/03/2025")
	if titles(mustRun(t, fx, "public", r0.Plan)) != "Beta 100% sure|Gamma" {
		t.Error("PT dd/mm/aaaa inclusivo")
	}
	r0, _ = fx.cfg.parsePhrase("public", `list posts where title contains "100%"`)
	if titles(mustRun(t, fx, "public", r0.Plan)) != "Beta 100% sure" {
		t.Error("% literal")
	}
}

func askKind(err error) string {
	if e, ok := err.(askErr); ok {
		return e.Kind
	}
	return "?"
}

func TestAskParseInvalidas(t *testing.T) {
	fx := parseFx(t)
	cases := []struct{ phrase, kind, has string }{
		{"", "not_understood", "Ask a question"},
		{"   ", "not_understood", "Ask a question"},
		{"hello there", "not_understood", "Não entendi"},
		{"posts", "not_understood", "Não entendi"},      // só o tipo: sem pista de idioma
		{"first 2 posts de 2025", "not_understood", ""}, // sem verbo e com idiomas misturados: não adivinha
		{"primeiros 2 posts from 2025", "not_understood", ""},
		{"first posts", "not_understood", ""},
		{"posts sorted", "not_understood", ""},
		{"list", "not_understood", "post"},
		{"hello there", "not_understood", "\n"}, // duas linhas, uma por idioma
		{"list cats", "not_understood", "post"},
		{"list posts banana", "not_understood", "post"},
		{"list posts from", "bad_value", "date"},
		{"list posts from yesterday", "unsupported", "Relative dates"},
		{"list posts from last week", "unsupported", "Relative dates"},
		{"list posts from 2025-13-45 to 2025-14-01", "bad_value", "does not exist"},
		{"list posts from 2025-02-30", "bad_value", "does not exist"},
		{"list posts from 30/03/2025 to 31/03/2025", "bad_value", "YYYY-MM-DD"}, // dd/mm/aaaa em EN
		{"list posts from 30/03 to 31/03", "bad_value", "YYYY-MM-DD"},
		{"liste posts de 30/03 ate 31/03", "bad_value", "AAAA-MM-DD"},
		{"liste posts de 31/02/2025", "bad_value", "não existe"},
		{"list posts with title", "bad_value", "quotes"},
		{`list posts with title contains "x`, "bad_phrase", "quotation"},
		{"list posts views greater than many", "bad_value", "number"},
		{"list posts first 0", "bad_value", "number"},
		{"list posts first -3", "bad_value", "number"},
		{"list posts first 99999999999999999999", "bad_value", "number"},
		{"list posts with views contains 5", "", ""}, // o motor recusa o operador (bad_op)
		// idiomas misturados
		{"liste posts from 2025-01-01 to 2025-03-30", "mixed_language", "Use um idioma"},
		{"list posts de 2025-01-01 ate 2025-03-30", "mixed_language", "one language"},
		{"liste posts sorted by views", "mixed_language", "Use um idioma"},
		{"list posts ordenados por views", "mixed_language", "one language"},
		// escrita: nunca aceita, em qualquer idioma
		{"delete all posts", "not_accepted", "Writing is not accepted"},
		{"apague todos os posts", "not_accepted", "Escrever não é aceito"},
		{"list posts and drop table posts", "not_accepted", ""},
		{"update posts set title to x", "not_accepted", ""},
		{"crie um post", "not_accepted", ""},
		{"list posts and delete them", "not_accepted", ""},
	}
	for _, c := range cases {
		got, err := fx.cfg.parsePhrase("public", c.phrase)
		if c.kind == "" {
			if err != nil {
				t.Errorf("%q: o parser deveria aceitar, veio %v", c.phrase, err)
				continue
			}
			if _, rerr := fx.run("public", got.Plan); rerr == nil || askKind(rerr) != "bad_op" {
				t.Errorf("%q: o motor deveria recusar com bad_op, veio %v", c.phrase, rerr)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q: deveria falhar, veio %s", c.phrase, planStr(got))
			continue
		}
		if askKind(err) != c.kind || !strings.Contains(err.Error(), c.has) {
			t.Errorf("%q: kind=%s msg=%q (queria %s / %q)", c.phrase, askKind(err), err.Error(), c.kind, c.has)
		}
	}
}

// campo/tipo escondido responde IGUAL a um que não existe (só a palavra refletida muda).
func TestAskParseEscondidoIgualInexistente(t *testing.T) {
	fx := parseFx(t)
	norm := func(msg, word string) string { return strings.ReplaceAll(msg, "\""+word+"\"", "\"X\"") }
	type pair struct{ level, tmpl, hidden string }
	for _, c := range []pair{
		{"public", "list posts with %s is \"x\"", "status"},
		{"public", "list posts with %s is \"x\"", "secret"},
		{"public", "list posts sorted by %s", "secret"},
		{"public", "liste posts onde o %s contem x", "secret"},
		{"public", "liste posts onde o titulo contem a e tenha %s x", "secret"},
		{"public", "list posts whose %s contains x", "status"},
		{"agent", "list posts that have the tag go and the %s containing x", "secret"},
		{"admin", "liste users cujo %s contem x", "password"},
		{"public", "list posts sorted by %s", "status"},
		{"public", "count posts where %s contains \"S\"", "secret"},
		{"agent", "list posts sorted by %s desc", "secret"},
		{"agent", "list posts where %s contains \"S\"", "secret"},
		{"public", "list %s", "user"},
		{"public", "count %s", "users"},
		{"agent", "list %s", "user"},
		{"admin", "list users where %s contains \"a\"", "email"},
		{"admin", "list users where %s contains \"a\"", "password"},
		{"admin", "list users sorted by %s", "password"},
	} {
		_, ea := fx.cfg.parsePhrase(c.level, fmt.Sprintf(c.tmpl, c.hidden))
		_, eb := fx.cfg.parsePhrase(c.level, fmt.Sprintf(c.tmpl, "zzzzzz"))
		if ea == nil || eb == nil {
			t.Errorf("%s %q: deveria falhar (%v / %v)", c.level, c.tmpl, ea, eb)
			continue
		}
		if askKind(ea) != askKind(eb) || norm(ea.Error(), c.hidden) != norm(eb.Error(), "zzzzzz") {
			t.Errorf("%s %q (%s) difere:\n  escondido:   %s\n  inexistente: %s", c.level, c.tmpl, c.hidden, ea, eb)
		}
	}
}

// nunca sai do plano um campo que o nível não enxerga, nem o nome real da coluna
func TestAskParseNaoVazaCampo(t *testing.T) {
	fx := parseFx(t)
	hiddenFor := map[string][]string{
		"public": {"status", "secret", "secreto"},
		"agent":  {"secret", "secreto"},
		"admin":  {"email", "password"},
	}
	phrases := []string{
		"list posts", "list posts sorted by secret", "list posts with secreto contains \"S\"", "list posts secret",
		"list posts with status is \"draft\"", "list users", "list users with email contains \"a\"",
		"liste posts com secret contem \"S\"", "list posts tagged internal", "count posts where password is \"x\"",
	}
	for level, hidden := range hiddenFor {
		v := fx.cfg.vocab(level)
		for _, ph := range phrases {
			got, err := fx.cfg.parsePhrase(level, ph)
			if err != nil {
				for _, h := range hidden {
					msg := regexp.MustCompile(`"[^"]*"`).ReplaceAllString(err.Error(), "X") // a palavra digitada pode voltar
					if strings.Contains(msg, h) {
						t.Errorf("%s %q: a mensagem lista o campo escondido %q: %s", level, ph, h, err)
					}
				}
				continue
			}
			view := fx.cfg.view(level, got.Plan.Type)
			for _, f := range got.Plan.Filters {
				if view == nil || !view.has(f.Field) {
					t.Errorf("%s %q: plano usa campo invisível %q", level, ph, f.Field)
				}
			}
			if got.Plan.Order != nil && !view.has(got.Plan.Order.Field) {
				t.Errorf("%s %q: ordena por campo invisível", level, ph)
			}
			_ = v
		}
	}
	// a ajuda do tipo visível lista só campos visíveis
	_, err := fx.cfg.parsePhrase("public", "list posts where zzz is \"x\"")
	for _, h := range []string{"secret", "status", "secreto"} {
		if err == nil || strings.Contains(err.Error(), h) {
			t.Errorf("a ajuda do público menciona %q: %v", h, err)
		}
	}
	_, err = fx.cfg.parsePhrase("public", "list things")
	if err == nil || strings.Contains(err.Error(), "user") {
		t.Errorf("a ajuda do público menciona o tipo escondido: %v", err)
	}
}

func TestAskParseInjecaoPorFrase(t *testing.T) {
	fx := parseFx(t)
	evil := []string{
		`list posts where title contains "'; DROP TABLE posts; --"`,
		`list posts where title is "' OR '1'='1"`,
		`list posts where title contains "%"`,
		`list posts where title contains "_"`,
		`list posts where title contains "\"`,
		`list posts tagged "x' OR 1=1 --"`,
		"list posts where title contains \"a\x00b\"",
		`list posts; DROP TABLE posts`,
		`list posts WHERE 1=1`,
		`list <script>alert(1)</script>`,
		`list posts sorted by title; DROP TABLE posts`,
		`liste posts com titulo contem "${x}" `,
	}
	rows := func() int {
		var n int
		fx.db.QueryRow("SELECT COUNT(*) FROM posts").Scan(&n)
		return n
	}
	for _, ph := range evil {
		got, err := fx.cfg.parsePhrase("public", ph)
		if err != nil {
			// nenhuma mensagem pode repetir <, >, ;, aspas cruas ou a frase
			for _, bad := range []string{"<script", "DROP", "';", "OR '1'"} {
				if strings.Contains(err.Error(), bad) {
					t.Errorf("%q: a mensagem reflete %q: %s", ph, bad, err)
				}
			}
			continue
		}
		fx.run("public", got.Plan) // pode recusar, mas nunca quebrar nem alterar
	}
	if rows() != 6 {
		t.Fatalf("a tabela mudou: %d linhas", rows())
	}
	// o texto entre aspas chega ao plano como valor puro
	got, err := fx.cfg.parsePhrase("public", `list posts where title contains "%' OR 1=1 --"`)
	if err != nil || got.Plan.Filters[0].Vals[0] != "%' OR 1=1 --" {
		t.Errorf("valor entre aspas: %v %v", got, err)
	}
	if r, err := fx.run("public", got.Plan); err != nil || len(r.Items) != 0 {
		t.Errorf("injeção por valor trouxe linhas: %v %v", r, err)
	}
}

func TestAskParseEnumInvalido(t *testing.T) {
	fx := parseFx(t)
	if _, err := fx.cfg.parsePhrase("agent", "list posts with status archived"); askKind(err) != "bad_value" {
		t.Errorf("enum fora da lista: %v", err)
	}
}

func TestAskParseLimites(t *testing.T) {
	fx := parseFx(t)
	if _, err := fx.cfg.parsePhrase("public", "list posts "+strings.Repeat("a", 300)); askKind(err) != "too_long" {
		t.Errorf("frase longa: %v", err)
	}
	if _, err := fx.cfg.parsePhrase("public", strings.Repeat("list ", 100)); askKind(err) != "too_long" {
		t.Errorf("muitas palavras: %v", err)
	}
	if _, err := fx.cfg.parsePhrase("public", "list posts\x00"); askKind(err) != "bad_phrase" {
		t.Errorf("NUL: %v", err)
	}
	if _, err := fx.cfg.parsePhrase("public", "list posts \xff\xfe"); askKind(err) != "bad_phrase" {
		t.Errorf("UTF-8 inválido: %v", err)
	}
	if _, err := fx.cfg.parsePhrase("public", "list posts \x1b[31m"); askKind(err) != "bad_phrase" {
		t.Errorf("controle: %v", err)
	}
	many := "list posts " + strings.Repeat("views is 1 ", 12)
	if _, err := fx.cfg.parsePhrase("public", many); err == nil || !strings.Contains(err.Error(), "Too many") {
		t.Errorf("condições demais: %v", err)
	}
}

// fuzz simples: nunca panica; ou plano válido (que o motor aceita ou recusa com askErr) ou erro askErr
func TestAskParseFuzz(t *testing.T) {
	fx := parseFx(t)
	words := []string{"list", "liste", "posts", "artigos", "post", "users", "from", "to", "de", "ate", "2025-01-01", "30/03/2025", "2025",
		"title", "titulo", "views", "tags", "status", "secret", "contains", "contem", "is", "e", "and", "\"x\"", "\"", "'", "%", "_", "0",
		"-1", "first", "page", "sorted", "by", "desc", "newest", "first", "tagged", "with", "where", "in", "em", "count", "how", "many",
		"delete", "onde", "que", "tenha", "tem", "tag", "marcado", "com", "ou", "or", "not", "sem", "hoje", "janeiro", "o", "a", "cujo", "whose", "has", "the", "contendo", "containing", "also", "tambem", "\x00", "ç", "é", "🙂", ",", ";", "(", ")", "=", ">", "<", ">=", "between", "entre", "99999999999999999999", "1.5", "published"}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 20000; i++ {
		n := 1 + rng.Intn(12)
		var parts []string
		for j := 0; j < n; j++ {
			parts = append(parts, words[rng.Intn(len(words))])
		}
		ph := strings.Join(parts, " ")
		for _, level := range []string{"public", "agent", "admin"} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("PANIC com %q (%s): %v", ph, level, r)
					}
				}()
				got, err := fx.cfg.parsePhrase(level, ph)
				if err != nil {
					if _, ok := err.(askErr); !ok {
						t.Fatalf("%q: erro que não é askErr: %v", ph, err)
					}
					return
				}
				if got.Plan.Type == "" || fx.cfg.view(level, got.Plan.Type) == nil {
					t.Fatalf("%q: plano com tipo inválido %+v", ph, got.Plan)
				}
				view := fx.cfg.view(level, got.Plan.Type)
				for _, f := range got.Plan.Filters {
					if !view.has(f.Field) {
						t.Fatalf("%q: campo invisível %q", ph, f.Field)
					}
				}
				if _, rerr := fx.run(level, got.Plan); rerr != nil {
					if _, ok := rerr.(askErr); !ok {
						t.Fatalf("%q: o motor devolveu erro cru: %v", ph, rerr)
					}
				}
			}()
		}
	}
	// bytes aleatórios
	for i := 0; i < 5000; i++ {
		b := make([]byte, rng.Intn(80))
		rng.Read(b)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC com bytes %q: %v", b, r)
				}
			}()
			fx.cfg.parsePhrase("admin", string(b))
		}()
	}
}
