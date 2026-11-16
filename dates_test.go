package main

import (
	"strings"
	"testing"
)

func TestDatas(t *testing.T) {
	src := `var d = Date.make:2025:1:31
print d
print d + 1
print d - 31
print d.year
print d.month
print d.day
print d.weekday
print d.format:"DD/MM/YYYY"
var e = Date.parse:"2025-03-30"
print e - d
print e > d
print e <= d
print d == Date.parse:"2025-01-31"
print d != e
print Date.parse:"2025-02-31" == nil
print Date.parse:"31/01/2025":"DD/MM/YYYY"
print Date.parse:"2025-1-5" == nil
print Date.today: > d
var t = DateTime.parse:"2025-01-31 14:05:00"
print t
print t.date
print t.unix
print t + 3600
print t - 60
print (t + 86400) - t
print t.hour
print t.format:"DD/MM/YYYY HH:mm"
print DateTime.parse:"2025-01-31T14:05:00-03:00"
print DateTime.parse:"2025-01-31T14:05:00.500Z"
print DateTime.parse:"31/01/2025 14:05":"DD/MM/YYYY HH:mm"
print DateTime.parse:"ontem" == nil
print DateTime.of:d
print DateTime.end_of:d
print DateTime.make:2025:1:31:14:5:0
print DateTime.from_unix:0
print Date.of:t
print (DateTime.end_of:d) > t
print Json.encode:{"d": d, "t": t}
`
	got, err := run(t, src)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"2025-01-31", "2025-02-01", "2024-12-31", "2025", "1", "31", "5", "31/01/2025",
		"58", "true", "false", "true", "true", "true", "2025-01-31", "true", "true",
		"2025-01-31T14:05:00Z", "2025-01-31", "1738332300", "2025-01-31T15:05:00Z", "2025-01-31T14:04:00Z", "86400", "14", "31/01/2025 14:05",
		"2025-01-31T17:05:00Z", "2025-01-31T14:05:00Z", "2025-01-31T14:05:00Z", "true",
		"2025-01-31T00:00:00Z", "2025-01-31T23:59:59Z", "2025-01-31T14:05:00Z", "1970-01-01T00:00:00Z", "2025-01-31", "true",
		`{"d":"2025-01-31","t":"2025-01-31T14:05:00Z"}`, "",
	}, "\n")
	if got != want {
		t.Errorf("saída:\n%s\nqueria:\n%s", got, want)
	}
}

func TestDatasErros(t *testing.T) {
	cases := map[string]string{
		`print Date.make:2025:1:31 < DateTime.now:`:         "não se misturam",
		`print Date.make:2025:1:31 == DateTime.now:`:        "não se misturam",
		`print (DateTime.now:) - (Date.make:2025:1:1)`:      "não se misturam",
		`print Date.make:2025:2:31`:                         "não é uma data que existe",
		`print Date.make:2025:13:1`:                         "não é uma data que existe",
		`print Date.make:0:1:1`:                             "não é uma data que existe",
		`print Date.make:2025:"1":1`:                        "números inteiros",
		`print DateTime.make:2025:1:1:25:0:0`:               "fora da faixa",
		`print DateTime.make:2025:1:1:1`:                    "espera (ano",
		`print (Date.make:2025:1:1) * 2`:                    "não dá para usar '*'",
		`print (Date.make:2025:1:1) + (Date.make:2025:1:2)`: "não dá para usar '+'",
		`print (Date.make:2025:1:1) + "a"`:                  "não dá para usar '+'",
		`print (Date.make:2025:1:1) < 5`:                    "não dá para usar '<'",
		`print (Date.make:2025:1:1).hour`:                   "Date não tem 'hour'",
		`print (Date.make:2025:1:1).format:"HH:mm"`:         "Date não tem hora",
		`print Date.parse:"01/02/2025":"HH:mm"`:             "Date não tem hora",
		`print (Date.make:2025:1:1).mes`:                    "Date não tem 'mes'",
		`print (Date.make:2025:1:1).somar:1`:                "não tem o método",
		`print (Date.make:9999:12:31) + 1`:                  "intervalo",
		`print Date.of:5`:                                   "Date ou DateTime",
		`print DateTime.of:(DateTime.now:)`:                 "espera um Date",
		`print Date.parse:5`:                                "String",
		`print DateTime.from_unix:"x"`:                      "Int",
	}
	for src, want := range cases {
		if _, err := run(t, src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: erro %v, queria %q", src, err, want)
		}
	}
}

func TestDatasEmStructs(t *testing.T) {
	got, err := run(t, `struct Post
    titulo[String]
    publicado[Date]
    criado[DateTime] = DateTime.make:2025:1:1
var p = Post:"Oi"
print p
print p.publicado == nil
p.publicado = Date.make:2025:3:1
print p
print Json.encode:p
p.publicado = nil
print p.publicado == nil
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "Post(titulo: \"Oi\", publicado: nil, criado: 2025-01-01T00:00:00Z)\ntrue\nPost(titulo: \"Oi\", publicado: 2025-03-01, criado: 2025-01-01T00:00:00Z)\n" +
		`{"titulo":"Oi","publicado":"2025-03-01","criado":"2025-01-01T00:00:00Z"}` + "\ntrue\n"
	if got != want {
		t.Errorf("saída:\n%s\nqueria:\n%s", got, want)
	}
	for _, src := range []string{
		"struct P\n    d[Date]\nvar p = P:5\n",
		"struct P\n    d[Date]\nvar p = P:(DateTime.now:)\n",
		"struct P\n    d[DateTime]\nvar p = P:(Date.today:)\n",
		"struct P\n    d[Date]\nvar p = P:nil\np.d = \"2025-01-01\"\n",
	} {
		if _, err := run(t, src); err == nil {
			t.Errorf("%q deveria dar erro", src)
		}
	}
}
