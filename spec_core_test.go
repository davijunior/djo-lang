package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specProject monta um projeto numa pasta temporária, com o booster expect instalado.
func specProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var o, e bytes.Buffer
	if code := boosterAdd(root, "boosters-oficiais/expect", "local", &o, &e); code != 0 {
		t.Fatalf("booster add expect: %s %s", o.String(), e.String())
	}
	return root
}

func runSpec(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := specMain(args, &out, &errw)
	return code, out.String(), errw.String()
}

const specBasico = `f soma{
    params[a[Int], b[Int]]
    return a + b
}
`

const specBasicoSpec = `f it_passa
    expect.eq:(soma:2:3):5

f it_falha
    expect.eq:(soma:2:3):6

f it_pula
    Spec.skip:"depois eu faço"

f it_erro
    var x = 1 / 0
`

func TestSpecPassaFalhaPulaErro(t *testing.T) {
	root := specProject(t, map[string]string{"core/m.djo": specBasico, "spec/m_spec.djo": specBasicoSpec})
	code, out, errw := runSpec(t, root)
	if code != 1 || errw != "" {
		t.Fatalf("código %d, stderr %q", code, errw)
	}
	for _, want := range []string{
		".F*E",
		"1) falha",
		"spec/m_spec.djo:5", // a linha do expect.eq que falhou, no arquivo de spec
		"os valores são diferentes",
		"esperado: 6",
		"recebido: 5",
		"2) erro [erro]",
		"divisão por zero",
		"* pula (spec/m_spec.djo): depois eu faço",
		"4 specs, 1 falha, 1 erro, 1 pulado, em ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q na saída:\n%s", want, out)
		}
	}
}

func TestSpecTudoPassaSaiZero(t *testing.T) {
	root := specProject(t, map[string]string{"core/m.djo": specBasico, "spec/m_spec.djo": "f it_a\n    expect.eq:(soma:1:1):2\n"})
	code, out, _ := runSpec(t, root)
	if code != 0 || !strings.HasPrefix(out, ".\n") || !strings.Contains(out, "1 spec, 0 falhas, 0 erros, 0 pulados") {
		t.Fatalf("código %d:\n%s", code, out)
	}
}

func TestSpecJSON(t *testing.T) {
	root := specProject(t, map[string]string{"core/m.djo": specBasico, "spec/m_spec.djo": specBasicoSpec})
	code, out, _ := runSpec(t, "--json", root)
	if code != 1 {
		t.Fatalf("código %d", code)
	}
	var doc struct {
		Resumo struct{ Total, Passaram, Falharam, Erros, Pulados int }
		Specs  []struct {
			Arquivo, Nome, Status, Mensagem, Esperado, Recebido string
			Linha                                               int
		}
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("JSON inválido: %v\n%s", err, out)
	}
	r := doc.Resumo
	if r.Total != 4 || r.Passaram != 1 || r.Falharam != 1 || r.Erros != 1 || r.Pulados != 1 {
		t.Errorf("resumo %+v", r)
	}
	if len(doc.Specs) != 4 || doc.Specs[1].Status != "falhou" || doc.Specs[1].Esperado != "6" || doc.Specs[1].Recebido != "5" || doc.Specs[1].Linha != 5 || doc.Specs[1].Arquivo != "spec/m_spec.djo" {
		t.Errorf("specs %+v", doc.Specs)
	}
	if strings.Contains(out, ".F*E") {
		t.Error("o --json não pode misturar o progresso")
	}
}

func TestSpecFiltro(t *testing.T) {
	root := specProject(t, map[string]string{"core/m.djo": specBasico, "spec/m_spec.djo": specBasicoSpec})
	code, out, _ := runSpec(t, "-e", "passa", root)
	if code != 0 || !strings.Contains(out, "1 spec, 0 falhas") {
		t.Errorf("-e passa: %d\n%s", code, out)
	}
	code, out, _ = runSpec(t, "-e", "passa", "--only", "pula", root)
	if code != 0 || !strings.Contains(out, "2 specs, 0 falhas, 0 erros, 1 pulado") {
		t.Errorf("dois filtros: %d\n%s", code, out)
	}
	code, out, _ = runSpec(t, "-e", "nada-disso", root)
	if code != 0 || !strings.Contains(out, "0 specs") {
		t.Errorf("filtro sem resultado: %d\n%s", code, out)
	}
}

func TestSpecOrdemEstavelEPorSemente(t *testing.T) {
	var sb strings.Builder
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		sb.WriteString("f it_" + n + "\n    expect.eq:1:" + map[bool]string{true: "1", false: "2"}[n != "c" && n != "f"] + "\n\n")
	}
	root := specProject(t, map[string]string{"spec/o_spec.djo": sb.String()})
	order := func(args ...string) string {
		_, out, _ := runSpec(t, append(args, "--json", root)...)
		var doc struct{ Specs []struct{ Nome string } }
		json.Unmarshal([]byte(out), &doc)
		var names []string
		for _, s := range doc.Specs {
			names = append(names, s.Nome)
		}
		return strings.Join(names, ",")
	}
	if got := order(); got != "a,b,c,d,e,f,g,h" {
		t.Errorf("a ordem padrão é a do arquivo: %s", got)
	}
	if order() != order() {
		t.Error("duas execuções iguais deram ordens diferentes")
	}
	s1, s2 := order("--seed", "42"), order("--seed", "42")
	if s1 != s2 {
		t.Errorf("a mesma semente deu ordens diferentes: %s x %s", s1, s2)
	}
	if s1 == "a,b,c,d,e,f,g,h" && order("--seed", "7") == s1 {
		t.Error("a semente não embaralhou")
	}
}

func TestSpecAntesDepois(t *testing.T) {
	root := specProject(t, map[string]string{"spec/a_spec.djo": `var n = 0

f antes
    print "antes"

f depois
    print "depois"

f it_falha_com_saida
    print "no meio"
    expect.eq:1:2

f it_passa
    expect.eq:1:1
`})
	code, out, _ := runSpec(t, root)
	if code != 1 {
		t.Fatalf("código %d", code)
	}
	// o depois roda mesmo com a falha, e o que o spec imprimiu aparece no relatório do que falhou
	for _, want := range []string{"saída do spec:", "antes", "no meio", "depois"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "antes") != 1 {
		t.Errorf("a saída do spec que passou não entra no relatório:\n%s", out)
	}
}

func TestSpecErroNoAntesNaoRodaOSpec(t *testing.T) {
	root := specProject(t, map[string]string{"spec/a_spec.djo": `f antes
    var x = 1 / 0

f it_a
    print "rodou"
`})
	_, out, _ := runSpec(t, root)
	if !strings.Contains(out, "E") || strings.Contains(out, "rodou") {
		t.Errorf("o it_ não pode rodar se o antes quebrou:\n%s", out)
	}
}

func TestSpecIngles(t *testing.T) {
	root := specProject(t, map[string]string{"spec/a_spec.djo": "f it_a\n    expect.eq:1:2\n\nf it_b\n    expect.include:[1]:2\n"})
	_, out, _ := runSpec(t, "--lang", "en", root)
	for _, want := range []string{"Problems:", "the values differ", "expected: 2", "received: 1", "does not include the item", "2 specs, 2 failures, 0 errors, 0 skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q:\n%s", want, out)
		}
	}
	t.Setenv("DJO_LANG", "en")
	if _, out, _ := runSpec(t, root); !strings.Contains(out, "Problems:") {
		t.Errorf("DJO_LANG=en não valeu:\n%s", out)
	}
	if code, _, errw := runSpec(t, "--lang", "xx", root); code != 2 || !strings.Contains(errw, "desconhecido") {
		t.Errorf("idioma inválido: %d %q", code, errw)
	}
}

func TestSpecTryRaises(t *testing.T) {
	root := specProject(t, map[string]string{"core/m.djo": specBasico, "spec/a_spec.djo": `f it_raises_ok
    expect.raises:"soma":["a", 1]:"Int"

f it_raises_sem_trecho
    expect.raises:"soma":["a", 1]:""

f it_raises_trecho_errado
    expect.raises:"soma":["a", 1]:"banana"

f it_raises_nao_deu_erro
    expect.raises:"soma":[1, 1]:""

f it_try_devolve_o_resultado
    var r = Spec.try:"soma":[1, 2]
    expect.eq:r["ok"]:true
    expect.eq:r["value"]:3
    var e = Spec.try:"soma":["x", 2]
    expect.eq:e["ok"]:false
    expect.include:e["error"]:"soma"
`})
	_, out, _ := runSpec(t, "--json", root)
	var doc struct {
		Specs []struct{ Nome, Status, Mensagem string }
	}
	json.Unmarshal([]byte(out), &doc)
	want := map[string]string{"raises ok": "passou", "raises sem trecho": "passou", "raises trecho errado": "falhou", "raises nao deu erro": "falhou", "try devolve o resultado": "passou"}
	if len(doc.Specs) != len(want) {
		t.Fatalf("saída:\n%s", out)
	}
	for _, s := range doc.Specs {
		if want[s.Nome] != s.Status {
			t.Errorf("%s: %s (%s)", s.Nome, s.Status, s.Mensagem)
		}
	}
}

func TestSpecTryNaoEngoleFalhaNemSkip(t *testing.T) {
	root := specProject(t, map[string]string{"core/m.djo": "f quebra\n    Spec.fail:\"de dentro\"\n\nf pula\n    Spec.skip:\"de dentro\"\n", "spec/a_spec.djo": `f it_falha
    Spec.try:"quebra":[]
    expect.eq:1:1

f it_pula
    Spec.try:"pula":[]
    expect.eq:1:1
`})
	_, out, _ := runSpec(t, "--json", root)
	var doc struct {
		Specs []struct{ Nome, Status, Mensagem string }
	}
	json.Unmarshal([]byte(out), &doc)
	if len(doc.Specs) != 2 || doc.Specs[0].Status != "falhou" || doc.Specs[0].Mensagem != "de dentro" || doc.Specs[1].Status != "pulado" {
		t.Errorf("%+v", doc.Specs)
	}
}

// Um laço sem fim vira erro do spec e não trava a suite.
func TestSpecLoopInfinito(t *testing.T) {
	root := specProject(t, map[string]string{"spec/a_spec.djo": "f it_loop\n    while{\n        cond[true]\n    }\n\nf it_depois_do_loop\n    expect.eq:1:1\n"})
	code, out, _ := runSpec(t, root)
	if code != 1 || !strings.Contains(out, "limite de passos") || !strings.Contains(out, "2 specs, 0 falhas, 1 erro") {
		t.Errorf("%d\n%s", code, out)
	}
}

// Spec.* só funciona dentro do djo spec: um script ou página comum recusa.
func TestSpecSoSobDjoSpec(t *testing.T) {
	for _, call := range []string{`Spec.fail:"x"`, `Spec.skip:"x"`, `Spec.try:"f":[]`, `Spec.lang:`, `Spec.get:"/"`} {
		prog, err := Parse(call)
		if err != nil {
			t.Fatal(err)
		}
		err = NewInterp(io.Discard).Run(prog)
		if err == nil || !strings.Contains(err.Error(), "só existe quando os testes rodam com 'djo spec'") {
			t.Errorf("%s fora do djo spec: %v", call, err)
		}
	}
}

func TestSpecMatchers(t *testing.T) {
	type c struct {
		name, call string
		ok         bool
		msg        string
	}
	cases := []c{
		{"eq int float", "expect.eq:1:1.0", true, ""},
		{"eq texto", `expect.eq:"a":"a"`, true, ""},
		{"eq lista funda", `expect.eq:[1, [2, {"a": 3}]]:[1, [2, {"a": 3}]]`, true, ""},
		{"eq mapa sem ordem", `expect.eq:{"a": 1, "b": 2}:{"b": 2, "a": 1}`, true, ""},
		{"eq date", "expect.eq:(Date.make:2025:1:31):(Date.make:2025:1:31)", true, ""},
		{"eq datetime", "expect.eq:(DateTime.make:2025:1:31:10:0:0):(DateTime.make:2025:1:31:10:0:0)", true, ""},
		{"eq date diferente", "expect.eq:(Date.make:2025:1:31):(Date.make:2025:2:1)", false, "os valores são diferentes"},
		{"eq date x datetime", "expect.eq:(Date.make:2025:1:31):(DateTime.make:2025:1:31:0:0:0)", false, "os valores são diferentes"},
		{"eq lista tamanho", "expect.eq:[1, 2]:[1, 2, 3]", false, "os valores são diferentes"},
		{"eq mapa chave", `expect.eq:{"a": 1}:{"b": 1}`, false, "os valores são diferentes"},
		{"eq tipo", `expect.eq:1:"1"`, false, "os valores são diferentes"},
		{"eq nil", "expect.eq:nil:nil", true, ""},
		{"not_eq", "expect.not_eq:1:2", true, ""},
		{"not_eq falha", "expect.not_eq:[1]:[1]", false, "deviam ser diferentes"},
		{"be_nil", "expect.be_nil:nil", true, ""},
		{"be_nil falha", "expect.be_nil:0", false, "esperava nil"},
		{"be_true", "expect.be_true:true", true, ""},
		{"be_true com 1", "expect.be_true:1", false, "esperava true"},
		{"be_false", "expect.be_false:false", true, ""},
		{"be_false com nil", "expect.be_false:nil", false, "esperava false"},
		{"include texto", `expect.include:"olá mundo":"mundo"`, true, ""},
		{"include lista", `expect.include:[1, {"a": 2}]:{"a": 2}`, true, ""},
		{"include mapa", `expect.include:{"k": 1}:"k"`, true, ""},
		{"include falha", "expect.include:[1, 2]:3", false, "não inclui"},
		{"include texto com int", `expect.include:"abc":1`, false, "outro texto"},
		{"gt", "expect.gt:3:2", true, ""},
		{"gt falha", "expect.gt:2:2", false, "esperava maior"},
		{"gte", "expect.gte:2:2", true, ""},
		{"lt", "expect.lt:1.5:2", true, ""},
		{"lt falha", "expect.lt:3:2", false, "esperava menor"},
		{"lte", "expect.lte:2:2", true, ""},
		{"gt com texto", `expect.gt:"a":1`, false, "esperava um número"},
		{"close", "expect.close:0.30000000000000004:0.3:0.001", true, ""},
		{"close falha", "expect.close:1:2:0.5", false, "fora da tolerância"},
		{"len lista", "expect.len:[1, 2]:2", true, ""},
		{"len mapa", `expect.len:{"a": 1}:1`, true, ""},
		{"len texto", `expect.len:"abc":3`, true, ""},
		{"len falha", "expect.len:[1]:2", false, "tamanho diferente"},
		{"len de int", "expect.len:5:1", false, "len espera"},
		{"empty", "expect.empty:[]", true, ""},
		{"empty falha", `expect.empty:"a"`, false, "esperava vazio"},
		{"starts", `expect.starts:"abcdef":"abc"`, true, ""},
		{"starts falha", `expect.starts:"abcdef":"bcd"`, false, "não começa"},
		{"starts com int", "expect.starts:1:2", false, "dois textos"},
		{"ends", `expect.ends:"abcdef":"def"`, true, ""},
		{"ends falha", `expect.ends:"abcdef":"abc"`, false, "não termina"},
		{"ends maior que o texto", `expect.ends:"ab":"abc"`, false, "não termina"},
	}
	var sb strings.Builder
	for i, k := range cases {
		sb.WriteString("f it_c" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + "\n    " + k.call + "\n\n")
	}
	root := specProject(t, map[string]string{"spec/m_spec.djo": sb.String()})
	_, out, _ := runSpec(t, "--json", root)
	var doc struct {
		Specs []struct{ Nome, Status, Mensagem string }
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Specs) != len(cases) {
		t.Fatalf("%v\n%s", err, out)
	}
	for i, k := range cases {
		s := doc.Specs[i]
		if k.ok && s.Status != "passou" {
			t.Errorf("%s: deveria passar, deu %s: %s", k.name, s.Status, s.Mensagem)
		}
		if !k.ok && (s.Status != "falhou" || !strings.Contains(s.Mensagem, k.msg)) {
			t.Errorf("%s: deveria falhar com %q, deu %s: %s", k.name, k.msg, s.Status, s.Mensagem)
		}
	}
}

func TestSpecDescobreRaizEArquivos(t *testing.T) {
	root := specProject(t, map[string]string{"core/m.djo": specBasico, "spec/a_spec.djo": "f it_a\n    expect.eq:1:1\n", "spec/sub/b_spec.djo": "f it_b\n    expect.eq:(soma:1:1):2\n", "spec/leia.txt": "x", "boosters/x_spec.djo": "f it_x\n    expect.eq:1:2\n"})
	for name, args := range map[string][]string{
		"raiz":     {root},
		"spec":     {filepath.Join(root, "spec")},
		"subpasta": {filepath.Join(root, "spec", "sub")},
		"arquivo":  {filepath.Join(root, "spec", "sub", "b_spec.djo")},
	} {
		code, out, errw := runSpec(t, args...)
		if code != 0 || errw != "" || strings.Contains(out, "F") {
			t.Errorf("%s: %d %q\n%s", name, code, errw, out)
		}
	}
	_, out, _ := runSpec(t, root)
	if !strings.Contains(out, "2 specs") {
		t.Errorf("o spec dentro de boosters/ não conta:\n%s", out)
	}
	if code, _, errw := runSpec(t, filepath.Join(root, "core")); code != 2 || !strings.Contains(errw, "nenhum arquivo") {
		t.Errorf("sem specs: %d %q", code, errw)
	}
	if code, _, errw := runSpec(t, filepath.Join(root, "spec", "leia.txt")); code != 2 || !strings.Contains(errw, "_spec.djo") {
		t.Errorf("arquivo que não é spec: %d %q", code, errw)
	}
}

func TestSpecErroDeSintaxeNoArquivo(t *testing.T) {
	root := specProject(t, map[string]string{"spec/a_spec.djo": "f it_a\n    expect.eq:(1\n", "spec/b_spec.djo": "f it_b\n    expect.eq:1:1\n"})
	code, out, _ := runSpec(t, root)
	if code != 1 || !strings.Contains(out, "E.") || !strings.Contains(out, "2 specs, 0 falhas, 1 erro") {
		t.Errorf("%d\n%s", code, out)
	}
}

func TestSpecPastaSpecNaoVaiNoBuild(t *testing.T) {
	d := dirEntryFor{isDir: true}
	if !buildExcluded("spec", d, nil) {
		t.Error("spec/ não pode entrar no djo build")
	}
	if buildExcluded("docs", d, nil) {
		t.Error("o filtro pegou demais")
	}
}

type dirEntryFor struct{ isDir bool }

func (d dirEntryFor) Name() string               { return "x" }
func (d dirEntryFor) IsDir() bool                { return d.isDir }
func (d dirEntryFor) Type() os.FileMode          { return os.ModeDir }
func (d dirEntryFor) Info() (os.FileInfo, error) { return nil, nil }

// Spec.try só chama funções declaradas no projeto: um nome errado (ou nativo) é erro, não "ok:false".
func TestSpecTrySoChamaFuncaoDoProjeto(t *testing.T) {
	root := specProject(t, map[string]string{"spec/a_spec.djo": "f it_nativa\n    expect.raises:\"len\":[[1]]:\"\"\n\nf it_typo\n    expect.raises:\"naoexiste\":[]:\"\"\n"})
	_, out, _ := runSpec(t, "--json", root)
	var doc struct {
		Specs []struct{ Status, Mensagem string }
	}
	json.Unmarshal([]byte(out), &doc)
	if len(doc.Specs) != 2 {
		t.Fatal(out)
	}
	for _, s := range doc.Specs {
		if s.Status != "erro" || !strings.Contains(s.Mensagem, "não existe no projeto") {
			t.Errorf("%+v", s)
		}
	}
}

func TestSpecBoosterExpectEmpacotaEDeclaraPermissao(t *testing.T) {
	reg := t.TempDir()
	var out, errw bytes.Buffer
	if code := boosterCmd([]string{"pack", "-registro", reg, "boosters-oficiais/expect"}, &out, &errw); code != 0 {
		t.Fatalf("pack: %d %s %s", code, out.String(), errw.String())
	}
	idx, err := os.ReadFile(filepath.Join(reg, "indice.json"))
	if err != nil || !strings.Contains(string(idx), `"permissoes": [`) || !strings.Contains(string(idx), `"Spec"`) || strings.Contains(string(idx), `"Http"`) {
		t.Errorf("o índice deve declarar só a permissão Spec: %v\n%s", err, idx)
	}
	// instalado, aparece no list com a permissão
	root := specProject(t, nil)
	out.Reset()
	if code := boosterList(root, &out, &errw); code != 0 || !strings.Contains(out.String(), "expect") || !strings.Contains(out.String(), "Spec") {
		t.Errorf("list: %d %q", code, out.String())
	}
}
