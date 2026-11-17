//go:build nodb || noweb

package main

import (
	"fmt"
	"io"
)

const hasJobs = false

// jobOffReason diz por que o Job não existe neste build.
const jobOffReason = "compilado sem banco ou sem servidor (-tags nodb ou noweb)"

func jobsMain(args []string, out, errw io.Writer) int {
	fmt.Fprintln(errw, "djo jobs não existe neste perfil ("+jobOffReason+")")
	return 1
}
