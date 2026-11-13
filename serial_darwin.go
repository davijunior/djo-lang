//go:build darwin

package main

import "golang.org/x/sys/unix"

func setTermiosSpeed(t *unix.Termios, baud int) bool {
	t.Ispeed, t.Ospeed = uint64(baud), uint64(baud)
	return true
}
