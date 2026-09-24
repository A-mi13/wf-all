# WhereFootball

Дворовой футбол: капитан собирает команду и матч, игрок находит игру своего уровня в своём городе.

## Stack
Go 1.27 (chi, pgx + sqlc, goose, River) · PostgreSQL 18 + PostGIS · Next.js 16 (`apps/web`) · React 19 + Vite 8 (`apps/admin`) · pnpm 12 workspace · go-task; Flutter — отдельный репо wf-native. Точные версии — `docs/versions.md`.

## Hard rules
1. Деньги за аренду платформа не принимает никогда: `rent_total_minor` — справочный расчёт, `payment_notes` — заметки капитана. Эквайринг, эскроу, кошельки, «оплата матча» не добавлять; выручка — подписки и валюта через штатные IAP (`docs/01-домен-и-правила.md`).
2. Суммы — `int64` в минорных единицах + ISO 4217, без float; валюта — из страны города, не выбирается руками (`backend/migrations/README.md`).
3. Время — `timestamptz` в UTC; матч «в 20:00» — время поля, таймзона у города/поля (`docs/02-архитектура.md`).
4. Роли — `role_assignments` со скоупом, не колонка в `users` (`backend/migrations/0003_identity.sql`).
5. Мутирующие запросы принимают `Idempotency-Key`; события пишутся в `outbox` в той же транзакции, что и данные (`backend/migrations/0001_platform.sql`).
6. Строки UI, включая админку, — только в `apps/*/messages/{ru,en}.json`; `en` с теми же ключами (`@wf/i18n`, тесты локалей).
7. TDD: сначала падающий тест, потом код — бэк и фронт; страж проверяется подсадкой бага (`docs/04-принципы-архитектуры.md`).
8. Версии — последние стабильные; отклонение только с записью в исключения `docs/versions.md`.

## File structure
- `backend/` — Go-монолит: `cmd/{api,admin-api,worker,migrate}`, `internal/platform/*` (общее), `internal/httpapi/{public,admin}`, `migrations/`
- `contracts/` — OpenAPI (`public.yaml`, `admin.yaml`) и `tokens/tokens.json` — источник истины
- `apps/web`, `apps/admin` — фронты; `packages/` — `@wf/config`, `@wf/tokens`, `@wf/i18n`
- `deploy/dev/` — роли и расширения БД; `scripts/` — `bootstrap.sh`, `pg.sh`, `doctor.sh`
- `design/` — бриф, `screens/*.dc.html`, `tools/extract-export.mjs`; `product/` — «почему так»
- Доки: `docs/01-домен-и-правила.md`, `docs/02-архитектура.md`, `docs/04-принципы-архитектуры.md`
- Правила по частям репо — `.claude/rules/` (грузятся при чтении файлов под их `paths`)

## Skills
Проектных нет: справочники восстанавливаются из `backend/migrations/README.md` и `design/README.md`.

## What NOT to do
- Сейчас НЕ делаем: чат (хватает комментариев под матчем с лимитом), турниры, городские рейтинги, подписки, кредиты, косметику, прогнозы, второй язык интерфейса. Схема под них есть, фича-флаги в сиде выключены.
- Системный PostgreSQL (служба `postgresql-x64-18`, :5432) не трогать. Docker/WSL на машине не работают — Docker только в CI.
- Два pnpm-процесса одновременно не запускать: pnpm 12 при запуске скриптов сам доустанавливает воркспейс.
- Временные worktree/копии проекта с установкой зависимостей и `rm -rf` вне проекта — запрещены (глобальный CLAUDE.md).
- `apps/web/AGENTS.md` и `apps/web/CLAUDE.md` пишет `next dev` — не удалять и не править.

## Git policy
- Коммит — одна строка по-русски, без тела и Co-Authored-By. Пуш — только по команде пользователя; `push --force` — никогда.
- В папке параллельно работают другие агенты: коммитить только свои пути — `git add <пути> && git commit -m "…" -- <пути>`.
- Никогда: `git add -A` / `git add .` / `git commit -a`, `git stash`, `git reset`, `git clean`, `git checkout -- .`.
- lefthook pre-commit (prettier, gofmt, gitleaks) не обходить через `--no-verify`.

## Naming
- Технический слаг — `wf` (Go-модуль `wf/backend`, npm `@wf/*`, префикс CSS `--wf-*`). Бренд не финален («ГДЕФУТБОЛ» в дизайне, WhereFootball в доках) — только в локалях и конфиге, не в идентификаторах.
- Комментарии в коде — по-русски, идентификаторы — по-английски. Переводы строк LF.

## Проверки
- Тесты гонять самому после правок: `./task backend:test` (нужен Postgres), `./task web:test`, `./task admin:test`, `./task packages:ci`, `./task design:test`; e2e веба — `./task web:e2e`.
- `./task ci` включает lint/typecheck/build — как и они, только по просьбе (глобальный CLAUDE.md).
- Кодогенерация — `./task gen`; сгенерированное коммитится (`gen:check` в CI сверяет).
- Локальный Postgres 18 + PostGIS без Docker: `bash scripts/pg.sh install|init|start|stop|status|psql` → 127.0.0.1:15432 (`.tools/pg`).
- `node --test` — только с глобом в кавычках: `node --test "dir/*.test.mjs"` (каталог на этой Node не сканируется).
