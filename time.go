package main

import (
	"strings"
	"time"
)

// Time: datas e horas. Datas são Int (segundos desde 1970, UTC).

func init() {
	natives["Time"] = map[string]builtin{
		// Time.now: segundos desde 1970 (UTC)
		"now": {0, func(in *Interp, env *Env, line int, a []any) any { return time.Now().Unix() }},
		// Time.ms: milissegundos desde 1970 (para medir intervalos)
		"ms": {0, func(in *Interp, env *Env, line int, a []any) any { return time.Now().UnixMilli() }},
		// Time.sleep:ms espera (até 1 hora; em páginas servidas, no máximo 2 s por página)
		"sleep": {1, func(in *Interp, env *Env, line int, a []any) any {
			ms, ok := a[0].(int64)
			if !ok || ms < 0 {
				fail(line, "Time.sleep espera Int em milissegundos (0 ou mais)")
			}
			if ms > 3600*1000 {
				fail(line, "Time.sleep: no máximo 1 hora por chamada")
			}
			if in.sleepMax > 0 { // job: espera até o timeout
				if in.slept+ms > in.sleepMax {
					fail(line, "Time.sleep: o job pode esperar no máximo %d segundos no total (o timeout); o valor de Time.sleep é em MILISSEGUNDOS (1000 = 1 s)", in.sleepMax/1000)
				}
				in.slept += ms
			} else if in.served {
				if in.slept+ms > 2000 {
					fail(line, "Time.sleep: páginas servidas podem esperar no máximo 2 segundos no total (o valor é em MILISSEGUNDOS: 2000 = 2 s; a espera de um lock{} também conta)")
				}
				in.slept += ms
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
			return nil
		}},
		// Time.format:ts:"DD/MM/YYYY HH:mm"
		"format": {2, func(in *Interp, env *Env, line int, a []any) any {
			return unixTime(a[0], "Time.format", line).Format(timeLayout(argStr(a, 1, line, "Time.format"), line))
		}},
		// Time.parse:"2025-01-31":"YYYY-MM-DD" -> Int, ou nil se não bater
		"parse": {2, func(in *Interp, env *Env, line int, a []any) any {
			t, err := time.Parse(timeLayout(argStr(a, 1, line, "Time.parse"), line), argStr(a, 0, line, "Time.parse"))
			if err != nil {
				return nil
			}
			return t.Unix()
		}},
		// Time.date:ts -> {year, month, day, hour, minute, second, weekday}
		"date": {1, func(in *Interp, env *Env, line int, a []any) any {
			t := unixTime(a[0], "Time.date", line)
			m := newMap()
			for _, kv := range []struct {
				k string
				v int
			}{{"year", t.Year()}, {"month", int(t.Month())}, {"day", t.Day()}, {"hour", t.Hour()}, {"minute", t.Minute()}, {"second", t.Second()}, {"weekday", int(t.Weekday())}} {
				m.set(kv.k, int64(kv.v))
			}
			return m
		}},
	}
}

func unixTime(v any, fn string, line int) time.Time {
	n, ok := v.(int64)
	if !ok {
		fail(line, "%s espera um Int (segundos desde 1970), mas recebeu %s", fn, typeName(v))
	}
	return time.Unix(n, 0).UTC()
}

var timeTokens = []struct{ tok, layout string }{
	{"YYYY", "2006"}, {"MM", "01"}, {"DD", "02"},
	{"HH", "15"}, {"mm", "04"}, {"ss", "05"},
}

// timeLayout troca YYYY MM DD HH mm ss pelo formato interno; o resto é texto.
func timeLayout(f string, line int) string {
	if f == "" {
		fail(line, "o formato de data não pode ser vazio")
	}
	var sb strings.Builder
	for i := 0; i < len(f); {
		matched := false
		for _, t := range timeTokens {
			if strings.HasPrefix(f[i:], t.tok) {
				sb.WriteString(t.layout)
				i += len(t.tok)
				matched = true
				break
			}
		}
		if !matched {
			// letras e dígitos soltos não podem virar parte de um formato do Go
			if c := f[i]; c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' {
				fail(line, "formato de data: '%c' não é reconhecido (use YYYY MM DD HH mm ss e símbolos como - / : espaço)", c)
			}
			sb.WriteByte(f[i])
			i++
		}
	}
	return sb.String()
}
