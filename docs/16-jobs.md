# Tarefas em segundo plano e agenda: `Job` e `Schedule`

Algumas coisas não devem fazer a pessoa esperar: mandar um e-mail, gerar um relatório, limpar sessões
velhas. O `Job.run` guarda um pedido numa **fila persistente** e responde na hora; uma função sua roda
**depois da resposta**. E o `agenda.djo` faz o djo disparar uma função sozinho, de tempos em tempos.

> **Ponto principal: cada job roda *pelo menos uma vez*, e pode rodar mais de uma.**
> Se o processo cair no meio de um job, ou o servidor não receber o resultado a tempo, a tentativa se repete.
> Escreva a função para ser **idempotente** (rodar duas vezes dá o mesmo resultado que uma) ou use a opção
> `key`. Não existe "exatamente uma vez" sem uma transação com o seu banco.

## Um job

```
// core/emails.djo
f enviar_boas_vindas
    params[args[Map]]
    var u = Db.one:"SELECT nome, email FROM users WHERE id = ?":[args["usuario"]]
    Mail.send:{"to": u["email"], "subject": "Bem-vinda!", "text": "Olá, " + u["nome"]}
```

```
// cadastro.djo (a página)
...
var id = Job.run:"enviar_boas_vindas":{"usuario": 7}
print "cadastrado"
```

A página responde logo. Um worker pega o job e chama `enviar_boas_vindas` com o mapa `{"usuario": 7}`.

- **A função** é uma `f` comum do `core/` (não uma `action`), com **um parâmetro** (o mapa dos argumentos) ou
  **nenhum**. O valor que ela devolve é ignorado.
- **Os argumentos** são só dados: texto, número, `Bool`, `nil`, lista, mapa (e `Date`/`DateTime`, que viram
  texto ISO). Uma struct ou função dá erro. No máximo 16 KB em JSON.
- O nome da função é conferido no `Job.run`: se não existe ou tem a assinatura errada, a página dá erro com a linha.

## Opções

```
Job.run:"relatorio":{"mes": 3}:{"in": "10m", "tries": 5, "timeout": "5m", "key": "relatorio-3"}
```

| Opção | O que faz |
|---|---|
| `in` | atraso: `"30s"`, `"10m"`, `"2h"`, `"1d"` (até 365 dias) |
| `at` | instante UTC: `"2026-12-01T09:00:00Z"` (ou um `DateTime`), até 365 dias à frente. Não use junto com `in` |
| `tries` | total de tentativas, de 1 a 10 (padrão 3) |
| `timeout` | limite de cada tentativa, de `"1s"` a `"15m"` (padrão `"60s"`) |
| `key` | chave de idempotência: se já existe um job com essa chave **na fila ou rodando**, o `Job.run` devolve o id dele e não cria outro |

Qualquer outra chave é erro. O `Job.run` devolve o **id** (Int) do job, ou `nil` se a fila está cheia
(10.000 pendentes) ou passou do limite de 300 enfileiramentos por minuto por processo (o aviso vai para o log).

## Consultar

```
var j = Job.status:id     // mapa ou nil
j["state"]                // "queued", "running", "done", "dead" ou "canceled"
Job.cancel:id             // true se ainda estava na fila
```

`Job.status` devolve `nil` quando o job não existe, e também quando ele foi **descartado**: um job retido
(veja abaixo) some da fila se a página ou a `action` que o enfileirou der erro. Um job retido aparece como
`"queued"`, e dá para cancelá-lo com `Job.cancel`.

O mapa do `Job.status` tem `id`, `fn`, `state`, `tries`, `max_tries`, `run_at`, `created_at`,
`finished_at` (`DateTime`; `nil` se não terminou) e `error` (o último erro, ou `nil`).

## Quando o job entra na fila

O `Job.run` **retém** o job até a página (ou a `action`) **terminar sem erro**. Se a página der erro depois
do `Job.run`, o job é descartado: ninguém recebe o e-mail de boas-vindas de um cadastro que falhou. O mesmo
vale para um `Job.run` feito **dentro de um job**: o filho só entra na fila quando o pai termina bem.

Enquanto espera, o job fica gravado na fila no estado interno `held` (retido). Ele já ocupa uma vaga dos
10.000 pendentes e já vale para a `key`, mas nenhum worker o pega; ao fim da página ou da `action` ele passa
para `queued`, ou é apagado se deu erro. O `djo jobs` mostra a contagem de `held`; o `Job.status` não
distingue: mostra `"queued"`.

## Como o job roda

- Num contexto novo, como uma requisição servida, mas **sem requisição**: `Http.bearer`, `Session` e `Route` não
  existem ali. `Db`, `File`, `Env`, `Mail` e `Http.get/post` funcionam, com as mesmas regras de sempre
  (`Http`: até 20 chamadas por execução do job, ou o que o `-jobs-http` disser, veja [Limite de chamadas Http](#limite-de-chamadas-http); `File`: 10 MB gravados; `Time.sleep` espera no máximo o `timeout` do job, somando todas as chamadas).
- Tem **limite de tempo** (`timeout`) e de passos. Estourou, a tentativa falha. O tempo limite interrompe um job
  que está **trabalhando** (um laço, por exemplo) com "o job passou do tempo limite (timeout)". Já um
  `Time.sleep` que passaria do `timeout` dá o erro de `Time.sleep` da tabela de erros, abaixo.
- Numa página ou `action` servida, o `Time.sleep` espera no máximo **2 segundos no total**. Trabalho mais longo
  que isso é o motivo de existir o `Job.run`.
- O `print` do job vai para o **log** do servidor (até 4 KB por tentativa).
- Em `djo serve -dev` o job roda normalmente, e o `Mail.send` dentro dele grava o `.eml` em `_saida/emails/`.
- `Job.give_up:"motivo"` (só dentro de um job) desiste **sem** repetir: o job vai direto para `dead`.

## Falhas e tentativas

Um erro de execução, o `timeout` ou uma queda do processo contam como **tentativa falha**. Se sobram tentativas,
o job volta para a fila com *backoff*: 15 s, 1 min, 4 min, 16 min, no máximo 1 hora (com até 10% de variação).
Gastas as tentativas, o job vai para `dead` com o erro (e a linha) guardado por **30 dias**. Jobs `done` ficam 24 h.

Se o processo cai no meio de um job, ele fica `running` com um *lease* (o `timeout` + 30 s). Passado o lease, a
fila o devolve (ao subir e a cada 30 s), contando como tentativa.

### Erros dentro do job: `promise`

Um erro que **sobe** do job (sem `error`) conta como tentativa falha e o job repete. Para decidir, trate o erro num `promise` e use `Job.give_up` ou `reject:`:

```
f sincronizar
    params[a[Map]]
    uniq promise{
        enviar_para_erp:a["id"]
    }error{
        [err]
        if
            cond[err["code"] == "erp_recusou"]
            Job.give_up:"o ERP recusou o pedido"     // vai para dead, sem repetir
        reject:err                                    // os outros erros: o job tenta de novo
    }finally{
        print "fim"                                   // roda antes de o give_up passar
    }
```

Para forçar um erro num teste, use `reject:"teste":"falhou de propósito"` (no lugar de uma conta como `1 / 0`). Um `Job.run` feito dentro de um `promise` que falha é descartado; o chamado dentro de um que dá certo segue a regra de sempre (fica retido até a página ou o job terminar sem erro). O limite de chamadas `Http` (20 por padrão) vale para a execução inteira do job, dentro ou fora de promises. Veja [Erros e promises](01-basico.md#erros-e-promises).

## Trava (`lock`) em jobs

Um job que usa [`lock`](04-web.md#trava-por-chave-lock) e não consegue a vez em `wait` falha com `lock_busy`. Se o erro **sai sem captura**, o job é **reagendado** (backoff de 5 s a 60 s) **sem gastar a tentativa**, no máximo 20 vezes; depois disso vale a falha normal. Enquanto espera, `Job.status:id` mostra `state: "waiting_lock"`, `lock_key` (a chave) e `lock_reschedules` (quantos reagendamentos já teve).

> **Atenção, idempotência:** o job **reexecuta do começo** a cada reagendamento. O que ele fez **antes** do `lock` (gravar no banco, mandar e-mail, enfileirar outro job) **se repete**. Por isso, ponha o `lock` no **início** da função do job, antes de qualquer efeito.

## Onde fica a fila

Num SQLite local: `_dados/jobs.db`, dentro da pasta do site. A pasta `_dados/` nunca é servida e, num app
do `djo build`, é a pasta de dados (`-data` / `DJO_DATA`): **a fila sobrevive às atualizações do app**. É sempre
SQLite local, mesmo que o seu site use PostgreSQL ou MySQL. O arquivo só é criado quando o projeto usa jobs.

## Workers

Por padrão, 2 workers rodam dentro do `djo serve` (e dos apps). `djo serve -jobs 4` muda (0 a 16);
`-jobs 0` só enfileira: nem worker, nem agenda, nem varredura (útil para uma cópia que só atende páginas). Sem uso (`Job.run`, `agenda.djo` ou um
`jobs.db` existente), **não há worker, nem banco, nem custo nenhum**.

Ao desligar (Ctrl+C ou SIGTERM) o servidor para de pegar jobs e espera os que estão rodando por até 30 s.
O que passar disso volta para a fila pelo lease no próximo start.

## Fora do servidor

Num script (`djo arquivo.djo`) ou no REPL, o `Job.run` dá o erro "Job só existe dentro de djo serve (ou de um
app do djo build); fora dele chame a função diretamente". Ele **não** executa na hora, para o comportamento não
depender de onde o código roda.

## A agenda: `agenda.djo`

Funções que o djo chama sozinho, no estilo do `ask.djo` e do `rotas.djo`:

```
Schedule.every:"10m":"limpar_sessoes"
Schedule.cron:"20 3 * * *":"backup"
Schedule.cron:"0 9 * * 1":"resumo_semanal":{"tries": 5, "timeout": "5m", "args": {"escopo": "todos"}}
```

- **`every`**: um intervalo, de `"30s"` a `"7d"`. O primeiro disparo é *subida + intervalo*, e não na hora.
- **`cron`**: 5 campos (minuto hora dia-do-mês mês dia-da-semana), **sempre em UTC**. Vale `*`, listas
  (`1,15`), faixas (`10-12`), passos (`*/15`, `5/20`). O dia da semana vai de 0 a 7 (0 e 7 são domingo). Com
  dia-do-mês **e** dia-da-semana preenchidos, dispara se **qualquer um** bater (como no cron clássico).
  Sem nomes (`mon`, `jan`) e sem segundos.
- **Opções** (3º argumento): `tries`, `timeout` (como no `Job.run`), `args` (o mapa que a função recebe) e
  `catch_up` (padrão `true`, veja abaixo).
- Cada disparo vira um **job** (mesmas tentativas, log, limites, `djo jobs`). A função segue as regras do job.
- **Sem sobreposição:** se o disparo anterior da mesma tarefa ainda está na fila ou rodando, o novo é pulado e
  logado.
- **Disparos perdidos:** se o servidor ficou parado e perdeu um ou mais horários, com `catch_up: true` ele roda
  **uma vez** ao subir (não uma por horário perdido); com `catch_up: false`, pula e espera o próximo.
- Uma tarefa é identificada por tipo + expressão + função: a **mesma função com a mesma expressão** não pode
  aparecer duas vezes (mesmo com `args` diferentes); use funções diferentes (ou outra expressão). O estado de uma
  tarefa que você removeu do arquivo fica na tabela `schedules` do `jobs.db` (alguns bytes), sem efeito.
- O arquivo é conferido ao iniciar o `djo serve`, no `djo build` e no `djo spec`: erro de sintaxe, função
  inexistente, cron inválido ou a mesma tarefa declarada duas vezes acusam a linha. Em `-dev` (e em produção),
  quando o arquivo muda, ele é relido; se a versão nova tiver erro, a anterior continua valendo e o erro vai
  para o log.
- O `agenda.djo` **nunca é servido** (mas vai no pacote do `djo build`).

> **Uma instância só.** A agenda e a fila foram feitas para um processo por pasta de dados. Dois processos no
> mesmo arquivo `jobs.db` não duplicam disparos nem jobs (a fila usa lease e a agenda usa uma chave por
> horário), mas **não há garantia entre máquinas diferentes**.

## Ver e consertar a fila: `djo jobs`

```
djo jobs [pasta]                         resumo: quantos em cada estado
djo jobs list [-state dead] [-n 20] [--json] [pasta]
djo jobs show ID [pasta]                 tudo sobre um job (argumentos, erro...)
djo jobs retry ID [pasta]                um job dead volta para a fila
djo jobs retry --all-dead [pasta]
djo jobs cancel ID [pasta]
djo jobs purge [-older 7d] [pasta]       apaga os já terminados (done, canceled, dead)
```

Funciona com o servidor ligado. Num app do `djo build`: `./blog jobs list` (usa a pasta de dados do app).

## Testando: `Spec.jobs`, `Spec.run_jobs`, `Spec.agenda`

No `djo spec` o `Job.run` **nunca roda nada sozinho**: ele captura, e o teste decide quando executar.

```
f it_cadastro_manda_boas_vindas
    Spec.post:"/cadastro":{"nome": "Ana"}
    var jobs = Spec.jobs:
    expect.len:jobs:1
    expect.eq:jobs[0]["fn"]:"enviar_boas_vindas"
    Spec.run_jobs:
    expect.len:(Spec.emails:):1
```

| Chamada | Faz |
|---|---|
| `Spec.jobs:` | os jobs enfileirados no teste atual (`id`, `fn`, `args`, `state`, `max_tries`, `timeout`, `key`, `in`, `at`, `error`) |
| `Spec.run_jobs:` | executa **em ordem e de forma síncrona** todos os enfileirados, inclusive os que eles criarem (até 100 rodadas). Devolve `[{id, fn, ok, error}]` |
| `Spec.run_job:id` | executa só esse job |
| `Spec.agenda:` | a agenda do `agenda.djo` (`kind`, `expr`, `fn`, `tries`, `timeout`, `catch_up`, `args`) |

No `Spec.run_jobs` o atraso e as retentativas **não** são simulados: se o job falha, o resultado traz `ok: false`
e o erro. Ele acontece uma vez só.

## Perfis e permissões

- `Job` precisa de banco e de servidor: **não existe** nos builds `nodb` e `noweb` (nem no nano). Chamar dá o erro
  "Job não existe neste perfil".
- No build `nonet`, `Job` existe, mas `Http` e `Mail` dentro do job dão o erro de perfil.
- Um booster que chama `Job.run`, `Job.status` ou `Job.cancel` precisa declarar a permissão `"Job"` no
  `booster.json`. O `Schedule` só existe dentro do `agenda.djo`.

## Limite de chamadas Http

Um job pode fazer até **20 chamadas** `Http.get`/`Http.post` por execução (cada tentativa conta de novo). Quem administra o servidor muda esse número com a opção `-jobs-http` (ou a variável `DJO_JOBS_HTTP`), de **1 a 200**:

```
djo serve -jobs-http 100 meusite
DJO_JOBS_HTTP=100 djo serve meusite
```

- O limite é **do servidor**, não do job: um job não pode pedir mais chamadas do que o servidor deixa (um limite que o próprio job pudesse subir não protegeria nada). Se as duas formas aparecerem, vale o `-jobs-http`.
- Vale só para **jobs** (e para o que roda dentro deles, como um `promise`). A **página** e a **action** servidas continuam com o limite delas, de **5 chamadas**, que o `-jobs-http` não muda.
- Um valor fora de 1 a 200, ou que não seja número, faz o `djo serve` recusar a subida com uma mensagem (código de saída 2).
- Passar do limite é um erro que **não** se captura (nem com `promise`, e o `finally` não roda): `Http.get: no máximo 20 chamadas externas por job (quem administra o servidor muda isso com djo serve -jobs-http N, até 200)`. O job falha e repete como qualquer erro.
- Cada chamada ainda tem o tempo máximo dela e o `timeout` do job corta o conjunto; 200 chamadas lentas não cabem em 60 s. Para sincronizar um ERP com muitas páginas, suba o `timeout` do job (até 15 min) junto com o `-jobs-http`, ou divida o trabalho em vários jobs (cada um com a sua cota), por exemplo um job por página.
- No `djo spec` o `Http` não tem esse limite.

**Timeout de cada chamada.** `Http.get:url:nil:30` (ou `"30s"`) escolhe o prazo da chamada: de 1 a 60 s, e nunca mais do que resta do `timeout` do job (o padrão sem o argumento é 8 s). Se o prazo do job acaba no meio da chamada, o job é interrompido como em qualquer estouro de `timeout`. Veja [Http](04-web.md#chamando-outros-serviços-httpget-e-httppost).

## Limites

| | |
|---|---|
| argumentos do job | 16 KB em JSON |
| jobs pendentes (`queued` + retidos) | 10.000 |
| enfileiramentos por minuto, por processo | 300 (a agenda não conta) |
| `Job.run` por página, action ou job | 100 |
| `tries` | 1 a 10 (padrão 3) |
| `timeout` | 1 s a 15 min (padrão 60 s) |
| chamadas `Http` por execução de job | 1 a 200 (padrão 20), pelo `-jobs-http` |
| workers | 0 a 16 (padrão 2) |
| agendamentos no `agenda.djo` | 200 |
| retenção | `done` 24 h, `dead` 30 dias |

## Quando algo dá errado

| Mensagem | O que fazer |
|---|---|
| `Job só existe dentro de djo serve...` | você chamou fora do servidor; chame a função direto |
| `Job.run: a função 'x' não existe no core/ do projeto` | o nome está errado, ou a função não está no `core/` |
| `Job.run: 'x' precisa ter zero ou um parâmetro` | a função aceita só o mapa de argumentos |
| `o job passou do tempo limite (timeout)` | aumente o `timeout` ou divida o trabalho em jobs menores |
| `Time.sleep: páginas servidas podem esperar no máximo 2 segundos no total` | você dormiu mais de 2 s numa página ou `action`; passe o trabalho para um `Job.run` |
| `Time.sleep: o job pode esperar no máximo N segundos no total (o timeout)` | a soma dos `Time.sleep` do job passou do `timeout`; aumente o `timeout` ou espere menos |
| `o job passou do limite de passos e foi interrompido` | o job tem um laço muito longo; divida o trabalho em jobs menores |
| `o processo parou durante a tentativa (o lease venceu)` | o servidor caiu ou foi desligado no meio; o job foi reenfileirado |
| `agenda.djo: ... a agenda anterior continua valendo` | o arquivo novo tem erro; corrija (o erro vai para o log) |
