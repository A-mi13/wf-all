#!/usr/bin/env bash
# scripts/e2e-admin.sh — живой e2e админки (Task 16): Playwright поверх настоящих admin-api, Postgres и
# Mailpit. Запуск — ./task admin:e2e (аргументы после -- уходят в playwright test).
#
# Готовит то, что нужно ДО webServer Playwright (он стартует раньше globalSetup):
#   0. замок прогона .tools/e2e/run.lock (ревью Task 16, M4): второй прогон, стартовавший одновременно,
#      прошёл бы проверку портов и пересоздал бы базу и exe под первым; замок держится до конца прогона,
#      брошенный (процесс с его PID умер) — забирается;
#   1. порты e2e свободны (свои, чтобы не задеть ./task dev: Vite 5174/5175, admin-api 18181/18182,
#      Mailpit 18026/11026);
#   2. база wf_e2e на том же кластере пересоздана (DROP … WITH (FORCE)): чистка и истечение подтверждений
#      идут по реальным часам — общие данные dev не трогаем;
#   3. миграции и права ролей (cmd/migrate up → grants.sql);
#   4. admin-api собран в .tools/e2e/;
#   5. одноразовый ключ сотрудников ADMIN_STAFF_SECRET_KEYS (только в окружении прогона, не выводится).
# Dev-значения ADMIN_* (корневой Taskfile грузит backend/.env) из окружения убираются: admin-api получает
# только окружение e2e (apps/admin/e2e/env.ts).
#
# Кластер: локально — scripts/pg.sh (127.0.0.1:WF_PG_PORT, суперпользователь postgres без пароля); в CI —
# сервис postgis/postgis:18-3.6 с ролями и расширениями из deploy/dev/initdb/20_wf.sql
# (WF_E2E_PG_SUPERUSER_URL=postgres://postgres:postgres@localhost:15432/postgres).
# Адреса ролей migrator и admin — с того же хоста и порта, что WF_E2E_PG_SUPERUSER_URL (ревью Task 16, M6).
# Проекты браузеров — WF_E2E_PROJECTS (по умолчанию chromium,webkit; CI — chromium).
# Две пары admin-api (Chromium и WebKit) работают на одной базе и обе исполняют очередь staff (River):
# письмо приглашения может уйти из пары другого браузера, и ссылка в нём — на чужой origin. Сценарии
# берут из письма только код, не ссылку (ревью Task 16, M5); сценарий по ссылке — только с одной парой
# (WF_E2E_PROJECTS=chromium) или своей базой на пару.
set -euo pipefail
cd "$(dirname "$0")/.."

die() { echo "e2e-admin: $*" >&2; exit 1; }

LOCAL_PORT="${WF_PG_PORT:-15432}"
case "$LOCAL_PORT" in '' | *[!0-9]*) die "WF_PG_PORT — не число" ;; esac
SUPER_URL="${WF_E2E_PG_SUPERUSER_URL:-postgres://postgres@127.0.0.1:${LOCAL_PORT}/postgres}"
# хост и порт ролей migrator и admin — из того же адреса, что у суперпользователя
read -r PG_HOST PG_PORT < <(node -e '
  const u = new URL(process.argv[1]);
  process.stdout.write(`${u.hostname || "127.0.0.1"} ${u.port || "5432"}
`);
' "$SUPER_URL") || die "WF_E2E_PG_SUPERUSER_URL — не адрес postgres://"
DB=wf_e2e
PROJECTS="${WF_E2E_PROJECTS:-chromium,webkit}"

exe=
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) exe=.exe ;; esac

# psql: локальный кластер (.tools/pg) или из PATH (CI)
PSQL=psql
[ -f ".tools/pg/pgsql/bin/psql$exe" ] && PSQL=".tools/pg/pgsql/bin/psql$exe"
command -v "$PSQL" >/dev/null || die "нет psql (локально — bash scripts/pg.sh install)"
[ -f ".tools/bin/mailpit$exe" ] || die "нет .tools/bin/mailpit — сначала bash scripts/bootstrap.sh"

# 0. Замок прогона: mkdir атомарен; в замке — PID держателя. Держатель жив — отказ; умер (брошенный
# замок после kill) — замок забирается. Снимается при любом выходе (trap), поэтому без exec ниже.
LOCK=.tools/e2e/run.lock
mkdir -p .tools/e2e
if ! mkdir "$LOCK" 2>/dev/null; then
  holder=$(cat "$LOCK/pid" 2>/dev/null || true)
  if [ -n "$holder" ] && kill -0 "$holder" 2>/dev/null; then
    die "уже идёт прогон e2e (PID $holder, замок $LOCK) — дождитесь его конца"
  fi
  echo "e2e-admin: замок $LOCK брошен (PID ${holder:-?} не жив) — забираю" >&2
  rm -f "$LOCK/pid"
  rmdir "$LOCK" 2>/dev/null || true
  mkdir "$LOCK" || die "замок $LOCK занят другим прогоном"
fi
echo $$ >"$LOCK/pid"
unlock() { rm -f "$LOCK/pid"; rmdir "$LOCK" 2>/dev/null || true; }
trap unlock EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# 1. Порты: занятый — понятная ошибка до сборки (чужие процессы не трогаем)
ports="18026 11026"
case ",$PROJECTS," in *,chromium,*) ports="$ports 5174 18181" ;; esac
case ",$PROJECTS," in *,webkit,*) ports="$ports 5175 18182" ;; esac
# shellcheck disable=SC2086
node -e '
  const net = require("node:net");
  const busy = [];
  Promise.all(process.argv.slice(1).map((p) => new Promise((done) => {
    const s = net.createServer();
    s.once("error", () => { busy.push(p); done(); });
    s.listen(Number(p), "127.0.0.1", () => s.close(done));
  }))).then(() => {
    if (busy.length) {
      console.error("e2e-admin: заняты порты " + busy.sort().join(", ") + " — остановите прежний прогон e2e (чужие процессы не трогаем)");
      process.exit(1);
    }
  });
' $ports

# 2. База e2e
"$PSQL" "$SUPER_URL" -v ON_ERROR_STOP=1 -q \
  -c "DROP DATABASE IF EXISTS $DB WITH (FORCE)" \
  -c "CREATE DATABASE $DB OWNER migrator"

# 3. Миграции и права (вывод — только при ошибке)
if ! out=$(cd backend && MIGRATOR_DATABASE_URL="postgres://migrator:migrator@${PG_HOST}:${PG_PORT}/${DB}?sslmode=disable" \
  MIGRATOR_LOG_FORMAT=text go run ./cmd/migrate up 2>&1); then
  printf '%s\n' "$out" | tail -n 30 >&2
  die "миграции wf_e2e не прошли"
fi

# 4. admin-api
(cd backend && go build -o "../.tools/e2e/admin-api$exe" ./cmd/admin-api)

# 5. Окружение прогона: без dev-настроек админки, с базой и одноразовым ключом e2e
for k in $(compgen -e); do
  case "$k" in ADMIN_* | WF_ADMIN_API_ORIGIN) unset "$k" ;; esac
done
WF_E2E_STAFF_SECRET_KEYS=$(node -e 'process.stdout.write(require("node:crypto").randomBytes(32).toString("base64"))')
export WF_E2E_STAFF_SECRET_KEYS
export WF_E2E_ADMIN_DATABASE_URL="postgres://admin:admin@${PG_HOST}:${PG_PORT}/${DB}?sslmode=disable"
export WF_E2E_PROJECTS="$PROJECTS"

cd apps/admin
# не exec: замок снимает trap EXIT этого процесса после конца прогона
pnpm exec playwright test "$@"
