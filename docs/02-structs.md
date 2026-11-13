# Structs

Uma struct agrupa campos e métodos. O nome começa com letra **maiúscula**.

```
struct Post{
    id[Int] nocall                // não entra na chamada
    titulo[String]
    conteudo[String]
    publicado[Bool] = false       // valor padrão

    f link
        return "/post?id=" + (str:self.id)

    f publicar
        self.publicado = true
}
```

## Criar e usar

```
var p = Post:"Olá":"texto"      // os campos, em ordem, sem o 'nocall'
p.id = 7                        // mas o id pode ser mudado depois
print p.link:                   // /post?id=7
p.publicar:
print p.publicado               // true
```

- Campo: `obj.campo` (leitura e escrita). Método: `obj.metodo:args`.
- Dentro de um método, `self` é o próprio objeto.
- `p.vistas++` e `p.vistas--` funcionam em campos numéricos.
- Os objetos são **compartilhados**: `var q = p` aponta para o mesmo objeto.
- Imprimir mostra `Post(id: 7, titulo: "Olá", ...)`.

## Campos

`nome[Tipo]`, com tipo obrigatório. O tipo trava o campo: `p.titulo = 10` é erro.

| Marca        | Efeito |
|--------------|--------|
| `nocall`     | O campo não é argumento do construtor (ex.: `id`). Continua alterável. |
| `= valor`    | Valor padrão, calculado a cada criação (`tags[Arr] = []` dá uma lista nova por objeto). |

Sem valor padrão, o campo começa vazio: `0`, `0.0`, `""`, `false`, `[]`, `{}`, e `nil`
para `Any` e para campos do tipo de outra struct.

Combinam: `id[Int] nocall = 100`.

## Argumentos do construtor

- Podem faltar no fim: `Post:"Olá"`.
- `.` no lugar de um argumento pede o valor padrão: `Post:.:"texto":true` cria um post sem título.
- O `.` só vale ao criar structs, não em funções comuns.

## Tipos struct

Uma struct serve de tipo de campo ou de parâmetro, e esses valores podem ser `nil`:

```
f mostrar
    params[p[Post]]
    print p.titulo
```

## Composição

Uma linha só com o nome de outra struct a inclui (como no Go). Campos e métodos dela
ficam acessíveis direto:

```
struct Autor{
    nome[String]
    f assinatura
        return "por " + self.nome
}

struct Post{
    Autor                         // inclui o Autor
    titulo[String]
}

var p = Post:.:"Oi"
p.nome = "Ana"
print p.assinatura:               // por Ana
print p.Autor.nome                // Ana
```

- O objeto incluído nasce vazio, ou você passa um pronto: `Post:meuAutor:"Oi"` (os dois
  compartilham o mesmo objeto).
- Se a struct de fora tiver o mesmo nome, ela vence.
- Num método herdado por composição, `self` é o objeto incluído.

## Herança: `sonof`

```
struct Veiculo{
    placa[String]
    f descrever
        return "veículo " + self.placa
}

struct Carro sonof Veiculo{
    portas[Int] = 4
    f descrever                              // sobrescreve
        return "carro, " + (super.descrever:)
}

var c = Carro:"ABC-123"
print c.descrever:                           // carro, veículo ABC-123
```

- O filho recebe os campos do pai. No construtor eles vêm **primeiro**, depois os do filho.
- Os métodos do pai são herdados e podem ser sobrescritos.
- `self` num método herdado é o objeto filho.
- `super.metodo:` chama a versão do pai (e funciona em cadeia). Só existe dentro de métodos
  de uma struct com `sonof`.
- Um `Carro` serve onde se espera um `Veiculo`, mas não o contrário.
- Um pai só. Para juntar várias coisas, combine com composição:
  `struct Carro sonof Veiculo{ Motor ... }`.
- São erros: pai inexistente, herança circular, campo repetido do pai.

**Qual usar?** Herança para "é um" (carro é um veículo); composição para "tem um"
(post tem um autor). Na dúvida, prefira a composição.

## Interfaces

Uma interface lista os métodos que um tipo precisa ter. Como no Go, **não é preciso
declarar** que uma struct a cumpre: se ela tem os métodos, serve.

```
interface Pagavel{
    f preco
    f desconto
        params[pct[Int]]
}

struct Livro{
    base[Int]
    f preco
        return self.base
    f desconto
        params[pct[Int]]
        return self.base - self.base * pct / 100
}

f total
    params[item[Pagavel]]          // aceita qualquer coisa que cumpra Pagavel
    return item.preco:

print total:(Livro:100)            // 100
```

- A interface só tem assinaturas: `f nome` e, se houver argumentos, o `params`. Sem corpo.
- Serve de tipo de parâmetro e de campo (`item[Pagavel]`), e esses valores podem ser `nil`.
- O método da struct precisa ter o mesmo número e os mesmos tipos de parâmetros.
- Métodos herdados (`sonof`) e promovidos por composição contam.
- Quando não serve, o erro diz o que falta: `o parâmetro 'item' de 'total' deve ser Pagavel,
  mas recebeu Foto (Foto não tem o método 'preco')`.
- Para perguntar em tempo de execução: `is:valor:"Pagavel"`. Também funciona com tipos
  (`is:3:"Int"`) e structs pai (`is:carro:"Veiculo"`).
- Interface e struct não podem ter o mesmo nome. Interfaces podem ficar em `core/`.

## Em templates

Os métodos de struct dentro de um template precisam de chaves `{ }`.
