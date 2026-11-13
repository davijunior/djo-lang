//go:build freebsd

package main

import "golang.org/x/sys/unix"

func setTermiosSpeed(t *unix.Termios, baud int) bool {
	t.Ispeed, t.Ospeed = uint32(baud), uint32(baud)
	return true
}
