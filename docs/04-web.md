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
djo serve [-addr host:porta] [-dev] [pasta]
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

- O caminho aceita `{nome}` (um trecho) e `{...nome}` (o resto, só no fim). Os valores chegam por
  `param:"nome"`, `Http.param:"nome"` e `Http.args:`, como nas rotas por arquivo.
- O destino é uma `String` (a página, valendo para o navegador) ou um Map com `"html"`, `"agent"`, `"about"`
  (descrição para o manifesto), `"returns"` (tipo da resposta de agente) e `"cache"` (segundos, só em `Route.get`; veja [Agentes](10-agentes.md)).
  Chave desconhecida (`"agente"`) é erro.
- **Tela e agente são páginas separadas na mesma URL.** A escolha é pelo cabeçalho `Accept`:
  `application/json` sem `text/html` vai para `"agent"`, o resto vai para `"html"`. A resposta leva
  `Vary: Accept`, e a face de agente responde `application/json` por padrão.
- Pedir um formato que a rota não tem dá **406**, em vez de entregar a outra face (um agente nunca recebe
  HTML para raspar). Sem preferência (`Accept` vazio ou `*/*`), vale a face que existe; se existem as duas, a tela.
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
| `Http.body:`             | corpo cru da requisição |
| `Http.json:`             | corpo lido como JSON (`nil` se inválido) |
| `Http.file:"campo"`      | arquivo enviado: mapa `{name, size, type}`, ou `nil` |
| `Http.upload:"campo":"pasta"` | grava o arquivo enviado e devolve o caminho, ou `nil` |
| `Http.ip:`               | endereço de quem chamou |
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
- As sessões ficam **na memória**: reiniciar o `djo serve` desloga todo mundo.
- Atrás de um proxy HTTPS, o cookie não recebe `Secure` automaticamente.

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

## Senhas e valores aleatórios: Hash

| Chamada                   | Faz |
|---------------------------|-----|
| `Hash.password:senha`     | texto para guardar no banco (PBKDF2-SHA256, 600 mil rodadas, sal aleatório) |
| `Hash.verify:senha:guardado` | `true` se a senha bate |
| `Hash.sha256:texto`       | SHA-256 em hexadecimal |
| `Hash.random:n`           | `n` bytes aleatórios seguros, em hexadecimal (1 a 64) |

Nunca guarde a senha em texto: guarde o resultado de `Hash.password:`.
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
