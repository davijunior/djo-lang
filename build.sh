#!/usr/bin/env bash
# Compila o djo em perfis, sem CGO (compilação cruzada direta).
#
#   ./build.sh                    perfil full para esta máquina
#   ./build.sh tiny               perfil tiny para esta máquina
#   ./build.sh lite linux/arm64   perfil e destino (os/arch)
#   ./build.sh iot                tiny para as placas comuns (ARM, ARM64, MIPS, RISC-V)
#   ./build.sh iot nano           o mesmo, no perfil nano
#
# Perfis:
#   full   tudo: servidor web, SQLite                  (~11 MB)
#   lite   servidor web, sem SQLite                    (~7 MB)   -tags nodb
#   noweb  SQLite, sem servidor web                    (~7,5 MB) -tags noweb
#   tiny   só a linguagem + Http.get/post (saída)      (~7 MB)   -tags "nodb noweb"
#   nano   só a linguagem, sem rede nenhuma            (~3 MB)   -tags "nodb noweb nonet"
set -euo pipefail
cd "$(dirname "$0")"

profile="${1:-full}"
target="${2:-}"

tags_for() {
  case "$1" in
    full) echo "" ;;
    lite) echo "nodb" ;;
    noweb) echo "noweb" ;;
    tiny) echo "nodb noweb" ;;
    nano) echo "nodb noweb nonet" ;;
    *) echo "perfil desconhecido: $1 (use full, lite, noweb, tiny, nano ou iot)" >&2; exit 2 ;;
  esac
}

build() { # perfil os arch [extra env...]
  local p="$1" os="$2" arch="$3"; shift 3
  local label="$arch"
  for e in "$@"; do [ "${e%%=*}" = GOARM ] && label="armv${e#*=}"; done
  local out="dist/djo-$p-$os-$label"
  [ "$os" = windows ] && out="$out.exe"
  mkdir -p dist
  env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$@" \
    go build -trimpath -ldflags "-s -w" -tags "$(tags_for "$p")" -o "$out" .
  awk -v f="$out" -v s="$(stat -c %s "$out")" 'BEGIN { printf "%-36s %5.1f MB\n", f, s / 1048576 }'
}

if [ "$profile" = iot ]; then
  p="${2:-tiny}"
  build "$p" linux arm GOARM=6          # Raspberry Pi Zero/1, muitas placas antigas
  build "$p" linux arm GOARM=7          # Raspberry Pi 2/3, BeagleBone
  build "$p" linux arm64                # Raspberry Pi 3/4/5, Orange Pi
  build "$p" linux mipsle GOMIPS=softfloat   # roteadores OpenWrt (MediaTek)
  build "$p" linux mips GOMIPS=softfloat     # roteadores OpenWrt (Atheros)
  build "$p" linux riscv64              # placas RISC-V
  build "$p" linux 386                  # x86 antigos
  exit 0
fi

if [ -n "$target" ]; then
  build "$profile" "${target%/*}" "${target#*/}"
else
  build "$profile" "$(go env GOOS)" "$(go env GOARCH)"
fi
