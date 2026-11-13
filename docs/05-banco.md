# Banco de dados

`Db` é um SQLite embutido na própria linguagem: o banco é um arquivo, sem servidor
para instalar. Funciona igual em qualquer sistema onde o `djo` roda.

```
Db.open:"site.db"
Db.exec:"CREATE TABLE IF NOT EXISTS posts (id INTEGER PRIMARY KEY, titulo TEXT)"
Db.exec:"INSERT INTO posts (titulo) VALUES (?)":[Http.form:"titulo"]
var posts = Db.query:"SELECT * FROM posts ORDER BY id DESC LIMIT 10"
```

## Funções

| Chamada                        | Devolve |
|--------------------------------|---------|
| `Db.open:"arquivo.db"`         | nada; abre (ou cria) o banco |
| `Db.exec:sql` ou `Db.exec:sql:[params]`  | mapa `{changes, last_id}` |
| `Db.query:sql:[params]`        | lista de mapas, um por linha |
| `Db.one:sql:[params]`          | o primeiro mapa, ou `nil` se não houver linha |
| `Db.begin:` `Db.commit:` `Db.rollback:` | transação |

- Chame `Db.open:` no começo de cada página (é barato: a conexão é reaproveitada).
  O arquivo precisa terminar em `.db` ou `.sqlite` e ficar dentro da pasta do site.
  Um `Db.open:` pode ser feito numa função do `core/`.
- As colunas viram chaves do mapa: `post["titulo"]`.

## Parâmetros: sempre use `?`

Nunca monte o SQL juntando texto. Passe os valores numa lista, na ordem dos `?`:

```
Db.query:"SELECT * FROM posts WHERE autor = ? AND nota > ?":[autor, 3]
```

Isso protege contra SQL injection. Valores aceitos: `Int`, `Float`, `String`, `Bool` e `nil`
(vira `NULL`).

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

- O arquivo do banco (`.db`, `.sqlite`, e os `-wal`/`-shm`) **nunca é servido** pelo `djo serve`.
- Uma consulta tem 10 segundos para terminar, e `Db.query` recusa mais de 100 mil linhas (use `LIMIT`).
- Há uma conexão por arquivo e o SQLite aceita um escritor por vez, o que é de sobra para
  sites e sistemas pequenos. Para muito acesso de escrita, ele não é o caminho.
- Erros de SQL aparecem com `-dev`; sem ele, a página mostra só um erro genérico.
