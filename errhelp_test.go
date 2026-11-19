package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runDjo roda um programa (com core/ opcional) e devolve o erro da execução ou da análise.
func runDjo(t *testing.T, core map[string]string, src string) error {
	t.Helper()
	root := t.TempDir()
	for name, c := range core {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	in := NewInterp(io.Discard)
	in.root = root
	if err := in.loadCore(root); err != nil {
		return err
	}
	prog, err := Parse(src)
	if err != nil {
		return err
	}
	return in.Run(prog)
}

func TestErrosComunsTemCodigoEDica(t *testing.T) {
	cases := []struct {
		nome, src, code, frag string
	}{
		{"palavra reservada f", "var f = 1\n", codeReservedName, "palavra reservada da djo (declara função)"},
		{"palavra reservada if", "var if = 1\n", codeReservedName, "palavra reservada"},
		{"dois pontos seguidos", "print Http.bearer::nil\n", codeNestedCall, "entre parênteses"},
		{"namespace como valor", "print Http.json:Json.decode:\"{}\"\n", codeNamespaceValue, "Http.json:(Json.decode:x)"},
		{"escape inválido", "print \"a\\rb\"\n", codeInvalidEscape, "os válidos são"},
		{"texto vezes número", "print \"x\" * 5\n", codeOpTypes, "Text.repeat"},
		{"lista mais lista", "print [1] + [2]\n", codeOpTypes, "push:lista:item"},
		{"número mais texto", "print 1 + \"a\"\n", codeOpTypes, "str:número"},
		{"argumentos demais", "f reg\n    params[a[Int], b[Int]]\n    return a\nprint reg:1:len:\"x\":nil\n", codeArity, "(a, b), recebeu 4; se um dos argumentos é outra chamada"},
		{"nil indexado", "var l = nil\nprint l[\"valor\"]\n", codeNilIndex, "mas l é nil"},
		{"nil indexado em membro", "var r = {\"a\": nil}\nprint r[\"a\"][\"b\"]\n", codeNilIndex, "r[\"a\"] é nil"},
		{"variável com erro de digitação", "var nome = 1\nprint nmoe\n", codeUnknownName, "quer dizer 'nome'?"},
		{"função com erro de digitação", "print flen:\"x\"\n", codeUnknownName, "quer dizer 'len'?"},
		{"membro de namespace", "print Json.decod:\"{}\"\n", codeUnknownMember, "quer dizer 'decode'?"},
		{"nada parecido não sugere", "print qqqqqqqq\n", codeUnknownName, "não existe"},
	}
	for _, c := range cases {
		err := runDjo(t, nil, c.src)
		var de *djoError
		if !errors.As(err, &de) {
			t.Errorf("%s: esperava erro da djo, veio %v", c.nome, err)
			continue
		}
		if de.Code != c.code {
			t.Errorf("%s: code %q, esperado %q (%v)", c.nome, de.Code, c.code, err)
		}
		if !strings.Contains(de.Msg, c.frag) {
			t.Errorf("%s: faltou %q em %q", c.nome, c.frag, de.Msg)
		}
	}
	// sem nada parecido, nenhuma sugestão
	if err := runDjo(t, nil, "print qqqqqqqq\n"); strings.Contains(err.Error(), "quer dizer") {
		t.Errorf("sugeriu sem ter nome parecido: %v", err)
	}
}

func TestQuerDizerSoSugereNomesVisiveis(t *testing.T) {
	// 'Crypto' existe, mas só nomes de função do namespace citado entram
	err := runDjo(t, nil, "print Json.sealx:\"a\"\n")
	if err == nil || strings.Contains(err.Error(), "Crypto") || strings.Contains(err.Error(), "quer dizer") {
		t.Errorf("%v", err)
	}
	if got := didYouMean("xyz", []string{"abc", "def"}); got != "" {
		t.Errorf("%q", got)
	}
	if got := didYouMean("lenn", []string{"len", "lens"}); got != " (quer dizer 'len'?)" {
		t.Errorf("%q", got)
	}
}

const errCoreA = `f interno
    params[x[Int]]
    return x / 0

f meio
    params[x[Int]]
    return interno:x

f capturado
    return promise{
        return meio:5
    }error{
        [err]
        return err
    }
`

func TestErroDentroDoCoreTemArquivoEPilha(t *testing.T) {
	core := map[string]string{"core/a.djo": errCoreA}
	// err["file"] agora é o arquivo ONDE o erro nasceu (antes vinha vazio para o core)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "core"), 0o755)
	os.WriteFile(filepath.Join(root, "core", "a.djo"), []byte(errCoreA), 0o644)
	var out strings.Builder
	in := NewInterp(&out)
	in.root = root
	if err := in.loadCore(root); err != nil {
		t.Fatal(err)
	}
	prog, _ := Parse("var e = capturado:\nprint e[\"file\"]\nprint e[\"line\"]\nprint len:e[\"stack\"]\nprint e[\"stack\"][0][\"fn\"]\nprint e[\"stack\"][1][\"fn\"]\nprint e[\"stack\"][1][\"line\"]\n")
	if err := in.Run(prog); err != nil {
		t.Fatal(err)
	}
	if want := "core/a.djo\n3\n2\ninterno\nmeio\n7\n"; out.String() != want {
		t.Errorf("saída %q, esperado %q", out.String(), want)
	}
	// erro fatal sem captura: o texto da pilha
	err := runDjo(t, core, "meio:3\n")
	tr := errTrace(err)
	for _, frag := range []string{"em core/a.djo:3 (função interno)", "chamada em core/a.djo:7 (função meio)", "chamada na linha 1 do arquivo que rodou"} {
		if !strings.Contains(tr, frag) {
			t.Errorf("faltou %q na pilha:\n%s", frag, tr)
		}
	}
	// a mensagem do erro em si não muda (specs que a comparam continuam valendo)
	if err.Error() != "erro (linha 3): divisão por zero" {
		t.Errorf("%q", err.Error())
	}
}

func TestPilhaTruncadaEmRecursaoFunda(t *testing.T) {
	core := map[string]string{"core/r.djo": "f desce\n    params[n[Int]]\n    if\n        cond[n == 0]\n            return 1 / 0\n    return desce:(n - 1)\n"}
	err := runDjo(t, core, "desce:50\n")
	var de *djoError
	if !errors.As(err, &de) || len(de.Stack) != errMaxFrames || de.omitted != 51-errMaxFrames {
		t.Fatalf("pilha %d, omitidos %d: %v", len(de.Stack), de.omitted, err)
	}
	if !strings.Contains(de.Trace(), "mais 43 chamada(s) omitida(s)") {
		t.Error(de.Trace())
	}
}

func TestCodigosDoCrypto(t *testing.T) {
	t.Setenv(cryptoKeyVar, "")
	t.Setenv(cryptoOldVar, "")
	cases := []struct{ src, code string }{
		{"Crypto.seal:\"a\"\n", "crypto_key_missing"},
		{"Crypto.seal:1\n", "crypto_input_invalid"},
		{"Crypto.seal:\"a\":5\n", "crypto_input_invalid"},
		{"Crypto.open:\"naoe\"\n", "crypto_input_invalid"},
	}
	for _, c := range cases {
		var de *djoError
		if err := runDjo(t, nil, c.src); !errors.As(err, &de) || de.Code != c.code {
			t.Errorf("%q: %v", c.src, err)
		}
	}
	t.Setenv(cryptoKeyVar, "zz")
	var de *djoError
	if err := runDjo(t, nil, "Crypto.seal:\"a\"\n"); !errors.As(err, &de) || de.Code != "crypto_key_invalid" || strings.Contains(de.Msg, "zz\"") {
		t.Errorf("%v", err)
	}
	key := randHex(32)
	t.Setenv(cryptoKeyVar, key)
	in := NewInterp(io.Discard)
	cur, _, _ := in.cryptoKeys()
	sealed, _ := cryptoSeal(cur, "x", "ctx")
	for _, c := range []struct{ src, code string }{
		{"Crypto.open:\"" + sealed + "\":\"outro\"\n", "crypto_open_failed"},
		{"Crypto.open:\"djo1.ffffffff.AAAA\"\n", "crypto_key_unknown"},
	} {
		if err := runDjo(t, nil, c.src); !errors.As(err, &de) || de.Code != c.code {
			t.Errorf("%q: %v", c.src, err)
		}
	}
}
