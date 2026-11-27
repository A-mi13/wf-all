---
paths:
  - "backend/internal/platform/config/**"
  - "backend/cmd/**"
  - "apps/*/src/**"
  - "apps/*/*.config.*"
  - "deploy/**"
  - "scripts/**"
  - "**/.env.example"
---

# Переменные окружения — добавлять сразу

- Нужен новый ключ → в ТОМ ЖЕ коммите: поле в `backend/internal/platform/config` (префикс бинарника `API_`/`ADMIN_`/`WORKER_`/`MIGRATOR_`, default или `required`), строка с комментарием в нужном `.env.example` (`backend/`, `apps/web/`, `apps/admin/`, `deploy/dev/`, `deploy/server/`), рабочее значение в локальном `backend/.env` / `apps/web/.env.local` / `apps/admin/.env.local` / `deploy/dev/.env` (они в .gitignore, `./task setup` создаёт их из шаблонов).
- Шаблоны (`.env.example`) правятся обычными инструментами. Рабочие `.env`, `.env.local`, `.env.*.local` закрыты для инструментов глобальным deny — в этом проекте пользователь разрешил писать их скриптом: .sh в scratchpad → `bash <скрипт>`, значения не выводить.
- Секреты — только плейсхолдеры в `.env.example`; значения не коммитить и не выводить в отчёты.
- Ключ-секрет в шаблоне — плейсхолдер `<base64-32-байта>`: `./task setup` заменит его (`scripts/env-keys.sh`). В уже
  существующий `backend/.env` новый ключ дописывает скрипт: `openssl rand -base64 32` прямо в файл, без вывода.
- Кольцо ключей (`*_SEEDS`, `*_KEYS`, `*_SECRET_KEYS`) — через запятую, первый — текущий. Ротация: новый ключ первым,
  перешифровка (для `ADMIN_STAFF_SECRET_KEYS` — `admin-api staff rekey`), затем старый убрать.
- Кольцо identity — одно значение у `API_IDENTITY_SECRET_KEYS` и `WORKER_IDENTITY_SECRET_KEYS` (письма с кодами
  собирает воркер); `scripts/env-keys.sh` выдаёт обоим один ключ. admin-api кольца identity не получает (спека §5.5).
  Ротация двухфазная: новый ключ сначала **вторым** у api и воркера → после перезапуска всех — **первым** →
  `api identity rekey` (сначала `--dry-run`) → старый ключ убрать, когда под ним нет резервных кодов (отчёт rekey) и
  прошло ≥ 7 дней; нечитаемые строки (код выхода 1) — вернуть ключ в кольцо. `API_IDENTITY_DEVICE_KEYS` — так же;
  MAC устройств переписываются текущим ключом при refresh и входе, старый ключ убирать, когда сессии обновились, и не
  раньше срока жизни refresh-сессий. `API_IPGEO_DB` (mmap) — замена только атомарным `rename` нового файла и
  перезапуском, иначе SIGBUS (`docs/deploy-dev.md`, раздел про identity).
- `API_EMBED_WORKER` (встроенный воркер в процессе api) — **только dev-стенд** Render (`render.yaml`): там `WORKER_*`
  заданы у сервиса `wf-api` (строка роли `worker`, кольцо, SMTP — `sync: false`), `WORKER_LOG_*` не действуют (журнал
  api). Api сверяет кольцо на старте: `WORKER_IDENTITY_SECRET_KEYS` ≠ `API_IDENTITY_SECRET_KEYS` по ключам и порядку —
  отказ старта, ротация — обе переменные вместе. На своём сервере — `false` (`deploy/server/compose.yaml`), `WORKER_*` —
  сервису `worker`. `ADMIN_WAKE_API_URL` — тоже только стенд.
- Шаблоны своего сервера — `deploy/server/{api,admin-api,worker,migrate}.env.example` и `.env.example` (переменные
  compose); рабочие `deploy/server/*.env` — под `.gitignore`. Новый обязательный ключ бинарника — и в шаблон сервиса
  (страж `TestServerEnvExamplesKnownKeys`: ключи известны config, обязательные есть, секреты — `<…>`).
- Хранилище файлов — `*_BLOB_*` и `*_MEDIA_PUBLIC_BASE_URL` у `API_`, `ADMIN_`, `WORKER_` читает отдельный
  `config.LoadBlob(prefix, environ)` (как `Mail` у воркера); `*_BLOB_BACKEND` обязателен: `fs` (dev, e2e: один
  `FS_ROOT` и один `*_BLOB_DEV_SIGN_KEY` у трёх процессов — `scripts/env-keys.sh` выдаёт им общий ключ), `s3` (свой
  сервер), `none` (стенд). `fs` при `WF_BUILD_KIND=prod` (финальная стадия `backend/Dockerfile`) — отказ старта.
- Ключи сотрудников есть только у admin-api (`ADMIN_STAFF_SECRET_KEYS`); воркеру их не заводить (решение D3).
- Во фронт-сборку попадают только публичные значения (`VITE_*`, `NEXT_PUBLIC_*`).
