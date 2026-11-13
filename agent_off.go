//go:build nonet

package main

import (
	"fmt"
	"os"
)

func agentMain(args []string) int {
	fmt.Fprintln(os.Stderr, "djo agent não está incluído neste build (compilado com -tags nonet)")
	return 1
}
