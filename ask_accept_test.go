//go:build !nodb

package main

import (
	"strings"
	"testing"
)

// Tabela de aceitação: frases que uma pessoa escreve de verdade, em PT e EN.
// Cada linha: nível, frase, plano esperado (ver planStr).
var askAcceptCases = []struct{ level, phrase, want string }{
	// --- a frase do usuário e variações de "onde ... e tenha tag ..." (PT)
	{"public", `liste posts onde o titulo contem "Primeiros" e tenha tag "teste"`, "pt:post; title contains Primeiros; tags has teste"},
	{"public", `liste posts onde o título contém "Primeiros" e tenha a tag "teste"`, "pt:post; title contains Primeiros; tags has teste"},
	{"public", `liste os posts cujo titulo contem "Alpha"`, "pt:post; title contains Alpha"},
	{"public", `liste posts com titulo contendo "Alpha"`, "pt:post; title contains Alpha"},
	{"public", `liste posts com o titulo igual a "Gamma"`, "pt:post; title eq Gamma"},
	{"public", `liste posts onde o titulo comeca com "Ga"`, "pt:post; title starts Ga"},
	{"public", `liste os posts que o titulo comece com "Ga"`, "pt:post; title starts Ga"},
	{"public", `liste posts onde titulo contem Primeiros`, "pt:post; title contains Primeiros"},
	{"public", `liste posts que tem a tag go`, "pt:post; tags has go"},
	{"public", `me mostra os posts que tem a tag go`, "pt:post; tags has go"},
	{"public", `liste posts que tenham a tag go`, "pt:post; tags has go"},
	{"public", `liste posts que tiver tag "go"`, "pt:post; tags has go"},
	{"public", `liste posts com tag go`, "pt:post; tags has go"},
	{"public", `liste posts com a tag go`, "pt:post; tags has go"},
	{"public", `liste posts marcados com go`, "pt:post; tags has go"},
	{"public", `liste posts marcados como "web"`, "pt:post; tags has web"},
	{"public", `liste posts tag go`, "pt:post; tags has go"},
	{"public", `liste posts com tag go e views maior que 10`, "pt:post; tags has go; views gt 10"},
	{"public", `liste posts com a tag go, titulo contem "a"`, "pt:post; tags has go; title contains a"},
	{"public", `liste posts onde titulo contem "a" e views pelo menos 5 e tag go`, "pt:post; title contains a; views gte 5; tags has go"},
	{"public", `liste posts onde o titulo contem "a" e tambem tenha a tag go`, "pt:post; title contains a; tags has go"},
	{"public", `liste posts ordenados por views decrescente com tag go`, "pt:post; tags has go; order views desc"},
	{"public", `liste posts primeiros 3 com tag go ordenados por views`, "pt:post; tags has go; order views asc; limit 3"},
	// --- contagem
	{"public", `quantos posts tem a tag go?`, "pt:post; tags has go; count"},
	{"public", `quantos posts tem a tag "go"`, "pt:post; tags has go; count"},
	{"public", `quantos artigos tem tag web`, "pt:post; tags has web; count"},
	{"public", `quantidade de posts com tag go`, "pt:post; tags has go; count"},
	{"public", `conte os posts de 2025`, "pt:post; published_at year 2025; count"},
	{"public", `quantos artigos em 2024`, "pt:post; published_at year 2024; count"},
	// --- datas, listas, ordem, limite (PT)
	{"public", `liste os posts de 2025-01-01 ate 2025-03-30`, "pt:post; published_at between 2025-01-01|2025-03-30"},
	{"public", `liste posts de 01/01/2025 ate 30/03/2025`, "pt:post; published_at between 2025-01-01|2025-03-30"},
	{"public", `liste posts desde 01/02/2025`, "pt:post; published_at from 2025-02-01"},
	{"public", `liste posts antes de 30/03/2025`, "pt:post; published_at before 2025-03-30"},
	{"public", `liste posts depois de 2025-03-30`, "pt:post; published_at after 2025-03-30"},
	{"public", `liste posts de 2025`, "pt:post; published_at year 2025"},
	{"public", `lista de posts em 2025`, "pt:post; published_at year 2025"},
	{"public", `me de a lista de artigos com tag go`, "pt:post; tags has go"},
	{"public", `quero ver os posts com views maior que 5`, "pt:post; views gt 5"},
	{"public", `liste posts com views entre 5 e 50`, "pt:post; views between 5|50"},
	{"public", `liste posts ordenados por views decrescente`, "pt:post; order views desc"},
	{"public", `liste posts ordenados por views`, "pt:post; order views asc"},
	{"public", `liste posts mais recentes primeiro`, "pt:post; order published_at desc"},
	{"public", `liste os posts mais antigos`, "pt:post; order published_at asc"},
	{"public", `liste os 5 primeiros posts`, "pt:post; limit 5"},
	{"public", `liste posts primeiros 5`, "pt:post; limit 5"},
	{"public", `liste posts limite 3 pagina 2`, "pt:post; limit 3; page 2"},
	{"agent", `mostre posts com status published`, "pt:post; status eq published"},
	{"agent", `liste posts onde status e draft`, "pt:post; status eq draft"},
	{"agent", `liste posts com status draft e a tag internal`, "pt:post; status eq draft; tags has internal"},
	{"admin", `liste users onde name contem "Ana"`, "pt:user; name contains Ana"},
	// --- EN
	{"public", `list posts where the title contains "Primeiros" and has tag "teste"`, "en:post; title contains Primeiros; tags has teste"},
	{"public", `list posts whose title contains "Alpha"`, "en:post; title contains Alpha"},
	{"public", `list posts with title containing "Alpha"`, "en:post; title contains Alpha"},
	{"public", `list posts where title is equal to "Gamma"`, "en:post; title eq Gamma"},
	{"public", `list posts where the title starts with "Ga"`, "en:post; title starts Ga"},
	{"public", `list posts with a title starting with "Ga"`, "en:post; title starts Ga"},
	{"public", `list posts where title contains alpha`, "en:post; title contains alpha"},
	{"public", `show me posts that have the tag go`, "en:post; tags has go"},
	{"public", `show me the posts which have tag go`, "en:post; tags has go"},
	{"public", `list posts that has the tag "go"`, "en:post; tags has go"},
	{"public", `list posts with tag go`, "en:post; tags has go"},
	{"public", `list posts with the tag go`, "en:post; tags has go"},
	{"public", `list posts tagged go`, "en:post; tags has go"},
	{"public", `list posts tagged with go`, "en:post; tags has go"},
	{"public", `list posts tag go`, "en:post; tags has go"},
	{"public", `how many posts have the tag go?`, "en:post; tags has go; count"},
	{"public", `how many posts are tagged go`, "en:post; tags has go; count"},
	{"public", `count posts with tag go`, "en:post; tags has go; count"},
	{"public", `count posts in 2025`, "en:post; published_at year 2025; count"},
	{"public", `list posts tagged "go" and with views greater than 10`, "en:post; tags has go; views gt 10"},
	{"public", `list posts tagged go, whose title contains "a"`, "en:post; tags has go; title contains a"},
	{"public", `list posts where title contains "a" and views at least 5 and tag go`, "en:post; title contains a; views gte 5; tags has go"},
	{"public", `list the posts from 2025-01-01 to 2025-03-30`, "en:post; published_at between 2025-01-01|2025-03-30"},
	{"public", `give me a list of posts from 2025-01-01 to 2026-03-30`, "en:post; published_at between 2025-01-01|2026-03-30"},
	{"public", `list posts since 2025-03-01`, "en:post; published_at from 2025-03-01"},
	{"public", `list posts in 2025`, "en:post; published_at year 2025"},
	{"public", `list posts sorted by views descending`, "en:post; order views desc"},
	{"public", `list posts ordered by views`, "en:post; order views asc"},
	{"public", `list posts newest first`, "en:post; order published_at desc"},
	{"public", `list the newest posts`, "en:post; order published_at desc"},
	{"public", `list the oldest posts`, "en:post; order published_at asc"},
	{"public", `list the first 5 posts`, "en:post; limit 5"},
	{"public", `show the top 3 posts`, "en:post; limit 3"},
	{"public", `list posts limit 2 page 2`, "en:post; limit 2; page 2"},
	{"public", `find posts with views between 5 and 50`, "en:post; views between 5|50"},
	{"agent", `list posts where status is draft`, "en:post; status eq draft"},
	{"agent", `list posts with status draft and the tag internal`, "en:post; status eq draft; tags has internal"},
	{"admin", `list users where the name contains "Ana"`, "en:user; name contains Ana"},
}

func TestAskAceitacaoFrases(t *testing.T) {
	fx := parseFx(t)
	n := 0
	for _, c := range askAcceptCases {
		if c.want == "" {
			if _, err := fx.cfg.parsePhrase(c.level, c.phrase); err == nil {
				t.Errorf("%q: lixo foi aceito", c.phrase)
			}
			continue
		}
		n++
		got, err := fx.cfg.parsePhrase(c.level, c.phrase)
		if err != nil {
			t.Errorf("%s %q:\n  recusada: %v", c.level, c.phrase, err)
			continue
		}
		if s := planStr(got); s != c.want {
			t.Errorf("%s %q:\n  veio    %s\n  queria  %s", c.level, c.phrase, s, c.want)
			continue
		}
		// e o plano roda no motor sem erro
		if _, rerr := fx.run(c.level, got.Plan); rerr != nil {
			t.Errorf("%q: o motor recusou o plano: %v", c.phrase, rerr)
		}
	}
	if n < 60 {
		t.Fatalf("a tabela de aceitação precisa de no mínimo 60 frases (tem %d)", n)
	}
}

// O que continua recusado DE PROPÓSITO precisa explicar o motivo (e não repetir "não entendi").
func TestAskRecusasExplicam(t *testing.T) {
	fx := parseFx(t)
	cases := []struct{ phrase, want string }{
		// OR
		{`list posts with tag go or tag web`, "OR is not supported"},
		{`list posts where title contains "a" or views at least 5`, "OR is not supported"},
		{`liste posts com tag go ou tag web`, "OU não existe"},
		{`liste posts onde titulo contem "a" ou titulo contem "b"`, "OU não existe"},
		// negação
		{`list posts without tag go`, "not supported"},
		{`list posts not tagged go`, "not supported"},
		{`list posts where title does not contain "a"`, "not supported"},
		{`liste posts sem a tag go`, "não é aceita"},
		{`liste posts onde titulo nao contem "a"`, "não é aceita"},
		{`liste posts exceto os de 2025`, "não é aceita"},
		// mês por extenso
		{`list posts from january 2025`, "Month names"},
		{`list posts in march`, "Month names"},
		{`liste posts em janeiro de 2025`, "meses por extenso"},
		{`liste posts de marco`, "meses por extenso"},
		// datas relativas
		{`list posts from today`, "Relative dates"},
		{`list posts since yesterday`, "Relative dates"},
		{`list posts from last week`, "Relative dates"},
		{`list posts this month`, "Relative dates"},
		{`liste posts de hoje`, "datas relativas"},
		{`liste posts de ontem`, "datas relativas"},
		{`liste posts do ultimo mes`, "datas relativas"},
		{`liste posts desde a semana passada`, "datas relativas"},
	}
	for _, c := range cases {
		_, err := fx.cfg.parsePhrase("public", c.phrase)
		if err == nil {
			t.Errorf("%q: deveria ser recusada", c.phrase)
			continue
		}
		e := err.(askErr)
		if e.Kind != "unsupported" || !strings.Contains(e.Msg, c.want) {
			t.Errorf("%q:\n  kind=%s msg=%q\n  queria algo com %q", c.phrase, e.Kind, e.Msg, c.want)
		}
		if strings.Contains(e.Msg, "I did not understand") || strings.Contains(e.Msg, "Não entendi") {
			t.Errorf("%q: a recusa deveria explicar, não dizer só 'não entendi': %s", c.phrase, e.Msg)
		}
	}
}

// A mensagem de erro diz o que já foi entendido, onde parou e dá um exemplo do tipo de cláusula que falhou.
func TestAskErroAjuda(t *testing.T) {
	fx := parseFx(t)
	cases := []struct {
		phrase string
		want   []string
		not    []string
	}{
		{`liste posts onde o titulo contem "Primeiros" e blablabla tag "teste"`,
			[]string{"Entendi: posts com title contendo \"Primeiros\"", "a partir de \"blablabla\"", "com a tag"}, []string{"de 2025-01-01 até"}},
		{`list posts where title contains "Alpha" and zzz`,
			[]string{"I understood: posts with title containing \"Alpha\"", "from \"zzz\""}, nil},
		{`list posts from 2025-01-01 to`, []string{"date"}, nil},
		{`list posts where title is`, []string{"title contains"}, []string{"from 2025-01-01 to 2025-12-31"}},
		{`list posts tagged`, []string{"tag"}, nil},
		{`list posts with views many`, []string{"number"}, nil},
	}
	for _, c := range cases {
		_, err := fx.cfg.parsePhrase("public", c.phrase)
		if err == nil {
			t.Errorf("%q: deveria falhar", c.phrase)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%q: falta %q em:\n  %s", c.phrase, w, err.Error())
			}
		}
		for _, w := range c.not {
			if strings.Contains(err.Error(), w) {
				t.Errorf("%q: não devia ter %q em:\n  %s", c.phrase, w, err.Error())
			}
		}
	}
	// o que a pessoa digitou nunca volta cru: só palavras e valores "limpos"
	_, err := fx.cfg.parsePhrase("public", `list posts where title contains "<script>alert(1)</script>" and ;DROP`)
	if err == nil || strings.Contains(err.Error(), "<script") || strings.Contains(err.Error(), "DROP") || strings.Contains(err.Error(), "alert(1)") {
		t.Errorf("a mensagem reflete texto cru: %v", err)
	}
	// e o que está escondido responde igual ao que não existe, mesmo com a explicação
	_, ea := fx.cfg.parsePhrase("public", `list posts where title contains "a" and secret contains "x"`)
	_, eb := fx.cfg.parsePhrase("public", `list posts where title contains "a" and zzzzzz contains "x"`)
	if ea == nil || eb == nil || strings.ReplaceAll(ea.Error(), "secret", "W") != strings.ReplaceAll(eb.Error(), "zzzzzz", "W") {
		t.Errorf("escondido x inexistente:\n  %v\n  %v", ea, eb)
	}
}

// Nomes comuns em português valem sem Ask.alias, mas só para campos que o nível enxerga.
func TestAskApelidosPadraoPT(t *testing.T) {
	semAlias := strings.Replace(askDjo, "Ask.alias:\"post.title\":[\"titulo\"]\n", "", 1)
	semAlias = strings.Replace(semAlias, "Ask.alias:\"post.published_at\":[\"published\", \"publicado em\"]\n", "", 1)
	if strings.Contains(semAlias, "post.title") || strings.Contains(semAlias, "post.published_at") {
		t.Fatal("a fixture ainda tem alias")
	}
	fx := askFixture(t, semAlias)
	ok := []struct{ level, phrase, want string }{
		{"public", `liste posts onde o título contém "a"`, "pt:post; title contains a"},
		{"public", `liste posts com titulo contendo "a" e etiquetas go`, "pt:post; title contains a; tags has go"},
		{"public", `liste posts publicado em 2025`, "pt:post; published_at year 2025"},
		{"public", `liste posts ordenados por criado decrescente`, "pt:post; order created_at desc"},
		{"agent", `liste posts com situacao draft`, "pt:post; status eq draft"},
		{"admin", `liste users onde nome contem "Ana"`, "pt:user; name contains Ana"},
	}
	for _, c := range ok {
		got, err := fx.cfg.parsePhrase(c.level, c.phrase)
		if err != nil || planStr(got) != c.want {
			t.Errorf("%s %q: %v %v", c.level, c.phrase, got, err)
		}
	}
	// escondido: "situacao" (status) não existe para o público, e responde igual a um nome qualquer
	_, ea := fx.cfg.parsePhrase("public", `liste posts com situacao draft`)
	_, eb := fx.cfg.parsePhrase("public", `liste posts com zzzzzz draft`)
	if ea == nil || eb == nil || strings.ReplaceAll(ea.Error(), "situacao", "W") != strings.ReplaceAll(eb.Error(), "zzzzzz", "W") {
		t.Errorf("escondido x inexistente:\n  %v\n  %v", ea, eb)
	}
	// negado: nenhum apelido padrão traz um campo negado (user.email/password não têm apelido, e o negado nunca entra)
	for _, ph := range []string{`liste users onde email contem "a"`, `liste users onde senha contem "a"`} {
		if _, err := fx.cfg.parsePhrase("admin", ph); err == nil {
			t.Errorf("%q: campo negado aceito", ph)
		}
	}
	// o que o ask.djo declarou ganha do padrão
	custom := strings.Replace(semAlias, `Ask.limits`, "Ask.alias:\"post.body\":[\"titulo\"]\nAsk.limits", 1)
	fy := askFixture(t, custom)
	got, err := fy.cfg.parsePhrase("public", `liste posts onde titulo contem "a"`)
	if err != nil || planStr(got) != "pt:post; body contains a" {
		t.Errorf("alias do site deveria vencer o padrão: %v %v", got, err)
	}
}
