# Каркас монорепо wf-all — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** из текущей папки с документами и миграциями получить монорепо, где `./task setup && ./task dev` поднимает связанные `api`, `admin-api`, `worker`, веб и админку, а `./task ci` зелёный.

**Architecture:** раскладка по ролям — `backend/` (Go, свой go.mod), `contracts/` (OpenAPI + токены, источник истины), `apps/web` и `apps/admin` (свои package.json, клиенты API внутри), `packages/*` (общее для фронтов: config, tokens, i18n). Всё сгенерированное коммитится. Каждый шаг — TDD.

**Tech Stack:** Go 1.27.1, chi 5.3.2, pgx 5.11, goose 3.28 (библиотекой), sqlc 1.31.1, oapi-codegen 2.8.0, River 0.47, pgtestdb 0.1.1, kin-openapi 0.149; PostgreSQL 18.6 + PostGIS 3.6; pnpm 12.6.0, Node 26.10, TypeScript 6.0.3 (5.9.3 у генератора), Next 16.3.6, React 19.3, Vite 8.3.1, Vitest 5.0.1, next-intl/use-intl 4.14.7, TanStack Query 5.103, MSW 2.15 + openapi-msw 2.0, ESLint 9.39.5, Playwright 1.63; go-task 3.53.1, golangci-lint 2.14.0, lefthook 2.1.14, gitleaks 8.30.1, actionlint 1.7.12.

**Spec:** `docs/superpowers/specs/2026-09-24-repo-structure-design.md`

## Global Constraints

- Слаг `wf`: Go-модуль `wf/backend`, npm-скоуп `@wf/*`. Слов «wherefootball»/«gdefutbol» в идентификаторах нет — бренд только в локалях.
- Версии — ровно из Tech Stack. Отклонение только при несовместимости, с записью в `docs/versions.md`.
- TDD: тест пишется и запускается ДО реализации, падение проверяется глазами (причина — отсутствие кода, а не опечатка).
- Никаких глобальных установок, кроме обновления глобального pnpm внутри 10.x — и только после «да» пользователя (Task 1).
- Никакого `rm -rf` вне рабочего каталога проекта; временных копий проекта с зависимостями не создавать.
- Все строки UI — в `apps/*/messages/{ru,en}.json`; `en` с теми же ключами, значения могут быть пустыми.
- Комментарии в коде — по-русски, идентификаторы — по-английски.
- Первая строка блока кода вида `// путь/к/файлу` — подпись, а не содержимое. В JSON-файлах (кроме tsconfig) её не писать: JSON не допускает комментариев.
- Переводы строк LF; порты ниже 49152 и через env; секретов в git нет.
- Коммиты одной строкой по-русски, без тела. Пуш — только по команде пользователя.
- Cwd для `go tool -modfile=...` должен быть внутри модуля (`backend/`), иначе ошибка `cannot find main module`.
- В Taskfile бинарники из `.tools/bin` вызываются по явному пути `{{.TOOLS}}/имя` — `env: PATH` в Taskfile не перекрывает PATH ОС.

## Review Focus

1. Postgres не запущен или порт 15432 занят → тесты БД падают с понятным сообщением «запусти `./task infra:up`», а не с паникой пула. Проверка: Task 4, Step 5 — остановить базу и увидеть сообщение `requireServer`; порт настраивается `WF_TEST_PG_PORT` (`TestConfigFromEnvOverride`).
2. Бинарник стартует без обязательной переменной окружения → сразу выходит с кодом ≠ 0 и именем переменной в ошибке. Тест: Task 7.
3. Неизвестный маршрут или неверный метод → `application/problem+json` с `code`, а не HTML chi. Тест: Task 8.
4. Паника в хендлере → 500 `problem+json`, процесс жив. Тест: Task 8.
5. Пустая строка в `en.json` → показывается русский текст, а не пустота. Тест: Task 15 (`withFallback`) и тесты локалей в Task 16/17.

## Карта файлов

```
tools/go.mod                    пины task, gitleaks, actionlint (сборка в .tools/bin)
tools/lint/go.mod               пин golangci-lint (отдельно: свой граф зависимостей)
scripts/bootstrap.sh            первая команда на чистой машине
scripts/doctor.sh               проверка версий окружения (тест для Task 1–2)
task                            обёртка: exec .tools/bin/task
scripts/pg.sh                   локальный PostgreSQL 18.6 + PostGIS 3.6.2 из zip в .tools/pg
deploy/dev/initdb/20_wf.sql     роли, расширения в template1, база wf
backend/go.mod, backend/tools/go.mod
backend/migrations/*.sql + embed.go
backend/cmd/{api,admin-api,worker,migrate}/main.go
backend/internal/platform/{config,logx,db,httpx,migrate,queue,flags}/
backend/internal/platform/testkit/{dbtest,apitest}/
backend/internal/httpapi/{public,admin}/
backend/internal/archtest/boundary_test.go
backend/sqlc.yaml  backend/.golangci.yml  backend/Taskfile.yml  backend/.env.example
contracts/openapi/{public,admin}.yaml  contracts/tokens/tokens.json
contracts/package.json  contracts/test/*.test.mjs  contracts/Taskfile.yml
packages/config/  packages/tokens/  packages/i18n/
apps/admin/  apps/web/
Taskfile.yml  package.json  pnpm-workspace.yaml  lefthook.yml
.github/workflows/{backend,contracts,packages,web,admin}.yml
design/screens/  design/tools/extract-export.mjs
docs/versions.md  docs/03-вынос-в-отдельный-репо.md  docs/04-принципы-архитектуры.md
```

---

## Фаза 0. Окружение

### Task 1: Корень pnpm-workspace и pnpm 12 / Node 26

**Files:**
- Create: `scripts/doctor.sh`, `package.json`, `pnpm-workspace.yaml`, `.editorconfig`
- Modify: `.gitignore`

**Interfaces:**
- Produces: `scripts/doctor.sh [pnpm|go|tools|all]` — печатает `ok <имя> <версия>` или `FAIL ...` и выходит с 1; корневой `package.json` с `packageManager`, `devEngines.runtime`; `pnpm-workspace.yaml` с `packages`, `catalog`, `catalogs.contracts`, `allowBuilds`.

- [ ] **Step 1: Написать проверку окружения (тест)**

```bash
# scripts/doctor.sh
#!/usr/bin/env bash
# Проверка версий окружения. Используется как тест фазы 0 и в README.
set -uo pipefail
cd "$(dirname "$0")/.."
fail=0
check() { # имя, ожидаемая версия, фактический вывод
  if [[ "$3" == *"$2"* ]]; then echo "ok   $1 $2"; else echo "FAIL $1: ждали $2, получили: $3"; fail=1; fi
}
what="${1:-all}"
if [[ "$what" == pnpm || "$what" == all ]]; then
  check pnpm 12.6.0 "$(pnpm --version 2>&1)"
  check node v26.10.0 "$(pnpm exec node --version 2>&1)"
fi
if [[ "$what" == go || "$what" == all ]]; then
  check go go1.27.1 "$(cd backend 2>/dev/null && go version 2>&1)"
fi
if [[ "$what" == tools || "$what" == all ]]; then
  check task 3.53.1 "$(.tools/bin/task --version 2>&1)"
  check golangci-lint 2.14.0 "$(.tools/bin/golangci-lint version 2>&1)"
  check gitleaks 8.30.1 "$(.tools/bin/gitleaks version 2>&1)"
  check actionlint 1.7.12 "$(.tools/bin/actionlint -version 2>&1)"
  check mailpit 1.31.2 "$(.tools/bin/mailpit version 2>&1)"
fi
exit $fail
```

- [ ] **Step 2: Запустить — должно упасть**

Run: `bash scripts/doctor.sh pnpm`
Expected: `FAIL pnpm: ждали 12.6.0, получили: 10.33.1` (и FAIL node).

- [ ] **Step 3: Проверить глобальный pnpm; при версии < 10.34.5 — СТОП, спросить пользователя**

Run: `pnpm --version`
Если `< 10.34.5`: pnpm 10.33 не умеет переключаться на 12 (заглушка бинарника, исправлено в 10.34.5).
Спросить: «Обновить глобальный pnpm до 10.34.5 командой `npm install -g pnpm@10.34.5`? Мажорная версия та же, другие проекты не затронет». Выполнять только после «да».

- [ ] **Step 4: Создать корневые файлы**

```json
// package.json
{
  "name": "wf",
  "private": true,
  "packageManager": "pnpm@12.6.0",
  "devEngines": {
    "runtime": { "name": "node", "version": "26.10.0", "onFail": "download" }
  },
  "scripts": {
    "format": "prettier --write --ignore-unknown ."
  }
}
```

```yaml
# pnpm-workspace.yaml
packages:
  - apps/*
  - packages/*
  - contracts

# Общие версии. Две версии TypeScript: приложения на 6.0.3,
# генератор клиентов (openapi-typescript, peer ^5.x) — на 5.9.3.
catalog:
  typescript: 6.0.3
  react: 19.3.0
  react-dom: 19.3.0
  '@types/react': 19.3.0
  '@types/react-dom': 19.3.0
  '@types/node': 26.6.2
  vitest: 5.0.1
  vite: 8.3.1
  eslint: 9.39.5
  prettier: 3.9.9
catalogs:
  contracts:
    typescript: 5.9.3

# pnpm 12: пакет со скриптом установки, которого нет в списке, роняет install.
allowBuilds:
  lefthook: true
  msw: false
  '@parcel/watcher': false
  '@swc/core': false
  unrs-resolver: false
```

```ini
# .editorconfig
root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
indent_style = space
indent_size = 2
trim_trailing_whitespace = true

[*.go]
indent_style = tab

[*.md]
trim_trailing_whitespace = false
```

Дописать в `.gitignore`:

```gitignore
# сборки фронтов
.next/
dist/
coverage/
playwright-report/
test-results/
*.tsbuildinfo
```

- [ ] **Step 5: Установить и перезапустить проверку**

Run: `pnpm install && bash scripts/doctor.sh pnpm`
Expected: `ok   pnpm 12.6.0`, `ok   node v26.10.0`. Появился `pnpm-lock.yaml`.
Если `devEngines.runtime` не скачал Node 26 на Windows — СТОП, записать фактический вывод и предложить пользователю `nvm install 26.10.0` (глобальный переключатель nvm-windows) как запасной путь.

- [ ] **Step 6: Коммит**

```bash
git add scripts/doctor.sh package.json pnpm-workspace.yaml pnpm-lock.yaml .editorconfig .gitignore
git commit -m "Корень pnpm-workspace: pnpm 12, Node 26, проверка окружения"
```

### Task 2: Бинарники инструментов в .tools/bin

**Files:**
- Create: `tools/go.mod`, `tools/go.sum`, `tools/lint/go.mod`, `tools/lint/go.sum`, `scripts/bootstrap.sh`, `task`

**Interfaces:**
- Consumes: `scripts/doctor.sh tools` (Task 1).
- Produces: `.tools/bin/{task,gitleaks,actionlint,golangci-lint,mailpit}`; `./task <цель>` — обёртка; `scripts/bootstrap.sh` — ставит всё без глобальных изменений.

- [ ] **Step 1: Запустить проверку — должна упасть**

Run: `bash scripts/doctor.sh tools`
Expected: пять `FAIL` (бинарников нет).

- [ ] **Step 2: Модули с пинами**

```bash
mkdir -p tools/lint
cd tools && go mod init wf/tools && go mod edit -go=1.27 -toolchain=go1.27.1 \
  && go get -tool github.com/go-task/task/v3/cmd/task@v3.53.1 \
  && go get -tool github.com/zricethezav/gitleaks/v8@v8.30.1 \
  && go get -tool github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 \n  && go get -tool github.com/axllent/mailpit@v1.31.2 && cd ..
cd tools/lint && go mod init wf/tools/lint && go mod edit -go=1.27 -toolchain=go1.27.1 \
  && go get -tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 && cd ../..
```

- [ ] **Step 3: bootstrap и обёртка**

```bash
# scripts/bootstrap.sh
#!/usr/bin/env bash
# Первая команда на чистой машине. Ничего не ставит глобально:
# Go-инструменты собираются в .tools/bin по пинам из tools/go.mod (проверка go.sum).
set -euo pipefail
cd "$(dirname "$0")/.."
BIN="$PWD/.tools/bin"
mkdir -p "$BIN"
go -C tools build -o "$BIN/" \
  github.com/go-task/task/v3/cmd/task \
  github.com/zricethezav/gitleaks/v8 \
  github.com/rhysd/actionlint/cmd/actionlint \n  github.com/axllent/mailpit
go -C tools/lint build -o "$BIN/" github.com/golangci/golangci-lint/v2/cmd/golangci-lint
pnpm install
echo "Готово. Дальше: ./task setup"
```

```sh
# task  (файл без расширения в корне)
#!/usr/bin/env sh
# Обёртка, чтобы не добавлять .tools/bin в PATH руками.
exec "$(dirname "$0")/.tools/bin/task" "$@"
```

Run: `chmod +x scripts/bootstrap.sh scripts/doctor.sh task && git update-index --chmod=+x scripts/bootstrap.sh scripts/doctor.sh task 2>/dev/null; true`

- [ ] **Step 4: Запустить bootstrap и проверку**

Run: `bash scripts/bootstrap.sh && bash scripts/doctor.sh tools`
Expected: пять `ok`. Если формат вывода версии у инструмента другой — поправить ожидание в `doctor.sh`, не версию.

- [ ] **Step 5: Коммит**

```bash
git add tools scripts/bootstrap.sh task
git update-index --chmod=+x scripts/bootstrap.sh scripts/doctor.sh task
git commit -m "Инструменты в .tools/bin по пинам, bootstrap и обёртка task"
```

### Task 3: Dev-инфраструктура: переносимый PostgreSQL 18 + PostGIS, роли

Docker на машине разработчика не работает (WSL), системный PostgreSQL 18.1 (служба на :5432) — без PostGIS и с неизвестным паролем суперпользователя. Поэтому — свой кластер из официальных zip-архивов в `.tools/pg/`, без прав администратора и без службы. Системный PostgreSQL не трогать. Docker остаётся только в CI (service container) и на сервере.

**Files:**
- Create: `scripts/pg.sh`, `deploy/dev/initdb/20_wf.sql`, `deploy/dev/.env.example`

**Interfaces:**
- Produces:
  - `bash scripts/pg.sh install` — скачать и распаковать PostgreSQL 18.6-2 (EDB, zip) и PostGIS 3.6.2 (OSGeo, zip) в `.tools/pg/pgsql`, с проверкой контрольных сумм; повторный запуск — no-op;
  - `bash scripts/pg.sh init` — `initdb` в `.tools/pg/data` (UTF8, суперпользователь `postgres`, trust только для 127.0.0.1), затем `deploy/dev/initdb/20_wf.sql`; если кластер уже есть — no-op;
  - `bash scripts/pg.sh start | stop | status` — `pg_ctl` на `127.0.0.1:${WF_PG_PORT:-15432}`, лог `.tools/pg/postgres.log`;
  - `bash scripts/pg.sh psql [args]` — psql к этому кластеру суперпользователем;
  - после `init`: роли `migrator`, `api`, `admin`, `worker` (пароль = имя, только dev); база `wf` (владелец `migrator`); расширения `btree_gist, unaccent, citext, postgis` в `template1` — наследуются всеми новыми базами, включая клоны pgtestdb.
- Суперпользователь `postgres/postgres`, порт 15432 — как ждут `dbtest` (Task 4) и `backend/.env.example` (Task 12). При trust-аутентификации пароль не проверяется, но в URL допустим.

- [ ] **Step 1: Проверка до реализации — падает**

Run: `bash scripts/pg.sh status`
Expected: `No such file or directory` — скрипта нет.

- [ ] **Step 2: SQL начальной настройки**

```sql
-- deploy/dev/initdb/20_wf.sql
-- Роли по бинарникам: ошибки прав всплывают в dev, а не на проде.
-- Точные права — в спеке бэкенда; здесь только каркас.
CREATE ROLE migrator LOGIN PASSWORD 'migrator';
CREATE ROLE api      LOGIN PASSWORD 'api';
CREATE ROLE admin    LOGIN PASSWORD 'admin';
CREATE ROLE worker   LOGIN PASSWORD 'worker';

-- PostGIS не «доверенное» расширение: создаёт только суперпользователь.
-- Ставим в template1 — его наследуют wf и все тестовые клоны,
-- а CREATE EXTENSION IF NOT EXISTS в миграциях становится no-op.
\connect template1
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS postgis;

\connect postgres
CREATE DATABASE wf OWNER migrator;

\connect wf
GRANT USAGE ON SCHEMA public TO api, admin, worker;
ALTER DEFAULT PRIVILEGES FOR ROLE migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO api, admin, worker;
ALTER DEFAULT PRIVILEGES FOR ROLE migrator IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO api, admin, worker;
```

```ini
# deploy/dev/.env.example — порт можно переопределить, если занят (Hyper-V резервирует блоки)
WF_PG_PORT=15432
```

- [ ] **Step 3: Скрипт кластера**

```bash
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
PG_SHA256=""
POSTGIS_SHA256=""

ROOT="$PWD/.tools/pg"
BIN="$ROOT/pgsql/bin"
DATA="$ROOT/data"
PORT="${WF_PG_PORT:-15432}"

die() { echo "pg.sh: $*" >&2; exit 1; }

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
  *) die "команды: install | init | start | stop | status | psql" ;;
esac
```

Run: `chmod +x scripts/pg.sh`

- [ ] **Step 4: Установка и закрепление контрольных сумм**

Run: `bash scripts/pg.sh install`
Expected: скачаны оба архива (~340 МБ и ~120 МБ), напечатаны две строки `SHA256 …: <хэш> — закрепи`, затем `postgres (PostgreSQL) 18.6`.
Сверить PostGIS с опубликованным MD5: `curl -s https://download.osgeo.org/postgis/windows/pg18/postgis-bundle-pg18-3.6.2x64.zip.md5` и `md5sum .tools/pg/dl/postgis.zip` — должны совпасть; иначе СТОП.
Вписать оба SHA256 в `PG_SHA256` и `POSTGIS_SHA256` и перезапустить `bash scripts/pg.sh install` → `уже установлено: postgres (PostgreSQL) 18.6`.
Если раскладка бандла PostGIS другая (нет одного каталога верхнего уровня) — поправить строку `cp` под фактическую; критерий — `ls .tools/pg/pgsql/share/extension/postgis.control` существует.

- [ ] **Step 5: Кластер и проверка**

Run: `bash scripts/pg.sh init && bash scripts/pg.sh psql -d wf -tAc "select string_agg(rolname, ',' order by rolname) from pg_roles where rolname in ('migrator','api','admin','worker'); select string_agg(extname, ',' order by extname) from pg_extension; select version(); select lower('МОСКВА');"`
Expected: `admin,api,migrator,worker`; `btree_gist,citext,plpgsql,postgis,unaccent`; строка с `PostgreSQL 18.6`; `москва` (кириллица в нижнем регистре — `normalize_text()` из миграций на это опирается).
Затем `bash scripts/pg.sh stop && bash scripts/pg.sh status` → `no server running`; `bash scripts/pg.sh start` → `запущен`.
Если порт 15432 занят — задать `WF_PG_PORT` в окружении и повторить; в отчёт — какой порт выбран.

- [ ] **Step 6: Коммит**

```bash
git add scripts/pg.sh deploy/dev && git commit -m "Локальный PostgreSQL 18 + PostGIS без Docker, роли" -- scripts/pg.sh deploy/dev
```

---

## Фаза 1. Бэкенд

### Task 4: Go-модуль, миграции в backend/, тестовая база и тест наката на PG18

**Files:**
- Create: `backend/go.mod`, `backend/migrations/embed.go`, `backend/internal/platform/testkit/dbtest/dbtest.go`, `backend/internal/platform/testkit/dbtest/dbtest_test.go`, `backend/internal/platform/migrate/migrate.go`, `backend/internal/platform/migrate/migrate_test.go`
- Move: `migrations/*` → `backend/migrations/` (`git mv`, SQL не менять)

**Interfaces:**
- Consumes: Postgres из Task 3.
- Produces:
  - `migrations.FS embed.FS` (пакет `wf/backend/migrations`, только `*.sql`);
  - `migrate.NewProvider(db *sql.DB) (*goose.Provider, error)`, `migrate.Up(ctx, db *sql.DB) error`;
  - `dbtest.Config() pgtestdb.Config` — из env `WF_TEST_PG_HOST` (localhost), `WF_TEST_PG_PORT` (15432), `WF_TEST_PG_USER` (postgres), `WF_TEST_PG_PASSWORD` (postgres);
  - `dbtest.New(t) *sql.DB`, `dbtest.NewPool(t) *pgxpool.Pool`, `dbtest.NewURL(t) string` — свой клон базы на каждый тест, уже мигрированный.

- [ ] **Step 1: Модуль и перенос миграций**

```bash
mkdir -p backend && git mv migrations backend/migrations
cd backend && go mod init wf/backend && go mod edit -go=1.27 -toolchain=go1.27.1 \
  && go get github.com/pressly/goose/v3@v3.28.0 github.com/jackc/pgx/v5@v5.11.0 \
     github.com/peterldowns/pgtestdb@v0.1.1 github.com/peterldowns/pgtestdb/migrators/goosemigrator@v0.1.1 && cd ..
```

```go
// backend/migrations/embed.go
// Package migrations вшивает SQL-миграции в бинарники и тесты.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 2: Тесты (падают: пакетов ещё нет)**

```go
// backend/internal/platform/testkit/dbtest/dbtest_test.go
package dbtest_test

import (
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "")
	c := dbtest.Config()
	if c.Host != "localhost" || c.Port != "15432" || c.User != "postgres" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestConfigFromEnvOverride(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "25432")
	if got := dbtest.Config().Port; got != "25432" {
		t.Fatalf("port = %q", got)
	}
}
```

```go
// backend/internal/platform/migrate/migrate_test.go
package migrate_test

import (
	"context"
	"testing"

	"wf/backend/internal/platform/migrate"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Клон уже мигрирован шаблоном: откатываем всё до нуля и накатываем снова.
// Ловит сломанные Down-секции и несовместимость с текущей версией PostgreSQL.
func TestMigrationsRoundTrip(t *testing.T) {
	ctx := context.Background()
	p, err := migrate.NewProvider(dbtest.New(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if v, _ := p.GetDBVersion(ctx); v != 0 {
		t.Fatalf("version after down = %d", v)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
```

- [ ] **Step 3: Запустить — должно упасть**

Run: `cd backend && go test ./internal/platform/... ; cd ..`
Expected: FAIL компиляции — пакеты `dbtest` и `migrate` не найдены.

- [ ] **Step 4: Реализация**

```go
// backend/internal/platform/testkit/dbtest/dbtest.go
// Package dbtest выдаёт каждому тесту свою чистую базу — клон мигрированного шаблона.
// pgtestdb мигрирует шаблон один раз на весь прогон (advisory-lock), клон ~10 мс.
package dbtest

import (
	"context"
	"database/sql"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // драйвер "pgx"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"

	"wf/backend/migrations"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Config — подключение суперпользователем: тестам нужно CREATE DATABASE.
func Config() pgtestdb.Config {
	return pgtestdb.Config{
		DriverName: "pgx",
		Host:       env("WF_TEST_PG_HOST", "localhost"),
		Port:       env("WF_TEST_PG_PORT", "15432"),
		User:       env("WF_TEST_PG_USER", "postgres"),
		Password:   env("WF_TEST_PG_PASSWORD", "postgres"),
		Database:   "postgres",
		Options:    "sslmode=disable",
		TestRole: &pgtestdb.Role{
			Username:     pgtestdb.DefaultRoleUsername,
			Password:     pgtestdb.DefaultRolePassword,
			Capabilities: "SUPERUSER",
		},
	}
}

func migrator() pgtestdb.Migrator {
	return goosemigrator.New(".", goosemigrator.WithFS(migrations.FS))
}

// requireServer падает с понятным советом, если Postgres не запущен.
func requireServer(t testing.TB, c pgtestdb.Config) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.Host, c.Port), 2*time.Second)
	if err != nil {
		t.Fatalf("Postgres недоступен на %s:%s — запусти ./task infra:up (%v)", c.Host, c.Port, err)
	}
	_ = conn.Close()
}

func New(t testing.TB) *sql.DB {
	t.Helper()
	c := Config()
	requireServer(t, c)
	return pgtestdb.New(t, c, migrator())
}

func NewURL(t testing.TB) string {
	t.Helper()
	c := Config()
	requireServer(t, c)
	return pgtestdb.Custom(t, c, migrator()).URL()
}

// NewPool — пул pgx: код на sqlc (sql_package pgx/v5) работает с ним, а не с *sql.DB.
func NewPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), NewURL(t))
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close) // закрывается раньше, чем pgtestdb удалит базу
	return pool
}
```

```go
// backend/internal/platform/migrate/migrate.go
// Package migrate накатывает вшитые миграции goose — без CLI, из любого бинарника и теста.
package migrate

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"wf/backend/migrations"
)

func NewProvider(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSessionLocker(locker),       // два инстанса не накатят одновременно
		goose.WithDisableGlobalRegistry(true), // только SQL-миграции
	)
}

func Up(ctx context.Context, db *sql.DB) error {
	p, err := NewProvider(db)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}
```

- [ ] **Step 5: Запустить — должно пройти (первая проверка миграций на PG18)**

Run: `cd backend && go mod tidy && go test ./internal/platform/... -count=1 ; cd ..`
Expected: PASS обоих пакетов.
Если `TestMigrationsRoundTrip` падает на конкретной миграции — СТОП: это находка «миграции не совместимы с PG18» (п. 17.3 спеки). Показать пользователю ошибку, не править SQL молча.
Проверка понятной ошибки: `bash scripts/pg.sh stop && (cd backend && go test ./internal/platform/migrate -count=1)` → FAIL с текстом `Postgres недоступен на localhost:15432 — запусти ./task infra:up`; затем `bash scripts/pg.sh start`.

- [ ] **Step 6: Коммит**

```bash
git add -A backend migrations
git commit -m "Go-модуль бэкенда, миграции в backend/, тестовая база и тест наката"
```

### Task 5: Миграции очереди River

**Files:**
- Create: `backend/tools/go.mod`, `backend/tools/go.sum`, `backend/migrations/0011_river_v2.sql` … `0016_river_v7.sql`
- Modify: `backend/internal/platform/migrate/migrate_test.go`

**Interfaces:**
- Consumes: `dbtest.New`, `migrate.NewProvider` (Task 4).
- Produces: таблицы River (`river_job`, `river_leader`, `river_queue`, …) после `Up`; модуль `backend/tools` с пинами `river`, `sqlc`, `oapi-codegen` для `go tool -modfile=tools/go.mod …` (cwd — `backend/`).

- [ ] **Step 1: Тест (падает: таблиц River нет)**

Дописать в `migrate_test.go`:

```go
func TestRiverSchemaPresent(t *testing.T) {
	var ok bool
	err := dbtest.New(t).QueryRow(`select to_regclass('public.river_job') is not null`).Scan(&ok)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("таблица river_job не создана миграциями")
	}
}
```

Run: `cd backend && go test ./internal/platform/migrate -run TestRiverSchemaPresent -count=1 ; cd ..`
Expected: FAIL `таблица river_job не создана миграциями`.

- [ ] **Step 2: Модуль инструментов бэкенда**

```bash
mkdir -p backend/tools && cd backend/tools && go mod init wf/backend/tools \
  && go mod edit -go=1.27 -toolchain=go1.27.1 \
  && go get -tool github.com/riverqueue/river/cmd/river@v0.47.0 \
  && go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 \
  && go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 && cd ../..
```

- [ ] **Step 3: Сгенерировать файлы миграций — по одному на версию River**

Версия 1 (таблица `river_migration` собственного мигратора River) не нужна: схемой управляет goose.
В версии 004 есть `ALTER TYPE … ADD VALUE`, а 006 использует новое значение — в одной транзакции так нельзя, поэтому каждая версия — отдельный файл goose (отдельная транзакция).

```bash
cd backend
for v in 2 3 4 5 6 7; do
  n=$(printf "%04d" $((v + 9)))
  f="migrations/${n}_river_v${v}.sql"
  {
    echo "-- ${n}_river_v${v}.sql"
    echo "-- Схема очереди River, версия ${v}. Не править руками — сгенерировано:"
    echo "-- go tool -modfile=tools/go.mod river migrate-get --version ${v} --up|--down"
    echo ""
    echo "-- +goose Up"
    echo "-- +goose StatementBegin"
    go tool -modfile=tools/go.mod river migrate-get --version "$v" --up
    echo "-- +goose StatementEnd"
    echo ""
    echo "-- +goose Down"
    echo "-- +goose StatementBegin"
    go tool -modfile=tools/go.mod river migrate-get --version "$v" --down
    echo "-- +goose StatementEnd"
  } > "$f"
done
cd ..
```

- [ ] **Step 4: Запустить все тесты миграций**

Run: `cd backend && go test ./internal/platform/migrate -count=1 ; cd ..`
Expected: PASS `TestRiverSchemaPresent` и `TestMigrationsRoundTrip`.
Если падает с `cannot insert multiple commands into a prepared statement` — драйвер не принял пакет команд одним Exec. Тогда убрать внешние `StatementBegin/End` и обернуть ими только блоки с `$$` (функции), остальные операторы goose разрежет по `;`. Перезапустить тест.

- [ ] **Step 5: Коммит**

```bash
git add backend/tools backend/migrations backend/internal/platform/migrate
git commit -m "Миграции очереди River через goose"
```

### Task 6: sqlc и первый запрос — фича-флаги

**Files:**
- Create: `backend/sqlc.yaml`, `backend/internal/platform/flags/queries/flags.sql`, `backend/internal/platform/flags/flags.go`, `backend/internal/platform/flags/flags_test.go`
- Generated: `backend/internal/platform/flags/flagsdb/*.go`

**Interfaces:**
- Consumes: `dbtest.NewPool` (Task 4), таблица `feature_flags` из `0001`, сид из `0010`.
- Produces: `flags.NewStore(db flagsdb.DBTX) *flags.Store`; `(*Store).EnabledGlobally(ctx, key string) (bool, error)`; `flags.ErrUnknownFlag`.

- [ ] **Step 1: Тест (падает: пакета нет)**

```go
// backend/internal/platform/flags/flags_test.go
package flags_test

import (
	"context"
	"errors"
	"testing"

	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Сид 0010 заводит флаги выключенными: «чего сейчас НЕ делаем» из CLAUDE.md.
func TestSeededFlagIsOff(t *testing.T) {
	on, err := flags.NewStore(dbtest.NewPool(t)).EnabledGlobally(context.Background(), "subscriptions")
	if err != nil {
		t.Fatal(err)
	}
	if on {
		t.Fatal("subscriptions должен быть выключен")
	}
}

func TestUnknownFlag(t *testing.T) {
	_, err := flags.NewStore(dbtest.NewPool(t)).EnabledGlobally(context.Background(), "no_such_flag")
	if !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("err = %v, ждали ErrUnknownFlag", err)
	}
}
```

Run: `cd backend && go test ./internal/platform/flags -count=1 ; cd ..`
Expected: FAIL компиляции — пакет `flags` не найден.

- [ ] **Step 2: Конфиг sqlc и запрос**

```yaml
# backend/sqlc.yaml
# sqlc разбирает грамматику PostgreSQL 17: синтаксис PG18 в миграциях и запросах не используем.
# Новый модуль = новый элемент списка sql со своими queries/ и out.
version: "2"
sql:
  - engine: postgresql
    schema: migrations/
    queries: internal/platform/flags/queries/
    gen:
      go:
        package: flagsdb
        out: internal/platform/flags/flagsdb
        sql_package: pgx/v5
        emit_interface: true
        emit_pointers_for_null_types: true
        overrides:
          - db_type: geography
            go_type: string
          - db_type: geography
            nullable: true
            go_type: { type: string, pointer: true }
```

```sql
-- backend/internal/platform/flags/queries/flags.sql
-- name: GetFlag :one
SELECT key, enabled_globally FROM feature_flags WHERE key = $1;
```

Run: `cd backend && go tool -modfile=tools/go.mod sqlc generate ; cd ..`
Expected: без ошибок, создан `internal/platform/flags/flagsdb/` (`db.go`, `models.go`, `querier.go`, `flags.sql.go`). Первый запуск ~20 с (сборка wasm-парсера).
Если sqlc не разбирает схему (п. 17.4 спеки) — СТОП, показать ошибку пользователю.

- [ ] **Step 3: Реализация**

```go
// backend/internal/platform/flags/flags.go
// Package flags читает фича-флаги. Модули зависят от Store, а не от SQL.
package flags

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/flags/flagsdb"
)

var ErrUnknownFlag = errors.New("flags: неизвестный флаг")

type Store struct{ q *flagsdb.Queries }

func NewStore(db flagsdb.DBTX) *Store { return &Store{q: flagsdb.New(db)} }

func (s *Store) EnabledGlobally(ctx context.Context, key string) (bool, error) {
	row, err := s.q.GetFlag(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUnknownFlag
	}
	if err != nil {
		return false, err
	}
	return row.EnabledGlobally, nil
}
```

- [ ] **Step 4: Запустить — PASS**

Run: `cd backend && go mod tidy && go test ./internal/platform/flags -count=1 ; cd ..`
Expected: PASS обоих тестов.

- [ ] **Step 5: Коммит**

```bash
git add backend/sqlc.yaml backend/internal/platform/flags backend/go.mod backend/go.sum
git commit -m "sqlc и первый запрос: фича-флаги"
```

### Task 7: Платформа: конфиг, логи, пул БД

**Files:**
- Create: `backend/internal/platform/config/config.go`, `config_test.go`; `backend/internal/platform/logx/logx.go`, `logx_test.go`; `backend/internal/platform/db/db.go`, `db_test.go`

**Interfaces:**
- Consumes: `dbtest.NewURL` (Task 4).
- Produces:
  - типы `config.Log{Level slog.Level; Format string}`, `config.HTTP{Addr string; ShutdownTimeout time.Duration}`, `config.DB{URL string; MaxConns int32}`;
  - конфиги бинарников `config.API{Log; HTTP; DB}`, `config.Admin{Log; HTTP; DB}`, `config.Worker{Log; DB; MaxWorkers int}`, `config.Migrator{Log; DB}`;
  - `config.Load[T any](prefix string, environ []string) (T, error)` — префиксы `API_`, `ADMIN_`, `WORKER_`, `MIGRATOR_`;
  - `logx.New(w io.Writer, c config.Log) *slog.Logger`;
  - `db.Open(ctx, c config.DB) (*pgxpool.Pool, error)` — с Ping: недоступная база = ошибка на старте.

- [ ] **Step 1: Тесты (падают: пакетов нет)**

```go
// backend/internal/platform/config/config_test.go
package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/config"
)

func TestLoadAPIWithDefaults(t *testing.T) {
	c, err := config.Load[config.API]("API_", []string{
		"API_HTTP_ADDR=:8080",
		"API_DATABASE_URL=postgres://api@localhost/wf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.Addr != ":8080" || c.HTTP.ShutdownTimeout != 10*time.Second {
		t.Fatalf("http: %+v", c.HTTP)
	}
	if c.DB.MaxConns != 10 || c.Log.Level != slog.LevelInfo || c.Log.Format != "json" {
		t.Fatalf("defaults: %+v", c)
	}
}

// Бинарник без обязательной переменной не должен стартовать молча.
func TestLoadFailsOnMissingRequiredAndNamesIt(t *testing.T) {
	_, err := config.Load[config.API]("API_", []string{"API_HTTP_ADDR=:8080"})
	if err == nil || !strings.Contains(err.Error(), "API_DATABASE_URL") {
		t.Fatalf("err = %v, ждали упоминание API_DATABASE_URL", err)
	}
}

func TestLoadParsesLogLevel(t *testing.T) {
	c, err := config.Load[config.Migrator]("MIGRATOR_", []string{
		"MIGRATOR_DATABASE_URL=postgres://m@localhost/wf",
		"MIGRATOR_LOG_LEVEL=debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Log.Level != slog.LevelDebug {
		t.Fatalf("level = %v", c.Log.Level)
	}
}
```

```go
// backend/internal/platform/logx/logx_test.go
package logx_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/logx"
)

func TestJSONAndLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	log := logx.New(&buf, config.Log{Level: slog.LevelInfo, Format: "json"})
	log.Debug("скрыто")
	log.Info("видно", "k", 1)
	out := buf.String()
	if strings.Contains(out, "скрыто") || !strings.Contains(out, `"msg":"видно"`) {
		t.Fatalf("out = %s", out)
	}
}
```

```go
// backend/internal/platform/db/db_test.go
package db_test

import (
	"context"
	"testing"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestOpenPingsDatabase(t *testing.T) {
	pool, err := db.Open(context.Background(), config.DB{URL: dbtest.NewURL(t), MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if got := pool.Config().MaxConns; got != 2 {
		t.Fatalf("MaxConns = %d", got)
	}
}

func TestOpenFailsFastOnUnreachableDB(t *testing.T) {
	_, err := db.Open(context.Background(), config.DB{
		URL: "postgres://x:x@127.0.0.1:1/wf?sslmode=disable&connect_timeout=1", MaxConns: 1,
	})
	if err == nil {
		t.Fatal("ждали ошибку подключения")
	}
}
```

Run: `cd backend && go test ./internal/platform/config ./internal/platform/logx ./internal/platform/db ; cd ..`
Expected: FAIL компиляции — пакетов нет.

- [ ] **Step 2: Реализация**

Run: `cd backend && go get github.com/caarlos0/env/v11@v11.4.1 ; cd ..`

```go
// backend/internal/platform/config/config.go
// Package config — типизированный конфиг бинарников из окружения.
// Каждый бинарник читает только свой префикс и получает только свои настройки.
package config

import (
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
)

type Log struct {
	Level  slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	Format string     `env:"LOG_FORMAT" envDefault:"json"` // json | text
}

type HTTP struct {
	Addr            string        `env:"HTTP_ADDR,required"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
}

type DB struct {
	URL      string `env:"DATABASE_URL,required"`
	MaxConns int32  `env:"DB_MAX_CONNS" envDefault:"10"`
}

type API struct {
	Log  Log
	HTTP HTTP
	DB   DB
}

// Admin — отдельный тип, хотя сейчас совпадает с API: конфиги разойдутся (2FA, allowlist).
type Admin struct {
	Log  Log
	HTTP HTTP
	DB   DB
}

type Worker struct {
	Log        Log
	DB         DB
	MaxWorkers int `env:"MAX_WORKERS" envDefault:"10"`
}

type Migrator struct {
	Log Log
	DB  DB
}

// Load разбирает окружение (os.Environ() в main, срез строк в тестах).
func Load[T any](prefix string, environ []string) (T, error) {
	var cfg T
	err := env.ParseWithOptions(&cfg, env.Options{
		Prefix:      prefix,
		Environment: env.ToMap(environ),
	})
	return cfg, err
}
```

```go
// backend/internal/platform/logx/logx.go
// Package logx — единая настройка slog для всех бинарников.
package logx

import (
	"io"
	"log/slog"

	"wf/backend/internal/platform/config"
)

func New(w io.Writer, c config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.Level}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
```

```go
// backend/internal/platform/db/db.go
// Package db открывает пул pgx по конфигу. Пинг на старте: недоступная база — ошибка сразу.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
)

func Open(ctx context.Context, c config.DB) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.URL)
	if err != nil {
		return nil, fmt.Errorf("db: разбор URL: %w", err)
	}
	pc.MaxConns = c.MaxConns
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("db: пул: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}
```

- [ ] **Step 3: Запустить — PASS**

Run: `cd backend && go mod tidy && go test ./internal/platform/config ./internal/platform/logx ./internal/platform/db -count=1 ; cd ..`
Expected: PASS. Если ошибка называет переменную без префикса — проверить в доке env v11, как `Options.Prefix` применяется к вложенным структурам, и поправить конфиг (например, тег `envPrefix` на полях). Тест не ослаблять: имя переменной в ошибке — требование.

- [ ] **Step 4: Коммит**

```bash
git add backend/internal/platform/config backend/internal/platform/logx backend/internal/platform/db backend/go.mod backend/go.sum
git commit -m "Платформа: типизированный конфиг, логи, пул БД"
```

### Task 8: Платформа HTTP: problem+json, middleware, graceful shutdown

**Files:**
- Create: `backend/internal/platform/httpx/problem.go`, `router.go`, `serve.go`, `httpx_test.go`

**Interfaces:**
- Produces:
  - `httpx.ContentTypeProblem = "application/problem+json"`;
  - `httpx.Problem{Type, Title string; Status int; Code, Detail, RequestID string}` (JSON: `type,title,status,code,detail,request_id`);
  - `httpx.WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string)` — `title` = `http.StatusText(status)`, человеческие тексты — на клиентах по `code`;
  - коды каркаса: `http.not_found`, `http.method_not_allowed`, `request.invalid`, `internal`;
  - `httpx.NewRouter(log *slog.Logger) chi.Router` — RequestID, лог запроса, recover → 500 problem, 404/405 → problem;
  - `httpx.RequestErrorHandler(w, r, err)` и `httpx.ResponseErrorHandler(log) func(w, r, err)` — для strict-сервера oapi-codegen;
  - `httpx.Serve(ctx, ln net.Listener, h http.Handler, shutdownTimeout time.Duration) error` — `nil` после отмены ctx и корректной остановки.

- [ ] **Step 1: Тесты (падают: пакета нет)**

```go
// backend/internal/platform/httpx/httpx_test.go
package httpx_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wf/backend/internal/platform/httpx"
)

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != httpx.ContentTypeProblem {
		t.Fatalf("Content-Type = %q", ct)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("тело не JSON: %v: %s", err, rec.Body.String())
	}
	return p
}

func TestUnknownRouteIsProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.NewRouter(quiet()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if p := decodeProblem(t, rec); rec.Code != 404 || p.Code != "http.not_found" || p.Status != 404 {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
}

func TestWrongMethodIsProblem(t *testing.T) {
	r := httpx.NewRouter(quiet())
	r.Get("/x", func(http.ResponseWriter, *http.Request) {})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if p := decodeProblem(t, rec); rec.Code != 405 || p.Code != "http.method_not_allowed" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
}

func TestPanicIsProblemAndServerSurvives(t *testing.T) {
	r := httpx.NewRouter(quiet())
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("бум") })
	r.Get("/ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if p := decodeProblem(t, rec); rec.Code != 500 || p.Code != "internal" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if rec.Code != 204 {
		t.Fatalf("после паники: %d", rec.Code)
	}
}

func TestProblemCarriesRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	req.Header.Set("X-Request-Id", "req-42")
	rec := httptest.NewRecorder()
	httpx.NewRouter(quiet()).ServeHTTP(rec, req)
	if p := decodeProblem(t, rec); p.RequestID != "req-42" {
		t.Fatalf("request_id = %q", p.RequestID)
	}
}

func TestServeStopsGracefullyOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- httpx.Serve(ctx, ln, http.NotFoundHandler(), time.Second) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve не остановился")
	}
}
```

Run: `cd backend && go test ./internal/platform/httpx ; cd ..`
Expected: FAIL компиляции — пакета нет.

- [ ] **Step 2: Реализация**

Run: `cd backend && go get github.com/go-chi/chi/v5@v5.3.2 ; cd ..`

```go
// backend/internal/platform/httpx/problem.go
// Package httpx — общая HTTP-платформа для api и admin-api: одна реализация на оба бинарника.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

const ContentTypeProblem = "application/problem+json"

// Problem — ответ об ошибке по RFC 9457. Клиенты переводят текст по Code;
// Title — техническая сводка (http.StatusText), не для показа пользователю.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	w.Header().Set("Content-Type", ContentTypeProblem)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Code:      code,
		Detail:    detail,
		RequestID: middleware.GetReqID(r.Context()),
	})
}

// RequestErrorHandler — невалидный запрос в strict-сервере oapi-codegen.
func RequestErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	WriteProblem(w, r, http.StatusBadRequest, "request.invalid", err.Error())
}

// ResponseErrorHandler — хендлер вернул ошибку. Детали — в лог, клиенту — только код.
func ResponseErrorHandler(log *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		log.ErrorContext(r.Context(), "handler error", "err", err, "request_id", middleware.GetReqID(r.Context()))
		WriteProblem(w, r, http.StatusInternalServerError, "internal", "")
	}
}
```

```go
// backend/internal/platform/httpx/router.go
package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter — роутер с общим набором middleware. Бинарники только монтируют свои маршруты.
func NewRouter(log *slog.Logger) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, accessLog(log), recoverer(log))
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, http.StatusNotFound, "http.not_found", "")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, http.StatusMethodNotAllowed, "http.method_not_allowed", "")
	})
	return r
}

func accessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.InfoContext(r.Context(), "http",
				"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()))
		})
	}
}

func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // так сигналит net/http
						panic(v)
					}
					log.ErrorContext(r.Context(), "panic", "value", v, "stack", string(debug.Stack()))
					WriteProblem(w, r, http.StatusInternalServerError, "internal", "")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
```

```go
// backend/internal/platform/httpx/serve.go
package httpx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Serve обслуживает ln до отмены ctx, затем даёт запросам доработать shutdownTimeout.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, shutdownTimeout time.Duration) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			return err
		}
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
```

- [ ] **Step 3: Запустить — PASS**

Run: `cd backend && go mod tidy && go test ./internal/platform/httpx -count=1 ; cd ..`
Expected: PASS всех пяти тестов.

- [ ] **Step 4: Коммит**

```bash
git add backend/internal/platform/httpx backend/go.mod backend/go.sum
git commit -m "HTTP-платформа: problem+json, middleware, graceful shutdown"
```

### Task 9: Контракты, кодогенерация Go и health по контракту

**Files:**
- Create: `contracts/openapi/public.yaml`, `contracts/openapi/admin.yaml`
- Create: `backend/internal/httpapi/public/oapi-codegen.yaml`, `server.go`, `handler.go`, `handler_test.go`; то же в `backend/internal/httpapi/admin/`
- Create: `backend/internal/platform/testkit/apitest/apitest.go`
- Generated: `backend/internal/httpapi/{public,admin}/api.gen.go`

**Interfaces:**
- Consumes: `httpx.NewRouter`, `httpx.RequestErrorHandler`, `httpx.ResponseErrorHandler` (Task 8).
- Produces:
  - `public.NewHandler(log *slog.Logger) http.Handler`, `admin.NewHandler(log *slog.Logger) http.Handler`;
  - `public.GetSpec() (*openapi3.T, error)`, `admin.GetSpec()` — вшитые спеки (`GetSwagger` в 2.8 устарел);
  - `apitest.New(t, load func() (*openapi3.T, error)) *apitest.Validator`; `(*Validator).Do(t, h http.Handler, req *http.Request) *httptest.ResponseRecorder` — валидирует запрос и ответ, падает при расхождении с контрактом.

- [ ] **Step 1: Контракты**

```yaml
# contracts/openapi/public.yaml
# Публичный API приложений и веба. Версии — теги contracts-vX.Y.Z.
# Схема Problem продублирована в admin.yaml намеренно (контракты версионируются
# независимо); тест в contracts/ падает, если копии разошлись.
openapi: 3.0.3
info:
  title: WF public API
  version: 0.1.0
paths:
  /v1/health:
    get:
      operationId: getHealth
      summary: Проверка живости сервиса
      responses:
        "200":
          description: Сервис жив
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Health"
        default:
          $ref: "#/components/responses/Problem"
components:
  responses:
    Problem:
      description: Ошибка в формате RFC 9457
      content:
        application/problem+json:
          schema:
            $ref: "#/components/schemas/Problem"
  schemas:
    Health:
      type: object
      required: [status]
      additionalProperties: false
      properties:
        status:
          type: string
          enum: [ok]
    Problem:
      type: object
      required: [type, title, status, code]
      properties:
        type:
          type: string
        title:
          type: string
        status:
          type: integer
        code:
          type: string
          description: Стабильный машинный код ошибки; клиенты переводят текст по нему
        detail:
          type: string
        request_id:
          type: string
```

```yaml
# contracts/openapi/admin.yaml
# API админки (admin-api). Потребитель — только apps/admin, теги не ставятся.
# Схема Problem продублирована в admin.yaml намеренно (контракты версионируются
# независимо); тест в contracts/ падает, если копии разошлись.
openapi: 3.0.3
info:
  title: WF admin API
  version: 0.1.0
paths:
  /v1/health:
    get:
      operationId: getHealth
      summary: Проверка живости сервиса
      responses:
        "200":
          description: Сервис жив
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Health"
        default:
          $ref: "#/components/responses/Problem"
components:
  responses:
    Problem:
      description: Ошибка в формате RFC 9457
      content:
        application/problem+json:
          schema:
            $ref: "#/components/schemas/Problem"
  schemas:
    Health:
      type: object
      required: [status]
      additionalProperties: false
      properties:
        status:
          type: string
          enum: [ok]
    Problem:
      type: object
      required: [type, title, status, code]
      properties:
        type:
          type: string
        title:
          type: string
        status:
          type: integer
        code:
          type: string
          description: Стабильный машинный код ошибки; клиенты переводят текст по нему
        detail:
          type: string
        request_id:
          type: string
```

- [ ] **Step 2: Тесты хендлеров (падают: пакетов нет)**

```go
// backend/internal/httpapi/public/handler_test.go
package public_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/platform/testkit/apitest"
)

func TestHealthMatchesContract(t *testing.T) {
	v := apitest.New(t, public.GetSpec)
	rec := v.Do(t, public.NewHandler(slog.New(slog.DiscardHandler)),
		httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
```

```go
// backend/internal/httpapi/admin/handler_test.go
package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/testkit/apitest"
)

func TestHealthMatchesContract(t *testing.T) {
	v := apitest.New(t, admin.GetSpec)
	rec := v.Do(t, admin.NewHandler(slog.New(slog.DiscardHandler)),
		httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
```

Run: `cd backend && go test ./internal/httpapi/... ; cd ..`
Expected: FAIL компиляции — пакетов нет.

- [ ] **Step 3: Валидатор контракта для тестов**

Run: `cd backend && go get github.com/getkin/kin-openapi@v0.149.0 ; cd ..`

```go
// backend/internal/platform/testkit/apitest/apitest.go
// Package apitest прогоняет запрос через хендлер и сверяет запрос и ответ с вшитой спекой:
// сервер не может тихо разойтись с контрактом.
package apitest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

type Validator struct{ router routers.Router }

func New(t testing.TB, load func() (*openapi3.T, error)) *Validator {
	t.Helper()
	spec, err := load()
	if err != nil {
		t.Fatalf("спека: %v", err)
	}
	spec.Servers = nil // иначе FindRoute ищет хост и префикс из servers
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("спека невалидна: %v", err)
	}
	r, err := gorillamux.NewRouter(spec)
	if err != nil {
		t.Fatalf("роутер: %v", err)
	}
	return &Validator{router: r}
}

func (v *Validator) Do(t testing.TB, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	ctx := context.Background()
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	route, params, err := v.router.FindRoute(req)
	if err != nil {
		t.Fatalf("маршрута нет в контракте: %s %s: %v", req.Method, req.URL.Path, err)
	}
	in := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	if err := openapi3filter.ValidateRequest(ctx, in); err != nil {
		t.Fatalf("запрос нарушает контракт: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	}
	out.SetBodyBytes(rec.Body.Bytes())
	if err := openapi3filter.ValidateResponse(ctx, out); err != nil {
		t.Fatalf("ответ нарушает контракт: %v\nтело: %s", err, rec.Body.String())
	}
	return rec
}
```

- [ ] **Step 4: Генерация и реализация**

```yaml
# backend/internal/httpapi/public/oapi-codegen.yaml
package: public
output: internal/httpapi/public/api.gen.go
generate:
  models: true
  chi-server: true
  strict-server: true
  embedded-spec: true
```

```yaml
# backend/internal/httpapi/admin/oapi-codegen.yaml
package: admin
output: internal/httpapi/admin/api.gen.go
generate:
  models: true
  chi-server: true
  strict-server: true
  embedded-spec: true
```

Run (из `backend/`):
```bash
cd backend
go tool -modfile=tools/go.mod oapi-codegen -config internal/httpapi/public/oapi-codegen.yaml ../contracts/openapi/public.yaml
go tool -modfile=tools/go.mod oapi-codegen -config internal/httpapi/admin/oapi-codegen.yaml ../contracts/openapi/admin.yaml
cd ..
```
Expected: созданы оба `api.gen.go`. Проверить имя константы enum: `grep -n "HealthStatus" backend/internal/httpapi/public/api.gen.go` (ожидается `Ok HealthStatus = "ok"`; если другое — использовать фактическое имя ниже).

```go
// backend/internal/httpapi/public/server.go
package public

import "context"

// Server реализует маршруты публичного API. Зависимости модулей добавляются полями.
type Server struct{}

var _ StrictServerInterface = Server{}

func (Server) GetHealth(context.Context, GetHealthRequestObject) (GetHealthResponseObject, error) {
	return GetHealth200JSONResponse{Status: Ok}, nil
}
```

```go
// backend/internal/httpapi/public/handler.go
package public

import (
	"log/slog"
	"net/http"

	"wf/backend/internal/platform/httpx"
)

// NewHandler собирает публичный API на общей HTTP-платформе.
func NewHandler(log *slog.Logger) http.Handler {
	strict := NewStrictHandlerWithOptions(Server{}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httpx.RequestErrorHandler,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	return HandlerFromMux(strict, httpx.NewRouter(log))
}
```

```go
// backend/internal/httpapi/admin/server.go
package admin

import "context"

// Server реализует маршруты API админки. Зависимости модулей добавляются полями.
type Server struct{}

var _ StrictServerInterface = Server{}

func (Server) GetHealth(context.Context, GetHealthRequestObject) (GetHealthResponseObject, error) {
	return GetHealth200JSONResponse{Status: Ok}, nil
}
```

```go
// backend/internal/httpapi/admin/handler.go
package admin

import (
	"log/slog"
	"net/http"

	"wf/backend/internal/platform/httpx"
)

// NewHandler собирает API админки на общей HTTP-платформе.
func NewHandler(log *slog.Logger) http.Handler {
	strict := NewStrictHandlerWithOptions(Server{}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httpx.RequestErrorHandler,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	return HandlerFromMux(strict, httpx.NewRouter(log))
}
```

- [ ] **Step 5: Запустить — PASS; подсадка бага**

Run: `cd backend && go mod tidy && go test ./internal/httpapi/... -count=1 ; cd ..`
Expected: PASS обоих.
Проверка стража: временно вернуть в `public/server.go` `Status: "bad"` → тест падает с `ответ нарушает контракт`; вернуть `Ok` → PASS.

- [ ] **Step 6: Коммит**

```bash
git add contracts/openapi backend/internal/httpapi backend/internal/platform/testkit/apitest backend/go.mod backend/go.sum
git commit -m "Контракты public и admin, кодогенерация Go, health по контракту"
```

### Task 10: Бинарники api, admin-api, migrate и страж границы админки

**Files:**
- Create: `backend/internal/platform/server/server.go`, `server_test.go`
- Create: `backend/cmd/api/main.go`, `main_test.go`; `backend/cmd/admin-api/main.go`, `main_test.go`; `backend/cmd/migrate/main.go`, `main_test.go`
- Create: `backend/internal/archtest/boundary_test.go`

**Interfaces:**
- Consumes: `config.Load`, `logx.New`, `db.Open` (Task 7); `httpx.Serve` (Task 8); `public.NewHandler`, `admin.NewHandler` (Task 9); `migrate.NewProvider` (Task 4); `dbtest.NewURL` (Task 4).
- Produces:
  - `server.RunHTTP(ctx, name string, c server.HTTPConfig, logOut io.Writer, build func(log *slog.Logger, pool *pgxpool.Pool) http.Handler) error` — общий жизненный цикл HTTP-бинарника; `server.HTTPConfig{Log config.Log; HTTP config.HTTP; DB config.DB}`;
  - в каждом `cmd/*`: `run(ctx, environ []string, logOut io.Writer) error` (у migrate — `run(ctx, args, environ []string, out io.Writer) error`), `main` только ловит сигналы и ставит код выхода;
  - команды migrate: `up`, `down` (один шаг), `status`, `reset` (до нуля и обратно — только dev).

- [ ] **Step 1: Тесты (падают: пакетов и бинарников нет)**

```go
// backend/internal/platform/server/server_test.go
package server_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/server"
	"wf/backend/internal/platform/testkit/dbtest"
)

// freeAddr — свободный локальный адрес для теста.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestRunHTTPServesAndStops(t *testing.T) {
	addr := freeAddr(t)
	url := dbtest.NewURL(t) // до горутины: t.Fatal нельзя звать из неё
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.RunHTTP(ctx, "test", server.HTTPConfig{
			HTTP: config.HTTP{Addr: addr, ShutdownTimeout: time.Second},
			DB:   config.DB{URL: url, MaxConns: 2},
		}, io.Discard, func(_ *slog.Logger, _ *pgxpool.Pool) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		})
	}()
	waitStatus(t, "http://"+addr+"/", 204)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunHTTP: %v", err)
	}
}

func waitStatus(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil { //nolint:noctx // тестовый опрос
			resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s не ответил %d за 10 с", url, want)
}
```

```go
// backend/cmd/api/main_test.go
package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunFailsWithoutConfig(t *testing.T) {
	err := run(context.Background(), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "API_") {
		t.Fatalf("err = %v", err)
	}
}
```

```go
// backend/cmd/admin-api/main_test.go
package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunFailsWithoutConfig(t *testing.T) {
	err := run(context.Background(), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "ADMIN_") {
		t.Fatalf("err = %v", err)
	}
}
```

```go
// backend/cmd/migrate/main_test.go
package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

func TestStatusListsMigrations(t *testing.T) {
	var out bytes.Buffer
	env := []string{"MIGRATOR_DATABASE_URL=" + dbtest.NewURL(t)}
	if err := run(context.Background(), []string{"status"}, env, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0001_platform.sql") {
		t.Fatalf("status без 0001: %s", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	env := []string{"MIGRATOR_DATABASE_URL=postgres://x@127.0.0.1:1/wf"}
	err := run(context.Background(), []string{"drop-everything"}, env, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "up | down | status | reset") {
		t.Fatalf("err = %v", err)
	}
}
```

```go
// backend/internal/archtest/boundary_test.go
// Package archtest — архитектурные стражи, которые не выразить линтером.
package archtest

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// Бинарник публичного API не должен даже транзитивно тянуть код админки.
// depguard видит только прямые импорты — этот тест ловит и транзитивные.
func TestPublicAPIDoesNotDependOnAdmin(t *testing.T) {
	forbidden := []string{"wf/backend/internal/httpapi/admin", "wf/backend/cmd/admin-api"}
	for _, goos := range []string{"linux", "windows"} {
		cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./cmd/api")
		cmd.Dir = "../.." // корень модуля backend
		cmd.Env = append(cmd.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				t.Fatalf("go list (%s): %v\n%s", goos, err, ee.Stderr)
			}
			t.Fatal(err)
		}
		for _, pkg := range strings.Fields(string(out)) {
			for _, f := range forbidden {
				if pkg == f || strings.HasPrefix(pkg, f+"/") {
					t.Errorf("GOOS=%s: cmd/api зависит от %s", goos, pkg)
				}
			}
		}
	}
}
```

Run: `cd backend && go test ./internal/platform/server ./cmd/... ./internal/archtest ; cd ..`
Expected: FAIL — пакетов `server`, `cmd/*` нет (`go list` в стражe тоже падает: нет `./cmd/api`).

- [ ] **Step 2: Общий жизненный цикл HTTP-бинарника**

```go
// backend/internal/platform/server/server.go
// Package server — общий жизненный цикл HTTP-бинарников: api и admin-api отличаются
// только префиксом конфига и хендлером.
package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/logx"
)

type HTTPConfig struct {
	Log  config.Log
	HTTP config.HTTP
	DB   config.DB
}

func RunHTTP(ctx context.Context, name string, c HTTPConfig, logOut io.Writer,
	build func(log *slog.Logger, pool *pgxpool.Pool) http.Handler) error {
	log := logx.New(logOut, c.Log).With("service", name)
	pool, err := db.Open(ctx, c.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	ln, err := net.Listen("tcp", c.HTTP.Addr)
	if err != nil {
		return err
	}
	log.Info("слушаю", "addr", ln.Addr().String())
	return httpx.Serve(ctx, ln, build(log, pool), c.HTTP.ShutdownTimeout)
}
```

- [ ] **Step 3: Бинарники**

```go
// backend/cmd/api/main.go
// Command api — публичный HTTP API для веба и приложений. Ничего админского сюда не линкуется.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Environ(), os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "api:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, environ []string, logOut io.Writer) error {
	cfg, err := config.Load[config.API]("API_", environ)
	if err != nil {
		return err
	}
	return server.RunHTTP(ctx, "api", server.HTTPConfig(cfg), logOut,
		func(log *slog.Logger, _ *pgxpool.Pool) http.Handler { return public.NewHandler(log) })
}
```

```go
// backend/cmd/admin-api/main.go
// Command admin-api — API админки. Отдельный бинарник, домен и порт.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Environ(), os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "admin-api:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, environ []string, logOut io.Writer) error {
	cfg, err := config.Load[config.Admin]("ADMIN_", environ)
	if err != nil {
		return err
	}
	return server.RunHTTP(ctx, "admin-api", server.HTTPConfig(cfg), logOut,
		func(log *slog.Logger, _ *pgxpool.Pool) http.Handler { return admin.NewHandler(log) })
}
```
Конверсия `server.HTTPConfig(cfg)` работает, пока поля `config.API`/`config.Admin` совпадают с `HTTPConfig` по именам и типам — при расхождении заменить на явный литерал.

```go
// backend/cmd/migrate/main.go
// Command migrate — миграции goose из вшитой FS: up | down | status | reset.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/migrate"
)

const usage = "up | down | status | reset"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Environ(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args, environ []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("нужна одна команда: %s", usage)
	}
	switch args[0] {
	case "up", "down", "status", "reset":
	default:
		return fmt.Errorf("неизвестная команда %q: %s", args[0], usage)
	}
	cfg, err := config.Load[config.Migrator]("MIGRATOR_", environ)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", cfg.DB.URL)
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := migrate.NewProvider(db)
	if err != nil {
		return err
	}
	switch args[0] {
	case "up":
		res, err := p.Up(ctx)
		for _, r := range res {
			fmt.Fprintln(out, r)
		}
		return err
	case "down":
		r, err := p.Down(ctx)
		if r != nil {
			fmt.Fprintln(out, r)
		}
		return err
	case "reset":
		if _, err := p.DownTo(ctx, 0); err != nil {
			return err
		}
		_, err := p.Up(ctx)
		return err
	default: // status
		st, err := p.Status(ctx)
		for _, s := range st {
			fmt.Fprintf(out, "%-10s %s\n", s.State, s.Source.Path)
		}
		return err
	}
}
```

- [ ] **Step 4: Запустить — PASS; подсадка бага в страже**

Run: `cd backend && go mod tidy && go test ./internal/platform/server ./cmd/... ./internal/archtest -count=1 ; cd ..`
Expected: PASS.
Проверка стража: временно добавить в `cmd/api/main.go` импорт `_ "wf/backend/internal/httpapi/admin"` → `TestPublicAPIDoesNotDependOnAdmin` падает с `cmd/api зависит от wf/backend/internal/httpapi/admin`; убрать → PASS.

- [ ] **Step 5: Коммит**

```bash
git add backend/cmd backend/internal/platform/server backend/internal/archtest backend/go.mod backend/go.sum
git commit -m "Бинарники api, admin-api, migrate и страж границы админки"
```

### Task 11: Очередь и worker

**Files:**
- Create: `backend/internal/platform/queue/queue.go`, `queue_test.go`; `backend/cmd/worker/main.go`, `main_test.go`

**Interfaces:**
- Consumes: `dbtest.NewPool` (Task 4), `config.Worker`, `logx.New`, `db.Open` (Task 7), таблицы River (Task 5).
- Produces:
  - `queue.PingArgs` (`Kind() == "platform.ping"`) — задача проверки живости очереди для мониторинга;
  - `queue.NewWorkers() *river.Workers` — реестр воркеров, модули добавляют свои;
  - `queue.NewClient(pool *pgxpool.Pool, workers *river.Workers, maxWorkers int, log *slog.Logger) (*river.Client[pgx.Tx], error)`;
  - `cmd/worker`: `run(ctx, environ []string, logOut io.Writer) error`.

- [ ] **Step 1: Тесты (падают: пакетов нет)**

```go
// backend/internal/platform/queue/queue_test.go
package queue_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Сквозная проверка: задача ставится в очередь в Postgres и выполняется воркером.
func TestPingJobIsProcessed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := dbtest.NewPool(t)
	client, err := queue.NewClient(pool, queue.NewWorkers(), 2, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := client.Subscribe(river.EventKindJobCompleted)
	defer unsubscribe()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Stop(context.Background()) }) // до закрытия пула
	if _, err := client.Insert(ctx, queue.PingArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Job.Kind != "platform.ping" {
			t.Fatalf("kind = %s", ev.Job.Kind)
		}
	case <-ctx.Done():
		t.Fatal("задача не выполнена за 15 с")
	}
}
```

```go
// backend/cmd/worker/main_test.go
package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunFailsWithoutConfig(t *testing.T) {
	err := run(context.Background(), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "WORKER_DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
}
```

Run: `cd backend && go test ./internal/platform/queue ./cmd/worker ; cd ..`
Expected: FAIL компиляции — пакетов нет.

- [ ] **Step 2: Реализация**

Run: `cd backend && go get github.com/riverqueue/river@v0.47.0 github.com/riverqueue/river/riverdriver/riverpgxv5@v0.47.0 ; cd ..`

```go
// backend/internal/platform/queue/queue.go
// Package queue — фоновые задачи на River (очередь в Postgres).
// Worker масштабируется отдельно от API: инстансов сколько угодно, задачи не задвоятся.
package queue

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// PingArgs — проверка живости очереди: мониторинг ставит задачу и ждёт её выполнения.
type PingArgs struct{}

func (PingArgs) Kind() string { return "platform.ping" }

type PingWorker struct{ river.WorkerDefaults[PingArgs] }

func (*PingWorker) Work(context.Context, *river.Job[PingArgs]) error { return nil }

// NewWorkers — реестр воркеров платформы. Модули регистрируют свои в этом же реестре.
func NewWorkers() *river.Workers {
	w := river.NewWorkers()
	river.AddWorker(w, &PingWorker{})
	return w
}

func NewClient(pool *pgxpool.Pool, workers *river.Workers, maxWorkers int, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: maxWorkers}},
		Workers: workers,
		Logger:  log,
	})
}
```

```go
// backend/cmd/worker/main.go
// Command worker — фоновые задачи: пуши, пересчёты, outbox.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/logx"
	"wf/backend/internal/platform/queue"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Environ(), os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, environ []string, logOut io.Writer) error {
	cfg, err := config.Load[config.Worker]("WORKER_", environ)
	if err != nil {
		return err
	}
	log := logx.New(logOut, cfg.Log).With("service", "worker")
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	client, err := queue.NewClient(pool, queue.NewWorkers(), cfg.MaxWorkers, log)
	if err != nil {
		return err
	}
	if err := client.Start(ctx); err != nil {
		return err
	}
	log.Info("очередь запущена", "max_workers", cfg.MaxWorkers)
	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.Stop(stopCtx)
}
```

- [ ] **Step 3: Запустить — PASS**

Run: `cd backend && go mod tidy && go test ./internal/platform/queue ./cmd/worker -count=1 ; cd ..`
Expected: PASS.

- [ ] **Step 4: Коммит**

```bash
git add backend/internal/platform/queue backend/cmd/worker backend/go.mod backend/go.sum
git commit -m "Очередь River и worker"
```

### Task 12: Линтер, Taskfile бэкенда, .env.example, проверка кодогенерации

**Files:**
- Create: `backend/.golangci.yml`, `backend/Taskfile.yml`, `backend/.env.example`
- Modify: `.gitignore` (`backend/.env` уже покрыт правилом `.env`)

**Interfaces:**
- Consumes: всё из Task 4–11; `.tools/bin/golangci-lint` (Task 2).
- Produces: цели `backend:gen`, `backend:gen:check`, `backend:lint`, `backend:test`, `backend:ci`, `backend:db:migrate`, `backend:db:status`, `backend:db:reset`, `backend:dev:api`, `backend:dev:admin`, `backend:dev:worker` (через корневой Taskfile — Task 18; до него — `./task -t backend/Taskfile.yml <цель>`).

- [ ] **Step 1: Конфиги**

```yaml
# backend/.golangci.yml
version: "2"
run:
  relative-path-mode: cfg
linters:
  default: standard
  enable: [bodyclose, depguard, errorlint, gosec, misspell, nilerr, noctx, revive, sqlclosecheck, unconvert, usestdlibvars]
  settings:
    depguard:
      rules:
        public-api-no-admin:
          list-mode: lax
          # глоб "**/cmd/api/**" — проверено: "**/cmd/api/**/*.go" не матчит файлы прямо в cmd/api
          files: ["**/cmd/api/**", "**/internal/httpapi/public/**"]
          deny:
            - pkg: wf/backend/internal/httpapi/admin
              desc: публичный API не зависит от админки
  exclusions:
    generated: lax
    presets: [comments, std-error-handling]
formatters:
  enable: [gofmt, goimports]
  settings:
    goimports:
      local-prefixes: [wf/backend]
```

```yaml
# backend/Taskfile.yml
# Цели бэкенда. Вызываются из корня как backend:<цель>; dotenv (backend/.env) грузит корневой Taskfile.
version: '3'

vars:
  TOOLS: '{{.ROOT_DIR}}/.tools/bin'
  GOTOOL: go tool -modfile=tools/go.mod

tasks:
  gen:
    desc: Кодогенерация бэкенда (sqlc, oapi-codegen)
    cmds:
      - '{{.GOTOOL}} sqlc generate'
      - '{{.GOTOOL}} oapi-codegen -config internal/httpapi/public/oapi-codegen.yaml ../contracts/openapi/public.yaml'
      - '{{.GOTOOL}} oapi-codegen -config internal/httpapi/admin/oapi-codegen.yaml ../contracts/openapi/admin.yaml'

  gen:check:
    desc: Сгенерированный код совпадает с контрактами и SQL
    cmds:
      - task: gen
      - git diff --exit-code -- . ../contracts

  lint:
    desc: golangci-lint
    cmds:
      - '{{.TOOLS}}/golangci-lint run ./...'

  test:
    desc: Все тесты бэкенда (нужен Postgres — ./task infra:up). Цикл TDD — ./task backend:test --watch
    sources: ['**/*.go', 'migrations/*.sql', 'sqlc.yaml']
    cmds:
      - go test ./... -count=1

  ci:
    desc: Всё, что проверяет CI для бэкенда
    cmds:
      - task: gen:check
      - task: lint
      - task: test

  db:migrate: { desc: Накатить миграции, cmds: ['go run ./cmd/migrate up'] }
  db:status: { desc: Статус миграций, cmds: ['go run ./cmd/migrate status'] }
  db:reset: { desc: Откатить всё и накатить заново (только dev), cmds: ['go run ./cmd/migrate reset'] }

  dev:api: { desc: Публичный API, cmds: ['go run ./cmd/api'] }
  dev:admin: { desc: API админки, cmds: ['go run ./cmd/admin-api'] }
  dev:worker: { desc: Worker, cmds: ['go run ./cmd/worker'] }
```

```ini
# backend/.env.example — скопировать в backend/.env. Только dev: пароли совпадают с deploy/dev/initdb.
API_HTTP_ADDR=127.0.0.1:8080
API_DATABASE_URL=postgres://api:api@localhost:15432/wf?sslmode=disable
API_LOG_FORMAT=text

ADMIN_HTTP_ADDR=127.0.0.1:8081
ADMIN_DATABASE_URL=postgres://admin:admin@localhost:15432/wf?sslmode=disable
ADMIN_LOG_FORMAT=text

WORKER_DATABASE_URL=postgres://worker:worker@localhost:15432/wf?sslmode=disable
WORKER_LOG_FORMAT=text

MIGRATOR_DATABASE_URL=postgres://migrator:migrator@localhost:15432/wf?sslmode=disable
```

- [ ] **Step 2: Линт — исправить найденное**

Run: `cd backend && ../.tools/bin/golangci-lint config verify && ../.tools/bin/golangci-lint run ./... ; cd ..`
Expected: `0 issues`. Каждую находку чинить в коде; `//nolint` — только с причиной в комментарии.

- [ ] **Step 3: Проверка кодогенерации и полный прогон**

Run: `cp backend/.env.example backend/.env && ./task -t backend/Taskfile.yml ci`
Expected: gen без изменений (`git diff` пуст), lint `0 issues`, все тесты PASS.

- [ ] **Step 4: Роли БД на деле: миграции ролью migrator, чтение ролью api**

Run: `set -a && . backend/.env && set +a && (cd backend && go run ./cmd/migrate up && go run ./cmd/migrate status | tail -3)`
Expected: миграции накатились ролью `migrator`, статус `applied` до `0016_river_v7.sql`.
Затем: `.tools/pg/pgsql/bin/psql "postgresql://api:api@127.0.0.1:15432/wf" -tAc "select count(*) from feature_flags"`
Expected: число > 0 (права по умолчанию сработали). Ошибка `permission denied` — находка для initdb, чинить там.

- [ ] **Step 5: Коммит**

```bash
git add backend/.golangci.yml backend/Taskfile.yml backend/.env.example
git commit -m "Линтер, Taskfile бэкенда, пример окружения"
```

---

## Фаза 2. Общее для фронтов

### Task 13: @wf/config — tsconfig, ESLint-границы, Prettier

**Files:**
- Create: `packages/config/package.json`, `packages/config/tsconfig/base.json`, `packages/config/tsconfig/react.json`, `packages/config/eslint/boundaries.js`, `packages/config/prettier.json`
- Create: `packages/config/test/boundaries.test.js`, фикстуры `packages/config/test/fixtures/apps/{web,admin}/src/*.js`
- Modify: `package.json` (корень: `prettier`, `devDependencies`)

**Interfaces:**
- Produces:
  - `@wf/config/tsconfig/base.json`, `@wf/config/tsconfig/react.json` — для `extends`;
  - `boundariesConfig(rootPath: string)` из `@wf/config/eslint` — flat-config объект: `apps/web` ↛ `apps/admin` и обратно;
  - `@wf/config/prettier` — общий конфиг Prettier.

- [ ] **Step 1: Пакет и тест (падает: конфига границ нет)**

```json
// packages/config/package.json
{
  "name": "@wf/config",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "exports": {
    "./tsconfig/base.json": "./tsconfig/base.json",
    "./tsconfig/react.json": "./tsconfig/react.json",
    "./eslint": "./eslint/boundaries.js",
    "./prettier": "./prettier.json"
  },
  "scripts": {
    "test": "vitest run"
  },
  "dependencies": {
    "eslint-plugin-boundaries": "7.2.0",
    "eslint-import-resolver-typescript": "4.4.5"
  },
  "peerDependencies": {
    "eslint": "^9.39.5"
  },
  "devDependencies": {
    "eslint": "catalog:",
    "vitest": "catalog:"
  }
}
```

Фикстуры (обычный JS, чтобы тест не зависел от TS-резолвера):

```js
// packages/config/test/fixtures/apps/admin/src/secret.js
export const secret = 1;
```
```js
// packages/config/test/fixtures/apps/web/src/local.js
export const local = 1;
```
```js
// packages/config/test/fixtures/apps/web/src/ok.js
import { local } from './local.js';
export default local;
```
```js
// packages/config/test/fixtures/apps/web/src/uses-admin.js
import { secret } from '../../admin/src/secret.js';
export default secret;
```

```js
// packages/config/test/boundaries.test.js
import { fileURLToPath } from 'node:url';
import { ESLint } from 'eslint';
import { describe, expect, test } from 'vitest';
import { boundariesConfig } from '../eslint/boundaries.js';

const root = fileURLToPath(new URL('./fixtures', import.meta.url));
const eslint = new ESLint({
  cwd: root,
  overrideConfigFile: true,
  overrideConfig: [{ files: ['**/*.js'] }, boundariesConfig(root)],
});

const ruleIds = async (file) => {
  const [result] = await eslint.lintFiles([file]);
  return result.messages.map((m) => m.ruleId);
};

describe('границы приложений', () => {
  test('веб не может импортировать админку', async () => {
    expect(await ruleIds('apps/web/src/uses-admin.js')).toContain('boundaries/dependencies');
  });
  test('свой код импортировать можно', async () => {
    expect(await ruleIds('apps/web/src/ok.js')).not.toContain('boundaries/dependencies');
  });
});
```

Run: `pnpm install && pnpm --filter @wf/config test`
Expected: FAIL — `Cannot find module '../eslint/boundaries.js'`.

- [ ] **Step 2: Реализация**

```js
// packages/config/eslint/boundaries.js
// Границы монорепо: приложения не импортируют друг друга, общее — только пакеты @wf/*.
import boundaries from 'eslint-plugin-boundaries';

/** @param {string} rootPath корень репозитория (или фикстур в тестах) */
export function boundariesConfig(rootPath) {
  return {
    plugins: { boundaries },
    settings: {
      'boundaries/root-path': rootPath,
      'boundaries/elements': [
        { type: 'web', pattern: 'apps/web' },
        { type: 'admin', pattern: 'apps/admin' },
        { type: 'package', pattern: 'packages/*' },
      ],
      'import/resolver': { typescript: { alwaysTryTypes: true } },
    },
    rules: {
      'boundaries/dependencies': [
        'error',
        {
          default: 'allow',
          policies: [
            { from: { element: { type: 'web' } }, disallow: { to: { element: { type: 'admin' } } } },
            { from: { element: { type: 'admin' } }, disallow: { to: { element: { type: 'web' } } } },
            { from: { element: { type: 'package' } }, disallow: { to: { element: { type: ['web', 'admin'] } } } },
          ],
        },
      ],
    },
  };
}
```

Если тест падает на синтаксисе правила (`policies` / имя `boundaries/dependencies`) — сверить с README eslint-plugin-boundaries 7.2.0 и привести конфиг к нему; тест не менять.

```json
// packages/config/tsconfig/base.json
{
  "$schema": "https://json.schemastore.org/tsconfig",
  "compilerOptions": {
    "target": "ES2023",
    "lib": ["ES2023"],
    "module": "esnext",
    "moduleResolution": "bundler",
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true,
    "verbatimModuleSyntax": true,
    "isolatedModules": true,
    "noUncheckedIndexedAccess": true,
    "resolveJsonModule": true,
    "types": []
  }
}
```

```json
// packages/config/tsconfig/react.json
// TS 6: types по умолчанию пустой — приложение перечисляет свои (vite/client, node, vitest/globals…).
{
  "extends": "./base.json",
  "compilerOptions": {
    "lib": ["ES2023", "DOM", "DOM.Iterable"],
    "jsx": "react-jsx"
  }
}
```

(В `react.json` комментарий допустим: tsconfig — JSONC.)

```json
// packages/config/prettier.json
{
  "singleQuote": true,
  "semi": true,
  "printWidth": 100,
  "trailingComma": "all"
}
```

Корневой `package.json` — добавить:

```json
  "prettier": "@wf/config/prettier",
  "devDependencies": {
    "@wf/config": "workspace:*",
    "prettier": "catalog:"
  }
```

- [ ] **Step 3: Запустить — PASS; подсадка бага**

Run: `pnpm install && pnpm --filter @wf/config test`
Expected: PASS обоих тестов.
Проверка стража: временно удалить первую политику (`web` → `admin`) → тест «веб не может импортировать админку» падает; вернуть → PASS.

- [ ] **Step 4: Коммит**

```bash
git add packages/config package.json pnpm-lock.yaml
git commit -m "Общий конфиг фронтов: tsconfig, границы ESLint, Prettier"
```

### Task 14: @wf/contracts и @wf/tokens — проверки контрактов и токены из макета

**Files:**
- Create: `contracts/package.json`, `contracts/test/contracts.test.mjs`
- Create: `contracts/tokens/tokens.json`
- Create: `packages/tokens/package.json`, `packages/tokens/scripts/build.mjs`, `packages/tokens/test/build.test.mjs`
- Generated: `packages/tokens/tokens.css`, `packages/tokens/tokens.ts`

**Interfaces:**
- Produces:
  - `@wf/contracts` — скрипты `gen:web`, `gen:admin` (подключаются в Task 16/17), `test`;
  - `@wf/tokens`: `@wf/tokens/tokens.css` (CSS-переменные `--wf-*`, `:root`, `color-scheme: dark`) и `@wf/tokens` (TS: `tokens`);
  - `buildCss(tokens): string`, `buildTs(tokens): string` из `packages/tokens/scripts/build.mjs`.

- [ ] **Step 1: Тест контрактов (падает: пакета и зависимостей нет)**

```js
// contracts/test/contracts.test.mjs
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import openapiTS, { astToString } from 'openapi-typescript';
import { parse } from 'yaml';

const load = async (name) =>
  parse(await readFile(new URL(`../openapi/${name}.yaml`, import.meta.url), 'utf8'));

test('схема Problem одинакова в public и admin', async () => {
  const [pub, adm] = await Promise.all([load('public'), load('admin')]);
  assert.deepEqual(adm.components.schemas.Problem, pub.components.schemas.Problem);
  assert.deepEqual(adm.components.responses.Problem, pub.components.responses.Problem);
});

for (const name of ['public', 'admin']) {
  test(`${name}.yaml генерирует TS-типы`, async () => {
    const out = astToString(await openapiTS(new URL(`../openapi/${name}.yaml`, import.meta.url)));
    assert.match(out, /"\/v1\/health"/);
  });
}
```

```json
// contracts/package.json
{
  "name": "@wf/contracts",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "scripts": {
    "gen:web": "openapi-typescript openapi/public.yaml -o ../apps/web/src/api/gen/public.d.ts",
    "gen:admin": "openapi-typescript openapi/admin.yaml -o ../apps/admin/src/api/gen/admin.d.ts",
    "test": "node --test \"test/*.test.mjs\""
  },
  "devDependencies": {
    "openapi-typescript": "7.13.0",
    "typescript": "catalog:contracts",
    "yaml": "2.9.1"
  }
}
```

Run: `pnpm install && pnpm --filter @wf/contracts test`
Expected: PASS — контракты из Task 9 уже согласованы. Проверка, что тест живой: временно поменять в `admin.yaml` у `Problem` `code` → `codes` → тест падает на `deepEqual`; вернуть.

- [ ] **Step 2: Токены из макета**

Значения сверены с артбордом «Токены» (`design/screens/12-Токены.dc.html` после Task 19) и шкалами из `design/03-токены.md`.

```json
// contracts/tokens/tokens.json
{
  "color": {
    "bg": "#0B0F0D",
    "bg-elevated": "#141A17",
    "pitch": "#16241C",
    "border": "#253329",
    "border-strong": "#3B5241",
    "text": "#EAF2EC",
    "text-secondary": "#C3D0C7",
    "text-muted": "#93A399",
    "text-subtle": "#7E8C83",
    "text-disabled": "#5E7266",
    "text-inverse": "#0B0F0D",
    "accent": "#C6F24E",
    "accent-hover": "#D9FA85",
    "success": "#4ADE80",
    "warning": "#E8B858",
    "danger": "#F97066",
    "slot-empty": "#1B241F",
    "slot-filled": "#2A3B2E"
  },
  "font": {
    "display": "'Oswald', sans-serif",
    "body": "'Manrope', system-ui, sans-serif"
  },
  "text": {
    "3xl": [32, 36],
    "2xl": [24, 30],
    "xl": [20, 28],
    "lg": [18, 26],
    "base": [16, 24],
    "sm": [14, 20],
    "xs": [12, 16]
  },
  "space": { "1": 4, "2": 8, "3": 12, "4": 16, "5": 20, "6": 24, "8": 32, "10": 40, "12": 48, "16": 64 },
  "radius": { "sm": 8, "md": 12, "button": 14, "lg": 16, "xl": 24, "full": 9999 },
  "size": { "touch-min": 44, "control": 48, "avatar-sm": 32, "avatar-md": 48, "avatar-lg": 96, "slot": 56, "bar": 8 },
  "duration": { "fast": 120, "base": 200, "slow": 400 },
  "ease": "cubic-bezier(0.2, 0, 0, 1)"
}
```

- [ ] **Step 3: Тест генератора токенов (падает: генератора нет)**

```js
// packages/tokens/test/build.test.mjs
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { buildCss, buildTs } from '../scripts/build.mjs';

const tokens = JSON.parse(
  await readFile(new URL('../../../contracts/tokens/tokens.json', import.meta.url), 'utf8'),
);
const css = buildCss(tokens);

test('цвета, шрифты, шкалы и движение — CSS-переменные', () => {
  assert.match(css, /--wf-color-accent: #C6F24E;/);
  assert.match(css, /--wf-font-display: 'Oswald', sans-serif;/);
  assert.match(css, /--wf-text-3xl: 32px;/);
  assert.match(css, /--wf-text-3xl-line: 36px;/);
  assert.match(css, /--wf-space-4: 16px;/);
  assert.match(css, /--wf-radius-button: 14px;/);
  assert.match(css, /--wf-duration-fast: 120ms;/);
  assert.match(css, /--wf-ease: cubic-bezier\(0\.2, 0, 0, 1\);/);
  assert.match(css, /color-scheme: dark;/);
});

test('закоммиченные файлы совпадают с tokens.json', async () => {
  const onDisk = (f) => readFile(new URL(`../${f}`, import.meta.url), 'utf8');
  assert.equal(await onDisk('tokens.css'), css, 'запусти pnpm --filter @wf/tokens build');
  assert.equal(await onDisk('tokens.ts'), buildTs(tokens), 'запусти pnpm --filter @wf/tokens build');
});
```

```json
// packages/tokens/package.json
{
  "name": "@wf/tokens",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "exports": {
    ".": "./tokens.ts",
    "./tokens.css": "./tokens.css"
  },
  "scripts": {
    "build": "node scripts/build.mjs",
    "test": "node --test \"test/*.test.mjs\""
  }
}
```

Run: `pnpm install && pnpm --filter @wf/tokens test`
Expected: FAIL — `Cannot find module '../scripts/build.mjs'`.

- [ ] **Step 4: Генератор**

```js
// packages/tokens/scripts/build.mjs
// tokens.json (contracts/) → tokens.css + tokens.ts. Источник один на веб, админку и позже Flutter.
import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const HEADER = 'Сгенерировано из contracts/tokens/tokens.json — не править руками.';
const px = (group, prefix) => Object.entries(group).map(([k, v]) => `--wf-${prefix}-${k}: ${v}px;`);

export function buildCss(t) {
  const vars = [
    ...Object.entries(t.color).map(([k, v]) => `--wf-color-${k}: ${v};`),
    ...Object.entries(t.font).map(([k, v]) => `--wf-font-${k}: ${v};`),
    ...Object.entries(t.text).flatMap(([k, [size, line]]) => [
      `--wf-text-${k}: ${size}px;`,
      `--wf-text-${k}-line: ${line}px;`,
    ]),
    ...px(t.space, 'space'),
    ...px(t.radius, 'radius'),
    ...px(t.size, 'size'),
    ...Object.entries(t.duration).map(([k, v]) => `--wf-duration-${k}: ${v}ms;`),
    `--wf-ease: ${t.ease};`,
  ];
  return `/* ${HEADER} */\n:root {\n  color-scheme: dark;\n${vars.map((v) => `  ${v}`).join('\n')}\n}\n`;
}

export function buildTs(t) {
  return `// ${HEADER}\nexport const tokens = ${JSON.stringify(t, null, 2)} as const;\n`;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const src = new URL('../../../contracts/tokens/tokens.json', import.meta.url);
  const tokens = JSON.parse(await readFile(src, 'utf8'));
  await writeFile(new URL('../tokens.css', import.meta.url), buildCss(tokens));
  await writeFile(new URL('../tokens.ts', import.meta.url), buildTs(tokens));
}
```

- [ ] **Step 5: Сгенерировать и прогнать**

Run: `pnpm --filter @wf/tokens build && pnpm --filter @wf/tokens test`
Expected: PASS обоих тестов.

- [ ] **Step 6: Коммит**

```bash
git add contracts/package.json contracts/test contracts/tokens packages/tokens pnpm-lock.yaml
git commit -m "Проверки контрактов и токены из макета"
```

### Task 15: @wf/i18n — fallback en→ru и сверка ключей

**Files:**
- Create: `packages/i18n/package.json`, `packages/i18n/tsconfig.json`, `packages/i18n/src/index.ts`, `packages/i18n/src/index.test.ts`

**Interfaces:**
- Produces (из `@wf/i18n`):
  - `type Messages = { [key: string]: string | Messages }`;
  - `withFallback(base: Messages, over: Messages): Messages` — значения `over`, кроме пустых строк; недостающее — из `base`;
  - `diffKeys(a: Messages, b: Messages): { missing: string[]; extra: string[] }` — пути ключей (`health.ok`), которых нет в `b` / лишние в `b`.

- [ ] **Step 1: Тесты (падают: модуля нет)**

```ts
// packages/i18n/src/index.test.ts
import { describe, expect, test } from 'vitest';
import { diffKeys, withFallback } from './index';

const ru = { health: { ok: 'API работает', down: 'API недоступен' }, title: 'Админка' };

describe('withFallback', () => {
  test('пустая строка в en показывает ru', () => {
    const en = { health: { ok: '', down: 'API is down' }, title: '' };
    expect(withFallback(ru, en)).toEqual({
      health: { ok: 'API работает', down: 'API is down' },
      title: 'Админка',
    });
  });
  test('недостающий ключ берётся из ru', () => {
    expect(withFallback(ru, { health: { ok: 'OK' } })).toEqual({
      health: { ok: 'OK', down: 'API недоступен' },
      title: 'Админка',
    });
  });
  test('base не мутируется', () => {
    withFallback(ru, { title: 'Admin' });
    expect(ru.title).toBe('Админка');
  });
});

describe('diffKeys', () => {
  test('одинаковые наборы — пусто', () => {
    expect(diffKeys(ru, { health: { ok: '', down: '' }, title: '' })).toEqual({ missing: [], extra: [] });
  });
  test('находит отсутствующие и лишние пути', () => {
    expect(diffKeys(ru, { health: { ok: '' }, title: '', extra: { x: '' } })).toEqual({
      missing: ['health.down'],
      extra: ['extra.x'],
    });
  });
});
```

```json
// packages/i18n/package.json
{
  "name": "@wf/i18n",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "exports": { ".": "./src/index.ts" },
  "scripts": {
    "test": "vitest run",
    "typecheck": "tsc -p tsconfig.json"
  },
  "devDependencies": {
    "@wf/config": "workspace:*",
    "typescript": "catalog:",
    "vitest": "catalog:"
  }
}
```

```json
// packages/i18n/tsconfig.json
{
  "extends": "@wf/config/tsconfig/base.json",
  "include": ["src"]
}
```

Run: `pnpm install && pnpm --filter @wf/i18n test`
Expected: FAIL — `Failed to resolve import "./index"`.

- [ ] **Step 2: Реализация**

```ts
// packages/i18n/src/index.ts
// Общая логика локализации веба и админки.
// use-intl считает "" найденным сообщением, поэтому наивный merge ru+en показал бы пустоту.

export type Messages = { [key: string]: string | Messages };

const isGroup = (v: unknown): v is Messages => typeof v === 'object' && v !== null;

export function withFallback(base: Messages, over: Messages): Messages {
  const out: Messages = { ...base };
  for (const [key, value] of Object.entries(over)) {
    if (typeof value === 'string') {
      if (value !== '') out[key] = value;
    } else {
      const b = base[key];
      out[key] = withFallback(isGroup(b) ? b : {}, value);
    }
  }
  return out;
}

function paths(m: Messages, prefix = ''): string[] {
  return Object.entries(m).flatMap(([k, v]) =>
    isGroup(v) ? paths(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  );
}

export function diffKeys(a: Messages, b: Messages): { missing: string[]; extra: string[] } {
  const pa = new Set(paths(a));
  const pb = new Set(paths(b));
  return {
    missing: [...pa].filter((p) => !pb.has(p)).sort(),
    extra: [...pb].filter((p) => !pa.has(p)).sort(),
  };
}
```

- [ ] **Step 3: Запустить — PASS, типы чистые**

Run: `pnpm --filter @wf/i18n test && pnpm --filter @wf/i18n typecheck`
Expected: PASS всех тестов, `tsc` без ошибок.

- [ ] **Step 4: Коммит**

```bash
git add packages/i18n pnpm-lock.yaml
git commit -m "Общая локализация: fallback en→ru и сверка ключей"
```

### Task 16: Админка — Vite + React, локали, клиент API, статус сервиса

**Files:**
- Create: `apps/admin/package.json`, `tsconfig.json`, `vite.config.ts`, `index.html`, `eslint.config.mjs`, `Taskfile.yml`, `.env.example`
- Create: `apps/admin/messages/ru.json`, `apps/admin/messages/en.json`
- Create: `apps/admin/src/main.tsx`, `src/App.tsx`, `src/api/client.ts`, `src/api/context.tsx`, `src/i18n/messages.ts`, `src/i18n/messages.test.ts`
- Create: `apps/admin/src/features/health/HealthStatus.tsx`, `HealthStatus.test.tsx`
- Create: `apps/admin/src/test/setup.ts`, `src/test/server.ts`, `src/test/render.tsx`, `src/test/jest-dom.d.ts`
- Generated: `apps/admin/src/api/gen/admin.d.ts`

**Interfaces:**
- Consumes: `@wf/contracts gen:admin` (Task 14), `@wf/tokens/tokens.css` (Task 14), `withFallback`, `diffKeys` (Task 15), `boundariesConfig` и `@wf/config/tsconfig/react.json` (Task 13), `admin-api` на `127.0.0.1:8081` (Task 10).
- Produces:
  - цель `gen:check` — сгенерированные типы совпадают с контрактом (входит в `ci`);
  - `createApi(baseUrl: string)` → клиент openapi-fetch по `paths` админского контракта; `ApiProvider`, `useApi()` — клиент через контекст (тесты подставляют свой адрес);
  - `messagesFor(locale: 'ru' | 'en')`;
  - `<HealthStatus />` — «Проверяем API…» / «API работает» / «API недоступен» из локали;
  - цели Taskfile: `gen`, `dev`, `test`, `lint`, `typecheck`, `build`, `ci`.
- Паттерн для веба (Task 17) тот же: клиент через контекст, API за прокси dev-сервера (same-origin — без CORS, куки админки не видны вебу).

- [ ] **Step 1: Пакет и типы клиента**

```json
{
  "name": "@wf/admin",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "engines": { "node": ">=26.10.0" },
  "scripts": {
    "gen": "pnpm --filter @wf/contracts gen:admin",
    "dev": "vite",
    "build": "tsc -p tsconfig.json && vite build",
    "typecheck": "tsc -p tsconfig.json",
    "lint": "eslint .",
    "test": "vitest run"
  },
  "dependencies": {
    "@tanstack/react-query": "5.103.2",
    "@wf/i18n": "workspace:*",
    "@wf/tokens": "workspace:*",
    "openapi-fetch": "0.17.0",
    "react": "catalog:",
    "react-dom": "catalog:",
    "use-intl": "4.14.7"
  },
  "devDependencies": {
    "@testing-library/dom": "10.4.2",
    "@testing-library/jest-dom": "7.0.1",
    "@testing-library/react": "16.3.3",
    "@testing-library/user-event": "14.6.7",
    "@types/react": "catalog:",
    "@types/react-dom": "catalog:",
    "@vitejs/plugin-react": "6.1.1",
    "@wf/config": "workspace:*",
    "eslint": "catalog:",
    "eslint-plugin-react-hooks": "7.1.1",
    "eslint-plugin-react-refresh": "0.5.7",
    "jsdom": "30.1.1",
    "msw": "2.15.0",
    "openapi-msw": "2.0.0",
    "typescript": "catalog:",
    "typescript-eslint": "8.70.1",
    "vite": "catalog:",
    "vitest": "catalog:"
  }
}
```
(Файл `apps/admin/package.json`.)

Run: `mkdir -p apps/admin/src/api/gen && pnpm install && pnpm --filter @wf/admin gen`
Expected: создан `apps/admin/src/api/gen/admin.d.ts` с `"/v1/health"`.

- [ ] **Step 2: Конфиги сборки и тестов**

```json
{
  "extends": "@wf/config/tsconfig/react.json",
  "compilerOptions": {
    "types": ["vite/client"]
  },
  "include": ["src"]
}
```
(Файл `apps/admin/tsconfig.json`.)

```ts
// apps/admin/vite.config.ts
/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// Dev: запросы /v1 проксируются в admin-api — same-origin, без CORS.
// В проде тот же путь отдаёт реверс-прокси (спека деплоя).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/v1': 'http://127.0.0.1:8081' },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
  },
});
```

```html
<!-- apps/admin/index.html -->
<!doctype html>
<html lang="ru">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>WF admin</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

```js
// apps/admin/eslint.config.mjs
import { fileURLToPath } from 'node:url';
import { boundariesConfig } from '@wf/config/eslint';
import { defineConfig, globalIgnores } from 'eslint/config';
import reactHooks from 'eslint-plugin-react-hooks';
import { reactRefresh } from 'eslint-plugin-react-refresh';
import tseslint from 'typescript-eslint';

const repoRoot = fileURLToPath(new URL('../..', import.meta.url));

export default defineConfig([
  globalIgnores(['dist/**', 'src/api/gen/**']),
  tseslint.configs.recommended,
  reactHooks.configs.flat.recommended,
  reactRefresh.configs.vite(),
  boundariesConfig(repoRoot),
]);
```

```ini
# apps/admin/.env.example — в сборку попадают только публичные VITE_* значения
# Пусто = тот же origin (dev-прокси Vite / реверс-прокси в проде)
VITE_ADMIN_API_URL=
```

- [ ] **Step 3: Тестовая обвязка**

```ts
// apps/admin/src/test/server.ts
import { setupServer } from 'msw/node';
import { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '../api/gen/admin';

// Моки типизированы из того же контракта, что и сервер.
export const TEST_API = 'http://admin.test';
export const http = createOpenApiHttp<paths>({ baseUrl: TEST_API });
export const server = setupServer(
  http.get('/v1/health', ({ response }) => response(200).json({ status: 'ok' })),
);
```

```ts
// apps/admin/src/test/setup.ts
import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterAll, afterEach, beforeAll } from 'vitest';
import { server } from './server';

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
});
afterAll(() => server.close());
```

```ts
// apps/admin/src/test/jest-dom.d.ts
// Vitest 5 расширяет Matchers<R, T>, а jest-dom 7.0.1 — старый Assertion<T>. Мост типов.
import 'vitest';
import type { TestingLibraryMatchers } from '@testing-library/jest-dom/matchers';

declare module 'vitest' {
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  interface Matchers<R = unknown, T = unknown> extends TestingLibraryMatchers<unknown, R> {}
}
```

```tsx
// apps/admin/src/test/render.tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import type { ReactElement } from 'react';
import { IntlProvider } from 'use-intl';
import { createApi } from '../api/client';
import { ApiProvider } from '../api/context';
import { messagesFor } from '../i18n/messages';
import { TEST_API } from './server';

export function renderWithProviders(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ApiProvider api={createApi(TEST_API)}>
      <QueryClientProvider client={queryClient}>
        <IntlProvider locale="ru" messages={messagesFor('ru')}>
          {ui}
        </IntlProvider>
      </QueryClientProvider>
    </ApiProvider>,
  );
}
```

- [ ] **Step 4: Тесты (падают: компонентов и локалей нет)**

```ts
// apps/admin/src/i18n/messages.test.ts
import { diffKeys, withFallback } from '@wf/i18n';
import { expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

test('в en те же ключи, что в ru', () => {
  expect(diffKeys(ru, en)).toEqual({ missing: [], extra: [] });
});

test('пустые строки en показываются по-русски', () => {
  expect(withFallback(ru, en)).toEqual(ru);
});
```

```tsx
// apps/admin/src/features/health/HealthStatus.test.tsx
import { screen } from '@testing-library/react';
import { HttpResponse, http as mswHttp } from 'msw';
import { expect, test } from 'vitest';
import { renderWithProviders } from '../../test/render';
import { TEST_API, server } from '../../test/server';
import { HealthStatus } from './HealthStatus';

test('API отвечает — показываем «API работает»', async () => {
  renderWithProviders(<HealthStatus />);
  expect(await screen.findByText('API работает')).toBeInTheDocument();
});

test('API вернул ошибку — показываем «API недоступен»', async () => {
  server.use(
    mswHttp.get(`${TEST_API}/v1/health`, () =>
      HttpResponse.json(
        { type: 'about:blank', title: 'Internal Server Error', status: 500, code: 'internal' },
        { status: 500, headers: { 'Content-Type': 'application/problem+json' } },
      ),
    ),
  );
  renderWithProviders(<HealthStatus />);
  expect(await screen.findByText('API недоступен')).toBeInTheDocument();
});
```

Run: `pnpm --filter @wf/admin test`
Expected: FAIL — не найдены `../../messages/ru.json`, `./HealthStatus`, `../api/client`.

- [ ] **Step 5: Реализация**

```json
{
  "app": { "title": "Администрирование" },
  "health": {
    "checking": "Проверяем API…",
    "ok": "API работает",
    "down": "API недоступен"
  }
}
```
(Файл `apps/admin/messages/ru.json`.)

```json
{
  "app": { "title": "" },
  "health": { "checking": "", "ok": "", "down": "" }
}
```
(Файл `apps/admin/messages/en.json` — ключи есть, перевода пока нет: показывается ru.)

```ts
// apps/admin/src/i18n/messages.ts
import { type Messages, withFallback } from '@wf/i18n';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

export type Locale = 'ru' | 'en';
export const defaultLocale: Locale = 'ru';

export function messagesFor(locale: Locale): Messages {
  return locale === 'en' ? withFallback(ru, en) : ru;
}
```

```ts
// apps/admin/src/api/client.ts
import createClient from 'openapi-fetch';
import type { paths } from './gen/admin';

// Клиент админского API. Существует только в apps/admin — веб его физически не видит.
export function createApi(baseUrl: string) {
  return createClient<paths>({ baseUrl });
}

export type Api = ReturnType<typeof createApi>;
```

```tsx
// apps/admin/src/api/context.tsx
import { createContext, type ReactNode, useContext } from 'react';
import type { Api } from './client';

const ApiContext = createContext<Api | null>(null);

export function ApiProvider({ api, children }: { api: Api; children: ReactNode }) {
  return <ApiContext value={api}>{children}</ApiContext>;
}

export function useApi(): Api {
  const api = useContext(ApiContext);
  if (!api) throw new Error('useApi: нет ApiProvider выше по дереву');
  return api;
}
```

```tsx
// apps/admin/src/features/health/HealthStatus.tsx
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { useApi } from '../../api/context';

export function HealthStatus() {
  const api = useApi();
  const t = useTranslations('health');
  const health = useQuery({
    queryKey: ['health'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/health');
      if (error || !data) throw new Error('health: API недоступен');
      return data;
    },
  });
  if (health.isPending) return <p role="status">{t('checking')}</p>;
  return <p role="status">{health.isError ? t('down') : t('ok')}</p>;
}
```

```tsx
// apps/admin/src/App.tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { IntlProvider, useTranslations } from 'use-intl';
import { createApi } from './api/client';
import { ApiProvider } from './api/context';
import { HealthStatus } from './features/health/HealthStatus';
import { defaultLocale, messagesFor } from './i18n/messages';

const api = createApi(import.meta.env.VITE_ADMIN_API_URL || window.location.origin);
const queryClient = new QueryClient();

function Home() {
  const t = useTranslations('app');
  return (
    <main>
      <h1>{t('title')}</h1>
      <HealthStatus />
    </main>
  );
}

export function App() {
  return (
    <ApiProvider api={api}>
      <QueryClientProvider client={queryClient}>
        <IntlProvider locale={defaultLocale} messages={messagesFor(defaultLocale)}>
          <Home />
        </IntlProvider>
      </QueryClientProvider>
    </ApiProvider>
  );
}
```

```tsx
// apps/admin/src/main.tsx
import '@wf/tokens/tokens.css';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';

const root = document.getElementById('root');
if (!root) throw new Error('нет #root в index.html');
createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
```

```yaml
# apps/admin/Taskfile.yml
version: '3'
tasks:
  gen: { desc: Типы клиента из admin.yaml, cmds: ['pnpm gen'] }
  gen:check:
    desc: Типы клиента совпадают с admin.yaml
    cmds: ['pnpm gen', 'git diff --exit-code -- src/api/gen']
  dev: { desc: Dev-сервер админки (:5173), cmds: ['pnpm dev'] }
  test: { desc: Тесты админки, cmds: ['pnpm test'] }
  lint: { cmds: ['pnpm lint'] }
  typecheck: { cmds: ['pnpm typecheck'] }
  build: { cmds: ['pnpm build'] }
  ci:
    desc: Всё, что проверяет CI для админки
    cmds:
      - task: gen:check
      - task: typecheck
      - task: lint
      - task: test
      - task: build
```

- [ ] **Step 6: Запустить — PASS; типы, линт, сборка**

Run: `pnpm --filter @wf/admin test && pnpm --filter @wf/admin typecheck && pnpm --filter @wf/admin lint && pnpm --filter @wf/admin build`
Expected: 4 теста PASS; `tsc` без ошибок (если мост типов jest-dom не сработал — `toBeInTheDocument` не найден: поправить `jest-dom.d.ts` по migration guide Vitest 5, не отключая проверку типов); ESLint чистый; `dist/` собран.
Проверка стража локалей: временно удалить `health.down` из `en.json` → тест `в en те же ключи` падает с `missing: ['health.down']`; вернуть.

- [ ] **Step 7: Коммит**

```bash
git add apps/admin pnpm-lock.yaml
git commit -m "Админка: Vite + React, локали, клиент API, статус сервиса"
```

### Task 17: Веб — Next.js, next-intl, статус сервиса, smoke-тест

**Files:**
- Create: `apps/web/package.json`, `tsconfig.json`, `next.config.ts`, `vitest.config.mts`, `playwright.config.ts`, `eslint.config.mjs`, `Taskfile.yml`, `.env.example`
- Create: `apps/web/messages/ru.json`, `apps/web/messages/en.json`
- Create: `apps/web/src/i18n/request.ts`, `src/i18n/messages.ts`, `src/i18n/messages.test.ts`
- Create: `apps/web/src/app/layout.tsx`, `src/app/page.tsx`, `src/app/providers.tsx`
- Create: `apps/web/src/api/client.ts`, `src/api/context.tsx`, `src/features/health/HealthStatus.tsx`, `HealthStatus.test.tsx`
- Create: `apps/web/src/test/setup.ts`, `src/test/server.ts`, `src/test/render.tsx`, `src/test/jest-dom.d.ts`
- Create: `apps/web/e2e/smoke.spec.ts`
- Generated: `apps/web/src/api/gen/public.d.ts`, `apps/web/next-env.d.ts`

**Interfaces:**
- Consumes: `@wf/contracts gen:web`, `@wf/tokens`, `@wf/i18n`, `@wf/config`; `api` на `127.0.0.1:8080` (Task 10).
- Produces: `createApi(baseUrl)`, `ApiProvider`, `useApi()`, `messagesFor(locale)`, `<HealthStatus />` — тот же паттерн, что в админке, но на next-intl; Next проксирует `/v1/*` в `WF_API_ORIGIN`; цели Taskfile `gen`, `dev`, `test`, `e2e`, `lint`, `typecheck`, `build`, `ci`.

- [ ] **Step 1: Пакет и типы клиента**

```json
{
  "name": "@wf/web",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "engines": { "node": ">=26.10.0" },
  "scripts": {
    "gen": "pnpm --filter @wf/contracts gen:web",
    "dev": "next dev --port 3000",
    "build": "next build",
    "start": "next start --port 3000",
    "typecheck": "tsc -p tsconfig.json --noEmit",
    "lint": "eslint .",
    "test": "vitest run",
    "e2e": "playwright test"
  },
  "dependencies": {
    "@tanstack/react-query": "5.103.2",
    "@wf/i18n": "workspace:*",
    "@wf/tokens": "workspace:*",
    "next": "16.3.6",
    "next-intl": "4.14.7",
    "openapi-fetch": "0.17.0",
    "react": "catalog:",
    "react-dom": "catalog:"
  },
  "devDependencies": {
    "@playwright/test": "1.63.0",
    "@testing-library/dom": "10.4.2",
    "@testing-library/jest-dom": "7.0.1",
    "@testing-library/react": "16.3.3",
    "@types/node": "catalog:",
    "@types/react": "catalog:",
    "@types/react-dom": "catalog:",
    "@vitejs/plugin-react": "6.1.1",
    "@wf/config": "workspace:*",
    "eslint": "catalog:",
    "eslint-config-next": "16.3.6",
    "jsdom": "30.1.1",
    "msw": "2.15.0",
    "openapi-msw": "2.0.0",
    "typescript": "catalog:",
    "vite": "catalog:",
    "vitest": "catalog:"
  }
}
```
(Файл `apps/web/package.json`.)

Run: `mkdir -p apps/web/src/api/gen && pnpm install && pnpm --filter @wf/web gen`
Expected: создан `apps/web/src/api/gen/public.d.ts` с `"/v1/health"`.

- [ ] **Step 2: Конфиги**

```json
{
  "extends": "@wf/config/tsconfig/react.json",
  "compilerOptions": {
    "types": ["node"],
    "allowJs": false,
    "incremental": true,
    "plugins": [{ "name": "next" }]
  },
  "include": ["next-env.d.ts", "src", ".next/types/**/*.ts", "next.config.ts"],
  "exclude": ["node_modules", "e2e"]
}
```
(Файл `apps/web/tsconfig.json`. Next при первом `build` может дописать нужные ему опции — принять и закоммитить; `baseUrl` не допускать: в TS 6 он устарел.)

```ts
// apps/web/next.config.ts
import type { NextConfig } from 'next';
import createNextIntlPlugin from 'next-intl/plugin';

// /v1/* проксируется в публичный API: браузер ходит на свой origin, CORS не нужен.
const apiOrigin = process.env.WF_API_ORIGIN ?? 'http://127.0.0.1:8080';

const config: NextConfig = {
  transpilePackages: ['@wf/i18n', '@wf/tokens'],
  async rewrites() {
    return [{ source: '/v1/:path*', destination: `${apiOrigin}/v1/:path*` }];
  },
};

export default createNextIntlPlugin()(config);
```

```ts
// apps/web/vitest.config.mts
// Vitest — для чистых функций и клиентских компонентов. Async Server Components он не рендерит:
// их покрывает Playwright (e2e/).
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
```

```ts
// apps/web/playwright.config.ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  use: { baseURL: 'http://localhost:3000' },
  webServer: {
    command: 'pnpm dev',
    url: 'http://localhost:3000',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
```

```js
// apps/web/eslint.config.mjs
// next lint в Next 16 удалён — ESLint вызывается напрямую.
import { fileURLToPath } from 'node:url';
import { boundariesConfig } from '@wf/config/eslint';
import nextVitals from 'eslint-config-next/core-web-vitals';
import nextTs from 'eslint-config-next/typescript';
import { defineConfig, globalIgnores } from 'eslint/config';

const repoRoot = fileURLToPath(new URL('../..', import.meta.url));

export default defineConfig([
  ...nextVitals,
  ...nextTs,
  boundariesConfig(repoRoot),
  globalIgnores(['.next/**', 'out/**', 'next-env.d.ts', 'src/api/gen/**', 'playwright-report/**']),
]);
```

```ini
# apps/web/.env.example
# Куда Next проксирует /v1/* (серверная переменная, в браузер не попадает)
WF_API_ORIGIN=http://127.0.0.1:8080
```

- [ ] **Step 3: Тестовая обвязка**

```ts
// apps/web/src/test/setup.ts
import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterAll, afterEach, beforeAll } from 'vitest';
import { server } from './server';

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
});
afterAll(() => server.close());
```

```ts
// apps/web/src/test/jest-dom.d.ts
// Vitest 5 расширяет Matchers<R, T>, а jest-dom 7.0.1 — старый Assertion<T>. Мост типов.
import 'vitest';
import type { TestingLibraryMatchers } from '@testing-library/jest-dom/matchers';

declare module 'vitest' {
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  interface Matchers<R = unknown, T = unknown> extends TestingLibraryMatchers<unknown, R> {}
}
```

```ts
// apps/web/src/test/server.ts
import { setupServer } from 'msw/node';
import { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '../api/gen/public';

export const TEST_API = 'http://web.test';
export const http = createOpenApiHttp<paths>({ baseUrl: TEST_API });
export const server = setupServer(
  http.get('/v1/health', ({ response }) => response(200).json({ status: 'ok' })),
);
```

```tsx
// apps/web/src/test/render.tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import { NextIntlClientProvider } from 'next-intl';
import type { ReactElement } from 'react';
import { createApi } from '../api/client';
import { ApiProvider } from '../api/context';
import { messagesFor } from '../i18n/messages';
import { TEST_API } from './server';

export function renderWithProviders(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ApiProvider api={createApi(TEST_API)}>
      <QueryClientProvider client={queryClient}>
        <NextIntlClientProvider locale="ru" messages={messagesFor('ru')}>
          {ui}
        </NextIntlClientProvider>
      </QueryClientProvider>
    </ApiProvider>,
  );
}
```

- [ ] **Step 4: Тесты (падают: кода нет)**

```ts
// apps/web/src/i18n/messages.test.ts
import { diffKeys, withFallback } from '@wf/i18n';
import { expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

test('в en те же ключи, что в ru', () => {
  expect(diffKeys(ru, en)).toEqual({ missing: [], extra: [] });
});

test('пустые строки en показываются по-русски', () => {
  expect(withFallback(ru, en)).toEqual(ru);
});
```

```tsx
// apps/web/src/features/health/HealthStatus.test.tsx
import { screen } from '@testing-library/react';
import { HttpResponse, http as mswHttp } from 'msw';
import { expect, test } from 'vitest';
import { renderWithProviders } from '../../test/render';
import { TEST_API, server } from '../../test/server';
import { HealthStatus } from './HealthStatus';

test('API отвечает — показываем «API работает»', async () => {
  renderWithProviders(<HealthStatus />);
  expect(await screen.findByText('API работает')).toBeInTheDocument();
});

test('API вернул ошибку — показываем «API недоступен»', async () => {
  server.use(
    mswHttp.get(`${TEST_API}/v1/health`, () =>
      HttpResponse.json(
        { type: 'about:blank', title: 'Internal Server Error', status: 500, code: 'internal' },
        { status: 500, headers: { 'Content-Type': 'application/problem+json' } },
      ),
    ),
  );
  renderWithProviders(<HealthStatus />);
  expect(await screen.findByText('API недоступен')).toBeInTheDocument();
});
```

```ts
// apps/web/e2e/smoke.spec.ts
// Серверный компонент страницы проверяется в браузере: Vitest его не рендерит.
import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const ru = JSON.parse(readFileSync(new URL('../messages/ru.json', import.meta.url), 'utf8'));

test('главная отдаёт заголовок из локали', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(ru.app.title);
});
```

Run: `pnpm --filter @wf/web test`
Expected: FAIL — не найдены локали, `HealthStatus`, `api/client`.

- [ ] **Step 5: Реализация**

```json
{
  "app": { "title": "ГДЕФУТБОЛ" },
  "health": {
    "checking": "Проверяем API…",
    "ok": "API работает",
    "down": "API недоступен"
  }
}
```
(Файл `apps/web/messages/ru.json`. Имя бренда живёт только здесь.)

```json
{
  "app": { "title": "" },
  "health": { "checking": "", "ok": "", "down": "" }
}
```
(Файл `apps/web/messages/en.json`.)

```ts
// apps/web/src/i18n/messages.ts
import { type Messages, withFallback } from '@wf/i18n';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

export type Locale = 'ru' | 'en';
export const defaultLocale: Locale = 'ru';

export function messagesFor(locale: Locale): Messages {
  return locale === 'en' ? withFallback(ru, en) : ru;
}
```

```ts
// apps/web/src/api/client.ts
import createClient from 'openapi-fetch';
import type { paths } from './gen/public';

// Клиент публичного API.
export function createApi(baseUrl: string) {
  return createClient<paths>({ baseUrl });
}

export type Api = ReturnType<typeof createApi>;
```

```tsx
// apps/web/src/api/context.tsx
'use client';

import { createContext, type ReactNode, useContext } from 'react';
import type { Api } from './client';

const ApiContext = createContext<Api | null>(null);

export function ApiProvider({ api, children }: { api: Api; children: ReactNode }) {
  return <ApiContext value={api}>{children}</ApiContext>;
}

export function useApi(): Api {
  const api = useContext(ApiContext);
  if (!api) throw new Error('useApi: нет ApiProvider выше по дереву');
  return api;
}
```

```tsx
// apps/web/src/features/health/HealthStatus.tsx
'use client';

import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { useApi } from '../../api/context';

export function HealthStatus() {
  const api = useApi();
  const t = useTranslations('health');
  const health = useQuery({
    queryKey: ['health'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/health');
      if (error || !data) throw new Error('health: API недоступен');
      return data;
    },
  });
  if (health.isPending) return <p role="status">{t('checking')}</p>;
  return <p role="status">{health.isError ? t('down') : t('ok')}</p>;
}
```

```ts
// apps/web/src/i18n/request.ts
// Локаль без роутинга: по умолчанию ru, en — только по куке (переключателя пока нет).
import { cookies } from 'next/headers';
import { getRequestConfig } from 'next-intl/server';
import { type Locale, messagesFor } from './messages';

export default getRequestConfig(async () => {
  const locale: Locale = (await cookies()).get('locale')?.value === 'en' ? 'en' : 'ru';
  return { locale, messages: messagesFor(locale) };
});
```

```tsx
// apps/web/src/app/providers.tsx
'use client';

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { type ReactNode, useState } from 'react';
import { createApi } from '../api/client';
import { ApiProvider } from '../api/context';

// Пустой baseUrl — тот же origin: Next проксирует /v1/* в API.
const api = createApi('');

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(() => new QueryClient());
  return (
    <ApiProvider api={api}>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </ApiProvider>
  );
}
```

```tsx
// apps/web/src/app/layout.tsx
import '@wf/tokens/tokens.css';
import type { Metadata } from 'next';
import { NextIntlClientProvider } from 'next-intl';
import { getLocale, getTranslations } from 'next-intl/server';
import type { ReactNode } from 'react';
import { Providers } from './providers';

export async function generateMetadata(): Promise<Metadata> {
  const t = await getTranslations('app');
  return { title: t('title') };
}

export default async function RootLayout({ children }: { children: ReactNode }) {
  const locale = await getLocale();
  return (
    <html lang={locale}>
      <body>
        <NextIntlClientProvider>
          <Providers>{children}</Providers>
        </NextIntlClientProvider>
      </body>
    </html>
  );
}
```

```tsx
// apps/web/src/app/page.tsx
import { getTranslations } from 'next-intl/server';
import { HealthStatus } from '../features/health/HealthStatus';

export default async function Home() {
  const t = await getTranslations('app');
  return (
    <main>
      <h1>{t('title')}</h1>
      <HealthStatus />
    </main>
  );
}
```

```yaml
# apps/web/Taskfile.yml
version: '3'
tasks:
  gen: { desc: Типы клиента из public.yaml, cmds: ['pnpm gen'] }
  gen:check:
    desc: Типы клиента совпадают с public.yaml
    cmds: ['pnpm gen', 'git diff --exit-code -- src/api/gen']
  dev: { desc: Dev-сервер веба (:3000), cmds: ['pnpm dev'] }
  test: { desc: Тесты веба (Vitest), cmds: ['pnpm test'] }
  e2e: { desc: Smoke в браузере (Playwright), cmds: ['pnpm e2e'] }
  lint: { cmds: ['pnpm lint'] }
  typecheck: { cmds: ['pnpm typecheck'] }
  build: { cmds: ['pnpm build'] }
  ci:
    desc: Всё, что проверяет CI для веба
    cmds:
      - task: gen:check
      - task: typecheck
      - task: lint
      - task: test
      - task: build
      - task: e2e
```

- [ ] **Step 6: Юнит-тесты, типы, линт, сборка (п. 17.5 спеки — `next build` на TS 6)**

Run: `pnpm --filter @wf/web test && pnpm --filter @wf/web build && pnpm --filter @wf/web typecheck && pnpm --filter @wf/web lint`
Expected: 4 теста PASS; `next build` успешен (если Next переписал `tsconfig.json` — посмотреть diff, `baseUrl` не допускать); `tsc` и ESLint чистые.
Если `next build` падает на TypeScript 6 — СТОП: показать ошибку пользователю, варианты — TS 5.9.3 для веба через catalog или ждать исправления; записать в `docs/versions.md`.

- [ ] **Step 7: Smoke в браузере**

Run: `pnpm --filter @wf/web exec playwright install chromium && pnpm --filter @wf/web e2e`
(Браузер скачивается в кэш пользователя `%LOCALAPPDATA%\ms-playwright` — один раз.)
Expected: `1 passed` — заголовок `ГДЕФУТБОЛ` из `messages/ru.json`.

- [ ] **Step 8: Коммит**

```bash
git add apps/web pnpm-lock.yaml
git commit -m "Веб: Next.js, next-intl, статус сервиса, smoke-тест"
```

---

## Фаза 3. Сборка в одно целое

### Task 18: Корневой Taskfile, git-хуки, CI-workflow

**Files:**
- Create: `Taskfile.yml`, `lefthook.yml`, `.prettierignore`
- Create: `.github/workflows/backend.yml`, `packages.yml`, `admin.yml`, `web.yml`, `repo.yml`
- Modify: `package.json` (корень: `lefthook`)

**Interfaces:**
- Consumes: Taskfile частей (Task 12, 16, 17), `@wf/*` пакеты (Task 13–15), `.tools/bin/*` (Task 2).
- Produces: `./task setup | dev | gen | test | ci | infra:up | infra:down | mail | secrets | workflows | packages:ci | design:test`; git-хук pre-commit; workflow на каждую часть, каждый вызывает `./task <часть>:ci`.

- [ ] **Step 1: Проверка до реализации — падает**

Run: `./task ci`
Expected: `task: No Taskfile found` — корневого Taskfile нет.

- [ ] **Step 2: Корневой Taskfile**

```yaml
# Taskfile.yml — единая точка входа. Логика проверок живёт в Taskfile частей,
# здесь только подключение и сценарии «первый запуск / разработка / CI».
version: '3'

output: prefixed

# Отсутствующие файлы пропускаются; setup создаёт их из .env.example.
dotenv: ['backend/.env', 'deploy/dev/.env']

vars:
  TOOLS: '{{.ROOT_DIR}}/.tools/bin'

includes:
  backend: { taskfile: ./backend/Taskfile.yml, dir: ./backend }
  admin: { taskfile: ./apps/admin/Taskfile.yml, dir: ./apps/admin }
  web: { taskfile: ./apps/web/Taskfile.yml, dir: ./apps/web }

tasks:
  infra:up:
    desc: Запустить локальный Postgres (.tools/pg)
    cmds: ['bash scripts/pg.sh start']

  infra:down:
    desc: Остановить локальный Postgres (данные в .tools/pg/data сохраняются)
    cmds: ['bash scripts/pg.sh stop']

  mail:
    desc: Mailpit — письма с кодами входа (UI :18025, SMTP :11025)
    cmds: ['{{.TOOLS}}/mailpit --listen 127.0.0.1:18025 --smtp 127.0.0.1:11025']

  setup:
    desc: Первый запуск после scripts/bootstrap.sh
    cmds:
      - test -f backend/.env || cp backend/.env.example backend/.env
      - test -f apps/web/.env.local || cp apps/web/.env.example apps/web/.env.local
      - pnpm install
      - bash scripts/pg.sh install
      - bash scripts/pg.sh init
      - task: infra:up
      # отдельный процесс: dotenv читается при старте task, а backend/.env мог только что появиться
      - '{{.TOOLS}}/task backend:db:migrate'

  gen:
    desc: Вся кодогенерация
    cmds:
      - task: backend:gen
      - pnpm --filter @wf/contracts gen:web
      - pnpm --filter @wf/contracts gen:admin
      - pnpm --filter @wf/tokens build

  dev:
    desc: api :8080, admin-api :8081, worker, веб :3000, админка :5173, Mailpit :18025 (Postgres — ./task infra:up)
    deps: [mail, backend:dev:api, backend:dev:admin, backend:dev:worker, web:dev, admin:dev]

  test:
    desc: Все тесты всех частей
    cmds:
      - task: packages:ci
      - task: backend:test
      - task: admin:test
      - task: web:test

  packages:ci:
    desc: Тесты общих пакетов и контрактов
    cmds:
      - pnpm --filter @wf/config --filter @wf/tokens --filter @wf/i18n --filter @wf/contracts test
      - pnpm --filter @wf/i18n typecheck

  design:test:
    desc: Тест извлечения экранов из экспорта канваса
    cmds: ['node --test "design/tools/*.test.mjs"']

  secrets:
    desc: gitleaks по истории репозитория
    cmds: ['{{.TOOLS}}/gitleaks git --redact --verbose']

  workflows:
    desc: actionlint по .github/workflows
    cmds: ['{{.TOOLS}}/actionlint']

  ci:
    desc: Всё, что проверяет CI, — локально
    cmds:
      - task: secrets
      - task: workflows
      - task: packages:ci
      - task: design:test
      - task: backend:ci
      - task: admin:ci
      - task: web:ci
```

Цель `design:test` запускает тест из Task 19, поэтому полный `./task ci` прогоняется в Task 19 (Step 4), а здесь — только `./task workflows` и dev-стенд.

- [ ] **Step 3: Git-хуки**

Корневой `package.json` — добавить в `devDependencies`: `"lefthook": "2.1.14"` (в `allowBuilds` он уже разрешён: его postinstall ставит хуки).

```yaml
# lefthook.yml — быстрые проверки на коммите. Тяжёлое — в ./task ci.
pre-commit:
  jobs:
    - name: prettier
      glob: "*.{ts,tsx,js,mjs,json,css,yml,yaml}"
      run: pnpm exec prettier --write --ignore-unknown {staged_files}
      stage_fixed: true
    - name: gofmt
      glob: "*.go"
      run: gofmt -w {staged_files}
      stage_fixed: true
    - name: gitleaks
      run: .tools/bin/gitleaks git --pre-commit --redact --staged --verbose
```

```gitignore
# .prettierignore — генерированное и документы не форматируем
pnpm-lock.yaml
**/src/api/gen/**
packages/tokens/tokens.css
packages/tokens/tokens.ts
**/.next/**
**/dist/**
design/
docs/
product/
*.md
```

Run: `pnpm install && ls .git/hooks/pre-commit`
Expected: файл хука существует.

- [ ] **Step 4: Проверить хуки подсадкой**

```bash
printf 'export const x   =   1\n' > packages/i18n/src/tmp-format.ts
git add packages/i18n/src/tmp-format.ts && pnpm exec lefthook run pre-commit
cat packages/i18n/src/tmp-format.ts        # ждём: export const x = 1;
printf 'token = "ghp_%s"\n' "$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')" > leak.txt
git add leak.txt && pnpm exec lefthook run pre-commit ; echo "exit=$?"   # ждём: gitleaks нашёл, exit≠0
git restore --staged packages/i18n/src/tmp-format.ts leak.txt && rm packages/i18n/src/tmp-format.ts leak.txt
```
Expected: Prettier переформатировал файл; gitleaks остановил коммит с секретом. Оба временных файла удалены (они внутри проекта).

- [ ] **Step 5: Workflow**

```yaml
# .github/workflows/backend.yml
name: backend
on:
  push:
    branches: [main]
    paths: &paths
      - 'backend/**'
      - 'contracts/openapi/**'
      - 'tools/**'
      - 'scripts/**'
      - 'Taskfile.yml'
      - '.github/workflows/backend.yml'
  pull_request:
    paths: *paths
jobs:
  ci:
    runs-on: ubuntu-latest
    services:
      db:
        image: postgis/postgis:18-3.6
        env:
          POSTGRES_PASSWORD: postgres
        ports: ['15432:5432']
        options: >-
          --health-cmd "pg_isready -U postgres"
          --health-interval 2s --health-timeout 3s --health-retries 30
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: backend/go.mod
          cache-dependency-path: '**/go.sum'
      - uses: pnpm/action-setup@v6
      - run: bash scripts/bootstrap.sh
      - run: ./task backend:ci
```

```yaml
# .github/workflows/packages.yml
name: packages
on:
  push:
    branches: [main]
    paths: &paths
      - 'packages/**'
      - 'contracts/**'
      - 'design/tools/**'
      - 'pnpm-workspace.yaml'
      - '.github/workflows/packages.yml'
  pull_request:
    paths: *paths
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: tools/go.mod
          cache-dependency-path: '**/go.sum'
      - uses: pnpm/action-setup@v6
      - run: bash scripts/bootstrap.sh
      - run: ./task packages:ci design:test
```

```yaml
# .github/workflows/admin.yml
name: admin
on:
  push:
    branches: [main]
    paths: &paths
      - 'apps/admin/**'
      - 'packages/**'
      - 'contracts/openapi/admin.yaml'
      - 'contracts/tokens/**'
      - 'pnpm-workspace.yaml'
      - '.github/workflows/admin.yml'
  pull_request:
    paths: *paths
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: tools/go.mod
          cache-dependency-path: '**/go.sum'
      - uses: pnpm/action-setup@v6
      - run: bash scripts/bootstrap.sh
      - run: ./task admin:ci
```

```yaml
# .github/workflows/web.yml
name: web
on:
  push:
    branches: [main]
    paths: &paths
      - 'apps/web/**'
      - 'packages/**'
      - 'contracts/openapi/public.yaml'
      - 'contracts/tokens/**'
      - 'pnpm-workspace.yaml'
      - '.github/workflows/web.yml'
  pull_request:
    paths: *paths
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: tools/go.mod
          cache-dependency-path: '**/go.sum'
      - uses: pnpm/action-setup@v6
      - run: bash scripts/bootstrap.sh
      - run: pnpm --filter @wf/web exec playwright install --with-deps chromium
      - run: ./task web:ci
        env:
          CI: 'true'
```

```yaml
# .github/workflows/repo.yml — секреты по всей истории и валидность самих workflow
name: repo
on:
  push:
    branches: [main]
  pull_request:
jobs:
  checks:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v7
        with:
          go-version-file: tools/go.mod
          cache-dependency-path: '**/go.sum'
      - run: |
          mkdir -p .tools/bin
          go -C tools build -o ../.tools/bin/ github.com/go-task/task/v3/cmd/task github.com/zricethezav/gitleaks/v8 github.com/rhysd/actionlint/cmd/actionlint
      - run: ./task secrets workflows
```

Если actionlint отвергнет YAML-якоря (`&paths`/`*paths`) — развернуть списки путей в обоих местах.

- [ ] **Step 6: Прогон: линт workflow, dev-стенд целиком**

Run: `./task workflows`
Expected: без замечаний.

В отдельном терминале: `./task dev`. Затем:
```bash
curl -s localhost:8080/v1/health     # {"status":"ok"}
curl -s localhost:8081/v1/health     # {"status":"ok"}
curl -s localhost:3000/v1/health     # {"status":"ok"} — через прокси Next
curl -s localhost:5173/v1/health     # {"status":"ok"} — через прокси Vite
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/nope   # 404 (problem+json)
```
Открыть `http://localhost:3000` и `http://localhost:5173`: заголовок и «API работает». Остановить `./task dev` (Ctrl+C) — все процессы завершаются без ошибок.

- [ ] **Step 7: Коммит**

```bash
git add Taskfile.yml lefthook.yml .prettierignore .github package.json pnpm-lock.yaml
git commit -m "Корневой Taskfile, git-хуки, CI-workflow"
```

### Task 19: Экраны дизайна в репозитории

**Files:**
- Create: `design/tools/extract-export.mjs`, `design/tools/extract-export.test.mjs`
- Create: `design/screens/*.dc.html` (18 файлов, результат скрипта)
- Move: `docs/WhereFootball — экраны приложения.html` → `design/_export/` (обычный `mv`: файл вне git)
- Modify: `.gitignore` (убрать строку про бандл в `docs/`)

**Interfaces:**
- Produces: `node design/tools/extract-export.mjs <бандл.html> <папка>` — пишет `NN-Название.dc.html`; функции `readBlock(html, type)`, `extractBoards(html) → {title, width, height, source}[]`, `stripFonts(src)`, `fileName(index, title)`.

- [ ] **Step 1: Тест на синтетическом бандле (падает: скрипта нет)**

```js
// design/tools/extract-export.test.mjs
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { gzipSync } from 'node:zlib';
import { extractBoards, fileName } from './extract-export.mjs';

// JSON внутри <script> в настоящем бандле экранирует закрывающий тег — повторяем это.
const island = (type, value) =>
  `<script type="__bundler/${type}">${JSON.stringify(value).replaceAll('</script>', '<\\/script>')}</script>`;

const pageHtml = island(
  'template',
  '<html><style>@font-face{font-family:X;src:url("u")}</style><x-dc><div>Привет</div></x-dc>' +
    '<script type="text/x-dc" data-dc-script="">class Component {}</script></html>',
);
const id = '11111111-2222-3333-4444-555555555555';
const bundle =
  island('manifest', {
    [id]: { mime: 'text/html', compressed: true, data: gzipSync(pageHtml).toString('base64') },
  }) +
  island(
    'template',
    `<iframe src="about:blank#${id}" title="Лента города" width="390" height="844"></iframe>`,
  );

test('достаёт артборд: разметка и логика без шрифтов', () => {
  const [board] = extractBoards(bundle);
  assert.equal(board.title, 'Лента города');
  assert.equal(board.width, 390);
  assert.match(board.source, /<x-dc><div>Привет<\/div><\/x-dc>/);
  assert.match(board.source, /class Component/);
  assert.doesNotMatch(board.source, /@font-face/);
});

test('имя файла — номер и название', () => {
  assert.equal(fileName(2, 'Лента города'), '03-Лента-города.dc.html');
});
```

Run: `node --test "design/tools/*.test.mjs"`
Expected: FAIL — `Cannot find module './extract-export.mjs'`.

- [ ] **Step 2: Скрипт**

```js
// design/tools/extract-export.mjs
// Достаёт исходники экранов из HTML-экспорта Design-канваса.
// Бандл: манифест {uuid: {mime, compressed, data}} + шаблон с iframe на каждый артборд;
// каждая страница — сама бандл, её шаблон содержит <x-dc>…</x-dc> и логику экрана.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gunzipSync } from 'node:zlib';

// Берём ПОСЛЕДНЕЕ вхождение: та же строка встречается в JS загрузчика бандла.
export function readBlock(html, type) {
  const open = `<script type="__bundler/${type}">`;
  const i = html.lastIndexOf(open);
  if (i < 0) return null;
  const start = i + open.length;
  return html.slice(start, html.indexOf('</script>', start));
}

// @font-face ссылаются на uuid-ресурсы бандла и вне него не работают. Шрифты — Oswald и Manrope.
export const stripFonts = (src) => src.replace(/<style>[^<]*@font-face[\s\S]*?<\/style>/g, '');

export const fileName = (index, title) =>
  `${String(index + 1).padStart(2, '0')}-${title.replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '')}.dc.html`;

function decode(entry) {
  const raw = Buffer.from(entry.data, 'base64');
  return (entry.compressed ? gunzipSync(raw) : raw).toString('utf8');
}

export function extractBoards(html) {
  const manifest = JSON.parse(readBlock(html, 'manifest'));
  const template = JSON.parse(readBlock(html, 'template'));
  const re = /about:blank#([0-9a-f-]{36})" title="([^"]*)" width="(\d+)" height="(\d+)"/g;
  return [...template.matchAll(re)].map(([, id, title, width, height]) => {
    const inner = JSON.parse(readBlock(decode(manifest[id]), 'template'));
    const markup = inner.slice(inner.indexOf('<x-dc>'), inner.lastIndexOf('</x-dc>') + '</x-dc>'.length);
    const logic = inner.match(/<script type="text\/x-dc"[\s\S]*?<\/script>/)?.[0] ?? '';
    return { title, width: Number(width), height: Number(height), source: `${stripFonts(markup)}\n\n${logic}\n` };
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [bundlePath, outDir] = process.argv.slice(2);
  if (!bundlePath || !outDir) {
    console.error('usage: node design/tools/extract-export.mjs <бандл.html> <папка>');
    process.exit(1);
  }
  const boards = extractBoards(await readFile(bundlePath, 'utf8'));
  await mkdir(outDir, { recursive: true });
  for (const [i, b] of boards.entries()) await writeFile(join(outDir, fileName(i, b.title)), b.source);
  console.log(`извлечено артбордов: ${boards.length}`);
}
```

- [ ] **Step 3: Тест — PASS; извлечь настоящие экраны**

Run: `node --test "design/tools/*.test.mjs"`
Expected: PASS обоих тестов.

```bash
mkdir -p design/_export && mv "docs/WhereFootball — экраны приложения.html" design/_export/
node design/tools/extract-export.mjs "design/_export/WhereFootball — экраны приложения.html" design/screens
ls design/screens | wc -l
```
Expected: `извлечено артбордов: 18`, 18 файлов (`01-Карта-экранов-и-состояний.dc.html` … `18-Страны-и-турниры.dc.html`), суммарно ~1,7 МБ.

Из `.gitignore` удалить строку `docs/WhereFootball — экраны приложения.html` (правило `design/_export/` остаётся).

- [ ] **Step 4: Полный прогон CI локально**

Run: `./task ci`
Expected: все части зелёные — это критерий готовности п. 4 спеки.

- [ ] **Step 5: Коммит**

```bash
git add design/tools design/screens .gitignore
git commit -m "Экраны дизайна в репозитории и скрипт извлечения"
```

### Task 20: Документация под новую структуру

**Files:**
- Create: `docs/versions.md`, `docs/03-вынос-в-отдельный-репо.md`, `docs/04-принципы-архитектуры.md`
- Modify: `README.md`, `docs/02-архитектура.md`, `backend/migrations/README.md`, `design/README.md`, `design/03-токены.md`
- (`CLAUDE.md` переписывается в Task 22.)

**Interfaces:**
- Consumes: итоговые пути и команды из Task 1–19.
- Produces: документы, которые не расходятся с кодом.

- [ ] **Step 1: Найти устаревшие места (проверка «до»)**

Run: `grep -rn "/migrations\|/cmd\|/internal\|/openapi\|PostgreSQL 1[67]\|Go 1.25\|goose -dir" README.md docs/02-архитектура.md backend/migrations/README.md design/README.md`
Expected: список мест со старыми путями и версиями — всё это правится в Step 2–3.

- [ ] **Step 2: Новые документы**

```markdown
<!-- docs/versions.md -->
# Версии

Правило: последние стабильные. Если экосистема не поддерживает последнюю — последняя
совместимая, с причиной и условием апгрейда в таблице исключений. Проверять по официальным
источникам: go.dev/dl, nodejs.org/dist/index.json, `npm view`, GitHub Releases, postgresql.org.

| Что | Версия | Где закреплено |
| --- | --- | --- |
| Go | 1.27.1 | `toolchain` в `backend/go.mod`, `tools/go.mod` |
| Node.js | 26.10.0 | `devEngines.runtime` в корневом `package.json` |
| pnpm | 12.6.0 | `packageManager` |
| PostgreSQL / PostGIS | 18.6 / 3.6.2 | `scripts/pg.sh` (локально, zip), `postgis/postgis:18-3.6` в CI |
| Next.js / React | 16.3.6 / 19.3.0 | `apps/web/package.json`, catalog |
| Vite / Vitest | 8.3.1 / 5.0.1 | catalog |
| Playwright | 1.63.0 | `apps/web/package.json` |
| chi / pgx | 5.3.2 / 5.11.0 | `backend/go.mod` |
| oapi-codegen / sqlc / River | 2.8.0 / 1.31.1 / 0.47.0 | `backend/tools/go.mod` |
| goose / pgtestdb / kin-openapi | 3.28.0 / 0.1.1 / 0.149.0 | `backend/go.mod` |
| go-task / gitleaks / actionlint / Mailpit | 3.53.1 / 8.30.1 / 1.7.12 / 1.31.2 | `tools/go.mod` |
| golangci-lint | 2.14.0 | `tools/lint/go.mod` |
| lefthook | 2.1.14 | корневой `package.json` |

## Исключения

| Что | Стоит | Последняя | Почему | Когда апгрейдить |
| --- | --- | --- | --- | --- |
| TypeScript (приложения) | 6.0.3 | 7.0.2 | typescript-eslint 8.70 поддерживает TS < 6.1; у 7.x нет классического JS API | когда typescript-eslint и eslint-config-next объявят поддержку TS 7 |
| TypeScript (генератор) | 5.9.3 | 7.0.2 | openapi-typescript 7.13 требует `typescript ^5.x` | когда openapi-typescript поднимет peer |
| ESLint | 9.39.5 | 10.11.0 | eslint-config-next 16.3.6 тянет eslint-plugin-react 7.37.5 (ESLint ≤ 9) | когда eslint-config-next перейдёт на ESLint 10 |

## Ловушки

- pnpm по умолчанию не ставит версии младше суток (`minimumReleaseAge`) — это защита, не баг.
- sqlc разбирает грамматику PostgreSQL 17: синтаксис PG18 в SQL не используем.
- Глобальный pnpm должен быть ≥ 10.34.5, иначе он не переключается на 12 по `packageManager`.
```

```markdown
<!-- docs/03-вынос-в-отдельный-репо.md -->
# Вынос части в отдельный репозиторий

Структура рассчитана на то, что вынос — механическая операция. У каждой части свой манифест
(`go.mod` / `package.json`), свой Taskfile, свой workflow и свой `.env.example`;
сгенерированный код закоммичен, поэтому часть собирается сразу.

## Шаги (на примере админки)

1. История папки в отдельную ветку: `git subtree split -P apps/admin -b split/admin`.
2. Новый репозиторий: `git push <новый-remote> split/admin:main`.
3. Контракт: скопировать `contracts/openapi/admin.yaml` в новый репо и зафиксировать
   источник (как у wf-native: копия + тег или коммит).
4. Общие пакеты `@wf/config`, `@wf/tokens`, `@wf/i18n`: опубликовать в GitHub Packages
   (npm-реестр требует токен даже для публичных пакетов) или скопировать в новый репо.
   Заменить `workspace:*` и `catalog:` на конкретные версии.
5. Перенести `.github/workflows/admin.yml`, заменить `./task admin:ci` на цели своего Taskfile.
6. В `pnpm-workspace.yaml` нового репо — `allowBuilds` из корня этого.

## Бэкенд

То же через `git subtree split -P backend`. Пути `../contracts` встречаются только
в конфигах кодогенерации (`backend/Taskfile.yml`, цель `gen`) — заменить на локальную копию
контракта. В рантайме путей нет: спека вшита в бинарник (`embedded-spec`).
В новом репо добавить в `backend/Taskfile.yml` `dotenv: ['.env']` и собственную сборку
`.tools/bin` (golangci-lint).
```

```markdown
<!-- docs/04-принципы-архитектуры.md -->
# Принципы архитектуры

По ним проверяется каждый PR.

## Переиспользование — одна реализация на весь проект

| Что | Где | Кто пользуется |
| --- | --- | --- |
| Ошибки `problem+json`, middleware, graceful shutdown | `backend/internal/platform/httpx` | api, admin-api |
| Жизненный цикл HTTP-бинарника | `backend/internal/platform/server` | api, admin-api |
| Конфиг, логи, пул БД, миграции, очередь | `backend/internal/platform/{config,logx,db,migrate,queue}` | все бинарники |
| Тестовая база, проверка по контракту | `backend/internal/platform/testkit` | тесты всех модулей |
| Контракт API | `contracts/openapi` | Go-сервер, TS-клиенты, позже Dart |
| Токены дизайна | `contracts/tokens/tokens.json` → `@wf/tokens` | веб, админка, позже Flutter |
| Локализация: fallback и сверка ключей | `@wf/i18n` | веб, админка |
| Конфиги TS, ESLint, Prettier | `@wf/config` | все JS-пакеты |

Общий код выносится при втором реальном потребителе или когда это граница платформы.
Абстракции «на всякий случай» не делаем.

## Масштабирование без костылей

- API без состояния в памяти процесса: сессии и идемпотентность — в базе. Второй инстанс
  за балансировщиком не требует изменений кода.
- Модули общаются через интерфейсы верхнего уровня и события outbox; в чужие таблицы не ходят.
  Вынос модуля в сервис — замена реализации интерфейса.
- Worker масштабируется отдельно: очередь River в Postgres, задачи не задваиваются.
- Пул, таймауты, число воркеров — из конфига, не из кода.

## Границы, которые проверяются автоматически

- `cmd/api` не зависит от админского кода — `backend/internal/archtest` (транзитивно) и depguard.
- `apps/web` и `apps/admin` не импортируют друг друга, пакеты не импортируют приложения —
  eslint-plugin-boundaries (`@wf/config/eslint`).
- Клиент админского API существует только в `apps/admin`.
- Сгенерированный код совпадает с контрактами — `backend:gen:check`, тест `@wf/tokens`.
- Схема `Problem` одинакова в обоих контрактах — тест `@wf/contracts`.

## TDD

Сначала падающий тест, потом код — на бэке и на фронте. Страж проверяется подсадкой бага.
```

- [ ] **Step 3: Правки существующих документов**

- `README.md`: таблицу «Три пакета» заменить на раскладку `backend/ contracts/ apps/ packages/ deploy/ design/ docs/ product/`; раздел «Состояние» — «каркас готов: `scripts/bootstrap.sh` → `./task setup` → `./task dev`; `./task ci` — все проверки»; добавить раздел «База без Docker»: `scripts/pg.sh install|init|start|stop`, кластер в `.tools/pg`, системный PostgreSQL не используется; и раздел «Если порт занят» с командой `netsh int ipv4 set dynamicport tcp start=49152 num=16384` (от администратора) и переменными `WF_PG_PORT` и др. из `deploy/dev/.env.example`.
- `docs/02-архитектура.md`: блок «Структура репозитория» — пути с префиксами `backend/`, плюс `contracts/`, `apps/`, `packages/`; в таблице стека «PostgreSQL 17+» → «PostgreSQL 18»; «goose или atlas» → «goose (библиотекой, `cmd/migrate`)».
- `backend/migrations/README.md`: «PostgreSQL 16+» → «PostgreSQL 18 (sqlc разбирает грамматику 17 — синтаксис 18 не использовать)»; блок наката — `./task backend:db:migrate` вместо `goose -dir`; в «Порядок» дописать `0011–0016_river_v2..v7.sql — схема очереди River, по файлу на версию`.
- `design/README.md`: раздел «Макеты» — ссылка на канвас `https://claude.ai/code/artifact/185013b0-1b61-4c89-92b1-64064cb9754c`, исходники экранов в `design/screens/`, обновление — экспорт HTML в `design/_export/` и `node design/tools/extract-export.mjs "<файл>" design/screens`.
- `design/03-токены.md`: в начало — «Источник значений — `contracts/tokens/tokens.json` (сверено с артбордом «Токены»); этот файл описывает имена и правила»; строку «Один шрифт, гротеск» → «Два шрифта: Oswald — крупные цифры и заголовки, Manrope — текст; оба с кириллицей».

- [ ] **Step 4: Проверка «после»**

Run: `grep -rn "PostgreSQL 1[67]\|goose -dir\|^/migrations" README.md docs/02-архитектура.md backend/migrations/README.md design/README.md`
Expected: пусто.

- [ ] **Step 5: Коммит**

```bash
git add README.md docs design backend/migrations/README.md
git commit -m "Документация под новую структуру"
```

### Task 21: Каркас репозитория wf-native

**Files (в `F:\ideas\wf-native\`):**
- Create: `README.md`, `CLAUDE.md`, `.gitattributes`, `.gitignore`

**Interfaces:**
- Consumes: тег `contracts-vX.Y.Z` публичного контракта wf-all (механизм описывается, тег ставится при первом потребителе).
- Produces: пустой репо с описанным механизмом синхронизации контракта; remote `origin` → `A-mi13/wf-native`, без пуша.

- [ ] **Step 1: Проверка «до»**

Run: `ls F:/ideas/wf-native 2>&1`
Expected: `No such file or directory`.

- [ ] **Step 2: Файлы**

```bash
mkdir -p F:/ideas/wf-native && cd F:/ideas/wf-native && git init -b main -q \
  && git remote add origin https://github.com/A-mi13/wf-native.git
printf '* text=auto eol=lf\n*.png binary\n*.jpg binary\n' > .gitattributes
printf '.dart_tool/\nbuild/\n.env\n' > .gitignore
```

```markdown
<!-- F:/ideas/wf-native/README.md -->
# wf-native

Мобильное приложение (Flutter) для iOS и Android. Бэкенд, веб и админка — в репо `wf-all`.

## Связь с бэкендом — только через контракт

Приложение не зависит от кода wf-all. Оно берёт из него два файла по тегу:

- `contracts/openapi/public.yaml` — публичный API (админский контракт сюда не попадает);
- `contracts/tokens/tokens.json` — токены дизайна.

Механизм (появится вместе с приложением, см. мобильную спеку):

1. `contract.lock` — тег `contracts-vX.Y.Z` из wf-all.
2. Синхронизация копирует оба файла в `contract/` (коммитится — сборка воспроизводима без сети).
3. Из копии генерируются Dart-клиент API и константы токенов.
4. Обновление контракта — осознанный шаг: поменять тег, синхронизировать, закоммитить.

Старые версии приложения живут на телефонах месяцами, поэтому API версионируется `/v1`,
а ломающие изменения публичного контракта в wf-all требуют нового major.

## Состояние

Пусто. Flutter-проект, генератор Dart и скрипт синхронизации — в мобильной спеке.
```

```markdown
<!-- F:/ideas/wf-native/CLAUDE.md -->
# wf-native — контекст для Claude Code

Flutter-приложение WF. Отвечай по-русски; комментарии в коде — по-русски, идентификаторы — по-английски.

- Код и контракт бэкенда — в `F:\ideas\wherefootball` (репо wf-all). Отсюда в wf-all не лезем:
  связь только через копию `public.yaml` и `tokens.json` в `contract/` по тегу из `contract.lock`.
- Технический слаг — `wf`; имя бренда только в локализации (бренд не окончательный).
- TDD: сначала падающий тест, потом код. Только последние стабильные версии (см. wf-all `docs/versions.md`).
- `flutter upgrade` глобальный — перед ним спросить пользователя.
- Коммиты одной строкой по-русски; пуш только по команде пользователя.
```

- [ ] **Step 3: Проверка и коммит (в wf-native)**

Run: `cd F:/ideas/wf-native && git add -A && git status --short && git remote -v`
Expected: 4 файла, `origin https://github.com/A-mi13/wf-native.git`.

```bash
cd F:/ideas/wf-native && git commit -m "Каркас репозитория: описание связи с контрактом"
```

### Task 22: Настройка Claude Code по BOOTSTRAP_PROMPT

**Files:**
- Rewrite: `CLAUDE.md` (≤ 80 строк)
- Create: `.claude/rules/backend.md`, `.claude/rules/frontend.md`, `.claude/rules/contracts.md`, `.claude/rules/env.md`, `.claude/settings.json`
- Create (только если есть материал — критерий ниже): `.claude/skills/<имя>/SKILL.md`
- Modify: `C:\Users\user\.claude\projects\F--ideas-wherefootball\memory\MEMORY.md` (≤ 50 строк)

**Interfaces:**
- Consumes: весь каркас; `C:\Users\user\.claude\BOOTSTRAP_PROMPT.md` (читать целиком перед задачей — он главный источник требований).
- Produces: slim-контекст для будущих сессий.

- [ ] **Step 1: Снимок «до»**

Run: `wc -l CLAUDE.md C:/Users/user/.claude/projects/F--ideas-wherefootball/memory/MEMORY.md && cat C:/Users/user/.claude/settings.json | head -50`
Записать числа — они нужны для отчёта (п. 8 промта). Глобальный settings.json только читать.

- [ ] **Step 2: CLAUDE.md ≤ 80 строк**

Разделы строго по промту: Stack (1 строка) · Hard rules (≤ 8) · File structure · Skills · What NOT to do · Git policy · Naming · Проверки. Всё, что уже в коде или в `.claude/rules/`, — не дублировать.
Hard rules — только с опорой на файл (указать источник в скобках):
1. Аренда поля — платформа деньги не принимает; `rent_total_minor` — справочно (`docs/01-домен-и-правила.md`).
2. Суммы — `int64` в минорных единицах + ISO 4217, без float (`backend/migrations/README.md`).
3. Время — `timestamptz` UTC, таймзона у города (`docs/02-архитектура.md`).
4. Роли — `role_assignments` со скоупом (`backend/migrations/0003_identity.sql`).
5. Мутирующие запросы принимают `Idempotency-Key`; события — в `outbox` в той же транзакции (`backend/migrations/0001_platform.sql`).
6. Строки UI — только в `apps/*/messages/{ru,en}.json`; `en` с теми же ключами (`@wf/i18n`, тесты локалей).
7. TDD: сначала падающий тест (`docs/04-принципы-архитектуры.md`).
8. Версии — последние стабильные, исключения в `docs/versions.md`.
Git policy: коммиты одной строкой по-русски, пуш только по команде, force-push запрещён (память проекта).
Проверки: `./task ci` — всё; точечно `./task backend:test`, `./task admin:test`, `./task web:test`; `typecheck/lint/build` — только по просьбе (глобальный CLAUDE.md).
В отчёте перечислить, что вынесено из старого CLAUDE.md и куда (например, список «что гарантирует база» → `.claude/rules/backend.md`).

- [ ] **Step 3: Правила с paths**

```markdown
---
paths: ["backend/**"]
---
<!-- .claude/rules/backend.md -->
# Бэкенд
- Новый модуль: `internal/<модуль>/`, SQL в `internal/<модуль>/queries/`, элемент в `backend/sqlc.yaml`.
- Общее — только в `internal/platform/*`; в чужие таблицы и пакеты модулей не ходить.
- Тесты с БД — `testkit/dbtest` (клон на тест); HTTP — `testkit/apitest` (проверка по контракту).
- Ошибки клиенту — `httpx.WriteProblem` с кодом `<модуль>.<ошибка>`; тексты не отдавать.
- Что уже гарантирует база (проверки в коде — ради понятной ошибки, не как единственная защита):
  два матча на одном поле в пересекающееся время (`EXCLUDE` + зазор 10 минут); одинаковые названия
  команд в городе и клоны латиницей (`normalize_text()`); один капитан — одна команда в городе;
  платный матч без суммы и бесплатный с суммой; прогнозы только за заработанные очки;
  больше трёх снятых броней позиции у игрока в команде; изменение записи аудит-лога.
- Кодогенерация: `./task backend:gen`; сгенерированное коммитится.
```

```markdown
---
paths: ["apps/**", "packages/**"]
---
<!-- .claude/rules/frontend.md -->
# Фронты
- Приложения не импортируют друг друга; общее — только `@wf/*` из `packages/`.
- Клиент API — через `ApiProvider`/`useApi`, адрес — тот же origin (прокси dev-сервера).
- Цвета, отступы, шрифты — только CSS-переменные `--wf-*` из `@wf/tokens`.
- Строки — `useTranslations`, ключи в `messages/ru.json` и `en.json` одновременно.
- Тест компонента — `renderWithProviders` + MSW-моки из `src/test/server.ts`.
- Серверные компоненты Next — через Playwright (`apps/web/e2e`), Vitest их не рендерит.
- Экран по макету: исходник в `design/screens/NN-*.dc.html`.
```

```markdown
---
paths: ["contracts/**"]
---
<!-- .claude/rules/contracts.md -->
# Контракты
- Изменил YAML → `./task gen` → закоммить сгенерированное в backend и apps.
- `Problem` правится в `public.yaml` и `admin.yaml` одинаково (тест `@wf/contracts`).
- Ломающее изменение `public.yaml` — только с новым major тега `contracts-vX.Y.Z`.
- `tokens.json` → `pnpm --filter @wf/tokens build`.
```

```markdown
---
paths: ["backend/internal/platform/config/**", "backend/cmd/**", "apps/*/src/**", "apps/*/*.config.*", "deploy/**", "scripts/**"]
---
<!-- .claude/rules/env.md -->
# Переменные окружения — добавлять сразу
- Нужен новый ключ → в ТОМ ЖЕ коммите: поле в `backend/internal/platform/config` (префикс бинарника,
  default или `required`), строка с комментарием в нужном `.env.example`, рабочее значение в локальном
  `backend/.env` / `apps/web/.env.local` (они в .gitignore).
- Инструменты не пишут `.env*` (глобальный deny). В этом проекте пользователь разрешил писать их скриптом:
  .sh в scratchpad → `bash <скрипт>` → коммит только `.env.example` через `git commit -- <пути>`.
- Секреты — только плейсхолдеры в `.env.example`; значения не коммитить и не выводить в отчёты.
- Во фронт-сборку попадают только публичные значения (`VITE_*`, `NEXT_PUBLIC_*`).
```

Проверить, что каждый glob матчит существующие файлы: `ls backend/cmd/api/main.go apps/admin/src/App.tsx packages/i18n/src/index.ts contracts/openapi/public.yaml backend/internal/platform/config/config.go deploy/dev/.env.example`.

- [ ] **Step 4: Skills — только при наличии материала**

Критерий промта: материал в проекте есть, нужен не каждую сессию и не восстанавливается чтением 1–2 файлов. Кандидаты проверить по этому критерию, решение записать в отчёт:
- `design-screens` — как найти исходник экрана и токены для UI-задачи (`design/screens/`, `design/tools/extract-export.mjs`, `contracts/tokens/tokens.json`);
- `db-schema` — навигация по 66 таблицам и гарантиям базы (`backend/migrations/*.sql`, README).
Каждый создаваемый SKILL.md — UTF-8 без BOM, `description: Use when …`, тело ≤ 150 строк.

- [ ] **Step 5: settings.json проекта**

```json
{
  "enabledPlugins": {
    "expo@claude-plugins-official": false,
    "figma@claude-plugins-official": false
  }
}
```
(Expo в проекте нет — Flutter; Figma не используется — дизайн в канвасе. Playwright и context7 оставить: e2e и доки библиотек.) Перед записью сверить точные имена плагинов с глобальным `settings.json`.

- [ ] **Step 6: MEMORY.md — индекс ≤ 50 строк**

Разделы промта: Project · Key Docs · External references (только имена env-переменных) · Implementation Order · Feedback pointers · Preferences. Существующие файлы памяти оставить, обновить `project-setup-order.md`: «каркас готов, дальше — спеки бэкенда и админки».

- [ ] **Step 7: Проверка (вставить вывод в отчёт)**

```bash
wc -l CLAUDE.md C:/Users/user/.claude/projects/F--ideas-wherefootball/memory/MEMORY.md
claude plugin validate .
for f in .claude/skills/*/SKILL.md; do [ -f "$f" ] && head -c 3 "$f" | od -An -tx1; done   # не "ef bb bf"
node -e "JSON.parse(require('fs').readFileSync('.claude/settings.json','utf8'));console.log('json ok')"
```
Expected: CLAUDE.md ≤ 80, MEMORY.md ≤ 50, validate без ошибок, BOM нет, JSON валиден. Любой пункт красный — исправить до отчёта.
Попросить пользователя выполнить `/context` и `/skill-doctor` и прислать вывод — цифры контекста только из них.

- [ ] **Step 8: Коммит**

```bash
git add CLAUDE.md .claude
git commit -m "Настройка Claude Code: slim CLAUDE.md, правила по путям"
```
