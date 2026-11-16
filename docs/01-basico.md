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
