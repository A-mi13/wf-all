#!/usr/bin/env bash
# Свежий backend/.env из шаблона: плейсхолдеры ключей «<base64-32-байта>» (API_JWT_SEEDS,
# API_HUMANCHECK_KEYS и любые будущие с тем же плейсхолдером) заменяются случайными ключами
# `openssl rand -base64 32`. Значения не выводятся. Вызывает `./task setup` сразу после
# копирования .env.example; уже заполненные ключи не трогает.
# Использование: bash scripts/env-keys.sh backend/.env
set -euo pipefail

f=${1:?нужен путь к .env}
placeholder='<base64-32-байта>'
tmp="$f.tmp.$$"
trap 'rm -f "$tmp"' EXIT

n=0
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    [A-Z]*"=$placeholder")
      line="${line%%=*}=$(openssl rand -base64 32)"
      n=$((n + 1))
      ;;
  esac
  printf '%s\n' "$line"
done <"$f" >"$tmp"
mv "$tmp" "$f"
echo "$f: сгенерировано ключей — $n"
