# Boosters

Um **booster** é código pronto, escrito em djo, que o seu projeto chama pelo nome. Os oficiais são o `markdown`, que converte Markdown em HTML ([guia](17-booster-markdown.md)), e o `expect`, os matchers do `djo spec` ([guia](18-booster-expect.md)).

## Usar

```
djo booster search               # o que o registro tem
djo booster add markdown         # baixa, confere o SHA256 e instala
```

Isso baixa o booster, confere o SHA256 do arquivo com o do índice do registro, copia para `boosters/markdown/` e anota a versão, o SHA256 da pasta e a origem no `boosters.lock`. Depois é só chamar, de qualquer página ou do `core/`:

```
var r = markdown.render:texto
print r["html"]
```

A chamada é `nome.função:argumento`, como as funções nativas (`Json.encode:x`). O nome do booster é o nome da pasta.

| Comando | O que faz |
|---|---|
| `djo booster list` | lista os instalados, com a versão e as permissões que pedem |
| `djo booster search [texto]` | procura no registro |
| `djo booster add nome[@versao]` | baixa do registro e instala (sem versão, a última) |
| `djo booster add endereço -sha256 X` | baixa de um endereço https e instala |
| `djo booster add pasta` | instala o booster que está em `pasta` |
| `djo booster create nome` | cria a estrutura básica de um booster novo |
| `djo booster pack pasta` | empacota um booster num `.tar.gz` para publicar |
| `djo booster remove nome` | remove o booster e a linha dele no lock |
| `djo booster verify` | confere as pastas com o `boosters.lock` |

O `djo serve` e o `djo build` fazem a conferência do lock sozinhos. Se alguém mexer no código de um booster depois de instalado, o servidor se recusa a subir e diz qual.

## O registro

O registro oficial fica em `https://djolang.zfxsoftware.cloud/registro`, e a lista está em [/boosters](https://djolang.zfxsoftware.cloud/boosters). Para usar outro, passe `-registro endereço` ou defina `DJO_REGISTRO`.

Um registro é só uma pasta (ou um site) com arquivos:

```
registro/indice.json
registro/markdown/1.1.0/markdown-1.1.0.tar.gz
```

O `indice.json` diz, para cada booster, a descrição, as permissões, a última versão e o endereço e o SHA256 do arquivo de cada versão:

```
{
  "formato": "djo-boosters/1",
  "boosters": [
    {"nome": "markdown", "descricao": "...", "permissoes": [], "ultima": "1.1.0",
     "versoes": [{"versao": "1.1.0", "url": "markdown/1.1.0/markdown-1.1.0.tar.gz", "sha256": "..."}]}
  ]
}
```

**Como a instalação se protege**
- Só endereços `https` são aceitos (`http` apenas em `localhost`, para testes), e um redirecionamento para `http` é recusado.
- O SHA256 do arquivo baixado tem que bater com o do índice. Se não bater, nada é instalado.
- O arquivo só pode ter arquivos comuns: links, caminhos com `..` ou absolutos, mais de 300 arquivos ou mais de 4 MB descompactado são recusados.
- Depois de extraído, o booster passa pelas mesmas regras de uma pasta local (`main.djo`, `core/`, manifesto válido), e o nome e a versão têm que ser os que o índice anunciou.
- Baixar de um endereço solto exige o `-sha256`: sem ele não há como saber que o arquivo é o que você espera.

O SHA256 do índice vem do mesmo servidor do arquivo. Ele protege contra download corrompido e contra arquivo trocado depois, mas quem controla o registro controla os dois. Por isso o `boosters.lock` guarda também o SHA256 da pasta instalada: depois do `add`, qualquer mudança é percebida.

## Publicar

```
djo booster pack meubooster                       # gera meubooster-1.0.0.tar.gz e mostra o SHA256
djo booster pack -registro pasta-do-registro meubooster
```

Com `-registro`, o arquivo vai para `pasta-do-registro/nome/versao/` e o `indice.json` é atualizado (a última versão, a descrição e as permissões vêm da versão mais nova). O arquivo é sempre igual para o mesmo conteúdo. **Uma versão publicada não muda**: publicar de novo a mesma versão com outro conteúdo dá erro, e a saída é aumentar a versão no `booster.json`. Depois, é só servir a pasta por https.

## A regra de mão única

O projeto chama os boosters, **nunca o contrário**. Dentro de um booster só valem as funções do próprio booster e as funções nativas da linguagem. Ele não enxerga o `core/` do projeto, as páginas nem as variáveis dele. Por isso um booster funciona igual em qualquer site, e instalar um não muda nada do que você já tem.

Uma variável com o mesmo nome do booster esconde o booster naquele trecho, como acontece com as funções nativas.

## Permissões

`Json`, `Time`, `Hash` e as funções de texto, lista e mapa estão sempre liberadas. Os namespaces que mexem no mundo (`Http`, `File`, `Db`, `Session`, `Serial`, `Route`, `Env`, `Mail`, `Job`, `Crypto` e `Spec`) só funcionam se o booster pedir no `booster.json`. O `djo booster add` avisa quais ele pediu antes de instalar. O `markdown` não pede nenhuma. O `expect` (os matchers do [`djo spec`](14-testes.md)) pede só `Spec`, que só existe dentro do `djo spec`.

## O main.djo e o core do booster

Todo booster tem duas partes:

```
boosters/markdown/
  booster.json
  main.djo         a API: só as f daqui o projeto enxerga
  core/            o interno: só o próprio booster enxerga
    markdown.djo
    destaque.djo
```

- **`main.djo` é obrigatório** e é o único `.djo` fora do `core/`. As `f` dele viram `nome.função` para o projeto. Ele só declara funções.
- **`core/` guarda o trabalho de verdade**: funções, `struct` e `interface` de uso interno. O `main.djo` usa o que está no `core/`, e o projeto só alcança o que o `main.djo` expõe. `markdown.md_inline:x` dá erro, e o tipo `Pessoa` de um booster não existe no projeto.
- **Os dois mundos não se misturam.** O booster pode ter uma `struct Pessoa` e o projeto outra, e uma função `ajuda` em cada um, sem conflito. Dentro do booster valem os nomes dele; fora, os do projeto.
- Uma struct do `core/` que o `main.djo` devolve pode ser usada normalmente (`q.nome`, `q.ola:`), mas o projeto não consegue escrevê-la como tipo em `params[...]`.
- Uma função do `main.djo` não pode ter o mesmo nome de uma do `core/` do mesmo booster.

## Criar um booster

```
djo booster create meubooster
```

cria a pasta `meubooster/` pronta, com uma função de exemplo (`-perm Http,File` já declara permissões). Teste num projeto com `djo booster add meubooster`. Ela tem:

```
meubooster/
  booster.json     nome, versão, descrição e permissões
  main.djo         a API (a função ola, de exemplo)
  core/base.djo    o interno que o main.djo usa
```

```
{
  "nome": "meubooster",
  "versao": "1.0.0",
  "descricao": "o que ele faz, numa frase",
  "permissoes": []
}
```

O nome usa minúsculas, números e `_`, e começa com letra. Qualquer outro `.djo` na raiz é recusado: mova para o `core/`. `struct`, `interface` e código solto só valem dentro do `core/` (e código solto nem lá). Links simbólicos são recusados. O limite é de 4 MB por booster. O `djo build` leva os boosters para dentro do executável.

## Mensagens de erro

| Mensagem | Causa |
|---|---|
| `o booster 'x' usa File mas não declarou essa permissão` | falta a permissão no `booster.json` |
| `o booster x não tem a função 'f'` | a função não está no `main.djo` (talvez esteja só no `core/`) |
| `o booster 'x' não tem a função 'f' (um booster não enxerga o core do projeto)` | o booster chamou algo que é do projeto |
| `boosters/x foi alterado: o SHA256 não bate` | o código mudou depois do `djo booster add` |
| `falta o main.djo` | o booster não tem a API |
| `fora do core/ só existe o main.djo` | outro `.djo` na raiz do booster |
| `x não está no boosters.lock` | pasta em `boosters/` que não foi instalada com o `add` |

## O que ainda não existe

Não há `djo booster update` (hoje é `remove` e `add`), assinatura dos boosters, nem boosters que dependam de outros. As structs do `core/` também não são públicas. E o registro oficial é um só, mantido à mão.
