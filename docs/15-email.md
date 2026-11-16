# E-mail: `Mail.send`

A djo envia e-mail por SMTP sem nenhuma dependência de fora. É uma chamada só:

```
var r = Mail.send:{
    "to": "ana@exemplo.com",
    "subject": "Bem-vinda!",
    "text": "Olá, Ana. Sua conta foi criada.",
    "html": "<p>Olá, <b>Ana</b>. Sua conta foi criada.</p>"
}
if r["ok"]
    print "enviado"
else
    print "não enviei: " + r["error"]
```

## O mapa

| Chave | O que é |
|---|---|
| `to` | **Obrigatório.** Um endereço (`"ana@x.com"` ou `"Ana <ana@x.com>"`) ou uma lista deles |
| `subject` | **Obrigatório.** De 1 a 200 bytes, numa linha só |
| `text` | **Obrigatório.** O corpo em texto puro, mesmo quando há `html` (quem lê sem HTML precisa de algo) |
| `html` | O mesmo corpo em HTML. Com ele o e-mail sai `multipart/alternative` |
| `cc`, `bcc` | Um endereço ou uma lista. O `bcc` nunca aparece nos cabeçalhos |
| `reply_to` | Um endereço para as respostas |
| `from` | Só troca o **nome exibido** (`"Equipe do Site"`). O endereço é sempre o de `DJO_SMTP_FROM`; um `from` com outro endereço é erro |

Qualquer outra chave é erro (um typo não passa em branco). Não há anexos nesta versão.

O resultado é um mapa: `ok`, `id` (o Message-ID), `sent` (`true` se saiu de verdade) e, em caso de falha, `error`.

- **Uso errado** (endereço inválido, falta `text`, mais de 20 destinatários, quebra de linha em `subject`, falta de configuração) é um **erro** da página, com a linha, como nas outras funções da djo.
- **Falha de rede ou do servidor** (não conectou, senha recusada, destinatário recusado, tempo esgotado) **não derruba a página**: volta `{"ok": false, "error": "smtp: ..."}` e o site decide o que fazer. A mensagem nunca traz a senha.

## Configuração: só pelo ambiente

A configuração vem das variáveis de ambiente do processo, lidas pelo próprio djo. **Ela nunca vira um valor da linguagem**: nenhuma página, log, erro ou booster consegue ler a senha.

| Variável | |
|---|---|
| `DJO_SMTP_HOST` | O servidor SMTP (obrigatório para enviar de verdade) |
| `DJO_SMTP_PORT` | Padrão `587` |
| `DJO_SMTP_USER`, `DJO_SMTP_PASS` | Login (AUTH PLAIN ou LOGIN), se o servidor pede |
| `DJO_SMTP_FROM` | **Obrigatório.** O remetente: `site@exemplo.com` ou `Meu Site <site@exemplo.com>` |
| `DJO_SMTP_SECURE` | `tls`, `starttls` ou `none`. Vazio = automático |

```
DJO_SMTP_HOST=smtp.exemplo.com DJO_SMTP_USER=site DJO_SMTP_PASS=... DJO_SMTP_FROM=site@exemplo.com djo serve .
```

## Criptografia: sempre

- Porta `465` (ou `DJO_SMTP_SECURE=tls`): TLS desde o começo.
- Qualquer outra porta: **STARTTLS obrigatório**. Se o servidor não oferece, o envio é cancelado; a djo nunca cai para texto puro.
- O certificado do servidor é sempre verificado. Não existe opção para desligar isso.
- A senha só sai depois do TLS.
- `DJO_SMTP_SECURE=none` (sem criptografia) só vale quando o host é `localhost`, `127.0.0.1` ou `::1`, para servidores de teste como o MailHog. Em qualquer outro host é erro.

## Segurança do conteúdo

- **Injeção de cabeçalho:** `to`, `cc`, `bcc`, `reply_to`, `from` e `subject` rejeitam quebra de linha e qualquer caractere de controle. Os endereços são validados e reescritos pela biblioteca (só ASCII no endereço; o nome pode ter acento).
- **Limites:** 20 destinatários por envio (`to`+`cc`+`bcc`), 200 bytes de assunto, 1 MB de `text`+`html`, 30 envios por minuto por processo e 4 ao mesmo tempo. Passou do limite por minuto: volta `ok: false`.
- **Tempo:** 10 segundos para conectar e 30 no total.
- **MIME:** UTF-8, assunto em RFC 2047, corpo em quoted-printable, `Message-ID`, `Date` e `MIME-Version`.
- O log só diz "e-mail enviado, id X, N destinatários": sem endereços, sem corpo.

## Em desenvolvimento: `djo serve -dev` não envia

Com `-dev`, `Mail.send` **não envia**: grava cada e-mail como `.eml` em `_saida/emails/` dentro do projeto e avisa no terminal. Dá para abrir o arquivo no programa de e-mail. A pasta `_saida` nunca é servida e não entra no `djo build`. O resultado vem com `sent: false` e `file`. Nesse modo nem é preciso configurar o SMTP.

```
djo serve -dev .              # grava em _saida/emails/
djo serve -dev -mail-send .   # envia de verdade (precisa do ambiente)
```

Fora do `-dev` (e em `djo arquivo.djo`) o envio é sempre de verdade.

## Nos testes: `Spec.emails`

Dentro de `djo spec` o `Mail.send` **nunca envia**, mesmo com o ambiente configurado. Cada e-mail fica numa lista que o spec consulta com `Spec.emails:` (zerada a cada `it_`), inclusive os disparados por `Spec.post` e companhia:

```
f it_cadastro_manda_boas_vindas
    Spec.post:"/cadastro":{"email": "ana@x.com"}
    var es = Spec.emails:
    expect.len:es:1
    expect.eq:es[0]["to"]:["ana@x.com"]
    expect.include:es[0]["text"]:"Bem-vinda"
```

Cada item tem `to`, `cc`, `bcc` (listas), `subject`, `text`, `html`, `reply_to`, `from`, `id` e `raw` (a mensagem inteira).

## Perfis e boosters

- Nos builds `nonet` e nano (`-tags nonet`) `Mail` não existe: chamar dá o erro "Mail não existe neste perfil".
- Um booster que chama `Mail.send` precisa declarar a permissão `"Mail"` no `booster.json`.

## Quando algo dá errado

| Mensagem | O que fazer |
|---|---|
| `falta DJO_SMTP_FROM no ambiente` | Defina `DJO_SMTP_FROM` (e `DJO_SMTP_HOST`) |
| `o remetente é sempre o configurado em DJO_SMTP_FROM` | O `from` só muda o nome; tire o endereço |
| `DJO_SMTP_SECURE=none só vale para o próprio computador` | Use TLS/STARTTLS; `none` só em localhost |
| `smtp: o servidor não oferece STARTTLS; recusei enviar sem criptografia` | Use a porta 465 (`DJO_SMTP_SECURE=tls`) ou um servidor com STARTTLS |
| `smtp: autenticação recusada` | Confira `DJO_SMTP_USER` e `DJO_SMTP_PASS` |
