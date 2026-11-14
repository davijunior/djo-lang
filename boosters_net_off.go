//go:build nonet

package main

import "fmt"

const hasBoosterNet = false

func httpGetLimited(raw string, limit int64) ([]byte, error) {
	return nil, fmt.Errorf("baixar boosters não está incluído neste build (compilado com -tags nonet); use uma pasta local")
}
