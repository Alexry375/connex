#!/usr/bin/env bash
# Compile connex pour linux/amd64 et linux/arm64 dans DIST (les binaires que le
# laptop pose sur le VPS et le Pi), puis copie celui de cette machine dans BIN.
# Chaque écriture est atomique : fichier temporaire du même dossier, puis mv.
#
#   scripts/install.sh [--dist D] [--bin B]
#   défauts : D = ~/.local/share/connex/dist, B = ~/.local/bin
set -euo pipefail

dist="$HOME/.local/share/connex/dist"
bin="$HOME/.local/bin"
while [ $# -gt 0 ]; do
  case "$1" in
    --dist) [ $# -ge 2 ] || { echo "install.sh : --dist attend un dossier" >&2; exit 2; }; dist=$2; shift 2 ;;
    --bin)  [ $# -ge 2 ] || { echo "install.sh : --bin attend un dossier" >&2; exit 2; }; bin=$2; shift 2 ;;
    -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
    *) echo "install.sh : argument inconnu : $1" >&2; exit 2 ;;
  esac
done

case "$(uname -m)" in
  x86_64) here=amd64 ;;
  aarch64|arm64) here=arm64 ;;
  *) echo "install.sh : architecture non prise en charge : $(uname -m)" >&2; exit 1 ;;
esac

mkdir -p "$dist" "$bin"
# Chemins absolus avant de passer à la racine du dépôt (un --dist relatif se
# lit depuis le dossier d'appel).
dist=$(cd "$dist" && pwd)
bin=$(cd "$bin" && pwd)
cd "$(dirname "$0")/.."
tmps=()
trap 'rm -f "${tmps[@]}"' EXIT

for arch in amd64 arm64; do
  tmp=$(mktemp "$dist/.connex-linux-$arch.XXXXXX")
  tmps+=("$tmp")
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -o "$tmp" ./cmd/connex
  chmod 755 "$tmp"
  mv -f "$tmp" "$dist/connex-linux-$arch"
  echo "compilé : $dist/connex-linux-$arch"
done

tmp=$(mktemp "$bin/.connex.XXXXXX")
tmps+=("$tmp")
cp "$dist/connex-linux-$here" "$tmp"
chmod 755 "$tmp"
mv -f "$tmp" "$bin/connex"
echo "installé : $bin/connex (linux-$here)"
