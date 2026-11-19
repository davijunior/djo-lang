//go:build !nonet

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// specStatuses roda os specs do projeto e devolve nome -> "status: mensagem".
func specStatuses(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	_, out, errw := runSpec(t, "--json", specProject(t, files))
	var doc struct {
		Specs []struct{ Nome, Status, Mensagem string }
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("saída inesperada: %v\n%s\n%s", err, out, errw)
	}
	m := map[string]string{}
	for _, s := range doc.Specs {
		m[s.Nome] = s.Status + ": " + s.Mensagem
	}
	return m
}

func wantPassed(t *testing.T, got map[string]string, names ...string) {
	t.Helper()
	if len(got) != len(names) {
		t.Errorf("esperava %d specs, vieram %d: %v", len(names), len(got), got)
	}
	for _, n := range names {
		if s, ok := got[n]; !ok || !strings.HasPrefix(s, "passou") {
			t.Errorf("%s: %q", n, s)
		}
	}
}

const specHTTPCore = `f chama
    params[url[String]]
    return Http.get:url
`

func TestSpecHTTPMockBasico(t *testing.T) {
	start := time.Now()
	got := specStatuses(t, map[string]string{"core/h.djo": specHTTPCore, "spec/h_spec.djo": `f it_responde
    Spec.http_mock:"GET":"https://api.x.com/a":{"status": 201, "body": "oi", "headers": {"X-A": "1"}}
    var r = Http.get:"https://api.x.com/a"
    expect.eq:r["status"]:201
    expect.be_true:r["ok"]
    expect.eq:r["body"]:"oi"
    expect.eq:r["headers"]["x-a"]:"1"

f it_json_e_metodo_minusculo
    Spec.http_mock:"post":"https://api.x.com/p":{"json": {"n": 1}}
    var r = Http.post:"https://api.x.com/p":{"q": 2}
    expect.eq:r["status"]:200
    expect.eq:(Json.decode:r["body"])["n"]:1
    expect.include:r["headers"]["content-type"]:"json"

f it_sequencia_e_a_ultima_repete
    Spec.http_mock:"GET":"https://api.x.com/s*":[{"status": 500}, {"status": 200}]
    expect.eq:(Http.get:"https://api.x.com/s?a=1")["status"]:500
    expect.eq:(Http.get:"https://api.x.com/s?a=2")["status"]:200
    expect.eq:(Http.get:"https://api.x.com/sx")["status"]:200

f it_status_ruim_nao_e_ok
    Spec.http_mock:"GET":"https://api.x.com/e":{"status": 500, "body": "<faultstring>x</faultstring>"}
    var r = Http.get:"https://api.x.com/e"
    expect.be_false:r["ok"]
    expect.eq:r["status"]:500

f it_falhas_de_rede
    Spec.http_mock:"GET":"https://api.x.com/t":{"timeout": true}
    Spec.http_mock:"GET":"https://api.x.com/f":{"error": "connection refused"}
    var a = Http.get:"https://api.x.com/t":nil:3
    expect.be_false:a["ok"]
    expect.eq:a["status"]:0
    expect.eq:a["error"]:"tempo esgotado (3 s)"
    expect.eq:(Http.get:"https://api.x.com/f")["error"]:"connection refused"

f it_delay_virtual_estoura_o_timeout_sem_esperar
    Spec.http_mock:"GET":"https://api.x.com/l":{"delay_ms": 7000, "body": "x"}
    var a = Http.get:"https://api.x.com/l":nil:5
    expect.eq:a["error"]:"tempo esgotado (5 s)"
    var b = Http.get:"https://api.x.com/l"
    expect.eq:b["body"]:"x"

f it_sem_mock_correspondente_e_erro_e_sem_segredo
    Spec.http_mock:"GET":"https://api.x.com/a":{"status": 200}
    var r = Spec.try:"chama":["https://u:senha99@outra.com/x?token=segredo123&q=1"]
    expect.be_false:r["ok"]
    expect.include:r["error"]:"sem mock para GET"
    expect.include:r["error"]:"token=***"
    expect.include:r["error"]:"q=1"
    expect.be_false:(contains:r["error"]:"segredo123")

f it_metodo_diferente_nao_casa
    Spec.http_mock:"GET":"https://api.x.com/a":{"status": 200}
    var r = Spec.try:"chama":["https://api.x.com/a"]
    expect.be_true:r["ok"]
    expect.raises:"chama":["https://api.x.com/b"]:"sem mock"

f it_clear_volta_ao_normal_e_zera_chamadas
    Spec.http_mock:"GET":"https://api.x.com/a":{"status": 200}
    Http.get:"https://api.x.com/a"
    var antes = Spec.http_calls:
    expect.eq:(len:antes):1
    Spec.http_mock_clear:
    var depois = Spec.http_calls:
    expect.eq:(len:depois):0
`})
	_ = start
	for n, s := range got {
		if !strings.HasPrefix(s, "passou") {
			t.Errorf("%s: %s", n, s)
		}
	}
	if len(got) != 9 {
		t.Errorf("esperava 9 specs: %v", got)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("o delay virtual não pode esperar de verdade (%s)", d)
	}
}

func TestSpecHTTPMockCorpoCondicoes(t *testing.T) {
	got := specStatuses(t, map[string]string{"spec/h_spec.djo": `f it_casa_pelo_corpo
    var u = "https://app.omie.com.br/api/v1/x/"
    Spec.http_mock:"POST":u:{"json_has": {"call": "ListarContas"}}:{"json": {"n": 1}}
    Spec.http_mock:"POST":u:{"body_contains": "Consultar"}:{"json": {"n": 2}}
    Spec.http_mock:"POST":u:{"status": 418}
    var a = Http.post:u:{"call": "ListarContas", "app_key": "k", "param": [{"p": 1}]}
    var b = Http.post:u:{"call": "ConsultarConta"}
    var c = Http.post:u:{"call": "Outra"}
    expect.eq:(Json.decode:a["body"])["n"]:1
    expect.eq:(Json.decode:b["body"])["n"]:2
    expect.eq:c["status"]:418

f it_json_has_nao_casa_com_corpo_texto
    var u = "https://api.x.com/y"
    Spec.http_mock:"POST":u:{"json_has": {"a": 1}}:{"status": 200}
    Spec.http_mock:"POST":u:{"status": 400}
    expect.eq:(Http.post:u:"não é json")["status"]:400
    expect.eq:(Http.post:u:{"a": 1, "b": 2})["status"]:200
    expect.eq:(Http.post:u:{"a": 2})["status"]:400
`})
	for _, n := range []string{"casa pelo corpo", "json has nao casa com corpo texto"} {
		if s := got[n]; !strings.HasPrefix(s, "passou") {
			t.Errorf("%s: %q", n, s)
		}
	}
}

func TestSpecHTTPMockValidacao(t *testing.T) {
	bad := map[string]string{
		`Spec.http_mock:"PATCH":"https://a.com/":{"status": 200}`:                     "o método é GET",
		`Spec.http_mock:"GET":"ftp://a.com/":{"status": 200}`:                         "http:// ou https://",
		`Spec.http_mock:"GET":"https://a.com/":{"status": 99}`:                        "status vai de 100 a 599",
		`Spec.http_mock:"GET":"https://a.com/":{"body": "a", "json": 1}`:              "body OU json",
		`Spec.http_mock:"GET":"https://a.com/":{"timeout": true, "status": 200}`:      "falhas de rede",
		`Spec.http_mock:"GET":"https://a.com/":{"foo": 1}`:                            "o campo foo não existe",
		`Spec.http_mock:"GET":"https://a.com/":[]`:                                    "vai de 1 a 50",
		`Spec.http_mock:"GET":"https://a.com/":{"delay_ms": -1}`:                      "delay_ms vai de 0",
		`Spec.http_mock:"GET":"https://a.com/":{"body_contains": ""}:{"status": 200}`: "body_contains é um texto",
	}
	files := map[string]string{}
	var names []string
	i := 0
	for src := range bad {
		n := "it_ruim_" + string(rune('a'+i))
		i++
		files["spec/h"+n+"_spec.djo"] = "f " + n + "\n    " + src + "\n"
		names = append(names, n)
	}
	got := specStatuses(t, files)
	for src, want := range bad {
		found := false
		for _, s := range got {
			if strings.Contains(s, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: faltou %q em %v", src, want, got)
		}
	}
}

// Com o mock ligado nenhuma chamada sai pela rede: o servidor real não recebe nada.
func TestSpecHTTPMockNaoUsaARede(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("rede"))
	}))
	defer srv.Close()
	got := specStatuses(t, map[string]string{"core/h.djo": specHTTPCore, "spec/h_spec.djo": `f it_mockado_nao_chama_a_rede
    Spec.http_mock:"GET":"` + srv.URL + `/ok":{"body": "mock"}
    expect.eq:(Http.get:"` + srv.URL + `/ok")["body"]:"mock"
    expect.raises:"chama":["` + srv.URL + `/outra"]:"sem mock"
    expect.raises:"chama":["` + srv.URL + `/ok?x=1"]:"sem mock"
`})
	for n, s := range got {
		if !strings.HasPrefix(s, "passou") {
			t.Errorf("%s: %s", n, s)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("o servidor recebeu %d chamadas com o mock ligado", hits.Load())
	}
}

func TestSpecHTTPCallsMascaraERaw(t *testing.T) {
	got := specStatuses(t, map[string]string{"spec/h_spec.djo": `f it_mascara_e_raw
    Spec.http_mock:"POST":"https://u:pw@api.x.com/z*":{"status": 200}
    Http.post:"https://u:pw@api.x.com/z?token=abc&n=1":{"app_secret": "s"}:{"Authorization": "Bearer tk", "Cookie": "a=b", "X-Api-Key": "kk", "X-Outro": "ok"}
    var cs = Spec.http_calls:
    var c = cs[0]
    expect.eq:c["method"]:"POST"
    expect.eq:c["headers"]["authorization"]:"***"
    expect.eq:c["headers"]["cookie"]:"***"
    expect.eq:c["headers"]["x-api-key"]:"***"
    expect.eq:c["headers"]["x-outro"]:"ok"
    expect.include:c["url"]:"token=***"
    expect.include:c["url"]:"n=1"
    expect.be_false:(contains:c["url"]:"abc")
    expect.be_false:(contains:c["url"]:"pw@")
    expect.include:c["body"]:"app_secret"
    var rs = Spec.http_calls_raw:
    var r = rs[0]
    expect.eq:r["headers"]["authorization"]:"Bearer tk"
    expect.include:r["url"]:"token=abc"
`})
	for n, s := range got {
		if !strings.HasPrefix(s, "passou") {
			t.Errorf("%s: %s", n, s)
		}
	}
	if len(got) != 1 {
		t.Errorf("%v", got)
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://a.com/x":                          "https://a.com/x",
		"https://u:pw@a.com/x":                     "https://u:%2A%2A%2A@a.com/x",
		"https://a.com/x?token=abc&q=1":            "https://a.com/x?token=***&q=1",
		"https://a.com/x?api_key=1&access_token=2": "https://a.com/x?api_key=***&access_token=***",
		"https://a.com/x?senha=1":                  "https://a.com/x?senha=***",
	} {
		got := redactURL(in)
		got = strings.ReplaceAll(got, "%2A%2A%2A", "***")
		want = strings.ReplaceAll(want, "%2A%2A%2A", "***")
		if got != want {
			t.Errorf("%s: %s (esperado %s)", in, got, want)
		}
	}
}
