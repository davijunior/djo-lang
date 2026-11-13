package main

import "testing"

func TestTexto(t *testing.T) {
	out, err := run(t, `print upper:"olá"
print lower:"OLÁ Mundo"
print replace:"a-b-c":"-":"+"
print starts:"djo.html":"djo"
print ends:"djo.html":".html"
print contains:"banana":"nan"
print contains:[1, "a", 3]:"a"
print contains:[1, 2]:5
print index:"ação":"o"
print index:"abc":"z"
print index:["a", "b"]:"b"
print slice:"olá mundo":0:3
print slice:"olá mundo":4
print slice:"abc":2:99
print slice:"abc":5:1
print slice:[1, 2, 3, 4]:1:3
print repeat:"ab":3
print padleft:"7":3:"0"
print padright:"ab":4:"."
print padleft:"abcdef":3:"0"
print slug:"  Olá, Mundo! Ação nº 1 — Çedilha  "
print slug:"!!!"
print urlencode:"a b&c=ç"
print urldecode:"a+b%26c"
print urldecode:"%zz" ?? "inválido"
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "OLÁ\nolá mundo\na+b+c\ntrue\ntrue\ntrue\ntrue\nfalse\n3\n-1\n1\nolá\nmundo\nc\n\n[2, 3]\nababab\n007\nab..\nabcdef\nola-mundo-acao-n-1-cedilha\n\na+b%26c%3D%C3%A7\na b&c\ninválido\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
}

func TestConversaoENumeros(t *testing.T) {
	out, err := run(t, `print int:"42"
print int:" 7 "
print int:"3.5" ?? "nil"
print int:"abc" ?? "nil"
print int:3.9
print int:true ?? "nil"
print float:"2.5"
print float:"1e3"
print float:"inf" ?? "nil"
print float:"0x10" ?? "nil"
print float:3
print (int:"5") + 1
print abs:-4
print abs:-2.5
print floor:2.7
print floor:-2.1
print ceil:2.1
print round:2.5
print round:3.14159:2
print round:2:1
print min:3:7
print max:3:7.5
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "42\n7\nnil\nnil\n3\nnil\n2.5\n1000.0\nnil\nnil\n3.0\n6\n4\n2.5\n2\n-3\n3\n3\n3.14\n2.0\n3\n7.5\n"
	if out != want {
		t.Fatalf("saída %q, esperado %q", out, want)
	}
	for _, bad := range []string{`print upper:5`, `print replace:"a":"":"b"`, `print split:"a":""`, `print repeat:"a":-1`, `print repeat:"abc":99999999`, `print padleft:"a":3:"ab"`, `print slice:5:1`, `print round:1.5:20`, `print abs:"x"`, `print min:"a":1`, `print slice:"abc"`, `print max:"2":1`} {
		if _, err := run(t, bad); err == nil {
			t.Errorf("esperava erro em %q", bad)
		}
	}
}
