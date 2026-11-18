# djo

Uma linguagem pequena para coisas simples da internet: sites, CMS e sistemas pequenos.
Um único binário, sem dependências, que roda em qualquer lugar onde o Go compila
(servidores, Raspberry Pi e outros dispositivos IoT).

Cada rota pode ter duas faces, uma para pessoas (HTML) e uma para agentes (JSON, com manifesto e `action`s
tipadas), na mesma URL. O site da linguagem é feito em djo e serve as duas:
<https://djolang.zfxsoftware.cloud>. Para ver como um agente o enxerga:
`curl -H 'Accept: application/json' https://djolang.zfxsoftware.cloud/`

> Projeto jovem e experimental: a linguagem e o formato do manifesto podem mudar, e os binários não são assinados.

```
struct Post{
    id[Int] nocall
    titulo[String]
    publicado[Bool] = false

    f link
        return "/post?id=" + (str:self.id)
}

var p = Post:"Olá"
print p.link:
```

## Instalação

**Para usar a djo não precisa de Go.** Baixe o arquivo do seu sistema na página do release (ou use o
instalador) e pronto: é um executável só.

```
curl -fsSL https://djolang.zfxsoftware.cloud/releases/v0.5.0/install.sh -o install.sh
DJO_BASE_URL=https://djolang.zfxsoftware.cloud/releases/v0.5.0 DJO_VERSION=v0.5.0 sh install.sh
```

O instalador escolhe o arquivo pelo seu sistema e processador, **confere o SHA256** antes de instalar e põe o
`djo` em `~/.local/bin` (mude com `DJO_DIR`). Em placas e roteadores, escolha o perfil menor:
`DJO_PROFILE=tiny` ou `nano`. No Windows, baixe o `.zip` e extraia o `djo.exe`. Veja [Releases](docs/11-releases.md).

**Compilar do código-fonte** precisa do Go 1.26 ou mais novo (a go.mod pede essa versão por causa do SQLite):

```
go build -o ~/.local/bin/djo .
```

Sem CGO, então a compilação cruzada é direta. O `./build.sh` gera versões menores (a `nano` tem 3 MB, sem servidor, SQLite nem rede) para Raspberry Pi, roteadores e placas RISC-V; veja [Builds e IoT](docs/07-builds.md).

## Uso

```
djo arquivo.djo              # roda um programa
djo pagina.djo.html          # roda um template e imprime o HTML
djo                          # REPL
djo new [-modelo site|script|iot] pasta   # cria um projeto pronto para rodar
djo serve [-addr host:porta] [-dev] [pasta]
djo build [-o app] [-runtime djo-de-outra-maquina] [pasta]   # um executável só com o site
djo agent [-raw] [-post '{...}'] endereço   # vê o site como um agente (manifesto e rotas)
djo booster search|add nome|list|create nome|pack|remove nome|verify   # código pronto que o projeto chama pelo nome
```

`djo new meusite` cria um site com tela, face de agente, manifesto e uma `action`, e já diz os próximos passos. Os modelos `script` (um arquivo) e `iot` (leitura de sensor na serial) também existem. Ele recusa uma pasta que já tem arquivos.

Um **booster** é código pronto em djo que o projeto chama pelo nome: `djo booster add markdown` (baixa do registro, confere o SHA256) e depois `markdown.render:texto`. Ele só enxerga o próprio código (nunca o do projeto), declara as permissões de que precisa e fica travado por SHA256 no `boosters.lock`. `djo booster create nome` gera a estrutura de um novo. Veja [Boosters](docs/12-boosters.md).

`djo serve` usa `localhost:8080` por padrão. Com `-dev`, os erros aparecem na página. Com `-allow-private`, as páginas podem chamar endereços da rede local com `Http.get`. `-trusted-proxy` diz em quais proxies reversos confiar para o `Secure` dos cookies e para o IP real do visitante em `Http.ip` (veja [Web](docs/04-web.md#produção-atrás-de-um-proxy)). `-agent-conc` e `-agent-rate` limitam o tráfego de agentes para ele não atrapalhar as pessoas (veja [Agentes](docs/10-agentes.md)).

## Documentação

1. [O básico](docs/01-basico.md): variáveis, tipos, funções, condicionais e laços
2. [Structs](docs/02-structs.md): campos, valores padrão, métodos, composição e herança
3. [Mapas e Json](docs/03-mapas-json.md)
4. [Web](docs/04-web.md): servidor, templates, rotas (por arquivo e `rotas.djo`), `core/`, `Http`, sessões, CSRF, senhas e upload
5. [Banco de dados](docs/05-banco.md): `Db` (SQLite, PostgreSQL e MySQL)
6. [Referência](docs/06-referencia.md): todas as funções, `Time` e mensagens de erro
7. [Builds e IoT](docs/07-builds.md): perfis `full`, `lite`, `noweb`, `tiny` e `nano` (3 MB)
8. [Hardware](docs/08-iot.md): porta serial (`Serial`), `Time.sleep`
9. [Empacotar](docs/09-empacotar.md): `djo build` gera um executável único com o seu site
10. [Agentes](docs/10-agentes.md): `action`, rotas para agentes e o manifesto
11. [Releases](docs/11-releases.md): instalar sem Go e publicar uma versão
12. [Boosters](docs/12-boosters.md): código pronto que o projeto chama pelo nome (os oficiais são o `markdown` e o `expect`)
13. [Fazendo uma API](docs/13-api.md): leituras, `action`s com `PUT`/`PATCH`/`DELETE`, token (`Http.bearer:`) e CORS
14. [Testes](docs/14-testes.md): `djo spec`, funções `it_...` e o booster `expect`, com rotas chamadas em processo
15. [E-mail](docs/15-email.md): `Mail.send` por SMTP, com TLS obrigatório
16. [Jobs e agenda](docs/16-jobs.md): `Job.run` (tarefas depois da resposta) e `agenda.djo` (`Schedule.every`, `Schedule.cron`)
17. [O booster `markdown`](docs/17-booster-markdown.md): `markdown.render` e `markdown.codigo`, o que converte e o que não, e a segurança da saída
18. [O booster `expect`](docs/18-booster-expect.md): os matchers do `djo spec`, as mensagens de falha e o idioma

Exemplos prontos em `exemplos/` (`exemplos/site/` é um site simples; `exemplos/blog/` tem login, banco e upload; `exemplos/api/` é uma API de tarefas com token e CORS, e tem `spec/` com os testes dela).

## Editor

`editor/sublime3/` tem a sintaxe para o Sublime Text 3. Copie os dois arquivos para
`~/.config/sublime-text/Packages/User/djo/`.

## Testes

```
go vet ./... && go test ./...
```

## Licença

[Apache License 2.0](LICENSE): use, modifique e distribua à vontade, inclusive em projetos comerciais, mantendo os avisos (`LICENSE`, `NOTICE`; as licenças das bibliotecas que o binário leva estão em `THIRD_PARTY_LICENSES`). As versões até a v0.6.0 foram publicadas sob a licença MIT.
