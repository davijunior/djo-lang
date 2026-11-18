package main

import (
	"strings"
	"testing"
)

type promiseCase struct {
	name string
	src  string
	out  string // saída esperada (quando não há erro)
	err  string // trecho do erro esperado ("" = sem erro)
}

func runPromiseCases(t *testing.T, cases []promiseCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, c.src)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("esperava erro com %q, veio %v (saída %q)", c.err, err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("erro inesperado: %v (saída %q)", err, out)
			}
			if out != c.out {
				t.Fatalf("saída %q, esperado %q", out, c.out)
			}
		})
	}
}

func TestPromiseValores(t *testing.T) {
	runPromiseCases(t, []promiseCase{
		{"success devolve o valor", `
var p = promise{
    return 2
}success{
    [d]
    return d * 10
}
print p
`, "20\n", ""},
		{"sem success o return do promise é o valor", `
var p = promise{
    return 7
}finally{
    print "fim"
}
print p
`, "fim\n7\n", ""},
		{"sem return resolve nil", `
var p = promise{
    var x = 1
}success{
    [d]
    return d
}
print p
`, "nil\n", ""},
		{"error devolve o valor", `
var p = promise{
    var x = 1 / 0
}success{
    [d]
    return "ok"
}error{
    [e]
    return "falhou: " + e["msg"]
}
print p
`, "falhou: divisão por zero\n", ""},
		{"error sem return vale nil", `
var p = promise{
    var x = 1 / 0
}error{
    [e]
    print "tratei"
}
print p
`, "tratei\nnil\n", ""},
		{"success e error sem parâmetro", `
uniq promise{
    return 1
}success{
    print "certo"
}error{
    print "errado"
}
`, "certo\n", ""},
		{"expressão como argumento e return", `
f dobro
    params[x]
    return x * 2
f usa
    return promise{
        return dobro:4
    }success{
        [d]
        return d + 1
    }
print usa:
print dobro:(promise{
    return 5
}success{
    [d]
    return d
})
`, "9\n10\n", ""},
		{"return sai do bloco e não da função", `
f f1
    uniq promise{
        return 1
    }success{
        [d]
        return 99
    }
    return "depois"
print f1:
`, "depois\n", ""},
		{"forma indentada", `
uniq promise
    return 3
success
    [d]
    print d
error
    [e]
    print "x"
finally
    print "fim"
`, "3\nfim\n", ""},
		{"chaves em linhas separadas", `
uniq promise{
    return 3
}
success{
    [d]
    print d
}
finally{
    print "fim"
}
`, "3\nfim\n", ""},
	})
}

func TestPromiseErroSobeEFinally(t *testing.T) {
	runPromiseCases(t, []promiseCase{
		{"sem error o erro sobe, depois do finally", `
uniq promise{
    var x = 1 / 0
}finally{
    print "limpou"
}
`, "", "divisão por zero"},
		{"erro no success sobe e o finally roda", `
var out = 0
f g
    uniq promise{
        return 1
    }success{
        [d]
        reject:"no_success":"quebrei no success"
    }error{
        print "nao deve pegar"
    }finally{
        print "finally"
    }
g:
`, "finally\n", "quebrei no success"},
		{"erro no error sobe", `
uniq promise{
    var x = 1 / 0
}error{
    [e]
    reject:"de_novo":"segundo erro"
}finally{
    print "fim"
}
`, "", "segundo erro"},
		{"erro no finally vence", `
uniq promise{
    var x = 1 / 0
}finally{
    reject:"do_finally":"erro do finally"
}
`, "", "erro do finally"},
		{"error vazio engole", `
uniq promise{
    var x = 1 / 0
}error{
}
print "seguiu"
`, "seguiu\n", ""},
		{"finally roda com error que relança", `
uniq promise{
    var x = 1 / 0
}error{
    [e]
    reject:e
}finally{
    print "finally"
}
`, "finally\n", "divisão por zero"},
		{"return no finally é erro de sintaxe", `
uniq promise{
    return 1
}finally{
    return 2
}
`, "", "return não vale dentro do finally"},
		{"parâmetro no finally", `
uniq promise{
    return 1
}finally{
    [x]
    print 1
}
`, "", "não tem parâmetro"},
		{"parâmetro no promise", `
uniq promise{
    [x]
    print 1
}success{
    print 1
}
`, "", "não tem parâmetro"},
		{"promise sem cláusula", `
uniq promise{
    print 1
}
`, "", "precisa de success, error ou finally"},
		{"cláusula fora de ordem", `
uniq promise{
    print 1
}error{
    print 2
}success{
    print 3
}
`, "", "fora de ordem"},
		{"success sem promise", `
success{
    print 1
}
`, "", "sem um promise antes"},
	})
}

func TestPromiseMapaDoErro(t *testing.T) {
	runPromiseCases(t, []promiseCase{
		{"erro do djo", `
uniq promise{
    var x = 1 / 0
}error{
    [e]
    print e["code"]
    print e["msg"]
    print e["line"]
    print e["data"]
    print e["file"]
}
`, "erro\ndivisão por zero\n3\nnil\nnil\n", ""},
		{"reject com código e dados", `
uniq promise{
    reject:"omie_fora":"Omie indisponível":{"status": 503}
}error{
    [e]
    print e["code"] + "|" + e["msg"] + "|" + (str:e["data"]["status"])
}
`, "omie_fora|Omie indisponível|503\n", ""},
		{"reject só com a mensagem", `
uniq promise{
    reject:"só a mensagem"
}error{
    [e]
    print e["code"] + "|" + e["msg"]
}
`, "erro|só a mensagem\n", ""},
		{"reject com o código reservado", `
reject:"erro":"x"
`, "", "reservado"},
		{"reject:e relança igual", `
uniq promise{
    uniq promise{
        reject:"c1":"msg1":[1, 2]
    }error{
        [e]
        reject:e
    }
}error{
    [e2]
    print e2["code"] + " " + e2["msg"] + " " + (str:(len:e2["data"])) + " " + (str:e2["line"])
}
`, "c1 msg1 2 4\n", ""},
		{"reject sem promise é erro de página", `
reject:"c":"fora"
`, "", "fora"},
		{"reject com argumento errado", `
reject:3
`, "", "reject:"},
	})
}

func TestPromiseEntregaNaEspera(t *testing.T) {
	runPromiseCases(t, []promiseCase{
		{"a saída entra onde o promise foi escrito", `
var p = promise{
    print "a"
    return 1
}success{
    [d]
    print "b"
    return d
}
print "c"
print p
print p
`, "a\nb\nc\n1\n1\n", ""},
		{"uniq entrega na hora", `
var p = uniq promise{
    print "a"
    return 1
}success{
    [d]
    return d
}
print "c"
print p
`, "a\nc\n1\n", ""},
		{"instrução sem uniq: a saída fica no lugar", `
f g
    promise{
        print "dentro"
    }success{
        print "ok"
    }
    print "corpo da função"
g:
print "fora"
`, "dentro\nok\ncorpo da função\nfora\n", ""},
		{"promise nunca lido: o erro sobe no fim da função", `
f g
    var p = promise{
        var x = 1 / 0
    }success{
        [d]
        return d
    }
    print "corpo"
g:
`, "", "divisão por zero"},
		{"promise nunca lido com error: tratado", `
f g
    var p = promise{
        var x = 1 / 0
    }error{
        [e]
        print "tratado"
    }
    print "corpo"
g:
print "fim"
`, "tratado\ncorpo\nfim\n", ""},
		{"instrução na página: a saída fica no lugar", `
promise{
    print "a"
}success{
    print "b"
}
print "c"
`, "a\nb\nc\n", ""},
		{"erro lido duas vezes sobe nas duas", `
var p = promise{
    reject:"c":"ruim"
}success{
    [d]
    return d
}
uniq promise{
    print p
}error{
    [e]
    print "1: " + e["msg"]
}
uniq promise{
    print p
}error{
    [e]
    print "2: " + e["msg"]
}
`, "1: ruim\n2: ruim\n", ""},
	})
}

func TestPromiseIsolamento(t *testing.T) {
	runPromiseCases(t, []promiseCase{
		{"lê cópia de fora", `
var base = 10
uniq promise{
    print base + 1
}success{
}
`, "11\n", ""},
		{"atribuir a variável de fora", `
var total = 1
uniq promise{
    total = 2
}error{
}
`, "", ""},
		{"atribuir a variável de fora dá erro claro", `
var total = 1
uniq promise{
    total = 2
}success{
}
`, "", "não altera variáveis de fora ('total')"},
		{"++ em variável de fora", `
var n = 1
uniq promise{
    n++
}error{
    [e]
    print e["msg"]
}
`, "o promise não altera variáveis de fora ('n'); devolva o valor com return\n", ""},
		{"índice em lista de fora", `
var l = [1, 2]
uniq promise{
    l[0] = 9
}error{
    [e]
    print e["msg"]
}
`, "o promise não altera variáveis de fora ('l'); devolva o valor com return\n", ""},
		{"mapa de fora", `
var m = {"a": 1}
uniq promise{
    m["a"] = 2
}error{
    [e]
    print e["msg"]
}
`, "o promise não altera variáveis de fora ('m'); devolva o valor com return\n", ""},
		{"a cópia é funda: mexer na cópia não muda o original", `
var l = [1, 2]
uniq promise{
    push:l:3
    print len:l
}success{
}
print len:l
`, "3\n2\n", ""},
		{"variável local do bloco pode ser escrita", `
uniq promise{
    var x = 1
    x = x + 1
    x++
    print x
}error{
}
`, "3\n", ""},
		{"função chamada do bloco não escreve em global", `
var g = 1
f muda
    g = 5
uniq promise{
    muda:
}error{
    [e]
    print e["msg"]
}
print g
`, "o promise não altera variáveis de fora ('g'); devolva o valor com return\n1\n", ""},
		{"fora do promise nada muda", `
var g = 1
f muda
    g = 5
muda:
print g
`, "5\n", ""},
		{"o success não vê as variáveis do corpo", `
uniq promise{
    var local = 1
}success{
    print local
}error{
    [e]
    print e["msg"]
}
`, "", "não existe"},
	})
}

func TestPromisePalavrasContextuais(t *testing.T) {
	runPromiseCases(t, []promiseCase{
		{"nomes antigos continuam variáveis", `
var error = "e"
var success = true
var promise = 1
var uniq = 2
var finally = 3
print error
print success
print promise + uniq + finally
var r = {"error": "x", "success": true, "promise": 1}
print r["error"]
`, "e\ntrue\n6\nx\n", ""},
		{"promise como variável depois de usar a palavra", `
var promise = 5
promise = promise + 1
print promise
`, "6\n", ""},
		{"nesta mesma linha chaves em mapa", `
var m = {"success": 1}
print m["success"]
`, "1\n", ""},
	})
}

func TestPromiseFatais(t *testing.T) {
	runPromiseCases(t, []promiseCase{
		{"recursão sem fim não é capturada", `
f r
    r:
uniq promise{
    r:
}error{
    [e]
    print "NAO"
}finally{
    print "NAO"
}
`, "", "recursão funda demais"},
	})
}

func TestPromisePassos(t *testing.T) {
	prog, err := Parse(`
uniq promise{
    print "vazou?"
    while{
        cond[true]
        var x = 1
    }
}error{
    [e]
    print "NAO"
}finally{
    print "NAO"
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	in := NewInterp(&out)
	in.maxSteps = 50000
	err = in.Run(prog)
	if err == nil || !strings.Contains(err.Error(), "limite de passos") {
		t.Fatalf("esperava o estouro de passos, veio %v", err)
	}
	if out.String() != "" {
		t.Fatalf("o error e o finally não podem rodar num limite fatal: %q", out.String())
	}
}

func TestPromiseAninhado(t *testing.T) {
	runPromiseCases(t, []promiseCase{
		{"promise dentro de success", `
uniq promise{
    return 1
}success{
    [a]
    uniq promise{
        return a + 1
    }success{
        [b]
        print b
    }
}
`, "2\n", ""},
		{"if dentro do corpo e cláusulas na coluna certa", `
f g
    params[n]
    uniq promise{
        if{ cond[n > 1] reject:"c":"grande" }
        return n
    }success{
        [d]
        print "ok " + (str:d)
    }error{
        [e]
        print "erro " + e["msg"]
    }
g:1
g:5
`, "ok 1\nerro grande\n", ""},
		{"promise dentro de if com else", `
var n = 2
if
    cond[n == 1]
    print "um"
else
    uniq promise
        print "dentro do else"
    success
        print "ok"
    finally
        print "fim"
`, "dentro do else\nok\nfim\n", ""},
	})
}

func TestPromiseFileDoBooster(t *testing.T) {
	root := t.TempDir()
	mkBooster(t, root, "mat", "", map[string]string{"main.djo": "f quebra\n    var x = 1 / 0\n\nf recusa\n    reject:\"mat_recusou\":\"não pode\":{\"k\": 1}\n"})
	out, err := runWith(t, root, `
uniq promise{
    mat.quebra:
}error{
    [e]
    print e["file"] + " " + e["msg"] + " " + e["code"]
}
uniq promise{
    mat.recusa:
}error{
    [e]
    print e["file"] + " " + e["code"] + " " + (str:e["data"]["k"])
}
uniq promise{
    var y = 1 / 0
}error{
    [e]
    print e["file"]
}
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "boosters/mat divisão por zero erro\nboosters/mat mat_recusou 1\nnil\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestPromiseEmTemplate(t *testing.T) {
	prog, err := ParseTemplate("<p>{% var p = promise{ return 2 }success{ [d] return d * 3 } %}{{ p }}</p>\n{% uniq promise{ var x = 1 / 0 }error{ [e] %}<b>{{ e[\"msg\"] }}</b>{% } %}")
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := NewInterp(&buf).Run(prog); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "<p>6</p>") || !strings.Contains(got, "<b>divisão por zero</b>") {
		t.Fatalf("%q", got)
	}
}

func TestPromiseSaidaNoLugarEDescartaEmErro(t *testing.T) {
	runPromiseCases(t, []promiseCase{
		{"A, dentro, success, B", `
print "A"
promise{
    print "dentro"
}success{
    print "success"
}
print "B"
`, "A\ndentro\nsuccess\nB\n", ""},
		{"corpo imprime e falha: só o error aparece", `
print "A"
promise{
    print "corpo"
    var x = 1 / 0
}success{
    print "success"
}error{
    [e]
    print "error"
}
print "B"
`, "A\nerror\nB\n", ""},
		{"success falha: o corpo do promise de fora, que falhou, perde a saída de tudo", `
uniq promise{
    uniq promise{
        print "corpo"
    }success{
        print "success"
        reject:"c":"quebrou no success"
    }finally{
        print "finally"
    }
}error{
    [e]
    print "tratado: " + e["msg"]
}
`, "tratado: quebrou no success\n", ""},
		{"error falha: idem", `
uniq promise{
    uniq promise{
        print "corpo"
        var x = 1 / 0
    }error{
        print "error"
        reject:"c":"quebrou no error"
    }finally{
        print "finally"
    }
}error{
    [e]
    print "fora: " + e["msg"]
}
`, "fora: quebrou no error\n", ""},
		{"erro sobe sem error: o promise de fora trata e só o error dele aparece", `
uniq promise{
    uniq promise{
        print "corpo"
        var x = 1 / 0
    }finally{
        print "finally"
    }
}error{
    print "pego"
}
`, "pego\n", ""},
		{"finally imprime sempre", `
uniq promise{
    print "corpo"
}finally{
    print "f1"
}
uniq promise{
    var x = 1 / 0
}error{
}finally{
    print "f2"
}
`, "corpo\nf1\nf2\n", ""},
		{"aninhado: o promise de dentro de um corpo que falha se perde", `
uniq promise{
    uniq promise{
        print "interno"
    }success{
        print "interno ok"
    }
    var x = 1 / 0
}error{
    print "tratado"
}
`, "tratado\n", ""},
		{"aninhado que dá certo aparece na ordem", `
uniq promise{
    print "1"
    promise{
        print "2"
    }success{
        print "3"
    }
    print "4"
}success{
    print "5"
}
print "6"
`, "1\n2\n3\n4\n5\n6\n", ""},
		{"expressão var p: a saída fica na declaração", `
print "A"
var p = promise{
    print "dentro"
    return 7
}success{
    [d]
    print "success"
    return d
}
print "B"
print p
`, "A\ndentro\nsuccess\nB\n7\n", ""},
		{"return promise: a saída fica no lugar", `
f g
    print "antes"
    return promise{
        print "dentro"
        return 1
    }success{
        [d]
        return d
    }
print g:
`, "antes\ndentro\n1\n", ""},
		{"dentro de função chamada", `
f g
    uniq promise{
        print "x"
        reject:"c":"falhou"
    }error{
        print "tratado"
    }
    print "depois"
print "A"
g:
print "B"
`, "A\ntratado\ndepois\nB\n", ""},
		{"o erro que sobe da página não deixa saída do promise", `
print "A"
uniq promise{
    print "corpo"
    var x = 1 / 0
}success{
}
`, "", "divisão por zero"},
	})
}

func TestPromiseSaidaNoJob(t *testing.T) {
	prog, err := Parse(`f tarefa
    uniq promise{
        print "corpo"
        var x = 1 / 0
    }error{
        print "error"
    }finally{
        print "finally"
    }
`)
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	in := NewInterp(&buf)
	if err := in.Run(prog); err != nil {
		t.Fatal(err)
	}
	if err, _, _ := jobInvoke(in, "tarefa", "{}"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "error\nfinally\n" {
		t.Fatalf("%q", buf.String())
	}
}

func TestPromiseSaidaEmTemplateNoLugar(t *testing.T) {
	prog, err := ParseTemplate("A {% promise{ %}dentro {% }success{ %}ok {% } %}B")
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := NewInterp(&buf).Run(prog); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "A dentro ok B" {
		t.Fatalf("%q", buf.String())
	}
}

// Quando o erro sobe do promise e ninguém o trata dentro de outro promise, a saída que sobra é
// a do finally (a do corpo, do success e do error some).
func TestPromiseErroQueSobeSoDeixaOFinally(t *testing.T) {
	for name, src := range map[string]string{
		"corpo falha": `print "A"
uniq promise{
    print "corpo"
    var x = 1 / 0
}success{
    print "success"
}error{
    print "error"
    reject:e2:"x"
}finally{
    print "finally"
}
`,
		"success falha": `print "A"
uniq promise{
    print "corpo"
}success{
    print "success"
    reject:"c":"no success"
}finally{
    print "finally"
}
`,
		"sem error": `print "A"
uniq promise{
    print "corpo"
    var x = 1 / 0
}finally{
    print "finally"
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := run(t, src)
			if err == nil {
				t.Fatal("o erro devia subir")
			}
			if out != "A\nfinally\n" {
				t.Fatalf("saída %q, esperado %q", out, "A\nfinally\n")
			}
		})
	}
}
