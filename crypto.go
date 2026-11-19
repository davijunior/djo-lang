package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Crypto.seal:texto[:contexto] e Crypto.open:cifrado[:contexto]: AES-256-GCM com a chave mestra do
// servidor (DJO_CRYPTO_KEY; as antigas, só para abrir, em DJO_CRYPTO_KEYS_OLD). A chave NUNCA vira
// valor da linguagem: é lida aqui, em Go; o Env.get e o ${VAR} do Db.open recusam esses nomes.
//
// Formato: djo1.<id da chave>.<base64url(nonce de 12 bytes + texto cifrado + etiqueta)>
// O contexto entra como dado autenticado (AAD): o texto cifrado para um contexto não abre em outro.

const (
	cryptoKeyVar    = "DJO_CRYPTO_KEY"
	cryptoOldVar    = "DJO_CRYPTO_KEYS_OLD"
	cryptoPrefix    = "DJO_CRYPTO_"
	cryptoMaxText   = 64 << 10
	cryptoFormatTag = "djo1"
)

// cryptoReserved: nomes de variável de ambiente que a linguagem não lê (guardam a chave).
func cryptoReserved(name string) bool {
	return strings.HasPrefix(strings.ToUpper(name), cryptoPrefix)
}

type cryptoKey struct {
	id  string
	raw [32]byte
}

func cryptoKeyFrom(hexKey string) (*cryptoKey, error) {
	hexKey = strings.TrimSpace(hexKey)
	b, err := hex.DecodeString(hexKey)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("a chave precisa ter 32 bytes em hexadecimal (64 letras e números)")
	}
	k := &cryptoKey{}
	copy(k.raw[:], b)
	sum := sha256.Sum256(append([]byte("djo-crypto-id"), b...))
	k.id = hex.EncodeToString(sum[:4])
	return k, nil
}

// specCryptoKey é a chave efêmera do djo spec (só da execução do processo).
var (
	specKeyOnce sync.Once
	specKey     *cryptoKey
)

func ephemeralKey() *cryptoKey {
	specKeyOnce.Do(func() { specKey, _ = cryptoKeyFrom(randHex(32)) })
	return specKey
}

// codedErr é um erro com código estável (vira err["code"] quando o Crypto falha numa página ou job).
type codedErr struct{ code, msg string }

func (e *codedErr) Error() string { return e.msg }

// cryptoStr: o primeiro argumento tem de ser texto (senão, crypto_input_invalid).
func cryptoStr(a []any, line int, fn string) string {
	s, ok := a[0].(string)
	if !ok {
		failc(line, "crypto_input_invalid", "%s espera um texto, mas recebeu %s", fn, typeName(a[0]))
	}
	return s
}

// cryptoFail lança o erro de uma chamada do Crypto, com o código do erro de chave se houver.
func cryptoFail(line int, fn string, err error) {
	if ce, ok := err.(*codedErr); ok {
		failc(line, ce.code, "%s: %v", fn, err)
	}
	failc(line, "crypto_input_invalid", "%s: %v", fn, err)
}

// cryptoKeys devolve a chave atual e as antigas. Erro (sem mostrar valores) se faltar ou estiver malformada.
func (in *Interp) cryptoKeys() (cur *cryptoKey, old []*cryptoKey, err error) {
	if v := os.Getenv(cryptoKeyVar); strings.TrimSpace(v) != "" {
		if cur, err = cryptoKeyFrom(v); err != nil {
			return nil, nil, &codedErr{"crypto_key_invalid", fmt.Sprintf("%s está malformada: %v (gere uma com: djo crypto newkey)", cryptoKeyVar, err)}
		}
	} else if in != nil && in.spec != nil {
		cur = ephemeralKey()
	} else {
		return nil, nil, &codedErr{"crypto_key_missing", fmt.Sprintf("defina %s no ambiente do servidor (32 bytes em hexadecimal; gere uma com: djo crypto newkey)", cryptoKeyVar)}
	}
	for _, s := range strings.Split(os.Getenv(cryptoOldVar), ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		k, e := cryptoKeyFrom(s)
		if e != nil {
			return nil, nil, &codedErr{"crypto_key_invalid", fmt.Sprintf("%s tem uma chave malformada: %v", cryptoOldVar, e)}
		}
		old = append(old, k)
	}
	return cur, old, nil
}

// checkCryptoEnv é a conferência da subida do servidor: uma chave malformada falha cedo.
func checkCryptoEnv() error {
	if os.Getenv(cryptoKeyVar) == "" && os.Getenv(cryptoOldVar) == "" {
		return nil
	}
	if os.Getenv(cryptoKeyVar) == "" {
		return fmt.Errorf("%s tem chaves antigas, mas falta a %s", cryptoOldVar, cryptoKeyVar)
	}
	_, _, err := (*Interp)(nil).cryptoKeys()
	return err
}

func cryptoSeal(k *cryptoKey, text, ctx string) (string, error) {
	block, err := aes.NewCipher(k.raw[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(text), []byte(ctx))
	return cryptoFormatTag + "." + k.id + "." + base64.RawURLEncoding.EncodeToString(out), nil
}

func cryptoOpen(k *cryptoKey, payload, ctx string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false
	}
	block, err := aes.NewCipher(k.raw[:])
	if err != nil {
		return "", false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize()+gcm.Overhead() {
		return "", false
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(ctx))
	if err != nil {
		return "", false
	}
	return string(plain), true
}

func init() {
	ns := nativeNS("Crypto")
	ctxArg := func(a []any, line int, fn string) string {
		if len(a) == 1 {
			return ""
		}
		c, ok := a[1].(string)
		if !ok {
			failc(line, "crypto_input_invalid", "%s: o contexto é um texto, como \"empresa:42\"", fn)
		}
		return c
	}
	ns["seal"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		if len(a) < 1 || len(a) > 2 {
			failc(line, "crypto_input_invalid", "Crypto.seal espera (texto) ou (texto, contexto)")
		}
		text := cryptoStr(a, line, "Crypto.seal")
		ctx := ctxArg(a, line, "Crypto.seal")
		if len(text) > cryptoMaxText {
			failc(line, "crypto_input_invalid", "Crypto.seal: o texto passa de %d KB", cryptoMaxText>>10)
		}
		cur, _, err := in.cryptoKeys()
		if err != nil {
			cryptoFail(line, "Crypto.seal", err)
		}
		out, err := cryptoSeal(cur, text, ctx)
		if err != nil {
			failc(line, "crypto_seal_failed", "Crypto.seal: não consegui cifrar")
		}
		return out
	}}
	ns["open"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		if len(a) < 1 || len(a) > 2 {
			failc(line, "crypto_input_invalid", "Crypto.open espera (cifrado) ou (cifrado, contexto)")
		}
		sealed := cryptoStr(a, line, "Crypto.open")
		ctx := ctxArg(a, line, "Crypto.open")
		parts := strings.SplitN(sealed, ".", 3)
		if len(parts) != 3 || parts[0] != cryptoFormatTag {
			failc(line, "crypto_input_invalid", "Crypto.open: formato não reconhecido")
		}
		cur, old, err := in.cryptoKeys()
		if err != nil {
			cryptoFail(line, "Crypto.open", err)
		}
		for _, k := range append([]*cryptoKey{cur}, old...) {
			if k.id == parts[1] {
				if plain, ok := cryptoOpen(k, parts[2], ctx); ok {
					return plain
				}
				failc(line, "crypto_open_failed", "Crypto.open: não consegui abrir (chave errada, contexto diferente ou texto alterado)")
			}
		}
		failc(line, "crypto_key_unknown", "Crypto.open: não achei a chave deste texto (a chave foi trocada? veja %s)", cryptoOldVar)
		return nil
	}}
}

// cryptoMain: djo crypto newkey
func cryptoMain(args []string, out, errw io.Writer) int {
	if len(args) == 1 && args[0] == "newkey" {
		fmt.Fprintln(out, randHex(32))
		return 0
	}
	fmt.Fprintln(errw, "uso: djo crypto newkey   (imprime uma chave nova para a variável "+cryptoKeyVar+")")
	return 2
}
