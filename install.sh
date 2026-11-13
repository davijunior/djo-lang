#!/bin/sh
# Instalador da djo: baixa o binário certo para esta máquina e confere o SHA256.
#
#   DJO_BASE_URL=https://onde-voce-publicou/v0.1.0 sh install.sh
#
# Variáveis:
#   DJO_BASE_URL  pasta do release (obrigatória: onde estão os .tar.gz e o SHA256SUMS)
#   DJO_VERSION   versão do arquivo, como v0.1.0 (obrigatória)
#   DJO_PROFILE   full | tiny | nano   (padrão: full; em ARM/MIPS/RISC-V pequenos use tiny ou nano)
#   DJO_DIR       onde instalar (padrão: ~/.local/bin)
set -eu

fail() { echo "install: $*" >&2; exit 1; }

[ -n "${DJO_BASE_URL:-}" ] || fail "defina DJO_BASE_URL com a pasta do release"
[ -n "${DJO_VERSION:-}" ] || fail "defina DJO_VERSION (ex.: v0.1.0)"
base="${DJO_BASE_URL%/}"
profile="${DJO_PROFILE:-full}"
dir="${DJO_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  FreeBSD) os=freebsd ;;
  *) fail "sistema $(uname -s) não suportado por este script (no Windows, baixe o .zip)" ;;
esac

m="$(uname -m)"
case "$m" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  armv7*|armv8l) arch=armv7 ;;
  armv6*) arch=armv6 ;;
  riscv64) arch=riscv64 ;;
  i386|i486|i586|i686) arch=386 ;;
  mips)
    # o mesmo uname serve para big e little endian; olha o byte order do /bin/sh
    if [ "$(od -An -tx1 -j5 -N1 /bin/sh 2>/dev/null | tr -d ' ')" = 01 ]; then arch=mipsle; else arch=mips; fi ;;
  mipsel|mipsle) arch=mipsle ;;
  *) fail "arquitetura $m não suportada" ;;
esac

name="djo-$DJO_VERSION-$profile-$os-$arch"
file="$name.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

get() { # url destino
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -q "$1" -O "$2"
  else fail "preciso de curl ou wget"; fi
}

echo "baixando $file"
get "$base/$file" "$tmp/$file" || fail "não consegui baixar $base/$file"
get "$base/SHA256SUMS" "$tmp/SHA256SUMS" || fail "não consegui baixar o SHA256SUMS"

want="$(grep "  $file\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)"
[ -n "$want" ] || fail "não há $file neste release (o perfil $profile pode não existir para $os/$arch; tente DJO_PROFILE=tiny ou nano)"
if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/$file" | cut -d' ' -f1)"
elif command -v shasum >/dev/null 2>&1; then got="$(shasum -a 256 "$tmp/$file" | cut -d' ' -f1)"
else fail "preciso de sha256sum ou shasum para conferir o download"; fi
[ "$want" = "$got" ] || fail "SHA256 não confere (esperado $want, veio $got); nada foi instalado"

tar -xzf "$tmp/$file" -C "$tmp"
mkdir -p "$dir"
cp "$tmp/$name/djo" "$dir/djo.new"
chmod 755 "$dir/djo.new"
mv "$dir/djo.new" "$dir/djo"

echo "instalado: $dir/djo"
"$dir/djo" version
case ":$PATH:" in *":$dir:"*) ;; *) echo "atenção: $dir não está no seu PATH" ;; esac
