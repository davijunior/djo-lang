package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if zr, f, exe := openSelf(); zr != nil {
		os.Exit(runEmbedded(zr, f, exe, os.Args[1:])) // este executável é um app djo
	}
	if len(os.Args) >= 2 && os.Args[1] == "build" {
		os.Exit(buildCmd(os.Args[2:]))
	}
	if len(os.Args) >= 2 && os.Args[1] == "new" {
		os.Exit(newCmd(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) >= 2 && os.Args[1] == "agent" {
		os.Exit(agentMain(os.Args[2:]))
	}
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println(versionInfo())
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "serve" {
		os.Exit(serveCmd(os.Args[2:]))
	}
	if len(os.Args) > 2 || (len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help")) {
		fmt.Fprintln(os.Stderr, "uso: djo [arquivo.djo | arquivo.djo.html]\n     djo version\n     djo new [-modelo site|script|iot] pasta\n     djo build [-o app] [-runtime djo-de-outra-maquina] [pasta]\n     djo serve [-addr host:porta] [-dev] [pasta]\n     djo agent [-raw] [-post '{...}'] endereço\n  sem argumentos abre o REPL")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		os.Exit(runFile(os.Args[1]))
	}
	repl()
}

// version é carimbada nos releases (-ldflags "-X main.version=v0.1.0").
var version = "dev"

// versionInfo diz qual perfil de build é este binário.
func versionInfo() string {
	profile := "full"
	switch {
	case !hasWeb && !hasDB && !hasNet:
		profile = "nano"
	case !hasWeb && !hasDB:
		profile = "tiny"
	case !hasDB:
		profile = "lite"
	case !hasWeb:
		profile = "noweb"
	}
	yn := func(b bool) string {
		if b {
			return "sim"
		}
		return "não"
	}
	return fmt.Sprintf("djo %s (perfil %s) %s/%s — servidor web: %s, banco SQLite: %s, Http.get/post: %s", version, profile, runtime.GOOS, runtime.GOARCH, yn(hasWeb), yn(hasDB), yn(hasNet))
}

func runFile(path string) int {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var prog []Stmt
	if isTemplate(path) {
		prog, err = ParseTemplate(string(src))
	} else {
		prog, err = Parse(string(src))
	}
	if err == nil {
		in := NewInterp(os.Stdout)
		in.root = filepath.Dir(path)
		if err = in.loadCore(in.root); err == nil {
			err = in.Run(prog)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// REPL: uma linha por vez; começando com 'f', 'while', 'until', 'if' ou 'match', lê até uma linha em branco.
func repl() {
	in := NewInterp(os.Stdout)
	in.Echo = true
	fmt.Println("djo — digite código, ctrl+d para sair")
	for {
		fmt.Print("> ")
		src, ok := in.readLine()
		if !ok {
			fmt.Println()
			return
		}
		if multiline(src) {
			for {
				fmt.Print("… ")
				line, ok := in.readLine()
				if !ok || strings.TrimSpace(line) == "" {
					break
				}
				src += "\n" + line
			}
		}
		prog, err := Parse(src)
		if err == nil {
			err = in.Run(prog)
		}
		if err != nil {
			fmt.Println(err)
		}
	}
}

func multiline(src string) bool {
	w := strings.Fields(src)
	if len(w) == 0 {
		return false
	}
	switch strings.TrimRight(strings.SplitN(w[0], "{", 2)[0], " ") {
	case "f", "while", "until", "for", "if", "match", "struct", "interface", "action":
		return true
	}
	return false
}
