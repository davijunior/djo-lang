//go:build nonet

package main

import (
	"fmt"
	"os"
)

func askMain(args []string) int {
	fmt.Fprintln(os.Stderr, "djo ask não está incluído neste build (compilado com -tags nonet)")
	return 1
}
