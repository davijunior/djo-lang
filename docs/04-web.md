# Web

## Começar um projeto

```
djo new meusite
cd meusite
djo serve -dev
```

`djo new` cria um site pronto: `rotas.djo` com tela e face de agente, uma `action`, `core/`, `paginas/`,
`agentes/` e `publico/`. Com `-modelo script` ele cria um programa de um arquivo só, e com `-modelo iot`
um programa que lê um sensor na porta serial. Recusa uma pasta que já tem arquivos.

## O servidor

```
djo serve [-addr host:porta] [-dev] [-trusted-proxy ips] [pasta]
```

O mapeamento de URL para arquivo é igual ao do PHP:

| URL          | Arquivo executado |
|--------------|-------------------|
| `/`          | `index.djo.html`, `index.djo` ou `index.html` |
| `/sobre`     | `sobre.djo.html` ou `sobre.djo` |
| `/estilo.css`| arquivo estático |

- Arquivos e pastas que começam com `.` ou `_` nunca são servidos (use `_topo.djo.html`
  para pedaços reutilizáveis, e `_dados/` para arquivos gravados com `File`) e as pastas `core/` e `boosters/` também não.
- `404.djo.html` na raiz personaliza a página de erro.
- Sem `-dev`, erros de execução mostram só uma página genérica (o detalhe vai para o log).
- `djo serve -jobs N` escolhe quantos workers de [Job](16-jobs.md) rodam (padrão 2; `0` só enfileira). `-jobs-http N` (ou `DJO_JOBS_HTTP`) muda o limite de chamadas `Http` por execução de job (padrão 20, de 1 a 200). O `agenda.djo` nunca é servido.
- Com `-dev`, `Mail.send` não envia: grava o e-mail em `_saida/emails/` (`-mail-send` envia de verdade). Veja [E-mail](15-email.md).
- Links simbólicos que saem da pasta do site são ignorados.

**Limites por requisição:** 1 MB de corpo (10 MB em formulários com arquivos), 10 MB de saída, 10 milhões de passos de
execução. Passar disso interrompe a página.

## Rotas amigáveis (por arquivo)

Para URLs como `/posts/ola-mundo` ou `/usuarios/7/editar`, o nome do arquivo (ou da pasta) entre
colchetes casa com qualquer trecho da URL:

| Arquivo                     | URL de exemplo      | Valor                    |
|-----------------------------|---------------------|--------------------------|
| `posts/[slug].djo`          | `/posts/ola-mundo`  | `slug` = `"ola-mundo"`   |
| `usuarios/[id]/editar.djo`  | `/usuarios/7/editar`| `id` = `"7"`             |
| `usuarios/[id]/index.djo`   | `/usuarios/7`       | `id` = `"7"`             |
| `docs/[...caminho].djo`     | `/docs/a/b/c`       | `caminho` = `"a/b/c"`    |

Dentro da página:

```
var slug = param:"slug"        // ou Http.param:"slug"; nil se não existe
var todos = Http.args:         // mapa com todas as partes: {slug: "ola-mundo"}
```

- **Quem tem nome exato vence:** com `posts/novo.djo` e `posts/[slug].djo`, `/posts/novo` abre o primeiro.
  A ordem é: nome exato, depois `[nome]`, depois `[...nome]` (que leva o resto da URL).
- Os valores vêm da URL e são sempre `String`: converta com `int:` e valide antes de usar (`Db.query` com `?`, nunca
  texto colado).
- Não use dois `[nome]` diferentes na mesma pasta (`[id]` e `[slug]`): vale o primeiro em ordem alfabética.
- Acessar `/[slug]` literalmente dá 404, e `core/` continua fora do alcance.
- `?pagina=2` continua em `Http.query:`; `param` é só para o que está no caminho.

## Rotas declaradas: `rotas.djo`

O roteamento por arquivo deixa qualquer `.djo` da pasta acessível. Se você prefere uma lista fechada, crie
um `rotas.djo` na raiz do site. **Com ele, só existe o que foi declarado**: o roteamento por arquivo
(inclusive `[slug].djo`) fica desligado, e o resto da pasta é código interno.

```
// rotas.djo
Route.get:"/":"paginas/inicio.djo.html"
Route.get:"/posts":{"html": "paginas/posts.djo.html", "agent": "agentes/posts.djo"}
Route.get:"/posts/{id}":{"html": "paginas/post.djo.html", "agent": "agentes/post.djo"}
Route.post:"/entrar":"paginas/entrar.djo"
Route.get:"/docs/{...caminho}":"paginas/docs.djo"
Route.static:"/assets":"publico"
```

| Chamada | Para quê |
|---------|----------|
| `Route.get:` `post:` `put:` `patch:` `delete:` | uma página para o método e o caminho |
| `Route.static:"/assets":"publico"` | serve arquivos de uma pasta (só `GET`) |
| `Route.site:{"nome": "x", "about": "y"}` | quem é o site: vai na saudação de agentes em `/` e no manifesto (veja [Agentes](10-agentes.md)) |
| `Route.action:"/posts":"criar_post"` | publica uma `action` para agentes, veja [Agentes](10-agentes.md) |
| `Route.put_action:` `patch_action:` `delete_action:` | a mesma `action`, publicada com `PUT`, `PATCH` ou `DELETE` (uma API no estilo REST) |
| `Route.cors:{"origins": ["https://app.com"]}` | deixa um front-end de outro endereço chamar o site; só as origens listadas (veja [Fazendo uma API](13-api.md)) |

- O caminho aceita `{nome}` (um trecho) e `{...nome}` (o resto, só no fim). Os valores chegam por
  `param:"nome"`, `Http.param:"nome"` e `Http.args:`, como nas rotas por arquivo.
- O destino é uma `String` (a página, valendo para o navegador) ou um Map com `"html"`, `"agent"`, `"about"`
  (descrição para o manifesto), `"returns"` (tipo da resposta de agente) e `"cache"` (segundos, só em `Route.get`; veja [Agentes](10-agentes.md)) e `"page": true` (a rota usa `Db.page:`: o manifesto anuncia `?pagina=` e `?limite=`).
  Chave desconhecida (`"agente"`) é erro.
- **Tela e agente são páginas separadas na mesma URL.** A escolha é pelo cabeçalho `Accept`:
  `application/json` sem `text/html` vai para `"agent"`, o resto vai para `"html"`. A resposta leva
  `Vary: Accept`, e a face de agente responde `application/json` por padrão.
- Pedir um formato que a rota não tem dá **406**, em vez de entregar a outra face (um agente nunca recebe
  HTML para raspar). O corpo do 406 diz se a rota é só de tela ou só de agente e, se o site tem manifesto,
  onde ele fica. Sem preferência (`Accept` vazio ou `*/*`), vale a face que existe; se existem as duas, a tela.
- No `-dev`, `?formato=agent` ou `?formato=html` força uma face, para testar no navegador.
- Método errado em caminho conhecido dá **405** com `Allow`. `HEAD` vale como `GET`.
- **Mais específica vence**, não importa a ordem: texto fixo, depois `{nome}`, depois `{...nome}`.
  Duas rotas iguais (`/posts/{id}` e `/posts/{slug}`) são erro.
- Páginas citadas precisam existir, ser `.djo` ou `.djo.html` e ficar dentro do site (nunca em `core/`).
  `Route.static` não serve `.djo`, nem arquivos ocultos, nem `core/`, nem bancos.
- O `rotas.djo` só aceita chamadas `Route.*` (nada de `if`, `print` ou variáveis soltas), então o servidor
  lê a lista sem executar lógica. Ele é conferido ao iniciar o `serve` e no `djo build`; em `-dev`, mudou o
  arquivo, as rotas recarregam.
- `404.djo.html` na raiz continua sendo a página de erro.

Exemplo completo em `exemplos/rotas/`: o `core/` guarda a regra (`struct Post`), a tela e o agente só apresentam.

## Dois tipos de página

**Código (`.djo`):** o que `print` escreve é o corpo da resposta.

```
Http.setheader:"Content-Type":"text/plain; charset=utf-8"
print "método: " + Http.method:
```

**Template (`.djo.html`):** HTML com trechos de código.

| Sintaxe     | Faz |
|-------------|-----|
| `{{ expr }}`| imprime com o HTML **escapado** |
| `{{! expr }}`| imprime sem escapar (use com cuidado) |
| `{% código %}` | executa código |
| `{# nota #}`| comentário |

Nos templates os blocos precisam de chaves, e `{% } else{ %}` fecha e abre ao mesmo tempo:

```
{% var nome = Http.query:"nome" ?? "visitante" %}
<h1>Olá, {{ nome }}!</h1>

{% if{ cond[len:posts > 2] %}
  <p>Vários posts.</p>
{% } else{ %}
  <p>Poucos.</p>
{% } %}

{% include:"_rodape.djo.html" %}
```

Se o seu front-end também usa `{{ }}` (Vue, Angular), escreva esses trechos num arquivo
`.html` comum, que é servido sem passar pela djo.

## Http

Disponível só nas páginas servidas por `djo serve`.

| Chamada                  | Faz |
|--------------------------|-----|
| `Http.method:`           | `"GET"`, `"POST"`... |
| `Http.path:`             | caminho da URL |
| `Http.query:"chave"`     | parâmetro da URL, ou `nil` |
| `Http.form:"campo"`      | campo de formulário (POST), ou `nil` |
| `Http.header:"Nome"`     | cabeçalho da requisição, ou `nil` |
| `Http.bearer:`           | o token de `Authorization: Bearer <token>`, ou `nil` (veja [Fazendo uma API](13-api.md)) |
| `Http.body:`             | corpo cru da requisição |
| `Http.json:`             | corpo lido como JSON (`nil` se inválido) |
| `Http.file:"campo"`      | arquivo enviado: mapa `{name, size, type}`, ou `nil` |
| `Http.upload:"campo":"pasta"` | grava o arquivo enviado e devolve o caminho, ou `nil` |
| `Http.request_id:`       | o id desta requisição (o do log e do cabeçalho `X-Request-Id`; veja [Log do servidor](#log-do-servidor)) |
| `Http.ip:`               | IP de quem chamou (o real, atrás de um proxy confiável: veja [Produção atrás de um proxy](#produção-atrás-de-um-proxy)) |
| `Http.get:url` `Http.post:url:corpo` ... | requisições de saída (veja abaixo) |
| `Http.cookie:"nome"`     | cookie, ou `nil` |
| `Http.setcookie:"nome":"valor"` | cria o cookie (`HttpOnly`, `SameSite=Lax`, `Secure` em HTTPS) |
| `Http.setheader:"Nome":"valor"` | cabeçalho da resposta |
| `Http.status:404`        | código da resposta (100 a 599) |
| `Http.redirect:"/ir"`    | redireciona e **encerra** a página |
| `Http.json:valor`        | responde JSON (define o `Content-Type`) |
| `Http.json:valor:201`    | o mesmo, com status |

Os nomes curtos antigos (`query:`, `form:`, `status:`...) continuam funcionando.

### Exemplo: uma API

```
if
    cond[Http.method: == "POST"]
    var d = Http.json:
    Http.json:{"recebido": d["n"] + 1}:201
else
    Http.json:{"erro": "use POST"}:405
```

`Http.body:` e `Http.json:` ficam vazios em formulários comuns (`application/x-www-form-urlencoded`),
que o servidor já leu para montar o `Http.form:`.

## Chamando outros serviços: Http.get e Http.post

O `Http` também faz requisições de **saída**: buscar uma API, enviar uma leitura de sensor,
chamar um webhook.

| Chamada                                 | Método |
|-----------------------------------------|--------|
| `Http.get:url`                          | GET |
| `Http.post:url:corpo`                   | POST |
| `Http.put:url:corpo`                    | PUT |
| `Http.delete:url`                       | DELETE |

Todas aceitam, no fim, um `Map` de cabeçalhos: `Http.get:url:{"Authorization": "Bearer abc"}`
e `Http.post:url:corpo:{"X-Chave": "abc"}`.

**Timeout por chamada.** O padrão é 8 s. Depois dos cabeçalhos (use `nil` se não tem cabeçalhos) vai, opcional, o
timeout: um número de segundos ou um texto como `"30s"` e `"2m"`. Vale para `get`, `post`, `put` e `delete`:

```
Http.get:url:nil:30                              // sem cabeçalhos, até 30 s
Http.get:url:{"Authorization": "Bearer abc"}:"30s"
Http.post:url:corpo:nil:"2m"
```

| Onde roda | Mínimo | Máximo por chamada |
|-----------|--------|--------------------|
| página e `action` servidas | 1 s | 20 s, e a **soma** das esperas de `Http` da página não passa de 25 s (o servidor corta a resposta em 30 s) |
| job | 1 s | 60 s, e nunca mais do que resta do `timeout` do job |
| `djo spec` e scripts | 1 s | 60 s |

Pedir fora da faixa dá erro claro (`o timeout máximo aqui é 20 s`). Estourar o prazo **não** é erro: volta
`{"ok": false, "status": 0, "error": "tempo esgotado (30 s)"}`. Se quem estoura é o prazo do **job**, no meio da
chamada, o job é interrompido (erro fatal: o `promise` não pega e o `finally` não roda). Numa página, a cota de 25 s
também corta as chamadas sem timeout (as de 8 s); depois dela a página para com erro.

```
var r = Http.get:"https://api.exemplo.com/clima?cidade=rio"
if
    cond[r["ok"]]
    var dados = Json.decode:r["body"]
    print dados["temp"]
else
    print "falhou: " + (r["error"] ?? str:r["status"])

Http.post:"https://meuservidor.com/leituras":{"sensor": "sala", "temp": 23.5}
```

**O que volta** é sempre um `Map`, e uma falha de rede **não** dá erro na página:

| Chave     | Valor |
|-----------|-------|
| `ok`      | `true` se o status é 2xx |
| `status`  | código HTTP (`0` se não chegou a conectar) |
| `body`    | corpo da resposta, em texto (use `Json.decode:`) |
| `headers` | cabeçalhos da resposta, com nomes em minúsculas (`r["headers"]["content-type"]`) |
| `error`   | só existe se falhou: o motivo |

**O corpo** do `post`/`put`: um texto é enviado como está (`text/plain`); um `Map`, `Arr` ou
struct é enviado como JSON (`application/json`). Os cabeçalhos que você passar valem mais.

**Limites:** 8 segundos por chamada, resposta de até 5 MB, no máximo 5 redirecionamentos,
só `http://` e `https://`.

### Segurança em `djo serve`

Uma página que busca uma URL escolhida pelo visitante poderia ser usada para alcançar a rede
interna do servidor (SSRF). Por isso, **nas páginas servidas**:

- endereços internos são bloqueados: `localhost`, `127.x`, `10.x`, `172.16-31.x`, `192.168.x`,
  `169.254.x` (inclusive o endereço de metadados de nuvem) e equivalentes IPv6. A checagem é
  feita no endereço já resolvido, então um nome de DNS que aponte para dentro também é barrado;
- cada página pode fazer até 5 chamadas externas.

Se o seu site precisa falar com algo da rede local (uma API em `192.168.0.10`, por exemplo),
inicie com `djo serve -allow-private`. Nunca use essa opção se alguma página aceita URLs
digitadas por visitantes.

Rodando um script direto (`djo arquivo.djo`), não há bloqueio nem limite de chamadas.

## Produção atrás de um proxy

É comum o nginx (ou Caddy, Traefik...) terminar o HTTPS e repassar o pedido em HTTP para a djo em
`127.0.0.1:porta`. Sem ajuda, a djo vê uma conexão HTTP e os cookies (`djo_sid` e os de
`Http.setcookie`) saem **sem `Secure`**. Para resolver, a djo lê o cabeçalho `X-Forwarded-Proto`, mas
**só de um proxy confiável**: se a conexão não vem de um endereço da lista, o cabeçalho é ignorado
(qualquer visitante poderia escrevê-lo).

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Real-IP $remote_addr;
}
```

As duas últimas linhas só importam para o IP do visitante (abaixo), mas deixe-as sempre: sem elas o nginx repassa o que o visitante escreveu.

| Configuração | Efeito |
|---|---|
| (padrão) | confia no loopback: `127.0.0.0/8` e `::1`. É o caso do nginx na mesma máquina. |
| `-trusted-proxy 10.0.0.0/8,192.168.1.7` | confia nesses IPs/CIDRs (proxy em outra máquina ou contêiner) |
| `-trusted-proxy none` | não confia em ninguém: o cabeçalho nunca é lido |
| `DJO_TRUSTED_PROXY=...` | o mesmo, pelo ambiente; a flag vence a variável |

- Configure o proxy para **sobrescrever** o cabeçalho, como no exemplo (`proxy_set_header X-Forwarded-Proto $scheme;`), e não para anexar: a djo usa só o primeiro valor, e num cabeçalho anexado o primeiro é o que o visitante mandou.
- Só o **primeiro** valor do cabeçalho conta e só `https` liga o `Secure`; `http`, lixo ou lista mal formada contam como HTTP.
- `-trusted-proxy` com valor inválido faz a djo recusar a partida. Não existe curinga: `*`, `all` e prefixos muito largos (abaixo de `/8` em IPv4 ou `/16` em IPv6) são recusados, para ninguém confiar na internet inteira por engano.
- Vale para `djo serve` e para o app gerado por `djo build` (que aceita a mesma flag e a mesma variável).
- **Um site rodando sem proxy não muda**: com o padrão (loopback), um visitante de fora que mande `X-Forwarded-Proto` é ignorado. A única mudança é o `Secure` dos cookies atrás do proxy.

### O IP do visitante atrás do proxy

> **Atenção:** se o proxy não sobrescreve `X-Forwarded-For`, qualquer visitante forja o IP. Por isso a djo **só lê os cabeçalhos de IP quando você configura a lista** (`-trusted-proxy` ou `DJO_TRUSTED_PROXY`, mesmo que seja `127.0.0.1`). No padrão, `Http.ip` e o `-agent-rate` usam o IP da conexão, como sempre. Atualizar a djo não muda nada sozinho.

Com a lista configurada, `Http.ip:` devolve o IP **real** do visitante, e não o do proxy (`127.0.0.1`). Isso importa
para limites por IP (por exemplo, "5 mensagens por hora por IP"): sem isso, todos dividiriam a mesma cota.

```nginx
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Real-IP $remote_addr;
```

- Os cabeçalhos são lidos só com a lista configurada **e** se a conexão vem de um endereço dela. Em qualquer outro caso (padrão, outro endereço, `-trusted-proxy none`) eles são ignorados, e `Http.ip` é o IP da conexão, como sempre foi.
- **`X-Forwarded-For`** é lido **da direita para a esquerda**, e vale o primeiro endereço que não é de proxy confiável: o da esquerda pode ter sido escrito pelo visitante. Com vários proxies em cadeia, liste todos em `-trusted-proxy`. Se esse endereço não é um IP válido, a djo para e usa o IP da conexão.
- **`X-Real-IP`** só é usado quando não há `X-Forwarded-For`. Use `$remote_addr` (o nginx sobrescreve o cabeçalho); se o seu proxy repassa o `X-Real-IP` do cliente sem trocar, não confie nele.
- Valor que não é um IP é ignorado e `Http.ip` cai no IP da conexão. IPv6 funciona; `::ffff:1.2.3.4` vira `1.2.3.4`.
- O **limite de agentes** (`-agent-rate`, veja [Agentes](10-agentes.md)) usa o mesmo IP: atrás de um proxy confiável ele passa a contar por visitante, e não mais um balde só para todos. Quem tinha ajustado `-agent-rate` para o total, pode reduzi-lo.
- O `/ask` tem a sua própria lista (`Ask.proxy`), independente de `-trusted-proxy`.

## Log do servidor

Por padrão o `djo serve` escreve uma linha de texto por requisição (`GET /posts 200 3ms`). Para um sistema de log (ou para procurar por id), use JSON:

```
djo serve -log-format json -log-ip trunc
```

- `-log-format text|json` (ou `DJO_LOG_FORMAT`; o flag vence o ambiente).
- `-log-ip trunc|full|off` (ou `DJO_LOG_IP`): o IP é dado pessoal. O padrão `trunc` zera o último byte (`203.0.113.0`; no IPv6, tudo depois do /48), `off` não registra, `full` registra inteiro.
- Cada linha JSON tem só campos de uma lista fechada: `ts`, `level`, `request_id`, `method`, `route`, `status`, `ms`, `bytes`, `ip`, `error_code` e, nas linhas de erro, `msg` e `stack`. A `route` é o **padrão** da rota (`/clientes/{id}`), nunca a URL: a URL pode ter um token. Query, cabeçalhos, cookies e corpo **nunca** entram.
- **ID da requisição:** toda resposta leva o cabeçalho `X-Request-Id`. Se o proxy mandou um (de 8 a 64 letras, números, `.`, `_` ou `-`), ele é mantido; senão o servidor cria um (`rq_...`). `Http.request_id:` devolve esse id dentro da página, para você pôr o mesmo id no erro que devolve ao cliente.
- **Segredos:** a mensagem de erro e a saída dos jobs passam por um depurador que troca por `***` os valores das variáveis de ambiente com `KEY`, `SECRET`, `TOKEN`, `PASS`, `PWD`, `DSN` ou `CREDENTIAL` no nome (e as `DJO_CRYPTO_*`), `Bearer ...`, textos `djo1....` (do `Crypto`), a senha de `usuario:senha@host` e valores depois de `password`, `senha`, `token`, `secret`, `api_key`, `app_key`, `app_secret`. É uma rede de segurança, não uma garantia: **o que o seu código imprime com `print` não é depurado**, e um segredo escrito de outro jeito pode passar. Não ponha segredo em mensagem de erro.

## Sessões

Dados do visitante guardados no servidor, ligados a um cookie (`djo_sid`: `HttpOnly`,
`SameSite=Lax`, e `Secure` em HTTPS).

| Chamada                   | Faz |
|---------------------------|-----|
| `Session.set:"chave":valor` | guarda um valor |
| `Session.get:"chave"`     | lê o valor, ou `nil` |
| `Session.del:"chave"`     | apaga uma chave |
| `Session.clear:`          | apaga todos os dados |
| `Session.renew:`          | troca o identificador, mantendo os dados. **Chame ao fazer login.** |
| `Session.destroy:`        | encerra a sessão (logout) |
| `Session.token:`          | token anti-CSRF da sessão |
| `Session.verify:valor`    | `true` se o valor é o token |

- Só cabem dados simples: texto, números, `Bool`, `Arr` e `Map` (nada de structs). Guarde
  o `id` do usuário e busque o resto no banco.
- Limite de 64 KB por sessão e 10 mil sessões; expiram após 24 h sem uso.
- **Ler não cria sessão.** `Session.get`, `Session.del`, `Session.clear` e `Session.verify` de quem não tem sessão devolvem `nil`/`false` e não mandam cookie; só `Session.set`, `Session.renew` e `Session.token:` (o formulário precisa do token) criam. Assim uma página que só consulta o usuário logado não enche o servidor de sessões.
- **Efeito no `cache` de rota.** Antes, toda página que lia `Session` criava o cookie e a resposta saía com `Cache-Control: private, no-store`, então o `"cache": N` da rota nunca valia para o visitante anônimo. Agora o anônimo que só lê não recebe cookie e a resposta sai `public, max-age=N`: a página fica em cache por até N segundos **mesmo depois de editar ou apagar o conteúdo** (quem tem cookie de sessão vê o novo). É o cache funcionando como declarado. Use `cache` só em páginas que toleram N segundos de atraso.
- Com 10 mil sessões, uma nova não derruba a página: sai a vencida, senão a mais antiga **sem dados** (só o token), senão a mais antiga de todas.
- As sessões ficam **na memória**: reiniciar o `djo serve` desloga todo mundo.
- Atrás de um proxy HTTPS, o cookie recebe `Secure` quando o proxy confiável manda `X-Forwarded-Proto: https` (veja [Produção atrás de um proxy](#produção-atrás-de-um-proxy)).

### Proteção CSRF

Todo formulário que muda algo (POST) leva o token escondido, e a página confere:

```
<input type="hidden" name="_token" value="{{ Session.token: }}">
```

```
if
    cond[not (Session.verify:(Http.form:"_token"))]
    Http.status:403
    print "token inválido"
    Http.redirect:"/entrar"
```

## Limite de uso por chave: `Limit`

O `-agent-rate` limita por IP. Para limitar **por token** (ou por empresa, por consumidor), use `Limit`:

```
var tok = Http.bearer:
Limit.enforce:("tok:" + tok):100:60        // 100 chamadas por 60 s; passou: responde 429 e a página PARA
var r = Limit.take:("tok:" + tok):100:60   // só conta e devolve {allowed, remaining, retry_after_s, reason}
```

- **`Limit.enforce:chave:quantas:janela`** (só em página): dentro do limite, acrescenta `RateLimit-Remaining` e segue. Fora dele responde **429** com `Retry-After: N` (segundos) e o corpo `{"error": "muitas_requisicoes", "retry_after_s": N}`, e a página termina como no `redirect`. Para o seu formato de erro, passe um 4º argumento: `Limit.enforce:chave:100:60:{"body": {"erro": {...}}, "headers": {"X-Algo": "1"}}` (`body` é texto ou dados, que viram JSON).
- **`Limit.take`** não responde nada: você decide. `reason` é `nil`, ou `"cheio"` (veja abaixo).
- `quantas` vai de 1 a 1.000.000; a janela, de 1 s a 24 h (em segundos). É uma janela **deslizante** aproximada (a janela anterior pesa na proporção do que falta dela): não dá para "gastar tudo no fim de uma janela e tudo no início da outra".
- **Escolha bem a chave.** Use a de quem **já foi autenticado** (token validado, id de empresa). A tabela guarda até 50.000 chaves vivas e **nunca expulsa uma viva**: se um atacante inventar chaves (IPs, e-mails digitados) até encher, quem chega depois recebe `"cheio"` (o `enforce` responde **503** com `Retry-After: 1`). Chave que o visitante escolhe é a que não deve alimentar o `Limit`.
- A chave é guardada só como SHA-256: o token não fica na memória do contador.
- **Os contadores ficam na memória do processo:** somem se o servidor reinicia e **não valem entre várias instâncias** do `djo serve`. (Um job que roda no mesmo `djo serve` conta nos mesmos contadores.)
- Numa rota com `cache` (resposta guardada e servida sem rodar a página), o limite só conta nas execuções de verdade; não use `Limit` em rota cacheada.
- No `djo spec`, cada teste começa com os contadores zerados; `Spec.limit_reset:` zera no meio do teste.

## Senhas e valores aleatórios: Hash

| Chamada                   | Faz |
|---------------------------|-----|
| `Hash.password:senha`     | texto para guardar no banco (PBKDF2-SHA256, 600 mil rodadas, sal aleatório) |
| `Hash.verify:senha:guardado` | `true` se a senha bate |
| `Hash.sha256:texto`       | SHA-256 em hexadecimal |
| `Hash.random:n`           | `n` bytes aleatórios seguros, em hexadecimal (1 a 64) |
| `Hash.hmac:chave:texto`   | HMAC-SHA256 em hexadecimal: assinatura de um texto com uma chave (webhooks) |
| `Hash.equal:a:b`          | `true` se os dois textos são iguais, em tempo constante (use para comparar tokens e assinaturas, nunca `==`) |

Nunca guarde a senha em texto: guarde o resultado de `Hash.password:`.

Para validar a assinatura de um webhook: `Hash.equal:(Hash.hmac:segredo:corpo):(Http.header:"X-Assinatura")`. Compare sempre com
`Hash.equal:`, que não revela por onde os textos começam a diferir.
O mesmo texto gera hashes diferentes (por causa do sal), então compare só com `Hash.verify:`.

## Upload de arquivos

O formulário precisa de `enctype="multipart/form-data"`:

```
<form method="post" enctype="multipart/form-data">
  <input type="file" name="foto">
</form>
```

```
var foto = Http.file:"foto"             // {name, size, type} ou nil
var caminho = Http.upload:"foto":"uploads"    // "uploads/9f2c...png"
```

- O arquivo é gravado com **nome novo e aleatório**; só a extensão é mantida. Guarde o
  caminho devolvido no banco.
- Aceitas: `jpg jpeg png gif webp avif ico pdf txt csv json md zip gz mp3 wav ogg mp4 webm`.
  Qualquer outra (inclusive `html`, `svg`, `js` e `djo`) é recusada com erro, para que um
  upload nunca vire uma página ou código executado pelo servidor.
- A pasta é criada se não existir e precisa ficar dentro do site.
- Limite de 10 MB por formulário. Os arquivos gravados são servidos como estáticos.

## A pasta `core/`

Structs e funções que o sistema inteiro usa ficam em `core/`, sem `include`:

```
meusite/
  core/
    post.djo        <- struct Post{...}
    util.djo
  index.djo.html    <- usa Post direto
```

- Todo `.djo` dentro de `core/` (inclusive subpastas) é carregado antes de cada página.
- O core **só declara** `struct`, `interface` e `f`. Código solto é erro, com o nome do arquivo.
- Não é servido como página (`/core/...` dá 404).

Não confunda com o **core da linguagem**: `Json`, `Http`, `Time`, `Db`, `Session` e `Hash`, que já vêm prontos.

O exemplo completo, com login, posts, banco e upload, está em `exemplos/blog/`.
