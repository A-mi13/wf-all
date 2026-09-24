#!/usr/bin/env bash
# scripts/pg.sh — локальный PostgreSQL 18 + PostGIS без Docker и без прав администратора.
# Кластер живёт в .tools/pg (в .gitignore); системный PostgreSQL не затрагивается.
set -euo pipefail
cd "$(dirname "$0")/.."

PG_VERSION=18.6-2
POSTGIS_VERSION=3.6.2
PG_URL="https://get.enterprisedb.com/postgresql/postgresql-${PG_VERSION}-windows-x64-binaries.zip"
POSTGIS_URL="https://download.osgeo.org/postgis/windows/pg18/postgis-bundle-pg18-${POSTGIS_VERSION}x64.zip"
# SHA256 архивов закрепляются при первой загрузке (Step 4) — дальше скачанное сверяется с ними.
PG_SHA256="41bb1496f60666d3745ae8a855753b877d14fa9e34667f266867635b790fc323"
POSTGIS_SHA256="0f41241cc536f7404dda43fd2a3f20ffe1fa1d71a8d4f6341428cd25931bf419"

ROOT="$PWD/.tools/pg"
BIN="$ROOT/pgsql/bin"
DATA="$ROOT/data"

die() { echo "pg.sh: $*" >&2; exit 1; }

# Порт: WF_PG_PORT из окружения (так его передаёт корневой Taskfile) → из deploy/dev/.env →
# 15432. Файл не source-ится: берётся только строка WF_PG_PORT=, значение — только цифры.
dev_env_port() {
  local f=deploy/dev/.env v
  [ -f "$f" ] || return 0
  v=$(grep -E '^[[:space:]]*(export[[:space:]]+)?WF_PG_PORT=' "$f" | tail -n 1 | cut -d= -f2- | tr -d '\r' || true)
  v=${v%%#*}                          # комментарий в конце строки
  v=$(printf '%s' "$v" | tr -d " \t\"'")
  [ -z "$v" ] && return 0
  case "$v" in *[!0-9]*) die "WF_PG_PORT в $f — не число" ;; esac
  printf '%s' "$v"
}
PORT="${WF_PG_PORT:-$(dev_env_port)}"
PORT="${PORT:-15432}"

require_windows() {
  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) ;;
    *) die "скрипт для Windows. На Linux/macOS — системный PostgreSQL 18 + PostGIS на порту $PORT (как в CI)." ;;
  esac
}

fetch() { # url, файл, ожидаемый sha256 (пусто — только напечатать)
  [ -f "$2" ] || curl -fL --retry 3 -o "$2" "$1"
  local got; got=$(sha256sum "$2" | cut -d' ' -f1)
  if [ -z "$3" ]; then
    echo "SHA256 $(basename "$2"): $got — закрепи в scripts/pg.sh"
  elif [ "$got" != "$3" ]; then
    rm -f "$2"; die "контрольная сумма $(basename "$2") не совпала"
  fi
}

install() {
  require_windows
  if [ -x "$BIN/postgres.exe" ] && [ -f "$ROOT/pgsql/share/extension/postgis.control" ]; then
    echo "уже установлено: $("$BIN/postgres" --version)"; return
  fi
  mkdir -p "$ROOT/dl"
  fetch "$PG_URL" "$ROOT/dl/pg.zip" "$PG_SHA256"
  fetch "$POSTGIS_URL" "$ROOT/dl/postgis.zip" "$POSTGIS_SHA256"
  unzip -q -o "$ROOT/dl/pg.zip" -d "$ROOT"            # → $ROOT/pgsql
  unzip -q -o "$ROOT/dl/postgis.zip" -d "$ROOT/dl/postgis"
  # Бандл PostGIS: один каталог верхнего уровня с bin/ lib/ share/ … — копируем поверх pgsql.
  cp -r "$ROOT/dl/postgis"/*/. "$ROOT/pgsql/"
  "$BIN/postgres" --version
}

init() {
  require_windows
  [ -x "$BIN/initdb.exe" ] || die "сначала: bash scripts/pg.sh install"
  if [ -f "$DATA/PG_VERSION" ]; then echo "кластер уже есть: $DATA"; return; fi
  "$BIN/initdb" -D "$DATA" -U postgres -A trust -E UTF8 \
    --locale-provider=builtin --builtin-locale=C.UTF-8 --locale=C >/dev/null
  printf "\nlisten_addresses = '127.0.0.1'\nport = %s\n" "$PORT" >> "$DATA/postgresql.conf"
  start
  "$BIN/psql" -h 127.0.0.1 -p "$PORT" -U postgres -d postgres -v ON_ERROR_STOP=1 -q \
    -f deploy/dev/initdb/20_wf.sql
  echo "кластер готов: 127.0.0.1:$PORT, база wf"
}

start() {
  require_windows
  if "$BIN/pg_ctl" -D "$DATA" status >/dev/null 2>&1; then echo "уже запущен на :$PORT"; return; fi
  "$BIN/pg_ctl" -D "$DATA" -l "$ROOT/postgres.log" -o "-p $PORT" -w start >/dev/null
  echo "запущен на 127.0.0.1:$PORT (лог: .tools/pg/postgres.log)"
}

stop() { require_windows; "$BIN/pg_ctl" -D "$DATA" -m fast -w stop; }
status() { require_windows; "$BIN/pg_ctl" -D "$DATA" status; }
psql_() { require_windows; "$BIN/psql" -h 127.0.0.1 -p "$PORT" -U postgres "$@"; }

case "${1:-}" in
  install) install ;;
  init) init ;;
  start) start ;;
  stop) stop ;;
  status) status ;;
  psql) shift; psql_ "$@" ;;
  port) echo "$PORT" ;;
  *) die "команды: install | init | start | stop | status | psql | port" ;;
esac
