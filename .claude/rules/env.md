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

- Нужен новый ключ → в ТОМ ЖЕ коммите: поле в `backend/internal/platform/config` (префикс бинарника `API_`/`ADMIN_`/`WORKER_`/`MIGRATOR_`, default или `required`), строка с комментарием в нужном `.env.example` (`backend/`, `apps/web/`, `apps/admin/`, `deploy/dev/`), рабочее значение в локальном `backend/.env` / `apps/web/.env.local` / `deploy/dev/.env` (они в .gitignore).
- Шаблоны (`.env.example`) правятся обычными инструментами. Рабочие `.env`, `.env.local`, `.env.*.local` закрыты для инструментов глобальным deny — в этом проекте пользователь разрешил писать их скриптом: .sh в scratchpad → `bash <скрипт>`, значения не выводить.
- Секреты — только плейсхолдеры в `.env.example`; значения не коммитить и не выводить в отчёты.
- Во фронт-сборку попадают только публичные значения (`VITE_*`, `NEXT_PUBLIC_*`).
