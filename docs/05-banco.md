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

O SQLite não tem tipo booleano: `true` vira `1` e `false` vira `0`. Guarde datas como `Int`
(`Time.now:`).

## Transações

```
Db.begin:
Db.exec:"UPDATE contas SET saldo = saldo - ? WHERE id = ?":[10, 1]
Db.exec:"UPDATE contas SET saldo = saldo + ? WHERE id = ?":[10, 2]
Db.commit:
```

Se a página terminar (ou der erro) sem `commit`, a transação é desfeita sozinha.

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
