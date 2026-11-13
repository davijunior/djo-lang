package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// djo new: cria um projeto pronto para rodar.
//
//	djo new meusite                 um site com tela e face de agente (modelo "site")
//	djo new -modelo script ferr     um programa de um arquivo só
//	djo new -modelo iot sensor      um programa que lê um sensor na porta serial
//
// Os modelos vivem dentro do executável: criar um projeto não precisa de rede.

type modelo struct {
	about string
	files map[string]string // caminho -> conteúdo ("__NOME__" vira o nome do projeto)
	next  []string          // o que fazer depois (cada linha é um comando)
}

var nomeProjeto = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func modelos() map[string]modelo {
	return map[string]modelo{
		"site": {
			about: "site com tela, face de agente, manifesto e uma action",
			files: modeloSite,
			next:  []string{"cd __PASTA__", "djo serve -dev", "djo agent localhost:8080     # o site como um agente o vê"},
		},
		"script": {
			about: "um programa de um arquivo só",
			files: map[string]string{
				"principal.djo": `// __NOME__: rode com  djo principal.djo

f saudar
    params[nome[String]]
    return "olá, " + nome + "!"

var msg = saudar:"mundo"
print msg
`,
				"README.md": "# __NOME__\n\n    djo principal.djo\n",
			},
			next: []string{"cd __PASTA__", "djo principal.djo"},
		},
		"iot": {
			about: "lê um sensor na porta serial e guarda as leituras",
			files: map[string]string{
				"leitura.djo": `// __NOME__: lê um sensor ligado na serial (Arduino, ESP32...) e guarda num arquivo.
// O sensor manda uma linha por leitura, como: 23.5
//
// Rode:  djo leitura.djo     (ajuste a porta abaixo)
var porta = Serial.open:"/dev/ttyUSB0":9600
var lidas = 0

until{
    eng[i = 0 ; lidas == 10 ; i++]
    var linha = Serial.readline:porta:5000
    if
        cond[linha == nil]
        print "sem resposta do sensor"
    else
        var valor = float:linha
        if
            cond[valor != nil]
            var agora = Time.now:
            File.append:"leituras.csv":(str:agora) + "," + linha + "\n"
            lidas++
    Time.sleep:1000
}

Serial.close:porta
`,
				"README.md": "# __NOME__\n\nLê um sensor na serial e guarda em leituras.csv.\n\n    djo leitura.djo\n\nPara um executável único: djo build -o leitura .\n",
			},
			next: []string{"cd __PASTA__", "djo leitura.djo"},
		},
	}
}

func newCmd(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(errw)
	nome := fs.String("modelo", "site", "site, script ou iot")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	all := modelos()
	if fs.NArg() != 1 {
		var names []string
		for k, m := range all {
			names = append(names, fmt.Sprintf("  %-7s %s", k, m.about))
		}
		sort.Strings(names)
		fmt.Fprintln(errw, "uso: djo new [-modelo nome] pasta\nmodelos:\n"+strings.Join(names, "\n"))
		return 2
	}
	m, ok := all[*nome]
	if !ok {
		fmt.Fprintf(errw, "modelo '%s' não existe (use site, script ou iot)\n", *nome)
		return 2
	}
	dir := fs.Arg(0)
	proj := filepath.Base(filepath.Clean(dir))
	if !nomeProjeto.MatchString(proj) {
		fmt.Fprintf(errw, "'%s' não serve de nome de projeto: use letras, números, '-', '_' ou '.'\n", proj)
		return 2
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		fmt.Fprintf(errw, "a pasta '%s' já existe e não está vazia: nada foi criado\n", dir)
		return 1
	}
	var paths []string
	for p := range m.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		body := strings.ReplaceAll(m.files[p], "__NOME__", proj)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
	}
	fmt.Fprintf(out, "projeto '%s' criado em %s (modelo %s)\n\n", proj, dir, *nome)
	for _, p := range paths {
		fmt.Fprintf(out, "  %s\n", p)
	}
	fmt.Fprintln(out, "\npróximos passos:")
	for _, l := range m.next {
		fmt.Fprintf(out, "  %s\n", strings.ReplaceAll(l, "__PASTA__", dir))
	}
	return 0
}

var modeloSite = map[string]string{
	"rotas.djo": `// Rotas de __NOME__. Só existe o que está declarado aqui.
Route.site:{"nome": "__NOME__", "about": "Um site feito em djo"}
Route.get:"/":"paginas/inicio.djo.html"
Route.get:"/notas":{"html": "paginas/notas.djo.html", "agent": "agentes/notas.djo", "about": "Lista as notas", "returns": ["Nota"], "cache": 10}
Route.get:"/notas/{id}":{"html": "paginas/nota.djo.html", "agent": "agentes/nota.djo", "returns": "Nota"}
Route.action:"/notas/{id}/recados":"deixar_recado":"Recado"
Route.static:"/assets":"publico"
`,
	"core/notas.djo": `// A regra de negócio mora aqui; a tela e o agente só apresentam.
struct Nota
    id[Int]
    titulo[String]
    texto[String]

struct Recado
    id[Int]
    texto[String]

f todas
    var a = Nota:1:"Primeira nota":"Olá, mundo."
    var b = Nota:2:"Segunda nota":"Troque estes dados em core/notas.djo."
    return [a, b]

f achar
    params[n[Int]]
    var lista = todas:
    for{ each[x in lista]
        if{ cond[x.id == n] return x }
    }
    return nil

action deixar_recado
    "Deixa um recado numa nota"
    params[id[Int], texto[String]]
    if{ cond[achar:id == nil]
        Http.status:404
        return {"erro": "nota não encontrada"}
    }
    var chave = str:id
    File.append:"_dados/recados.txt":(chave + ": " + texto + "\n")
    Http.status:201
    return {"id": id, "texto": texto}
`,
	"paginas/inicio.djo.html": `<link rel="stylesheet" href="/assets/estilo.css">
<h1>__NOME__</h1>
<p>Um site feito em djo. <a href="/notas">ver as notas</a></p>
`,
	"paginas/notas.djo.html": `<link rel="stylesheet" href="/assets/estilo.css">
<h1>Notas</h1>
{% var lista = todas: %}
{% for{ each[n in lista] %}
  <p><a href="/notas/{{ n.id }}">{{ n.titulo }}</a></p>
{% } %}
`,
	"paginas/nota.djo.html": `<link rel="stylesheet" href="/assets/estilo.css">
{% var n = achar:(int:(param:"id")) %}
{% if{ cond[n == nil] %}{% status:404 %}<p>Nota não encontrada.</p>{% } %}
{% if{ cond[n != nil] %}<h1>{{ n.titulo }}</h1><p>{{ n.texto }}</p>{% } %}
`,
	"agentes/notas.djo": `var lista = todas:
Http.json:lista
`,
	"agentes/nota.djo": `var n = achar:(int:(param:"id"))
if{ cond[n == nil]
    Http.status:404
    Http.json:{"erro": "nota não encontrada"}
    return
}
Http.json:n
`,
	"publico/estilo.css": "body{font-family:sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem}\n",
	"README.md": `# __NOME__

Site em djo: cada rota tem uma face para pessoas (HTML) e uma para agentes (JSON).

    djo serve -dev          # http://localhost:8080
    djo agent localhost:8080          # o manifesto, como um agente o vê
    djo agent localhost:8080/notas    # a face de agente de /notas

Onde mexer:

- rotas.djo: as rotas e o que cada uma devolve (so existe o que esta declarado la)
- core/: a regra de negocio (structs, funcoes e actions); nunca e servido
- paginas/: a face humana (HTML com {{ }} e {% %})
- agentes/: a face de agente (JSON)
- publico/: arquivos estaticos, em /assets

Para um executavel unico: djo build -o __NOME__ .
`,
}
