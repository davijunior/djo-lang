# O básico

Comentários começam com `//`. Uma instrução termina no fim da linha.
Blocos podem usar **indentação** ou **chaves `{ }`**: os dois valem em todo lugar.

## Variáveis e tipos

```
var nome = "Ana"
var idade = 30
var preco = 9.5
var ativo = true
var nada = nil
arr itens = [1, 2, 3]        // lista: o valor precisa ser uma lista
map dados = {"a": 1}         // mapa: o valor precisa ser um mapa
```

| Tipo     | Exemplo            |
|----------|--------------------|
| `Int`    | `42`               |
| `Float`  | `3.14`             |
| `String` | `"texto"`          |
| `Bool`   | `true`, `false`    |
| `Arr`    | `[1, "a"]`         |
| `Map`    | `{"k": 1}`         |
| `Date`   | `Date.make:2025:1:31` (um dia) |
| `DateTime` | `DateTime.now:` (um instante, em UTC) |
| `Any`    | qualquer um        |

`Date` e `DateTime` são tipos de verdade, com contas e comparação; veja [a referência](06-referencia.md#date-e-datetime).

Escapes em strings: `\n`, `\t`, `\"`, `\\`.

### Tipo travado

Uma variável comum aceita qualquer valor. Já `arr` e `map`, e os parâmetros e campos
com tipo, **travam** o tipo: depois disso só recebem valores do mesmo tipo.

```
arr lista = [1]
lista = 5            // erro: 'lista' é Arr e não pode receber Int
```

## Operadores

- Aritmética: `+ - * / %`. A divisão entre `Int` é inteira (`7 / 2` dá `3`).
- `Int` vira `Float` quando preciso (`1 + 0.5`).
- Comparação: `== != < > <= >=`.
- Lógicos: `and`, `or`, `not`. As condições precisam ser `Bool`.
- `a ?? b`: usa `b` se `a` for `nil`.
- `i++` e `i--`.
- `pick[cond ; a ; b]`: escolhe `a` se `cond` for verdadeira, senão `b`.

### Texto e números

Não há conversão automática, **com uma exceção**: o `+` entre texto e número lê o
texto como número.

```
print 2 + 2          // 4
print 2 + "2"        // 4
print "2" + "2"      // 22  (dois textos: concatena)
print "a" + 1        // erro: "a" não é número
```

Para juntar um número a um texto, use `str:`: `"id " + (str:n)`. Para o caminho inverso,
`int:"42"` e `float:"2.5"` convertem (e dão `nil` se o texto não for número). A lista completa
de funções de texto está na [Referência](06-referencia.md).

## Funções

```
f saudar                 // sem parâmetros
    print "oi"

f soma{
    params[n1[Int], n2]  // o tipo é opcional (sem tipo = Any)
    return n1 + n2
}

saudar:                  // chamada sem argumentos
print soma:3:2           // 5
```

- Chamada: `nome:arg1:arg2`.
- Um parâmetro com tipo é validado e fica travado dentro da função.
- Quem não usa `return` devolve `nil`.
- **Cuidado com `+` nos argumentos.** O último argumento engole o que vem depois,
  então `len:"olá" + len:[1,2]` lê tudo como um argumento só. Use parênteses:
  `(len:"olá") + (len:[1,2])`.

## Condicionais

```
if
    cond[idade >= 18]
    print "adulto"
elif
    cond[idade >= 12]
    print "jovem"
else
    print "criança"
```

Com chaves: `if{ cond[...] ... } elif{ cond[...] ... } else{ ... }`.

### match

```
match
    val[nota]
    case[10] print "perfeito"
    case[8, 9] print "ótimo"
    else print "continue estudando"
```

## Laços

`while` repete enquanto a condição for verdadeira. O `params` declara variáveis que
nascem uma vez, antes do laço:

```
while{
    cond[i < 3]
    params[var i = 0]
    print i
    i++
}
```

`until` repete **até** a condição ficar verdadeira. O `eng` traz início, condição e passo:

```
until{
    eng[i = 0 ; i == 4 ; i++]
    print i
}
```

`for` percorre listas, mapas e textos:

```
for{
    each[fruta in ["maçã", "uva"]]
    print fruta
}

for{
    each[chave, valor in pessoa]       // mapa: chave e valor
    print chave + "=" + (str:valor)
}

for{ each[i in range:0:5] print i }    // 0 a 4 (o fim não entra)
```

- Em lista, `each[x in lista]` dá cada item; `each[i, x in lista]` dá também a posição.
- Em mapa, `each[k in mapa]` dá as chaves; `each[k, v in mapa]` dá chave e valor.
- Em texto, percorre as letras.
- `range:a:b` cria a lista de `a` até `b - 1` (máximo de 1 milhão de itens).
- O `for` percorre uma cópia: mudar a lista dentro do laço não muda a volta.
- Um `return` dentro do `for` sai da função.

## Listas

```
arr x = [10, 20, 30]
print x[0]            // 10
x[1] = 99
push:x:40
print len:x           // 4
```

Um índice fora da lista é erro. Listas são compartilhadas: `var y = x` aponta para a
mesma lista.

## Entrada

```
var nome = input "Qual seu nome? "
```

## Arquivos

`include:"outro.djo"` executa outro arquivo (o caminho é sempre dentro da pasta do site).

## Erros e promises

Um erro de execução (uma divisão por zero, um índice fora da lista, uma chamada que falha) derruba a página com um 500, ou faz o job tentar de novo. O `promise` deixa você **tentar uma coisa e decidir o que fazer se ela falhar**, sem derrubar nada:

```
promise{
    var r = Http.get:"https://api.exemplo.com/clientes"
    if
        cond[not r["ok"]]
        reject:"api_fora":"a API não respondeu":{"status": r["status"]}
    return Json.decode:r["body"]
}success{
    [dados]
    print len:dados["itens"]
}error{
    [err]
    print "falhou: " + err["msg"]
}finally{
    print "Finally!"
}
```

**Os blocos.** A ordem é sempre `promise`, `success`, `error`, `finally`. O `promise` é obrigatório, e precisa de pelo menos um dos outros três.

- `promise{ ... }` é a tentativa.
- `success{ [dados] ... }` roda se o `promise` deu certo. A primeira linha do bloco, `[dados]`, dá nome ao valor que o `promise` devolveu.
- `error{ [err] ... }` roda se o `promise` deu erro. `[err]` é o erro (veja abaixo).
- `finally{ ... }` roda no fim, deu certo ou errado. Serve para limpar. Não tem parâmetro e não aceita `return`.

O `[nome]` é opcional: `success{ ... }` e `error{ ... }` valem sem ele. Na forma indentada, sem chaves, as cláusulas ficam na coluna do `promise`:

```
promise
    return 1 / 0
error
    [err]
    print err["msg"]
```

**O `return` sai do bloco.** Dentro de um `promise`, de um `success` ou de um `error`, o `return` termina **aquele bloco** com um valor. Ele não sai da função que contém o promise. O `return` do `promise` é o valor que o `success` recebe.

**O valor do promise.** Um promise pode ser usado como valor:

```
var p = promise{
    return Json.decode:texto
}success{
    [dados]
    return dados["itens"]
}error{
    [err]
    return []
}
print len:p
```

O valor é o `return` do `success` (se deu certo), ou o `return` do `error` (se deu erro). Sem `success`, vale o `return` do próprio `promise`. Sem nenhum `return`, é `nil`. O `finally` nunca muda o valor. Também vale `return promise{ ... }success{ ... }`.

**O erro que o `error` recebe** é um mapa:

| Campo | O que é |
|---|---|
| `msg` | a mensagem |
| `line` | a linha onde o erro nasceu |
| `code` | `"erro"` nos erros da própria djo (tipo errado, índice fora, divisão por zero, banco...); nos erros que você lança, o código que você deu |
| `data` | `nil` nos erros da djo; nos seus, os dados que você mandou |
| `file` | `"boosters/nome"` se o erro nasceu dentro de um booster (a `line` é a do arquivo do booster), senão `nil` |

**Lançar um erro: `reject:`.**

```
reject:"mensagem"
reject:"codigo":"mensagem"
reject:"codigo":"mensagem":dados
reject:err                      // relança o erro que o error{ [err] } recebeu
```

O código `"erro"` é reservado aos erros da linguagem: `reject:"erro":"x"` dá erro. `reject:` vale em qualquer lugar, dentro de funções também. Fora de um `promise`, ele é um erro de página comum.

**O erro não some em silêncio.**

- Sem `error`, o erro **continua subindo** (depois de rodar o `finally`): numa página vira 500, num job a tentativa falha e ele repete. Para ignorar um erro de propósito, escreva `error{ }` vazio.
- O `error` **não pega os erros do `success`**. Um erro dentro do `success`, do `error` ou do `finally` sobe (o `finally` ainda roda). Se o `finally` falhar, o erro dele vence o que estava pendente.

**O que o promise não captura.** Alguns erros existem para proteger o servidor e passam direto: o limite de passos e o de tempo, o limite de chamadas `Http` por página ou job, a recursão sem fim. Nesses casos o `error` **e o `finally` não rodam** (um laço com `promise` não pode furar o limite). Por isso, se o `finally` solta uma marca guardada fora da djo (um "rodando = 1" no banco), dê a ela um prazo de validade.
Também passam, e o `finally` ainda roda antes: `Job.give_up`, `redirect` e `Spec.fail`/`Spec.skip`.

**Ponto de salvamento.** Se o `promise` falha, o que ele **abriu** é desfeito: uma transação aberta com `Db.begin` dentro dele leva `rollback`, e os jobs que ele enfileirou com `Job.run` são descartados. O que foi feito antes do `promise` continua valendo, e o que já saiu para o mundo (um e-mail, um `Http.post`) não volta.

### A saída, os erros e `uniq`

**A saída (`print`) de um promise entra na página no lugar onde ele foi escrito**, na ordem do código:

```
print "A"
promise{
    print "dentro"
}success{
    print "success"
}
print "B"
```

A saída é `A`, `dentro`, `success`, `B`. Vale para o promise como instrução, para `var p = promise{...}` e para `return promise{...}`.

**Se der erro, a saída do que falhou não aparece.** É a mesma ideia do ponto de salvamento do banco:

- Se o **corpo** falha, o que ele imprimiu antes da falha é descartado. O `error{}` e o `finally{}` imprimem normalmente (é o tratamento).
- Se um erro **vai subir** do promise (sem `error`, ou porque o `success`, o `error` ou o `finally` falharam), a saída do corpo, do `success` e do `error` é descartada. **Só a do `finally` fica**, porque ele roda sempre.
- Um promise dentro do corpo de outro que falha perde a saída junto com o corpo.
- Num limite fatal (passos, tempo, `Http`, recursão), nada do promise aparece: a página já termina em erro.
- A regra é a mesma dentro de função, de job (o log do job) e de `action`, e com `uniq`.

**Ordem.** A ordem da **saída** é sempre a do código. A ordem de **execução** é outra história: `promise` sem `uniq` **não dá garantia de ordem** em relação ao resto do código, porque a djo pode vir a rodá-lo ao mesmo tempo que ele. **Hoje ainda é sequencial**: cada promise roda inteiro, na hora, um depois do outro. O que muda é só o resultado, que chega na **espera**: o primeiro uso da variável (`print p`, `p["x"]`, passá-la a uma função) ou o fim da função (da página, da `action`, do job) que o criou. Um erro que ninguém tratou também sobe só na espera, e um promise nunca fica solto: ele termina antes de a função sair.

`uniq promise{ ... }` **espera o promise se resolver antes de continuar**: roda e entrega o resultado (e o erro) ali mesmo. Use `uniq` quando a ordem de execução importa.

```
var a = promise{
    print "buscando A"
    return 1
}error{
    return 0
}
print "meio"
print a          // a saída é: buscando A, meio, 1

uniq promise{
    print "agora"
}success{
    print "e já terminou"
}
```

### Isolamento: o bloco trabalha com cópias

Para que a ordem não mude o resultado, o `promise` (e o `success`, o `error` e o `finally` dele) **lê uma cópia** das variáveis de fora e **não escreve nelas**. O resultado sai só pelo `return`.

```
var total = 10
uniq promise{
    print total + 1        // lê a cópia: 11
    total = 5              // erro: o promise não altera variáveis de fora ('total')
}success{
}
```

- Listas, mapas e structs são copiados por inteiro: mexer na cópia (`push:lista:x`) não muda a original. `lista[0] = x` e `obj.campo = x` numa variável de fora dão erro; mexer na lista só pelo `push:` altera a cópia sem avisar. Devolva o que mudou com `return`.
- Uma função chamada de dentro do promise também só lê as variáveis globais.
- Variáveis criadas dentro do bloco (`var x = ...`) são só dele: o `success` e o `error` não as enxergam, só recebem o `[nome]`.
- Dentro de um promise **não** se altera a resposta nem a sessão da página: `Session.set`, `Session.del`, `Session.clear`, `Session.destroy`, `Session.renew`, `redirect`, `status`, `setheader`, `setcookie`, `Http.json` e `Page.headers` dão erro. Faça isso fora do promise. Ler a requisição e a sessão vale.
- Com um `Db.begin` aberto **fora** do promise, o promise não pode usar o `Db` (erro claro): use o banco antes do `begin` ou depois do `commit`.

### As palavras

`promise`, `uniq`, `success`, `error` e `finally` **não são reservadas**: só valem na posição certa, como `val` e `case` do `match`. `var error = 1`, `r["error"]` (o campo que o `Http.get` devolve) e `{"success": true}` continuam funcionando.
