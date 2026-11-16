#!/usr/bin/env bash
# Свежий backend/.env из шаблона: плейсхолдеры ключей «<base64-32-байта>» (API_JWT_SEEDS,
# API_HUMANCHECK_KEYS и любые будущие с тем же плейсхолдером) заменяются случайными ключами
# `openssl rand -base64 32`. Значения не выводятся. `./task setup` вызывает его на временной
# копии .env.example (backend/.env.tmp) и переносит её в backend/.env только при успехе; уже
# заполненные ключи не трогает. Сбой openssl — ненулевой выход, файл не меняется.
# Кольцо identity — одно значение у API_IDENTITY_SECRET_KEYS и WORKER_IDENTITY_SECRET_KEYS (письма с кодами
# собирает воркер тем же кольцом): заполнен один — второй получает его значение, не заполнен ни один — общий новый ключ.
# Использование: bash scripts/env-keys.sh backend/.env
set -euo pipefail

f=${1:?нужен путь к .env}
placeholder='<base64-32-байта>'
shared='API_IDENTITY_SECRET_KEYS WORKER_IDENTITY_SECRET_KEYS'
tmp="$f.tmp.$$"
trap 'rm -f "$tmp"' EXIT

# tr: openssl под Windows может закончить вывод на \r — $(...) срезает только \n
gen() { openssl rand -base64 32 | tr -d '\r\n'; }

# общее значение кольца identity: заполненное в файле (первое) или новое; заполнены оба разными значениями —
# предупреждение в stderr (без значений): воркер не прочитает коды из писем под ключом api (m2 ревью 1)
ring=
differ=
while IFS= read -r line || [ -n "$line" ]; do
  for k in $shared; do
    case "$line" in
      "$k=$placeholder" | "$k=") ;;
      "$k="*)
        if [ -z "$ring" ]; then
          ring=${line#*=}
        elif [ "$ring" != "${line#*=}" ]; then
          differ=1
        fi
        ;;
    esac
  done
done <"$f"
if [ -n "$differ" ]; then
  echo "$f: внимание — API_IDENTITY_SECRET_KEYS и WORKER_IDENTITY_SECRET_KEYS заполнены разными значениями;" \
    "кольцо identity у api и воркера должно совпадать (письма с кодами собирает воркер)" >&2
fi

n=0
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    [A-Z]*"=$placeholder")
      k=${line%%=*}
      case " $shared " in
        *" $k "*)
          [ -n "$ring" ] || ring=$(gen)
          line="$k=$ring"
          ;;
        *) line="$k=$(gen)" ;;
      esac
      n=$((n + 1))
      ;;
  esac
  printf '%s\n' "$line"
done <"$f" >"$tmp"
mv "$tmp" "$f"
echo "$f: сгенерировано ключей — $n"
