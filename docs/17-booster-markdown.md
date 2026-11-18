# O booster `markdown`

Converte Markdown em HTML, escrito em djo. É o booster que gera as páginas de documentação do site da djo. Não pede nenhuma permissão: só transforma texto em texto (a regra geral dos boosters está em [Boosters](12-boosters.md)).

```
djo booster add markdown
```

```
var r = markdown.render:"# Olá\n\nUm **teste**."
print r["titulo"]
print r["html"]
```

Saída real:

```
Olá
<p>Um <strong>teste</strong>.</p>
```

A API tem só duas funções, `render` e `codigo`. O resto (texto corrido, tabelas, listas, realce) é interno e não pode ser chamado de fora: `markdown.md_inline:x` dá `o booster markdown não tem a função 'md_inline'`.

Esta página descreve o `markdown` **1.2.0**. **Leia a seção [Segurança](#segurança) antes de converter texto que não é seu** (comentários, textos de visitantes): o HTML cru é escapado e os links só aceitam endereços seguros, mas a saída entra na página e vale saber como usá-la. As mudanças em relação à 1.1.0 estão em [O que mudou na 1.2.0](#o-que-mudou-na-120).

## `markdown.render:texto`

| | |
|---|---|
| Parâmetro | `texto`: **String**. Qualquer outro tipo dá `erro (linha N): o parâmetro 'texto' de 'markdown.render' deve ser String, mas recebeu Int` |
| Devolve | um mapa com quatro chaves: `html`, `titulo`, `titulo_html`, `toc` |
| Texto vazio | `{"html": "", "titulo": "", "titulo_html": "", "toc": []}` |
| Limites | não há limite de tamanho. Um arquivo de 21 KB (a referência da djo) converte em cerca de 0,14 s |

### O mapa devolvido

| Chave | Tipo | O que é |
|---|---|---|
| `html` | String | o corpo já em HTML, uma construção por linha, terminando em `\n`. **Não** inclui o `# Título` do documento |
| `titulo` | String | o texto da **última** linha `# Título` do documento (cada `# ` troca o valor; só um deveria existir). Vazio se não houver. É **texto puro, sem escape** (o que está escrito, com as crases e o `&`): no HTML, escreva com `{{ }}` (que escapa), nunca com `{{! }}` |
| `titulo_html` | String | o mesmo título **já escapado e convertido** (`<code>`, `<em>`, `&amp;`), no formato do `titulo` de cada item do `toc`. Escreva com `{{! }}`. Novo na 1.2.0 |
| `toc` | lista | um item por título `## ` (só o nível 2): `{"id": ..., "titulo": ...}`. O `titulo` do `toc` **já é HTML** (com `<em>`, `<code>`, `&amp;`): escreva com `{{! }}`. Os títulos `###` ficam no `html`, mas **não** entram no `toc` |

Exemplo (entrada e saída reais):

```
var r = markdown.render:"# Título do doc\n\nUm parágrafo com **negrito**, *itálico*, `código` e um [link](04-web.md).\n\n## Seção um\n\n### Sub"
print r["titulo"]
print r["toc"]
print r["html"]
```

```
Título do doc
[{"id": "secao-um", "titulo": "Seção um"}]
<p>Um parágrafo com <strong>negrito</strong>, <em>itálico</em>, <code>código</code> e um <a href="/docs/web">link</a>.</p>
<h2 id="secao-um">Seção um</h2>
<h3 id="sub">Sub</h3>
```

O `id` do título é o texto sem acentos, em minúsculas, com `-` no lugar do que não é letra ou número (`Ação rápida!` vira `acao-rapida`). **Dois títulos com o mesmo texto geram o mesmo `id`** (a djo não numera): o âncora vai para o primeiro.

### O que o conversor entende

| Construção | Como escrever | Saída |
|---|---|---|
| Título do documento | `# Texto` | vai para `titulo`, **não** para o `html` |
| Título de seção | `## Texto` | `<h2 id="...">`, e entra no `toc` |
| Subtítulo | `### Texto` | `<h3 id="...">`, fora do `toc` |
| Parágrafo | linhas seguidas | `<p>`; as quebras de linha viram espaço; uma linha em branco separa |
| Lista | linhas com `- ` | `<ul><li>`; uma linha seguinte sem `- ` continua o item anterior |
| Lista numerada | `1. ` (qualquer número, depois `. `) | `<ol><li>`; os números escritos **não** importam (`3.` e `4.` viram itens 1 e 2). Linhas indentadas continuam o item, e uma cerca de código indentada fica dentro dele |
| Tabela | linhas que começam com a barra vertical | `<div class="tabela"><table>`, com `<thead>` e `<tbody>` |
| Bloco de código | cerca de três crases | `<pre class="code code-...">` com realce (veja [O realce](#o-realce-de-código)) |
| Negrito | `**texto**` | `<strong>` |
| Itálico | `*texto*` | `<em>` (o `*` de abertura não pode ter espaço depois, nem o de fechamento antes) |
| Código em linha | texto entre duas crases | `<code>`, com o conteúdo escapado e **sem** interpretar `**` e `*` dentro |
| Link | `[texto](destino)` | `<a href="...">`; o texto aceita negrito, itálico e código |

Dentro de itens de lista, células de tabela, títulos e links valem as mesmas marcações em linha (negrito, itálico, código, link).

Exemplo de lista, lista numerada e tabela (entrada e saída reais):

```
var r = markdown.render:"- item a\n- item b\n  continua\n\n1. primeiro\n2. segundo\n\n| a | b |\n|---|---|\n| 1 | **2** |"
print r["html"]
```

```
<ul>
<li>item a</li>
<li>item b continua</li>
</ul>
<ol>
<li>primeiro</li>
<li>segundo</li>
</ol>
<div class="tabela"><table>
<thead><tr><th>a</th><th>b</th></tr></thead>
<tbody>
<tr><td>1</td><td><strong>2</strong></td></tr>
</tbody></table></div>
```

### Links para outros `.md`

Um destino relativo que **termina em `.md`** vira um caminho de `/docs/`: a pasta e o prefixo numérico são tirados (`04-web.md` → `/docs/web`, `../02-structs.md` → `/docs/structs`, `README.md` → `/docs/README`). **Não** são reescritos os endereços de fora: os que têm esquema (`https://e.com/x.md`) e os que começam com `//` (`//e.com/x.md`) ficam como estão. Um destino com âncora (`docs/04-web.md#api`) não termina em `.md` e também fica. Os demais (`/pagina`, `#ancora`) passam sem mudar, só escapados.

### Endereços de link aceitos

O destino de um link só vira `<a href>` se for **seguro**:

- **Aceitos:** `http:`, `https:` e `mailto:` (sem diferença entre maiúsculas e minúsculas: `HTTPS://X` vale), caminhos relativos (`pagina`, `/pagina`, `../a.md`), endereços sem esquema que começam com `//` e âncoras (`#secao`).
- **Recusados:** qualquer outro esquema (`javascript:`, `data:`, `vbscript:`, `file:`...) e qualquer coisa que **tente esconder** o esquema (espaços, tabulação ou quebra de linha no meio, como `java<TAB>script:`; espaço no começo também). Um link recusado **não vira link**: sai só o texto dele (`[texto](javascript:x)` vira `texto`), sem aviso e sem erro.
- O endereço de um link termina no **primeiro `)`**, então um endereço com parêntese dentro (como `a_(b)`) fica cortado ali.

Um destino como `&#106;avascript:x` não vira script: o `&` é escapado (`href="&amp;#106;avascript:x"`), então o navegador lê o texto como um caminho relativo comum.

### O que NÃO é suportado

Estes casos **não dão erro**: a marcação sai como texto (escapado) ou, em alguns, com um efeito diferente do esperado.

- Imagens `![alt](arquivo.png)`: sai `!` seguido de um link comum (`!<a href="a.png">alt</a>`).
- Citação `> texto` e linha horizontal `---`: viram parágrafo (`<p>&gt; texto</p>`, `<p>---</p>`).
- Título `####` ou mais fundo, e `#` sem espaço depois (`#etiqueta`): viram **parágrafo**, com o texto como está (`<p>#### x</p>`). O `render` nunca trava por causa disso (na 1.1.0 travava).
- Lista com `*` ou `+`, lista aninhada, lista de tarefas `[ ]`: uma linha `  - b` depois de `- a` é lida como **continuação** do item (`- a\n  - b` vira um item `a - b`), e `* a` ou `+ c` viram parágrafo ou continuação.
- Itálico com `_`, negrito com `__`, tachado `~~`: ficam como texto.
- Link por referência (`[a][b]`), link automático (`<https://...>`), nota de rodapé, quebra de linha forçada.
- **HTML cru**: é escapado (veja [Segurança](#segurança)), então `<b>x</b>` aparece como texto.
- Tabela: a **segunda linha é sempre tratada como o separador** (`|---|---|`), mesmo que não seja. Sem o separador, a primeira linha de dados some. Não há alinhamento de colunas (`:---:` é ignorado). Uma célula não pode ter a barra vertical (a barra com contrabarra não escapa: a célula é cortada ali).
- Várias linhas em branco seguidas valem uma só; espaços no começo de um parágrafo são cortados.

## `markdown.codigo:fonte`

Um bloco de código com realce, em HTML. É o que o `render` usa nas cercas, e serve para mostrar um trecho avulso numa página.

| | |
|---|---|
| Parâmetro | `fonte`: **String** (`... deve ser String, mas recebeu Int` para outro tipo) |
| Devolve | uma String: `<pre class="code code-LINGUAGEM"><code>...</code></pre>\n` |

```
print markdown.codigo:"var x = 1\nprint \"oi\" // c"
```

```
<pre class="code code-djo"><code><span class="t-k">var</span> x = <span class="t-n">1</span>
<span class="t-k">print</span> <span class="t-s">&#34;oi&#34;</span> <span class="t-c">// c</span></code></pre>
```

### O realce de código

O realce é **só para a própria linguagem djo**. O conversor **não lê** o texto depois das três crases de abertura (como `djo` ou `js`): ele é ignorado, e a linguagem é adivinhada pelo começo do código, nesta ordem:

1. **Shell** (`code-sh`): o código, sem espaços no começo, começa com um destes prefixos: `djo `, `$ `, `go `, `CGO_`, `./`, `scp `, `PORT=`, `DJO_`, `git `, `curl `, `GET `, `POST `, `sudo `, `sha256sum`, `id,titulo`, `{`, `[` ou `# `. Só os comentários (a partir de `#`) são realçados; o resto sai escapado.
2. **HTML** (`code-html`): começa com `<`. Sai escapado, **sem** realce. (O ramo para `{%` e `{{` existe no código, mas nunca é alcançado: como `{` já vale como shell, um bloco que começa com `{{ x }}` ou `{% var a = 1 %}` sai como `code-sh`.)
3. **djo** (`code-djo`): todo o resto, inclusive o texto vazio. Aqui há realce.

Em outras palavras: **JSON, JavaScript, Python, SQL e qualquer outra linguagem saem como `code-djo` ou `code-sh`**, sem realce próprio, e um JSON começando com `{` sai como `code-sh`. Para forçar uma aparência, mude o CSS pela classe (`code-sh`, `code-html`, `code-djo`), não o texto depois das crases.

As classes dos pedaços realçados em `code-djo` (o booster **não entrega CSS**: estilize você):

| Classe | O que é | Exemplo |
|---|---|---|
| `t-k` | palavra-chave: `f var arr map print params return true false nil input cond while until for eng if elif else match and or not pick struct sonof interface super nocall each in val case self` | `var`, `return` |
| `t-s` | texto entre aspas (com `\` escapando) | `"oi"` |
| `t-n` | número | `1`, `2.5` |
| `t-c` | comentário `//` até o fim da linha | `// c` |
| `t-y` | palavra que começa com maiúscula (tipos, structs, namespaces) | `Pessoa`, `String` |
| `t-f` | palavra seguida de `:` (chamada de função) | `soma` em `soma:1:2` |

Exemplo (real):

```
print markdown.codigo:"struct Pessoa\n    nome[String]\n\nvar p = Pessoa:\"Ana\" // cria\nprint soma:1:2.5"
```

```
<pre class="code code-djo"><code><span class="t-k">struct</span> <span class="t-y">Pessoa</span>
    nome[<span class="t-y">String</span>]

<span class="t-k">var</span> p = <span class="t-y">Pessoa</span>:<span class="t-s">&#34;Ana&#34;</span> <span class="t-c">// cria</span>
<span class="t-k">print</span> <span class="t-f">soma</span>:<span class="t-n">1</span>:<span class="t-n">2.5</span></code></pre>
```

Um bloco sem a cerca de fechamento vai até o fim do texto (o que vem depois da abertura vira o código).

## Segurança

A saída do `markdown` **entra na página**, então vale saber o que é escapado e o que não é. O escape usado é o da djo (`escape:`): troca `&` por `&amp;`, `<` por `&lt;`, `>` por `&gt;` e as aspas por `&#34;` e `&#39;`.

| O quê | Escapado? |
|---|---|
| Texto de parágrafos, itens, células e títulos (`html` e `toc`) | **Sim**. `<script>alert(1)</script>` sai `&lt;script&gt;alert(1)&lt;/script&gt;`, e `<b>x</b>` também |
| Conteúdo de `` `código` `` e dos blocos de código | **Sim** |
| Valor do `href` (as aspas viram `&#34;`, então não dá para sair do atributo) | **Sim** |
| Esquema do link (`javascript:`, `data:`, `vbscript:`...) | **Filtrado** desde a 1.2.0: só `http`, `https`, `mailto`, relativos e âncoras viram link (veja [Endereços de link aceitos](#endereços-de-link-aceitos)) |
| O mapa `titulo` | **Não é escapado**, por contrato: é o texto puro do `# Título` (`# <b>x</b>` devolve `<b>x</b>`). Use `{{ }}` |
| O mapa `titulo_html` | **Sim**, já é HTML seguro: `&lt;b&gt;x&lt;/b&gt;`. Use `{{! }}` |
| O `titulo` de cada item do `toc` | já é HTML escapado (com `<em>` e `<code>`). Use `{{! }}` |
| O atributo `id` dos títulos | passa pelo `slug` (só letras, números e `-`) |

Um link que usaria `javascript:` não vira link. Entrada e saída reais:

```
print (markdown.render:"[x](javascript:alert`1`) e [ok](https://ex.com/a)")["html"]
```

```
<p>x e <a href="https://ex.com/a">ok</a></p>
```

Como usar com segurança:

1. Escreva o `titulo` com `{{ r["titulo"] }}` (que escapa) **ou** o `titulo_html` com `{{! r["titulo_html"] }}`. Nunca o `titulo` com `{{! }}`: ele pode ter `<` e `&` do texto original.
2. Escreva o `html` e o `titulo` de cada item do `toc` com `{{! ... }}`: já são HTML.
3. Com Markdown de visitantes (comentários, perfis), a saída já vem sem HTML cru e sem links de script. Mesmo assim, **só mostre links de visitantes se quiser que o site deles seja alcançável**: um link `https://` é um link, e o seu site passa a apontar para ele. Para evitar isso, não use o `html` direto e mostre o texto.
4. Não há limite de tamanho no `render`: para texto de visitante, corte o tamanho antes (um arquivo de 21 KB converte em cerca de 0,14 s, e o tempo cresce com o texto).

Os boosters sem permissão **não tocam o mundo** (arquivo, rede, banco), mas isso não os torna inofensivos: a saída vai para a página, como acima.

## O que mudou na 1.2.0

Corrigiu três problemas da 1.1.0 e um comportamento:

- **O `render` não trava mais.** Na 1.1.0, uma linha começando com `#` que não fosse `# `, `## ` ou `### ` (como `#### x` ou `#etiqueta`) fazia o conversor repetir o mesmo passo para sempre. Agora essas linhas viram parágrafo, e nenhuma volta do laço deixa de avançar.
- **Esquemas perigosos nos links são recusados** (`javascript:`, `data:`, `vbscript:`...): sai só o texto, sem link.
- **`titulo_html`** é a versão escapada do título. O `titulo` continua texto puro, como na 1.1.0 (quem usa `{{ r["titulo"] }}` não precisa mudar nada).
- **Links `.md` de fora** (`https://e.com/x.md`, `//e.com/x.md`) não são mais reescritos para `/docs/`.

Quem usa o `markdown` 1.1.0 num site com Markdown de terceiros deve atualizar.

## Um exemplo numa página real

Um site com uma página `ler.djo.html`, que lê um arquivo Markdown e mostra o título, o sumário e o corpo:

```
{% var r = markdown.render:(File.read:"textos/ola.md") %}
<h1>{{ r["titulo"] }}</h1>
<ul>
{% for{ each[t in r["toc"]] %}
  <li><a href="#{{ t["id"] }}">{{! t["titulo"] }}</a></li>
{% } %}
</ul>
{{! r["html"] }}
```

Com `textos/ola.md`:

```
# Olá, mundo

Este texto veio de um arquivo **Markdown**.

## Primeiros passos

1. Instale o djo.
2. Rode `djo serve`.

## Código

Um bloco de código usa a cerca de três crases, como neste documento.
```

A resposta de `GET /ler`, real:

```
<h1>Olá, mundo</h1>
<ul>
  <li><a href="#primeiros-passos">Primeiros passos</a></li>
  <li><a href="#codigo">Código</a></li>
</ul>
<p>Este texto veio de um arquivo <strong>Markdown</strong>.</p>
<h2 id="primeiros-passos">Primeiros passos</h2>
<ol>
<li>Instale o djo.</li>
<li>Rode <code>djo serve</code>.</li>
</ol>
<h2 id="codigo">Código</h2>
<p>Um bloco de código usa a cerca de três crases, como neste documento.</p>
```

O site da djo faz isto com a documentação (`paginas/doc.djo.html`): lê o `.md`, chama `markdown.render:` e monta o menu lateral com o `toc`.

## Mensagens de erro

| Mensagem | Causa |
|---|---|
| `o parâmetro 'texto' de 'markdown.render' deve ser String, mas recebeu Int` | passou um não-texto para o `render` (o mesmo vale para `fonte` no `codigo`) |
| `'markdown.render' espera 1 argumento(s), recebeu 2` | argumentos a mais ou a menos |
| `o booster markdown não tem a função 'html'` | função que não está na API (só `render` e `codigo`) |

O `markdown` não tem erros próprios: texto estranho nunca dá erro, só uma saída diferente do esperado (ou o travamento descrito em [Segurança](#segurança)).
