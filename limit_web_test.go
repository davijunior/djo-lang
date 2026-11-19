//go:build !noweb

package main

import (
	"strings"
	"testing"
)

func TestLimitEnforceNaPagina(t *testing.T) {
	got := specStatuses(t, map[string]string{
		"rotas.djo":     "Route.get:\"/a\":{\"agent\": \"agentes/a.djo\", \"about\": \"a\"}\nRoute.get:\"/b\":{\"agent\": \"agentes/b.djo\", \"about\": \"b\"}\nRoute.get:\"/c\":{\"agent\": \"agentes/c.djo\", \"about\": \"c\"}\n",
		"agentes/a.djo": "Limit.enforce:\"tok:1\":2:60\nHttp.json:{\"ok\": true}\n",
		"agentes/b.djo": "Limit.enforce:\"tok:2\":1:60:{\"body\": {\"erro\": {\"codigo\": \"limite\", \"tentar_novamente_em_s\": 3}}, \"headers\": {\"X-Hub\": \"1\"}}\nHttp.json:{\"ok\": true}\n",
		"agentes/c.djo": "var r = Limit.take:\"tok:3\":1:60\nHttp.json:r\n",
		"spec/l_spec.djo": `f it_libera_e_depois_429
    var r1 = Spec.get:"/a"
    var r2 = Spec.get:"/a"
    var r3 = Spec.get:"/a"
    expect.status:r1:200
    expect.eq:r1["headers"]["Ratelimit-Remaining"]:"1"
    expect.status:r2:200
    expect.status:r3:429
    expect.eq:(r3["headers"]["Retry-After"] != nil):true
    expect.json:r3:"error":"muitas_requisicoes"
    expect.be_false:(contains:r3["body"]:"ok")

f it_corpo_e_cabecalho_customizados
    Spec.get:"/b"
    var r = Spec.get:"/b"
    expect.status:r:429
    expect.eq:r["headers"]["X-Hub"]:"1"
    var d = Json.decode:r["body"]
    expect.eq:d["erro"]["codigo"]:"limite"
    expect.eq:d["erro"]["tentar_novamente_em_s"]:3

f it_cada_teste_comeca_limpo
    var r = Spec.get:"/a"
    expect.status:r:200
    expect.eq:r["headers"]["Ratelimit-Remaining"]:"1"

f it_take_nao_para_a_pagina
    Spec.get:"/c"
    var r = Spec.get:"/c"
    expect.status:r:200
    var d = Json.decode:r["body"]
    expect.be_false:d["allowed"]
    expect.eq:d["reason"]:nil
`,
	})
	if len(got) != 4 {
		t.Fatalf("esperava 4 specs: %v", got)
	}
	for n, s := range got {
		if !strings.HasPrefix(s, "passou") {
			t.Errorf("%s: %s", n, s)
		}
	}
}
