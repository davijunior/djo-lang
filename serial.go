package main

import (
	"path/filepath"
	"strings"
	"time"
)

// Serial: porta serial (USB-serial, Arduino, GPS, sensores).
//
//	var p = Serial.open:"/dev/ttyUSB0":9600     8N1; devolve um número que identifica a porta
//	Serial.readline:p                           uma linha (sem o fim de linha), ou nil se não chegou
//	Serial.readline:p:5000                      esperando até 5000 ms (padrão 2000)
//	Serial.read:p  /  Serial.read:p:500         o que já chegou (texto, "" se nada)
//	Serial.write:p:"LIGA\n"                     envia; devolve quantos bytes
//	Serial.flush:p  Serial.close:p  Serial.ports:

const (
	serialMaxPorts   = 4
	serialDefaultMs  = 2000
	serialMaxServed  = 5000 // espera máxima por chamada em páginas servidas
	serialMaxWaitMs  = 10 * 60 * 1000
	serialMaxLine    = 64 << 10
	serialReadChunk  = 4096
	serialWriteLimit = 1 << 20
)

var serialBauds = map[int64]bool{1200: true, 2400: true, 4800: true, 9600: true, 19200: true, 38400: true, 57600: true, 115200: true, 230400: true}

// Só dispositivos de terminal: o resto de /dev (discos, memória...) nunca é aberto.
func serialPathOK(p string) bool {
	if strings.Contains(p, "..") || !strings.HasPrefix(p, "/dev/") {
		return false
	}
	rest := strings.TrimPrefix(p, "/dev/")
	if strings.HasPrefix(rest, "pts/") || strings.HasPrefix(rest, "serial/") {
		return true
	}
	return !strings.Contains(rest, "/") && (strings.HasPrefix(rest, "tty") || strings.HasPrefix(rest, "cu.") || strings.HasPrefix(rest, "serial"))
}

func (in *Interp) serialPort(a []any, line int, fn string) *serialPort {
	id, ok := a[0].(int64)
	if !ok {
		fail(line, "%s espera a porta devolvida por Serial.open, mas recebeu %s", fn, typeName(a[0]))
	}
	p := in.serial.ports[id]
	if p == nil {
		fail(line, "%s: a porta %d não está aberta", fn, id)
	}
	return p
}

func (in *Interp) waitMs(a []any, i int, line int, fn string) time.Duration {
	ms := int64(serialDefaultMs)
	if len(a) > i {
		var ok bool
		if ms, ok = a[i].(int64); !ok || ms < 0 {
			fail(line, "%s: a espera é um Int em milissegundos", fn)
		}
	}
	limit := int64(serialMaxWaitMs)
	if in.served {
		limit = serialMaxServed
	}
	if ms > limit {
		ms = limit
	}
	return time.Duration(ms) * time.Millisecond
}

func (in *Interp) closeSerials() {
	for id, p := range in.serial.ports {
		p.close()
		delete(in.serial.ports, id)
	}
}

func init() {
	ns := nativeNS("Serial")
	ns["open"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		path := argStr(a, 0, line, "Serial.open")
		baud, ok := a[1].(int64)
		if !ok || !serialBauds[baud] {
			fail(line, "Serial.open: velocidade inválida (use 1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200 ou 230400)")
		}
		if !serialPathOK(path) {
			fail(line, "Serial.open: '%s' não é uma porta serial (use /dev/ttyUSB0, /dev/ttyACM0, /dev/serial0...)", path)
		}
		if len(in.serial.ports) >= serialMaxPorts {
			fail(line, "Serial.open: no máximo %d portas abertas", serialMaxPorts)
		}
		p, err := openSerial(path, int(baud))
		if err != nil {
			fail(line, "Serial.open '%s': %v", path, err)
		}
		if in.serial.ports == nil {
			in.serial.ports = map[int64]*serialPort{}
		}
		in.serial.next++
		in.serial.ports[in.serial.next] = p
		return in.serial.next
	}}
	ns["readline"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		if len(a) < 1 || len(a) > 2 {
			fail(line, "Serial.readline espera (porta) ou (porta, espera em ms)")
		}
		p := in.serialPort(a, line, "Serial.readline")
		s, ok, err := p.readLine(in.waitMs(a, 1, line, "Serial.readline"))
		if err != nil {
			fail(line, "Serial.readline: %v", err)
		}
		if !ok {
			return nil
		}
		return s
	}}
	ns["read"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		if len(a) < 1 || len(a) > 2 {
			fail(line, "Serial.read espera (porta) ou (porta, espera em ms)")
		}
		p := in.serialPort(a, line, "Serial.read")
		wait := time.Duration(0)
		if len(a) == 2 {
			wait = in.waitMs(a, 1, line, "Serial.read")
		}
		s, err := p.read(wait)
		if err != nil {
			fail(line, "Serial.read: %v", err)
		}
		return s
	}}
	ns["write"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		p := in.serialPort(a, line, "Serial.write")
		s := argStr(a, 1, line, "Serial.write")
		if len(s) > serialWriteLimit {
			fail(line, "Serial.write: texto grande demais")
		}
		n, err := p.write(s)
		if err != nil {
			fail(line, "Serial.write: %v", err)
		}
		return int64(n)
	}}
	ns["flush"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		in.serialPort(a, line, "Serial.flush").flush()
		return nil
	}}
	ns["close"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		p := in.serialPort(a, line, "Serial.close")
		p.close()
		delete(in.serial.ports, a[0].(int64))
		return true
	}}
	ns["ports"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any {
		var found []string
		for _, pat := range []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/ttyAMA*", "/dev/ttyS[0-9]*", "/dev/serial[0-9]*", "/dev/cu.usb*", "/dev/cu.wch*", "/dev/cu.SLAB*"} {
			m, _ := filepath.Glob(pat)
			found = append(found, m...)
		}
		return strList(found)
	}}
}
