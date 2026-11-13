//go:build !(linux || darwin || freebsd)

package main

import (
	"errors"
	"time"
)

// Sistemas sem suporte à porta serial (por exemplo, Windows).

type serialState struct {
	ports map[int64]*serialPort
	next  int64
}

type serialPort struct{}

var errNoSerial = errors.New("a porta serial não é suportada neste sistema operacional")

func openSerial(path string, baud int) (*serialPort, error)        { return nil, errNoSerial }
func (p *serialPort) readLine(time.Duration) (string, bool, error) { return "", false, errNoSerial }
func (p *serialPort) read(time.Duration) (string, error)           { return "", errNoSerial }
func (p *serialPort) write(string) (int, error)                    { return 0, errNoSerial }
func (p *serialPort) flush()                                       {}
func (p *serialPort) close()                                       {}
