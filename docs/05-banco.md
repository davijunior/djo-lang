# Banco de dados

`Db` fala com três bancos: o **SQLite**, embutido na própria linguagem (o banco é um arquivo, sem
servidor para instalar), e os servidores **PostgreSQL** e **MySQL/MariaDB**. As funções são as mesmas
nos três; muda só o argumento do `Db.open:`.

```
Db.open:"site.db"
Db.exec:"CREATE TABLE IF NOT EXISTS posts (id INTEGER PRIMARY KEY, titulo TEXT)"
Db.exec:"INSERT INTO posts (titulo) VALUES (?)":[Http.form:"titulo"]
var posts = Db.query:"SELECT * FROM posts ORDER BY id DESC LIMIT 10"
```

## Funções

| Chamada                        | Devolve |
|--------------------------------|---------|
| `Db.open:"arquivo.db"`         | nada; abre (ou cria) o banco SQLite |
| `Db.open:"postgres://..."` / `"mysql://..."` | nada; conecta a um servidor (veja abaixo) |
| `Db.exec:sql` ou `Db.exec:sql:[params]`  | mapa `{changes, last_id}` |
| `Db.query:sql:[params]`        | lista de mapas, um por linha |
| `Db.one:sql:[params]`          | o primeiro mapa, ou `nil` se não houver linha |
| `Db.begin:` `Db.commit:` `Db.rollback:` | transação |

- Chame `Db.open:` no começo de cada página (é barato: a conexão é reaproveitada).
  O arquivo precisa terminar em `.db` ou `.sqlite` e ficar dentro da pasta do site.
  Um `Db.open:` pode ser feito numa função do `core/`.
- As colunas viram chaves do mapa: `post["titulo"]`.

## PostgreSQL e MySQL

```
Db.open:"postgres://app:${DB_SENHA}@localhost:5432/loja?sslmode=require"
Db.open:"mysql://app:${DB_SENHA}@localhost:3306/loja?tls=true"
```

O endereço é `postgres://usuario:senha@servidor:porta/banco` (ou `postgresql://`) e `mysql://...`
(ou `mariadb://...`). A porta padrão é 5432 no PostgreSQL e 3306 no MySQL.

- **Senha fora do código.** `${NOME}` é trocado pelo valor da variável de ambiente `NOME` na hora de
  conectar. Se ela não existir, o erro diz qual. Dentro de um endereço, o valor é escapado, então uma
  senha com `@` ou `/` funciona. Se o texto inteiro é só `${NOME}`, a variável traz o endereço completo
  (`Db.open:"${DATABASE_URL}"`). Os erros nunca mostram a senha.
- **Criptografia.** O PostgreSQL exige TLS por padrão (`sslmode=require`); num servidor local sem TLS use
  `?sslmode=disable`. No MySQL, `?tls=true` liga o TLS (`skip-verify` aceita certificado sem validar).
  Para um servidor em outra máquina, ligue sempre.
- **Conexões.** Cada endereço tem um pool de até 10 conexões, reaproveitado entre as páginas. Uma
  transação usa uma conexão só até o `commit` ou o `rollback`. Os limites de 10 s por consulta e de 100
  mil linhas valem aqui também.
- **A mesma `Db.open:` por página.** Pode ficar numa função do `core/`. Uma página pode abrir um banco
  depois do outro, e vale o último aberto.

## O que muda entre os bancos

O SQL é o de cada banco, então tipos, funções e a criação de tabelas mudam. O djo só uniformiza:

- **Parâmetros.** Escreva sempre `?`. No PostgreSQL o djo troca por `$1`, `$2`... (sem mexer no que está
  dentro de textos `'...'`, nomes `"..."`, comentários e `$$...$$`). Para o operador `?` do `jsonb`,
  escreva `??`.
- **`last_id`.** O MySQL devolve o id gerado. O PostgreSQL não tem isso, e `last_id` vem `nil`: use
  `RETURNING`:

```
var r = Db.one:"INSERT INTO posts (titulo) VALUES (?) RETURNING id":[titulo]
print r["id"]
```

- **Tipos.** Inteiros viram `Int`, decimais de ponto flutuante `Float`, textos `String`, `NULL` vira `nil`
  e datas e horas viram `Int` (segundos desde 1970). `bool` do PostgreSQL vira `Bool` (no MySQL, `BOOLEAN`
  é um inteiro, então vem `1` ou `0`). `NUMERIC`, `UUID`, `JSON`, arrays e `BYTEA` chegam como `String`.
- **Transações** funcionam igual. No MySQL, comandos que mudam a estrutura (`CREATE`, `DROP`, `ALTER`)
  encerram a transação sozinhos.

## Parâmetros: sempre use `?`

Nunca monte o SQL juntando texto. Passe os valores numa lista, na ordem dos `?`:

```
Db.query:"SELECT * FROM posts WHERE autor = ? AND nota > ?":[autor, 3]
```

Isso protege contra SQL injection. Valores aceitos: `Int`, `Float`, `String`, `Bool` e `nil`
(vira `NULL`). No SQLite, `true` vira `1`.

## Tipos

| SQLite           | djo       |
|------------------|-----------|
| INTEGER          | `Int`     |
| REAL             | `Float`   |
| TEXT             | `String`  |
| NULL             | `nil`     |
| BLOB             | `String`  |

O SQLite não tem tipo booleano: `true` vira `1` e `false` vira `0`.

**Datas.** Um `Date` ou `DateTime` pode ir como parâmetro (`?`). No SQLite ele é gravado como **texto**:
`"2025-01-31"` para `Date` e `"2025-01-31 14:05:00"` (UTC, sem fuso) para `DateTime`. Esse texto ordena e
compara certo (`WHERE dia >= ? AND dia <= ?`, `ORDER BY inicio`), então a coluna pode ser `TEXT`. No
PostgreSQL e no MySQL o valor vai como `date`/`timestamp` em UTC. Na leitura o `Db` devolve o que a coluna
guarda (texto no SQLite): converta com `Date.parse:` e `DateTime.parse:`, que aceitam esse formato. Colunas
`Int` com segundos desde 1970 viram `DateTime` com `DateTime.from_unix:`. Veja
[Date e DateTime](06-referencia.md#date-e-datetime).

## Transações

```
Db.begin:
Db.exec:"UPDATE contas SET saldo = saldo - ? WHERE id = ?":[10, 1]
Db.exec:"UPDATE contas SET saldo = saldo + ? WHERE id = ?":[10, 2]
Db.commit:
```

Se a página terminar (ou der erro) sem `commit`, a transação é desfeita sozinha.

**Dentro de um `promise`.** Uma transação aberta com `Db.begin` **dentro** de um `promise` que falha é desfeita (`rollback`) antes de o `error` rodar:

```
uniq promise{
    Db.begin:
    Db.exec:"UPDATE contas SET saldo = saldo - ? WHERE id = ?":[10, 1]
    reject:"limite":"acima do limite"
    Db.commit:
}error{
    [err]
    print "nada mudou: " + err["msg"]
}
```

Uma transação aberta **antes** do `promise` não é tocada, e o promise **não pode usar o `Db`** enquanto ela estiver aberta (erro claro). No PostgreSQL, um erro de SQL numa transação aberta a deixa abortada ("current transaction is aborted"): quem captura esse erro com um promise **fora** da transação deve chamar `Db.rollback:` e abrir outra. (Esse caso de PostgreSQL não foi testado contra um servidor de verdade.) Veja [Erros e promises](01-basico.md#erros-e-promises).

## Paginação: `Db.page:`

Para listas grandes, não traga tudo: `Db.page:` devolve uma página e diz se há mais.

```
var r = Db.page:"SELECT id, titulo FROM posts WHERE autor = ? ORDER BY id DESC":[autor]
r["itens"]      // a lista de linhas desta página
r["pagina"]     // 1, 2, 3...
r["limite"]     // itens por página
r["proxima"]    // o número da próxima página, ou nil se esta é a última
r["anterior"]   // o número da página anterior, ou nil na primeira
```

Numa página do servidor, a página e o tamanho vêm da URL: `?pagina=2&limite=20`. O padrão é 20 por página e
o máximo é 100. Valor ausente, que não seja número ou fora da faixa **não dá erro**: volta para um valor
seguro (um `?limite=100000` vira 100). Uma página além do fim devolve a lista vazia.

Opções, no terceiro argumento: `Db.page:sql:[params]:{"limite": 10, "max": 50, "total": true}`.

| Opção       | O que faz |
|-------------|-----------|
| `"limite"`  | o tamanho padrão da página (de 1 a 1000) |
| `"max"`     | o maior tamanho que o cliente pode pedir (padrão 100; de 1 a 1000) |
| `"total"`   | `true` conta as linhas e acrescenta `r["total"]` e `r["paginas"]`; padrão `false` |
| `"pagina"`  | fixa a página (para scripts, sem URL) |

- O SQL **precisa** ter `ORDER BY`: sem uma ordem fixa, a mesma linha pode aparecer em duas páginas.
- **Não escreva** `LIMIT` nem `OFFSET`: o `Db.page:` os acrescenta (com números já validados). Só vale `SELECT` (ou `WITH`).
- Por padrão ele **não conta** a tabela. Busca uma linha a mais só para saber se existe a próxima página. Contar
  é caro em tabela grande e, a cada página, repete o trabalho; peça `"total": true` só quando a tela precisa
  mostrar "página 3 de 12".
- Página muito funda (`?pagina=50000`) fica lenta em qualquer banco, porque ele precisa pular as linhas
  anteriores. O número da página vai até 1.000.000. Para listas enormes, pagine pela chave:
  `WHERE id < ? ORDER BY id DESC` com o último `id` da página anterior, usando `Page.read:` para o tamanho.

Para uma API, `Page.headers:r` põe na resposta o `Link: <...>; rel="next"` / `rel="prev"` e o `X-Total-Count`
(se pediu o total), e o corpo continua sendo só a lista. Veja [Fazendo uma API](13-api.md).

## Exemplo com structs

```
struct Post{
    id[Int] nocall
    titulo[String]

    f salvar
        var r = Db.exec:"INSERT INTO posts (titulo) VALUES (?)":[self.titulo]
        self.id = r["last_id"]
}
```

## Limites e segurança

- O arquivo do banco (`.db`, `.sqlite`, e os `-wal`/`-shm`) **nunca é servido** pelo `djo serve`. O endereço de um
  banco de rede é só o que você escreve no código: nunca monte esse texto com dados de visitante.
- Uma consulta tem 10 segundos para terminar, e `Db.query` recusa mais de 100 mil linhas (use `LIMIT`).
- No SQLite há uma conexão por arquivo e um escritor por vez, o que é de sobra para sites e
  sistemas pequenos. Para muito acesso de escrita, ou vários servidores no mesmo banco, use o
  PostgreSQL ou o MySQL.
- Erros de SQL aparecem com `-dev`; sem ele, a página mostra só um erro genérico.

> **Não verificado:** parâmetros `Date`/`DateTime` em PostgreSQL e MySQL (enviados como `time.Time` UTC) ainda não foram testados contra servidores reais; no SQLite vão como texto.
