# O booster `expect`

Os matchers do [`djo spec`](14-testes.md): funções que conferem um valor e, se ele não bate, **falham o teste** mostrando o esperado e o recebido. Pede **só a permissão `Spec`**, que só existe dentro do `djo spec`. Não toca em arquivo, rede nem banco (a regra geral dos boosters está em [Boosters](12-boosters.md)).

```
djo booster add expect       # uma vez por projeto
djo spec                     # roda spec/**/*_spec.djo
```

O `djo booster add` avisa, ao instalar, `atenção: ele pede permissão para usar Spec`. É esperado: é a única permissão dele.

## Como funciona

Um teste é uma função `it_...` num arquivo `*_spec.djo`. Cada matcher segue o formato `expect.nome:real:esperado`, com o valor que o seu código produziu primeiro e o que você esperava depois:

```
// spec/soma_spec.djo
f it_soma_dois_numeros
    expect.eq:(soma:2:3):5

f it_soma_rejeita_texto
    expect.raises:"soma":["a", 1]:"Int"
```

- Se o valor **bate**, o matcher não faz nada e o teste segue para a linha seguinte.
- Se **não bate**, o matcher chama `Spec.fail` com a mensagem, o esperado e o recebido, e o teste **para ali** (as linhas depois dele não rodam). O relatório mostra a linha do `expect` no arquivo de spec.
- O nome do teste vem da função: `it_soma_dois_numeros` aparece como `soma dois numeros`.
- Um matcher usado com o tipo errado (`expect.gt:"a":1`) também falha o teste, com uma mensagem própria (veja a tabela de [mensagens](#mensagens-de-falha)).
- Chamar com o número errado de argumentos é **erro**, não falha: `expect.eq:1` dá `'expect.eq' espera 2 argumento(s), recebeu 1`, e o teste aparece como `[erro]`.

Um relatório de falha, real:

```
  1) status errado
     spec/rotas_spec.djo:9
     código HTTP diferente
     esperado: 404
     recebido: 200 "{\"titulo\":\"estudar\",\"itens\":[{\"id\":7},{\"id\":8}]}"
```

No `--json` os mesmos dados saem em `mensagem`, `esperado`, `recebido` e `linha` de cada teste (veja [Testes](14-testes.md)).

## Os matchers

Todos recebem o **valor real primeiro**. Os exemplos abaixo passam; as mensagens de falha de cada um estão na [seção seguinte](#mensagens-de-falha).

| Matcher | Passa quando | Exemplo |
|---|---|---|
| `eq:real:esperado` | os valores são iguais **em profundidade** | `expect.eq:[1, {"a": 2}]:[1, {"a": 2}]` |
| `not_eq:real:esperado` | os valores são diferentes | `expect.not_eq:"a":"b"` |
| `be_nil:v` | `v` é `nil` | `expect.be_nil:nil` |
| `be_true:v` | `v` é o `Bool` `true` (o número `1` **não** vale) | `expect.be_true:(2 > 1)` |
| `be_false:v` | `v` é o `Bool` `false` (`nil` **não** vale) | `expect.be_false:false` |
| `include:colecao:item` | o texto contém o texto, a lista tem o item ou o mapa tem a chave | `expect.include:[1, 2, 3]:2` |
| `gt:a:b` | `a > b` (só números) | `expect.gt:3:2` |
| `gte:a:b` | `a >= b` | `expect.gte:3:3` |
| `lt:a:b` | `a < b` | `expect.lt:1:2` |
| `lte:a:b` | `a <= b` | `expect.lte:2:2` |
| `close:real:esperado:tolerancia` | a diferença absoluta é no máximo a tolerância | `expect.close:0.30000000000000004:0.3:0.0001` |
| `len:colecao:n` | a lista, o mapa ou o texto tem `n` itens (ou letras) | `expect.len:"abc":3` |
| `empty:colecao` | a lista, o mapa ou o texto está vazio | `expect.empty:[]` |
| `starts:texto:prefixo` | o texto começa com o prefixo | `expect.starts:"djo lang":"djo"` |
| `ends:texto:sufixo` | o texto termina com o sufixo | `expect.ends:"djo lang":"lang"` |
| `raises:"funcao":[args]:"trecho"` | a função do projeto dá **erro de execução** e a mensagem tem o trecho (`""` aceita qualquer erro) | `expect.raises:"soma":["a", 1]:"Int"` |
| `status:resposta:codigo` | a resposta de `Spec.get` (ou `post`, `put`, `patch`, `delete`) tem esse código HTTP | `expect.status:r:200` |
| `json:resposta:"caminho":valor` | o corpo JSON da resposta tem esse valor no caminho | `expect.json:r:"itens.1.id":8` |

### O que `eq` considera igual

A comparação é funda (a mesma de `not_eq`, `include` em listas e `json`):

- **Listas**: mesmo tamanho e itens iguais **na mesma ordem**. `[1, 2]` e `[2, 1]` são diferentes.
- **Mapas**: mesmas chaves e valores, **sem olhar a ordem das chaves**.
- **Números**: `1` e `1.0` são iguais. `"1"` (texto) e `1` (número) são diferentes.
- **`Date` e `DateTime`**: cada um só é igual ao do mesmo tipo e instante; **`Date` e `DateTime` nunca são iguais**, mesmo no mesmo dia.
- **Structs**: iguais quando o texto de todos os campos é igual (`expect.eq:(Ponto:1:2):(Ponto:1:2)`). Uma `struct` nunca é igual a uma lista, mapa, `Date` ou `DateTime`.
- `nil` só é igual a `nil`.

### `close`, para decimais

Números decimais pedem tolerância (`0.1 + 0.2` não é exatamente `0.3`):

```
f it_decimais
    expect.close:(0.1 + 0.2):0.3:0.0001
```

Os três argumentos precisam ser números (`Int` ou `Float`).

### `raises`, para erros esperados

`expect.raises:"nome":[argumentos]:"trecho"` chama a função `nome` **do projeto** com a lista de argumentos e espera um **erro de execução**. Passa se ele ocorrer e a mensagem contiver o `trecho`; com `""`, qualquer erro serve.

```
f it_divisao_por_zero
    expect.raises:"dividir":[1, 0]:"zero"
```

- Só vale para funções do projeto (`core/` ou `.djo`). Uma função que não existe é **erro** (não falha): `Spec.try: a função 'nao_existe' não existe no projeto (só as funções do djo valem, sem as nativas)`.
- Se a função termina bem, o teste falha (`esperava um erro, mas a função terminou bem`).

### `status` e `json`, para rotas

Funcionam com a resposta de `Spec.get:"/rota"` (e `post`, `put`, `patch`, `delete`), que o `djo spec` roda em processo, sem rede (veja [Testes](14-testes.md)):

```
f it_api_de_tarefas
    var r = Spec.get:"/api"
    expect.status:r:200
    expect.json:r:"titulo":"estudar"
    expect.json:r:"itens.1.id":8
    expect.json:r:"nao.existe":nil
```

O caminho do `json` junta com ponto as chaves de mapa e as posições de lista (a partir de 0): `"itens.1.id"` é `corpo["itens"][1]["id"]`. Um caminho que não existe vale `nil` (por isso `expect.json:r:"nao.existe":nil` passa). Se o corpo não for JSON, o teste falha. Se o primeiro argumento não for uma resposta de `Spec.get` e companhia, o teste falha com `esperava a resposta de Spec.get/post/put/patch/delete`.

### O que o `expect` não tem

- Só `not_eq` tem a forma negada. Para negar os outros, confira uma condição: `expect.be_false:(contains:texto:"x")` ou `expect.be_true:(a != b)`.
- `gt`, `gte`, `lt` e `lte` só comparam números. Para datas, use `expect.be_true:(a < b)`.
- Não há matcher para o corpo de uma rota que não seja JSON, nem para cabeçalhos: use `expect.eq` sobre o mapa devolvido (`r["body"]`, `r["status"]`).

## Mensagens de falha

Cada matcher falha com uma mensagem, e a maioria mostra o esperado e o recebido. Saídas reais, em português e em inglês:

| Matcher | Mensagem em português | Mensagem em inglês |
|---|---|---|
| `eq` | `os valores são diferentes` | `the values differ` |
| `not_eq` | `os valores deviam ser diferentes` (esperado `≠ valor`) | `the values should differ` |
| `be_nil` | `esperava nil` | `expected nil` |
| `be_true` | `esperava true` | `expected true` |
| `be_false` | `esperava false` | `expected false` |
| `include` | `não inclui o item` (esperado `incluir item`) | `does not include the item` (expected: `include 3`) |
| `include` com texto e item não texto | `com um texto, include espera outro texto` | `with a string, include expects another string` |
| `include` com outro tipo | `include espera texto, lista ou mapa, mas veio 42` | `include expects a string, list or map, got 42` |
| `gt` | `esperava maior` (esperado `> 2`) | `expected greater` |
| `gte` | `esperava maior ou igual` (esperado `>= 2`) | `expected greater or equal` |
| `lt` | `esperava menor` (esperado `< 1`) | `expected less` |
| `lte` | `esperava menor ou igual` (esperado `<= 1`) | `expected less or equal` |
| `gt` e companhia com texto | `esperava um número em gt, mas veio "a"` | `expected a number in gt, got "a"` |
| `close` | `fora da tolerância` (esperado `1.0 ± 0.1`) | `outside the tolerance` |
| `len` | `tamanho diferente` (esperado `3`, recebido `2`) | `different length` |
| `len` com outro tipo | `len espera lista, mapa ou texto, mas veio 42` | `len expects a list, map or string, got 42` |
| `empty` | `esperava vazio` (esperado `vazio`) | `expected empty` (esperado `empty`) |
| `empty` com outro tipo | `empty espera lista, mapa ou texto, mas veio nil` | `empty expects a list, map or string, got nil` |
| `starts` | `não começa com o prefixo` | `does not start with the prefix` |
| `ends` | `não termina com o sufixo` | `does not end with the suffix` |
| `starts` ou `ends` sem dois textos | `starts espera dois textos` | `starts expects two strings` |
| `raises` sem erro | `esperava um erro, mas a função terminou bem` | `expected an error, but the function finished fine` |
| `raises` com outro trecho | `o erro não tem o trecho esperado` | `the error lacks the expected text` |
| `status` | `código HTTP diferente` | `different HTTP status` |
| `json` | `valor diferente em itens.0.id` | `different value at itens.0.id` |
| `json` sem corpo JSON | `o corpo da resposta não é JSON` | `the response body is not JSON` |
| `status` ou `json` sem resposta | `esperava a resposta de Spec.get/post/put/patch/delete` | `expected the response of Spec.get/post/put/patch/delete` |

Os valores aparecem escritos assim: texto entre aspas, listas e mapas em JSON (`[1,3]`, `{"a":1}`), `nil` e números como são. O relatório rotula as duas linhas `esperado:` e `recebido:` (em inglês, `expected:` e `received:`). Nos matchers de tipo errado (`include`, `gt`, `len`, `empty`, `starts`) só a mensagem aparece, sem esperado e recebido.

Um relatório completo em inglês, real:

```
  1) status errado
     spec/rotas_spec.djo:9
     different HTTP status
     expected: 404
     received: 200 "{\"titulo\":\"estudar\",\"itens\":[{\"id\":7},{\"id\":8}]}"
```

## Idioma: português ou inglês

As mensagens saem em **português por padrão**. O idioma é escolhido **por execução** do `djo spec`, não por teste:

| Como | Efeito |
|---|---|
| (nada) | português |
| `djo spec --lang en` | inglês |
| `DJO_LANG=en djo spec` | inglês (a flag vence a variável) |
| `--lang pt`, `pt-br` ou vazio | português |
| qualquer outro valor | `idioma 'fr' desconhecido (valem: pt, en)` e o `djo spec` sai com código 2 |

Os matchers perguntam a língua ao runner (`Spec.lang`) e escolhem o texto. O `--json` segue o mesmo idioma nas mensagens; as **chaves** do JSON (`resumo`, `mensagem`, `esperado`, `recebido`) ficam sempre em português.

## Só funciona dentro do `djo spec`

O `expect` usa o namespace `Spec`, que **só existe quando os testes rodam com `djo spec`**:

- Num script ou numa página, um matcher que **passa** não faz nada, e um que **não passa** dá um erro, não uma falha: `erro (linha N): Spec.lang só existe quando os testes rodam com 'djo spec'`. Não use o `expect` fora dos testes.
- Para usar o `Spec` num booster seu, ele precisa declarar a permissão `Spec` no `booster.json` (é o caso do `expect`). No `core/` do projeto ou no arquivo de spec o `Spec` já está liberado.

## Mensagens de erro

| Mensagem | Causa |
|---|---|
| `'expect.eq' espera 2 argumento(s), recebeu 1` | número de argumentos errado (é **erro**, o teste aparece como `[erro]`) |
| `o booster expect não tem a função 'x'` | matcher que não existe |
| `Spec.lang só existe quando os testes rodam com 'djo spec'` | o `expect` foi chamado fora do `djo spec` |
| `Spec.try: a função 'x' não existe no projeto ...` | o `raises` recebeu o nome de uma função que o projeto não tem |
