#!/usr/bin/env bash
# scripts/s3.sh — dev-хранилище S3 (Garage v2, план media R1) в Docker: интеграционные тесты blob/s3 (R13) и живой
# e2e. Контейнер и том — только wf-s3-dev; секреты (rpc_secret, ключи процессов) генерируются при `up` и выводятся
# только командой `env`.
#   up      — создать или запустить контейнер, настроить бакеты, ключи и права (scripts/s3/init.sh; повтор безопасен)
#   env     — строки `export WF_TEST_S3_…=…` для eval: адрес, регион, ключи api, admin, worker
#   status  — состояние контейнера
#   down    — удалить контейнер и том wf-s3-dev (все объекты и ключи пропадут)
# Порты (только 127.0.0.1): S3 API — WF_S3_PORT (13900), сайт бакета wf-pub — WF_S3_WEB_PORT (13902).
set -euo pipefail
cd "$(dirname "$0")/.."
# Git Bash иначе переписывает /garage и dst=/var/lib/garage в пути Windows
export MSYS_NO_PATHCONV=1

NAME=wf-s3-dev
IMAGE=dxflrs/garage:v2.6.0
PORT=${WF_S3_PORT:-13900}
WEB_PORT=${WF_S3_WEB_PORT:-13902}
REGION=garage

die() { echo "s3.sh: $*" >&2; exit 1; }
usage() { die "команды: up | env | status | down"; }

for p in "$PORT" "$WEB_PORT"; do
  case "$p" in '' | *[!0-9]*) die "порт «$p» — не число (WF_S3_PORT, WF_S3_WEB_PORT)" ;; esac
done

need_docker() {
  command -v docker >/dev/null 2>&1 || die "нет docker в PATH — установи Docker и запусти демон"
}
exists() { docker container inspect "$NAME" >/dev/null 2>&1; }
running() { [ "$(docker container inspect -f '{{.State.Running}}' "$NAME" 2>/dev/null)" = true ]; }
g() { docker exec -e RUST_LOG=warn "$NAME" /garage "$@"; }

# endpoint — адрес S3 API запущенного контейнера (порт мог быть задан прошлым `up`).
endpoint() {
  local hp
  hp=$(docker port "$NAME" 3900/tcp | head -n 1)
  [ -n "$hp" ] || die "у $NAME не опубликован порт 3900 — bash scripts/s3.sh down && bash scripts/s3.sh up"
  printf 'http://127.0.0.1:%s' "${hp##*:}"
}

up() {
  need_docker
  if ! exists; then
    docker volume create "$NAME" >/dev/null
    # Лимиты dev-Garage (пользователь, 27.11.2026): ему хватает доли ядра и сотни МБ; уже созданный контейнер —
    # docker update --cpus 1 --memory 512m --memory-swap 512m wf-s3-dev
    docker create --name "$NAME" -e "GARAGE_RPC_SECRET=$(openssl rand -hex 32)" \
      --cpus 1 --memory 512m --memory-swap 512m \
      -p "127.0.0.1:$PORT:3900" -p "127.0.0.1:$WEB_PORT:3902" \
      --mount "type=volume,src=$NAME,dst=/var/lib/garage" "$IMAGE" >/dev/null
    docker cp scripts/s3/garage.toml "$NAME:/etc/garage.toml"
  fi
  running || docker start "$NAME" >/dev/null
  local i
  for i in $(seq 1 30); do
    g status >/dev/null 2>&1 && break
    [ "$i" = 30 ] && { docker logs --tail 20 "$NAME" >&2 || true; die "Garage не ответил за 30 с"; }
    sleep 1
  done
  WF_S3_CONTAINER="$NAME" WF_S3_ENDPOINT="$(endpoint)" bash scripts/s3/init.sh
  echo "готово: S3 $(endpoint), бакеты wf-in, wf-pub, wf-priv; ключи — eval \"\$(bash scripts/s3.sh env)\""
}

# key_field — поле «Key ID» или «Secret key» из `garage key info --show-secret`.
key_field() { awk -v f="$1" -F': *' '$1 == f { print $2; exit }'; }

env_() {
  need_docker
  running || die "контейнер $NAME не запущен — сначала bash scripts/s3.sh up"
  local ep role info id secret up_role
  ep=$(endpoint)
  printf 'export WF_TEST_S3_ENDPOINT=%s\n' "$ep"
  # другой Host у подписывающего клиента (R9): подпись SigV4 включает хост
  printf 'export WF_TEST_S3_PUBLIC_ENDPOINT=%s\n' "${ep/127.0.0.1/localhost}"
  printf 'export WF_TEST_S3_REGION=%s\n' "$REGION"
  for role in api admin worker; do
    info=$(g key info --show-secret "wf-dev-$role" 2>/dev/null) || die "нет ключа wf-dev-$role — повтори bash scripts/s3.sh up"
    id=$(printf '%s\n' "$info" | key_field 'Key ID')
    secret=$(printf '%s\n' "$info" | key_field 'Secret key')
    [ -n "$id" ] && [ -n "$secret" ] || die "не разобран вывод garage key info для wf-dev-$role"
    up_role=$(printf '%s' "$role" | tr '[:lower:]' '[:upper:]')
    printf 'export WF_TEST_S3_%s_ACCESS_KEY=%s\nexport WF_TEST_S3_%s_SECRET_KEY=%s\n' "$up_role" "$id" "$up_role" "$secret"
  done
}

status() {
  need_docker
  exists || { echo "нет контейнера $NAME — bash scripts/s3.sh up"; return 1; }
  echo "$NAME: $(docker container inspect -f '{{.State.Status}}' "$NAME")"
  if running; then echo "S3: $(endpoint)"; fi
}

down() {
  need_docker
  if exists; then docker rm -f "$NAME" >/dev/null; fi
  if docker volume inspect "$NAME" >/dev/null 2>&1; then docker volume rm "$NAME" >/dev/null; fi
  echo "удалены контейнер и том $NAME"
}

[ "$#" -le 1 ] || usage
case "${1:-}" in
  up) up ;;
  env) env_ ;;
  status) status ;;
  down) down ;;
  *) usage ;;
esac
