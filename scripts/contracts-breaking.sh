#!/usr/bin/env bash
# Ломающие изменения контрактов (бандлов) относительно базового коммита — oasdiff (спека §8.3).
# База — $CONTRACTS_BASE (в CI: ветка PR или коммит до пуша), по умолчанию origin/main.
# Нет базы или бандла в ней (новая ветка, первый коммит контракта) — проверка пропускается.
set -euo pipefail
cd "$(dirname "$0")/.."
BASE="${CONTRACTS_BASE:-origin/main}"
OASDIFF="$PWD/.tools/bin/oasdiff"
TMP="$PWD/.tools/tmp"
mkdir -p "$TMP"
if ! git rev-parse --verify --quiet "$BASE^{commit}" >/dev/null; then
  echo "contracts:breaking: базы $BASE нет — пропуск"
  exit 0
fi
status=0
for name in public admin; do
  file="contracts/openapi/$name.yaml"
  if ! git cat-file -e "$BASE:$file" 2>/dev/null; then
    echo "contracts:breaking: $file нет в $BASE — пропуск"
    continue
  fi
  git show "$BASE:$file" > "$TMP/contracts-base-$name.yaml"
  echo "contracts:breaking: $file против $BASE"
  "$OASDIFF" breaking "$TMP/contracts-base-$name.yaml" "$file" --fail-on ERR || status=1
  rm -f "$TMP/contracts-base-$name.yaml"
done
exit $status
