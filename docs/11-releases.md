# Releases: instalar sem Go e publicar uma versão

## Quem usa

Não precisa de Go. Escolha o arquivo pelo sistema e pelo processador:

| Sistema | Arquivos (perfil `full`: servidor, SQLite, tudo) |
|---------|--------------------------------------------------|
| Linux x86-64 / ARM64 | `djo-vX.Y.Z-full-linux-amd64.tar.gz`, `...-arm64.tar.gz` |
| macOS (Intel / Apple Silicon) | `djo-vX.Y.Z-full-darwin-amd64.tar.gz`, `...-arm64.tar.gz` |
| Windows x86-64 | `djo-vX.Y.Z-full-windows-amd64.zip` |
| FreeBSD x86-64 | `djo-vX.Y.Z-full-freebsd-amd64.tar.gz` |

Placas, roteadores e computadores pequenos usam os perfis menores (veja [Builds e IoT](07-builds.md)):

| Perfil | Tamanho do arquivo | Tem |
|--------|--------------------|-----|
| `tiny` | ~3 MB | a linguagem + `Http.get/post` (sem servidor web e sem SQLite) |
| `nano` | ~1,6 MB | só a linguagem, sem rede |

Disponíveis para Linux em `armv6` (Pi Zero/1), `armv7` (Pi 2/3), `arm64`, `amd64`, `386`, `riscv64`, `mipsle` e
`mips` (roteadores OpenWrt).

Cada arquivo traz o executável `djo`, o `README.md`, a pasta `docs/` e a `exemplos/`. Se `djo version` mostra
`djo v0.5.0 (perfil full) ...`, a instalação está certa.

### Instalador

```
DJO_BASE_URL=https://onde-esta-o-release DJO_VERSION=v0.5.0 sh install.sh
```

| Variável | Para quê |
|----------|----------|
| `DJO_BASE_URL` | pasta onde estão os arquivos e o `SHA256SUMS` (obrigatória) |
| `DJO_VERSION` | a versão, como `v0.5.0` (obrigatória) |
| `DJO_PROFILE` | `full` (padrão), `tiny` ou `nano` |
| `DJO_DIR` | onde instalar (padrão `~/.local/bin`) |

O script detecta o sistema e o processador, baixa o arquivo, **confere o SHA256** e só então instala. Se a
soma não bater, nada é instalado. Se o perfil não existir para a sua máquina, ele avisa qual tentar.

Conferir à mão: `sha256sum -c SHA256SUMS`.

## Quem publica uma versão

```
./release.sh v0.5.0           # tudo: 24 arquivos, uns 5 minutos
./release.sh v0.5.0 --quick   # só linux/amd64, para testar o processo
```

O script roda os testes (completo e `nano`), compila cada combinação sem CGO, carimba a versão no binário e
empacota em `dist/release/v0.5.0/`: os `.tar.gz` / `.zip`, o `SHA256SUMS` e o `install.sh`. Depois é só
publicar o conteúdo dessa pasta onde você quiser (um release do GitHub, um bucket, um servidor estático). O
`DJO_BASE_URL` do instalador aponta para esse lugar.

- O `release.sh` não publica nada sozinho.
- Precisa de Go, `tar`, `sha256sum` e `python3` (para gerar o `.zip`).
- Os binários **não são assinados**: no macOS o sistema pede autorização na primeira execução, e no Windows o
  SmartScreen pode avisar. A conferência de integridade é só o SHA256, que protege contra download corrompido, mas
  não contra quem controla o servidor do release.
- Um app gerado com `djo build` não precisa de nada disso: é um executável só, sem Go e sem a djo instalada.
