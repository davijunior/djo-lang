# Referência

## Palavras reservadas

`f var arr map print params return true false nil input cond while until for eng if elif else
match and or not pick struct sonof interface action super nocall`

(`each` e `in` valem dentro do `for`.)

(`val` e `case` valem dentro do `match`.)

(`promise`, `uniq`, `success`, `error` e `finally` valem só na posição de um promise e **não são reservadas**: `var error = 1` continua válido. Veja [Erros e promises](01-basico.md#erros-e-promises).)

## Funções que já vêm na linguagem

Funções suas com o mesmo nome têm prioridade sobre as da linguagem.

### Listas e mapas

| Função              | Faz |
|---------------------|-----|
| `len:x`             | tamanho de `Arr`, `Map` ou `String` (em letras) |
| `push:lista:valor`  | acrescenta no fim da lista |
| `range:a:b`         | lista de `a` até `b - 1` |
| `keys:m` `values:m` | chaves / valores de um mapa, em lista |
| `has:m:chave`       | o mapa tem a chave? |
| `del:m:chave`       | apaga a chave (`true` se existia) |

### Texto

As posições contam **letras** (não bytes), então `"ação"` tem 4.

| Função                         | Faz |
|--------------------------------|-----|
| `str:x`                        | qualquer valor em texto |
| `upper:s` `lower:s`            | maiúsculas / minúsculas |
| `trim:s`                       | tira espaços e quebras de linha das pontas |
| `split:s:sep`                  | quebra numa lista: `split:"a,b":","` dá `["a", "b"]` |
| `join:lista:sep`               | junta a lista num texto |
| `replace:s:de:para`            | troca todas as ocorrências |
| `contains:s:pedaço`            | tem o pedaço? Também em listas: `contains:lista:item` |
| `starts:s:p` `ends:s:p`        | começa / termina com |
| `index:s:pedaço`               | posição, ou `-1`. Também em listas: `index:lista:item` |
| `slice:s:de:até`               | pedaço do texto (o `até` não entra; sem ele vai até o fim). Também em listas |
| `repeat:s:n`                   | repete `n` vezes |
| `padleft:s:largura:"0"`        | completa à esquerda: `padleft:"7":2:"0"` dá `"07"` (`padright` à direita) |
| `slug:s`                       | para URLs: `slug:"Olá, Mundo!"` dá `"ola-mundo"` (tira acentos) |
| `escape:s`                     | escapa HTML |
| `urlencode:s` `urldecode:s`    | codifica / decodifica para URL (`urldecode` dá `nil` se inválido) |

`slice` ajusta limites que passam do tamanho em vez de dar erro (`slice:"abc":2:99` dá `"c"`).

### Conversão e números

As conversões **não dão erro**: devolvem `nil` quando não dá, para combinar com `??`.

| Função            | Faz |
|-------------------|-----|
| `int:x`           | texto ou decimal em `Int`: `int:"42"` é `42`, `int:3.9` é `3`, `int:"abc"` é `nil` |
| `float:x`         | texto ou `Int` em `Float`: `float:"2.5"`. Não aceita `inf`, `nan` nem hexadecimal |
| `abs:x`           | valor absoluto |
| `floor:x` `ceil:x`| arredonda para baixo / para cima, em `Int` |
| `round:x`         | `Int` mais próximo |
| `round:x:casas`   | `Float` com essas casas: `round:3.14159:2` dá `3.14` |
| `min:a:b` `max:a:b` | o menor / o maior de dois números |

```
var pagina = int:(Http.query:"p") ?? 1
var titulo = replace:(trim:(Http.form:"titulo")):"  ":" "
var url = "/posts/" + (slug:titulo)
```

### Arquivos e includes

`include:"arq"` executa outro arquivo da pasta do site. Veja `File` abaixo para ler e gravar.

## Erros: `promise` e `reject:`

| Forma | O que faz |
|---|---|
| `promise{ ... }success{ [x] ... }error{ [e] ... }finally{ ... }` | tenta o bloco; `success` se deu certo, `error` se deu erro, `finally` sempre (menos nos limites fatais) |
| `uniq promise{ ... }...` | o mesmo, esperando o resultado ali mesmo |
| `var p = promise{ ... }...` | o promise como valor: o `return` do `success` ou do `error` |
| `return` dentro de um bloco do promise | termina aquele bloco com o valor (não sai da função) |
| `reject:"mensagem"` ou `reject:"codigo":"mensagem":dados` | lança um erro; o código `"erro"` é reservado |
| `reject:e` | relança o erro que o `error{ [e] }` recebeu |

O erro do `error{ [e] }` é um mapa com `msg`, `line`, `code`, `data` e `file`. Passam direto, sem o `error` nem o `finally`: o limite de passos, o de tempo, o de chamadas `Http` por página ou job e a recursão sem fim. Passam pelo `finally`: `Job.give_up`, `redirect`, `Spec.fail` e `Spec.skip`. Tudo isso, com exemplos, em [Erros e promises](01-basico.md#erros-e-promises).

## Json

`Json.encode:x`, `Json.pretty:x`, `Json.decode:s`, `Json.valid:s`. Veja [Mapas e Json](03-mapas-json.md).

## Http

`action` (função que vira endpoint para agentes, veja [Agentes](10-agentes.md)). Rotas (`rotas.djo`: `Route.get:`, `Route.post:`, `Route.static:`, `Route.site:`...), entrada (`Http.query:`, `Http.param:`, `Http.args:`, `Http.form:`, `Http.bearer:`...), resposta (`Http.json:`, `Http.redirect:`...) e saída (`Http.get:`, `Http.post:`, `Http.put:`, `Http.delete:`). Veja [Web](04-web.md).

## File

Lê e grava arquivos **dentro da pasta do projeto** (a do script, ou a do site em `djo serve`).
Os caminhos são relativos: `"dados/leituras.txt"`.

| Chamada                    | Faz |
|----------------------------|-----|
| `File.read:"a.txt"`        | o conteúdo como texto, ou `nil` se não existe |
| `File.write:"a.txt":texto` | grava (cria as pastas que faltarem); devolve `true` |
| `File.append:"log.txt":texto` | acrescenta no fim (cria o arquivo se preciso); `true` |
| `File.exists:"a.txt"`      | `true` ou `false` |
| `File.delete:"a.txt"`      | apaga um arquivo ou pasta vazia; `false` se não existia |
| `File.list:"pasta"`        | nomes em ordem (pastas terminam em `/`), ou `nil` se não existe |
| `File.info:"a.txt"`        | `{size, modified, dir}`, ou `nil` |
| `File.mkdir:"a/b"`         | cria a pasta (e as de cima) |

```
var agora = Time.now:
File.append:"leituras.csv":(str:agora) + ",23.5\n"
for{
    each[linha in split:(trim:(File.read:"leituras.csv" ?? "")):"\n"]
    print linha
}
File.write:"estado.json":(Json.encode:{"ligado": true})
```

- Só texto: para dados use `Json.encode:` ao gravar e `Json.decode:` ao ler.
- Nada sai da pasta do projeto: `..`, caminhos absolutos e links simbólicos que apontam para fora dão erro.
- `File.write` grava num arquivo temporário e renomeia, então quem lê nunca vê o arquivo pela metade.
- Limites: 10 MB por leitura e por gravação.
- **Nunca monte o caminho com texto de visitante** (`File.read:Http.query:"arq"`): um visitante poderia
  ler `blog.db` ou qualquer arquivo do site. Valide contra uma lista fixa.

**Em `djo serve` há regras extras de gravação**, para uma página não virar um jeito de plantar código:
não se grava `.djo`, `.html`, `.htm`, `.svg`, `.js`, `.xml` nem nada dentro de `core/`; e cada página grava
no máximo 10 MB. Guarde seus dados numa pasta que começa com `_` (como `_dados/`): o servidor
nunca serve esses arquivos, então ninguém os baixa pela URL.

## Session, Hash, upload

Veja [Web](04-web.md).

## Serial

Porta serial para sensores e placas. Veja [Hardware](08-iot.md).

## Db

Veja [Banco de dados](05-banco.md). `Db.page:` pagina uma consulta.

## Page

| Função | O que faz |
|--------|-----------|
| `Page.read:` | lê `?pagina=` e `?limite=` e devolve `{"pagina", "limite", "deslocamento"}`; 20 por página, no máximo 100 |
| `Page.read:10:50` | o mesmo, com o tamanho padrão e o máximo que você escolher (até 1000) |
| `Page.headers:r` | põe `Link` (`next`/`prev`) e `X-Total-Count` na resposta, a partir do resultado de `Db.page:` |

## Env

| Função | O que faz |
|--------|-----------|
| `Env.get:"NOME"` | o valor da variável de ambiente, ou `nil` se ela não existe |
| `Env.get:"NOME":"padrão"` | o mesmo, com um valor para quando ela não existe |

Uma variável que existe e está vazia devolve `""`, e não o padrão. É o lugar dos segredos (senhas, chaves de
serviços): ficam fora do código e do executável (`djo build` não leva o ambiente). Qualquer página e qualquer
`f` do projeto pode ler todas as variáveis do processo, então **nunca imprima** o resultado em uma página. Um
booster só usa `Env` se pedir a permissão no `booster.json`.

Os nomes que começam com `DJO_CRYPTO_` são **reservados**: `Env.get` (e o `${VAR}` do `Db.open`) os recusam, para a chave do `Crypto` nunca virar um valor que uma página possa ler.

## Crypto

Cifra para guardar segredos no banco (credenciais de um sistema externo, tokens por empresa). AES-256-GCM, só com a biblioteca padrão: existe em todos os perfis.

| Chamada | O que faz |
|--------|-----------|
| `Crypto.seal:texto` | cifra com a chave atual e devolve um texto próprio para o banco (`djo1.<id da chave>.<dados>`) |
| `Crypto.seal:texto:contexto` | o mesmo, **amarrando** o texto ao contexto (`"empresa:42"`) |
| `Crypto.open:cifrado` / `Crypto.open:cifrado:contexto` | devolve o texto original; o contexto tem de ser o mesmo do `seal` |

```
var guardado = Crypto.seal:token:"empresa:42"
var token = Crypto.open:guardado:"empresa:42"
```

- **Use sempre o contexto para segredos por empresa (ou por usuário).** Sem ele, quem consegue copiar o texto cifrado da empresa 7 para a linha da empresa 42 faz o sistema abri-lo lá. Com o contexto, isso dá erro.
- **A chave** vem do ambiente do servidor: `DJO_CRYPTO_KEY`, 32 bytes em hexadecimal (64 letras e números). `djo crypto newkey` gera uma. Ela nunca é um valor da linguagem: o `Env.get` a recusa e nenhuma mensagem de erro a mostra. Sem a chave, `Crypto.seal` e `Crypto.open` dão um erro que diz o que fazer; com a chave malformada, o `djo serve` nem sobe. Guarde a chave num lugar seguro: **sem ela, o que foi cifrado não abre**.
- **Rotação:** ponha a chave nova em `DJO_CRYPTO_KEY` e a velha em `DJO_CRYPTO_KEYS_OLD` (várias, separadas por vírgula; só abrem). Cada texto traz o id da chave que o cifrou. Para aposentar a velha, rode um job que faz `Crypto.seal:(Crypto.open:x:ctx):ctx` em cada linha.
- **O id da chave** que aparece no texto cifrado (`djo1.<id>.…`) são 4 bytes de um SHA-256 da chave: serve só para achar a chave certa na rotação e **não é segredo**.
- **Onde pôr a chave:** num arquivo de ambiente do serviço (no systemd, `EnvironmentFile=/etc/meusite/djo.env` com modo `600`, dono do usuário do serviço), nunca na linha de comando (ela aparece no `ps`) nem no repositório.
- `Crypto.open` com texto alterado, contexto errado ou chave errada dá **o mesmo erro** (`não consegui abrir`), que o `promise` pega como qualquer outro. Máximo de 64 KB por texto.
- No `djo spec`, sem `DJO_CRYPTO_KEY`, vale uma chave aleatória só daquela execução (os testes cifram e abrem; nada fica de um teste para o outro). No `djo serve -dev` a falta da chave continua sendo erro: uma chave escondida deixaria dados que não abrem depois.
- Um booster precisa da permissão `Crypto` no `booster.json`.

## Mail

| Chamada | O que faz |
|---|---|
| `Mail.send:{"to": ..., "subject": ..., "text": ..., "html": ..., "cc": ..., "bcc": ..., "reply_to": ..., "from": ...}` | envia um e-mail por SMTP e devolve `{"ok", "id", "sent", "error"}` |
| `Spec.emails:` | (só no `djo spec`) os e-mails que o `Mail.send` "enviou" no teste atual |

`to`, `subject` e `text` são obrigatórios; `from` só troca o nome exibido (o endereço é sempre o de `DJO_SMTP_FROM`). A configuração vem **só do ambiente** (`DJO_SMTP_HOST`, `DJO_SMTP_PORT`, `DJO_SMTP_USER`, `DJO_SMTP_PASS`, `DJO_SMTP_FROM`, `DJO_SMTP_SECURE`), com STARTTLS obrigatório (ou TLS na 465) e certificado sempre verificado. Em `djo serve -dev` não envia: grava `.eml` em `_saida/emails/` (use `-mail-send` para enviar mesmo assim). No `djo spec` nunca envia. Não existe nos builds `nonet`; um booster precisa da permissão `Mail`. Veja [E-mail](15-email.md).

## Job e Schedule

| Chamada | O que faz |
|---|---|
| `Job.run:"fn":{args}` | enfileira a função `fn` do `core/` para rodar **depois da resposta**; devolve o id (Int), ou `nil` se a fila está cheia |
| `Job.run:"fn":{args}:{"in": "10m", "at": ..., "tries": 3, "timeout": "60s", "key": "..."}` | com opções (`in` e `at` não valem juntos) |
| `Job.status:id` | `{id, fn, state, tries, max_tries, run_at, created_at, finished_at, error}`, ou `nil` |
| `Job.cancel:id` | cancela um job que ainda está na fila (`true`/`false`) |
| `Job.give_up:"motivo"` | (dentro de um job) desiste sem repetir |
| `Schedule.every:"10m":"fn"` `Schedule.cron:"20 3 * * *":"fn"` | (só no `agenda.djo`) dispara `fn` de tempos em tempos; cron em UTC |
| `Spec.jobs:` `Spec.run_jobs:` `Spec.run_job:id` `Spec.agenda:` | (só no `djo spec`) veja [Testes](14-testes.md) |

Cada job roda **pelo menos uma vez**: escreva a função para ser idempotente. A fila é um SQLite local em
`_dados/jobs.db`; `djo serve -jobs N` escolhe os workers (padrão 2); `djo jobs` mostra e conserta a fila. O
`Job.run` só enfileira se a página terminar sem erro. Não existe nos builds `nodb` e `noweb`; um booster precisa
da permissão `Job`. Veja [Tarefas em segundo plano e agenda](16-jobs.md).

## Time

Datas são `Int`: segundos desde 1970 (UTC). Somar e subtrair é aritmética comum
(`Time.now: + 3600` é daqui a uma hora).

| Chamada                       | Faz |
|-------------------------------|-----|
| `Time.now:`                   | agora |
| `Time.format:ts:"formato"`    | `Int` para texto |
| `Time.parse:"texto":"formato"`| texto para `Int`, ou `nil` se não bater |
| `Time.ms:`                    | milissegundos desde 1970 |
| `Time.sleep:ms`               | espera (veja [Hardware](08-iot.md)) |
| `Time.date:ts`                | mapa com `year`, `month`, `day`, `hour`, `minute`, `second`, `weekday` (0 = domingo) |

Formatos: `YYYY` ano, `MM` mês, `DD` dia, `HH` hora (0 a 23), `mm` minuto, `ss` segundo.
Pode usar os símbolos `- / : . ,` e espaço. Outras letras e números soltos dão erro.

```
var agora = Time.now:
print Time.format:agora:"DD/MM/YYYY HH:mm"         // 31/01/2025 14:05
var t = Time.parse:"2025-01-31":"YYYY-MM-DD"
print (Time.date:t)["month"]                       // 1
```

## Date e DateTime

`Date` é um dia do calendário, sem hora nem fuso (`2025-01-31`). `DateTime` é um instante, sempre em **UTC**
(`2025-01-31T14:05:00Z`). Os dois vão e voltam como texto ISO no JSON, aparecem no manifesto como `string` com
`format` `date` e `date-time`, e um campo ou parâmetro vazio é `nil`. O `Time` (segundos em `Int`) continua igual.

```
struct Evento
    nome[String]
    dia[Date]
    inicio[DateTime] = DateTime.now:

var d = Date.make:2025:1:31
print d + 1                         // 2025-02-01   (Date + Int soma dias)
print Date.parse:"2025-03-30" - d   // 58           (Date - Date dá dias)
print d.year                        // 2025
print d.format:"DD/MM/YYYY"         // 31/01/2025
```

| Chamada | Faz |
|---------|-----|
| `Date.today:` | o dia de hoje (em UTC) |
| `Date.make:2025:1:31` | cria; uma data que não existe (`2025-02-31`) é erro |
| `Date.parse:"2025-01-31"` | texto ISO para `Date`, ou `nil` se não bater |
| `Date.parse:"31/01/2025":"DD/MM/YYYY"` | com formato (`YYYY MM DD`); `nil` se não bater |
| `Date.of:valor` | o dia de um `DateTime` (ou o próprio `Date`) |
| `DateTime.now:` | agora |
| `DateTime.make:2025:1:31` / `:14:5:0` | meia-noite do dia, ou com hora, minuto e segundo |
| `DateTime.parse:texto` | RFC 3339 (`2025-01-31T14:05:00Z`, ou com deslocamento como `-03:00`, convertido para UTC) ou o formato de banco `"2025-01-31 14:05:00"` (lido como UTC); `nil` se não bater |
| `DateTime.parse:texto:"DD/MM/YYYY HH:mm"` | com formato; `nil` se não bater |
| `DateTime.from_unix:n` | de segundos desde 1970 |
| `DateTime.of:data` | meia-noite UTC do dia |
| `DateTime.end_of:data` | 23:59:59 UTC do dia (para "até a data, inclusive") |

Propriedades, sem `:`: em `Date`, `year`, `month`, `day` e `weekday` (0 = domingo); em `DateTime`, também `hour`,
`minute`, `second`, `unix` (segundos desde 1970) e `date`. O método `format:"..."` usa os mesmos símbolos do
`Time.format` (`YYYY MM DD HH mm ss`); em um `Date`, `HH`, `mm` e `ss` são erro.

- **Contas:** `Date ± Int` soma ou tira dias; `DateTime ± Int`, segundos. `Date - Date` dá dias e `DateTime - DateTime`,
  segundos. `==`, `!=`, `<`, `>`, `<=` e `>=` valem entre dois do mesmo tipo.
- **Não se misturam:** comparar ou somar `Date` com `DateTime` é erro (não há conversão em silêncio). Use `dt.date`
  ou `DateTime.of:d` / `DateTime.end_of:d`. Um intervalo "de X até Y, inclusive" com datas é
  `DateTime.of:X` até `DateTime.end_of:Y`.
- **Fuso:** só UTC. `Date.today:` e `DateTime.now:` são o dia e a hora em UTC, que no Brasil à noite já é o dia
  seguinte. Um deslocamento no texto (`-03:00`) é convertido para UTC na leitura.
- **Intervalo:** anos 1 a 9999; uma conta que sai disso é erro.
- **Em actions e rotas:** o corpo JSON e a URL aceitam só o formato ISO (`AAAA-MM-DD`; `AAAA-MM-DDTHH:MM:SSZ`);
  outro formato ou uma data que não existe dá `422` no campo certo. Veja [Agentes](10-agentes.md).

## Ask (`/ask` e `djo ask`)

Perguntas em linguagem natural (inglês e português) para **humanos**; agentes seguem usando o manifesto e as actions.
Só lê. Sem `ask.djo` na raiz o `/ask` não existe. O `ask.djo` só aceita chamadas `Ask.*` e nunca é servido.
Guia completo: `docs/ask.md`.

| Chamada | O que faz |
|---|---|
| `Ask.db:"site.db"` | **Obrigatório.** O banco consultado: arquivo `.db`/`.sqlite` do site ou `postgres://` / `mysql://` |
| `Ask.type:"post":{...}` | Um tipo consultável: `table`, `struct`, `show_by`, `fields` (cada campo com `column`, `kind`, `attr`, `as`, `main`, `detail`...) |
| `Ask.level:"public":{"allow": [...]}` | O que cada nível enxerga (`public < agent < admin`): `type`, `where` fixo, `fields` |
| `Ask.deny:["user.password"]` | Campos que ninguém enxerga, em nenhuma operação |
| `Ask.alias:"post":["artigos"]` | Apelidos de tipo (`"post"`) ou de campo (`"post.title"`), em minúsculas |
| `Ask.limits:{...}` | `max_results` (1–1000), `per_minute` por IP e por token, `global_per_minute` (teto global opcional), `timeout_ms` (100–10000), `detail_max_bytes` (1024–500000, padrão 200 KB) |
| `Ask.reply:{"refused": ..., "unknown": ...}` | Mensagens de "não aceito" e "não entendi", um texto ou `{"en": ..., "pt": ...}` |
| `Ask.level_from:"nivel_do_pedido"` | Função do `core/` (sem parâmetros, não action) que devolve `"public"`, `"agent"` ou `"admin"`; lê o pedido com `Http.bearer:` ou a sessão. Sem ela tudo é `public` e tokens dão 401 |
| `Ask.proxy:["127.0.0.1", "10.0.0.0/8"]` | Proxies de confiança. Só quando a conexão vem de um deles o `X-Forwarded-For` é lido (usa o último IP não confiável da cadeia) |

Níveis: falha fechada. Sem gancho, erro, `nil` ou valor desconhecido = `public`; token Bearer presente que o gancho
não aceita = 401. O nível nunca depende do que a pessoa escreveu.

**Atrás de nginx ou outro proxy**, sem `Ask.proxy` o IP de todos é o do proxy: o limite por IP vira um limite
único para todos os anônimos. Liste o proxy em `Ask.proxy` e faça-o enviar `X-Forwarded-For`.

`per_minute` conta por processo, em memória: com várias instâncias, cada uma tem o seu limite.

**Toda pergunta é um POST** (nunca vai na URL): `POST /ask` `{"question": "...", "page": 2, "lang": "pt"}` responde JSON
(`answer`, `data`, `page`, `more`, `next`, `calls`, `confirm: null`...). `GET /ask` no navegador mostra só a página com a
caixa de pergunta, que envia por POST de formulário; `GET /ask?q=...` ignora a pergunta.

```
djo ask meusite.com "list posts from 2025-01-01 to 2025-03-30"
djo ask --page 2 meusite.com "liste posts primeiros 5"
djo ask 'djo://meusite.com/ask="me da a lista de posts"'
```

`djo ask`: `https` obrigatório (só `localhost`, `127.0.0.1` e `::1` aceitam `http://`; `--insecure` libera o resto);
o token vai em `DJO_ASK_TOKEN`, `--token-file` ou `--token-stdin` (nunca em argumento); redirecionamentos não são
seguidos; o texto do servidor sai com os caracteres de controle do terminal escapados. `--json` imprime a resposta como veio.
Códigos de saída: 0 ok · 1 uso · 2 rede ou servidor · 3 recusado, não entendi ou site sem `/ask` · 4 token recusado ou limite.
`djo://site/ask='...'` é só açúcar para o endereço https: nenhum handler é registrado no sistema.

Palavras de ligação (`o a que tenha tem the which has...`) são ignoradas entre as condições; `e`/`and`/`,` ligam condições (só
E). Tag: `tem a tag X`, `com a tag X`, `marcados com X`, `tagged X`, `has the tag X`. Texto: `titulo contem|contendo|comeca com|igual a X`.
Recusado de propósito, com o motivo (kind `unsupported`, 422): OU/or, negação, mês por extenso e datas relativas.

**Texto longo (`detail`) e um item só.** Um campo `{"kind": "text", "detail": true}` (o corpo de um post, por exemplo) **nunca sai em lista, em contagem nem no JSON `data`**; continua valendo em filtro (`posts com texto contendo "go"`) se o nível o enxerga. Ele só vem quando a pessoa pede **um item**: o tipo declara `"show_by": ["slug", "title"]` (até 4 campos text ou enum, na ordem) e a frase é `mostre o post "bem-vindo"`, `show post "welcome"`, `show me the post bem-vindo` ou `o post bem-vindo` (tipo no singular, sem cláusulas depois). Um achado = todos os campos que o nível enxerga, com o texto longo (`item` e `detail_fields` na resposta); vários = uma lista curta sem o texto longo, pedindo para ser mais específico; nenhum = "não encontrei", **igual** para um item que o nível não enxerga (`where`) e para um que não existe. O `detail` só aparece se estiver na lista `fields` do nível e não estiver em `Ask.deny`. O texto devolvido é cortado em `detail_max_bytes` (`"truncated": true`). No `djo ask` o item sai como `campo: valor`, com o texto longo por último; na página `/ask` ele vai em `<pre>` escapado (nada de Markdown nem HTML).

**Aliases em português.** Nomes de campo e de tipo vêm do `ask.djo` (`Ask.alias:"post.title":["titulo"]`). Para nomes comuns de
**campo** o djo já entende um apelido padrão (`title`=titulo, `text`=texto, `body`=corpo/conteudo, `tags`=etiquetas, `status`=situacao,
`name`=nome, `views`=visualizacoes, `created_at`=criado em/criado, `published_at`=publicado em/publicado...), só se o campo existe e o nível o
enxerga. O que o `ask.djo` declara vence o padrão; tipos nunca têm padrão (declare `Ask.alias:"post":["artigos"]`).

> **Não verificado:** consultas em PostgreSQL e MySQL (o SQL só foi conferido como texto) e parâmetros `Date`/`DateTime` nesses bancos.

## Boosters

| Comando | O que faz |
|---|---|
| `djo booster list` | lista os boosters do projeto |
| `djo booster search [texto]` | procura no registro |
| `djo booster create [-perm Http,File] nome` | cria a estrutura de um booster novo |
| `djo booster add nome[@versao]` | baixa do registro, confere o SHA256 e instala em `boosters/` (anota no `boosters.lock`) |
| `djo booster add endereço -sha256 X` / `add pasta` | instala de um endereço https ou de uma pasta |
| `djo booster pack [-registro pasta] pasta` | empacota um booster para publicar |
| `djo booster remove nome` | remove |
| `djo booster verify` | confere as pastas com o `boosters.lock` |

No código: `nome.função:argumento`, por exemplo `markdown.render:texto`. Um booster só enxerga o próprio código, e o projeto só alcança as `f` do `main.djo` dele. Veja [Boosters](12-boosters.md).

## Testes (`djo spec`)

```
djo spec [-e filtro] [--json] [--lang en] [--seed N] [pasta | arquivo_spec.djo]
```

Roda as funções `it_...` dos arquivos `*_spec.djo` (com `antes` e `depois` opcionais), cada uma isolada e com os bancos SQLite numa pasta temporária. Sai com `1` se algum teste falhar ou der erro. O namespace `Spec` (`fail`, `skip`, `try`, `lang`, `emails`, `jobs`, `run_jobs`, `run_job`, `agenda`, `get`, `post`, `put`, `patch`, `delete`) **só existe dentro do `djo spec`**; fora dele, qualquer chamada dá erro. Os matchers (`expect.eq:real:esperado`, `include`, `raises`, `status`, `json`...) vêm do booster `expect`. A pasta `spec/` nunca é servida nem entra no `djo build`. Veja [Testes](14-testes.md).

## Mensagens de erro

Todo erro traz a linha: `erro (linha 12): ...`. Os mais comuns:

| Mensagem | Causa |
|----------|-------|
| `'x' é Arr e não pode receber Int` | atribuição contra o tipo travado |
| `não dá para usar '+' entre String e Int` | texto que não é número num `+` |
| `índice 5 fora da lista (tamanho 3)` | índice inválido em lista |
| `a condição do if precisa ser Bool, mas é Int` | condição que não é `true`/`false` |
| `'super' só existe dentro de método de uma struct com sonof` | `super` fora de lugar |
| `o core só declara structs e funções` | código solto em `core/` |
| `Spec.fail só existe quando os testes rodam com 'djo spec'` | `Spec` chamado fora do `djo spec` (veja [Testes](14-testes.md)) |
| `o booster 'x' usa File mas não declarou essa permissão` | falta a permissão no `booster.json` (veja [Boosters](12-boosters.md)) |
| `a página demorou demais` | passou do limite de passos |
| `o promise não altera variáveis de fora ('x')` | atribuição a uma variável de fora, dentro de um promise: devolva o valor com `return` |
| `'Session.set' altera a resposta ou a sessão da página` | `Session`, `redirect`, `status`, cabeçalhos ou cookies dentro de um promise |
| `um promise precisa de success, error ou finally` | promise sem nenhuma cláusula, ou fora da ordem promise, success, error, finally |

## Ainda não existe

Compilação para WASM, GPIO, I2C e SPI, expressões regulares, formatação de números (`format`), datas em outros fusos e a assinatura de apps. Sessões em disco (hoje ficam na memória) também.
