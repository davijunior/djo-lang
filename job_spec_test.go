//go:build !nodb && !noweb

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const jobSpecCore = `f gravar
    params[a[Map]]
    Db.open:"s.db"
    Db.exec:"CREATE TABLE IF NOT EXISTS s(t TEXT)"
    Db.exec:"INSERT INTO s(t) VALUES (?)":[a["texto"]]

f lido
    Db.open:"s.db"
    Db.exec:"CREATE TABLE IF NOT EXISTS s(t TEXT)"
    return Db.query:"SELECT t FROM s ORDER BY rowid"

f quebra
    var x = 1 / 0

f pai
    Job.run:"gravar":{"texto": "filho"}

f cadastrar
    params[nome[String]]
    Job.run:"gravar":{"texto": nome}
`

func TestJobNoSpecCapturaENuncaRodaSozinho(t *testing.T) {
	root := specProject(t, map[string]string{
		"core/j.djo": jobSpecCore,
		"index.djo":  "Job.run:\"gravar\":{\"texto\": \"da pagina\"}\nprint \"ok\"",
		"agenda.djo": "Schedule.every:\"10m\":\"gravar\":{\"args\": {\"texto\": \"agendado\"}}\nSchedule.cron:\"20 3 * * *\":\"quebra\":{\"tries\": 2, \"catch_up\": false}\n",
		"spec/j_spec.djo": `f it_captura
    expect.eq:(Spec.jobs:):[]
    var id = Job.run:"gravar":{"texto": "oi"}:{"in": "5m", "key": "k", "tries": 4}
    var js = Spec.jobs:
    expect.len:js:1
    expect.eq:js[0]["id"]:id
    expect.eq:js[0]["fn"]:"gravar"
    expect.eq:js[0]["args"]:{"texto": "oi"}
    expect.eq:js[0]["state"]:"queued"
    expect.eq:js[0]["max_tries"]:4
    expect.eq:js[0]["in"]:300
    expect.eq:js[0]["key"]:"k"
    expect.eq:(Job.run:"gravar":{"texto": "outro"}:{"key": "k"}):id
    expect.len:(Spec.jobs:):1
    expect.eq:(Job.status:id)["state"]:"queued"
    expect.be_true:(Job.cancel:id)
    expect.eq:(Spec.jobs:)[0]["state"]:"canceled"

f it_nada_roda_sozinho
    Job.run:"gravar":{"texto": "x"}
    expect.eq:(lido:):[]
    expect.len:(Spec.jobs:):1

f it_run_jobs_executa_em_ordem_e_inclui_os_filhos
    Job.run:"gravar":{"texto": "um"}
    Job.run:"pai":{}
    var rs = Spec.run_jobs:
    expect.len:rs:3
    expect.be_true:rs[0]["ok"]
    expect.eq:rs[1]["fn"]:"pai"
    expect.eq:rs[2]["fn"]:"gravar"
    expect.eq:(lido:):[{"t": "um"}, {"t": "filho"}]
    expect.eq:(Spec.run_jobs:):[]

f it_falha_aparece_no_resultado
    Job.run:"quebra":{}
    var rs = Spec.run_jobs:
    expect.be_false:rs[0]["ok"]
    expect.include:rs[0]["error"]:"zero"
    expect.eq:(Spec.jobs:)[0]["state"]:"dead"

f it_run_job_so_um
    var a = Job.run:"gravar":{"texto": "a"}
    var b = Job.run:"gravar":{"texto": "b"}
    var rs = Spec.run_job:b
    expect.len:rs:1
    expect.eq:(lido:):[{"t": "b"}]
    expect.eq:(Job.status:a)["state"]:"queued"

f it_pagina_enfileira_e_rodar_depois
    var r = Spec.get:"/"
    expect.status:r:200
    expect.len:(Spec.jobs:):1
    Spec.run_jobs:
    expect.eq:(lido:):[{"t": "da pagina"}]

f it_cada_spec_comeca_limpo
    expect.eq:(Spec.jobs:):[]
    expect.eq:(lido:):[]

f it_agenda
    var ag = Spec.agenda:
    expect.len:ag:2
    expect.eq:ag[0]["kind"]:"every"
    expect.eq:ag[0]["fn"]:"gravar"
    expect.eq:ag[0]["args"]:{"texto": "agendado"}
    expect.eq:ag[1]["expr"]:"20 3 * * *"
    expect.eq:ag[1]["tries"]:2
    expect.be_false:ag[1]["catch_up"]
`,
	})
	code, out, errw := runSpec(t, root)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errw)
	}
	// nada foi enfileirado de verdade: nem a fila nem o arquivo de saída existem na pasta do projeto
	if _, err := os.Stat(filepath.Join(root, "_dados")); err == nil {
		t.Error("o spec não pode criar _dados/ no projeto")
	}
}

func TestJobSpecAgendaInvalidaImpedeORunner(t *testing.T) {
	root := specProject(t, map[string]string{
		"core/j.djo":      jobSpecCore,
		"agenda.djo":      "Schedule.every:\"10m\":\"nao_existe\"\n",
		"spec/j_spec.djo": "f it_x\n    expect.eq:1:1\n",
	})
	code, _, errw := runSpec(t, root)
	if code != 2 || !strings.Contains(errw, "agenda.djo") || !strings.Contains(errw, "nao_existe") {
		t.Errorf("%d %q", code, errw)
	}
}

func TestJobGiveUpNoSpec(t *testing.T) {
	root := specProject(t, map[string]string{
		"core/j.djo": "f desiste\n    Job.give_up:\"motivo\"\n",
		"spec/j_spec.djo": `f it_give_up
    Job.run:"desiste":{}
    var rs = Spec.run_jobs:
    expect.be_false:rs[0]["ok"]
    expect.eq:rs[0]["error"]:"motivo"
`,
	})
	if code, out, errw := runSpec(t, root); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errw)
	}
}

func TestJobScriptForaDoServidorDaErroClaro(t *testing.T) {
	_, err := runIn(t, t.TempDir(), "f gravar\n    print 1\nJob.run:\"gravar\":{}", false)
	if err == nil || !strings.Contains(err.Error(), "Job só existe dentro de djo serve") {
		t.Errorf("%v", err)
	}
	if _, err := runIn(t, t.TempDir(), "var x = Job.status:1", false); err == nil {
		t.Error("Job.status também")
	}
}

func TestJobPermissaoDeBooster(t *testing.T) {
	if !restrictedNS["Job"] {
		t.Fatal("Job precisa ser uma permissão de booster")
	}
}

// e2e: workers de verdade, sem relógio falso: a página enfileira e o job roda sozinho.
func TestJobE2EWorkersDeVerdade(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `Job.run:"gravar":{"texto": "e2e"}`})
	f.srv.jobs.workers = 1
	if code, _ := f.get(t, "/"); code != 200 {
		t.Fatal(code)
	}
	waitFor(t, func() bool { return f.saida() == "e2e\n" }, "o worker rodar o job")
	waitFor(t, func() bool { return f.engine(t).q.counts()["done"] == 1 }, "o job virar done")
	f.srv.jobsShutdown(2 * time.Second)
}
