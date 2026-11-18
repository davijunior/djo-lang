//go:build !nodb && !noweb

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const jobCore = `f gravar
    params[a[Map]]
    File.append:"_dados/saida.txt":(a["texto"] + "\n")

f sem_args
    File.append:"_dados/saida.txt":"sem args\n"

f quebra
    var x = 1 / 0

f desiste
    Job.give_up:"não adianta tentar de novo"

f dorme
    Time.sleep:3000

f laco
    while{
        cond[true]
        params[var i = 0]
        i++
    }

f le_requisicao
    var t = Http.bearer:
    File.append:"_dados/saida.txt":"leu\n"

f pai_ok
    Job.run:"gravar":{"texto": "do filho"}

f pai_erro
    Job.run:"gravar":{"texto": "do filho que não vale"}
    var x = 1 / 0

f manda_email
    Mail.send:{"to": "a@x.com", "subject": "do job", "text": "oi"}

f soma_status
    params[a[Map]]
    var s = Job.status:(a["id"])
    File.append:"_dados/saida.txt":(s["state"] + "\n")
`

type jobSiteFx struct {
	root string
	srv  *Server
	ts   *httptest.Server
}

func newJobSite(t *testing.T, dev bool, files map[string]string) *jobSiteFx {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{"core/j.djo": jobCore}
	for k, v := range files {
		all[k] = v
	}
	for name, content := range all {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := NewServer(root, dev)
	if err != nil {
		t.Fatal(err)
	}
	srv.jobs.workers = 0 // nada de workers: os testes executam os jobs à mão
	jobJitter = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() { jobJitter = func(d time.Duration) time.Duration { return d + time.Duration(int64(d)/10) } })
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &jobSiteFx{root: root, srv: srv, ts: ts}
}

func (f *jobSiteFx) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(f.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func (f *jobSiteFx) engine(t *testing.T) *jobEngine {
	t.Helper()
	be, err := f.srv.jobs.backend()
	if err != nil {
		t.Fatal(err)
	}
	return be.(*jobEngine)
}

// runNext pega o próximo job e o executa de verdade (como um worker), no instante now.
func (f *jobSiteFx) runNext(t *testing.T, now time.Time) *jobRow {
	t.Helper()
	e := f.engine(t)
	j, err := e.q.claim(now)
	if err != nil || j == nil {
		t.Fatalf("não havia job: %v %v", j, err)
	}
	e.now = func() time.Time { return now }
	e.runOne(j)
	return e.q.get(j.id)
}

func (f *jobSiteFx) saida() string {
	b, _ := os.ReadFile(filepath.Join(f.root, "_dados", "saida.txt"))
	return string(b)
}

func TestJobPaginaEnfileiraEOWorkerExecuta(t *testing.T) {
	f := newJobSite(t, false, map[string]string{
		"index.djo": "var id = Job.run:\"gravar\":{\"texto\": \"olá\"}\nvar s = Job.status:id\nprint id\nprint s[\"state\"]\nprint s[\"fn\"]\nprint s[\"max_tries\"]",
	})
	code, body := f.get(t, "/")
	if code != 200 || body != "1\nqueued\ngravar\n3" {
		t.Fatalf("%d %q", code, body)
	}
	if f.saida() != "" {
		t.Error("o job não roda na página")
	}
	j := f.runNext(t, time.Now())
	if j.state != "done" || f.saida() != "olá\n" {
		t.Fatalf("%+v saida=%q", j, f.saida())
	}
}

func TestJobFuncaoSemParametro(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": "Job.run:\"sem_args\":{}"})
	f.get(t, "/")
	f.runNext(t, time.Now())
	if f.saida() != "sem args\n" {
		t.Errorf("%q", f.saida())
	}
}

func TestJobPaginaEmErroNaoDeixaJobs(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": "Job.run:\"gravar\":{\"texto\": \"x\"}\nvar y = 1 / 0"})
	if code, _ := f.get(t, "/"); code != 500 {
		t.Fatalf("%d", code)
	}
	if n := f.engine(t).q.counts(); n["queued"]+n["held"] != 0 {
		t.Errorf("a página falhou, não devia sobrar job: %v", n)
	}
	// e uma página boa depois funciona
	os.WriteFile(filepath.Join(f.root, "ok.djo"), []byte("Job.run:\"gravar\":{\"texto\": \"y\"}"), 0o644)
	f.get(t, "/ok")
	if n := f.engine(t).q.counts(); n["queued"] != 1 {
		t.Errorf("%v", n)
	}
}

func TestJobOpcoesDelayKeyETries(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `var a = Job.run:"gravar":{"texto": "1"}:{"in": "10m", "key": "k", "tries": 5, "timeout": "2m"}
var b = Job.run:"gravar":{"texto": "2"}:{"in": "10m", "key": "k"}
print a == b
var s = Job.status:a
print s["max_tries"]`})
	if _, body := f.get(t, "/"); body != "true\n5" {
		t.Fatalf("%q", body)
	}
	e := f.engine(t)
	if j, _ := e.q.claim(time.Now()); j != nil {
		t.Error("o atraso de 10m ainda não venceu")
	}
	j, _ := e.q.claim(time.Now().Add(11 * time.Minute))
	if j == nil || j.timeoutS != 120 || j.maxTries != 5 {
		t.Errorf("%+v", j)
	}
}

func TestJobCancelarEStatusDePagina(t *testing.T) {
	f := newJobSite(t, false, map[string]string{
		"index.djo":   `var id = Job.run:"gravar":{"texto": "z"}:{"in": "1h"}`,
		"cancela.djo": `print Job.cancel:1` + "\n" + `print Job.cancel:1` + "\n" + `print (Job.status:1)["state"]` + "\n" + `print Job.status:999`,
	})
	f.get(t, "/")
	if _, body := f.get(t, "/cancela"); body != "true\nfalse\ncanceled\nnil" {
		t.Errorf("%q", body)
	}
}

func TestJobErrosDeUsoNaPagina(t *testing.T) {
	cases := map[string]string{
		"função inexistente": `Job.run:"nao_existe":{}`,
		"action":             `Job.run:"fazer":{}`,
		"args não é mapa":    `Job.run:"gravar":5`,
		"args com struct":    "struct P\n    n[Int]\nvar p = P{n: 1}\nJob.run:\"gravar\":{\"p\": p}",
		"opção desconhecida": `Job.run:"gravar":{"texto": "x"}:{"vezes": 2}`,
		"at longe demais":    `Job.run:"gravar":{"texto": "x"}:{"at": "2999-01-01T00:00:00Z"}`,
		"in e at juntos":     `Job.run:"gravar":{"texto": "x"}:{"in": "1m", "at": "2030-01-01T00:00:00Z"}`,
		"in ruim":            `Job.run:"gravar":{"texto": "x"}:{"in": "logo"}`,
		"tries zero":         `Job.run:"gravar":{"texto": "x"}:{"tries": 0}`,
		"timeout enorme":     `Job.run:"gravar":{"texto": "x"}:{"timeout": "1h"}`,
		"key com quebra":     "Job.run:\"gravar\":{\"texto\": \"x\"}:{\"key\": \"a\\nb\"}",
		"sem argumentos":     `Job.run:"gravar"`,
		"status não é id":    `Job.status:"x"`,
		"give_up fora":       `Job.give_up:"x"`,
	}
	files := map[string]string{"core/a.djo": "action fazer\n    return 1\n"}
	n := 0
	for name, src := range cases {
		n++
		files[strings.ReplaceAll("p"+string(rune('a'+n))+".djo", " ", "")] = src
		_ = name
	}
	f := newJobSite(t, false, files)
	n = 0
	for name := range cases {
		n++
		if code, body := f.get(t, "/p"+string(rune('a'+n))); code != 500 {
			t.Errorf("%s: esperava erro, veio %d %q", name, code, body)
		}
	}
	if c := f.engine(t).q.counts(); len(c) != 0 {
		t.Errorf("nenhum job devia existir: %v", c)
	}
}

func TestJobArgumentosGrandesDemais(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `var s = "x"
until{
    eng[i = 0 ; i == 14 ; i++]
    s = s + s
}
Job.run:"gravar":{"texto": s}`})
	if code, _ := f.get(t, "/"); code != 500 {
		t.Errorf("args de 16 KB+ deviam ser recusados: %d", code)
	}
}

func TestJobFalhaDeVerdadeRegistraErroELinha(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `Job.run:"quebra":{}:{"tries": 2}`})
	f.get(t, "/")
	j := f.runNext(t, time.Now())
	if j.state != "queued" || j.tries != 1 || !strings.Contains(j.err, "divisão por zero") && !strings.Contains(j.err, "zero") {
		t.Fatalf("%+v", j)
	}
	j = f.runNext(t, time.Now().Add(time.Minute))
	if j.state != "dead" || j.tries != 2 {
		t.Errorf("%+v", j)
	}
}

func TestJobGiveUpNaoRepete(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `Job.run:"desiste":{}:{"tries": 5}`})
	f.get(t, "/")
	j := f.runNext(t, time.Now())
	if j.state != "dead" || j.tries != 1 || j.err != "não adianta tentar de novo" {
		t.Errorf("%+v", j)
	}
}

func TestJobTimeoutCortaLaçoESleep(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `Job.run:"laco":{}:{"tries": 1, "timeout": "1s"}
Job.run:"dorme":{}:{"tries": 1, "timeout": "1s"}`})
	f.get(t, "/")
	t0 := time.Now()
	a := f.runNext(t, time.Now())
	b := f.runNext(t, time.Now())
	if time.Since(t0) > 20*time.Second {
		t.Errorf("demorou %v", time.Since(t0))
	}
	if a.state != "dead" || !strings.Contains(a.err, "tempo limite") {
		t.Errorf("laço: %+v", a)
	}
	if b.state != "dead" || !strings.Contains(b.err, "Time.sleep") {
		t.Errorf("sleep: %+v", b)
	}
}

func TestJobNaoTemRequisicao(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `Job.run:"le_requisicao":{}:{"tries": 1}`})
	f.get(t, "/")
	j := f.runNext(t, time.Now())
	if j.state != "dead" || j.err == "" || f.saida() != "" {
		t.Errorf("Http.bearer num job devia falhar: %+v", j)
	}
}

func TestJobFilhoFicaRetidoAteOPaiTerminar(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `Job.run:"pai_ok":{}
Job.run:"pai_erro":{}:{"tries": 1}`})
	f.get(t, "/")
	e := f.engine(t)
	a := f.runNext(t, time.Now()) // pai_ok: termina bem, o filho vira queued
	if a.state != "done" {
		t.Fatalf("%+v", a)
	}
	if c := e.q.counts(); c["queued"] != 2 { // pai_erro + filho de pai_ok
		t.Fatalf("depois do pai_ok: %v", c)
	}
	rows, _ := e.q.list("queued", 10)
	fns := []string{}
	for _, r := range rows {
		fns = append(fns, r.fn)
	}
	// agora o pai_erro: cria um filho e falha; o filho deve ser descartado
	b := f.runNext(t, time.Now()) // o mais antigo: pai_erro (id 2) ou o filho (id 3)? claim ordena por run_at,id
	if b.fn != "pai_erro" || b.state != "dead" {
		t.Fatalf("%+v (fila: %v)", b, fns)
	}
	if c := e.q.counts(); c["held"] != 0 || c["queued"] != 1 {
		t.Errorf("o filho do pai que falhou deve sumir: %v", c)
	}
	f.runNext(t, time.Now()) // o filho de pai_ok
	if !strings.Contains(f.saida(), "do filho\n") || strings.Contains(f.saida(), "não vale") {
		t.Errorf("saida %q", f.saida())
	}
}

func TestJobEmDevOMailGravaEml(t *testing.T) {
	if !hasMail {
		t.Skip("sem Mail neste perfil (nonet)")
	}
	t.Setenv("DJO_SMTP_HOST", "")
	t.Setenv("DJO_SMTP_FROM", "")
	f := newJobSite(t, true, map[string]string{"index.djo": `Job.run:"manda_email":{}`})
	f.get(t, "/")
	j := f.runNext(t, time.Now())
	if j.state != "done" {
		t.Fatalf("%+v", j)
	}
	files, _ := filepath.Glob(filepath.Join(f.root, "_saida", "emails", "*.eml"))
	if len(files) != 1 {
		t.Errorf("em -dev o job deve gravar o .eml: %v", files)
	}
}

func TestJobStatusDentroDoJob(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `var a = Job.run:"gravar":{"texto": "x"}:{"in": "1h"}
Job.run:"soma_status":{"id": a}`})
	f.get(t, "/")
	f.runNext(t, time.Now())
	if f.saida() != "queued\n" {
		t.Errorf("%q", f.saida())
	}
}

func TestJobCustoZeroSemUso(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"index.djo": `print "oi"`})
	f.srv.jobsBoot(2)
	if code, body := f.get(t, "/"); code != 200 || body != "oi" {
		t.Fatalf("%d %q", code, body)
	}
	if _, err := os.Stat(filepath.Join(f.root, "_dados")); err == nil {
		t.Error("sem Job, nada de _dados/ nem jobs.db")
	}
	f.srv.jobs.mu.Lock()
	be := f.srv.jobs.be
	f.srv.jobs.mu.Unlock()
	if be != nil {
		t.Error("a fila não devia ter sido criada")
	}
}

func TestJobBootLigaAFilaSeHaAgendaOuHistorico(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"agenda.djo": "Schedule.every:\"10m\":\"sem_args\"\n"})
	f.srv.jobsBoot(0)
	if be, _ := f.srv.jobs.backend(); be == nil {
		t.Fatal("com agenda.djo a fila liga no boot")
	}
	if _, err := os.Stat(jobQueuePath(f.root)); err != nil {
		t.Errorf("faltou o jobs.db: %v", err)
	}
	// um agenda.djo com erro é acusado no boot (e não derruba o servidor)
	g := newJobSite(t, false, map[string]string{"agenda.djo": "Schedule.every:\"10m\":\"nao_existe\"\n"})
	g.srv.jobsBoot(0)
}

func TestJobAgendaNuncaEServida(t *testing.T) {
	f := newJobSite(t, false, map[string]string{"agenda.djo": "Schedule.every:\"10m\":\"sem_args\"\n"})
	for _, p := range []string{"/agenda.djo", "/AGENDA.DJO", "/_dados/jobs.db"} {
		if code, _ := f.get(t, p); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestJobRotaActionEnfileira(t *testing.T) {
	f := newJobSite(t, false, map[string]string{
		"core/a.djo": "action cadastrar\n    params[nome[String]]\n    Job.run:\"gravar\":{\"texto\": nome}\n    return {\"ok\": true}\n\naction cadastrar_falha\n    params[nome[String]]\n    Job.run:\"gravar\":{\"texto\": nome}\n    var x = 1 / 0\n",
		"rotas.djo":  "Route.action:\"/cadastrar\":\"cadastrar\"\nRoute.action:\"/falha\":\"cadastrar_falha\"\n",
	})
	post := func(p string) int {
		resp, err := http.Post(f.ts.URL+p, "application/json", strings.NewReader(`{"nome": "Ana"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post("/cadastrar"); c != 200 {
		t.Fatalf("%d", c)
	}
	if c := post("/falha"); c != 500 {
		t.Fatalf("%d", c)
	}
	if n := f.engine(t).q.counts(); n["queued"] != 1 || n["held"] != 0 {
		t.Errorf("só o da action que deu certo fica: %v", n)
	}
}

func TestPromisePaginaJobDeDentroQueFalhaEDescartado(t *testing.T) {
	f := newJobSite(t, false, map[string]string{
		"index.djo": `Job.run:"gravar":{"texto": "antes"}
uniq promise{
    Job.run:"gravar":{"texto": "dentro"}
    var y = 1 / 0
}error{
    [e]
    print "tratado: " + e["msg"]
}
`,
	})
	code, body := f.get(t, "/")
	if code != 200 || body != "tratado: divisão por zero" {
		t.Fatalf("%d %q", code, body)
	}
	// o de antes fica; o de dentro do promise que falhou foi descartado
	if n := f.engine(t).q.counts(); n["queued"] != 1 || n["held"] != 0 {
		t.Fatalf("%v", n)
	}
	f.runNext(t, time.Now())
	if f.saida() != "antes\n" {
		t.Errorf("%q", f.saida())
	}
}

func TestPromisePaginaJobDeDentroQueDaCertoFica(t *testing.T) {
	f := newJobSite(t, false, map[string]string{
		"index.djo": `uniq promise{
    Job.run:"gravar":{"texto": "dentro"}
}success{
}
`,
	})
	if code, _ := f.get(t, "/"); code != 200 {
		t.Fatalf("%d", code)
	}
	if n := f.engine(t).q.counts(); n["queued"] != 1 {
		t.Fatalf("%v", n)
	}
}

func TestPromisePaginaErroDepoisDoPromiseDescartaTudo(t *testing.T) {
	f := newJobSite(t, false, map[string]string{
		"index.djo": `uniq promise{
    Job.run:"gravar":{"texto": "dentro"}
}success{
}
var y = 1 / 0
`,
	})
	if code, _ := f.get(t, "/"); code != 500 {
		t.Fatalf("%d", code)
	}
	if n := f.engine(t).q.counts(); n["queued"]+n["held"] != 0 {
		t.Fatalf("%v", n)
	}
}

func TestPromiseNaPaginaNaoAlteraARespostaNemASessao(t *testing.T) {
	f := newJobSite(t, false, map[string]string{
		"index.djo": `uniq promise{
    Session.set:"a":1
}error{
    [e]
    print e["msg"]
}
uniq promise{
    redirect:"/x"
}error{
    [e]
    print e["msg"]
}
`,
	})
	code, body := f.get(t, "/")
	if code != 200 || strings.Count(body, "altera a resposta ou a sessão") != 2 {
		t.Fatalf("%d %q", code, body)
	}
}

func TestPromiseJobGiveUpDentroDoPromiseVaiParaDead(t *testing.T) {
	f := newJobSite(t, false, map[string]string{
		"core/j.djo": jobCore + `
f desiste_em_promise
    uniq promise{
        var x = 1 / 0
    }error{
        [e]
        Job.give_up:"não adianta"
    }finally{
        File.append:"_dados/saida.txt":"finally\n"
    }
`,
		"index.djo": `Job.run:"desiste_em_promise":{}`,
	})
	f.get(t, "/")
	j := f.runNext(t, time.Now())
	if j.state != "dead" || j.tries != 1 {
		t.Fatalf("o give_up dentro do error devia ir direto para dead: %+v", j)
	}
	if f.saida() != "finally\n" {
		t.Errorf("o finally roda antes de o give_up passar: %q", f.saida())
	}
}

func TestPromiseJobErroSemErrorRepete(t *testing.T) {
	f := newJobSite(t, false, map[string]string{
		"core/j.djo": jobCore + `
f falha_em_promise
    uniq promise{
        var x = 1 / 0
    }finally{
        File.append:"_dados/saida.txt":"finally\n"
    }
`,
		"index.djo": `Job.run:"falha_em_promise":{}`,
	})
	f.get(t, "/")
	j := f.runNext(t, time.Now())
	if j.state != "queued" || j.tries != 1 {
		t.Fatalf("o erro sobe e o job tenta de novo: %+v", j)
	}
}
