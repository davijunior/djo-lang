package main

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"
)

func run(t *testing.T, src string) (string, error) {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = NewInterp(&buf).Run(prog)
	return buf.String(), err
}

func TestExemploBasico(t *testing.T) {
	src, err := os.ReadFile("exemplos/basico.djo")
	if err != nil {
		t.Fatal(err)
	}
	out, err := run(t, string(src))
	if err != nil {
		t.Fatal(err)
	}
	if want := "teste\nteste dois\n5\n"; out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestRetornoEEscopo(t *testing.T) {
	out, err := run(t, `
f dobro
    params[x[Int]]
    return x * 2

var a = 10
a = (dobro:a) + 1
print a
print "oi " + "mundo"
print 7 / 2
print 7.0 / 2
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "21\noi mundo\n3\n3.5\n"; out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestTipoOpcionalETravado(t *testing.T) {
	out, err := run(t, `
f f1
    params[a, b[Int]]
    a = "agora string"
    print a
    b = 5
    print b

f1:1:2
f1:"x":3
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "agora string\n5\nagora string\n5\n"; out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	_, err = run(t, "f g\n  params[b[Int]]\n  b = \"x\"\ng:1")
	if err == nil || !strings.Contains(err.Error(), "'b' é Int e não pode receber String") {
		t.Errorf("erro %v", err)
	}
}

func TestErros(t *testing.T) {
	casos := map[string]string{
		`print "abc" + 3`:                "não dá para usar '+' entre String e Int",
		"f g\n  params[a[Int]]\ng:\"x\"": "deve ser Int",
		"f g\n  params[a[Int]]\ng:":      "espera 1 argumento",
		`print naoexiste`:                "não existe",
		`nada:`:                          "função ou struct 'nada' não existe",
		`print 1 / 0`:                    "divisão por zero",
		"f g{\n print 1":                 "esperava '}'",
	}
	for src, want := range casos {
		_, err := run(t, src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: erro %v, esperava conter %q", src, err, want)
		}
	}
}

func TestRegrasDeSoma(t *testing.T) {
	out, err := run(t, `
print 2 + 2
print 2 + "2"
print "2" + 2
print "2" + "2"
print ("2" + "2") + 1
print 1.5 + "2"
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "4\n4\n4\n22\n23\n3.5\n"; out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestLacosEListas(t *testing.T) {
	src, err := os.ReadFile("exemplos/regras.djo")
	if err != nil {
		t.Fatal(err)
	}
	out, err := run(t, string(src))
	if err != nil {
		t.Fatal(err)
	}
	want := "4\n4\n22\nt\nte\ntex\ntext\ntexto\n0\n1\n2\n3\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestIncrementoListaEInput(t *testing.T) {
	prog, err := Parse(`
var n = input "nome: "
print n
arr xs = [1, 2, 3]
xs[1] = "dois"
print xs
var k = 5
k--
k--
print k
print "abc"[1]
`)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	in := NewInterp(&buf)
	in.stdin = bufio.NewReader(strings.NewReader("Ana\n"))
	if err := in.Run(prog); err != nil {
		t.Fatal(err)
	}
	if want := "nome: Ana\n[1, \"dois\", 3]\n3\nb\n"; buf.String() != want {
		t.Fatalf("saída %q, esperado %q", buf.String(), want)
	}
}

func TestErrosNovos(t *testing.T) {
	casos := map[string]string{
		"arr xs = 5":                               "precisa receber uma lista",
		"arr xs = [1]\nprint xs[3]":                "fora da lista",
		"var s = \"a\"\ns++":                       "só funcionam com número",
		"while{\n  print 1\n}":                     "precisa de um cond",
		"until{\n eng[i=0 ; i ; i++]\n print 1\n}": "precisa ser Bool",
		"until{\n print 1\n}":                      "precisa começar com eng",
	}
	for src, want := range casos {
		_, err := run(t, src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: erro %v, esperava conter %q", src, err, want)
		}
	}
}

func TestCondicionais(t *testing.T) {
	src, err := os.ReadFile("exemplos/condicionais.djo")
	if err != nil {
		t.Fatal(err)
	}
	out, err := run(t, string(src))
	if err != nil {
		t.Fatal(err)
	}
	want := "adulto\nnegativo\nzero\npositivo\nperfeito\nótimo\ncontinue estudando\nentrar\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestIfAninhadoEElse(t *testing.T) {
	// else do if de fora não pode ser capturado pelo if de dentro
	out, err := run(t, `
f teste
    params[a, b]
    if
        cond[a]
        if
            cond[b]
            print "a e b"
    else
        print "não a"

teste:true:true
teste:true:false
teste:false:true

if{ cond[true] print "chaves em uma linha" }
if{
    cond[false]
    print "x"
}
else{ print "else com chaves" }
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "a e b\nnão a\nchaves em uma linha\nelse com chaves\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestLogicosECurtoCircuito(t *testing.T) {
	out, err := run(t, `
print true and false
print true or false
print not false
print false and (1 / 0 == 1)
print true or (1 / 0 == 1)
print not 1 == 2 and 3 > 2
print pick[1 < 2 ; "sim" ; 1 / 0]
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "false\ntrue\ntrue\nfalse\ntrue\ntrue\nsim\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestMatchComChaves(t *testing.T) {
	out, err := run(t, `
var x = "b"
match{
    val[x]
    case["a"] print "A"
    case["b", "c"]
        print "B ou C"
        print "fim"
    else print "outro"
}
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "B ou C\nfim\n"; out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestErrosCondicionais(t *testing.T) {
	casos := map[string]string{
		"if{\n cond[1]\n print 1\n}":  "precisa ser Bool",
		"else{\n print 1\n}":          "sem um 'if'",
		"if{\n print 1\n}":            "precisa começar com cond",
		"if\n  cond[true] print 1":    "começa na linha de baixo",
		"match{\n case[1] print 1\n}": "precisa começar com val",
		"print true and 1":            "'and' precisa de Bool",
		"print not 3":                 "'not' precisa de Bool",
		"print pick[1 ; 2 ; 3]":       "precisa ser Bool",
	}
	for src, want := range casos {
		_, err := run(t, src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: erro %v, esperava conter %q", src, err, want)
		}
	}
}

func TestStructs(t *testing.T) {
	src, err := os.ReadFile("exemplos/structs.djo")
	if err != nil {
		t.Fatal(err)
	}
	out, err := run(t, string(src))
	if err != nil {
		t.Fatal(err)
	}
	want := "/post?id=7\nfalse\ntrue\nPost(id: 0, titulo: \"\", conteudo: \"só texto\", publicado: true)\nNovo título\ncarro, veículo ABC-123\ncarro, veículo ABC-123\ntrue\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestStructSemChavesEOutrosRecursos(t *testing.T) {
	out, err := run(t, `
struct Autor
    nome[String] = "anônimo"

struct Artigo
    id[Int] nocall = 100
    autor[Autor]
    tags[Arr] = []
    vistas[Int]

    f ver
        self.vistas++
        return self.vistas

    f dobro
        self.ver:
        return self.ver:

var a = Artigo:.
var b = Artigo:(Autor:.)
print a.id
print a.autor == nil
print b.autor.nome
push:a.tags:"x"
print len:a.tags
print len:b.tags
print a.dobro:
print a.vistas

f nomeDe
    params[x[Artigo]]
    return x.id
print nomeDe:b
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "100\ntrue\nanônimo\n1\n0\n2\n2\n100\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestErrosDeStruct(t *testing.T) {
	base := "struct P{\n  id[Int] nocall\n  nome[String]\n  f oi\n    return 1\n}\nvar p = P:\"a\"\n"
	casos := map[string]string{
		base + "var q = P:5":           "o campo 'nome' de P deve ser String, mas recebeu Int",
		base + "var q = P:\"a\":\"b\"": "aceita no máximo 1 argumento",
		base + "p.nome = 3":            "'nome' é String e não pode receber Int",
		base + "print p.idade":         "P não tem o campo 'idade'",
		base + "print p.oi":            "é um método de P",
		base + "p.nome:":               "é um campo de P",
		base + "p.xyz:":                "P não tem o método 'xyz'",
		base + "var n = 5\nprint n.x":  "só dá para acessar o campo",
		base + "f soma\n  params[a, b]\n  return 1\nsoma:.:1": "só vale ao criar uma struct",
		"struct post{\n x[Int]\n}":                            "começam com letra maiúscula",
		"struct P{\n x[Int]\n x[Int]\n}":                      "aparece duas vezes",
		"struct P{\n x[Int]\n f x\n  return 1\n}":             "campo e método",
		"struct P{\n print 1\n}":                              "só cabem campos",
		"struct P{\n x[Coisa]\n}\nvar a = P:.":                "usa o tipo 'Coisa'",
		"struct P{\n x[Int] = \"a\"\n}\nvar a = P:.":          "deve ser Int",
	}
	for src, want := range casos {
		_, err := run(t, src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: erro %v, esperava conter %q", src, err, want)
		}
	}
}

func TestComposicao(t *testing.T) {
	out, err := run(t, `struct Autor{
    nome[String]
    f assinatura
        return "por " + self.nome
}
struct Post{
    Autor
    titulo[String]
    f resumo
        return self.titulo + " " + self.assinatura:
}
var p = Post:.:"Oi"
p.nome = "Ana"
print p.resumo:
print p.Autor.nome
var a = Autor:"Beto"
var q = Post:a:"Tchau"
a.nome = "Caio"
print q.assinatura:
print q.nome
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "Oi por Ana\nAna\npor Caio\nCaio\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	if _, err := run(t, "struct A{\n B\n}\nvar a = A:\nprint a.x"); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestHeranca(t *testing.T) {
	out, err := run(t, `struct Motor{
    cv[Int] = 100
}
struct Vehicle{
    placa[String]
    rodas[Int] = 4
    f descrever
        return "veículo " + self.placa
}
struct Car sonof Vehicle{
    Motor
    portas[Int] = 4
    f descrever
        return "carro " + (super.descrever:) + " com " + (str:self.portas) + " portas"
}
struct Taxi sonof Car{
    f descrever
        return "táxi/" + (super.descrever:)
}
f mostra
    params[v[Vehicle]]
    return v.descrever:
var c = Car:"ABC":4:.:2
print c.descrever:
print c.cv
print mostra:c
print mostra:(Taxi:"T1")
print mostra:(Vehicle:"V")
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "carro veículo ABC com 2 portas\n100\ncarro veículo ABC com 2 portas\ntáxi/carro veículo T1 com 4 portas\nveículo V\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	for _, bad := range []string{
		"struct A sonof B{\n}\nvar a = A:",
		"struct A sonof B{\n}\nstruct B sonof A{\n}\nvar a = A:",
		"struct B{\n x[Int]\n}\nstruct A sonof B{\n x[Int]\n}\nvar a = A:",
		"struct B{\n}\nstruct A sonof B{\n}\nf g\n params[a[A]]\n return 1\nprint g:(B:)",
		"print super.x:",
	} {
		if _, err := run(t, bad); err == nil {
			t.Fatalf("esperava erro em %q", bad)
		}
	}
}

func TestMaps(t *testing.T) {
	out, err := run(t, `map m = {"nome": "Ana", "idade": 30}
m["cidade"] = "Rio"
m["idade"] = m["idade"] + 1
print m["idade"]
print m["nada"] ?? "vazio"
print len:m
print has:m:"nome"
print del:m:"nome"
print keys:m
print values:m
var vazio = {}
print vazio
var n = {1: "um", 2: "dois"}
print n[2]
var outro = m
outro["x"] = true
print m
f muda
    params[c[Map]]
    c["k"] = 1
muda:vazio
print vazio
var tabela = {
    "a": [1, 2],
    "b": {"c": 3}
}
print tabela["b"]["c"]
print tabela["a"][1]
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "31\nvazio\n3\ntrue\ntrue\n[\"idade\", \"cidade\"]\n[31, \"Rio\"]\n{}\ndois\n{\"idade\": 31, \"cidade\": \"Rio\", \"x\": true}\n{\"k\": 1}\n3\n2\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	for _, bad := range []string{`map m = [1]`, `var m = {1.5: 2}`, `map m = {}
m = 5`, `print keys:3`, `var x = {"a" 1}`} {
		if _, err := run(t, bad); err == nil {
			t.Fatalf("esperava erro em %q", bad)
		}
	}
}

func TestJson(t *testing.T) {
	out, err := run(t, `struct P{
    nome[String]
    n[Int]
}
map m = {"a": [1, 2.5, true, nil], "b": {"c": "x\"y"}, 3: "três"}
print Json.encode:m
print Json.encode:(P:"Ana":2)
var d = Json.decode:"{\"z\": 1, \"a\": [1, 2, {\"k\": null}], \"f\": 1.5, \"s\": \"é\"}"
print d["z"] + 1
print d["a"][2]
print d["f"]
print keys:d
print Json.decode:"quebrado" ?? "nil"
print Json.valid:"{}"
print Json.valid:"{"
print Json.encode:(Json.decode:"[1,{\"a\":2}]")
print Json.pretty:{"a": [1], "b": {}}
`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[1,2.5,true,null],"b":{"c":"x\"y"},"3":"três"}
{"nome":"Ana","n":2}
2
{"k": nil}
1.5
["z", "a", "f", "s"]
nil
true
false
[1,{"a":2}]
{
  "a": [
    1
  ],
  "b": {}
}
`
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	for _, bad := range []string{`print Json.nada:1`, `print Json.encode:1:2`, `print Json.decode:5`} {
		if _, err := run(t, bad); err == nil {
			t.Fatalf("esperava erro em %q", bad)
		}
	}
}

func TestFor(t *testing.T) {
	out, err := run(t, `for{
    each[x in [10, 20]]
    print x
}
map m = {"a": 1, "b": 2}
for
    each[k, v in m]
    print k + str:v
for{ each[k in m] print k }
for{ each[i, c in "oi"] print (str:i) + c }
for{
    each[i in range:0:3]
    if
        cond[i != 1]
        print i
}
f acha
    params[l]
    for{
        each[x in l]
        if{ cond[x > 1] return x }
    }
    return nil
print acha:[1, 5, 9]
var soma = 0
for{ each[n in range:1:5] soma = soma + n }
print soma
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "10\n20\na1\nb2\na\nb\n0o\n1i\n0\n2\n5\n10\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	for _, bad := range []string{"for{ each[x in 5] print x }", "for{ print 1 }", "for{ each[x of [1]] print x }", "print range:0:\"a\""} {
		if _, err := run(t, bad); err == nil {
			t.Fatalf("esperava erro em %q", bad)
		}
	}
}

func TestInterfaces(t *testing.T) {
	out, err := run(t, `interface Pagavel{
    f preco
    f desconto
        params[pct[Int]]
}
struct Livro{
    titulo[String]
    base[Int]
    f preco
        return self.base
    f desconto
        params[pct[Int]]
        return self.base - self.base * pct / 100
}
struct Base{
    base[Int] = 50
    f preco
        return self.base
    f desconto
        params[pct[Int]]
        return 0
}
struct Combo{
    Base
    nome[String]
}
struct Foto{
    nome[String]
}
struct Errado{
    f preco
        return 1
    f desconto
        params[x]
        return 1
}
struct Carrinho{
    item[Pagavel]
}
f total
    params[p[Pagavel]]
    return p.preco:
print total:(Livro:"A":100)
print total:(Combo:)
print (Livro:"A":200).desconto:10
var c = Carrinho:(Livro:"B":30)
print c.item.preco:
var vazio = Carrinho:
print vazio.item
print is:(Livro:"A":1):"Pagavel"
print is:(Foto:"x"):"Pagavel"
print is:3:"Int"
print is:(Combo:):"Base"
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "100\n50\n180\n30\nnil\ntrue\nfalse\ntrue\nfalse\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	pre := "interface P{\n f preco\n}\nstruct F{\n n[String]\n}\nstruct E{\n f preco\n  params[x]\n  return 1\n}\nf t\n params[p[P]]\n return 1\n"
	for _, c := range []struct{ src, msg string }{
		{pre + "print t:(F:\"a\")", "F não tem o método 'preco'"},
		{pre + "print t:(E:)", "parâmetros diferentes"},
		{pre + "print t:5", "deve ser P"},
		{"interface P{\n f x\n  print 1\n}", "não pode ter corpo"},
		{"interface P{\n n[Int]\n}", "só cabem métodos"},
		{"struct P{\n}\ninterface P{\n f x\n}", "já existe"},
	} {
		_, err := run(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Fatalf("esperava erro com %q em %q, veio %v", c.msg, c.src, err)
		}
	}
}
