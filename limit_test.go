package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func limitClock(t *testing.T, start int64) *int64 {
	t.Helper()
	now := start
	limitNow = func() time.Time { return time.Unix(atomic.LoadInt64(&now), 0) }
	t.Cleanup(func() { limitNow = time.Now })
	return &now
}

func TestLimitRajadaEJanela(t *testing.T) {
	now := limitClock(t, 1000*60) // começo de janela de 60 s
	l := &limiter{}
	for i := int64(0); i < 5; i++ {
		r := l.take("tok:a", 5, 60)
		if !r.allowed || r.remaining != 4-i {
			t.Fatalf("%d: %+v", i, r)
		}
	}
	r := l.take("tok:a", 5, 60)
	if r.allowed || r.retry < 1 || r.retry > 60 || r.remaining != 0 {
		t.Fatalf("6ª deveria recusar: %+v", r)
	}
	if !l.take("tok:b", 5, 60).allowed { // outra chave não é afetada
		t.Fatal("chaves são independentes")
	}
	// 30 s depois: ainda na mesma janela, continua recusando
	atomic.AddInt64(now, 30)
	if l.take("tok:a", 5, 60).allowed {
		t.Error("ainda na janela")
	}
	// na janela seguinte, logo no início, o peso da anterior ainda conta (janela deslizante)
	atomic.StoreInt64(now, 1000*60+60)
	if l.take("tok:a", 5, 60).allowed {
		t.Error("no começo da janela nova a anterior ainda pesa")
	}
	// meio da janela seguinte: a anterior pesa 50%, cabem ~2
	atomic.StoreInt64(now, 1000*60+90)
	ok := 0
	for i := 0; i < 5; i++ {
		if l.take("tok:a", 5, 60).allowed {
			ok++
		}
	}
	if ok < 2 || ok > 3 {
		t.Errorf("esperava 2 a 3 liberadas no meio da janela seguinte, vieram %d", ok)
	}
	// duas janelas depois: tudo livre
	atomic.StoreInt64(now, 1000*60+60*3)
	if r := l.take("tok:a", 5, 60); !r.allowed || r.remaining != 4 {
		t.Errorf("%+v", r)
	}
}

func TestLimitTabelaCheiaNaoExpulsaChaveViva(t *testing.T) {
	now := limitClock(t, 5000*60)
	l := &limiter{}
	l.take("tok:legitimo", 2, 60)
	l.take("tok:legitimo", 2, 60) // gastou o limite
	allowed, refused := 0, 0
	for i := 0; i < 100000; i++ {
		r := l.take(fmt.Sprintf("atacante:%d", i), 10, 60)
		switch {
		case r.allowed:
			allowed++
		case r.full:
			refused++
		default:
			t.Fatalf("recusa sem ser por tabela cheia: %+v", r)
		}
	}
	if allowed != limitMaxKeys-1 || refused != 100000-(limitMaxKeys-1) {
		t.Fatalf("liberadas %d, recusadas por cheio %d", allowed, refused)
	}
	if len(l.m) > limitMaxKeys {
		t.Fatalf("a tabela passou do teto: %d", len(l.m))
	}
	// o contador do legítimo NÃO foi expulso: ele continua no limite (não ganhou crédito)
	if r := l.take("tok:legitimo", 2, 60); r.allowed {
		t.Fatalf("a chave viva foi expulsa e o limite zerou: %+v", r)
	}
	// uma chave nova recebe "cheio" (fail closed), com o motivo
	if r := l.take("novo", 2, 60); r.allowed || !r.full {
		t.Fatalf("%+v", r)
	}
	// passadas as janelas, a varredura libera espaço
	atomic.AddInt64(now, 60*5)
	if r := l.take("novo", 2, 60); !r.allowed {
		t.Fatalf("depois de expirar tinha de liberar: %+v", r)
	}
}

func TestLimitConcorrente(t *testing.T) {
	limitClock(t, 9000*60)
	l := &limiter{}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if l.take("k", 100, 60).allowed {
					ok.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 100 {
		t.Fatalf("liberadas %d de 500, esperado exatamente 100", ok.Load())
	}
}

func TestLimitArgumentosInvalidos(t *testing.T) {
	for _, src := range []string{
		"Limit.take:\"k\":0:60\n", "Limit.take:\"k\":5:0\n", "Limit.take:\"k\":5:86401\n",
		"Limit.take:\"\":5:60\n", "Limit.take:1:5:60\n", "Limit.take:\"k\":\"5\":60\n", "Limit.take:\"k\":5\n",
	} {
		err := runDjo(t, nil, src)
		var de *djoError
		if err == nil || !strings.Contains(err.Error(), "Limit.take") {
			t.Errorf("%q: %v", src, err)
		}
		_ = de
	}
	if err := runDjo(t, nil, "var r = Limit.take:\"k\":5:60\nprint r\n"); err != nil {
		t.Error(err)
	}
}

// A chave nunca fica guardada em texto: só o SHA-256.
func TestLimitNaoGuardaAChave(t *testing.T) {
	limitClock(t, 1000)
	l := &limiter{}
	l.take("segredo-do-token", 5, 60)
	for k := range l.m {
		if strings.Contains(fmt.Sprintf("%x %v", k, k), "segredo") {
			t.Fatal("a chave apareceu em claro")
		}
	}
}
