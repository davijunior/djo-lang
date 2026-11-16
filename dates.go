package main

import (
	"strings"
	"time"
)

// Date e DateTime.
//
//	Date      um dia do calendário, sem hora nem fuso      2025-01-31
//	DateTime  um instante, sempre em UTC                   2025-01-31T14:05:00Z
//
// Os dois são valores comparáveis (==, <, >), vão e voltam como texto ISO no JSON e aparecem no
// manifesto como string com format "date" e "date-time". Um campo Date ou DateTime vazio é nil.
// Date + Int soma dias; DateTime + Int soma segundos. Date e DateTime nunca se misturam em
// silêncio: compare com d.date ou DateTime.of:d.

type Date struct{ days int64 }    // dias desde 1970-01-01
type DateTime struct{ sec int64 } // segundos desde 1970-01-01T00:00:00Z

const (
	dateMinDays = -719162 // 0001-01-01
	dateMaxDays = 2932896 // 9999-12-31
	dtMinSec    = -62135596800
	dtMaxSec    = 253402300799
)

const (
	isoDate     = "2006-01-02"
	isoDateTime = "2006-01-02T15:04:05Z"
	sqlDateTime = "2006-01-02 15:04:05"
)

func (d Date) time() time.Time     { return time.Unix(d.days*86400, 0).UTC() }
func (t DateTime) time() time.Time { return time.Unix(t.sec, 0).UTC() }
func (d Date) String() string      { return d.time().Format(isoDate) }
func (t DateTime) String() string  { return t.time().Format(isoDateTime) }

func dateOf(t time.Time) Date {
	t = t.UTC()
	return Date{time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400}
}

func dateInRange(days int64) bool { return days >= dateMinDays && days <= dateMaxDays }
func dtInRange(sec int64) bool    { return sec >= dtMinSec && sec <= dtMaxSec }

// makeDate valida o dia: 2025-02-31 e mês 13 não existem.
func makeDate(y, m, d int64) (Date, bool) {
	if y < 1 || y > 9999 || m < 1 || m > 12 || d < 1 || d > 31 {
		return Date{}, false
	}
	t := time.Date(int(y), time.Month(m), int(d), 0, 0, 0, 0, time.UTC)
	if int64(t.Year()) != y || int64(t.Month()) != m || int64(t.Day()) != d {
		return Date{}, false
	}
	return Date{t.Unix() / 86400}, true
}

func parseDate(s string) (Date, bool) {
	t, err := time.Parse(isoDate, s)
	if err != nil {
		return Date{}, false
	}
	return dateOf(t), true
}

// parseDateTime aceita RFC 3339 ("2025-01-31T14:05:00Z", com ou sem deslocamento) e o formato
// de banco "2025-01-31 14:05:00" (sem fuso, lido como UTC). O resultado é sempre UTC.
func parseDateTime(s string) (DateTime, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", sqlDateTime} {
		if t, err := time.Parse(layout, s); err == nil {
			if !dtInRange(t.Unix()) {
				return DateTime{}, false
			}
			return DateTime{t.Unix()}, true
		}
	}
	return DateTime{}, false
}

func hasClock(format string) bool {
	return strings.Contains(format, "HH") || strings.Contains(format, "mm") || strings.Contains(format, "ss")
}

// parseDateFmt lê texto no formato dado (YYYY MM DD HH mm ss), por ex. "DD/MM/YYYY".
func parseDateFmt(text, format string, line int) (time.Time, bool) {
	t, err := time.Parse(timeLayout(format, line), text)
	return t, err == nil
}

// ---- operadores ----

func isDateVal(v any) bool {
	switch v.(type) {
	case Date, DateTime:
		return true
	}
	return false
}

// dateMixCheck recusa comparar ou operar Date com DateTime.
func dateMixCheck(l, r any, line int) {
	_, ld := l.(Date)
	_, lt := l.(DateTime)
	_, rd := r.(Date)
	_, rt := r.(DateTime)
	if (ld && rt) || (lt && rd) {
		fail(line, "Date e DateTime não se misturam: use data.date (ou DateTime.of:data) para comparar na mesma unidade")
	}
}

// dateBinary faz as contas com datas. ok é false se nenhum lado é Date/DateTime.
func dateBinary(op Kind, l, r any, line int) (any, bool) {
	if !isDateVal(l) && !isDateVal(r) {
		return nil, false
	}
	switch a := l.(type) {
	case Date:
		switch b := r.(type) {
		case Date:
			switch op {
			case Minus:
				return a.days - b.days, true
			case Lt:
				return a.days < b.days, true
			case Gt:
				return a.days > b.days, true
			case Lte:
				return a.days <= b.days, true
			case Gte:
				return a.days >= b.days, true
			}
		case int64:
			if op == Plus || op == Minus {
				n := a.days + b
				if op == Minus {
					n = a.days - b
				}
				if !dateInRange(n) {
					fail(line, "a conta saiu do intervalo das datas (anos 1 a 9999)")
				}
				return Date{n}, true
			}
		}
	case DateTime:
		switch b := r.(type) {
		case DateTime:
			switch op {
			case Minus:
				return a.sec - b.sec, true
			case Lt:
				return a.sec < b.sec, true
			case Gt:
				return a.sec > b.sec, true
			case Lte:
				return a.sec <= b.sec, true
			case Gte:
				return a.sec >= b.sec, true
			}
		case int64:
			if op == Plus || op == Minus {
				n := a.sec + b
				if op == Minus {
					n = a.sec - b
				}
				if !dtInRange(n) {
					fail(line, "a conta saiu do intervalo das datas (anos 1 a 9999)")
				}
				return DateTime{n}, true
			}
		}
	case int64:
		if op == Plus {
			switch b := r.(type) {
			case Date:
				return dateBinaryPlus(Date{b.days}, a, line), true
			case DateTime:
				return dateBinaryPlus(DateTime{b.sec}, a, line), true
			}
		}
	}
	return nil, false // o resto cai na mensagem de erro de sempre
}

func dateBinaryPlus(v any, n int64, line int) any {
	r, _ := dateBinary(Plus, v, n, line)
	return r
}

// ---- propriedades e métodos: d.year, dt.unix, d.format:"DD/MM/YYYY" ----

func dateProp(v any, name string, line int) (any, bool) {
	switch x := v.(type) {
	case Date:
		t := x.time()
		switch name {
		case "year":
			return int64(t.Year()), true
		case "month":
			return int64(t.Month()), true
		case "day":
			return int64(t.Day()), true
		case "weekday":
			return int64(t.Weekday()), true
		}
		fail(line, "Date não tem '%s' (tem year, month, day, weekday e o método format:)", name)
	case DateTime:
		t := x.time()
		switch name {
		case "year":
			return int64(t.Year()), true
		case "month":
			return int64(t.Month()), true
		case "day":
			return int64(t.Day()), true
		case "hour":
			return int64(t.Hour()), true
		case "minute":
			return int64(t.Minute()), true
		case "second":
			return int64(t.Second()), true
		case "weekday":
			return int64(t.Weekday()), true
		case "unix":
			return x.sec, true
		case "date":
			return dateOf(t), true
		}
		fail(line, "DateTime não tem '%s' (tem year, month, day, hour, minute, second, weekday, unix, date e o método format:)", name)
	}
	return nil, false
}

func dateFormat(v any, format, fn string, line int) string {
	switch x := v.(type) {
	case Date:
		if hasClock(format) {
			fail(line, "%s: Date não tem hora (use só YYYY, MM e DD; para hora use DateTime)", fn)
		}
		return x.time().Format(timeLayout(format, line))
	case DateTime:
		return x.time().Format(timeLayout(format, line))
	}
	fail(line, "%s espera Date ou DateTime, mas recebeu %s", fn, typeName(v))
	return ""
}

func (in *Interp) dateMethod(v any, n *MethodCall, env *Env) (any, bool) {
	if !isDateVal(v) {
		return nil, false
	}
	if n.Name != "format" {
		fail(n.Line, "%s não tem o método '%s' (só format:; o resto é propriedade, como .year)", typeName(v), n.Name)
	}
	if len(n.Args) != 1 {
		fail(n.Line, "%s.format espera o formato, como format:\"DD/MM/YYYY\"", typeName(v))
	}
	f, ok := in.eval(n.Args[0], env).(string)
	if !ok {
		fail(n.Line, "%s.format espera o formato como String", typeName(v))
	}
	return dateFormat(v, f, typeName(v)+".format", n.Line), true
}

// ---- Date.* e DateTime.* ----

func intArgs(a []any, fn string, line int) []int64 {
	out := make([]int64, len(a))
	for i, v := range a {
		n, ok := v.(int64)
		if !ok {
			fail(line, "%s espera números inteiros, mas o argumento %d é %s", fn, i+1, typeName(v))
		}
		out[i] = n
	}
	return out
}

func init() {
	nativeNS("Date")["today"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any { return dateOf(time.Now()) }}
	nativeNS("Date")["make"] = builtin{3, func(in *Interp, env *Env, line int, a []any) any {
		n := intArgs(a, "Date.make", line)
		d, ok := makeDate(n[0], n[1], n[2])
		if !ok {
			fail(line, "Date.make: %d-%02d-%02d não é uma data que existe", n[0], n[1], n[2])
		}
		return d
	}}
	// Date.parse:"2025-01-31"  ou  Date.parse:"31/01/2025":"DD/MM/YYYY"; nil se não bater
	nativeNS("Date")["parse"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		if len(a) < 1 || len(a) > 2 {
			fail(line, "Date.parse espera (texto) ou (texto, formato)")
		}
		s, ok := a[0].(string)
		if !ok {
			fail(line, "Date.parse espera o texto como String, mas recebeu %s", typeName(a[0]))
		}
		if len(a) == 1 {
			if d, ok := parseDate(s); ok {
				return d
			}
			return nil
		}
		f, ok := a[1].(string)
		if !ok {
			fail(line, "Date.parse espera o formato como String")
		}
		if hasClock(f) {
			fail(line, "Date.parse: Date não tem hora (use só YYYY, MM e DD)")
		}
		if t, ok := parseDateFmt(s, f, line); ok {
			return dateOf(t)
		}
		return nil
	}}
	nativeNS("Date")["of"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		switch x := a[0].(type) {
		case Date:
			return x
		case DateTime:
			return dateOf(x.time())
		}
		fail(line, "Date.of espera Date ou DateTime, mas recebeu %s", typeName(a[0]))
		return nil
	}}
	nativeNS("Date")["format"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		return dateFormat(a[0], argStr(a, 1, line, "Date.format"), "Date.format", line)
	}}

	dt := nativeNS("DateTime")
	dt["now"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any { return DateTime{time.Now().Unix()} }}
	dt["make"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		if len(a) != 3 && len(a) != 6 {
			fail(line, "DateTime.make espera (ano, mês, dia) ou (ano, mês, dia, hora, minuto, segundo)")
		}
		n := intArgs(a, "DateTime.make", line)
		d, ok := makeDate(n[0], n[1], n[2])
		if !ok {
			fail(line, "DateTime.make: %d-%02d-%02d não é uma data que existe", n[0], n[1], n[2])
		}
		var h, mi, s int64
		if len(n) == 6 {
			h, mi, s = n[3], n[4], n[5]
		}
		if h < 0 || h > 23 || mi < 0 || mi > 59 || s < 0 || s > 59 {
			fail(line, "DateTime.make: hora, minuto ou segundo fora da faixa (%d:%d:%d)", h, mi, s)
		}
		return DateTime{d.days*86400 + h*3600 + mi*60 + s}
	}}
	// DateTime.parse:"2025-01-31T14:05:00Z"  (RFC 3339) ou "2025-01-31 14:05:00" (UTC);
	// com formato: DateTime.parse:"31/01/2025 14:05":"DD/MM/YYYY HH:mm"; nil se não bater
	dt["parse"] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
		if len(a) < 1 || len(a) > 2 {
			fail(line, "DateTime.parse espera (texto) ou (texto, formato)")
		}
		s, ok := a[0].(string)
		if !ok {
			fail(line, "DateTime.parse espera o texto como String, mas recebeu %s", typeName(a[0]))
		}
		if len(a) == 1 {
			if v, ok := parseDateTime(s); ok {
				return v
			}
			return nil
		}
		f, ok := a[1].(string)
		if !ok {
			fail(line, "DateTime.parse espera o formato como String")
		}
		if t, ok := parseDateFmt(s, f, line); ok && dtInRange(t.Unix()) {
			return DateTime{t.Unix()}
		}
		return nil
	}}
	dt["from_unix"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		n, ok := a[0].(int64)
		if !ok || !dtInRange(n) {
			fail(line, "DateTime.from_unix espera um Int de segundos (anos 1 a 9999)")
		}
		return DateTime{n}
	}}
	// DateTime.of:data  meia-noite UTC do dia;  DateTime.end_of:data  23:59:59 do dia
	dt["of"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		d, ok := a[0].(Date)
		if !ok {
			fail(line, "DateTime.of espera um Date, mas recebeu %s", typeName(a[0]))
		}
		return DateTime{d.days * 86400}
	}}
	dt["end_of"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		d, ok := a[0].(Date)
		if !ok {
			fail(line, "DateTime.end_of espera um Date, mas recebeu %s", typeName(a[0]))
		}
		return DateTime{d.days*86400 + 86399}
	}}
	dt["format"] = builtin{2, func(in *Interp, env *Env, line int, a []any) any {
		return dateFormat(a[0], argStr(a, 1, line, "DateTime.format"), "DateTime.format", line)
	}}
}
