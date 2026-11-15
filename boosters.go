package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Boosters: código pronto, em djo, que o projeto chama pelo nome.
//
//	boosters/markdown/booster.json     nome, versão, descrição, permissões
//	boosters/markdown/main.djo         a API: só as f daqui o projeto enxerga
//	boosters/markdown/core/            o interno (f, struct, interface): só o booster enxerga
//	boosters.lock                      versão e SHA256 de cada booster instalado
//
// No código do projeto:  var r = markdown.render:texto
//
// A dependência é de mão única: o core e as páginas chamam os boosters; um
// booster nunca enxerga o core nem o projeto. Dentro dele só valem as funções
// do próprio booster e as funções nativas puras. Http, File, Db, Session, Serial
// e Route só funcionam se o booster pedir a permissão no booster.json.

type booster struct {
	name, version, desc string
	perms               map[string]bool
	funcs               map[string]*FuncDecl      // a API: funções dos .djo da raiz do booster
	priv                map[string]*FuncDecl      // funções do core/ do booster: só ele enxerga
	structs             map[string]*StructDecl    // structs do core/ (mundo próprio, sem colidir com o projeto)
	interfaces          map[string]*InterfaceDecl // interfaces do core/
}

type boosterManifest struct {
	Nome       string   `json:"nome"`
	Versao     string   `json:"versao"`
	Descricao  string   `json:"descricao"`
	Permissoes []string `json:"permissoes"`
}

type lockEntry struct {
	Versao string `json:"versao"`
	SHA256 string `json:"sha256"`
	Origem string `json:"origem,omitempty"` // de onde veio (endereço ou "local")
}

const (
	boosterMain = "main.djo" // a API do booster: só as f daqui o projeto enxerga
	boostersDir = "boosters"
	lockFile    = "boosters.lock"
	maxBooster  = 4 << 20 // bytes de um booster inteiro
)

var boosterName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// permissões que um booster precisa declarar; o resto (Json, Time, Hash...) é puro.
var restrictedNS = map[string]bool{"Http": true, "File": true, "Db": true, "Session": true, "Serial": true, "Route": true, "Env": true}

// checkPerm barra um booster que usa um namespace com efeito no mundo sem declarar.
func (in *Interp) checkPerm(ns string, line int) {
	if in.cur != nil && restrictedNS[ns] && !in.cur.perms[ns] {
		fail(line, "o booster '%s' usa %s mas não declarou essa permissão no booster.json", in.cur.name, ns)
	}
}

func boosterFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s é um link simbólico; boosters não podem ter links", p)
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// hashBooster resume a pasta inteira (nomes e conteúdos) num SHA256.
func hashBooster(dir string) (string, error) {
	files, err := boosterFiles(dir)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var total int64
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		if total += int64(len(data)); total > maxBooster {
			return "", fmt.Errorf("booster grande demais (limite %d MB)", maxBooster>>20)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readBoosterManifest(dir string) (*boosterManifest, error) {
	if fi, err := os.Stat(filepath.Join(dir, boosterMain)); err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("falta o %s (a API do booster: as f que o projeto pode chamar)", boosterMain)
	}
	data, err := os.ReadFile(filepath.Join(dir, "booster.json"))
	if err != nil {
		return nil, fmt.Errorf("falta o booster.json")
	}
	var m boosterManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("booster.json inválido: %v", err)
	}
	if !boosterName.MatchString(m.Nome) {
		return nil, fmt.Errorf("booster.json: nome '%s' inválido (minúsculas, números e _; começa com letra)", m.Nome)
	}
	if strings.TrimSpace(m.Versao) == "" {
		return nil, fmt.Errorf("booster.json: falta a versão")
	}
	for _, p := range m.Permissoes {
		if !restrictedNS[p] {
			return nil, fmt.Errorf("booster.json: permissão '%s' desconhecida (valem: Http, File, Db, Session, Serial, Route, Env)", p)
		}
	}
	return &m, nil
}

// loadBoosters registra as funções de root/boosters/*/ no interpretador.
func (in *Interp) loadBoosters(root string) error {
	base := filepath.Join(root, boostersDir)
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil // projeto sem boosters
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		m, err := readBoosterManifest(dir)
		if err != nil {
			return &djoError{0, boostersDir + "/" + e.Name() + ": " + err.Error()}
		}
		if m.Nome != e.Name() {
			return &djoError{0, fmt.Sprintf("%s/%s: o booster.json diz '%s', mas a pasta se chama '%s'", boostersDir, e.Name(), m.Nome, e.Name())}
		}
		b := &booster{name: m.Nome, version: m.Versao, desc: m.Descricao, perms: map[string]bool{},
			funcs: map[string]*FuncDecl{}, priv: map[string]*FuncDecl{},
			structs: map[string]*StructDecl{}, interfaces: map[string]*InterfaceDecl{}}
		for _, p := range m.Permissoes {
			b.perms[p] = true
		}
		files, err := boosterFiles(dir)
		if err != nil {
			return &djoError{0, boostersDir + "/" + e.Name() + ": " + err.Error()}
		}
		if err := in.loadBoosterFiles(root, dir, files, b); err != nil {
			return err
		}
		if in.boosters == nil {
			in.boosters = map[string]*booster{}
		}
		in.boosters[b.name] = b
	}
	return nil
}

func readLock(root string) (map[string]lockEntry, error) {
	data, err := os.ReadFile(filepath.Join(root, lockFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lock := map[string]lockEntry{}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("%s inválido: %v", lockFile, err)
	}
	return lock, nil
}

func writeLock(root string, lock map[string]lockEntry) error {
	if len(lock) == 0 {
		err := os.Remove(filepath.Join(root, lockFile))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	data, _ := json.MarshalIndent(lock, "", "  ")
	return os.WriteFile(filepath.Join(root, lockFile), append(data, '\n'), 0o644)
}

// verifyBoosters confere cada booster instalado com o boosters.lock. Sem lock, não confere.
func verifyBoosters(root string) []string {
	lock, err := readLock(root)
	if err != nil {
		return []string{err.Error()}
	}
	if lock == nil {
		return nil
	}
	var problems []string
	entries, _ := os.ReadDir(filepath.Join(root, boostersDir))
	seen := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		seen[e.Name()] = true
		want, ok := lock[e.Name()]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s/%s não está no %s (use: djo booster add)", boostersDir, e.Name(), lockFile))
			continue
		}
		got, err := hashBooster(filepath.Join(root, boostersDir, e.Name()))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s/%s: %v", boostersDir, e.Name(), err))
		} else if got != want.SHA256 {
			problems = append(problems, fmt.Sprintf("%s/%s foi alterado: o SHA256 não bate com o %s", boostersDir, e.Name(), lockFile))
		}
	}
	for n := range lock {
		if !seen[n] {
			problems = append(problems, fmt.Sprintf("o %s lista '%s', mas a pasta %s/%s não existe", lockFile, n, boostersDir, n))
		}
	}
	sort.Strings(problems)
	return problems
}

// ---------- djo booster ----------

const boosterUsage = `uso: djo booster list                  lista os boosters do projeto (pasta atual)
     djo booster search [texto]        procura no registro (índice de boosters)
     djo booster add nome[@versao]     baixa do registro e instala
     djo booster add endereço -sha256 X   baixa de um endereço https e instala
     djo booster add pasta             instala o booster que está em "pasta"
     djo booster create [-perm Http,File] nome   cria a estrutura básica de um booster novo
     djo booster pack [-registro pasta] pasta    empacota um booster (.tar.gz) para publicar
     djo booster remove nome           remove um booster
     djo booster verify                confere as pastas com o boosters.lock
  -registro endereço (ou DJO_REGISTRO) muda o registro; o padrão é ` + defaultRegURL

// parseMixed aceita flags antes ou depois dos argumentos ("add nome -registro x").
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func boosterCmd(args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errw, boosterUsage)
		return 2
	}
	root, _ := os.Getwd()
	switch args[0] {
	case "list":
		return boosterList(root, out, errw)
	case "create":
		return boosterCreate(root, args[1:], out, errw)
	case "add":
		return boosterAddCmd(root, args[1:], out, errw)
	case "search":
		return boosterSearch(args[1:], out, errw)
	case "pack":
		return boosterPack(args[1:], out, errw)
	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(errw, boosterUsage)
			return 2
		}
		return boosterRemove(root, args[1], out, errw)
	case "verify":
		if p := verifyBoosters(root); len(p) > 0 {
			for _, x := range p {
				fmt.Fprintln(errw, x)
			}
			return 1
		}
		fmt.Fprintln(out, "ok: os boosters batem com o "+lockFile)
		return 0
	}
	fmt.Fprintln(errw, boosterUsage)
	return 2
}

func boosterList(root string, out, errw io.Writer) int {
	entries, _ := os.ReadDir(filepath.Join(root, boostersDir))
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := readBoosterManifest(filepath.Join(root, boostersDir, e.Name()))
		if err != nil {
			fmt.Fprintf(errw, "%s: %v\n", e.Name(), err)
			continue
		}
		n++
		perm := "nenhuma permissão"
		if len(m.Permissoes) > 0 {
			perm = "usa " + strings.Join(m.Permissoes, ", ")
		}
		fmt.Fprintf(out, "%s %s  (%s)\n  %s\n", m.Nome, m.Versao, perm, m.Descricao)
	}
	if n == 0 {
		fmt.Fprintln(out, "nenhum booster instalado (veja: djo booster add)")
	}
	return 0
}

func boosterAdd(root, src, origem string, out, errw io.Writer) int {
	m, err := readBoosterManifest(src)
	if err != nil {
		fmt.Fprintf(errw, "%s: %v\n", src, err)
		return 1
	}
	// confere antes de instalar: o código tem que ser só funções que parseiam
	if err := checkBoosterSource(src); err != nil {
		fmt.Fprintf(errw, "%s: %v\n", src, err)
		return 1
	}
	dest := filepath.Join(root, boostersDir, m.Nome)
	if _, err := os.Stat(dest); err == nil {
		fmt.Fprintf(errw, "o booster '%s' já está instalado; remova antes (djo booster remove %s)\n", m.Nome, m.Nome)
		return 1
	}
	if _, err := hashBooster(src); err != nil {
		fmt.Fprintf(errw, "%s: %v\n", src, err)
		return 1
	}
	if err := copyTree(src, dest); err != nil {
		os.RemoveAll(dest)
		fmt.Fprintf(errw, "não consegui copiar: %v\n", err)
		return 1
	}
	sum, err := hashBooster(dest)
	if err != nil {
		os.RemoveAll(dest)
		fmt.Fprintln(errw, err)
		return 1
	}
	lock, err := readLock(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if lock == nil {
		lock = map[string]lockEntry{}
	}
	lock[m.Nome] = lockEntry{m.Versao, sum, origem}
	if err := writeLock(root, lock); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	fmt.Fprintf(out, "booster %s %s instalado em %s/%s\n", m.Nome, m.Versao, boostersDir, m.Nome)
	if len(m.Permissoes) > 0 {
		fmt.Fprintf(out, "atenção: ele pede permissão para usar %s\n", strings.Join(m.Permissoes, ", "))
	}
	fmt.Fprintf(out, "use assim: %s.nome_da_funcao:argumento\n", m.Nome)
	return 0
}

func checkBoosterSource(dir string) error {
	files, err := boosterFiles(dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		if !strings.HasSuffix(f, ".djo") {
			continue
		}
		prog, err := parseFile(f)
		if err != nil {
			return err
		}
		internal := isBoosterCore(dir, f)
		rel, _ := filepath.Rel(dir, f)
		if !internal && filepath.ToSlash(rel) != boosterMain {
			return fmt.Errorf("%s: fora do core/ só existe o %s", filepath.ToSlash(rel), boosterMain)
		}
		for _, s := range prog {
			switch s.(type) {
			case *FuncDecl:
			case *StructDecl, *InterfaceDecl:
				if !internal {
					return fmt.Errorf("%s: structs e interfaces ficam no core/ do booster", filepath.ToSlash(rel))
				}
			default:
				return fmt.Errorf("%s: um booster só declara funções, structs e interfaces (código solto não vale)", filepath.ToSlash(rel))
			}
		}
	}
	return nil
}

func boosterRemove(root, name string, out, errw io.Writer) int {
	if !boosterName.MatchString(name) {
		fmt.Fprintf(errw, "nome inválido: %s\n", name)
		return 2
	}
	dest := filepath.Join(root, boostersDir, name)
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		fmt.Fprintf(errw, "o booster '%s' não está instalado\n", name)
		return 1
	}
	if err := os.RemoveAll(dest); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if lock, err := readLock(root); err == nil && lock != nil {
		delete(lock, name)
		if err := writeLock(root, lock); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
	}
	fmt.Fprintf(out, "booster %s removido\n", name)
	return 0
}

func copyTree(src, dst string) error {
	files, err := boosterFiles(src)
	if err != nil {
		return err
	}
	for _, f := range files {
		rel, _ := filepath.Rel(src, f)
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(to, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// djo booster create: a estrutura básica de um booster, numa pasta nova com o nome dele.
func boosterCreate(root string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("booster create", flag.ContinueOnError)
	fs.SetOutput(errw)
	perm := fs.String("perm", "", "permissões que o booster pede, separadas por vírgula (Http, File, Db, Session, Serial, Route, Env)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(errw, boosterUsage)
		return 2
	}
	nome := pos[0]
	if !boosterName.MatchString(nome) {
		fmt.Fprintf(errw, "nome inválido: %s (minúsculas, números e _; começa com letra; até 32 letras)\n", nome)
		return 2
	}
	var perms []string
	for _, p := range strings.Split(*perm, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if !restrictedNS[p] {
			fmt.Fprintf(errw, "permissão '%s' desconhecida (valem: Http, File, Db, Session, Serial, Route, Env)\n", p)
			return 2
		}
		perms = append(perms, p)
	}
	dir := filepath.Join(root, nome)
	if _, err := os.Stat(dir); err == nil {
		fmt.Fprintf(errw, "%s já existe; escolha outro nome ou apague a pasta\n", nome)
		return 1
	}
	permJSON, _ := json.Marshal(perms)
	if perms == nil {
		permJSON = []byte("[]")
	}
	files := map[string]string{
		"booster.json":  fmt.Sprintf("{\n  \"nome\": %q,\n  \"versao\": \"0.1.0\",\n  \"descricao\": \"Diga numa frase o que este booster faz.\",\n  \"permissoes\": %s\n}\n", nome, permJSON),
		"main.djo":      "// A API do booster: só as funções (f) deste arquivo são visíveis para o projeto,\n// como " + nome + ".ola. O trabalho de verdade fica em core/, que só o booster enxerga.\n\nf ola\n    params[quem[String]]\n    return saudacao:quem\n",
		"core/base.djo": "// O interno do booster: funções, structs e interfaces que o projeto não alcança.\n\nf saudacao\n    params[quem[String]]\n    return \"Olá, \" + quem + \"!\"\n",
		"README.md":     "# " + nome + "\n\nDiga aqui o que este booster faz.\n\n    djo booster add " + nome + "\n\n    print " + nome + ".ola:\"mundo\"      // Olá, mundo!\n",
	}
	for n, c := range files {
		to := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err == nil {
			err = os.WriteFile(to, []byte(c), 0o644)
		}
		if _, err := os.Stat(to); err != nil {
			fmt.Fprintf(errw, "não consegui criar %s\n", to)
			return 1
		}
	}
	fmt.Fprintf(out, "booster %s criado em %s/\n  %s/booster.json    nome, versão, descrição e permissões\n  %s/main.djo        a API: as f que o projeto pode chamar\n  %s/core/           o interno, que só o booster enxerga\n\nPara testar num projeto:  djo booster add %s\n", nome, nome, nome, nome, nome, nome)
	if len(perms) > 0 {
		fmt.Fprintf(out, "ele pede permissão para usar %s\n", strings.Join(perms, ", "))
	}
	return 0
}

// isBoosterCore diz se o arquivo está em core/ (o mundo interno do booster).
func isBoosterCore(dir, file string) bool {
	rel, _ := filepath.Rel(dir, file)
	return strings.HasPrefix(filepath.ToSlash(rel), "core/")
}

// loadBoosterFiles lê os .djo do booster: os da raiz são a API (só funções); os de
// core/ são internos (funções, structs e interfaces) e ficam num mundo próprio.
func (in *Interp) loadBoosterFiles(root, dir string, files []string, b *booster) (err error) {
	oldS, oldI := in.structs, in.interfaces
	in.structs, in.interfaces = b.structs, b.interfaces // os tipos do booster não vão para o projeto
	defer func() {
		in.structs, in.interfaces = oldS, oldI
		if r := recover(); r != nil {
			de, ok := r.(*djoError)
			if !ok {
				panic(r)
			}
			err = &djoError{de.Line, boostersDir + "/" + b.name + ": " + de.Msg}
		}
	}()
	for _, f := range files {
		if !strings.HasSuffix(f, ".djo") {
			continue
		}
		prog, err := loadProgram(f)
		if err != nil {
			return coreError(root, f, err)
		}
		internal := isBoosterCore(dir, f)
		if rel, _ := filepath.Rel(dir, f); !internal && filepath.ToSlash(rel) != boosterMain {
			return coreError(root, f, &djoError{0, "fora do core/ só existe o " + boosterMain + " (a API); mova este arquivo para o core/"})
		}
		for _, s := range prog {
			switch d := s.(type) {
			case *FuncDecl:
				target := b.funcs
				if internal {
					target = b.priv
				}
				if _, dup := b.funcs[d.Name]; dup {
					return coreError(root, f, &djoError{d.Line, "a função '" + d.Name + "' já existe neste booster"})
				}
				if _, dup := b.priv[d.Name]; dup {
					return coreError(root, f, &djoError{d.Line, "a função '" + d.Name + "' já existe neste booster"})
				}
				c := *d // a função vem do cache: não altere a original
				c.Booster = b
				target[d.Name] = &c
			case *StructDecl:
				if !internal {
					return coreError(root, f, &djoError{d.Line, "structs ficam no core/ do booster; fora dele só valem funções (f)"})
				}
				in.declare(d.Name, d.Line, d, nil)
				for _, m := range d.Methods { // métodos também rodam dentro do booster
					m.Booster = b
				}
			case *InterfaceDecl:
				if !internal {
					return coreError(root, f, &djoError{d.Line, "interfaces ficam no core/ do booster; fora dele só valem funções (f)"})
				}
				in.declare(d.Name, d.Line, nil, d)
			default:
				return coreError(root, f, &djoError{lineOf(s), "um booster só declara f, struct e interface (estas só no core/); código solto não é permitido"})
			}
		}
	}
	return nil
}

// ---------- add, search e pack ----------

var specRe = regexp.MustCompile(`^([a-z][a-z0-9_]{0,31})(?:@([0-9A-Za-z.+-]{1,32}))?$`)

func registryURL(flagVal string) string {
	if flagVal != "" {
		return strings.TrimRight(flagVal, "/")
	}
	if e := os.Getenv("DJO_REGISTRO"); e != "" {
		return strings.TrimRight(e, "/")
	}
	return defaultRegURL
}

func boosterAddCmd(root string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("booster add", flag.ContinueOnError)
	fs.SetOutput(errw)
	reg := fs.String("registro", "", "endereço do registro (padrão: "+defaultRegURL+")")
	sha := fs.String("sha256", "", "SHA256 do .tar.gz (obrigatório ao baixar de um endereço)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(errw, boosterUsage)
		return 2
	}
	alvo := pos[0]
	switch {
	case strings.Contains(alvo, "://"):
		return addFromURL(root, alvo, strings.ToLower(strings.TrimSpace(*sha)), out, errw)
	default:
		if fi, err := os.Stat(alvo); err == nil && fi.IsDir() {
			return boosterAdd(root, alvo, "local", out, errw)
		}
		m := specRe.FindStringSubmatch(alvo)
		if m == nil {
			fmt.Fprintf(errw, "'%s' não é uma pasta, um endereço nem o nome de um booster (nome ou nome@versao)\n", alvo)
			return 2
		}
		return addFromRegistry(root, m[1], m[2], registryURL(*reg), out, errw)
	}
}

// installArchive confere o hash, extrai numa pasta temporária e instala como qualquer pasta.
func installArchive(root string, data []byte, wantSum, origem string, expect *boosterManifest, out, errw io.Writer) int {
	if got := sumHex(data); got != wantSum {
		fmt.Fprintf(errw, "o SHA256 do arquivo baixado não bate (veio %s, esperava %s); nada foi instalado\n", got, wantSum)
		return 1
	}
	tmp, err := os.MkdirTemp("", "djo-booster-")
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	if err := unpackBooster(data, tmp); err != nil {
		fmt.Fprintf(errw, "arquivo recusado: %v\n", err)
		return 1
	}
	if expect != nil {
		m, err := readBoosterManifest(tmp)
		if err != nil {
			fmt.Fprintf(errw, "arquivo recusado: %v\n", err)
			return 1
		}
		if m.Nome != expect.Nome || m.Versao != expect.Versao {
			fmt.Fprintf(errw, "o arquivo diz ser %s %s, mas o registro anunciou %s %s; nada foi instalado\n", m.Nome, m.Versao, expect.Nome, expect.Versao)
			return 1
		}
	}
	return boosterAdd(root, tmp, origem, out, errw)
}

func addFromURL(root, raw, sha string, out, errw io.Writer) int {
	if len(sha) != 64 {
		fmt.Fprintln(errw, "baixar de um endereço exige o SHA256 do arquivo, para saber que ele é o que você espera:\n  djo booster add "+raw+" -sha256 <64 letras e números>\n(o registro mostra esse valor; ao instalar pelo nome ele é conferido sozinho)")
		return 2
	}
	data, err := httpGetLimited(raw, maxArchive)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	return installArchive(root, data, sha, raw, nil, out, errw)
}

func fetchIndex(reg string) (*regIndex, string, error) {
	iu := reg + "/" + indexFile
	data, err := httpGetLimited(iu, 1<<20)
	if err != nil {
		return nil, "", err
	}
	ix, err := readIndex(data)
	return ix, iu, err
}

func addFromRegistry(root, nome, versao, reg string, out, errw io.Writer) int {
	ix, iu, err := fetchIndex(reg)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	b := ix.find(nome)
	if b == nil {
		fmt.Fprintf(errw, "o registro %s não tem o booster '%s' (veja: djo booster search)\n", reg, nome)
		return 1
	}
	if versao == "" {
		versao = b.Ultima
	}
	v := b.version(versao)
	if v == nil {
		var vs []string
		for _, x := range b.Versoes {
			vs = append(vs, x.Versao)
		}
		fmt.Fprintf(errw, "o booster %s não tem a versão '%s' (existem: %s)\n", nome, versao, strings.Join(vs, ", "))
		return 1
	}
	au, err := resolveURL(iu, v.URL)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	data, err := httpGetLimited(au, maxArchive)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	return installArchive(root, data, strings.ToLower(v.SHA256), au, &boosterManifest{Nome: nome, Versao: v.Versao}, out, errw)
}

func boosterSearch(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("booster search", flag.ContinueOnError)
	fs.SetOutput(errw)
	reg := fs.String("registro", "", "endereço do registro")
	pos, err := parseMixed(fs, args)
	if err != nil || len(pos) > 1 {
		fmt.Fprintln(errw, boosterUsage)
		return 2
	}
	ix, _, err := fetchIndex(registryURL(*reg))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	q := ""
	if len(pos) == 1 {
		q = strings.ToLower(pos[0])
	}
	n := 0
	for _, b := range ix.Boosters {
		if q != "" && !strings.Contains(strings.ToLower(b.Nome+" "+b.Descricao), q) {
			continue
		}
		n++
		perm := "sem permissões"
		if len(b.Permissoes) > 0 {
			perm = "usa " + strings.Join(b.Permissoes, ", ")
		}
		fmt.Fprintf(out, "%s %s  (%s)\n  %s\n", b.Nome, b.Ultima, perm, b.Descricao)
	}
	if n == 0 {
		fmt.Fprintln(out, "nenhum booster encontrado")
	}
	return 0
}

func boosterPack(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("booster pack", flag.ContinueOnError)
	fs.SetOutput(errw)
	reg := fs.String("registro", "", "pasta do registro: guarda o arquivo lá e atualiza o indice.json")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(errw, boosterUsage)
		return 2
	}
	data, m, err := packBooster(pos[0])
	if err != nil {
		fmt.Fprintf(errw, "%s: %v\n", pos[0], err)
		return 1
	}
	sum := sumHex(data)
	var to string
	if *reg != "" {
		if to, err = publishToRegistry(*reg, m, data); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
	} else {
		to = fmt.Sprintf("%s-%s.tar.gz", m.Nome, m.Versao)
		if err := os.WriteFile(to, data, 0o644); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
	}
	fmt.Fprintf(out, "%s (%d bytes)\nSHA256 %s\n", to, len(data), sum)
	if *reg != "" {
		fmt.Fprintf(out, "indice.json atualizado: %s %s\n", m.Nome, m.Versao)
	}
	return 0
}
