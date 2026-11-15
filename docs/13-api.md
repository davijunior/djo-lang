# Fazendo uma API

Uma API em djo é um site só com rotas que respondem JSON. Você não precisa de nada além do que já existe:
**leituras** são rotas `GET` com face de agente, **escritas** são `action`s, e a autenticação e o CORS são
duas linhas. O exemplo completo está em `exemplos/api/`.

## As peças

```
// rotas.djo
Route.site:{"nome": "API de tarefas", "about": "Cria, lista, troca, marca e apaga tarefas."}
Route.cors:{"origins": ["https://app.exemplo.com"]}

Route.get:"/tarefas":{"agent": "agentes/tarefas.djo", "about": "Lista as tarefas", "returns": ["Tarefa"]}
Route.get:"/tarefas/{id}":{"agent": "agentes/tarefa.djo", "returns": "Tarefa"}

Route.action:"/tarefas":"criar_tarefa":"Tarefa"                    // POST   /tarefas
Route.put_action:"/tarefas/{id}":"trocar_tarefa":"Tarefa"          // PUT    /tarefas/3
Route.patch_action:"/tarefas/{id}/feita":"marcar_feita":"Tarefa"   // PATCH  /tarefas/3/feita
Route.delete_action:"/tarefas/{id}":"apagar_tarefa"                // DELETE /tarefas/3
```

| Para...                    | Use                                  | Você ganha |
|----------------------------|--------------------------------------|------------|
| ler (`GET`)                | `Route.get:` com `"agent"`           | `?campos=`, `?tabela` (CSV), cache, manifesto |
| criar, trocar, apagar      | `Route.action:`, `put_action:`, `patch_action:`, `delete_action:` | tipos conferidos (`422`), JSON obrigatório (`415`), manifesto |
| saber quem chama           | `Http.bearer:`                       | o token do cabeçalho `Authorization` |
| deixar um front-end de outro endereço chamar | `Route.cors:`      | só as origens que você listar |

Uma rota só de agente (sem `"html"`) responde `406` a quem pedir uma tela. Quem chama a API manda
`Accept: application/json`; as actions não precisam disso.

As leituras e as escritas moram no mesmo caminho: `GET /tarefas/3` e `DELETE /tarefas/3` são rotas diferentes.
Um método que o caminho não tem responde `405` com o `Allow` certo.

## Paginação

Uma leitura que devolve muitas linhas usa `Db.page:`. O corpo continua sendo só a lista, e a paginação vai
nos cabeçalhos:

```
Route.get:"/tarefas":{"agent": "agentes/tarefas.djo", "returns": ["Tarefa"], "page": true}
```
```
var r = Db.page:"SELECT * FROM tarefas ORDER BY id":[]:{"total": true}
Page.headers:r
Http.json:r["itens"]
```
```
$ curl -i 'localhost:8080/tarefas?limite=2&pagina=2' ...
Link: </tarefas?limite=2&pagina=3>; rel="next", </tarefas?limite=2&pagina=1>; rel="prev"
X-Total-Count: 5
```

`"page": true` faz o manifesto anunciar `?pagina=` e `?limite=` para os agentes. Pedidos ruins (página
negativa, limite enorme) não dão erro: viram valores seguros. Veja [Banco de dados](05-banco.md) para o que
`Db.page:` faz e o que evita (contar a tabela a cada página).

## Autenticação por token

`Http.bearer:` devolve o token de `Authorization: Bearer <token>`, ou `nil` (sem o cabeçalho, com outro tipo
de autenticação ou com o token vazio). O que fazer com ele é com você. O exemplo guarda só o **hash** do token
no banco, então quem lê o banco não consegue usar a API:

```
f autorizado
    var token = Http.bearer:
    if{ cond[token == nil] return false }
    abrir:
    return Db.one:"SELECT 1 AS ok FROM tokens WHERE hash = ?":[Hash.sha256:token] != nil
```

Cada action e cada leitura começam recusando quem não está autorizado:

```
if{ cond[not (autorizado:)]
    Http.status:401
    Http.setheader:"WWW-Authenticate":"Bearer"
    return {"erro": "envie o cabeçalho Authorization: Bearer SEU_TOKEN"}
}
```

Para gerar um token (o exemplo tem o `criar_token.djo`): `Hash.random:24` cria 48 caracteres hex, e o banco
guarda `Hash.sha256:token`. O token só aparece uma vez, na hora de criar.

- Uma chave de outro serviço (que a API chama) não vai no código: leia com `Env.get:"CHAVE"`.
- Mande o token só por HTTPS. Em HTTP puro, qualquer um no caminho o lê.
- Um token que vaza vale até você apagar a linha dele da tabela. Se precisar de validade, guarde uma data
  junto e confira no `autorizado`.

## CORS

Sem `Route.cors:`, o navegador **bloqueia** chamadas de outros endereços (o `OPTIONS` que ele manda antes
responde `405`). Isso é o certo para uma API chamada só por servidores e pelo próprio site. Para liberar um
front-end:

```
Route.cors:{"origins": ["https://app.exemplo.com", "http://localhost:5173"],
            "headers": ["Content-Type", "Authorization"], "max_age": 600}
```

| Chave         | O que é |
|---------------|---------|
| `"origins"`   | lista de origens permitidas, como o navegador as escreve (`https://site.com`, `http://localhost:5173`; sem barra no fim). `"*"` libera qualquer uma |
| `"headers"`   | cabeçalhos que o front-end pode mandar (padrão: `Content-Type` e `Authorization`) |
| `"credentials"` | `true` deixa o navegador mandar cookies. Não vale junto com `"*"`: liste as origens |
| `"max_age"`   | por quantos segundos o navegador guarda a resposta do preflight (padrão 600, máximo 86400) |

- O djo responde sozinho ao preflight (`OPTIONS`) de qualquer caminho que exista, com os métodos que **aquele**
  caminho tem. Uma origem que não está na lista recebe `403`.
- Nas respostas normais, o `Access-Control-Allow-Origin` vai só para a origem que perguntou, e a resposta leva
  `Vary: Origin`, então o cache de rotas nunca entrega a permissão de uma origem a outra.
- CORS protege o **navegador** de quem chama. Não substitui o token: quem não é navegador (um `curl`, outro
  servidor) ignora o CORS.
- Só vale no modo `rotas.djo`, e só pode aparecer uma vez.

## Testando

```
djo criar_token.djo                  # imprime o token (uma vez)
djo serve .

curl -X POST localhost:8080/tarefas -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' -d '{"titulo":"Comprar pão"}'
curl localhost:8080/tarefas -H "Authorization: Bearer $TOKEN" -H 'Accept: application/json'
curl -X DELETE localhost:8080/tarefas/1 -H "Authorization: Bearer $TOKEN"

djo agent localhost:8080                    # o manifesto, com cada rota e cada ação
djo agent -method DELETE localhost:8080/tarefas/1
```

## O que fica de fora

- **Ordenação e filtros** são com você (`?ordem=nome` chega por `Http.query:`; valide os valores antes de usar no SQL).
- **Limite de requisições por chamador** existe só para a face de agente (`-agent-rate`).
- **Versão da API** (`/v1/...`) é só um prefixo nas rotas.
