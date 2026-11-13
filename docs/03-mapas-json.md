# Mapas e Json

## Mapas

```
map pessoa = {"nome": "Ana", "idade": 30}
pessoa["cidade"] = "Rio"            // adiciona ou troca
print pessoa["nome"]                // Ana
print pessoa["nada"] ?? "sem"       // chave que não existe vale nil
```

- Chaves: `String` ou `Int`. Outro tipo é erro.
- Os valores podem ser qualquer coisa, inclusive listas e outros mapas (`t["b"]["c"]`).
- A ordem em que as chaves entraram é mantida.
- Mapas são compartilhados, como as listas.
- Pode ocupar várias linhas:

```
var tabela = {
    "a": [1, 2],
    "b": {"c": 3}
}
```

Funções: `len:m`, `keys:m` (lista das chaves), `values:m`, `has:m:chave` (`Bool`),
`del:m:chave` (`true` se apagou).

Para percorrer:

```
for{
    each[chave, valor in pessoa]
    print chave + " = " + (str:valor)
}
```

## Json

`Json` é um namespace nativo da linguagem (não precisa de include).

| Chamada            | Faz |
|--------------------|-----|
| `Json.encode:x`    | Converte para texto JSON |
| `Json.pretty:x`    | O mesmo, com indentação |
| `Json.decode:s`    | Converte texto em valores; `nil` se for inválido |
| `Json.valid:s`     | `true` se o texto é JSON válido |

```
map pessoa = {"nome": "Ana", "tags": ["a", "b"]}
var texto = Json.encode:pessoa       // {"nome":"Ana","tags":["a","b"]}
var d = Json.decode:texto
print d["tags"][0]                   // a
```

**Conversões**

| djo                    | JSON      |
|------------------------|-----------|
| `Map`                  | objeto (chaves `Int` viram texto) |
| struct                 | objeto, com os campos em ordem |
| `Arr`                  | array     |
| `Int`, `Float`         | número    |
| `String`, `Bool`, `nil`| igual     |

Na volta, inteiro vira `Int`, decimal vira `Float`, objeto vira `Map` e array vira `Arr`.
Estruturas fundas demais ou com ciclo dão erro ao codificar.

`Json.decode` devolve `nil` em vez de erro para texto inválido, para que entrada ruim
não derrube a página. Se precisar diferenciar de um `null` legítimo, use `Json.valid:`.
