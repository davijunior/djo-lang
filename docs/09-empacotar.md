# Empacotar: `djo build`

`djo build` transforma o projeto num **único executável**. Copie esse arquivo para o servidor, ou para a
placa, e rode. Não precisa instalar o `djo`, nem copiar a pasta do site.

```
djo build                     # empacota a pasta atual → dist/<nome-da-pasta>
djo build meusite             # empacota outra pasta
djo build -o app meusite      # escolhe o nome do arquivo
```

```
$ djo build blog
app gerado: dist/blog (11.5 MB, 6 arquivos, modo serve)
rode com: ./blog -addr :8080
```

## Como funciona

O executável é o próprio `djo` com o seu site (compactado) colado no fim. Ao iniciar, ele vê que tem um
site dentro e vira o seu app.

No primeiro uso, o site é desempacotado numa pasta ao lado do executável, `blog.d/`, e roda de lá. Por isso
o banco (`Db`), os uploads e os arquivos gravados (`File`) ficam em pastas comuns, que você vê, copia e
guarda em backup.

- **Atualizar:** gere um app novo e troque o executável. Ao iniciar, ele nota a versão nova, substitui os
  arquivos do site e **mantém os dados** (tudo o que o app criou: `.db`, uploads, logs).
- **Outra pasta de dados:** `./blog -data /var/lib/blog` (ou a variável `DJO_DATA`).
- **Versão:** `./blog version`.

## Modos

| Se o projeto tem...   | O app...                    | Rode com |
|-----------------------|-----------------------------|----------|
| `main.djo`            | executa o `main.djo` (programa, ideal para IoT) | `./app` |
| só páginas            | serve o site (como `djo serve`) | `./app -addr :8080` |

No modo servidor valem as mesmas opções do `djo serve` (`-addr`, `-dev`, `-allow-private`). Também vale a
variável `PORT`: `PORT=8080 ./blog` equivale a `-addr :8080`, o que facilita hospedar em serviços que a definem.

## O que entra no app

Tudo da pasta, **menos**:

- arquivos e pastas que começam com `.` (`.git`, `.env`...);
- bancos de dados (`.db`, `.sqlite`...): são dados, não código;
- a pasta `dist/` e `node_modules/`;
- o que estiver em `.djoignore`, um padrão por linha (`#` para comentário):

```
# .djoignore
rascunhos
*.tmp
```

## Verificação

Antes de empacotar, o `djo build` lê **todo** o código: arquivos `.djo`, templates `.djo.html` e a regra do
`core/` (que só declara) e os `boosters/` (que também vão para dentro do executável). Com erro de sintaxe, nada é gerado e o erro aparece com o arquivo e a linha:

```
djo build: 1 erro(s) no código, nada foi gerado:
  entrar.djo.html: erro (linha 12): expressão inválida: encontrei '}}'
```

Um erro que só aparece ao executar (variável que não existe, por exemplo) não é pego.

## Para outra máquina (IoT, servidor ARM...)

O app usa como base o `djo` que está rodando o build. Para gerar para **outra arquitetura**, aponte um `djo`
daquele sistema com `-runtime`, por exemplo um dos que o `./build.sh` gera:

```
./build.sh iot                              # gera os binários em dist/
djo build -runtime dist/djo-tiny-linux-arm64 -o sensor-pi sensor/
```

O `sensor-pi` tem uns 3 a 7 MB (o runtime mais o seu site) e roda direto na placa. Não é preciso ter o Go
instalado para isso, só os binários do `djo`.

Escolha o perfil do runtime pelo que o projeto usa (veja [Builds e IoT](07-builds.md)): um site precisa de
um runtime com servidor (`full` ou `lite`); um programa de sensor roda no `tiny` ou no `nano`.

**Dica de tamanho:** o `djo` que você instalou com `go build` simples não tem os símbolos removidos. Use o
`./build.sh` (que usa `-ldflags "-s -w"`) para o runtime ficar uns 35% menor.

## Observações

- No macOS, o executável gerado não é assinado; o Gatekeeper pode pedir autorização na primeira execução.
- O site é lido do disco (`blog.d/`), não de dentro do executável. Isso é o que permite as páginas gravarem
  arquivos e abrirem o banco ao lado delas.
- Quem tem acesso ao executável tem acesso ao código-fonte do site (ele só está compactado). Não coloque
  segredos no projeto; use variáveis de ambiente ou arquivos criados no servidor.
