package main

import "testing"

func TestHashHmacEEqual(t *testing.T) {
	// vetor do RFC 4231 (caso 2): chave "Jefe", texto "what do ya want for nothing?"
	out, err := run(t, `print Hash.hmac:"Jefe":"what do ya want for nothing?"
print Hash.hmac:"k":"" == Hash.hmac:"k":""
print Hash.hmac:"k":"a" == Hash.hmac:"k2":"a"
print len:(Hash.hmac:"k":"a")
print Hash.equal:"abc":"abc"
print Hash.equal:"abc":"abd"
print Hash.equal:"abc":"abcd"
print Hash.equal:"":""
print Hash.equal:"a":5
print Hash.equal:nil:nil
`)
	if err != nil {
		t.Fatal(err)
	}
	want := "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843\ntrue\nfalse\n64\ntrue\nfalse\nfalse\ntrue\nfalse\nfalse\n"
	if out != want {
		t.Fatalf("saída:\n%q\nesperado:\n%q", out, want)
	}
	for _, src := range []string{"print Hash.hmac:5:\"a\"", "print Hash.hmac:\"k\":5"} {
		if _, err := run(t, src+"\n"); err == nil {
			t.Errorf("%s deveria dar erro", src)
		}
	}
}
