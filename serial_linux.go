//go:build linux

package main

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
	ioctlFlush      = unix.TCFLSH
)

var linuxBauds = map[int]uint32{
	1200: unix.B1200, 2400: unix.B2400, 4800: unix.B4800, 9600: unix.B9600, 19200: unix.B19200,
	38400: unix.B38400, 57600: unix.B57600, 115200: unix.B115200, 230400: unix.B230400,
}

// Só os bits de velocidade do Cflag: funciona em todas as arquiteturas (ARM, MIPS, RISC-V...).
func setTermiosSpeed(t *unix.Termios, baud int) bool {
	b, ok := linuxBauds[baud]
	if !ok {
		return false
	}
	t.Cflag = t.Cflag&^unix.CBAUD | b
	return true
}
