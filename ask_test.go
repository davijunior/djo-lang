package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const askCore = `struct Post
    titulo[String]
    texto[String]
    status[String]
    publicado[DateTime]
    criado[Int]
    views[Int]
    secreto[String]
    tags[Arr]
`

const askDjo = `Ask.type:"post":{"table": "posts", "struct": "Post", "fields": {
    "title": {"column": "titulo", "attr": "titulo"},
    "body": {"column": "texto", "attr": "texto"},
    "status": {"kind": "enum", "values": ["draft", "published"], "attr": "status"},
    "published_at": {"column": "publicar_em", "attr": "publicado", "main": true},
    "created_at": {"column": "criado", "attr": "criado", "as": "epoch"},
    "views": {"attr": "views"},
    "secret": {"column": "secreto", "attr": "secreto"},
    "tags": {"kind": "tags", "from": "post_tags", "key": "post_id", "value": "tag", "attr": "tags"}
}}
Ask.type:"user":{"table": "users", "fields": {
    "name": {"kind": "text"},
    "email": {"kind": "text"},
    "password": {"kind": "text"},
    "created_at": {"kind": "datetime", "column": "criado"}
}}
Ask.level:"public":{"allow": [{"type": "post", "where": {"status": "published"}, "fields": ["title", "body", "published_at", "created_at", "views", "tags"]}]}
Ask.level:"agent":{"allow": [{"type": "post"}]}
Ask.level:"admin":{"allow": [{"type": "user"}]}
Ask.deny:["user.password", "user.email", "post.secret"]
Ask.alias:"post":["posts", "artigos"]
Ask.alias:"post.title":["titulo"]
Ask.alias:"post.published_at":["published", "publicado em"]
Ask.db:"t.db"
Ask.limits:{"max_results": 50, "per_minute": 30, "timeout_ms": 3000}
Ask.reply:{"refused": {"en": "I can't answer that.", "pt": "Não posso responder isso."}, "unknown": "I did not understand."}
`

func askLoad(t *testing.T, core, ask string) error {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "core"), 0o755)
	os.WriteFile(filepath.Join(root, "core", "c.djo"), []byte(core), 0o644)
	if ask != "" {
		os.WriteFile(filepath.Join(root, "ask.djo"), []byte(ask), 0o644)
	}
	return checkAsk(root)
}

func TestAskConfigOK(t *testing.T) {
	if err := askLoad(t, askCore, askDjo); err != nil {
		t.Fatal(err)
	}
	if err := askLoad(t, askCore, ""); err != nil { // sem ask.djo = /ask desligado
		t.Fatal(err)
	}
}

func TestAskConfigErros(t *testing.T) {
	lvl := `Ask.level:"public":{"allow": [{"type": "post"}]}` + "\n"
	typ := `Ask.type:"post":{"table": "posts", "struct": "Post", "fields": {"title": {"column": "titulo", "attr": "titulo"}}}` + "\n"
	cases := map[string]struct{ ask, want string }{
		"só Ask.* é aceito":     {"x = 1\n" + typ + lvl, "só aceita chamadas Ask"},
		"sem tipo":              {lvl, "declare Ask.type"},
		"sem nível":             {typ, "ao menos um Ask.level"},
		"nível inventado":       {typ + `Ask.level:"root":{"allow": [{"type": "post"}]}`, "root"},
		"tipo desconhecido":     {typ + `Ask.level:"public":{"allow": [{"type": "nada"}]}`, "nada"},
		"campo desconhecido":    {typ + `Ask.level:"public":{"allow": [{"type": "post", "fields": ["zzz"]}]}`, "zzz"},
		"chave desconhecida":    {`Ask.type:"post":{"table": "posts", "boom": 1, "fields": {"t": {"kind": "text"}}}` + "\n" + lvl, "boom"},
		"deny de campo ausente": {typ + lvl + `Ask.deny:["post.zzz"]`, "zzz"},
		"tabela com SQL":        {`Ask.type:"post":{"table": "posts; DROP TABLE x", "fields": {"t": {"kind": "text"}}}` + "\n" + lvl, "table"},
	}
	for name, c := range cases {
		err := askLoad(t, askCore, c.ask)
		if err == nil {
			t.Errorf("%s: deveria falhar", name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: erro %q não menciona %q", name, err, c.want)
		}
	}
}

func TestAskDjoNaoEServido(t *testing.T) {
	if !hiddenForTest("/ask.djo") || !hiddenForTest("/ASK.DJO") {
		t.Error("ask.djo não pode ser servido")
	}
}
