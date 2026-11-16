# /ask — perguntas em linguagem natural (RASCUNHO técnico)

> **Estado:** rascunho dos marcos (a)–(e). Existem a **política** (`ask.djo`), o **motor de
> consulta parametrizado**, o **parser de frases** (PT/EN) e o **endpoint HTTP** `/ask`. **Ainda não existe** o endpoint
> HTTP `/ask`, a página humana, `Ask.reply` em uso, `per_minute`, nem escrita. A especificação
> do autor prevalece sobre este texto onde divergirem.

O `/ask` é para **humanos** e para sites de terceiros falarem com o site em linguagem natural.
Agentes continuam usando as *actions* e o manifesto. Tudo é determinístico: sem modelo.

## ask.djo

Só aceita chamadas `Ask.*` (qualquer outra coisa é erro). Sem o arquivo, o `/ask` fica
desligado. O arquivo **nunca é servido** e é conferido ao iniciar o servidor e no `djo build`.

```
Ask.type:"post":{"table": "posts", "struct": "Post", "fields": {
    "title": {"column": "titulo", "attr": "titulo"},
    "status": {"kind": "enum", "values": ["draft", "published"], "attr": "status"},
    "published_at": {"column": "publicar_em", "attr": "publicado", "main": true},
    "created_at": {"column": "criado", "attr": "criado", "as": "epoch"},
    "tags": {"kind": "tags", "from": "post_tags", "key": "post_id", "value": "tag", "attr": "tags"}
}}
Ask.level:"public":{"allow": [{"type": "post", "where": {"status": "published"}, "fields": ["title", "tags"]}]}
Ask.level:"agent":{"allow": [{"type": "post"}]}
Ask.deny:["post.secret"]
Ask.alias:"post":["posts", "artigos"]
Ask.limits:{"max_results": 50, "timeout_ms": 3000}
```

- **Tipos** vêm de `Ask.type`; a fonte (tabela, colunas) é declarada ali, nunca deduzida da frase.
- **Níveis** `public < agent < admin`. O nível sai do token/sessão e é decidido **antes** de
  interpretar a frase. Um nível herda os tipos do inferior; a regra do próprio nível troca a herdada.
- **`where`** de um nível é sempre aplicado e não pode ser afrouxado por filtro algum.
- **`Ask.deny`** vence tudo: leitura, filtro, ordem e contagem.
- Campo ou tipo **escondido é indistinguível de inexistente** (mesmo erro, mesma mensagem).
- Tipos de campo: texto, número, enum, data/datetime (coluna de texto ou `"as": "epoch"`), `tags`.

## Motor de consulta

Entrada: plano estruturado (tipo, filtros, ordem, campos, página, limite, contagem). Operadores:
`eq, contains, starts, in, gt, gte, lt, lte, between, from, to, before, after, year, has`.

- Valores sempre vão como **parâmetros** do SQL; nomes de campo/operador/tipo/ordem nunca viram SQL.
- `LIKE` escapa `%`, `_` e o escape; texto até 200 caracteres, sem byte NUL.
- `Date` num campo `DateTime` cobre o dia inteiro (UTC); `to`/`between` são inclusivos.
- `max_results` é teto; busca-se uma linha a mais para saber se há próxima página; `timeout_ms` corta a consulta.
- Erro do banco não vaza detalhe na mensagem (fica em `Cause`, só para log).

## Parser de frases (`parsePhrase(level, frase)`)

Função pura: frase + nível → plano (`askPlan`) ou erro que ensina. Linguagem controlada, sem modelo;
tabelas EN/PT em `askLexicons` (verbos, cláusulas, operadores), fáceis de ampliar.

```
VERBO [artigos] TIPO { CLÁUSULA }
list | show me | give me | find | display | I want to see | count | how many
liste | lista | mostra | mostre | quero ver | traz | me traz | me dê | veja | conte | quantos | quantidade de
```

Sem verbo também vale, quando não há dúvida do idioma: `first 2 posts`, `primeiros 2 posts`, `posts de 2025`,
`posts from 2025-01-01 to 2025-03-30`. Só o tipo (`posts`) não basta.

| Cláusula | EN | PT |
|---|---|---|
| intervalo (inclusivo) | `from D to D` | `de D até D` |
| ano | `in 2025`, `from 2025` | `em 2025`, `de 2025` (`de 2024 até 2025` = os dois anos inteiros) |
| um lado | `since / until / before / after / on D` | `desde / até / antes de / depois de / no dia D` |
| ano | `in 2025` | `em 2025` |
| campo | `with title contains "x"`, `views at least 5` | `com titulo contem "x"`, `views pelo menos 5` |
| tag | `tagged go` | `com a tag go` |
| ordem | `sorted by views desc`, `newest first` | `ordenados por views decrescente`, `mais antigos primeiro` |
| limite/página | `first 5`, `page 2` | `primeiros 5`, `pagina 2` |

Regras:
- O idioma vem do primeiro verbo; palavra do outro idioma = erro `mixed_language` que ensina.
- Datas: ISO sempre; `dd/mm/aaaa` só em PT; `dd/mm` ou `dd/mm/aaaa` em EN pedem ISO; sem datas relativas.
- Data sem campo vale para o campo `"main": true` do tipo (ou diga o campo antes: `created_at from D to D`).
- Texto vai entre aspas; o conteúdo entre aspas é sempre só valor.
- Verbos de escrita (`delete`, `apague`, `update`, `crie`…) → `not_accepted`; a gramática não tem escrita.
- Tipo/campo escondido ou inexistente: **a mesma resposta** ("não entendi"), listando só o que o nível enxerga.
- A frase nunca é devolvida: só uma palavra reduzida a `[a-z0-9_-]` (até 30) pode aparecer na mensagem.
- Limites: 300 bytes, 60 palavras, 6 condições; bytes de controle/UTF-8 inválido são recusados.

## Endpoint `/ask` (só leitura)

Sem `ask.djo` o `/ask` não existe (cai nas rotas normais: 404 igual a qualquer caminho). Não aparece no
manifesto nem no `llms.txt`. Funciona nos modos páginas e rotas.

```
Ask.db:"site.db"                      # banco consultado (arquivo do site ou postgres:// / mysql://)
Ask.level_from:"nivel_do_pedido"      # função do core/, sem parâmetros: devolve "public" | "agent" | "admin"
Ask.limits:{"per_minute": 30, "global_per_minute": 600}
```

- **Nível:** o djo não tem autenticação embutida; o projeto decide. `nivel_do_pedido` lê o pedido (`Http.bearer:`,
  sessão…) e devolve o nível. Falha fechada: sem gancho, erro, `nil` ou valor desconhecido = `public`.
  Token Bearer **presente** que o gancho não aceita (`nil`/desconhecido/erro) = **401**, nunca rebaixa em silêncio.
  Sem `Ask.level_from`, tokens não são aceitos (401) e tudo é `public`. O nível é decidido antes de ler a pergunta.
- **Toda pergunta é um POST.** Nada é processado por GET: a pergunta não vai na URL (some de logs de acesso, histórico,
  `Referer` e cache). `GET /ask?q=...` ignora a pergunta (não responde dados e não a reflete).
- **POST `/ask`** `{"question": "...", "page": 2, "lang": "pt"}` (JSON, até 4 KB; `page` e `lang` opcionais; campo
  desconhecido = 422) → `{lang, answer, type, data, page, limit, more, next, count, calls, confirm: null}`. Em contagem vem
  `count` em vez de `data`. `answer` é um texto curto no idioma da pergunta; a página também pode vir na frase
  (`page 2`/`pagina 2`). `next` é só o corpo do próximo POST (`{"page": 2}`), nunca uma URL.
  `lang` (`en`/`pt`) e o `Accept-Language` só escolhem em que idioma saem as mensagens quando a frase não revela o idioma;
  não mudam como a frase é lida. Sem pista nenhuma, o "não entendi" sai nos dois idiomas, uma linha para cada.
- **GET `/ask`** no navegador = página com a caixa de pergunta e exemplos (só dos tipos do nível). Ela envia por
  **POST de formulário** (`application/x-www-form-urlencoded`, campos `question` e `page`, os mesmos limites do JSON) e a
  resposta volta na própria página, em tabela; tudo escapado, CSP `default-src 'none'; form-action 'self'`. O POST de
  formulário só vale vindo da própria origem (`Origin` e `Sec-Fetch-Site` conferidos); um POST de outro site não lê a
  resposta (sem CORS) e é recusado. Hoje só há leitura; se um dia houver escrita, o nível por cookie/sessão exigirá CSRF.
- **Erros:** 401 token recusado · 403 `not_accepted` (escrita) · 413 corpo grande · 415 content-type · 422 não entendi / valor
  inválido / idiomas misturados · 429 limite (com `Retry-After`) · 504 tempo esgotado · 503 banco indisponível.
- **`Ask.reply`:** `refused` (não aceito) e `unknown` (não entendi) trocam a mensagem padrão, por idioma ou `"*"`.
- **Limites:** `per_minute` por IP (antes do gancho) e por token; `global_per_minute` opcional. Atrás de proxy o IP é o do
  proxy (o djo não confia em `X-Forwarded-For`).
- Sem CORS por padrão; `Cache-Control: no-store`; `Vary: Authorization, Cookie, Accept`.

### Atrás de proxy (`Ask.proxy`)

Por padrão o IP de quem pergunta é o da conexão e `X-Forwarded-For` é **ignorado** (ele pode ser forjado).
`Ask.proxy:["127.0.0.1", "10.0.0.0/8"]` lista os proxies de confiança: só se a conexão vem de um deles o header é lido,
da direita para a esquerda, pulando os proxies listados; vale o último IP que não é de confiança. Cadeia inválida ou
só de proxies = o IP da conexão. `0.0.0.0/0` é recusado. Sem isso, atrás de nginx o limite por IP vira um limite
único para todos os anônimos. O contador (`per_minute`) é por processo e em memória.

## Cliente: `djo ask`

Veja `docs/06-referencia.md` ("Ask"). Resumo: `djo ask site "pergunta"`, token em `DJO_ASK_TOKEN`/`--token-file`/`--token-stdin`,
https obrigatório fora de localhost, saída sem caracteres de controle, códigos de saída 0–4.

### Palavras de ligação, tags e o que continua recusado

- Palavras de ligação são ignoradas entre as condições: PT `o a os as um uma que quais tenha tem tiver seja também`, EN
  `the a an that which have has is are also`. `e`/`and`/`,` ligam condições (só **E**).
- Tag: `tem a tag X`, `tenha tag X`, `com a tag X`, `marcados com/como X`, `etiqueta X`, `tag X`; EN `has the tag X`,
  `tagged (with) X`, `with the tag X`, `tag X`.
- Texto: `titulo contem|contendo|comeca com|igual a X` (e `title contains|containing|starting with|equal to X`), com ou sem
  `o`/`cujo`/`com`; `X` entre aspas ou uma palavra solta.
- Recusado de propósito, **com o motivo** (kind `unsupported`, 422): `ou`/`or` (só existe E), negação (`não`, `sem`, `not`,
  `without`, `exceto`), nomes de mês por extenso e datas relativas (`hoje`, `last week`, `ultimo mes`). Escrita: `not_accepted` (403).
- Quando não entende, a mensagem diz o que já foi lido e onde parou, e sugere um exemplo do tipo de cláusula que falhou
  (data, texto, tag, número...), só com campos que o nível enxerga. A frase digitada nunca volta: só palavras simples ou valores
  simples (letras, números, espaço, `_ - .`).

Palavras de ligação (`o a que tenha tem the which has...`) são ignoradas entre as condições; `e`/`and`/`,` ligam condições (só
E). Tag: `tem a tag X`, `com a tag X`, `marcados com X`, `tagged X`, `has the tag X`. Texto: `titulo contem|contendo|comeca com|igual a X`.
Recusado de propósito, com o motivo (kind `unsupported`, 422): OU/or, negação, mês por extenso e datas relativas.

**Aliases em português.** Nomes de campo e de tipo vêm do `ask.djo` (`Ask.alias:"post.title":["titulo"]`). Para nomes comuns de
**campo** o djo já entende um apelido padrão (`title`=titulo, `text`=texto, `body`=corpo/conteudo, `tags`=etiquetas, `status`=situacao,
`name`=nome, `views`=visualizacoes, `created_at`=criado em/criado, `published_at`=publicado em/publicado...), só se o campo existe e o nível o
enxerga. O que o `ask.djo` declara vence o padrão; tipos nunca têm padrão (declare `Ask.alias:"post":["artigos"]`).

## Verificação

Testado em SQLite (`ask_engine_test.go`, `ask_test.go`), em todos os perfis de build.
O SQL para **PostgreSQL e MySQL só foi conferido como texto** (aspas, marcadores, parâmetros);
**não foi executado contra servidores reais**.
