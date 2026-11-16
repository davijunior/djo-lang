//go:build !noweb && !nodb

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const coreDatas = `struct Evento
    id[Int] nocall
    nome[String]
    dia[Date]
    inicio[DateTime]

f abrir
    Db.open:"datas.db"
    Db.exec:"CREATE TABLE IF NOT EXISTS eventos (id INTEGER PRIMARY KEY, nome TEXT, dia TEXT, inicio TEXT)"

action agendar
    "Agenda um evento"
    params[nome[String], dia[Date], inicio[DateTime]]
    abrir:
    Db.exec:"INSERT INTO eventos (nome, dia, inicio) VALUES (?, ?, ?)":[nome, dia, inicio]
    return Evento:nome:dia:inicio

action do_dia
    "Eventos de um dia"
    params[dia[Date]]
    abrir:
    var r = Db.query:"SELECT nome, dia, inicio FROM eventos WHERE dia = ? ORDER BY inicio":[dia]
    var out = []
    for{ each[l in r]
        push:out:(Evento:(l["nome"]):(Date.parse:(l["dia"])):(DateTime.parse:(l["inicio"])))
    }
    return {"dia": dia, "eventos": out}

action entre
    "Eventos entre duas datas, o último dia incluído"
    params[de[Date], ate[Date]]
    abrir:
    var r = Db.query:"SELECT nome FROM eventos WHERE inicio >= ? AND inicio <= ? ORDER BY inicio":[DateTime.of:de, DateTime.end_of:ate]
    var nomes = []
    for{ each[l in r]
        push:nomes:(l["nome"])
    }
    return nomes
`

func TestDatasActions(t *testing.T) {
	ts := site(t, map[string]string{
		"core/c.djo": coreDatas,
		"rotas.djo": `Route.action:"/eventos":"agendar":"Evento"
Route.action:"/dia":"do_dia"
Route.action:"/dia/{dia}":"do_dia"
Route.action:"/entre":"entre"
Route.get:"/eventos":{"agent": "p/lista.djo", "returns": ["Evento"]}
`,
		"p/lista.djo": `abrir:
var l = []
for{ each[r in Db.query:"SELECT * FROM eventos ORDER BY inicio"]
    push:l:(Evento:(r["nome"]):(Date.parse:(r["dia"])):(DateTime.parse:(r["inicio"])))
}
Http.json:l`,
	})
	post := func(p, body string) (int, string) {
		req, _ := http.NewRequest("POST", ts.URL+p, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b))
	}
	get := func(p string) (int, string) {
		req, _ := http.NewRequest("GET", ts.URL+p, nil)
		req.Header.Set("Accept", "application/json")
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b))
	}

	// grava e devolve o mesmo evento, com as datas em ISO
	c, b := post("/eventos", `{"nome":"Reunião","dia":"2025-03-30","inicio":"2025-03-30T14:00:00Z"}`)
	if c != 200 || b != `{"id":0,"nome":"Reunião","dia":"2025-03-30","inicio":"2025-03-30T14:00:00Z"}` {
		t.Fatalf("agendar = %d %s", c, b)
	}
	post("/eventos", `{"nome":"Almoço","dia":"2025-03-31","inicio":"2025-03-31T23:59:59-00:00"}`)
	post("/eventos", `{"nome":"Fora","dia":"2025-04-01","inicio":"2025-04-01T00:00:00Z"}`)
	// o deslocamento vira UTC
	post("/eventos", `{"nome":"Fuso","dia":"2025-03-30","inicio":"2025-03-30T09:00:00-03:00"}`)

	// o dia no SQLite é comparável como texto
	if c, b := post("/dia", `{"dia":"2025-03-30"}`); c != 200 || !strings.Contains(b, `"Fuso"`) || !strings.Contains(b, `"Reunião"`) || strings.Index(b, "Fuso") > strings.Index(b, "Reunião") {
		t.Errorf("do_dia = %d %s", c, b)
	}
	// a data também pode vir na URL
	if c, b := post("/dia/2025-03-30", ""); c != 200 || !strings.Contains(b, `"Reunião"`) {
		t.Errorf("data na URL = %d %s", c, b)
	}
	if c, b := post("/dia/ontem", ""); c != 422 || !strings.Contains(b, `"dia":"na URL deve ser Date"`) {
		t.Errorf("data inválida na URL = %d %s", c, b)
	}
	// "até" inclusivo: o último dia inteiro entra (23:59:59), o seguinte não
	if c, b := post("/entre", `{"de":"2025-03-30","ate":"2025-03-31"}`); c != 200 || b != `["Fuso","Reunião","Almoço"]` {
		t.Errorf("entre = %d %s", c, b)
	}

	// entradas inválidas: 422 com o campo certo
	for body, want := range map[string]string{
		`{"nome":"x","dia":"2025-02-31","inicio":"2025-03-30T14:00:00Z"}`: `"dia":"não é uma data válida; use AAAA-MM-DD`,
		`{"nome":"x","dia":"30/03/2025","inicio":"2025-03-30T14:00:00Z"}`: `"dia":"não é uma data válida`,
		`{"nome":"x","dia":20250330,"inicio":"2025-03-30T14:00:00Z"}`:     `"dia":"deve ser Date (texto AAAA-MM-DD), mas veio Int"`,
		`{"nome":"x","dia":"2025-03-30","inicio":"ontem"}`:                `"inicio":"não é uma data e hora válida`,
		`{"nome":"x","dia":"2025-03-30","inicio":"2025-03-30"}`:           `"inicio":"não é uma data e hora válida`,
	} {
		if c, b := post("/eventos", body); c != 422 || !strings.Contains(b, want) {
			t.Errorf("%s = %d %s, queria 422 com %s", body, c, b, want)
		}
	}

	// o manifesto descreve as datas como string com format
	_, m := get("/.well-known/ai-manifest.json")
	var man struct {
		Actions []struct {
			Name  string
			Input struct{ Properties map[string]map[string]any }
		}
	}
	if err := json.Unmarshal([]byte(m), &man); err != nil {
		t.Fatal(err)
	}
	for _, a := range man.Actions {
		if a.Name != "agendar" {
			continue
		}
		if p := a.Input.Properties["dia"]; p["type"] != "string" || p["format"] != "date" {
			t.Errorf("schema de dia = %v", p)
		}
		if p := a.Input.Properties["inicio"]; p["type"] != "string" || p["format"] != "date-time" {
			t.Errorf("schema de inicio = %v", p)
		}
	}
	if !strings.Contains(m, `"format": "date-time"`) {
		t.Errorf("returns Evento sem format date-time:\n%s", m)
	}

	// a leitura (rota "returns") devolve as datas em ISO
	_, l := get("/eventos")
	if !strings.Contains(l, `"dia":"2025-03-30"`) || !strings.Contains(l, `"inicio":"2025-03-30T12:00:00Z"`) {
		t.Errorf("lista = %s", l)
	}
}
