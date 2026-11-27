#!/usr/bin/env bash
# Свежий backend/.env из шаблона: плейсхолдеры ключей «<base64-32-байта>» (API_JWT_SEEDS,
# API_HUMANCHECK_KEYS и любые будущие с тем же плейсхолдером) заменяются случайными ключами
# `openssl rand -base64 32`. Значения не выводятся. `./task setup` вызывает его на временной
# копии .env.example (backend/.env.tmp) и переносит её в backend/.env только при успехе; уже
# заполненные ключи не трогает. Сбой openssl — ненулевой выход, файл не меняется.
# Группы с одним значением на всех: кольцо identity у API_IDENTITY_SECRET_KEYS и WORKER_IDENTITY_SECRET_KEYS (письма с
# кодами собирает воркер тем же кольцом) и ключ подписи путей fs у API_, ADMIN_ и WORKER_BLOB_DEV_SIGN_KEY (admin-api
# подписывает ссылки на закрытое, api их проверяет): заполнен один — остальные получают его значение, не заполнен ни
# один — общий новый ключ.
# Использование: bash scripts/env-keys.sh backend/.env
set -euo pipefail

f=${1:?нужен путь к .env}
placeholder='<base64-32-байта>'
groups=(
  'API_IDENTITY_SECRET_KEYS WORKER_IDENTITY_SECRET_KEYS'
  'API_BLOB_DEV_SIGN_KEY ADMIN_BLOB_DEV_SIGN_KEY WORKER_BLOB_DEV_SIGN_KEY'
)
vals=()
differ=()
tmp="$f.tmp.$$"
trap 'rm -f "$tmp"' EXIT

# tr: openssl под Windows может закончить вывод на \r — $(...) срезает только \n
gen() { openssl rand -base64 32 | tr -d '\r\n'; }

# group_of K — номер группы ключа K в g (пусто — не из группы); без подстановки $(…): fork на строку медленный в Git Bash
group_of() {
  g=
  local i
  for i in "${!groups[@]}"; do
    case " ${groups[$i]} " in
      *" $1 "*)
        g=$i
        return
        ;;
    esac
  done
}

# общее значение группы: заполненное в файле (первое) или новое; заполнены разными значениями — предупреждение в
# stderr (без значений): например, воркер не прочитает коды из писем под ключом api (m2 ревью 1)
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    [A-Z]*=*) ;;
    *) continue ;;
  esac
  group_of "${line%%=*}"
  [ -n "$g" ] || continue
  v=${line#*=}
  case "$v" in
    "$placeholder" | "") ;;
    *)
      if [ -z "${vals[$g]:-}" ]; then
        vals[$g]=$v
      elif [ "${vals[$g]}" != "$v" ]; then
        differ[$g]=1
      fi
      ;;
  esac
done <"$f"
for i in "${!groups[@]}"; do
  if [ -n "${differ[$i]:-}" ]; then
    echo "$f: внимание — ${groups[$i]// / и } заполнены разными значениями; у них должно быть одно значение" \
      "(кольцо identity — письма с кодами собирает воркер; ключ подписи fs — ссылки admin-api проверяет api)" >&2
  fi
done

n=0
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    [A-Z]*"=$placeholder")
      k=${line%%=*}
      group_of "$k"
      if [ -n "$g" ]; then
        [ -n "${vals[$g]:-}" ] || vals[$g]=$(gen)
        line="$k=${vals[$g]}"
      else
        line="$k=$(gen)"
      fi
      n=$((n + 1))
      ;;
  esac
  printf '%s\n' "$line"
done <"$f" >"$tmp"
mv "$tmp" "$f"
echo "$f: сгенерировано ключей — $n"
