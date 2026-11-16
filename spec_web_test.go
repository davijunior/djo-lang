//go:build !noweb && !nodb

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const specSite = `Route.get:"/tarefas":{"agent": "agentes/lista.djo", "about": "lista"}
Route.action:"/tarefas":"criar"
`

const specSiteCore = `f abrir
    Db.open:"app.db"
    Db.exec:"CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY, titulo TEXT NOT NULL)"

action criar
    "cria"
    params[titulo[String]]
    abrir:
    if{ cond[len:titulo == 0]
        Http.status:400
        return {"erro": "vazio"}
    }
    Db.exec:"INSERT INTO t (titulo) VALUES (?)":[titulo]
    Http.status:201
    return Db.one:"SELECT id, titulo FROM t ORDER BY id DESC LIMIT 1"
`

const specSiteLista = `abrir:
Http.json:(Db.query:"SELECT id, titulo FROM t ORDER BY id")
`

func specSiteFiles(spec string) map[string]string {
	return map[string]string{"rotas.djo": specSite, "core/app.djo": specSiteCore, "agentes/lista.djo": specSiteLista, "spec/t_spec.djo": spec}
}

func TestSpecRotaEmProcessoEBancoIsolado(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	root := specProject(t, specSiteFiles(`f it_cria_e_lista
    var r = Spec.post:"/tarefas":{"titulo": "estudar"}
    expect.status:r:201
    expect.json:r:"titulo":"estudar"
    var l = Spec.get:"/tarefas"
    expect.status:l:200
    expect.len:(Json.decode:l["body"]):1
    expect.include:l["headers"]["Content-Type"]:"json"

f it_vazio_da_400
    var r = Spec.post:"/tarefas":{"titulo": ""}
    expect.status:r:400
    expect.json:r:"erro":"vazio"

f it_o_banco_comeca_vazio_em_cada_spec
    var l = Spec.get:"/tarefas"
    expect.len:(Json.decode:l["body"]):0
    Spec.post:"/tarefas":{"titulo": "x"}

f it_o_spec_tambem_enxerga_o_mesmo_banco
    Db.open:"app.db"
    Db.exec:"CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY, titulo TEXT NOT NULL)"
    Db.exec:"INSERT INTO t (titulo) VALUES ('de dentro')"
    var l = Spec.get:"/tarefas"
    expect.json:l:"0.titulo":"de dentro"

f it_rota_que_nao_existe
    var r = Spec.get:"/nao-existe"
    expect.status:r:404
`))
	code, out, _ := runSpec(t, root)
	if code != 0 || !strings.Contains(out, "5 specs, 0 falhas, 0 erros") {
		t.Fatalf("%d\n%s", code, out)
	}
	// nada de banco no projeto, e a pasta temporária de cada spec foi apagada
	var dbs []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".db") {
			dbs = append(dbs, p)
		}
		return nil
	})
	if len(dbs) > 0 {
		t.Errorf("o projeto ficou com banco: %v", dbs)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("sobrou lixo na pasta temporária: %v", left)
	}
}

func TestSpecCaminhoDeRotaSoLocal(t *testing.T) {
	root := specProject(t, specSiteFiles(`f it_url_inteira
    Spec.get:"http://example.com/"

f it_dois_barras
    Spec.get:"//example.com/"

f it_sem_barra
    Spec.get:"tarefas"
`))
	_, out, _ := runSpec(t, "--json", root)
	var doc struct {
		Specs []struct{ Status, Mensagem string }
	}
	json.Unmarshal([]byte(out), &doc)
	for i, s := range doc.Specs {
		if s.Status != "erro" || !strings.Contains(s.Mensagem, "caminho deve ser local") {
			t.Errorf("%d: %+v", i, s)
		}
	}
}

func TestSpecCabecalhoEMetodos(t *testing.T) {
	root := specProject(t, map[string]string{
		"rotas.djo":        "Route.action:\"/eco\":\"eco\"\nRoute.get:\"/quem\":{\"agent\": \"agentes/quem.djo\"}\n",
		"core/a.djo":       "action eco\n    \"eco\"\n    params[n[Int]]\n    return {\"n\": n + 1}\n",
		"agentes/quem.djo": "Http.json:{\"auth\": Http.header:\"Authorization\"}\n",
		"spec/h_spec.djo": `f it_cabecalho
    var r = Spec.get:"/quem":{"Authorization": "Bearer abc"}
    expect.json:r:"auth":"Bearer abc"

f it_post_json
    var r = Spec.post:"/eco":{"n": 41}
    expect.json:r:"n":42
`})
	code, out, _ := runSpec(t, root)
	if code != 0 {
		t.Fatalf("%d\n%s", code, out)
	}
}
