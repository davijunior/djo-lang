//go:build !nonet && !noweb && !nodb

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// O mock vale na página servida (Spec.get), no job (Spec.run_jobs) e respeita a cota de espera da página.
func TestSpecHTTPMockEmPaginaEJob(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	got := specStatuses(t, map[string]string{
		"rotas.djo": "Route.get:\"/p\":{\"agent\": \"agentes/p.djo\", \"about\": \"p\"}\nRoute.get:\"/q\":{\"agent\": \"agentes/q.djo\", \"about\": \"q\"}\n",
		"agentes/p.djo": `var r = Http.get:"https://api.x.com/a"
Http.json:{"s": r["status"], "b": r["body"]}
`,
		// 4 chamadas de 7 s simulados: a cota de 25 s da página corta a 4ª em 4 s
		"agentes/q.djo": `var out = []
var i = 0
while{
    cond[i < 4]
    var r = Http.get:"https://api.x.com/lenta"
    push:out:(r["error"])
    i++
}
Http.json:out
`,
		"core/j.djo": `f busca
    var r = Http.get:"` + srv.URL + `/x"
    if
        cond[r["body"] != "mock"]
        reject:"corpo_errado":(Text.from:r["status"])
`,
		"spec/w_spec.djo": `f it_pagina_usa_o_mock
    Spec.http_mock:"GET":"https://api.x.com/a":{"status": 202, "body": "da pagina"}
    var r = Spec.get:"/p"
    expect.json:r:"s":202
    expect.json:r:"b":"da pagina"
    var cs = Spec.http_calls:
    expect.eq:(len:cs):1

f it_pagina_sem_mock_correspondente_da_500
    Spec.http_mock:"GET":"https://api.x.com/outra":{"status": 200}
    var r = Spec.get:"/p"
    expect.status:r:500

f it_cota_de_espera_da_pagina_vale_com_delay_virtual
    Spec.http_mock:"GET":"https://api.x.com/lenta":{"delay_ms": 7000, "body": "x"}
    var r = Spec.get:"/q"
    var l = Json.decode:r["body"]
    expect.eq:(len:l):4
    expect.eq:l[0]:nil
    expect.eq:l[2]:nil
    expect.eq:l[3]:"tempo esgotado (4 s)"

f it_job_usa_o_mock
    Spec.http_mock:"GET":"` + srv.URL + `/x":{"body": "mock"}
    Job.run:"busca":{}
    var rs = Spec.run_jobs:
    expect.be_true:rs[0]["ok"]
    Spec.http_mock:"GET":"` + srv.URL + `/x":{"status": 503, "body": "fora"}
`,
	})
	for n, s := range got {
		if !strings.HasPrefix(s, "passou") {
			t.Errorf("%s: %s", n, s)
		}
	}
	if len(got) != 4 {
		t.Errorf("esperava 4 specs: %v", got)
	}
	if hits.Load() != 0 {
		t.Fatalf("o servidor real recebeu %d chamadas", hits.Load())
	}
}
