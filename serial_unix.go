//go:build linux || darwin || freebsd

package main

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

type serialState struct {
	ports map[int64]*serialPort
	next  int64
}

type serialPort struct {
	fd  int
	buf []byte // o que chegou e ainda não foi lido
}

func openSerial(path string, baud int) (*serialPort, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("%v (a porta existe? seu usuário está no grupo 'dialout'?)", err)
	}
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		unix.Close(fd)
		return nil, errors.New("isso não é um terminal serial")
	}
	// modo "raw": bytes puros, sem eco nem tradução, 8 bits sem paridade (8N1)
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB | unix.CSTOPB
	t.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 0, 0
	if !setTermiosSpeed(t, baud) {
		unix.Close(fd)
		return nil, fmt.Errorf("velocidade %d não suportada neste sistema", baud)
	}
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, t); err != nil {
		unix.Close(fd)
		return nil, err
	}
	p := &serialPort{fd: fd}
	p.flush()
	return p, nil
}

// wait espera haver dados (ou o prazo acabar).
func (p *serialPort) wait(d time.Duration, events int16) (bool, error) {
	ms := int(d / time.Millisecond)
	if d > 0 && ms == 0 {
		ms = 1
	}
	fds := []unix.PollFd{{Fd: int32(p.fd), Events: events}}
	for {
		n, err := unix.Poll(fds, ms)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if n > 0 && fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 && fds[0].Revents&unix.POLLIN == 0 {
			return false, errors.New("a porta foi desconectada")
		}
		return n > 0, nil
	}
}

// fill lê o que estiver disponível agora para o buffer.
func (p *serialPort) fill() error {
	chunk := make([]byte, serialReadChunk)
	for len(p.buf) < serialMaxLine {
		n, err := unix.Read(p.fd, chunk)
		if n > 0 {
			p.buf = append(p.buf, chunk[:n]...)
			if n < len(chunk) {
				return nil
			}
			continue
		}
		if err == unix.EAGAIN || err == unix.EINTR || (err == nil && n == 0) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("a porta foi desconectada (%v)", err)
		}
	}
	return nil
}

func (p *serialPort) readLine(timeout time.Duration) (string, bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := p.fill(); err != nil {
			return "", false, err
		}
		if i := bytes.IndexByte(p.buf, '\n'); i >= 0 {
			line := string(bytes.TrimRight(p.buf[:i], "\r"))
			p.buf = p.buf[i+1:]
			return line, true, nil
		}
		if len(p.buf) >= serialMaxLine { // linha sem fim: entrega o que tem
			line := string(p.buf)
			p.buf = nil
			return line, true, nil
		}
		left := time.Until(deadline)
		if left <= 0 {
			return "", false, nil
		}
		if _, err := p.wait(left, unix.POLLIN); err != nil {
			return "", false, err
		}
	}
}

func (p *serialPort) read(timeout time.Duration) (string, error) {
	if err := p.fill(); err != nil {
		return "", err
	}
	if len(p.buf) == 0 && timeout > 0 {
		if _, err := p.wait(timeout, unix.POLLIN); err != nil {
			return "", err
		}
		if err := p.fill(); err != nil {
			return "", err
		}
	}
	s := string(p.buf)
	p.buf = nil
	return s, nil
}

func (p *serialPort) write(s string) (int, error) {
	data := []byte(s)
	sent := 0
	deadline := time.Now().Add(5 * time.Second)
	for sent < len(data) {
		n, err := unix.Write(p.fd, data[sent:])
		if n > 0 {
			sent += n
		}
		if err == nil || err == unix.EINTR {
			continue
		}
		if err != unix.EAGAIN {
			return sent, fmt.Errorf("a porta foi desconectada (%v)", err)
		}
		left := time.Until(deadline)
		if left <= 0 {
			return sent, errors.New("a porta não aceitou os dados a tempo")
		}
		if _, err := p.wait(left, unix.POLLOUT); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

func (p *serialPort) flush() {
	p.buf = nil
	unix.IoctlSetInt(p.fd, ioctlFlush, unix.TCIFLUSH)
}

func (p *serialPort) close() { unix.Close(p.fd) }
