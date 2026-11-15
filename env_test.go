package main

import (
	"strings"
	"testing"
)

func TestEnvGet(t *testing.T) {
	t.Setenv("DJO_TESTE_ENV", "segredo")
	t.Setenv("DJO_TESTE_VAZIA", "")
	got, err := run(t, `print Env.get:"DJO_TESTE_ENV"
print Env.get:"DJO_TESTE_VAZIA":"padrao" == ""
print Env.get:"DJO_NAO_EXISTE_XYZ" == nil
print Env.get:"DJO_NAO_EXISTE_XYZ":"padrao"`)
	if err != nil || got != "segredo\ntrue\ntrue\npadrao\n" {
		t.Errorf("saída = %q (%v)", got, err)
	}
	for _, src := range []string{`print Env.get:"com espaço"`, `print Env.get:5`, `print Env.get:"A":5`, `print Env.get:`} {
		if _, err := run(t, src); err == nil {
			t.Errorf("%q deveria dar erro", src)
		}
	}
	if _, err := run(t, `print Env.get:"1X"`); err == nil || !strings.Contains(err.Error(), "nome da variável") {
		t.Errorf("nome inválido: %v", err)
	}
}

func TestEnvExigePermissaoNoBooster(t *testing.T) {
	t.Setenv("DJO_TESTE_ENV", "segredo")
	root := t.TempDir()
	mkBooster(t, root, "lerenv", "", map[string]string{"main.djo": "f ler\n    return Env.get:\"DJO_TESTE_ENV\"\n"})
	if _, err := runWith(t, root, "print lerenv.ler:\n"); err == nil || !strings.Contains(err.Error(), "não declarou essa permissão") {
		t.Fatalf("Env sem permissão: %v", err)
	}
	root2 := t.TempDir()
	mkBooster(t, root2, "lerenv", `{"nome":"lerenv","versao":"1","permissoes":["Env"]}`, map[string]string{"main.djo": "f ler\n    return Env.get:\"DJO_TESTE_ENV\"\n"})
	if out, err := runWith(t, root2, "print lerenv.ler:\n"); err != nil || out != "segredo\n" {
		t.Fatalf("com permissão: %q %v", out, err)
	}
}
