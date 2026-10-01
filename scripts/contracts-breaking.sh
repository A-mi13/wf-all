#!/usr/bin/env bash
# Ломающие изменения контрактов (бандлов) относительно базового коммита — oasdiff (спека §8.3).
# База — $CONTRACTS_BASE (в CI: ветка PR или коммит до пуша), по умолчанию origin/main.
# Нет базы или бандла в ней (новая ветка, первый коммит контракта) — локально проверка пропускается;
# в CI (CI=true) нерезолвящаяся база — ошибка, иначе потеря fetch-depth или force-push молча отключат проверку.
# Исключение — нулевой SHA (github.event.before при первом пуше ветки): пропуск и в CI.
# Уровни серьёзности — contracts/oasdiff-severity.txt (формат oasdiff: «<id правила> <уровень>», комментарии
# в файле не допускаются, поэтому причины здесь). Добавление значения в enum ответа — не ломающее (спека §8.3:
# enum'ы открытые, клиент обязан переживать незнакомое значение); oasdiff по умолчанию считает его ERR.
set -euo pipefail
cd "$(dirname "$0")/.."
BASE="${CONTRACTS_BASE:-origin/main}"
OASDIFF="$PWD/.tools/bin/oasdiff"
TMP="$PWD/.tools/tmp"
mkdir -p "$TMP"
if ! git rev-parse --verify --quiet "$BASE^{commit}" >/dev/null; then
  if [[ "${CI:-}" == "true" && -n "${CONTRACTS_BASE:-}" && ! "$BASE" =~ ^0+$ ]]; then
    echo "contracts:breaking: ОШИБКА — база $BASE недоступна в CI (fetch-depth: 0? force-push?)" >&2
    exit 1
  fi
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
  "$OASDIFF" breaking "$TMP/contracts-base-$name.yaml" "$file" --fail-on ERR \
    --severity-levels contracts/oasdiff-severity.txt || status=1
  rm -f "$TMP/contracts-base-$name.yaml"
done
exit $status
