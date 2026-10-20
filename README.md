# WhereFootball

Приложение для организации дворового футбола: капитан собирает команду и матч,
игрок находит игру своего уровня в своём городе. Первый город — Ставрополь.

Рабочее название, домены не проверены — папку можно переименовать.

## Раскладка репозитория

| Папка | Для чего |
| --- | --- |
| `backend/` | Go-монолит: `cmd/{api,admin-api,worker,migrate}`, `internal/*`, `migrations/` |
| `contracts/` | контракты OpenAPI и токены дизайна — источник истины для кода и фронтов |
| `apps/` | `web` (Next.js) и `admin` (React + Vite) |
| `packages/` | общий код фронтов: `@wf/config`, `@wf/tokens`, `@wf/i18n` |
| `deploy/` | окружение для разработки (роли и расширения БД) |
| `design/` | бриф, экраны, токены — дизайн-пакет, самодостаточен |
| `docs/` | техспека: архитектура, версии, принципы, вынос в отдельный репозиторий |
| `product/` | рынок, монетизация, роадмап — читать самому, отвечает на «почему так» |

`CLAUDE.md` в корне — контекст для Claude Code: стек, жёсткие правила, структура
репозитория и чего делать нельзя. Правила по частям (что уже гарантирует база, фронты,
контракты, окружение) — в `.claude/rules/`, подгружаются при работе с файлами этой части.

## Что решено

| | |
| --- | --- |
| Первый город | Ставрополь или Михайловск |
| Старт | веб-фирст на Next.js, Flutter позже |
| Бэкенд | Go, модульный монолит, PostgreSQL |
| Админка | React + Vite, отдельный домен, тот же VPS |
| Аренда поля | платформа не принимает деньги никогда, только справочный расчёт |
| Выручка | подписка, внутриигровая валюта, косметика |
| Возраст | с 14 лет, капитанство с 18 |

## Состояние

Каркас готов: `scripts/bootstrap.sh` → `./task setup` → `./task dev`;
`./task ci` — все проверки (линт, типы, тесты всех частей, кодогенерация,
накат и откат миграций).

## Запуск всего разом

```
./task dev      # bash
.\task dev      # PowerShell / cmd (обёртка task.cmd)
```

Поднимает Postgres (если не запущен), накатывает миграции и запускает всё в одном окне
с префиксами логов; Ctrl+C останавливает сервисы, Postgres остаётся работать
(`./task infra:down` — выключить).

| Что | Адрес |
| --- | --- |
| веб | http://127.0.0.1:3000 |
| админка | http://127.0.0.1:5173 |
| api / admin-api | http://127.0.0.1:8080/healthz и /readyz, http://127.0.0.1:8081/healthz и /readyz |
| письма (Mailpit) | http://127.0.0.1:18025 |

Первый владелец админки — команда оператора (письмо с кодом отправит запущенный admin-api в Mailpit):
`./task backend:admin:staff -- bootstrap --email owner@wf.local --name "Владелец" --print-code`. Все команды
оператора (`invite-resend`, `reset-access`, `disable`, `rekey`) и ротация ключей — `docs/deploy-dev.md`, раздел
«admin-api».

### Живой e2e админки

`./task admin:e2e` (аргументы Playwright — после `--`: `./task admin:e2e -- --headed`) — сценарий спеки
сотрудников §11 в браузере поверх настоящих admin-api, Postgres и Mailpit: bootstrap двух владельцев, принятие
приглашения и 2FA, выход и вход, приглашение админа вдвоём, журнал; вход с протухшей cookie — в Chromium и WebKit.
Нужен запущенный Postgres (`./task infra:up`); `./task dev` можно не останавливать — у e2e свои порты и база:

| Что | Порт / база |
| --- | --- |
| Vite (Chromium / WebKit) | 5174 / 5175 (= `ADMIN_ORIGIN` своей пары) |
| admin-api (Chromium / WebKit) | 18181 / 18182 (у WebKit `ADMIN_COOKIE_SECURE=false`: `__Host-` на http он не принимает) |
| Mailpit UI / SMTP | 18026 / 11026 (без `--database` — пустой ящик на прогон) |
| база | `wf_e2e` на том же кластере (пересоздаётся в начале прогона) |

Скрипт `scripts/e2e-admin.sh` проверяет, что порты свободны, пересоздаёт базу, накатывает миграции и права,
собирает admin-api в `.tools/e2e/` и задаёт одноразовый `ADMIN_STAFF_SECRET_KEYS`; dev-значения `ADMIN_*` из
`backend/.env` в прогон не попадают. Браузеры — `pnpm --filter @wf/admin exec playwright install chromium webkit`;
только Chromium — `WF_E2E_PROJECTS=chromium ./task admin:e2e` (так в CI). Порты e2e — константы
`apps/admin/e2e/env.ts` и список в `scripts/e2e-admin.sh`.

Дальше: вертикальный срез на вебе — регистрация, команда, матч, ссылка,
запись, протокол.

До кода стоит поговорить с 8–10 капитанами в Ставрополе. Схема от этого
не изменится, а половина продуктовых решений может.

## База без Docker

На машине разработчика Docker/WSL не работает, поэтому `scripts/pg.sh` разворачивает
PostgreSQL + PostGIS из официальных zip-архивов прямо в `.tools/pg` — без прав
администратора и без установки службы. Системный PostgreSQL (если он есть) не трогается.

```
scripts/pg.sh install   # скачать и распаковать (один раз, сверяет SHA256)
scripts/pg.sh init      # создать кластер в .tools/pg/data (один раз)
scripts/pg.sh start     # поднять на порту WF_PG_PORT (окружение → deploy/dev/.env → 15432)
scripts/pg.sh port      # какой порт возьмёт скрипт
scripts/pg.sh stop      # остановить, данные остаются
```

Кластер одноразовый, поэтому `init` выключает `fsync`, `full_page_writes` и `synchronous_commit`.
На Windows каждый `DROP DATABASE` делает принудительный checkpoint; с fsync очистка клонов баз в
тестах встаёт в очередь до таймаута. Кластер, созданный раньше, переключается так:
`scripts/pg.sh psql -c "ALTER SYSTEM SET fsync = off" -c "ALTER SYSTEM SET full_page_writes = off"
-c "ALTER SYSTEM SET synchronous_commit = off" -c "SELECT pg_reload_conf()"`. После сбоя ОС
кластер может испортиться: `stop`, удалить `.tools/pg/data`, `init`. Тесты бэкенда идут с `-p 4`
(`./task backend:test`): так они быстрее, чем все пакеты разом.

Точные версии — `docs/versions.md`. На Linux/macOS вместо скрипта используется системный
PostgreSQL + PostGIS на том же порту (Docker — только в CI и на сервере).

`./task backend:db:reset` (откат всех миграций и накат заново) работает только против
локальной базы: команда отказывается выполняться, если хост в `MIGRATOR_DATABASE_URL`
не `localhost`/`127.0.0.1`/`::1` — это защита от случайного сноса прод-базы.

## Если порт занят

На части машин Hyper-V резервирует часть портов из динамического диапазона Windows,
который по умолчанию начинается с 1024 — это может конфликтовать с портами из таблицы
ниже. Лечится (нужны права администратора):

```
netsh int ipv4 set dynamicport tcp start=49152 num=16384
```

Все порты каркаса — ниже 49152. Одной переменной меняется не каждый: ниже — все места,
которые надо поправить вместе (локальные файлы создаёт `./task setup` из `.env.example`).
CI поднимает свою базу на 15432 (`.github/workflows/backend.yml`) и от этих правок не зависит.

| Сервис | Порт | Где менять — все места |
| --- | --- | --- |
| Postgres | 15432 | `WF_PG_PORT` в `deploy/dev/.env` — его читают `scripts/pg.sh` (и напрямую, и через task) и тесты бэкенда (`WF_TEST_PG_PORT` → `WF_PG_PORT` → 15432; `WF_PG_PORT` в окружение подставляет корневой Taskfile, при голом `go test` задать самому); **плюс** порт в четырёх `*_DATABASE_URL` в `backend/.env` |
| api | 8080 | `API_HTTP_ADDR` в `backend/.env` **и** `WF_API_ORIGIN` в `apps/web/.env.local` (куда Next проксирует `/v1`) |
| admin-api | 8081 | `ADMIN_HTTP_ADDR` в `backend/.env` **и** `WF_ADMIN_API_ORIGIN` в `apps/admin/.env.local` (куда Vite проксирует `/v1`) |
| веб (Next.js) | 3000 | скрипты `dev` и `start` в `apps/web/package.json`; сервер e2e — `WF_WEB_PORT` в окружении процесса (`WF_WEB_PORT=3100 pnpm e2e`; по умолчанию 3000; `baseURL`, `webServer.url` и команда в `apps/web/playwright.config.ts` берут его оттуда) |
| админка (Vite) | 5173 | `server.port` в `apps/admin/vite.config.ts` |
| Mailpit UI / SMTP | 18025 / 11025 | литералы в задаче `mail` корневого `Taskfile.yml` |
| e2e админки | 5174/5175, 18181/18182, 18026/11026 | `PAIRS` и `MAILPIT` в `apps/admin/e2e/env.ts` **и** список `ports` в `scripts/e2e-admin.sh` |

Порты в описаниях задач (`./task --list`) и в таблице адресов выше — справочные, их при
смене порта править не обязательно.

## Переменные окружения

Новая переменная добавляется сразу, одним коммитом: поле в
`backend/internal/platform/config` (с префиксом бинарника — `API_`, `ADMIN_`, `WORKER_`
или `MIGRATOR_`) + строка в соответствующем `.env.example` + значение в локальном файле.
Секреты — только плейсхолдерами, в `.env.example` их не бывает настоящими. Корневого `.env`
нет: у каждой части свой `.env.example` (`backend/`, `apps/web/`, `apps/admin/`,
`deploy/dev/`), локальный файл рядом с ним не коммитится (`backend/.env`,
`apps/web/.env.local`, `apps/admin/.env.local`, `deploy/dev/.env`) — их создаёт `./task setup`.

Ключи `API_JWT_SEEDS`, `API_HUMANCHECK_KEYS` и `ADMIN_STAFF_SECRET_KEYS` без значения (переменной нет
или она пуста) не дают api и admin-api стартовать. `./task setup`, создавая `backend/.env`, сразу
заменяет их плейсхолдеры (`<base64-32-байта>`) случайными ключами — `scripts/env-keys.sh`
(значения не выводит). Файл собирается во временном `backend/.env.tmp` и встаёт на место только
целиком: при сбое (нет `openssl`) `backend/.env` не появится, и повторный `./task setup` сделает
шаг заново. Уже существующий `backend/.env` setup не трогает: остались плейсхолдеры —
`bash scripts/env-keys.sh backend/.env`. `API_BFF_*` пусты до спеки веба. В уже существующий
`backend/.env` новая переменная с ключом дописывается скриптом, который генерирует значение и не
выводит его (`.claude/rules/env.md`).

## Про исходный документ

`docs/00-полный-документ.md` — исходный документ, из которого собраны все
остальные файлы. Это снимок: он не ведётся и с кодом не сверяется.
Авторитетны `docs/01`–`docs/05`, спеки в `docs/superpowers/specs/` и код.
