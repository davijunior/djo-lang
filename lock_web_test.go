//go:build !noweb && !nonet && !nodb

package main

import (
	"strings"
	"testing"
)

func TestLockNoSpecPaginaEHold(t *testing.T) {
	got := specStatuses(t, map[string]string{
		"rotas.djo": "Route.get:\"/e/{id}\":{\"agent\": \"agentes/e.djo\", \"about\": \"e\"}\nRoute.get:\"/r\":{\"agent\": \"agentes/r.djo\", \"about\": \"r\"}\nRoute.get:\"/t\":{\"agent\": \"agentes/t.djo\", \"about\": \"t\"}\n",
		// o Hub: pega a trava por empresa+método, captura lock_busy e responde 503
		"agentes/e.djo": `var id = Http.param:"id"
var r = uniq promise{
    lock{
        key["omie:" + id + ":Listar"]
        wait[1]
        return {"ok": true}
    }
}error{
    [e]
    return {"code": e["code"], "key": e["data"]["key"]}
}
if
    cond[r["code"] != nil]
        Http.status:503
        Http.json:{"erro": {"codigo": "erp_ocupado", "chave": r["key"]}}
else
    Http.json:r
`,
		"agentes/r.djo": "lock{\n    key[\"redir\"]\n    wait[0]\n    redirect:\"/outro\"\n}\n",
		"agentes/t.djo": "Db.open:\"t.db\"\nDb.begin:\nlock{\n    key[\"tx\"]\n    wait[0]\n    print 1\n}\n",
		"spec/l_spec.djo": `f it_livre_responde_200
    var r = Spec.get:"/e/42"
    expect.status:r:200

f it_ocupada_responde_503_com_codigo
    Spec.lock_hold:"omie:42:Listar"
    var r = Spec.get:"/e/42"
    expect.status:r:503
    expect.json:r:"erro":{"codigo": "erp_ocupado", "chave": "omie:42:Listar"}

f it_outra_empresa_nao_e_afetada
    Spec.lock_hold:"omie:42:Listar"
    var r = Spec.get:"/e/7"
    expect.status:r:200

f it_solta_e_entra
    Spec.lock_hold:"omie:42:Listar"
    expect.status:(Spec.get:"/e/42"):503
    Spec.lock_release:"omie:42:Listar"
    expect.status:(Spec.get:"/e/42"):200

f it_o_hold_acaba_com_o_teste
    expect.status:(Spec.get:"/e/42"):200

f it_redirect_solta_a_trava
    expect.status:(Spec.get:"/r"):302
    expect.status:(Spec.get:"/r"):302

f it_lock_dentro_de_transacao_e_erro
    var r = Spec.get:"/t"
    expect.status:r:500

f it_spec_fail_dentro_do_lock_solta
    lock{
        key["do-spec"]
        wait[0]
        Spec.fail:"de propósito"
    }
`,
	})
	for n, s := range got {
		if n == "spec fail dentro do lock solta" {
			if !strings.HasPrefix(s, "falhou") {
				t.Errorf("%s: %s", n, s)
			}
			continue
		}
		if !strings.HasPrefix(s, "passou") {
			t.Errorf("%s: %s", n, s)
		}
	}
	if len(got) != 8 {
		t.Errorf("esperava 8 specs: %v", got)
	}
	lockEmpty(t)
}
