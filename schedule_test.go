package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCronProximo(t *testing.T) {
	u := func(s string) time.Time {
		x, err := time.Parse("2006-01-02 15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	cases := []struct{ expr, after, want string }{
		{"20 3 * * *", "2026-01-10 12:00", "2026-01-11 03:20"},
		{"20 3 * * *", "2026-01-10 03:19", "2026-01-10 03:20"},
		{"20 3 * * *", "2026-01-10 03:20", "2026-01-11 03:20"}, // estritamente depois
		{"* * * * *", "2026-01-10 12:00", "2026-01-10 12:01"},
		{"*/15 * * * *", "2026-01-10 12:14", "2026-01-10 12:15"},
		{"*/15 * * * *", "2026-01-10 12:45", "2026-01-10 13:00"},
		{"0 9 * * 1", "2026-01-10 12:00", "2026-01-12 09:00"}, // segunda
		{"0 9 * * 7", "2026-01-10 12:00", "2026-01-11 09:00"}, // 7 = domingo
		{"0 0 1 * *", "2026-01-31 23:59", "2026-02-01 00:00"},
		{"0 0 29 2 *", "2026-01-01 00:00", "2028-02-29 00:00"},    // só em ano bissexto
		{"30 23 31 12 *", "2026-12-31 23:30", "2027-12-31 23:30"}, // virada de ano
		{"0 0 1,15 * *", "2026-01-02 00:00", "2026-01-15 00:00"},  // lista
		{"10-12 8 * * *", "2026-01-10 08:12", "2026-01-11 08:10"}, // faixa
		{"0 12 1 * 1", "2026-01-02 00:00", "2026-01-05 12:00"},    // dia do mês OU dia da semana (segunda 5)
		{"0 12 1 * 1", "2026-02-01 12:00", "2026-02-02 12:00"},    // OU: segunda dia 2
		{"0 0 * 6-8 *", "2026-01-01 00:00", "2026-06-01 00:00"},   // meses
		{"5/20 * * * *", "2026-01-10 12:06", "2026-01-10 12:25"},  // a/n = a-59/n
	}
	for _, c := range cases {
		cr, err := parseCron(c.expr)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		if got := cr.next(u(c.after)); !got.Equal(u(c.want)) {
			t.Errorf("%q depois de %s: %s, esperava %s", c.expr, c.after, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}

func TestCronInvalido(t *testing.T) {
	for _, e := range []string{"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *", "* * * 13 *", "* * * * 8",
		"a * * * *", "5-1 * * * *", "*/0 * * * *", "*/x * * * *", "1,,2 * * * *", "-1 * * * *", "@daily", "0 0 30 2 *", "0 0 31 4 *"} {
		cr, err := parseCron(e)
		if err == nil && !cr.next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero() {
			t.Errorf("%q devia ser recusado ou nunca disparar", e)
		}
	}
}

func TestDuracaoDeJob(t *testing.T) {
	ok := map[string]time.Duration{"30s": 30 * time.Second, "10m": 10 * time.Minute, "2h": 2 * time.Hour, "1d": 24 * time.Hour, " 5m ": 5 * time.Minute}
	for s, w := range ok {
		if d, good := parseJobDur(s); !good || d != w {
			t.Errorf("%q: %v %v", s, d, good)
		}
	}
	for _, s := range []string{"", "10", "m", "-5m", "1.5h", "10 m", "5w", "1000000s", "1e3s", "10M"} {
		if _, good := parseJobDur(s); good {
			t.Errorf("%q devia ser inválido", s)
		}
	}
}

func agendaRoot(t *testing.T, agenda string, core string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "core"), 0o755)
	if core != "" {
		os.WriteFile(filepath.Join(root, "core", "c.djo"), []byte(core), 0o644)
	}
	if agenda != "" {
		os.WriteFile(filepath.Join(root, "agenda.djo"), []byte(agenda), 0o644)
	}
	return root
}

const agendaCore = "f limpar\n    print \"x\"\n\nf recebe\n    params[a[Map]]\n    print \"y\"\n\nf dois\n    params[a[Int], b[Int]]\n    print a\n\naction fazer\n    return 1\n"

func TestAgendaCarrega(t *testing.T) {
	root := agendaRoot(t, `Schedule.every:"10m":"limpar"
Schedule.cron:"20 3 * * *":"recebe":{"tries": 5, "timeout": "5m", "args": {"escopo": "todos"}, "catch_up": false}
`, agendaCore)
	defs, err := loadAgenda(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 || defs[0].every != 10*time.Minute || defs[0].tries != 3 || !defs[0].catchUp || defs[0].timeout != time.Minute {
		t.Fatalf("%+v", defs[0])
	}
	d := defs[1]
	if d.cron == nil || d.tries != 5 || d.timeout != 5*time.Minute || d.catchUp || d.args != `{"escopo":"todos"}` || d.fn != "recebe" {
		t.Errorf("%+v", d)
	}
	if defs, err := loadAgenda(agendaRoot(t, "", agendaCore)); err != nil || defs != nil {
		t.Errorf("sem agenda: %v %v", defs, err)
	}
}

func TestAgendaErros(t *testing.T) {
	cases := map[string]string{
		"função inexistente":      `Schedule.every:"10m":"nao_existe"`,
		"função com 2 parâmetros": `Schedule.every:"10m":"dois"`,
		"action":                  `Schedule.every:"10m":"fazer"`,
		"duração ruim":            `Schedule.every:"dez":"limpar"`,
		"duração curta demais":    `Schedule.every:"5s":"limpar"`,
		"duração longa demais":    `Schedule.every:"8d":"limpar"`,
		"cron ruim":               `Schedule.cron:"* * *":"limpar"`,
		"cron que nunca dispara":  `Schedule.cron:"0 0 30 2 *":"limpar"`,
		"código solto":            "var x = 1",
		"outra chamada":           `Route.get:"/":"x"`,
		"opção desconhecida":      `Schedule.every:"10m":"limpar":{"vezes": 3}`,
		"tries fora":              `Schedule.every:"10m":"limpar":{"tries": 11}`,
		"timeout fora":            `Schedule.every:"10m":"limpar":{"timeout": "1h"}`,
		"args não é mapa":         `Schedule.every:"10m":"limpar":{"args": 5}`,
		"catch_up não é bool":     `Schedule.every:"10m":"limpar":{"catch_up": "sim"}`,
		"duplicado":               "Schedule.every:\"10m\":\"limpar\"\nSchedule.every:\"10m\":\"limpar\"",
		"sintaxe":                 `Schedule.every:`,
		"argumento faltando":      `Schedule.every:"10m"`,
	}
	for name, src := range cases {
		root := agendaRoot(t, src+"\n", agendaCore)
		_, err := loadAgenda(root)
		if err == nil {
			t.Errorf("%s: devia dar erro", name)
			continue
		}
		if !strings.HasPrefix(err.Error(), "erro") && !strings.Contains(err.Error(), "agenda.djo") {
			t.Errorf("%s: o erro não cita o agenda.djo: %v", name, err)
		}
	}
	// o erro traz a linha
	root := agendaRoot(t, "Schedule.every:\"10m\":\"limpar\"\nSchedule.every:\"10m\":\"nao_existe\"\n", agendaCore)
	if _, err := loadAgenda(root); err == nil || !strings.Contains(err.Error(), "linha 2") || !strings.Contains(err.Error(), "nao_existe") {
		t.Errorf("%v", err)
	}
}

func TestScheduleSoValeNoAgenda(t *testing.T) {
	_, err := run(t, `Schedule.every:"10m":"limpar"`)
	if err == nil || !strings.Contains(err.Error(), "só vale dentro do agenda.djo") {
		t.Errorf("%v", err)
	}
}

func TestAgendaNoBuildEhConferida(t *testing.T) {
	root := agendaRoot(t, "Schedule.every:\"10m\":\"nao_existe\"\n", agendaCore)
	if err := checkAgenda(root); err == nil {
		t.Error("o build devia acusar a agenda")
	}
}
