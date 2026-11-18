# markdown

Booster oficial do djo: converte Markdown em HTML. Não pede nenhuma permissão.

    djo booster add markdown

    var r = markdown.render:texto      // {"html": ..., "titulo": ..., "toc": [{"id", "titulo"}]}
    var c = markdown.codigo:fonte      // bloco de código com realce, em HTML

Cobre títulos (#, ##, ###), parágrafos, listas com `-` e numeradas, tabelas,
blocos de código com cerca, **negrito**, `código` e [links](arquivo.md).

Referência completa (o que converte e o que não, o mapa devolvido, o realce de código e a segurança da saída): [O booster markdown](../../docs/17-booster-markdown.md). **Atenção:** a saída não filtra links `javascript:`, e uma linha que começa com `#` fora de `# `, `## ` e `### ` trava o `render`: não converta texto de visitantes sem ler a seção de segurança.

A API é o `main.djo`, com só `render` e `codigo`. O resto (inline, tabelas, listas e o realce de código) fica em `core/` e é interno.
