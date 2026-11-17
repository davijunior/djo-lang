package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// agenda.djo: tarefas agendadas, no estilo do ask.djo e do rotas.djo.
//
//	Schedule.every:"10m":"limpar_sessoes"
//	Schedule.cron:"20 3 * * *":"backup"
//	Schedule.cron:"0 9 * * 1":"resumo":{"tries": 5, "timeout": "5m", "args": {"escopo": "todos"}, "catch_up": false}
//
// O cron tem 5 campos (minuto hora dia-do-mês mês dia-da-semana), sempre em UTC.
// Cada disparo vira um Job (job.go). Este arquivo só lê e confere a agenda.

const (
	agendaFile       = "agenda.djo"
	schedMinEvery    = 30 * time.Second
	schedMaxEvery    = 7 * 24 * time.Hour
	schedMaxEntries  = 200
	schedFreshWindow = 15 * time.Second // um disparo mais velho que isso é "perdido" (catch_up)
)

type schedDef struct {
	id, kind, expr, fn string
	every              time.Duration
	cron               *cronSpec
	tries              int
	timeout            time.Duration
	args               string
	catchUp            bool
	line               int
}

// ---- cron ----

type cronSpec struct {
	min, hour, dom, mon, dow uint64
	domStar, dowStar         bool
}

func cronField(s string, lo, hi int, name string) (uint64, bool, error) {
	var bits uint64
	star := strings.HasPrefix(s, "*")
	for _, part := range strings.Split(s, ",") {
		if part == "" {
			return 0, false, fmt.Errorf("campo %s vazio", name)
		}
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n < 1 || n > hi {
				return 0, false, fmt.Errorf("passo inválido em %s: %q", name, part)
			}
			step, part = n, part[:i]
		}
		a, b := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			p := strings.SplitN(part, "-", 2)
			x, e1 := strconv.Atoi(p[0])
			y, e2 := strconv.Atoi(p[1])
			if e1 != nil || e2 != nil || x > y {
				return 0, false, fmt.Errorf("faixa inválida em %s: %q", name, part)
			}
			a, b = x, y
		default:
			x, err := strconv.Atoi(part)
			if err != nil {
				return 0, false, fmt.Errorf("valor inválido em %s: %q", name, part)
			}
			a, b = x, x
			if step > 1 {
				b = hi
			}
		}
		if a < lo || b > hi {
			return 0, false, fmt.Errorf("%s fora de %d-%d: %q", name, lo, hi, part)
		}
		for v := a; v <= b; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, star, nil
}

func parseCron(expr string) (*cronSpec, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, fmt.Errorf("o cron precisa de 5 campos (minuto hora dia mês dia-da-semana), mas tem %d", len(f))
	}
	c := &cronSpec{}
	var err error
	if c.min, _, err = cronField(f[0], 0, 59, "minuto"); err != nil {
		return nil, err
	}
	if c.hour, _, err = cronField(f[1], 0, 23, "hora"); err != nil {
		return nil, err
	}
	if c.dom, c.domStar, err = cronField(f[2], 1, 31, "dia do mês"); err != nil {
		return nil, err
	}
	if c.mon, _, err = cronField(f[3], 1, 12, "mês"); err != nil {
		return nil, err
	}
	if c.dow, c.dowStar, err = cronField(f[4], 0, 7, "dia da semana"); err != nil {
		return nil, err
	}
	if c.dow&(1<<7) != 0 { // 7 também é domingo
		c.dow |= 1
	}
	return c, nil
}

func (c *cronSpec) dayOK(t time.Time) bool {
	d := c.dom&(1<<uint(t.Day())) != 0
	w := c.dow&(1<<uint(t.Weekday())) != 0
	if c.domStar || c.dowStar {
		return d && w
	}
	return d || w
}

// next é o primeiro disparo estritamente depois de after (UTC, minuto cheio); zero se não há em 8 anos.
func (c *cronSpec) next(after time.Time) time.Time {
	t := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(8, 0, 0)
	for t.Before(limit) {
		switch {
		case c.mon&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		case !c.dayOK(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
		case c.hour&(1<<uint(t.Hour())) == 0:
			t = t.Truncate(time.Hour).Add(time.Hour)
		case c.min&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

// ---- o arquivo ----

func (d *schedDef) slotAfter(last time.Time) time.Time {
	if d.cron != nil {
		return d.cron.next(last)
	}
	return last.Add(d.every)
}

func init() {
	ns := nativeNS("Schedule")
	for _, kind := range []string{"every", "cron"} {
		kind := kind
		ns[kind] = builtin{-1, func(in *Interp, env *Env, line int, a []any) any {
			if in.schedLoad == nil {
				fail(line, "Schedule.%s só vale dentro do agenda.djo", kind)
			}
			if len(a) < 2 || len(a) > 3 {
				fail(line, "Schedule.%s espera a %s, o nome da função e, se quiser, um mapa de opções", kind, map[string]string{"every": "duração (\"10m\")", "cron": "expressão (\"20 3 * * *\")"}[kind])
			}
			expr, ok1 := a[0].(string)
			fn, ok2 := a[1].(string)
			if !ok1 || !ok2 {
				fail(line, "Schedule.%s: a %s e o nome da função são textos", kind, kind)
			}
			d := &schedDef{kind: kind, expr: strings.TrimSpace(expr), fn: fn, tries: jobDefaultTries, timeout: jobDefTimeout, args: "{}", catchUp: true, line: line}
			if kind == "every" {
				dur, ok := parseJobDur(d.expr)
				if !ok {
					fail(line, "Schedule.every: %q não é uma duração (use 30s, 10m, 2h ou 1d)", expr)
				}
				if dur < schedMinEvery || dur > schedMaxEvery {
					fail(line, "Schedule.every: a duração vai de 30s a 7d")
				}
				d.every = dur
			} else {
				c, err := parseCron(d.expr)
				if err != nil {
					fail(line, "Schedule.cron: %v", err)
				}
				if c.next(time.Now()).IsZero() {
					fail(line, "Schedule.cron: %q nunca dispara", expr)
				}
				d.cron = c
			}
			if len(a) == 3 {
				o, ok := a[2].(*Map)
				if !ok {
					fail(line, "Schedule.%s: as opções são um mapa (tries, timeout, args, catch_up)", kind)
				}
				for i, k := range o.Keys {
					ks, _ := k.(string)
					v := o.Vals[i]
					switch ks {
					case "tries":
						n, ok := v.(int64)
						if !ok || n < 1 || n > jobMaxTries {
							fail(line, "Schedule.%s: 'tries' é um número de 1 a %d", kind, jobMaxTries)
						}
						d.tries = int(n)
					case "timeout":
						d.timeout = jobDurOpt(v, "timeout", line)
						if d.timeout < time.Second || d.timeout > jobMaxTimeout {
							fail(line, "Schedule.%s: 'timeout' vai de 1s a 15m", kind)
						}
					case "args":
						m, ok := v.(*Map)
						if !ok {
							fail(line, "Schedule.%s: 'args' é um mapa", kind)
						}
						jobData(m, 0, line)
						d.args = jsonEncode(m, "", line)
						if len(d.args) > jobMaxArgs {
							fail(line, "Schedule.%s: 'args' passa de %d KB", kind, jobMaxArgs>>10)
						}
					case "catch_up":
						b, ok := v.(bool)
						if !ok {
							fail(line, "Schedule.%s: 'catch_up' é true ou false", kind)
						}
						d.catchUp = b
					default:
						fail(line, "Schedule.%s: a opção '%s' não existe (valem: tries, timeout, args, catch_up)", kind, ks)
					}
				}
			}
			d.id = kind + ":" + d.expr + ":" + fn
			for _, o := range *in.schedLoad {
				if o.id == d.id {
					fail(line, "Schedule.%s: o mesmo agendamento já foi declarado na linha %d", kind, o.line)
				}
			}
			if len(*in.schedLoad) >= schedMaxEntries {
				fail(line, "agenda.djo: no máximo %d agendamentos", schedMaxEntries)
			}
			*in.schedLoad = append(*in.schedLoad, d)
			return nil
		}}
	}
}

// loadAgenda lê e confere o agenda.djo (nil se o projeto não tem).
func loadAgenda(root string) ([]*schedDef, error) {
	path := filepath.Join(root, agendaFile)
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	wrap := func(err error) error {
		if de, ok := err.(*djoError); ok {
			return &djoError{de.Line, "agenda.djo: " + de.Msg}
		}
		return err
	}
	prog, err := Parse(string(src))
	if err != nil {
		return nil, wrap(err)
	}
	for _, s := range prog {
		es, ok := s.(*ExprStmt)
		var mc *MethodCall
		if ok {
			mc, ok = es.X.(*MethodCall)
		}
		if ok {
			id, isID := mc.X.(*Ident)
			ok = isID && id.Name == "Schedule"
		}
		if !ok {
			return nil, &djoError{lineOf(s), "agenda.djo: só aceita chamadas Schedule.every: e Schedule.cron:"}
		}
	}
	var defs []*schedDef
	in := NewInterp(io.Discard)
	in.root = root
	in.schedLoad = &defs
	in.maxSteps = 100000
	if err := in.Run(prog); err != nil {
		return nil, wrap(err)
	}
	core := NewInterp(io.Discard)
	if err := core.loadCore(root); err != nil {
		return nil, err
	}
	for _, d := range defs {
		if err := jobFuncCheckErr(core.funcs, d.fn); err != nil {
			return nil, &djoError{d.line, "agenda.djo: " + err.Error()}
		}
	}
	return defs, nil
}

func jobFuncCheckErr(funcs map[string]*FuncDecl, name string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if de, ok := r.(*djoError); ok {
				err = fmt.Errorf("%s", strings.TrimPrefix(de.Msg, "Job.run: "))
				return
			}
			panic(r)
		}
	}()
	jobFuncCheck(funcs, name, "Job.run", 0)
	return nil
}

// checkAgenda é o que o djo build e o djo spec chamam.
func checkAgenda(root string) error {
	_, err := loadAgenda(root)
	return err
}
