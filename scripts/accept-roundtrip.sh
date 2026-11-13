#!/usr/bin/env bash
# Проверяет roundtrip копий исходных файлов и отрицательные контроли.
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
source_home=$HOME
corpus=${1:-${CORPUS:-}}
if [ -z "$corpus" ]; then
  echo "нужен внешний список файлов: укажите путь аргументом или через CORPUS" >&2
  exit 2
fi
if [ ! -f "$corpus" ] || [ ! -r "$corpus" ]; then
  echo "список файлов недоступен: $corpus" >&2
  exit 2
fi
tmp=$(mktemp -d "${TMPDIR:?TMPDIR must point to scratch space}/lex-accept.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
go -C "$repo" build -o "$tmp/lex" ./cmd/lex
codex="$tmp/codex"
mkdir -p "$tmp/synthetic-home/inputs"
n=0
canonical_inputs="$tmp/canonical-inputs"
: > "$canonical_inputs"
while IFS= read -r rel || [ -n "$rel" ]; do
  [ -n "$rel" ] || continue
  source="$source_home/$rel"
  if [ ! -f "$source" ]; then
    echo "исходный файл недоступен: $source" >&2
    exit 2
  fi
  canonical=$(realpath "$source")
  if grep -Fxq "$canonical" "$canonical_inputs"; then
    continue
  fi
  printf '%s\n' "$canonical" >> "$canonical_inputs"
  copy="$tmp/synthetic-home/inputs/$(printf '%04d' "$n").md"
  sed '/^<!-- lex: собран из /d' "$source" > "$copy"
  HOME="$tmp/synthetic-home" "$tmp/lex" import --codex "$codex" "$copy"
  n=$((n+1))
done < "$corpus"
[ "$n" -gt 0 ] || { echo "в списке нет файлов: $corpus" >&2; exit 2; }
echo "imported $n copied files"
# Ссылка на уже взятый файл — тот же файл: второго манифеста быть не должно.
ln -s "$tmp/synthetic-home/inputs/0000.md" "$tmp/synthetic-home/alias.md"
HOME="$tmp/synthetic-home" "$tmp/lex" import --codex "$codex" "$tmp/synthetic-home/alias.md"
manifests=$(find "$codex/manifest" -type f -name '*.yaml' | wc -l | tr -d ' ')
[ "$manifests" -eq "$n" ] || { echo "FAIL: expected one manifest per file ($manifests manifests for $n files and a link to one of them)"; exit 1; }
HOME="$tmp/synthetic-home" "$tmp/lex" build --check --no-header --codex "$codex" || { echo "FAIL: build --check differs on copied corpus"; exit 1; }
units=0
for copy in "$tmp"/synthetic-home/inputs/*.md; do
  [ -e "$copy" ] || continue
  units=$((units + $(grep -cE '^(- |\* |#{1,6} )' "$copy" || true)))
done
frags=$(find "$codex/frag" -type f -name '*.md' | wc -l)
echo "fragments $frags, top-level units $units"
[ $((frags * 10)) -ge $((units * 9)) ] || { echo "FAIL: too few fragments"; exit 1; }
victim=$(find "$codex/frag" -type f -name '*.md' | sort | sed -n '100p')
[ -n "$victim" ] || { echo "FAIL: corpus produced fewer than 100 fragments"; exit 1; }
printf 'X' >> "$victim"
if HOME="$tmp/synthetic-home" "$tmp/lex" build --check --codex "$codex" >"$tmp/mut.out" 2>&1; then echo "FAIL: corrupted fragment not detected"; exit 1; fi
grep -q '/' "$tmp/mut.out" || { echo "FAIL: no path in mismatch report"; exit 1; }
echo "OK"
