#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d "${TMPDIR:?}/lex-refs-measure.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
export TMPDIR="$work"
if [ "$#" -ne 1 ] || [ -z "$1" ]; then echo "нужна внешняя опись: укажите путь аргументом" >&2; exit 2; fi
graft exec --heavy -- go run ./scripts/text-refs-measure.go "$1"
