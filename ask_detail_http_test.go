//go:build !noweb && !nodb

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// askDetailSite monta um site de verdade com o tipo post (texto longo em detail).
func askDetailSite(t *testing.T, public string) *askSite {
	t.Helper()
	root := filepath.Join(t.TempDir(), "site")
	files := map[string]string{
		"core/c.djo": askCore + askHook,
		"ask.djo":    askDetailDjo(public, "Ask.level_from:\"nivel_do_pedido\"\n"),
	}
	for name, content := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := openDB(filepath.Join(root, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	askDetailRows(t, db)
	srv, err := NewServer(root, false)
	if err != nil {
		t.Fatal(err)
	}
	srv.maxSteps = 1_000_000
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &askSite{ts, srv}
}

func TestAskHTTPDetailListaEItem(t *testing.T) {
	a := askDetailSite(t, askDetailAll)
	// lista: nada do texto longo, nem no JSON "data"
	st, b := a.ask(t, "list posts", "")
	if st != 200 || strings.Contains(b, `"body"`) || strings.Contains(b, "LINHA1") || strings.Contains(b, "CORPO") || strings.Contains(b, "script") {
		t.Fatalf("lista: %d %s", st, b)
	}
	if st, b = a.ask(t, "quantos posts", ""); st != 200 || strings.Contains(b, "CORPO") {
		t.Errorf("contagem: %d %s", st, b)
	}
	// um item: o texto vem, junto com o aviso de quais campos são longos
	st, b = a.ask(t, `show me the post "bem-vindo"`, "")
	flat := strings.ReplaceAll(b, " ", "")
	for _, want := range []string{`"item":{`, `"body":"LINHA1\n\u003cscript\u003e`, `"detail_fields":["body"]`, `"truncated":false`, `"op":"show"`, `"answer":"Hereisthepost."`} {
		if st != 200 || !strings.Contains(flat, strings.ReplaceAll(want, " ", "")) {
			t.Errorf("faltou %s em %d %s", want, st, b)
		}
	}
	if strings.Contains(b, `"data"`) || strings.Contains(b, "TOPSECRET") || strings.Contains(b, "CORPO-DO-RASCUNHO") {
		t.Errorf("item com sobras: %s", b)
	}
	// rascunho: o público recebe exatamente a mesma resposta de um que não existe
	_, hidden := a.ask(t, `show post "rascunho"`, "")
	_, none := a.ask(t, `show post "nao-existe"`, "")
	if strings.ReplaceAll(hidden, "rascunho", "X") != strings.ReplaceAll(none, "nao-existe", "X") || strings.Contains(hidden, "CORPO-DO-RASCUNHO") {
		t.Errorf("escondido x inexistente:\n%s\n%s", hidden, none)
	}
	if !strings.Contains(none, `"answer":"I did not find it."`) && !strings.Contains(none, `"answer": "I did not find it."`) {
		t.Errorf("não achei: %s", none)
	}
	// o agent acha o rascunho
	if st, b = a.ask(t, `show post "rascunho"`, "tok-agent"); st != 200 || !strings.Contains(b, "CORPO-DO-RASCUNHO") || !strings.Contains(b, `"status"`) {
		t.Errorf("agent: %d %s", st, b)
	}
	// ambíguo: lista curta sem texto
	if st, b = a.ask(t, `mostre o post "Go tips"`, ""); st != 200 || strings.Contains(b, "CORPO") || !strings.Contains(strings.ReplaceAll(b, " ", ""), `"ambiguous":true`) || !strings.Contains(strings.ReplaceAll(b, " ", ""), `"item":null`) {
		t.Errorf("ambíguo: %d %s", st, b)
	}
	// uma frase em PT: a resposta fala PT
	if _, b = a.ask(t, `mostre o post "bem-vindo"`, ""); !strings.Contains(b, "Encontrei: post.") {
		t.Errorf("PT: %s", b)
	}
	// o nível pode não listar o texto longo: então nem o item o traz
	a2 := askDetailSite(t, `["slug", "title"]`)
	if _, b = a2.ask(t, `show post "bem-vindo"`, ""); strings.Contains(b, "LINHA1") || strings.Contains(b, `"body"`) {
		t.Errorf("campo fora do nível: %s", b)
	}
}

func TestAskHTTPDetailPaginaEscapada(t *testing.T) {
	a := askDetailSite(t, askDetailAll)
	req, _ := http.NewRequest("POST", a.ts.URL+"/ask", strings.NewReader("question=show+post+%22bem-vindo%22"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", a.ts.URL)
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	page := sb.String()
	if resp.StatusCode != 200 || !strings.Contains(page, "<pre>LINHA1\n&lt;script&gt;alert(1)&lt;/script&gt;</pre>") {
		t.Fatalf("o texto longo vai em <pre> escapado: %d\n%s", resp.StatusCode, page)
	}
	if strings.Contains(page, "<script>alert") {
		t.Error("HTML do site virou marcação na página")
	}
	if !strings.Contains(page, "<h3>body</h3>") || !strings.Contains(page, "<dt>title</dt><dd>Bem-vindo ao djo</dd>") {
		t.Errorf("faltam os campos do item:\n%s", page)
	}
}

func TestAskRotuloPTNoEncontrei(t *testing.T) {
	cfg := askDetailFx(t, askPageDjo).cfg
	if got := askSingularLabel(cfg, "pt", "page"); got != "página" {
		t.Errorf("pt: %q", got)
	}
	if got := askSingularLabel(cfg, "en", "page"); got != "page" {
		t.Errorf("en: %q", got)
	}
	// sem apelido no singular, vale o nome do tipo
	cfg2 := askDetailFx(t, askDetailDjo(askDetailAll, "")).cfg
	if got := askSingularLabel(cfg2, "pt", "post"); got != "post" {
		t.Errorf("sem apelido singular: %q", got)
	}
}
