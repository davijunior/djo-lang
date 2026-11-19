package main

import (
	"bytes"
	"strings"
	"testing"
)

const testKeyA = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
const testKeyB = "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"

func cryptoRun(t *testing.T, src string) (string, error) {
	t.Helper()
	return run(t, src)
}

func TestCryptoIdaEVolta(t *testing.T) {
	t.Setenv(cryptoKeyVar, testKeyA)
	t.Setenv(cryptoOldVar, "")
	big := strings.Repeat("a", 64<<10)
	for _, txt := range []string{"", "token-do-omie", "ação ✓ 日本", big} {
		src := "var c = Crypto.seal:" + q(txt) + ":\"empresa:42\"\nprint Crypto.open:c:\"empresa:42\" == " + q(txt) + "\nprint len:c > 20\n"
		out, err := cryptoRun(t, src)
		if err != nil || out != "true\ntrue\n" {
			t.Fatalf("%.20q: %q %v", txt, out, err)
		}
	}
	out, err := cryptoRun(t, "var a = Crypto.seal:\"x\"\nvar b = Crypto.seal:\"x\"\nprint a == b\nprint Crypto.open:a == Crypto.open:b\n")
	if err != nil || out != "false\ntrue\n" {
		t.Fatalf("nonce: %q %v", out, err)
	}
}

func q(s string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(s) + "\""
}

func TestCryptoContextoEAdulteracao(t *testing.T) {
	t.Setenv(cryptoKeyVar, testKeyA)
	t.Setenv(cryptoOldVar, "")
	bad := "Crypto.open: não consegui abrir"
	cases := []struct{ name, src, err string }{
		{"copiado para outra empresa", "var c = Crypto.seal:\"s\":\"empresa:7\"\nprint Crypto.open:c:\"empresa:42\"\n", bad},
		{"sem contexto no open", "var c = Crypto.seal:\"s\":\"empresa:7\"\nprint Crypto.open:c\n", bad},
		{"contexto no open e não no seal", "var c = Crypto.seal:\"s\"\nprint Crypto.open:c:\"x\"\n", bad},
		{"formato desconhecido", "print Crypto.open:\"djo9.abcd.AAAA\"\n", "formato não reconhecido"},
		{"lixo", "print Crypto.open:\"qualquer coisa\"\n", "formato não reconhecido"},
		{"id de chave desconhecido", "print Crypto.open:\"djo1.deadbeef.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"\n", "não achei a chave deste texto"},
		{"texto não é String", "print Crypto.seal:5\n", "Crypto.seal"},
		{"contexto não é String", "print Crypto.seal:\"a\":5\n", "contexto"},
		{"grande demais", "print Crypto.seal:" + q(strings.Repeat("a", 64<<10+1)) + "\n", "passa de 64 KB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := cryptoRun(t, c.src)
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Fatalf("esperava %q, veio %v (%q)", c.err, err, out)
			}
		})
	}
}

func TestCryptoAdulteraBytes(t *testing.T) {
	k, _ := cryptoKeyFrom(testKeyA)
	s, err := cryptoSeal(k, "segredo", "ctx")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(s, ".", 3)
	payload := []byte(parts[2])
	if got, ok := cryptoOpen(k, string(payload), "ctx"); !ok || got != "segredo" {
		t.Fatalf("ida e volta: %q %v", got, ok)
	}
	// o último caractere do base64 pode ter bits sobrando (não muda os bytes): fica de fora
	for i := 0; i < len(payload)-1; i++ { // trocar qualquer caractere do payload invalida
		mod := bytes.Clone(payload)
		if mod[i] == 'A' {
			mod[i] = 'B'
		} else {
			mod[i] = 'A'
		}
		if _, ok := cryptoOpen(k, string(mod), "ctx"); ok {
			t.Fatalf("a mudança na posição %d passou", i)
		}
	}
	k2, _ := cryptoKeyFrom(testKeyB)
	if _, ok := cryptoOpen(k2, parts[2], "ctx"); ok {
		t.Fatal("chave errada abriu")
	}
}

func TestCryptoRotacao(t *testing.T) {
	t.Setenv(cryptoKeyVar, testKeyA)
	t.Setenv(cryptoOldVar, "")
	out, err := cryptoRun(t, "print Crypto.seal:\"v\":\"c\"\n")
	if err != nil {
		t.Fatal(err)
	}
	velho := strings.TrimSpace(out)
	// troca: B é a atual, A vira antiga
	t.Setenv(cryptoKeyVar, testKeyB)
	t.Setenv(cryptoOldVar, testKeyA)
	out, err = cryptoRun(t, "print Crypto.open:\""+velho+"\":\"c\"\n")
	if err != nil || out != "v\n" {
		t.Fatalf("a antiga deveria abrir: %q %v", out, err)
	}
	out, _ = cryptoRun(t, "print Crypto.seal:\"v\":\"c\"\n")
	novo := strings.TrimSpace(out)
	k, _ := cryptoKeyFrom(testKeyB)
	if !strings.HasPrefix(novo, "djo1."+k.id+".") {
		t.Fatalf("a nova chave deveria cifrar: %s", novo)
	}
	// sem a antiga, o velho não abre
	t.Setenv(cryptoOldVar, "")
	if _, err = cryptoRun(t, "print Crypto.open:\""+velho+"\":\"c\"\n"); err == nil || !strings.Contains(err.Error(), "não achei a chave") {
		t.Fatalf("esperava 'não achei a chave', veio %v", err)
	}
}

func TestCryptoSemChaveOuMalformada(t *testing.T) {
	t.Setenv(cryptoOldVar, "")
	t.Setenv(cryptoKeyVar, "")
	_, err := cryptoRun(t, "print Crypto.seal:\"x\"\n")
	if err == nil || !strings.Contains(err.Error(), "defina DJO_CRYPTO_KEY") || !strings.Contains(err.Error(), "djo crypto newkey") {
		t.Fatalf("sem chave: %v", err)
	}
	for _, bad := range []string{"curta", testKeyA[:62], testKeyA + "00", strings.Repeat("zz", 32)} {
		t.Setenv(cryptoKeyVar, bad)
		_, err = cryptoRun(t, "print Crypto.seal:\"x\"\n")
		if err == nil || !strings.Contains(err.Error(), "malformada") {
			t.Fatalf("%q: %v", bad, err)
		}
		if strings.Contains(err.Error(), bad) && len(bad) > 5 {
			t.Fatalf("o erro mostrou o valor da chave: %v", err)
		}
		if checkCryptoEnv() == nil {
			t.Fatalf("o servidor deveria recusar subir com %q", bad)
		}
	}
	t.Setenv(cryptoKeyVar, "")
	if checkCryptoEnv() != nil {
		t.Fatal("sem a variável o servidor sobe normalmente")
	}
	t.Setenv(cryptoOldVar, testKeyA)
	if checkCryptoEnv() == nil {
		t.Fatal("chaves antigas sem a atual deveriam recusar")
	}
	t.Setenv(cryptoKeyVar, testKeyA)
	t.Setenv(cryptoOldVar, "lixo")
	if err := checkCryptoEnv(); err == nil || strings.Contains(err.Error(), "lixo") && !strings.Contains(err.Error(), cryptoOldVar) {
		t.Fatalf("antiga malformada: %v", err)
	}
}

// Caminhos que poderiam expor a chave: nenhum deles pode devolvê-la.
func TestCryptoChaveNaoVaza(t *testing.T) {
	t.Setenv(cryptoKeyVar, testKeyA)
	t.Setenv(cryptoOldVar, testKeyB)
	t.Setenv("DJO_CRYPTO_OUTRA", "segredo-outra")
	for _, name := range []string{cryptoKeyVar, cryptoOldVar, "DJO_CRYPTO_OUTRA", "djo_crypto_key", "DJO_CRYPTO_"} {
		out, err := cryptoRun(t, "print Env.get:\""+name+"\"\n")
		if err == nil || !strings.Contains(err.Error(), "reservada") {
			t.Errorf("Env.get:%s deveria ser recusado (%q, %v)", name, out, err)
		}
		out, err = cryptoRun(t, "print Env.get:\""+name+"\":\"padrao\"\n")
		if err == nil || out != "" {
			t.Errorf("Env.get com padrão:%s vazou %q", name, out)
		}
	}
	// nenhuma mensagem de erro do Crypto cita a chave
	k, _ := cryptoKeyFrom(testKeyA)
	for _, src := range []string{
		"print Crypto.open:\"djo1." + k.id + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"\n",
		"print Crypto.open:\"x\"\n",
		"print Crypto.seal:5\n",
	} {
		_, err := cryptoRun(t, src)
		if err != nil && (strings.Contains(err.Error(), testKeyA) || strings.Contains(err.Error(), testKeyB)) {
			t.Errorf("erro com a chave: %v", err)
		}
	}
	// o id da chave não revela a chave
	if strings.Contains(testKeyA, k.id) && k.id == testKeyA[:8] {
		t.Error("o id parece o começo da chave")
	}
	// variáveis comuns continuam funcionando
	t.Setenv("DJO_OUTRA_COISA", "ok")
	if out, err := cryptoRun(t, "print Env.get:\"DJO_OUTRA_COISA\"\n"); err != nil || out != "ok\n" {
		t.Errorf("Env.get comum: %q %v", out, err)
	}
}

func TestCryptoBoosterPrecisaDePermissao(t *testing.T) {
	if !restrictedNS["Crypto"] {
		t.Fatal("Crypto deveria exigir permissão de booster")
	}
}

func TestCryptoNewkey(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := cryptoMain([]string{"newkey"}, &out, &errw); rc != 0 {
		t.Fatal(rc)
	}
	k := strings.TrimSpace(out.String())
	if _, err := cryptoKeyFrom(k); err != nil {
		t.Fatalf("a chave gerada não vale: %v", err)
	}
	var o2 bytes.Buffer
	cryptoMain([]string{"newkey"}, &o2, &errw)
	if o2.String() == out.String() {
		t.Fatal("duas chaves iguais")
	}
	if rc := cryptoMain([]string{"x"}, &out, &errw); rc != 2 {
		t.Fatal(rc)
	}
}

func TestCryptoSpecUsaChaveEfemera(t *testing.T) {
	t.Setenv(cryptoKeyVar, "")
	t.Setenv(cryptoOldVar, "")
	in := &Interp{spec: &specCtx{}}
	cur, _, err := in.cryptoKeys()
	if err != nil || cur == nil {
		t.Fatalf("o djo spec deveria ter chave efêmera: %v", err)
	}
	s, _ := cryptoSeal(cur, "x", "c")
	if got, ok := cryptoOpen(cur, strings.SplitN(s, ".", 3)[2], "c"); !ok || got != "x" {
		t.Fatal("ida e volta com a chave efêmera")
	}
	// fora do spec, sem a variável, continua erro
	if _, _, err := (&Interp{}).cryptoKeys(); err == nil {
		t.Fatal("fora do spec a falta da chave é erro")
	}
	// a chave efêmera não é a de produção: com a variável definida, ela vale
	t.Setenv(cryptoKeyVar, testKeyA)
	cur2, _, _ := in.cryptoKeys()
	if cur2.raw == cur.raw {
		t.Fatal("a variável deveria valer sobre a efêmera")
	}
}
