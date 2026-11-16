# Testes: `djo spec` e o booster `expect`

`djo spec` roda os testes de um projeto. Um teste é uma função `it_...` num arquivo `*_spec.djo`, e as conferências vêm do booster `expect`:

```
djo booster add expect        # uma vez por projeto (pede só a permissão Spec)
djo spec                      # roda spec/**/*_spec.djo
```

(O `expect` ainda não está no registro: enquanto isso, instale da pasta do código do djo com `djo booster add caminho/do/djo/boosters-oficiais/expect`.)

```
// spec/soma_spec.djo
f it_soma_dois_numeros
    expect.eq:(soma:2:3):5

f it_erra_com_texto
    expect.raises:"soma":["a", 1]:"Int"

f it_cria_uma_tarefa
    var r = Spec.post:"/tarefas":{"titulo": "estudar"}
    expect.status:r:201
    expect.json:r:"titulo":"estudar"
```

```
..F*E

Problemas:

  1) soma dois numeros
     spec/soma_spec.djo:5
     os valores são diferentes
     esperado: 6
     recebido: 5

5 specs, 1 falha, 1 erro, 1 pulado, em 0.03s
```

Um exemplo que roda: `exemplos/api/spec/tarefas_spec.djo` (`cd exemplos/api && djo spec`).

## O que é um spec

- O arquivo termina em `_spec.djo` e fica em `spec/` (ou em qualquer pasta que você passar ao comando).
- **Cada função `it_nome` é um teste.** O nome vira a descrição: `it_soma_dois_numeros` aparece como "soma dois numeros". A ordem é a do arquivo.
- `antes` e `depois` são funções opcionais do arquivo: `antes` roda antes de cada teste, `depois` roda depois (mesmo que o teste tenha falhado).
- Fora das funções pode haver variáveis e outras funções de ajuda: o arquivo inteiro é executado de novo para cada teste.
- O `core/` e os boosters do projeto já estão carregados, então os testes chamam as funções do projeto pelo nome.

**Cada teste roda isolado**: um interpretador novo (nenhuma variável sobrevive de um teste para o outro) e uma pasta temporária só para os bancos SQLite. `Db.open:"app.db"` abre `app.db` nessa pasta, e ela é apagada no fim do teste. O banco do projeto nunca é tocado, e as rotas chamadas por `Spec.get` e companhia usam a mesma pasta, então enxergam o que o teste gravou.

Quando um teste para por uma falha, um erro ou um `Spec.skip`, os seguintes continuam rodando.

## `djo spec`

```
djo spec [-e filtro] [--json] [--lang en] [--seed N] [pasta | arquivo_spec.djo]
```

| Opção | Faz |
|---|---|
| (nada) | roda `spec/` do projeto atual |
| `pasta` | a raiz do projeto (usa o `spec/` dela) ou qualquer pasta com specs |
| `arquivo_spec.djo` | roda só esse arquivo |
| `-e texto` (ou `--only`) | só os testes cujo arquivo, descrição ou nome contém o texto; pode repetir |
| `--json` | saída em JSON, para agentes e CI (sem o progresso) |
| `--lang en` | mensagens em inglês (ou `DJO_LANG=en`); o padrão é português |
| `--seed N` | embaralha a ordem com essa semente (a mesma semente dá a mesma ordem) |

A ordem padrão é estável: arquivos em ordem alfabética, testes na ordem do arquivo.

**Código de saída:** `0` se tudo passou (ou foi pulado), `1` se algum teste falhou ou deu erro, `2` se o comando foi mal usado ou não achou nenhum `*_spec.djo`. Um filtro que não acha nada sai com `0` e "0 specs".

**Progresso:** `.` passou, `F` falhou (um `expect` não bateu), `E` erro (o código quebrou, ou o arquivo de spec tem erro de sintaxe), `*` pulado.

**JSON:**

```
{
  "resumo": {"total": 4, "passaram": 1, "falharam": 1, "erros": 1, "pulados": 1, "duracao_ms": 12},
  "specs": [
    {"arquivo": "spec/soma_spec.djo", "nome": "soma dois numeros", "status": "falhou",
     "mensagem": "os valores são diferentes", "esperado": "6", "recebido": "5", "linha": 5, "duracao_ms": 1}
  ]
}
```

`status` é `passou`, `falhou`, `erro` ou `pulado`. O que o teste imprimiu (`print`) só aparece, em `saida`, nos que falharam ou deram erro (até 4 KB).

**Limites por teste:** 5 milhões de passos e 30 segundos. Um laço sem fim vira erro e a suíte segue.

## Matchers (`expect`)

Cada um confere um valor; se não bater, o teste para ali e o relatório mostra o esperado e o recebido. Todos seguem `expect.nome:real:esperado`.

| Matcher | Passa quando |
|---|---|
| `expect.eq:real:esperado` | os dois são iguais, **em profundidade**: listas, mapas (sem olhar a ordem das chaves), structs, `Date`, `DateTime`; `1` e `1.0` são iguais; `Date` e `DateTime` nunca são |
| `expect.not_eq:real:esperado` | são diferentes |
| `expect.be_nil:v` `be_true:v` `be_false:v` | o valor é `nil`, `true` (um `Bool`, não `1`) ou `false` |
| `expect.include:colecao:item` | o texto contém o texto, a lista tem o item (igualdade funda) ou o mapa tem a chave |
| `expect.gt:a:b` `gte` `lt` `lte` | `a > b`, `a >= b`, `a < b`, `a <= b` (números) |
| `expect.close:real:esperado:tolerancia` | a diferença é no máximo a tolerância (para `Float`) |
| `expect.len:colecao:n` | o tamanho de lista, mapa ou texto é `n` |
| `expect.empty:colecao` | lista, mapa ou texto vazio |
| `expect.starts:texto:prefixo` `ends:texto:sufixo` | o texto começa / termina com |
| `expect.raises:"funcao":[args]:"trecho"` | a função do projeto dá **erro de execução** e a mensagem tem o trecho (`""` aceita qualquer erro) |
| `expect.status:resposta:codigo` | a resposta de uma rota tem esse código HTTP |
| `expect.json:resposta:"a.b.0.c":valor` | o corpo JSON da resposta tem esse valor no caminho (chaves de mapa e posições de lista, separadas por ponto; um caminho que não existe vale `nil`) |

A negação existe só em `not_eq`. Para negar os outros, confira uma condição: `expect.be_false:(contains:texto:"x")`, `expect.be_true:(a != b)`.

`gt`, `gte`, `lt` e `lte` só comparam números. Para datas, use `expect.eq:(a < b):true`.

## O namespace `Spec`

`Spec` é o que o runner dá ao teste e ao booster `expect`. **Só existe dentro do `djo spec`**: um script, uma página ou o servidor que chamem `Spec.qualquer-coisa` recebem um erro. Um booster que use `Spec` precisa declarar a permissão `Spec` no `booster.json`.

| Chamada | Faz |
|---|---|
| `Spec.fail:"mensagem"` | falha o teste e para (use para criar seus próprios matchers) |
| `Spec.fail:{"mensagem": m, "esperado": e, "recebido": r}` | o mesmo, com esperado e recebido no relatório |
| `Spec.skip:"motivo"` | marca o teste como pulado e para |
| `Spec.try:"funcao":[args]` | chama uma função **do projeto** e devolve `{"ok", "value", "error", "line"}`; captura o erro de execução, mas deixa passar `Spec.fail` e `Spec.skip` |
| `Spec.emails:` | a lista dos e-mails que o `Mail.send` "enviou" neste teste (nada sai de verdade); veja [E-mail](15-email.md) |
| `Spec.lang:` | `"pt"` ou `"en"`, o idioma do relatório |
| `Spec.get:"/caminho"` `Spec.delete:"/caminho"` | chama uma rota, action ou página do site, em processo |
| `Spec.post:"/caminho":corpo` `put` `patch` | o mesmo, com corpo |
| os de rota aceitam um último argumento: um mapa de cabeçalhos | `Spec.get:"/tarefas":{"Authorization": "Bearer x"}` |

`Spec.try` só chama funções que o projeto declarou (no `core/` ou no arquivo de spec), nunca as nativas, e dá erro para um nome que não existe (um erro de digitação não pode passar por "erro esperado").

### Rotas e actions em processo

`Spec.get`, `post`, `put`, `patch` e `delete` passam a requisição direto pelo servidor do projeto, **sem abrir porta**. Valem as mesmas regras de um acesso de verdade: `rotas.djo`, páginas, actions, `core/`, CORS, o tráfego de agentes e as respostas de erro. O corpo de um mapa ou lista vai como JSON; um texto vai como formulário (`"titulo=x&n=1"`) e você pode trocar o `Content-Type` nos cabeçalhos. O caminho começa com `/` e é sempre local (`http://...` é recusado).

A resposta é um mapa: `{"status": 201, "body": "...", "headers": {"Content-Type": "..."}}`. Use `Json.decode:r["body"]`, ou os matchers `status` e `json`.

## Limites (o que o `djo spec` não faz)

- **Só o SQLite é isolado.** Um `Db.open:"postgres://..."` ou `mysql://...` fala com o servidor de verdade, e o teste que fizer isso escreve nele. Nos testes, use um `.db`.
- **`File` escreve na pasta real do projeto**, como em qualquer script. Só os bancos vão para a pasta temporária.
- Sessão e cookies não passam de uma chamada `Spec.*` para a outra (o `Set-Cookie` aparece em `headers`, mas você tem que reenviá-lo em `Cookie`). Upload (multipart) e SSE não têm atalho.
- Os testes do mesmo arquivo rodam um por vez, e nada roda em paralelo.
- **A linha do relatório é a da chamada ao `expect` no arquivo de spec.** Se o `expect` for chamado de dentro de uma função do `core/`, a linha é a de lá.
- O erro "limite de passos" dispara por contagem de instruções; uma chamada que bloqueia (por exemplo `Http.get` para um servidor lento) espera pelo próprio limite de tempo dela.
- O `djo build` e o servidor ignoram a pasta `spec/`: ela nunca vai para o executável nem é servida pelo `djo serve`.

## Escrever o seu matcher

Um matcher é uma função que chama `Spec.fail`:

```
// core/matchers.djo (ou no arquivo de spec)
f ser_par
    params[n]
    if
        cond[n % 2 != 0]
        Spec.fail:{"mensagem": "esperava um número par", "esperado": "par", "recebido": str:n}
```

(Só o `expect` precisa de permissão `Spec` no booster; no `core/` do projeto ou no arquivo de spec o `Spec` já está liberado.)
