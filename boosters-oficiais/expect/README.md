# expect

Booster oficial do djo: os matchers do `djo spec`. Pede só a permissão `Spec` (que só existe dentro do `djo spec`) e não toca em arquivo, rede nem banco.

    djo booster add expect

(Enquanto não estiver no registro: `djo booster add caminho/do/djo/boosters-oficiais/expect`.)

```
f it_soma
    expect.eq:(soma:2:3):5

f it_erro
    expect.raises:"soma":["a", 1]:"Int"
```

| Matcher | Passa quando |
|---|---|
| `eq:real:esperado` / `not_eq` | iguais / diferentes, em profundidade (lista, mapa, struct, Date, DateTime) |
| `be_nil:v` `be_true:v` `be_false:v` | `nil`, `true`, `false` |
| `include:colecao:item` | texto contém texto, lista tem o item, mapa tem a chave |
| `gt` `gte` `lt` `lte` `:a:b` | comparação de números |
| `close:real:esperado:tolerancia` | diferença dentro da tolerância |
| `len:colecao:n` `empty:colecao` | tamanho / vazio |
| `starts:texto:prefixo` `ends:texto:sufixo` | começa / termina com |
| `raises:"funcao":[args]:"trecho"` | a função do projeto dá erro de execução (com o trecho na mensagem) |
| `status:resposta:codigo` | código HTTP de uma resposta de `Spec.get/post/...` |
| `json:resposta:"a.b.0":valor` | valor no corpo JSON da resposta |

Quando um matcher falha, ele chama `Spec.fail` com a mensagem, o esperado e o recebido, e o teste para. As mensagens saem em português, ou em inglês com `djo spec --lang en`.

A API é o `main.djo`; o resto (comparação funda, texto das mensagens) fica em `core/` e é interno. Guia completo: [Testes](../../docs/14-testes.md).

Não funciona fora do `djo spec`: qualquer matcher chamado num script ou numa página dá o erro "Spec.fail só existe quando os testes rodam com 'djo spec'".
