//go:build !nonet && !noweb

package main

import "testing"

func TestMailNoSpecViaRota(t *testing.T) {
	mailEnvSet(t, map[string]string{"DJO_SMTP_HOST": "127.0.0.1", "DJO_SMTP_PORT": "1", "DJO_SMTP_FROM": "site@x.com"})
	root := specProject(t, map[string]string{
		"index.djo": `Mail.send:{"to": "ana@x.com", "subject": "Da página", "text": "oi"}
print "ok"
`,
		"spec/pagina_spec.djo": `f it_pagina_manda
    var r = Spec.get:"/"
    expect.status:r:200
    var es = Spec.emails:
    expect.len:es:1
    expect.eq:es[0]["subject"]:"Da página"
`,
	})
	if code, out, errw := runSpec(t, root); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errw)
	}
}

func TestMailSaidaNuncaEServida(t *testing.T) {
	if !hidden("/_saida/emails/x.eml") {
		t.Error("_saida precisa ser escondida")
	}
}
