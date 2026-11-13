//go:build linux

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// newPty cria um par de terminais virtuais: o djo abre o "slave" como se fosse um
// dispositivo USB-serial, e o teste fala pelo "master".
func newPty(t *testing.T) (master int, slave string) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("sem /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("pty: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Skipf("pty: %v", err)
	}
	return fd, fmt.Sprintf("/dev/pts/%d", n)
}

func TestSerial(t *testing.T) {
	master, slave := newPty(t)
	go func() {
		time.Sleep(100 * time.Millisecond)
		unix.Write(master, []byte("23.5\r\nabc"))
		time.Sleep(300 * time.Millisecond)
		unix.Write(master, []byte("def\nresto"))
	}()
	out, err := run(t, `var p = Serial.open:"`+slave+`":9600
print Serial.readline:p
print Serial.readline:p:50 ?? "sem linha"
print Serial.readline:p:2000
print Serial.write:p:"LIGA\n"
print Time.ms: > 0
print Serial.read:p:1000
Serial.flush:p
print Serial.close:p
`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "23.5\nsem linha\nabcdef\n5\ntrue\nresto\ntrue\n" {
		t.Fatalf("saída %q", out)
	}
	buf := make([]byte, 16)
	n, _ := unix.Read(master, buf)
	if !strings.Contains(string(buf[:n]), "LIGA") {
		t.Fatalf("o dispositivo recebeu %q", buf[:n])
	}
}

func TestSerialErros(t *testing.T) {
	_, slave := newPty(t)
	for _, src := range []string{
		`Serial.open:"/dev/null":9600`, // não é terminal
		`Serial.open:"/dev/sda":9600`,  // não é porta serial
		`Serial.open:"/etc/passwd":9600`,
		`Serial.open:"/dev/../etc/passwd":9600`,
		`Serial.open:"/dev/ttyNAOEXISTE":9600`,
		`Serial.open:"` + slave + `":1234`, // velocidade inválida
		`Serial.open:"` + slave + `":"9600"`,
		`Serial.readline:99`,
		`Serial.readline:"x"`,
		`var p = Serial.open:"` + slave + `":9600
Serial.close:p
Serial.write:p:"x"`,
	} {
		if _, err := run(t, src); err == nil {
			t.Errorf("esperava erro em %q", src)
		}
	}
	// portas abertas são fechadas ao fim do programa
	if _, err := run(t, `var p = Serial.open:"`+slave+`":115200`); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, `print Serial.ports:`)
	if err != nil || !strings.HasPrefix(out, "[") {
		t.Fatalf("%q %v", out, err)
	}
}
