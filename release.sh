#!/usr/bin/env bash
# Gera os arquivos de um release em dist/release/, prontos para publicar.
#
#   ./release.sh v0.1.0              tudo: desktop (full) + placas (tiny e nano)
#   ./release.sh v0.1.0 --quick      só linux/amd64 (para testar o processo)
#
# Cada arquivo contém o binário "djo" + README.md + docs/ + exemplos/.
# No fim saem o SHA256SUMS e o install.sh, que são o que o usuário baixa.
set -euo pipefail
cd "$(dirname "$0")"

version="${1:-}"
quick="${2:-}"
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]]; then
  echo "uso: ./release.sh vMAIOR.MENOR.PATCH [--quick]   (ex.: v0.1.0)" >&2
  exit 2
fi

echo "== testes"
go vet ./...
go test ./... >/dev/null
go vet -tags "nodb noweb nonet" ./...
go test -tags "nodb noweb nonet" ./... >/dev/null

out="dist/release/$version"
rm -rf "$out"
mkdir -p "$out"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

tags_for() {
  case "$1" in
    full) echo "" ;;
    tiny) echo "nodb noweb" ;;
    nano) echo "nodb noweb nonet" ;;
  esac
}

pack() { # perfil os arch rótulo-da-arch [env...]
  local p="$1" os="$2" arch="$3" label="$4"; shift 4
  local name="djo-$version-$p-$os-$label"
  local dir="$stage/$name"
  local bin="djo"; [ "$os" = windows ] && bin="djo.exe"
  mkdir -p "$dir"
  env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$@" \
    go build -trimpath -ldflags "-s -w -X main.version=$version" -tags "$(tags_for "$p")" -o "$dir/$bin" .
  cp README.md "$dir/"
  cp -r docs exemplos "$dir/"
  # sobras de quando os exemplos foram rodados: não fazem parte do release
  find "$dir/exemplos" \( -name _saida -o -name _dados \) -prune -exec rm -rf {} +
  if [ "$os" = windows ]; then
    python3 - "$stage" "$name" "$out/$name.zip" <<'PY'
import os, sys, zipfile
stage, name, dest = sys.argv[1:]
with zipfile.ZipFile(dest, "w", zipfile.ZIP_DEFLATED) as z:
    for root, _, files in os.walk(os.path.join(stage, name)):
        for f in sorted(files):
            full = os.path.join(root, f)
            z.write(full, os.path.relpath(full, stage))
PY
  else
    tar -C "$stage" -czf "$out/$name.tar.gz" "$name"
  fi
  rm -rf "$dir"
  printf "%-46s %s\n" "$name" "$(du -h "$out/$name".* | cut -f1)"
}

echo "== desktop e servidor (perfil full)"
pack full linux amd64 amd64
if [ "$quick" != "--quick" ]; then
  pack full linux arm64 arm64
  pack full darwin amd64 amd64
  pack full darwin arm64 arm64
  pack full windows amd64 amd64
  pack full freebsd amd64 amd64

  echo "== placas e roteadores (perfis tiny e nano)"
  for p in tiny nano; do
    pack "$p" linux arm 'armv6' GOARM=6
    pack "$p" linux arm 'armv7' GOARM=7
    pack "$p" linux arm64 arm64
    pack "$p" linux amd64 amd64
    pack "$p" linux riscv64 riscv64
    pack "$p" linux mipsle mipsle GOMIPS=softfloat
    pack "$p" linux mips mips GOMIPS=softfloat
    pack "$p" linux 386 386
  done
fi

cp install.sh "$out/install.sh"
(cd "$out" && sha256sum -- *.tar.gz *.zip 2>/dev/null > SHA256SUMS || sha256sum -- *.tar.gz > SHA256SUMS)
echo "== pronto: $out ($(ls "$out" | wc -l) arquivos)"
echo "   publique o conteúdo dessa pasta como o release $version"
