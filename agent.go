//go:build !nonet

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// djo agent: vê um site pelos olhos de um agente.
//
//	djo agent localhost:8080                    o manifesto, em texto legível
//	djo agent localhost:8080/posts              a resposta de agente dessa rota
//	djo agent -post '{"texto":"oi"}' host/posts/2/comentarios    chama uma action
//	djo agent -raw host                         o JSON como veio

const maxAgentBody = 4 << 20

func agentCmd(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(errw)
	post := fs.String("post", "", "chama a rota com POST e este corpo JSON (para actions)")
	raw := fs.Bool("raw", false, "mostra o JSON sem formatar")
	timeout := fs.Duration("timeout", 15*time.Second, "tempo máximo")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errw, "uso: djo agent [-raw] [-post '{...}'] endereço\n  sem caminho (ou com /): mostra o manifesto\n  com caminho: mostra a resposta de agente da rota")
		return 2
	}
	target := fs.Arg(0)
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		fmt.Fprintf(errw, "endereço inválido: %s\n", fs.Arg(0))
		return 2
	}
	client := &http.Client{Timeout: *timeout}

	if *post == "" && (u.Path == "" || u.Path == "/") {
		return showManifest(client, u, *raw, out, errw)
	}
	method := http.MethodGet
	var body io.Reader
	if *post != "" {
		method, body = http.MethodPost, strings.NewReader(*post)
	}
	req, _ := http.NewRequest(method, u.String(), body)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, data, err := doAgent(client, req)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	fmt.Fprintf(out, "%s %s  (Accept: application/json)\n→ %s", method, u.String(), res.Status)
	if ct := res.Header.Get("Content-Type"); ct != "" {
		fmt.Fprintf(out, "  %s", ct)
	}
	fmt.Fprintln(out)
	if l := res.Header.Get("Link"); l != "" {
		fmt.Fprintf(out, "  Link: %s\n", l)
	}
	if res.StatusCode == http.StatusNotAcceptable {
		fmt.Fprintln(out, "  esta rota não tem resposta de agente (só tela). Declare \"agent\" no rotas.djo.")
	}
	fmt.Fprintln(out)
	printBody(out, data, res.Header.Get("Content-Type"), *raw)
	if res.StatusCode >= 400 {
		return 1
	}
	return 0
}

func doAgent(c *http.Client, req *http.Request) (*http.Response, []byte, error) {
	res, err := c.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("não consegui falar com %s: %v", req.URL.Host, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxAgentBody))
	if err != nil {
		return nil, nil, err
	}
	return res, data, nil
}

func printBody(out io.Writer, data []byte, ctype string, raw bool) {
	if strings.Contains(ctype, "json") {
		if !raw {
			var b bytes.Buffer
			if json.Indent(&b, data, "", "  ") == nil {
				data = b.Bytes()
			}
		}
		fmt.Fprintln(out, strings.TrimRight(string(data), "\n"))
		return
	}
	const max = 1500
	text := strings.TrimRight(string(data), "\n")
	if len(text) > max {
		text = text[:max] + fmt.Sprintf("\n… (%d bytes no total)", len(data))
	}
	fmt.Fprintln(out, text)
}

func showManifest(c *http.Client, u *url.URL, raw bool, out, errw io.Writer) int {
	mu := *u
	mu.Path, mu.RawQuery, mu.Fragment = manifestURLPath, "", ""
	req, _ := http.NewRequest(http.MethodGet, mu.String(), nil)
	req.Header.Set("Accept", "application/json")
	res, data, err := doAgent(c, req)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(errw, "%s respondeu %s: este site não publica manifesto.\n(precisa de rotas.djo com ao menos uma rota com \"agent\" ou uma action)\n", mu.String(), res.Status)
		return 1
	}
	if raw {
		fmt.Fprintln(out, strings.TrimRight(string(data), "\n"))
		return 0
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		fmt.Fprintf(errw, "o manifesto não é JSON válido: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "%s  (%v)\n", u.Scheme+"://"+u.Host, m["format"])
	if site, ok := m["site"].(map[string]any); ok {
		if n := str(site["nome"]); n != "" {
			fmt.Fprintf(out, "%s\n", n)
		}
		if a := str(site["about"]); a != "" {
			fmt.Fprintf(out, "%s\n", a)
		}
	}
	routes, _ := m["routes"].([]any)
	actions, _ := m["actions"].([]any)

	fmt.Fprintf(out, "\nLeituras (%d)  — peça com Accept: application/json\n", len(routes))
	for _, r := range routes {
		rm, _ := r.(map[string]any)
		fmt.Fprintf(out, "  %-6s %s\n", str(rm["method"]), str(rm["path"]))
		if d := str(rm["description"]); d != "" {
			fmt.Fprintf(out, "         %s\n", d)
		}
		printPathParams(out, rm)
		printReturns(out, rm)
		if q, _ := rm["query"].([]any); len(q) > 0 {
			for _, x := range q {
				switch str(x.(map[string]any)["name"]) {
				case "campos":
					fmt.Fprintln(out, "         aceita ?campos=a,b (devolve só esses campos)")
				case "tabela":
					fmt.Fprintln(out, "         aceita ?tabela (lista como CSV, bem menor)")
				}
			}
		}
	}
	fmt.Fprintf(out, "\nAções (%d)  — envie POST com corpo application/json\n", len(actions))
	for _, a := range actions {
		am, _ := a.(map[string]any)
		fmt.Fprintf(out, "  %s\n         %s %s\n", str(am["name"]), str(am["method"]), str(am["path"]))
		if d := str(am["description"]); d != "" {
			fmt.Fprintf(out, "         %s\n", d)
		}
		printPathParams(out, am)
		printReturns(out, am)
		in, _ := am["input"].(map[string]any)
		props, _ := in["properties"].(map[string]any)
		if len(props) == 0 {
			fmt.Fprintln(out, "         corpo: nenhum")
			continue
		}
		fmt.Fprintln(out, "         corpo:")
		for _, line := range schemaLines(in, true) {
			fmt.Fprintf(out, "           %s\n", line)
		}
	}
	fmt.Fprintf(out, "\nTeste: djo agent %s/<rota>   |   djo agent -post '{...}' %s/<rota da ação>\n", u.Host, u.Host)
	return 0
}

const manifestURLPath = "/.well-known/ai-manifest.json"

func str(v any) string {
	s, _ := v.(string)
	return s
}

// printReturns descreve o tipo da resposta (o "returns" do manifesto).
func printReturns(out io.Writer, m map[string]any) {
	r, _ := m["returns"].(map[string]any)
	if r == nil {
		return
	}
	item, label := r, ""
	if str(r["type"]) == "array" {
		if it, ok := r["items"].(map[string]any); ok {
			item, label = it, "lista de "
		}
	}
	name := str(item["title"])
	if name == "" {
		name = str(item["type"])
	}
	fmt.Fprintf(out, "         responde: %s%s\n", label, name)
	if item["properties"] != nil {
		for _, l := range schemaLines(item, false) {
			fmt.Fprintf(out, "           %s\n", l)
		}
	}
}

func printPathParams(out io.Writer, m map[string]any) {
	ps, _ := m["path_params"].([]any)
	for _, p := range ps {
		pm, _ := p.(map[string]any)
		fmt.Fprintf(out, "         na URL: %s (%s)\n", str(pm["name"]), str(pm["type"]))
	}
}

// schemaLines descreve as propriedades de um JSON Schema de objeto, uma por linha.
func schemaLines(s map[string]any, withReq bool) []string {
	props, _ := s["properties"].(map[string]any)
	required := map[string]bool{}
	if rs, ok := s["required"].([]any); ok {
		for _, r := range rs {
			required[str(r)] = true
		}
	}
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	var lines []string
	for _, n := range names {
		p, _ := props[n].(map[string]any)
		mark := "opcional"
		if required[n] {
			mark = "obrigatório"
		}
		if !withReq {
			mark = ""
		}
		typ := str(p["type"])
		if typ == "object" && p["properties"] != nil {
			typ = "objeto " + str(p["title"])
		}
		line := fmt.Sprintf("%s: %s", n, strings.TrimSpace(typ))
		if mark != "" {
			line += " (" + mark + ")"
		}
		lines = append(lines, line)
		if p["properties"] != nil {
			for _, l := range schemaLines(p, withReq) {
				lines = append(lines, "  "+l)
			}
		}
	}
	return lines
}

func agentMain(args []string) int { return agentCmd(args, os.Stdout, os.Stderr) }
