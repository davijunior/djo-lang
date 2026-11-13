# Hardware: serial e tempo

Para ler sensores e falar com placas (Arduino, ESP32, GPS, leitores de cartão...), o `djo` abre
**portas seriais** (USB-serial ou UART). Não precisa instalar nada além do próprio `djo`.

Funciona em Linux (inclusive Raspberry Pi, OpenWrt), macOS e FreeBSD. No Windows, `Serial`
avisa que não é suportado. Existe em todos os perfis.

```
var porta = Serial.open:"/dev/ttyUSB0":9600
var linha = Serial.readline:porta        // "23.5", ou nil se nada chegou em 2 s
Serial.write:porta:"LIGA\n"
Serial.close:porta
```

## Serial

| Chamada                         | Faz |
|---------------------------------|-----|
| `Serial.open:"/dev/ttyUSB0":9600` | abre a porta (8 bits, sem paridade, 1 stop) e devolve um número que a identifica |
| `Serial.readline:porta`         | espera uma linha; devolve o texto **sem** o fim de linha, ou `nil` se passou o tempo |
| `Serial.readline:porta:5000`    | o mesmo, esperando até 5000 ms (o padrão é 2000) |
| `Serial.read:porta`             | o que já chegou, na hora (texto; `""` se nada) |
| `Serial.read:porta:500`         | espera até 500 ms por algum dado |
| `Serial.write:porta:texto`      | envia; devolve quantos bytes foram |
| `Serial.flush:porta`            | descarta o que chegou e ainda não foi lido |
| `Serial.close:porta`            | fecha |
| `Serial.ports:`                 | lista as portas que existem (`/dev/ttyUSB*`, `/dev/ttyACM*`, `/dev/ttyAMA*`, ...) |

- **Velocidades:** 1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200 e 230400.
- **Linhas:** `\n` ou `\r\n` encerram a linha. Se algo chega e a linha ainda não terminou, o que já veio fica
  guardado para a próxima chamada. Uma linha sem fim passa de 64 KB e é entregue como está.
- **Texto:** os dados são lidos e enviados como texto. Para protocolos binários, o `djo` ainda não ajuda.
- Portas abertas são **fechadas sozinhas** quando o programa termina. No máximo 4 abertas ao mesmo tempo.
- Só abre terminais: `/dev/tty*`, `/dev/cu.*`, `/dev/serial*`, `/dev/pts/*` (portas virtuais, como as do
  `socat`) e `/dev/serial/by-id/*`. Um caminho como `/dev/sda` ou `/etc/passwd` dá erro.
- Em páginas servidas por `djo serve`, nenhuma espera passa de 5 segundos.

### Permissão no Linux

Se der `permission denied`, o seu usuário não está no grupo da porta:

```
sudo usermod -aG dialout $USER     # depois saia e entre de novo
```

No Raspberry Pi, a UART dos pinos GPIO é `/dev/serial0` (precisa estar ligada em `raspi-config`).

## Tempo: Time.sleep e Time.ms

| Chamada            | Faz |
|--------------------|-----|
| `Time.sleep:1000`  | espera 1000 ms (no máximo 1 hora por chamada) |
| `Time.ms:`         | milissegundos desde 1970, para medir intervalos |

```
var inicio = Time.ms:
Time.sleep:250
print Time.ms: - inicio        // ~250
```

Em páginas servidas por `djo serve`, `Time.sleep` soma no máximo 2 segundos por página, para uma página
não prender o servidor.

## Exemplo completo

`exemplos/iot/sensor.djo` lê 10 temperaturas de um sensor serial, grava cada uma em `leituras.csv`
(`File.append`) e envia para um servidor (`Http.post`), esperando 1 s entre as leituras.

No `djo serve`, uma página pode mostrar a leitura do momento:

```
var porta = Serial.open:"/dev/ttyUSB0":9600
Serial.flush:porta
print "Temperatura: " + (Serial.readline:porta:3000 ?? "sem leitura")
```

Para um sensor que fica ligado o tempo todo, o melhor é um programa à parte (como o exemplo) que grava as
leituras com `File` ou `Db`, e as páginas só leem o que foi gravado.

## O que ainda falta

- GPIO, I2C e SPI (acender um LED, ler um sensor I2C direto da placa). Hoje isso se faz por uma placa ligada
  na serial (um Arduino ou ESP32 que fala com o `djo` por texto).
- Serial no Windows e protocolos binários.
