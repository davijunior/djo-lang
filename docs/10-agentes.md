# Agentes: `action` e o manifesto

A djo atende dois tipos de cliente com o mesmo código: pessoas (HTML) e agentes (IA, outros programas,
dispositivos). Nada disso vale sem `rotas.djo`: a lista de rotas é o que o servidor usa para saber o que existe
(veja [Web](04-web.md)).

## As duas faces de uma rota

```
Route.get:"/posts":{"html": "paginas/posts.djo.html", "agent": "agentes/posts.djo", "about": "Lista os posts"}
```

O navegador recebe `"html"`; quem pede `Accept: application/json` recebe `"agent"` (normalmente `Http.json:`).
`"about"` é a descrição que vai para o manifesto. Rota sem `"agent"` não aparece no manifesto, e um agente que
a peça recebe `406` em vez de HTML. O corpo do `406` diz de que lado a rota é (só tela ou só agente) e onde
fica o manifesto.

## `action`: uma ação com tipos fixos

```
// core/posts.djo
struct Autor
    nome[String]
    idade[Int] = 18

action criar_post
    "Cria um post novo"
    params[titulo[String], nota[Float], autor[Autor]]
    Http.status:201
    return {"titulo": titulo, "autor": autor.nome}
```

- `action` se escreve como `f`, e **também continua sendo uma função**: `criar_post:"Oi":4.5:autor` funciona em
  qualquer página. A tela e o agente usam a mesma validação e a mesma regra de negócio.
- A linha entre aspas, logo depois do nome, é a descrição (vai para o manifesto).
- **Todo parâmetro precisa de tipo**: `Int`, `Float`, `String`, `Bool`, `Date`, `DateTime`, `Arr`, `Map` ou uma `struct`. `Date` e `DateTime` chegam como texto ISO e entram no manifesto como `string` com `format` `date` e `date-time`. É o que
  permite descrever a entrada com exatidão.
- Declare-a no `core/`, como as outras funções.

Para publicar a ação:

```
// rotas.djo
Route.action:"/posts":"criar_post"
Route.action:"/posts/{id}/publicar":"publicar"
```

`Route.action:` publica por `POST`. Para os outros métodos, use `Route.put_action:`, `Route.patch_action:` e
`Route.delete_action:`, que fazem o mesmo (validação dos tipos, manifesto, resposta em JSON) com outro método:

```
Route.action:"/posts":"criar_post"                    // POST   /posts
Route.put_action:"/posts/{id}":"trocar_post"          // PUT    /posts/3
Route.patch_action:"/posts/{id}":"mudar_post"         // PATCH  /posts/3
Route.delete_action:"/posts/{id}":"apagar_post"       // DELETE /posts/3
```

O mesmo caminho pode ter um método de cada, e junto com uma rota `Route.get:` de leitura. Isso já dá uma API no
estilo REST. Um método que o caminho não tem responde `405` com o `Allow` certo. No `DELETE` (e em qualquer
chamada sem corpo) só vale o que vem na URL; se mandar corpo, ele precisa ser `application/json`.

### O que acontece numa chamada

```
curl -X POST localhost:8080/posts -H 'Content-Type: application/json' \
     -d '{"titulo":"Oi","nota":4,"autor":{"nome":"Ana"}}'
```

1. O método é o da rota (`POST` em `Route.action:`), e o corpo, se houver, precisa ser `application/json` (outro tipo dá `415`). Isso também protege contra
   CSRF: um site de fora não consegue enviar JSON ao seu servidor pelo navegador.
2. O corpo é conferido contra os tipos **antes** de a action rodar. Campo faltando, tipo errado ou campo
   desconhecido dá `422`:
   ```
   {"erro": "entrada inválida", "campos": {"titulo": "obrigatório", "autor.nome": "deve ser String, mas veio Int"}}
   ```
   Uma `struct` chega como objeto JSON; campos com valor padrão (`idade[Int] = 18`) são opcionais, e `nocall` fica de fora.
3. `{id}` no caminho vira o parâmetro `id`, convertido para o tipo declarado (`/posts/abc/publicar` com `id[Int]`
   dá `422`). Esse parâmetro não pode ir também no corpo.
4. O que a action **retorna** vira o corpo da resposta em JSON; sem retorno, `{"ok":true}`. O que ela imprime
   com `print` é descartado. Para outro status, use `Http.status:` antes de retornar.
5. Erros de execução dão `500` com `{"erro": "erro interno"}` (o detalhe só aparece em `-dev`).

Autenticação: as sessões e os cookies funcionam como numa página (`Session.get:`), então a action pode recusar
com `Http.status:401` e `return {"erro": "entre primeiro"}`.

## O manifesto

Quando alguma rota tem face de agente ou há uma action, o servidor publica:

```
GET /.well-known/ai-manifest.json
```

e acrescenta a toda resposta o cabeçalho `Link: </.well-known/ai-manifest.json>; rel="ai-manifest"`, para quem
chegar por qualquer página descobrir o resto. O manifesto é gerado do `rotas.djo` e dos tipos do `core/`:

```
{
  "format": "djo-manifest/1",
  "routes":  [ {"method": "GET", "path": "/posts", "description": "Lista os posts"} ],
  "actions": [
    {
      "name": "criar_post", "method": "POST", "path": "/posts",
      "description": "Cria um post novo",
      "input": { "type": "object", "required": ["titulo", "autor"], "additionalProperties": false,
                 "properties": { "titulo": {"type": "string"}, "autor": {"type": "object", ...} } }
    }
  ]
}
```

- `input` é um **JSON Schema** padrão, então serve direto como definição de ferramenta para quem já usa
  *function calling* ou MCP.
- `path_params` lista o que vem da URL, com o tipo declarado.
- O manifesto só tem o que foi declarado para agentes. Páginas só de tela, arquivos estáticos e o resto do
  site não aparecem.
- **A porta aparece também no HTML.** Muitos agentes só fazem um `GET` comum e leem a página. Por isso, em
  um site que tem face de agente, o djo acrescenta a toda página de tela completa (a que tem `</head>` e
  `<body>`) um `<link rel="alternate" type="application/json">` e um `<meta name="ai-manifest">` no fim do
  `<head>`, e um comentário logo depois de abrir o `<body>`: "esta é a face para humanos; a sua porta é
  `/.well-known/ai-manifest.json`, ou peça esta mesma URL com `Accept: application/json`". O humano não vê
  nada. Fragmentos de HTML, páginas que já têm um `ai-manifest` e a face de agente ficam como estão, e um
  site sem face de agente não anuncia nada.
- **`/llms.txt`.** O djo também gera `GET /llms.txt`, em Markdown, que é o primeiro arquivo que muitos agentes
  procuram: o nome e o `about` do site (de `Route.site`), uma frase explicando as duas faces, o link do
  manifesto e a lista das leituras e ações com a descrição de cada uma. Ele sai do mesmo `rotas.djo` e só
  existe em site com alguma face de agente (senão dá `404`). Para escrever o seu, declare a rota:
  `Route.get:"/llms.txt":"paginas/llms.djo"` (a sua vence). Só vale no modo `rotas.djo`.
- `rel="ai-manifest"` e o caminho `/.well-known/ai-manifest.json` são uma convenção da djo, não um padrão
  registrado.

### A porta de entrada: `/` para agentes

Um agente costuma começar pela `/`. Se o `rotas.djo` não declara uma face `"agent"` para `GET /`, o djo responde
por ela (a `/` da tela humana não muda) com uma saudação e um resumo do que o site oferece:

```
Route.site:{"nome": "djo", "about": "A linguagem djo: documentação e instalação"}
```
```json
{
  "saudacao": "Olá! Este é o site djo. A linguagem djo: documentação e instalação",
  "manifesto": "/.well-known/ai-manifest.json",
  "rotas": [{"method": "GET", "path": "/docs", "description": "Índice da documentação"}],
  "acoes": [],
  "site": {"nome": "djo", "about": "A linguagem djo: documentação e instalação"}
}
```

- O resumo traz só método, caminho e descrição. Os tipos e schemas ficam no manifesto, para a `/` continuar leve.
- `Route.site` é opcional (uma vez, chaves `"nome"` e `"about"`, textos de até 300 caracteres). Sem ele a saudação é
  genérica. O mesmo texto aparece no manifesto, em `"site"`.
- Se você declarar uma face `"agent"` para a `/`, a sua vale e a padrão não é usada.
- Só existe se o site tem alguma rota com face de agente ou alguma action; senão a `/` continua dando `406`.
- Usa `ETag` e o mesmo cache do manifesto (`max-age=60`).

## O tipo da resposta e `?campos=`

O manifesto já descreve o que o agente **envia**. Para descrever o que ele **recebe**, declare `"returns"` com
o nome de uma `struct` do `core/` (ou `["Post"]` para uma lista):

```
Route.get:"/posts":{"html": "p/posts.djo.html", "agent": "p/posts.djo", "returns": ["Post"]}
Route.get:"/posts/{id}":{"agent": "p/post.djo", "returns": "Post"}
Route.action:"/posts/{id}/comentarios":"comentar":"Comentario"
```

(Na action, o tipo é o terceiro argumento.) O manifesto ganha `"returns"` com um JSON Schema, e o agente passa a
saber de antemão cada campo e seu tipo. `Int`, `Float`, `String`, `Bool`, `Arr` e `Map` também valem.

**Pedir só o que precisa.** Rotas com `"returns"` aceitam `?campos=`, que devolve apenas os campos escolhidos
e poupa tokens quando o objeto é grande:

```
GET /posts?campos=id,titulo            → [{"id":1,"titulo":"Ola"}, ...]
GET /posts?campos=id,autor.nome        → campos aninhados com ponto
GET /posts?campos=id,autor             → "autor" inteiro
```

- Um campo que não existe no tipo dá `400` **sem executar a página**, e a mensagem lista os campos válidos.
- Rota sem `"returns"` responde `400` a `?campos=`: o recorte precisa do tipo para saber o que é válido.
- Vale só na face de agente (a tela ignora). Combina com `ETag` e `"cache"`: cada `?campos=` tem a sua entrada.
- O recorte acontece depois que a página roda e **não** diminui o trabalho do servidor, só o que trafega.
  Para poupar o trabalho, use `"cache"`.

**Conferência em `-dev`.** Com `djo serve -dev`, a resposta de agente é conferida contra o `"returns"`: tipo
errado ou campo que o tipo não tem vira `500` com a lista do que não bate. Assim o manifesto não mente. Em
produção essa conferência não roda. Campos que o tipo tem mas a resposta omitiu são aceitos (o manifesto não
os marca como obrigatórios). A resposta de uma `action` não é conferida.

### `?tabela`: listas como CSV

Em rotas que devolvem uma lista (`"returns": ["Post"]`), `?tabela` troca o JSON por CSV
(`Content-Type: text/csv`). O JSON repete o nome de cada campo em cada item; o CSV diz os nomes
uma vez, no cabeçalho:

```
GET /posts?tabela
id,titulo,publicado,autor.nome
1,Titulo do post,true,Ana
2,Titulo do post,true,Ana
```

- Colunas na ordem do tipo; structs aninhadas viram colunas com ponto (`autor.nome`); `Arr` e `Map`
  viram uma célula com JSON compacto; `nil` fica vazio. Aspas seguem o RFC 4180.
- Combina com `?campos=` (`?tabela&campos=id,titulo`) e com `ETag` e `"cache"`.
- Numa lista de 100 posts com 4 campos o CSV tem cerca de 37% dos bytes do JSON. O ganho cresce com
  o tamanho da lista e com o número de campos.
- Em rota sem `"returns"` ou que devolve um único objeto, `?tabela` responde `400`.
- O manifesto anuncia `tabela` na lista de `query` das rotas que a aceitam.

## Volume: cache, ETag e a pista dos agentes

Muitos agentes lendo o mesmo site não podem deixar a tela lenta para as pessoas, nem gastar tokens relendo o
que não mudou. Quatro mecanismos, todos automáticos ou de uma linha:

**`ETag` e `304`.** Toda resposta de agente (200) leva `ETag`. Quem repete o pedido com `If-None-Match`
recebe `304 Not Modified` e **sem corpo**: zero tokens para o agente reler o que não mudou.

**`"cache"` na rota.** Guarda a resposta de um `GET` em memória por alguns segundos:

```
Route.get:"/posts":{"html": "p/posts.djo.html", "agent": "p/posts.djo", "cache": 30}
```

Mil agentes pedindo `/posts` executam a página **uma vez** a cada 30 segundos. A resposta sai com
`Cache-Control: public, max-age=30`. Regras de segurança, para nunca misturar usuários:

- só `GET`, e só valem requisições **sem `Cookie` e sem `Authorization`**; quem manda um dos dois sempre
  executa a página de verdade;
- resposta que define cookie (`Http.setcookie:`, `Session`) ou que não é 200 nunca é guardada;
- tela e agente têm entradas separadas, e a query (`?x=1`) faz parte da chave;
- até 500 respostas de no máximo 256 KB cada; passou disso, as mais antigas saem.

Use `"cache"` em rotas **públicas** (a mesma resposta para todo mundo). Não use em páginas que mostram dados
de quem está logado: elas já ficam de fora pelo cookie, mas o jeito certo é nem pedir cache.

**A pista dos agentes.** Respostas de agente e `Route.action` passam por um limite próprio. A tela de quem usa
o navegador **nunca entra nessa fila**.

```
djo serve -agent-conc 16 -agent-rate 50
```

| Opção | Padrão | Efeito |
|-------|--------|--------|
| `-agent-conc N` | 2 por núcleo | máximo de chamadas de agente executando ao mesmo tempo; as que passam disso esperam até 250 ms por uma vaga |
| `-agent-rate N` | 50 | requisições por segundo por cliente (rajada de 2N); `0` desliga |

Sem vaga ou acima da taxa, o agente recebe `429 Too Many Requests` com `Retry-After: 1` e um JSON
`{"erro": ...}`. O cliente é identificado pelo IP do visitante: o da conexão ou, atrás de um proxy configurado
(`-trusted-proxy` explícito), o de `X-Forwarded-For` (veja [Produção atrás de um proxy](04-web.md#produção-atrás-de-um-proxy)).
Sem proxy confiável, todos parecem ter o IP do proxy: ajuste `-agent-rate` para o total, ou `0` e limite no próprio proxy.

**Código já interpretado.** O servidor guarda a versão interpretada de cada `.djo`, `.djo.html` e `core/`,
refeita só quando o arquivo muda (horário ou tamanho). Isso vale para todas as páginas, de pessoas ou agentes, e
praticamente dobrou a vazão no benchmark do repositório (`go test -bench .`).

O manifesto também leva `ETag` e `Cache-Control: max-age=60`.

## Ver o site como um agente: `djo agent`

```
djo agent localhost:8080                     # o manifesto, em texto legível
djo agent localhost:8080/posts/2             # a resposta de agente dessa rota
djo agent -post '{"texto":"oi"}' localhost:8080/posts/2/comentarios    # chama uma action
djo agent -raw localhost:8080                # o JSON do manifesto, sem formatar
```

- Sem caminho (ou só `/`) ele busca o manifesto e lista as leituras e as ações, com o que vai na URL e cada
  campo do corpo (tipo e se é obrigatório).
- Com caminho, faz a requisição com `Accept: application/json` e mostra o status, o `Content-Type`, o `Link` e o
  corpo formatado. Uma rota só de tela mostra o `406` com a dica de declarar `"agent"`.
- `-post` envia o corpo como `application/json`. `-method` troca o método (`PUT`, `PATCH`, `DELETE`); com `-post` e sem `-method` é `POST`. As opções vêm **antes** do endereço.
- Sem `http://`, ele assume `http://`. Sai com código `1` se a resposta for 4xx/5xx ou o site não responder,
  então serve em scripts.
- É só HTTP: o mesmo resultado sai de `curl -H "Accept: application/json" ...`. Não vem nos builds `nano`
  (sem rede).

## Limites (por ora)

- `action` não recebe formulário HTML; para formulários use uma `Route.post` comum chamando a mesma função.
- Não há servidor MCP: o manifesto é o que um adaptador MCP leria, mas esse adaptador ainda não existe.
- Os parâmetros de uma action não têm valor padrão (só os campos de struct).
- Os limites e o cache vivem na memória de **um processo**. Com várias instâncias atrás de um balanceador, cada uma
  tem os seus.
- Para listas grandes use `Db.page:` (paginação) e `?tabela` (CSV compacto); veja [Banco de dados](05-banco.md).

Exemplo completo: `exemplos/rotas/` (tela, agente e a action `comentar`).
