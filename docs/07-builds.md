# Builds e IoT

O `djo` é um binário único, sem dependências e sem CGO. Dá para compilar versões menores,
tirando o que o dispositivo não usa. Cada versão é um **perfil**:

| Perfil  | Tem                                          | Tamanho | Como              |
|---------|----------------------------------------------|---------|-------------------|
| `full`  | linguagem, servidor web, SQLite, PostgreSQL, MySQL, `Http.get` | ~14 MB  | padrão            |
| `lite`  | linguagem, servidor web, `Http.get`          | ~7 MB   | `-tags nodb`      |
| `noweb` | linguagem, SQLite, `Http.get`                | ~7,5 MB | `-tags noweb`     |
| `tiny`  | linguagem e `Http.get`/`post` (só saída)     | ~7 MB   | `-tags "nodb noweb"` |
| `nano`  | só a linguagem, **sem rede nenhuma**         | ~3 MB   | `-tags "nodb noweb nonet"` |

Em todos os perfis existem: a linguagem inteira (structs, interfaces, mapas, `for`...),
`Json`, `Time`, `Hash`, `File`, `Serial`, templates (`djo arquivo.djo.html`) e o REPL.

| Recurso                                | full | lite | noweb | tiny | nano |
|----------------------------------------|:----:|:----:|:-----:|:----:|:----:|
| `djo serve`, `Http` de entrada, `Session`, upload | sim | sim | não | não | não |
| `Db` (SQLite, PostgreSQL, MySQL)        | sim  | não  | sim   | não  | não  |
| `Http.get`, `Http.post`... (saída)     | sim  | sim  | sim   | sim  | não  |

O que pesa: o SQLite custa uns 4 MB, o PostgreSQL uns 0,7 MB e o MySQL uns 0,5 MB (`-tags nopg` e `-tags nomysql` tiram cada um), o servidor web uns 3,5 MB, e o suporte a HTTPS/rede
uns 3,6 MB. O `nano` tira tudo isso.

Usar um recurso que o perfil não tem dá um erro claro:
`erro (linha 3): Db não está incluído neste build (compilado com -tags nodb)`
(`Http.get`, no `nano`, avisa o mesmo com `-tags nonet`).

Para saber qual você tem: `djo version`.

```
$ djo version
djo (perfil tiny) linux/arm64 — servidor web: não, banco SQLite: não, Http.get/post: sim
```

## Compilando

O script `build.sh` na raiz do projeto cuida de tudo (precisa do Go instalado):

```
./build.sh                       # full, para esta máquina
./build.sh tiny                  # tiny, para esta máquina
./build.sh lite linux/arm64      # perfil e destino
./build.sh iot                   # tiny para as placas mais comuns
./build.sh iot nano              # o mesmo, no perfil nano (3 MB)
```

Os binários vão para `dist/`, já sem símbolos de depuração (`-ldflags "-s -w"`).

`./build.sh iot` gera (cada arquivo tem de 6,5 a 7 MB no `tiny`, ou uns 3 MB no `nano`):

| Arquivo                   | Para quê |
|---------------------------|----------|
| `djo-tiny-linux-armv6`    | Raspberry Pi Zero/1 e placas ARM antigas |
| `djo-tiny-linux-armv7`    | Raspberry Pi 2/3, BeagleBone |
| `djo-tiny-linux-arm64`    | Raspberry Pi 3/4/5, Orange Pi |
| `djo-tiny-linux-mipsle`   | roteadores OpenWrt (MediaTek) |
| `djo-tiny-linux-mips`     | roteadores OpenWrt (Atheros) |
| `djo-tiny-linux-riscv64`  | placas RISC-V |
| `djo-tiny-linux-386`      | x86 de 32 bits |

Sem o script, o comando equivalente é:

```
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -tags "nodb noweb" -o djo .
```

Copie o binário para o dispositivo (`scp dist/djo-tiny-linux-arm64 pi@placa:djo`) e rode
`./djo programa.djo`. Não precisa instalar mais nada nele.

## Um programa para IoT

```
// sensor.djo: monta a leitura e envia para o servidor
map leitura = {"sensor": "sala", "temp": 23.5, "hora": Time.now:}

var r = Http.post:"http://192.168.0.10:8080/leituras":leitura
if
    cond[not r["ok"]]
    print "não enviou: " + (r["error"] ?? str:r["status"])
```

Dica: em placas minúsculas, prefira `http://` para a rede local. Para `https://` o `djo` usa os
certificados do sistema (`/etc/ssl/certs`), que alguns Linux embarcados, como o OpenWrt, não
trazem instalados.

## O que mais é preciso para IoT

Para falar com sensores e placas, veja [Hardware: serial e tempo](08-iot.md): `Serial`, `Time.sleep` e `Time.ms`.
Eles existem em todos os perfis. Ainda **não existe** acesso direto a GPIO, I2C e SPI.
