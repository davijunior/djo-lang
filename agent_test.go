//go:build !noweb && !nonet

package main

import (
	"bytes"
	"strings"
	"testing"
)

func agentRun(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := agentCmd(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestAgentManifesto(t *testing.T) {
	ts := site(t, map[string]string{
		"core/m.djo":  coreAcoes,
		"rotas.djo":   rotasAcoes,
		"p/posts.djo": `print "tela"`,
		"p/api.djo":   `print Json.encode:{"posts": [1, 2]}`,
		"p/sobre.djo": `print "sobre"`,
	})
	code, out, _ := agentRun(ts.URL)
	if code != 0 {
		t.Fatalf("código %d: %s", code, out)
	}
	for _, want := range []string{
		"djo-manifest/1",
		"Leituras (1)", "GET    /posts", "Lista os posts",
		"Ações (3)", "criar_post", "POST /posts", "Cria um post novo",
		"titulo: string (obrigatório)", "nota: number (obrigatório)", "publico: boolean (obrigatório)",
		"autor: objeto Autor (obrigatório)", "nome: string (obrigatório)", "idade: integer (opcional)",
		"na URL: id (integer)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q na saída:\n%s", want, out)
		}
	}
	// sem esquema http:// e com barra no fim
	if code, _, _ := agentRun(strings.TrimPrefix(ts.URL, "http://") + "/"); code != 0 {
		t.Errorf("endereço sem http:// falhou")
	}
	// -raw devolve o JSON do servidor
	if _, raw, _ := agentRun("-raw", ts.URL); !strings.Contains(raw, `"format": "djo-manifest/1"`) {
		t.Errorf("raw: %s", raw)
	}
}

func TestAgentRotaEAcao(t *testing.T) {
	ts := site(t, map[string]string{
		"core/m.djo":  coreAcoes,
		"rotas.djo":   rotasAcoes,
		"p/posts.djo": `print "tela"`,
		"p/api.djo":   `print Json.encode:{"posts": [1, 2]}`,
		"p/sobre.djo": `print "sobre"`,
	})
	code, out, _ := agentRun(ts.URL + "/posts")
	if code != 0 || !strings.Contains(out, "→ 200 OK  application/json") || !strings.Contains(out, "\"posts\": [") || !strings.Contains(out, `rel="ai-manifest"`) {
		t.Errorf("GET /posts = %d\n%s", code, out)
	}
	// rota só de tela: 406 com a dica
	code, out, _ = agentRun(ts.URL + "/sobre")
	if code != 1 || !strings.Contains(out, "406") || !strings.Contains(out, "não tem resposta de agente") {
		t.Errorf("GET /sobre = %d\n%s", code, out)
	}
	// ação
	code, out, _ = agentRun("-post", `{"id":1}`, ts.URL+"/posts/5/publicar")
	if code != 1 || !strings.Contains(out, "422") || !strings.Contains(out, "vem da URL") {
		t.Errorf("POST repetindo o id = %d\n%s", code, out)
	}
	code, out, _ = agentRun("-post", `{}`, ts.URL+"/posts/5/publicar")
	if code != 0 || !strings.Contains(out, `"id": 5`) {
		t.Errorf("POST ação = %d\n%s", code, out)
	}
}

func TestAgentSemManifestoEErros(t *testing.T) {
	ts := site(t, map[string]string{"index.djo": `print "x"`})
	code, _, errs := agentRun(ts.URL)
	if code != 1 || !strings.Contains(errs, "não publica manifesto") {
		t.Errorf("sem manifesto = %d %q", code, errs)
	}
	if code, _, _ := agentRun(); code != 2 {
		t.Errorf("sem argumento = %d", code)
	}
	if code, _, errs := agentRun("http://127.0.0.1:1"); code != 1 || !strings.Contains(errs, "não consegui falar") {
		t.Errorf("sem servidor = %d %q", code, errs)
	}
}

func TestAgentMetodo(t *testing.T) {
	ts := site(t, map[string]string{
		"core/m.djo": coreAcoesMetodos,
		"rotas.djo": `Route.put_action:"/posts/{id}":"trocar_post"
Route.delete_action:"/posts/{id}":"apagar_post"
`,
	})
	code, out, _ := agentRun("-method", "put", "-post", `{"titulo":"Novo"}`, ts.URL+"/posts/3")
	if code != 0 || !strings.Contains(out, "PUT "+ts.URL+"/posts/3") || !strings.Contains(out, `"metodo": "PUT"`) {
		t.Errorf("PUT: %d\n%s", code, out)
	}
	// DELETE não precisa de corpo
	code, out, _ = agentRun("-method", "DELETE", ts.URL+"/posts/9")
	if code != 0 || !strings.Contains(out, "DELETE ") || !strings.Contains(out, `"apagado": 9`) {
		t.Errorf("DELETE: %d\n%s", code, out)
	}
	// o manifesto mostra o método de cada ação
	_, out, _ = agentRun(ts.URL)
	if !strings.Contains(out, "PUT /posts/{id}") || !strings.Contains(out, "DELETE /posts/{id}") {
		t.Errorf("manifesto:\n%s", out)
	}
}
