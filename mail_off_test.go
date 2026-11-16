//go:build nonet

package main

import (
	"strings"
	"testing"
)

func TestMailNaoExisteNoPerfilNonet(t *testing.T) {
	_, err := run(t, `var r = Mail.send:{"to": "a@x.com", "subject": "s", "text": "t"}`)
	if err == nil || !strings.Contains(err.Error(), "Mail não existe neste perfil") {
		t.Fatalf("%v", err)
	}
}
