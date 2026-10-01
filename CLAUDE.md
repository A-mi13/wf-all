# WhereFootball

Дворовой футбол: капитан собирает команду и матч, игрок находит игру своего уровня в своём городе.

## Stack
Go 1.27 (chi, pgx + sqlc, goose, River) · PostgreSQL 18 + PostGIS · Next.js 16 (`apps/web`) · React 19 + Vite 8 (`apps/admin`) · pnpm 12 workspace · go-task; Flutter — отдельный репо wf-native. Точные версии — `docs/versions.md`.

## Hard rules
1. Деньги за аренду платформа не принимает никогда: `rent_total_minor` — справочный расчёт, `payment_notes` — заметки капитана. Эквайринг, эскроу, кошельки, «оплата матча» не добавлять; выручка — подписки и валюта через штатные IAP (`docs/01-домен-и-правила.md`).
2. Суммы — `int64` в минорных единицах + ISO 4217, без float; валюта — из страны города, не выбирается руками (`backend/migrations/README.md`).
3. Время — `timestamptz` в UTC; матч «в 20:00» — время поля, таймзона у города/поля (`docs/02-архитектура.md`).
4. Роли — `role_assignments` со скоупом, не колонка в `users` (`backend/migrations/0003_identity.sql`).
5. Мутирующие запросы принимают `Idempotency-Key` (кроме анонимных: регистрация, вход, коды); события пишутся в `outbox` в той же транзакции, что и данные (спека бэкенда §6.4–6.5).
6. Строки UI, включая админку, — только в `apps/*/messages/{ru,en}.json`; тексты писем и пушей — в `backend/locales/{ru,en}.json`; `en` с теми же ключами (`@wf/i18n`, тесты локалей).
7. TDD: сначала падающий тест, потом код — бэк и фронт; страж проверяется подсадкой бага (`docs/04-принципы-архитектуры.md`).
8. Версии — последние стабильные; отклонение только с записью в исключения `docs/versions.md`.

## File structure
- `backend/` — Go-монолит: `cmd/{api,admin-api,worker,migrate}`, `internal/platform/*` (общее), `internal/httpapi/{public,admin}`, `migrations/`
- `contracts/` — OpenAPI: исходники `openapi/{public,admin}/` по модулям, бандлы `public.yaml`, `admin.yaml` (сгенерированы) — источник истины; `tokens/tokens.json`
- `apps/web`, `apps/admin` — фронты; `packages/` — `@wf/config`, `@wf/tokens`, `@wf/i18n`
- `deploy/dev/` — роли и расширения БД; `scripts/` — `bootstrap.sh`, `pg.sh`, `doctor.sh`, `check-format.sh` (pre-commit)
- `design/` — бриф, `screens/*.dc.html`, `tools/extract-export.mjs`; `product/` — «почему так»
- Доки: `docs/01-домен-и-правила.md`, `docs/02-архитектура.md`, `docs/04-принципы-архитектуры.md`
- Архитектура бэкенда (модули, владение таблицами, платформа, стражи, порядок): `docs/superpowers/specs/2026-10-01-backend-architecture-design.md`
- Правила по частям репо — `.claude/rules/` (грузятся при чтении файлов под их `paths`)

## Skills
Проектных нет: справочники восстанавливаются из `backend/migrations/README.md` и `design/README.md`.

## What NOT to do
- Не урезать объём и не обходить доменные правила временно (автоодобрение и т. п.): строим весь продукт, включая турниры, рейтинги, подписки, кредиты, косметику, прогнозы, чат, второй язык; выключенное — за фича-флагами по городам (пользователь, 01.10.2026).
- Системный PostgreSQL (служба `postgresql-x64-18`, :5432) не трогать. Docker/WSL на машине не работают — Docker только в CI.
- Два pnpm-процесса одновременно не запускать, `pnpm install` — тем более. Скрипты не доустанавливают воркспейс (`verifyDepsBeforeRun: error`): `ERR_PNPM_VERIFY_DEPS_BEFORE_RUN` — сделать один `pnpm install`.
- Временные worktree/копии проекта с установкой зависимостей и `rm -rf` вне проекта — запрещены (глобальный CLAUDE.md).
- `apps/web/AGENTS.md` и `apps/web/CLAUDE.md` пишет `next dev` — не удалять и не править.

## Git policy
- В этом проекте коммитить по ходу работы разрешено (пользователь, 24.09.2026); формат коммита, пуш и запреты — по глобальному CLAUDE.md.
- В папке параллельно работают другие агенты: коммитить только свои пути — `git add <пути> && git commit -m "…" -- <пути>`.
- Никогда: `git add -A` / `git add .` / `git commit -a`, `git stash`, `git reset`, `git clean`, `git checkout -- .`.
- pre-commit (lefthook) только проверяет: неотформатированное (prettier, gofmt) и секреты (gitleaks) отклоняют коммит, команда исправления — в сообщении хука.

## Naming
- Технический слаг — `wf` (Go-модуль `wf/backend`, npm `@wf/*`, префикс CSS `--wf-*`). Бренд не финален («ГДЕФУТБОЛ» в дизайне, WhereFootball в доках) — только в локалях и конфиге, не в идентификаторах.
- Комментарии в коде — по-русски, идентификаторы — по-английски. Переводы строк LF.

## Проверки
- Тесты гонять самому после правок: `./task backend:test` (нужен Postgres), `./task web:test`, `./task admin:test`, `./task packages:ci`, `./task design:test`; e2e веба — `./task web:e2e`.
- `./task ci` включает lint/typecheck/build — как и они, только по просьбе (глобальный CLAUDE.md).
- Кодогенерация — `./task gen`; сгенерированное коммитится (`gen:check` в CI сверяет).
- Локальный Postgres 18 + PostGIS без Docker: `bash scripts/pg.sh install|init|start|stop|status|psql|port` → 127.0.0.1:`WF_PG_PORT` из `deploy/dev/.env` (по умолчанию 15432, `.tools/pg`).
- `node --test` — только с глобом в кавычках: `node --test "dir/*.test.mjs"` (каталог на этой Node не сканируется).
