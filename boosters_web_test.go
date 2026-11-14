//go:build !noweb

package main

import "testing"

func TestBoostersNaoSaoServidos(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.djo.html", "oi")
	mkBooster(t, root, "mat", "", map[string]string{"main.djo": dobra})
	srv, err := NewServer(root, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = srv
	for _, p := range []string{"/boosters", "/boosters/mat/booster.json", "/boosters/mat/mat.djo", "/boosters.lock"} {
		if !hidden(p) && p != "/boosters.lock" {
			t.Errorf("%s não deveria ser servido", p)
		}
	}
}

func TestServidorRecusaBoosterAdulterado(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.djo.html", "oi")
	mkBooster(t, root, "mat", "", map[string]string{"main.djo": dobra})
	writeFile(t, root, "boosters.lock", `{"mat":{"versao":"1.0.0","sha256":"00"}}`)
	if _, err := NewServer(root, false); err == nil {
		t.Fatal("o servidor deveria recusar um booster que não bate com o lock")
	}
}
