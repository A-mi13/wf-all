# Структура репозиториев и каркас — дизайн

Дата: 2026-09-24. Статус: на ревью у пользователя.
Этап: каркас репозиториев, инструментов и тестовой инфраструктуры. Бизнес-код бэкенда,
экраны веба и админки, Flutter-приложение и деплой — отдельные спеки после этой.

## 1. Цель и критерий готовности

Бэкенд, веб, админка и мобильное приложение разделены так, чтобы не путаться при разработке
и чтобы любую часть можно было вынести в отдельный репозиторий механической операцией.

Этап готов, когда на чистой машине разработчика (Windows 10, Git Bash):

1. `scripts/bootstrap.sh` → `task setup` проходит без ручных шагов;
2. `task dev` поднимает `api`, `admin-api`, `worker`, веб и админку;
3. `GET /v1/health` отвечает у `api` и `admin-api`, веб и админка показывают этот статус
   строкой из файла локализации;
4. `task ci` зелёный: линт, типы, тесты всех частей, проверка кодогенерации,
   накат и откат миграций;
5. каждая строка кода каркаса появилась после падающего теста.

## 2. Принятые решения

| Вопрос | Решение |
| --- | --- |
| Технический слаг | `wf`. Бренд не окончательный («ГДЕФУТБОЛ» в макетах, WhereFootball в доках) — живёт только в локалях и конфиге |
| Репозитории | `A-mi13/wf-all` — монорепо (локально `F:\ideas\wherefootball`); `A-mi13/wf-native` — Flutter (локально `F:\ideas\wf-native`) |
| Версии | последние стабильные; если экосистема не поддерживает — последняя совместимая, причина и условие апгрейда в `docs/versions.md` |
| Разработка | TDD везде: падающий тест → минимальная реализация → зелёный → рефакторинг. Бэк и фронт |
| Раскладка | по ролям: `backend/`, `contracts/`, `apps/*`, `packages/*` |
| Локализация | строки только в файлах локализации с первого коммита; `ru` заполнен, `en` с теми же ключами (пустое значение → fallback на `ru`); переключателя языка нет |
| OpenAPI | 3.0.3 (поддержка 3.1 в oapi-codegen 2.8 — «initial») |
| Git | коммиты одной строкой по-русски; пуш только по команде пользователя |

## 3. Структура wf-all

```
F:\ideas\wherefootball\                 репо wf-all
├─ backend/                             Go, module wf/backend
│  ├─ go.mod                            go 1.27, toolchain go1.27.1
│  ├─ tools/go.mod                      goose, sqlc, oapi-codegen, river CLI — отдельно от графа бэкенда
│  ├─ cmd/api  cmd/admin-api  cmd/worker
│  ├─ internal/{identity,geo,teams,matches,stats,reputation,economy,notify,moderation,platform}
│  ├─ internal/httpapi/public/gen       сгенерировано из contracts/openapi/public.yaml
│  ├─ internal/httpapi/admin/gen        сгенерировано из contracts/openapi/admin.yaml
│  ├─ migrations/                       переезжает из корня + миграция таблиц River
│  ├─ oapi-codegen.*.yaml  sqlc.yaml
│  ├─ Taskfile.yml  .env.example
├─ contracts/                           нейтральная территория, пакет @wf/contracts (private)
│  ├─ openapi/public.yaml               API приложений и веба
│  ├─ openapi/admin.yaml                отдельный контракт admin-api
│  ├─ tokens/tokens.json                токены из макета «Токены»
│  ├─ scripts/                          генерация TS-клиентов и tokens.css в приложения
│  ├─ package.json                      openapi-typescript + свой typescript 5.9.3
│  └─ Taskfile.yml
├─ apps/
│  ├─ web/                              Next.js
│  │  ├─ src/api/gen/                   клиент из public.yaml (коммитится)
│  │  ├─ src/styles/tokens.css          из tokens.json (коммитится)
│  │  ├─ messages/{ru,en}.json
│  │  └─ Taskfile.yml  .env.example  package.json (engines)
│  └─ admin/                            React + Vite, та же раскладка, клиент из admin.yaml
├─ packages/
│  └─ config/                           @wf/config: базовые tsconfig, eslint, prettier. Бизнес-логики нет
├─ deploy/dev/
│  ├─ compose.yml                       Postgres 18 + PostGIS 3.6, Mailpit
│  └─ initdb/*.sql                      роли migrator/api/admin/worker, базы wf, wf_test
├─ design/
│  ├─ screens/*.dc.html                 18 экранов из экспорта канваса (без битых @font-face)
│  ├─ tools/extract-export.mjs          повторное извлечение при обновлении макетов
│  └─ _export/                          исходный бандл, в .gitignore
├─ docs/  product/
├─ scripts/bootstrap.sh                 ставит task и бинарники фиксированных версий в .tools/bin
├─ Taskfile.yml                         только includes частей + setup/dev/ci
├─ package.json  pnpm-workspace.yaml    packageManager pnpm@12.6.0, devEngines.runtime node 26
├─ lefthook.yml  .editorconfig  .gitattributes  .gitignore
├─ .github/workflows/{backend,web,admin,contracts}.yml
└─ CLAUDE.md  README.md
```

## 4. Границы

1. `apps/web` и `apps/admin` не импортируют друг друга. Общие у них только `@wf/config`.
2. Клиент админского API существует только внутри `apps/admin` — веб физически не может
   подтянуть админские ручки в публичный бандл.
3. `packages/*` не импортируют `apps/*` и не содержат бизнес-логики.
4. `backend/` ничего не знает о JS; связь с фронтами — только через `contracts/`.
5. `cmd/api` не линкует ничего админского. Страж-тест падает, если в `go list -deps ./cmd/api`
   есть пакет с `/admin` в пути. Разделение админских сценариев внутри модулей — в спеке бэкенда.
6. Никаких относительных путей наверх (`../../`) в конфигах приложений — только пакеты.
   Исключение одно: пути к `contracts/` в конфигах кодогенерации (время сборки, не рантайм).

Проверка: плагин границ в ESLint (eslint-plugin-boundaries), страж-тест в Go, правила
модулей в golangci-lint (детали — спека бэкенда).

## 5. Контракты и кодогенерация

- Источник истины — `contracts/`. Код генерируется из YAML, не наоборот.
- Go: oapi-codegen (chi strict server) с `embedded-spec` — спека вшита в бинарник, в рантайме
  нет путей к файлам. Она же используется валидатором ответов в тестах.
- TS: `contracts/scripts` запускает openapi-typescript (со своим TypeScript 5.9.3 — у генератора
  peer `typescript ^5.x`) и пишет результат в `apps/*/src/api/gen`. Рантайм-клиент — openapi-fetch.
- Весь сгенерированный код коммитится. `task ci` выполняет `task gen` и `git diff --exit-code`.
- Ошибки — `application/problem+json` (RFC 9457) со стабильным полем `code`
  (`match.slot_taken`). Клиенты переводят по коду; сервер не шлёт человеческих текстов.
- Пути с `/v1`. Релизы публичного контракта — тег `contracts-vX.Y.Z` (только `public.yaml`).
  Админскому контракту теги не нужны: его единственный потребитель в этом же репо.
- Гейт ломающих изменений (oasdiff) включается, когда у публичного контракта появится
  первый внешний потребитель (wf-native). До тех пор — отложено.

## 6. Токены дизайна

Источник — `contracts/tokens/tokens.json`, значения взяты из артборда «Токены»:
Oswald (цифры, заголовки) + Manrope (текст); `bg #0B0F0D`, `elevated #141A17`, `pitch #16241C`,
`border #253329`, `accent #C6F24E`, `success #4ADE80`, `warning #E8B858`, `danger #F97066`,
`text #EAF2EC`, `muted #93A399`; кнопка 48 / радиус 14; шаг отступов 4. Тёмная тема основная,
светлая производная. Скрипт в `contracts/scripts` генерирует `tokens.css` (CSS-переменные)
в каждое приложение. Style Dictionary не используем — для одного JSON это лишний слой.
`design/03-токены.md` обновляется: «один шрифт» устарел, источник значений — `tokens.json`.

## 7. Локализация

- Формат сообщений ICU, файлы `apps/*/messages/{ru,en}.json`.
- Веб — next-intl, админка — use-intl (ядро next-intl): одинаковый API в обоих приложениях.
- `en` содержит те же ключи, что `ru`; пустое значение отдаётся как `ru`. Тест в каждом
  приложении падает, если наборы ключей расходятся.
- Переключателя языка нет. Язык берётся из настроек, по умолчанию `ru`.
- Тексты пушей и писем — у бэкенда, по `users.locale` (спека бэкенда).

## 8. Локальная среда

- **Установка без глобальных изменений.** Go — `toolchain go1.27.1` в `go.mod` (GOTOOLCHAIN=auto
  скачает сам). pnpm — поле `packageManager`. Node 26 — `devEngines.runtime` в `package.json`
  (pnpm скачает Node для проекта; глобальный Node 24 через nvm-windows не трогаем).
  fnm не ставим. `scripts/bootstrap.sh` ставит в `.tools/bin` (в `.gitignore`) go-task,
  golangci-lint и gitleaks фиксированных версий через `GOBIN=... go install`.
- **Инфраструктура** — `deploy/dev/compose.yml`: `postgis/postgis:18-3.6`, именованный том
  на `/var/lib/postgresql` (в образе PG18 путь данных изменился), Mailpit для писем с кодами.
  Valkey в каркас не входит — идемпотентность и очередь River живут в Postgres; добавим
  в спеке бэкенда, если понадобится.
- **Роли БД с первого дня:** `migrator` (владелец схемы), `api`, `admin`, `worker` с раздельными
  правами. Ошибки GRANT всплывают в dev, а не на проде. Точные права — спека бэкенда.
- **Порты — через env, все ниже 49152.** По умолчанию: Postgres `15432`, Mailpit `18025`/`11025`,
  api `8080`, admin-api `8081`, веб `3000`, админка `5173`. На этой машине Hyper-V резервирует
  блоки из динамического диапазона, который начинается с 1024, поэтому README описывает
  `netsh int ipv4 set dynamicport tcp start=49152 num=16384` (нужны права администратора).
- **Команды.** Корневой `Taskfile.yml` подключает Taskfile частей через `includes`:

| Команда | Что делает |
| --- | --- |
| `task setup` | pnpm install, compose up, миграции, роли |
| `task dev` | api, admin-api, worker, веб, админка параллельно, лог с префиксами |
| `task gen` | Go-сервер, TS-клиенты, tokens.css, sqlc |
| `task test` / `task lint` | все части; точечно — `task backend:test`, `task admin:test` |
| `task ci` | всё, что делает CI; именно это означает «CI проходит локально» |
| `task db:migrate` / `task db:reset` | goose против dev-базы |

  Каждое приложение запускается и без Taskfile (`go run ./cmd/api`, `pnpm dev`).
- **Dev-хосты:** админка на `admin.localhost`, куки админки и приложения с разными именами —
  куки на localhost не разделяются по портам. Детали — спека админки.

## 9. Конфиг и секреты

- Свой `.env.example` у `backend/`, `apps/web/`, `apps/admin/`. Корневого `.env` нет.
- Go читает конфиг в типизированную структуру и падает на старте, если чего-то не хватает.
  Каждый бинарник получает только свои секреты.
- Во фронтах секретов нет. Админка — статика, в сборку попадают только публичные значения.
- gitleaks — в pre-commit и в `task ci`.

## 10. Тестирование (TDD)

| Слой | Инструмент | Что пишется первым |
| --- | --- | --- |
| Go, логика модулей | `go test` | тест правила |
| Go, БД | интеграционные тесты на реальном Postgres 18 | тест, что констрейнт превращается в `problem+json` с нужным `code` |
| Go, HTTP | тесты хендлеров + валидатор ответов по вшитой спеке | тест «ответ соответствует контракту» |
| Миграции | накат и откат на отдельной чистой базе | тест up → down → up |
| SQL-запросы | `sqlc generate` | первый тест каркаса: sqlc разбирает текущие 10 миграций |
| Фронты, логика и клиентские компоненты | Vitest + Testing Library | тест поведения глазами пользователя |
| Фронты, сеть | MSW, моки типизированы из контракта | мок до экрана |
| Next, серверные компоненты | Playwright (Vitest их не рендерит) | smoke-сценарий |

**Тестовая база:** база-шаблон `wf_template` мигрируется один раз, каждый тестовый пакет
получает свой клон `CREATE DATABASE … TEMPLATE` (паттерн pgtestdb). Так `go test ./...`
безопасно гоняет пакеты параллельно, а тесты гонок и `EXCLUDE` работают с настоящими коммитами.
Код доступа к БД принимает интерфейс `DBTX`.

Быстрый цикл: `task backend:test:watch`, `pnpm test --watch`. Покрытие считаем, порога нет.

## 11. Качество, хуки, CI

- `.gitattributes`: `* text=auto eol=lf` — в первом же коммите (в системном gitconfig
  `core.autocrlf=true`). Скрипты БД пишутся в `.sql`, не в `.sh`.
- Go: golangci-lint v2, gofmt/goimports.
- Фронты: ESLint 9 (flat config) + Prettier + eslint-plugin-boundaries.
- lefthook (npm-пакет в корне): pre-commit — форматирование изменённых файлов и gitleaks.
  Тяжёлые проверки — в `task ci`, не в хуках.
- GitHub Actions: `backend.yml`, `web.yml`, `admin.yml`, `contracts.yml` с фильтрами по путям.
  Каждый workflow только вызывает `task <часть>:ci` — логика проверок не дублируется в YAML.
- `pnpm-workspace.yaml`: `allowBuilds` для пакетов со скриптами установки (lefthook, esbuild,
  unrs-resolver, msw, @parcel/watcher). pnpm по умолчанию не ставит версии младше суток —
  оставляем как защиту от атак через свежие пакеты.

## 12. wf-native на этом этапе

`git init`, remote `A-mi13/wf-native`, README и CLAUDE.md, где описан механизм:
`contract.lock` с тегом `contracts-vX.Y.Z` → копия `public.yaml` и `tokens.json` в `contract/`
(коммитится) → генерация Dart-клиента и токенов. `flutter create`, выбор генератора Dart и скрипт
синхронизации — в мобильной спеке. Обновление Flutter до 3.47.5 глобальное — спросить перед ним.

## 13. Перенос текущего и обновление документов

- `git init` в `F:\ideas\wherefootball`, ветка `main`, remote `origin` → `A-mi13/wf-all`, без пуша.
- `migrations/` → `backend/migrations/`, SQL не меняется. Первым тестом — накат и откат на PG18.
- Бандл `docs/WhereFootball — экраны приложения.html` → `design/_export/` (не удаляется).
- Обновить: `CLAUDE.md` (пути, PostgreSQL 18, Go 1.27, строка «без дизайна» — теперь по макетам,
  локализация по п. 7), `README.md`, `docs/02-архитектура.md` (структура, PG),
  `backend/migrations/README.md`, `design/README.md` (ссылка на канвас, извлечение),
  `design/03-токены.md`.
- Новые: `docs/versions.md`, `docs/03-вынос-в-отдельный-репо.md`.
- После каркаса — настройка Claude по `~/.claude/BOOTSTRAP_PROMPT.md`: slim `CLAUDE.md`,
  `.claude/rules/` с `paths` для backend, web, admin, contracts; project settings.

## 14. Git и вынос части

- `main` всегда зелёный; ветки `<часть>/<тема>`; слияние через PR, когда появится remote-CI.
- Вынос части (`docs/03-вынос-в-отдельный-репо.md`): `git subtree split -P <путь>`; в новый репо
  копируется нужный контракт из `contracts/` (как у Flutter: копия + тег); `@wf/config`
  копируется или публикуется; workflow части переезжает вместе с ней. Сгенерированный код
  закоммичен, поэтому часть собирается сразу, генератор нужен только для обновления контракта.

## 15. Версии (проверены 24.09.2026)

| Что | Версия | Примечание |
| --- | --- | --- |
| Go | 1.27.1 | через `toolchain` |
| Node.js | 26.10 | LTS по графику Node в октябре 2026 |
| pnpm | 12.6.0 | |
| PostgreSQL / PostGIS | 18.6 / 3.6 | образ только amd64 |
| Next.js / React | 16.3.6 / 19.3.0 | |
| TypeScript | **6.0.3** в приложениях, **5.9.3** у генератора | 7.0.2 не поддержан typescript-eslint (<6.1) и openapi-typescript (^5.x); у 7.x нет классического JS API |
| ESLint | **9.39.5** (maintenance) | 10.x не поддержан eslint-config-next 16.3.6 (плагины react/import/jsx-a11y ≤9) |
| Vite / Vitest | 8.3.1 / 5.0.1 | |
| Playwright | 1.63.0 | |
| Prettier | 3.9.9 | |
| openapi-typescript / openapi-fetch | 7.13.0 / 0.17.0 | |
| chi / pgx | 5.3.2 / 5.11.0 | |
| oapi-codegen / sqlc / goose / River | 2.8.0 / 1.31.1 / 3.28.0 / 0.47.0 | sqlc разбирает грамматику PG17 — синтаксис PG18 в SQL не используем |
| go-task / golangci-lint / lefthook / gitleaks | 3.53.1 / 2.14.0 / 2.1.14 / 8.30.1 | |
| Mailpit | 1.31.2 | |
| Flutter / Dart | 3.47.5 / 3.13.4 | для wf-native, позже |

Эта таблица переезжает в `docs/versions.md` и обновляется при каждом апгрейде.

## 16. Вне рамок этапа

Бизнес-логика модулей, auth, «правило двух рук» из макета админки (в `docs/01` после решения
в спеке админки), UUIDv7 (в PG18 есть `uuidv7()` — решение в спеке бэкенда), Valkey, oasdiff,
Flutter-приложение, Dockerfile и деплой на VPS, e2e сверх smoke-теста.

## 17. Не проверено — проверить первыми шагами плана

1. Переключится ли глобальный pnpm 10.33 на 12.6.0 по полю `packageManager`.
2. Работает ли `devEngines.runtime` (Node 26 для проекта) на Windows.
3. Проходят ли 10 миграций накат и откат на PostgreSQL 18.
4. Разбирает ли sqlc текущую схему (`geography`, `EXCLUDE`, generated-колонки).
5. Проходит ли проверка типов `next build` на TypeScript 6.0.3.
6. Версии библиотек, выбранных в этой спеке без проверки: next-intl / use-intl, MSW,
   eslint-plugin-boundaries, pgtestdb, kin-openapi — последние стабильные и совместимые.
