package main

import (
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func lockEmpty(t *testing.T) {
	t.Helper()
	lockTab.mu.Lock()
	n := len(lockTab.m)
	lockTab.mu.Unlock()
	if n != 0 {
		t.Errorf("sobraram %d chave(s) na tabela de travas", n)
	}
}

func TestLockExclusaoMutuaEFIFO(t *testing.T) {
	defer lockEmpty(t)
	var active, maxActive atomic.Int32
	var order []int
	var omu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, ok := lockAcquire("k", 10*time.Second, 30*time.Second)
			if !ok {
				t.Errorf("%d: não conseguiu", i)
				return
			}
			if n := active.Add(1); n > maxActive.Load() {
				maxActive.Store(n)
			}
			omu.Lock()
			order = append(order, i)
			omu.Unlock()
			time.Sleep(time.Millisecond)
			active.Add(-1)
			lockRelease(h)
		}()
		time.Sleep(2 * time.Millisecond) // chegada escalonada: a ordem de chegada é conhecida
	}
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatalf("%d donas ao mesmo tempo", maxActive.Load())
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("a fila não é FIFO: %v", order)
		}
	}
}

func TestLockChavesDiferentesEmParalelo(t *testing.T) {
	defer lockEmpty(t)
	a, ok1 := lockAcquire("empresa:1", 0, time.Minute)
	b, ok2 := lockAcquire("empresa:2", 0, time.Minute)
	if !ok1 || !ok2 {
		t.Fatal("chaves diferentes têm de entrar juntas")
	}
	if _, ok := lockAcquire("empresa:1", 0, time.Minute); ok {
		t.Fatal("a mesma chave entrou duas vezes")
	}
	lockRelease(a)
	lockRelease(b)
}

func TestLockOcupadaEFilaCheia(t *testing.T) {
	defer lockEmpty(t)
	h, _ := lockAcquire("k", 0, time.Minute)
	start := time.Now()
	if _, ok := lockAcquire("k", 60*time.Millisecond, time.Minute); ok || time.Since(start) < 50*time.Millisecond {
		t.Fatalf("devia esperar e desistir (%s)", time.Since(start))
	}
	// 100 esperando: o 101º recusa na hora
	var wg sync.WaitGroup
	for i := 0; i < lockMaxWaiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h2, ok := lockAcquire("k", 5*time.Second, time.Minute); ok {
				lockRelease(h2)
			}
		}()
	}
	for tries := 0; tries < 200; tries++ {
		lockTab.mu.Lock()
		n := len(lockTab.m["k"].q)
		lockTab.mu.Unlock()
		if n == lockMaxWaiters {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	start = time.Now()
	if _, ok := lockAcquire("k", 5*time.Second, time.Minute); ok || time.Since(start) > time.Second {
		t.Fatalf("o 101º devia recusar na hora")
	}
	lockRelease(h)
	wg.Wait()
}

func lockProg(t *testing.T, src string) error {
	t.Helper()
	in := NewInterp(io.Discard)
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	return in.Run(prog)
}

// Toda saída do bloco solta a trava, e a próxima execução entra.
func TestLockTodaSaidaLiberaATrava(t *testing.T) {
	nativeNS("Zz")["boom"] = builtin{0, func(in *Interp, env *Env, line int, a []any) any { panic(haltSignal{}) }}
	cases := map[string]string{
		"return":             "f a\n    lock{\n        key[\"k\"]\n        wait[0]\n        return 1\n    }\nprint a:\n",
		"return em if e for": "f a\n    lock{\n        key[\"k\"]\n        wait[0]\n        for{\n            each[x in [1, 2]]\n            if\n                cond[x == 2]\n                    return x\n        }\n    }\nprint a:\n",
		"erro":               "lock{\n    key[\"k\"]\n    wait[0]\n    var x = 1 / 0\n}\n",
		"reject":             "lock{\n    key[\"k\"]\n    wait[0]\n    reject:\"x\":\"falhou\"\n}\n",
		"halt":               "lock{\n    key[\"k\"]\n    wait[0]\n    Zz.boom:\n}\n",
		"promise falho":      "promise{\n    lock{\n        key[\"k\"]\n        wait[0]\n        reject:\"x\":\"falhou\"\n    }\n}error{\n    [e]\n    print e[\"code\"]\n}\n",
		"promise ok":         "promise{\n    lock{\n        key[\"k\"]\n        wait[0]\n        print 1\n    }\n}success{\n    [v]\n    print 2\n}\n",
		"aninhado":           "lock{\n    key[\"a\"]\n    wait[0]\n    lock{\n        key[\"b\"]\n        wait[0]\n        var x = 1 / 0\n    }\n}\n",
	}
	for nome, src := range cases {
		lockProg(t, src) // o resultado (erro ou não) não importa: o que importa é soltar
		lockEmpty(t)
		// a próxima execução entra na mesma chave
		if err := lockProg(t, "lock{\n    key[\"k\"]\n    wait[0]\n    var ok = 1\n}\nlock{\n    key[\"a\"]\n    wait[0]\n    var ok = 1\n}\nlock{\n    key[\"b\"]\n    wait[0]\n    var ok = 1\n}\n"); err != nil {
			t.Errorf("%s: a trava não foi solta: %v", nome, err)
		}
	}
	// limite fatal de passos dentro do lock
	in := NewInterp(io.Discard)
	in.maxSteps = 200
	prog, _ := Parse("lock{\n    key[\"k\"]\n    wait[0]\n    var i = 0\n    while{\n        cond[true]\n        i++\n    }\n}\n")
	var de *djoError
	if err := in.Run(prog); !errors.As(err, &de) || !de.Fatal {
		t.Errorf("devia estourar o limite de passos: %v", err)
	}
	lockEmpty(t)
}

func TestLockBusyTemCodigoEDadosECapturaNoPromise(t *testing.T) {
	h, _ := lockAcquire("omie:42:Listar", 0, time.Minute)
	defer lockRelease(h)
	err := lockProg(t, "lock{\n    key[\"omie:42:Listar\"]\n    wait[0]\n    print \"nao\"\n}\n")
	var de *djoError
	if !errors.As(err, &de) || de.Code != "lock_busy" {
		t.Fatalf("%v", err)
	}
	if m, ok := de.Data.(*Map); !ok {
		t.Fatalf("data %T", de.Data)
	} else if k, _ := m.get("key"); k != "omie:42:Listar" {
		t.Errorf("%v", k)
	}
	out := &strings.Builder{}
	in := NewInterp(out)
	prog, _ := Parse("promise{\n    lock{\n        key[\"omie:42:Listar\"]\n        wait[0]\n        print \"nao\"\n    }\n}error{\n    [e]\n    print e[\"code\"] + \" \" + e[\"data\"][\"key\"]\n}\n")
	if err := in.Run(prog); err != nil || out.String() != "lock_busy omie:42:Listar\n" {
		t.Errorf("%q %v", out.String(), err)
	}
}

func TestLockReentranteEArgumentos(t *testing.T) {
	defer lockEmpty(t)
	for src, code := range map[string]string{
		"lock{\n    key[\"a\"]\n    wait[0]\n    lock{\n        key[\"a\"]\n        wait[0]\n        print 1\n    }\n}\n": "lock_reentrant",
		"lock{\n    key[1]\n    wait[0]\n    print 1\n}\n":                                                                "lock_args",
		"lock{\n    key[\"\"]\n    wait[0]\n    print 1\n}\n":                                                             "lock_args",
		"lock{\n    key[\"a\"]\n    wait[\"xx\"]\n    print 1\n}\n":                                                       "lock_args",
		"lock{\n    key[\"a\"]\n    wait[-1]\n    print 1\n}\n":                                                           "lock_args",
		"lock{\n    key[\"a\"]\n    wait[61]\n    print 1\n}\n":                                                           "lock_args",
	} {
		var de *djoError
		if err := lockProg(t, src); !errors.As(err, &de) || de.Code != code {
			t.Errorf("%q: %v", src, err)
		}
	}
	for _, bad := range []string{"lock{\n    wait[0]\n    print 1\n}\n", "lock{\n    key[\"a\"]\n    print 1\n}\n", "lock{\n    key[\"a\"]\n    key[\"b\"]\n    wait[0]\n}\n"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("devia recusar: %q", bad)
		}
	}
	// lock não é palavra reservada
	if err := lockProg(t, "var lock = 3\nprint lock\n"); err != nil {
		t.Error(err)
	}
}

// A espera numa página conta na cota de 2 s do Time.sleep; wait acima dela é erro claro.
func TestLockEsperaDePaginaTemTeto(t *testing.T) {
	in := NewInterp(io.Discard)
	in.served = true
	prog, _ := Parse("lock{\n    key[\"k\"]\n    wait[3]\n    print 1\n}\n")
	if err := in.Run(prog); err == nil || !strings.Contains(err.Error(), "2 segundos") {
		t.Errorf("%v", err)
	}
	in2 := NewInterp(io.Discard)
	in2.served = true
	in2.slept = 1500
	prog, _ = Parse("lock{\n    key[\"k\"]\n    wait[1]\n    print 1\n}\n")
	if err := in2.Run(prog); err == nil || !strings.Contains(err.Error(), "não cabe") {
		t.Errorf("já tinha esperado 1,5 s: %v", err)
	}
	h, _ := lockAcquire("k", 0, time.Minute)
	in3 := NewInterp(io.Discard)
	in3.served = true
	prog, _ = Parse("lock{\n    key[\"k\"]\n    wait[1]\n    print 1\n}\n")
	go func() {
		time.Sleep(300 * time.Millisecond)
		lockRelease(h)
	}()
	err := in3.Run(prog)
	if err != nil || in3.slept < 250 {
		t.Errorf("a espera (%d ms) devia entrar na cota: %v", in3.slept, err)
	}
}

// Revogação: a dona lenta é cedida ao fim do prazo e recebe lock_revoked no passo seguinte.
func TestLockRevogacaoNuncaDuasDonasRodando(t *testing.T) {
	defer lockEmpty(t)
	lockLeaseOverride = 80 * time.Millisecond
	defer func() { lockLeaseOverride = 0 }()
	var mu sync.Mutex
	var marks []int
	nativeNS("Zz")["mark"] = builtin{1, func(in *Interp, env *Env, line int, a []any) any {
		mu.Lock()
		marks = append(marks, int(a[0].(int64)))
		mu.Unlock()
		return nil
	}}
	var wg sync.WaitGroup
	var err1, err2 error
	wg.Add(2)
	go func() { // dona lenta: um laço sem Http e sem fim aparente
		defer wg.Done()
		err1 = lockProg(t, "lock{\n    key[\"k\"]\n    wait[0]\n    var i = 0\n    while{\n        cond[i < 400]\n        Zz.mark:1\n        Time.sleep:2\n        i++\n    }\n}\n")
	}()
	time.Sleep(20 * time.Millisecond)
	go func() { // a 2ª espera na fila e entra quando o prazo da 1ª vence
		defer wg.Done()
		err2 = lockProg(t, "lock{\n    key[\"k\"]\n    wait[5]\n    Zz.mark:2\n    Time.sleep:5\n    Zz.mark:2\n}\n")
	}()
	wg.Wait()
	var de *djoError
	if !errors.As(err1, &de) || de.Code != "lock_revoked" || !de.Fatal {
		t.Fatalf("a dona lenta devia receber lock_revoked: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("a 2ª devia ter entrado: %v", err2)
	}
	first2 := -1
	for i, m := range marks {
		if m == 2 {
			first2 = i
			break
		}
	}
	if first2 < 0 {
		t.Fatal("a 2ª nunca rodou")
	}
	after := 0
	for _, m := range marks[first2:] {
		if m == 1 {
			after++
		}
	}
	if after > 1 { // no máximo o passo que já estava em andamento
		t.Fatalf("a 1ª ainda rodou %d passos depois de ceder a trava: %v", after, marks)
	}
}
