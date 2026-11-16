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

- Нужен новый ключ → в ТОМ ЖЕ коммите: поле в `backend/internal/platform/config` (префикс бинарника `API_`/`ADMIN_`/`WORKER_`/`MIGRATOR_`, default или `required`), строка с комментарием в нужном `.env.example` (`backend/`, `apps/web/`, `apps/admin/`, `deploy/dev/`), рабочее значение в локальном `backend/.env` / `apps/web/.env.local` / `apps/admin/.env.local` / `deploy/dev/.env` (они в .gitignore, `./task setup` создаёт их из шаблонов).
- Шаблоны (`.env.example`) правятся обычными инструментами. Рабочие `.env`, `.env.local`, `.env.*.local` закрыты для инструментов глобальным deny — в этом проекте пользователь разрешил писать их скриптом: .sh в scratchpad → `bash <скрипт>`, значения не выводить.
- Секреты — только плейсхолдеры в `.env.example`; значения не коммитить и не выводить в отчёты.
- Ключ-секрет в шаблоне — плейсхолдер `<base64-32-байта>`: `./task setup` заменит его (`scripts/env-keys.sh`). В уже
  существующий `backend/.env` новый ключ дописывает скрипт: `openssl rand -base64 32` прямо в файл, без вывода.
- Кольцо ключей (`*_SEEDS`, `*_KEYS`, `*_SECRET_KEYS`) — через запятую, первый — текущий. Ротация: новый ключ первым,
  перешифровка (для `ADMIN_STAFF_SECRET_KEYS` — `admin-api staff rekey`), затем старый убрать.
- Кольцо identity — одно значение у `API_IDENTITY_SECRET_KEYS` и `WORKER_IDENTITY_SECRET_KEYS` (письма с кодами
  собирает воркер); `scripts/env-keys.sh` выдаёт обоим один ключ. admin-api кольца identity не получает (спека §5.5).
  Ротация: новый ключ первым у api и воркера одновременно → `api identity rekey` (сначала `--dry-run`) → старый ключ
  убрать, когда под ним нет резервных кодов (отчёт rekey) и прошло ≥ 7 дней; нечитаемые строки (код выхода 1) —
  вернуть ключ в кольцо. `API_IDENTITY_DEVICE_KEYS` — новый первым, HMAC устройств не пересчитывается
  (`docs/deploy-dev.md`, «Ротация `API_IDENTITY_SECRET_KEYS`»).
- Ключи сотрудников есть только у admin-api (`ADMIN_STAFF_SECRET_KEYS`); воркеру их не заводить (решение D3).
- Во фронт-сборку попадают только публичные значения (`VITE_*`, `NEXT_PUBLIC_*`).
