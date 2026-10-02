# Фундамент 3/3: край платформы — токены, Principal, IP, rate limit, антибот, идемпотентность, почта, файлы, локали, часы — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Всё, что стоит между запросом и сценарием модуля, готово и покрыто тестами: реальный IP клиента и доверие к BFF, access-токены и Principal из сессии, требование аутентификации из контракта, rate limit, проверка «человек ли», идемпотентность мутирующих запросов; рядом — часы, хэширование паролей, серверные локали, почта задачей River, файловое хранилище, флаги по городам, курсор keyset-пагинации. После плана модуль `identity` пишет только свои сценарии.

**Architecture:** Каждый механизм — пакет `backend/internal/platform/<имя>` с интерфейсом и реализацией на Postgres (как `events`, `audit`); SQL — через sqlc. Конвейер публичного API (§6.1): recover → `request_id` → лог → **peer** (реальный IP, BFF) → лимит тела → **таймаут** → **маршрут контракта в `ctx`** → **аутентификация** (необязательная, кладёт Principal) → **валидация** (требование аутентификации — из `security` контракта, 401) → **rate limit** (класс — расширение `x-rate-limit` операции) → **решение антибота в `ctx`** → **идемпотентность** (ключ вставляется хуком `db.InTx` в бизнес-транзакцию) → strict-хендлер. Ошибки механизмов — `httpx.ProblemError`: хендлер возвращает их как обычные ошибки, ответ — их `problem+json`.

**Tech Stack:** Go 1.27.1, pgx v5.11.0, River v0.47.0, sqlc 1.31.1, kin-openapi v0.149.0, chi v5.3.2; новые: github.com/golang-jwt/jwt/v5 v5.3.1, golang.org/x/crypto v0.57.0 (argon2), golang.org/x/sync v0.23.0 (singleflight), github.com/kaptinlin/messageformat-go/mf1 v0.8.6 (ICU MessageFormat v1), github.com/wneessen/go-mail v0.8.1 (SMTP), github.com/altcha-org/altcha-lib-go v1.0.0 (только тесты совместимости ALTCHA).

**Spec:** `docs/superpowers/specs/2026-10-01-backend-architecture-design.md` — §6.1 (конвейер), §6.2 (токены), §6.3 (Principal), §6.4 (идемпотентность), §6.6 (rate limit), §6.7 (антибот), §6.8 (ошибки), §6.9 (часы, серверные тексты, почта, файлы, наблюдаемость — маскирование логов, флаги, хэширование паролей, конфиг и секреты), §7.1 (пароль: argon2id, длина, список), §8.3 (`x-extensible-enum`), §8.4 (IP клиента через BFF), §10.1 (роли БД), §11 (`rate_limits`, `humancheck_spent`), §12 (тесты безопасности и гонок, стражи 4, 5, 7), §14 (непроверенное: ALTCHA, список паролей). Третий из трёх планов «фундамента» (§13 п. 0); предыдущие — `docs/superpowers/plans/2026-10-01-foundation-1-guards-contract.md`, `…-foundation-2-events-audit-grants.md`.

## Global Constraints

- Go 1.27.1; модуль `wf/backend`; sqlc разбирает грамматику PostgreSQL 17 — синтаксис PG18 в SQL не использовать.
- TDD: сначала падающий тест, потом код; каждый страж проверяется подсадкой бага.
- Комментарии в коде — по-русски, идентификаторы — по-английски; переводы строк LF.
- ID — UUIDv7 из `platform/id`; время в коде — только из `clock.Clock` (в тестах — `clocktest.Fake`), хранение — UTC.
- Коды ошибок — `<область>.<ошибка>`; код платформы — константа в `internal/platform/httpx/codes.go`, запись в `PlatformCodes`, строка `x-error-codes-common` обоих контрактов, тексты `errors.<код>` в `apps/{web,admin}/messages/{ru,en}.json` (en — пустая строка, как у существующих кодов). Литерал кода в коде платформы — падение стража `TestProblemCodesAreConstants`.
- Персональных данных нет в логах (маскирование в `logx`), в payload событий и в аргументах задач River (в задаче — id получателя, не адрес).
- Секреты (сиды JWT, ключи PoW, секрет BFF, пароль SMTP) — только из окружения, списком для ротации, base64; в git, память и вывод команд не попадают.
- Новый env-ключ — в том же коммите: поле в `internal/platform/config`, строка с комментарием в `backend/.env.example`, значение в локальном `backend/.env` (писать скриптом, значения не выводить — `.claude/rules/env.md`). Все правки `config.go` и `.env*` — только в Task 19.
- `go.mod`/`go.sum` меняет только Task 1 (и `go mod tidy` в Task 20): параллельные `go get` портят файл.
- `sqlc.yaml` и сгенерированные `*db` пакеты меняет только Task 3. Кодогенерация в задачах — точными командами задачи, не `./task gen` целиком (параллельные задачи не должны генерировать чужое).
- Один pnpm-процесс одновременно; `pnpm install` не запускать (если `ERR_PNPM_VERIFY_DEPS_BEFORE_RUN` — остановиться и сообщить контроллеру).
- Тесты бэкенда — `./task backend:test` (нужен Postgres: `bash scripts/pg.sh start` в фоне — команда не возвращает управление); один пакет — `go test ./internal/platform/<пакет>/... -count=1` из `backend/`. `typecheck`, `lint`, `build` (и `go build`, `go vet`) не запускать без явной просьбы пользователя; компиляцию проверяет `go test … -run '^$'`.
- Коммиты: одна строка по-русски, без тела; только свои пути: `git add <пути> && git commit -m "…" -- <пути>`. Никогда `git add -A`, `git add .`, `git commit -a`, `git stash`, `git reset`, `git clean`, `git checkout -- .`.
- `rm -rf` вне проекта запрещён; временные файлы — в `.tools/tmp/`.
- Тексты с обратными кавычками не передавать через `node -e "…"`/двойные кавычки bash — только Edit/Write или heredoc `<<'EOF'`.

## Решения контроллера (до исполнения)

- **Вход сотрудников — не в этом плане.** Сессия админки (непрозрачная cookie, `audience=admin`), TOTP, приглашения, CSRF админки (Origin + обязательный заголовок) и IP-аллоулист admin-api — спека `identity` (§13 п. 2: «вместе со входом сотрудников»). Здесь admin-api получает peer, таймаут, маршрут, валидацию, rate limit и идемпотентность; место аутентификации в его конвейере помечено комментарием. Цена ошибки — одна задача в плане identity.
- **Сессии — интерфейс, а не таблица.** Таблица `sessions` принадлежит `identity`; платформа задаёт `auth.SessionLoader` (одним запросом по `sid` — Principal) и кэш на 5 с. До identity `cmd/api` подключает `auth.NoSessions` — любой токен отвергается 401; операций с `security: bearer` в контракте пока нет. Бан и удалённый аккаунт — ответственность загрузчика identity: он возвращает `auth.ErrSessionInvalid`.
- **Refresh — криптопримитивы в платформе, ротация — в identity.** `token.NewRefresh`, `HashRefresh`, `SealSuccessor`/`OpenSuccessor` (HKDF от предъявленного токена → AES-256-GCM, §6.2) — чистые функции; семьи, повтор, окно 15 с — сценарии identity.
- **JWT — golang-jwt v5 с жёсткими опциями.** `WithValidMethods(["EdDSA"])`, `WithAudience("public")`, `WithExpirationRequired`, `WithIssuedAt`, ключ — только по `kid` из нашего набора. Свой разборщик JWT не пишем: ошибки разбора — частая уязвимость, библиотека их уже прошла. `kid` не задаётся руками: это первые 8 байт SHA-256 открытого ключа (`platform/keys.ID`), в окружении — только сиды.
- **ALTCHA — своя серверная часть, библиотека — только в тестах.** `altcha-lib-go` v1.0.0 (MIT) проверяет срок по `time.Now`, берёт алгоритм из присланного решения и не знает ротации ключей. Протокол прост (SHA-256 от соли и числа, HMAC подписи) — пишем его сами на `clock.Clock`, только SHA-256, `kid` в параметрах соли. Совместимость с виджетом доказывает тест: задачу решает `altcha.SolveChallenge`, а `altcha.VerifySolution` принимает наше решение. Закрывает §14 «Go-реализация ALTCHA».
- **Список распространённых паролей — SecLists `100k-most-used-passwords-NCSC.txt` (MIT, © 2018 Daniel Miessler).** Короче 10 символов пароль не пройдёт и так (§7.1), поэтому вшиваются только строки ≥ 10 символов — 9 248 строк, плюс десяток русских раскладочных. Закрывает §14 «Источник и лицензия списка».
- **Почта в dev — Mailpit по SMTP, а не файлы.** Mailpit уже в репо (`./task mail`, :11025, UI :18025); одна реализация `mail.SMTP` на dev и прод. Спека §6.9 правится в Task 20. Письма собирает `mail.Composer` модуля по id из аргументов задачи (адреса в задаче нет); платформа — только очередь, реестр видов и отправка.
- **Файлы — интерфейс `blob.Store`, реализации FS (dev) и Memory (тесты).** S3-совместимая реализация — в спеке `media` (первый потребитель; провайдер выбирается отдельно, §16). В бинарники `blob` не подключается — некому.
- **Идемпотентность: одна транзакция на запрос.** Ключ вставляет хук в начале первой `db.InTx` запроса; вторая `db.InTx` в том же идемпотентном запросе — ошибка (иначе ключ закоммитится с первой, а действие — нет). Чтения до транзакции — без `InTx`. Успешный ответ (2xx) без транзакции — 500 и запись в лог: ручка, требующая идемпотентности, обязана выполняться в транзакции (§6.4). Сохраняются ответы со статусом < 500 и заголовки `Content-Type`, `Location`, `ETag` (новая колонка `response_headers`).
- **Требование аутентификации — из контракта.** Слой аутентификации необязательный (§6.1): нет `Authorization` — дальше без Principal; битый, просроченный, чужой токен или отозванная сессия — 401 сразу. Нужен ли вход операции, решает `security` контракта: валидатор зовёт `AuthenticationFunc`, которая смотрит Principal в `ctx`. Код — `auth.unauthenticated` (401, `WWW-Authenticate: Bearer`).
- **Rate limit: класс — в контракте, политика — в конфиге.** Операция объявляет `x-rate-limit: <класс>` (без расширения — `default`); политики классов по ключам IP/пользователь/устройство — в коде (`ratelimit.DefaultRules`) с переопределением строкой `API_RATE_LIMITS`. Отказ хранилища лимитов — пропуск запроса с ошибкой в логе (fail-open): база недоступна — не работает и всё остальное, а 500 на каждый запрос ничего не защищает.
- **`/readyz` — кэш результата на 1 с со схлопыванием.** Открытый вопрос из 2/3 («публичный и без лимита — соединение пула на каждый запрос»): лимит частоты для проб неудобен оркестратору, а кэш снимает нагрузку на базу при любом потоке запросов.
- **Реальный IP за балансировщиком.** Кроме BFF (§8.4) на Render перед API стоит прокси хостинга: `X-Forwarded-For` читается только от адресов `API_TRUSTED_PROXIES`, справа налево до первого недоверенного. На Render — частные сети (`10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16`): клиент из интернета с частного адреса не придёт.
- **Ключи на Render — `generateValue`.** `API_JWT_SEEDS` и `API_HUMANCHECK_KEYS` в `render.yaml` объявляются с `generateValue: true` (Render генерирует base64 от 256 бит — ровно сид Ed25519 и ключ HMAC); `keys.Decode` принимает и стандартный, и URL-алфавит base64. Значения не проходят ни через git, ни через контроллера.
- **`GET /v1/app/min-version` (§8.3) — не здесь.** Это первая продуктовая операция платформы с версиями приложений в конфиге; она уходит в план первого модуля (`geo`) вместе с удалением `/v1/health` — на той же операции переедут страж «тег = модуль» и smoke клиентов.
- **`/v1/health` остаётся в контракте** до первой операции модуля (на нём держатся страж «тег = модуль», smoke клиентов и тест бандла) — как решено в 2/3.

## Review Focus

1. **Клиент подделал `X-Forwarded-For` или `X-WF-Client-IP`, подключившись напрямую** — IP берётся от реального собеседника, лимиты считаются по нему, подделка не помогает обойти rate limit (Task 7, тесты «прямой клиент с X-WF-Client-IP», «левые адреса в X-Forwarded-For»; Task 10, тест «лимит по реальному IP»).
2. **Повтор идемпотентного запроса, пока первый ещё в транзакции** (двойной клик, ретрай мобильного клиента) — действие исполняется ровно один раз, второй получает `409 idempotency.in_progress`, третий после коммита — сохранённый ответ (Task 12, тест «параллельный дубль»).
3. **Сессию отозвали («выйти везде», бан), а access-токен ещё жив** — 401 не позже чем через 5 секунд на любой ручке (Task 9, тест «кэш сессии живёт не дольше ttl»).
4. **Токен с `alg=none`, HS256 на открытом ключе, чужим `aud`, неизвестным `kid` или просроченный** — 401, ни одна ветка не принимает (Task 8, табличный тест безопасности).
5. **Решение PoW предъявлено второй раз, просрочено или подписано выведенным из ротации ключом** — `403 humancheck.required` с новой задачей (Task 11, тесты «повтор», «срок», «ротация»).

## Карта файлов

```
backend/
  go.mod, go.sum                                           Task 1 (зависимости), Task 20 (tidy)
  migrations/0018_platform_edge.sql, migrations/README.md  Task 3
  sqlc.yaml                                                Task 3 — ratelimitdb, humancheckdb, idempotencydb, uuid у flagsdb
  internal/archtest/ownership.go                           Task 3
  internal/platform/
    clock/clock.go, clock_test.go                          Task 2
    testkit/clocktest/clocktest.go, clocktest_test.go      Task 2
    testkit/dbtest/dbtest.go, dbtest_test.go               Task 3 — NewPoolsAs (код под ролью)
    grants/grants_test.go                                  Task 3 — права на новые таблицы
    logx/logx.go, mask.go, mask_test.go                    Task 4
    httpx/problem.go, codes.go, codes_test.go, problem_test.go   Task 5
    httpx/route.go, route_test.go, validate.go, validate_test.go, timeout.go   Task 6
    peer/peer.go, peer_test.go                             Task 7
    keys/keys.go, keys_test.go                             Task 8
    token/token.go, refresh.go, token_test.go, refresh_test.go   Task 8
    auth/principal.go, loader.go, middleware.go, *_test.go Task 9
    ratelimit/queries/ratelimit.sql, ratelimitdb/          Task 3
    ratelimit/limiter.go, rules.go, middleware.go, cleanup.go, *_test.go   Task 10
    humancheck/queries/humancheck.sql, humancheckdb/       Task 3
    humancheck/pow.go, humancheck.go, cleanup.go, *_test.go   Task 11
    risk/risk.go, risk_test.go                             Task 11
    idempotency/queries/idempotency.sql, idempotencydb/    Task 3
    db/tx.go, tx_test.go                                   Task 12 — хук транзакции
    idempotency/idempotency.go, buffer.go, cleanup.go, *_test.go   Task 12
    password/password.go, policy.go, common.txt, *_test.go Task 13
    i18n/i18n.go, i18n_test.go                             Task 14
    mail/mail.go, smtp.go, job.go, *_test.go               Task 15
    blob/blob.go, fs.go, memory.go, blob_test.go           Task 16
    flags/queries/flags.sql, flagsdb/                      Task 3
    flags/flags.go, flags_test.go                          Task 17
    page/page.go, page_test.go                             Task 18 — курсор keyset-пагинации
    health/health.go, health_test.go                       Task 19 — кэш /readyz
    config/config.go, config_test.go                       Task 19
  locales/embed.go, ru.json, en.json, locales_test.go      Task 14
  internal/httpapi/{public,admin}/handler.go, handler_test.go   Task 6, Task 19
  cmd/api/main.go, main_test.go; cmd/admin-api/main.go; cmd/worker/main.go, main_test.go   Task 19
  .env.example                                             Task 19
contracts/
  openapi/{public,admin}/root.yaml, public/platform/schemas.yaml, admin/platform/schemas.yaml   Task 5
  openapi/{public,admin}.yaml (бандлы), src/conventions.mjs, test/conventions.test.mjs   Task 5
apps/{web,admin}/messages/{ru,en}.json, apps/{web,admin}/src/api/gen/*.d.ts   Task 5
render.yaml, .github/workflows/backend.yml                 Task 19
docs/versions.md                                           Task 1, Task 20
docs/superpowers/specs/2026-10-01-backend-architecture-design.md, docs/02-архитектура.md,
docs/deploy-dev.md, .claude/rules/backend-platform.md, .claude/rules/backend-http.md   Task 20
```

## Порядок и параллельность

```
Task 1 ─┬─ Task 2 (clock) ──────────── Task 8 (keys, token) ─┐
        ├─ Task 3 (схема, sqlc, роли) ───────────────────────┼─ Task 10 (ratelimit) ── Task 11 (humancheck, risk) ─┐
        ├─ Task 4 (логи)                                     │                                                    │
        ├─ Task 5 (коды, контракт) ── Task 6 (маршрут) ──────┼─ Task 9 (auth) ─────── Task 12 (идемпотентность) ──┼─ Task 19 ── Task 20
        ├─ Task 7 (peer)                                     │                                                    │
        ├─ Task 13 (пароли), Task 14 (локали) ── Task 15 (почта), Task 16 (blob), Task 17 (флаги, после 3 и 5), Task 18 (курсор, после 5) ─┘
```

Task 1 — первым и один. Дальше параллельно всё, что не связано стрелкой. Общие ресурсы: pnpm запускает только Task 5; `sqlc generate` — только Task 3; `config.go` и `.env*` — только Task 19.

---

## Фаза A. Основа

### Task 1: Зависимости плана

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`, `docs/versions.md`
- Create (временно, удаляется в этой же задаче): `backend/depsprobe/probe.go`

**Interfaces:**
- Consumes: —
- Produces: модули в `go.mod` прямыми зависимостями с полными записями `go.sum` — последующие задачи только импортируют, `go.mod` не трогают.

- [ ] **Step 1: Получить модули**

Из `backend/`:

```bash
go get github.com/golang-jwt/jwt/v5@v5.3.1 \
  golang.org/x/crypto@v0.57.0 \
  golang.org/x/sync@v0.23.0 \
  github.com/kaptinlin/messageformat-go/mf1@v0.8.6 \
  github.com/wneessen/go-mail@v0.8.1 \
  github.com/altcha-org/altcha-lib-go@v1.0.0
```

Expected: `go.mod` содержит все шесть; `git diff go.mod` — посмотреть, что заодно поднялись только транзитивные зависимости (например `golang.org/x/text`), прямые зависимости проекта не понижены.

- [ ] **Step 2: Пробный пакет, чтобы `go mod tidy` записал всё нужное для сборки**

`backend/depsprobe/probe.go`:

```go
// Временный пакет Task 1: импортирует новые зависимости, чтобы go mod tidy записал в go.sum
// всё, что нужно для их сборки. Удаляется в этой же задаче.
package depsprobe

import (
	_ "github.com/altcha-org/altcha-lib-go"
	_ "github.com/golang-jwt/jwt/v5"
	_ "github.com/kaptinlin/messageformat-go/mf1"
	_ "github.com/wneessen/go-mail"
	_ "golang.org/x/crypto/argon2"
	_ "golang.org/x/sync/singleflight"
)
```

Из `backend/`:

```bash
go mod tidy && go test ./depsprobe -count=1 -run '^$'
```

Expected: `ok` или `[no test files]`; в `go.mod` шесть модулей без `// indirect`.

- [ ] **Step 3: Удалить пробный пакет и убедиться, что всё компилируется**

```bash
rm -r depsprobe && go test ./... -count=1 -run '^$'
```

Expected: все пакеты `ok`/`[no test files]`. `go.mod` после удаления пробы не трогаем: `go mod tidy` вернул бы модули в `// indirect` — финальный `tidy` в Task 20, когда все импорты появятся.

- [ ] **Step 4: Версии в `docs/versions.md`**

В таблицу версий после строки `google/uuid`:

```markdown
| golang-jwt/jwt | 5.3.1 | `backend/go.mod` (access-токены, EdDSA — `internal/platform/token`) |
| golang.org/x/crypto / x/sync | 0.57.0 / 0.23.0 | `backend/go.mod` (argon2id — `internal/platform/password`; singleflight) |
| messageformat-go/mf1 | 0.8.6 | `backend/go.mod` (ICU MessageFormat v1 серверных локалей — `internal/platform/i18n`) |
| go-mail | 0.8.1 | `backend/go.mod` (SMTP — `internal/platform/mail`) |
| altcha-lib-go | 1.0.0 | `backend/go.mod` (только тесты совместимости ALTCHA — `internal/platform/humancheck`) |
```

- [ ] **Step 5: Commit**

```bash
git add backend/go.mod backend/go.sum docs/versions.md
git commit -m "Зависимости плана 3/3: JWT, argon2id, ICU-сообщения, SMTP, ALTCHA" -- backend/go.mod backend/go.sum docs/versions.md
```

---

### Task 2: Часы

**Files:**
- Create: `backend/internal/platform/clock/clock.go`, `backend/internal/platform/clock/clock_test.go`
- Create: `backend/internal/platform/testkit/clocktest/clocktest.go`, `backend/internal/platform/testkit/clocktest/clocktest_test.go`

**Interfaces:**
- Consumes: —
- Produces:
  - `clock.Clock interface { Now() time.Time }`; `clock.System Clock` — время в UTC.
  - `clock.In(t time.Time, zones ...string) (time.Time, error)` — местное время по первой непустой таймзоне; `clock.ErrNoZone`.
  - `clocktest.New(t time.Time) *clocktest.Fake` с методами `Now() time.Time`, `Set(time.Time)`, `Advance(time.Duration)` — реализует `clock.Clock`, безопасен из нескольких горутин.

- [ ] **Step 1: Падающие тесты часов**

`backend/internal/platform/clock/clock_test.go`:

```go
package clock_test

import (
	"errors"
	"testing"
	"time"

	"wf/backend/internal/platform/clock"
)

func TestSystemIsUTCNow(t *testing.T) {
	before := time.Now()
	got := clock.System.Now()
	if got.Location() != time.UTC {
		t.Fatalf("локация = %v, нужен UTC", got.Location())
	}
	if got.Before(before.Add(-time.Second)) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("время %v далеко от текущего", got)
	}
}

func TestInPicksFirstNonEmptyZone(t *testing.T) {
	at := time.Date(2026, 10, 1, 17, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		zones  []string
		offset int // секунды от UTC
	}{
		{"поле задано", []string{"Europe/Moscow", "Asia/Novosibirsk"}, 3 * 3600},
		{"поле пусто — город", []string{"", "Asia/Novosibirsk"}, 7 * 3600},
		// получасовой пояс: ночной пересчёт §9.3 не должен на нём промахиваться
		{"получасовой сдвиг", []string{"Asia/Kolkata"}, 5*3600 + 1800},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := clock.In(at, c.zones...)
			if err != nil {
				t.Fatal(err)
			}
			if _, off := got.Zone(); off != c.offset {
				t.Fatalf("сдвиг = %d, нужен %d", off, c.offset)
			}
			if !got.Equal(at) {
				t.Fatal("момент времени изменился — In должна менять только представление")
			}
		})
	}
}

func TestInErrors(t *testing.T) {
	at := time.Now()
	if _, err := clock.In(at); !errors.Is(err, clock.ErrNoZone) {
		t.Fatalf("без таймзон: %v, нужна ErrNoZone", err)
	}
	if _, err := clock.In(at, "", ""); !errors.Is(err, clock.ErrNoZone) {
		t.Fatalf("только пустые: %v, нужна ErrNoZone", err)
	}
	// неизвестная таймзона — ошибка, а не тихий UTC
	if _, err := clock.In(at, "Mars/Olympus"); err == nil {
		t.Fatal("неизвестная таймзона принята")
	}
	// Local — таймзона машины, а не места: в данных её быть не может
	if _, err := clock.In(at, "Local"); err == nil {
		t.Fatal("Local принята")
	}
}
```

- [ ] **Step 2: Падающие тесты поддельных часов**

`backend/internal/platform/testkit/clocktest/clocktest_test.go`:

```go
package clocktest_test

import (
	"sync"
	"testing"
	"time"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/testkit/clocktest"
)

var _ clock.Clock = (*clocktest.Fake)(nil)

func TestFakeSetAndAdvance(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	f := clocktest.New(start)
	if got := f.Now(); !got.Equal(start) || got.Location() != time.UTC {
		t.Fatalf("Now = %v, нужен %v в UTC", got, start)
	}
	f.Advance(90 * time.Second)
	if got := f.Now(); !got.Equal(start.Add(90 * time.Second)) {
		t.Fatalf("после Advance: %v", got)
	}
	later := start.Add(time.Hour)
	f.Set(later)
	if got := f.Now(); !got.Equal(later) || got.Location() != time.UTC {
		t.Fatalf("после Set: %v", got)
	}
}

func TestFakeConcurrentAdvance(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f := clocktest.New(start)
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { f.Advance(time.Second); _ = f.Now() })
	}
	wg.Wait()
	if got := f.Now(); !got.Equal(start.Add(100 * time.Second)) {
		t.Fatalf("после 100 сдвигов: %v", got)
	}
}
```

- [ ] **Step 3: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/clock/... ./internal/platform/testkit/clocktest/... -count=1`
Expected: FAIL — пакетов `clock` и `clocktest` нет.

- [ ] **Step 4: Реализация**

`backend/internal/platform/clock/clock.go`:

```go
// Package clock — текущее время для платформы и модулей (спека бэкенда §6.9): в коде —
// clock.System, в тестах — clocktest.Fake. Хранится и передаётся только UTC; местное время
// считается по таймзоне места (поле → город) функцией In. База таймзон вшита в бинарник
// (time/tzdata): контейнер без tzdata не ломает расчёт местного времени.
package clock

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata" // база таймзон в бинарнике
)

// Clock — источник текущего времени.
type Clock interface {
	Now() time.Time
}

type system struct{}

func (system) Now() time.Time { return time.Now().UTC() }

// System — настоящие часы, время в UTC.
var System Clock = system{}

// ErrNoZone — ни одной непустой таймзоны не передано.
var ErrNoZone = errors.New("clock: не задано ни одной таймзоны")

// In — t в местном времени первой непустой таймзоны из zones (таймзона поля, затем города;
// пустая users.timezone пропускается). Неизвестная таймзона — ошибка, а не тихий UTC.
func In(t time.Time, zones ...string) (time.Time, error) {
	for _, z := range zones {
		if z == "" {
			continue
		}
		// Local — таймзона машины, а не места
		if z == "Local" {
			return time.Time{}, fmt.Errorf("clock: таймзона %q не принимается", z)
		}
		loc, err := time.LoadLocation(z)
		if err != nil {
			return time.Time{}, fmt.Errorf("clock: таймзона %q: %w", z, err)
		}
		return t.In(loc), nil
	}
	return time.Time{}, ErrNoZone
}
```

`backend/internal/platform/testkit/clocktest/clocktest.go`:

```go
// Package clocktest — управляемые часы для тестов (clock.Clock).
package clocktest

import (
	"sync"
	"time"
)

// Fake — часы, которые идут только по команде теста.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

func New(t time.Time) *Fake { return &Fake{now: t.UTC()} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.UTC()
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
```

- [ ] **Step 5: Тесты проходят**

Из `backend/`: `go test ./internal/platform/clock/... ./internal/platform/testkit/clocktest/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/platform/clock backend/internal/platform/testkit/clocktest
git commit -m "Платформа: часы в UTC, местное время по таймзоне места, поддельные часы для тестов" -- backend/internal/platform/clock backend/internal/platform/testkit/clocktest
```

---

### Task 3: Схема и запросы края платформы, код под ролями БД

**Files:**
- Create: `backend/migrations/0018_platform_edge.sql`
- Modify: `backend/migrations/README.md`, `backend/internal/archtest/ownership.go`, `backend/sqlc.yaml`
- Create: `backend/internal/platform/ratelimit/queries/ratelimit.sql`, `backend/internal/platform/humancheck/queries/humancheck.sql`, `backend/internal/platform/idempotency/queries/idempotency.sql`
- Modify: `backend/internal/platform/flags/queries/flags.sql`
- Generated: `backend/internal/platform/{ratelimit/ratelimitdb,humancheck/humancheckdb,idempotency/idempotencydb,flags/flagsdb}/*.go`
- Modify: `backend/internal/platform/testkit/dbtest/dbtest.go`; Create: `backend/internal/platform/testkit/dbtest/dbtest_test.go` (если файла нет; иначе дописать)
- Modify: `backend/internal/platform/grants/grants_test.go`, `backend/cmd/migrate/main_test.go` (комментарий)

**Interfaces:**
- Consumes: —
- Produces:
  - Таблицы `rate_limits (key text PK, tat timestamptz)` и `humancheck_spent (signature text PK, expires_at timestamptz)` — `UNLOGGED`; колонка `idempotency_keys.response_headers jsonb`.
  - `ratelimitdb.Queries`: `Take(ctx, TakeParams{Key string; Now time.Time; IntervalS float64; WindowS float64}) (time.Time, error)` (0 строк — `pgx.ErrNoRows`, лимит исчерпан); `Add(ctx, AddParams{Key string; Now time.Time; IntervalS float64}) error`; `GetTAT(ctx, key string) (time.Time, error)`; `DeleteExpired(ctx, DeleteExpiredParams{Now time.Time; Batch int32}) (int64, error)`.
  - `humancheckdb.Queries`: `Spend(ctx, SpendParams{Signature string; ExpiresAt time.Time}) (int64, error)` (0 — уже израсходовано); `DeleteExpired(ctx, DeleteExpiredParams{Now time.Time; Batch int32}) (int64, error)`.
  - `idempotencydb.Queries`: `Get(ctx, GetParams{UserID uuid.UUID; Key string}) (GetRow, error)` с полями `RequestHash string`, `ResponseStatus *int32`, `ResponseHeaders []byte`, `ResponseBody []byte`; `Claim(ctx, ClaimParams{UserID; Key; Endpoint; RequestHash string}) (time.Time, error)` (0 строк — `pgx.ErrNoRows`, ключ занят); `SaveResponse(ctx, SaveResponseParams{Status *int32; Headers []byte; Body []byte; UserID uuid.UUID; Key string; RequestHash string}) (int64, error)`; `CurrentLockTimeout(ctx) (string, error)`; `SetLockTimeout(ctx, value string) error`; `DeleteExpired(ctx, batch int32) (int64, error)`.
  - `flagsdb.Queries.IsEnabled(ctx, IsEnabledParams{CityID uuid.UUID; Key string}) (bool, error)` (0 строк — `pgx.ErrNoRows`, флага нет).
  - `dbtest.NewPoolsAs(t testing.TB, role string) dbtest.Pools` — `Pools{Owner, As *pgxpool.Pool}` на одной чистой базе: `Owner` — суперпользователь (подготовка и проверки), `As` — каждое соединение под `SET ROLE <role>` с правами из `grants.sql`.

Точные имена полей сгенерированных структур — как выдаст sqlc (по `@`-именам из SQL ниже); если sqlc назвал поле иначе, чем в списке выше, исправить список в отчёте — его читают Task 10–12, 17.

- [ ] **Step 1: Падающие тесты прав на новые таблицы**

В `backend/internal/platform/grants/grants_test.go` в срез `cases` перед закрывающей скобкой добавить:

```go
		// край платформы (план 3/3): API считает лимиты, тратит решения PoW, ведёт ключи
		// идемпотентности; воркер чистит просроченное
		{"api", "INSERT INTO rate_limits (key, tat) VALUES ('probe', now()) ON CONFLICT (key) DO UPDATE SET tat = excluded.tat", true},
		{"api", "SELECT tat FROM rate_limits WHERE key = 'probe'", true},
		{"api", "INSERT INTO humancheck_spent (signature, expires_at) VALUES ('probe', now()) ON CONFLICT DO NOTHING", true},
		{"api", "INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash) VALUES (gen_random_uuid(), 'probe', '/p', 'h')", true},
		{"api", "UPDATE idempotency_keys SET response_status = 200, response_headers = '{}' WHERE false", true},
		{"worker", "DELETE FROM rate_limits WHERE tat < now()", true},
		{"worker", "DELETE FROM humancheck_spent WHERE expires_at < now()", true},
		{"worker", "DELETE FROM idempotency_keys WHERE expires_at < now()", true},
```

- [ ] **Step 2: Падающий тест пулов под ролью**

`backend/internal/platform/testkit/dbtest/dbtest_test.go` (создать или дописать):

```go
package dbtest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/testkit/dbtest"
)

// Код платформы проверяется с правами прода: пул As работает под ролью, Owner — для подготовки.
func TestNewPoolsAsRunsUnderRole(t *testing.T) {
	ctx := context.Background()
	p := dbtest.NewPoolsAs(t, "api")
	var who string
	if err := p.As.QueryRow(ctx, "SELECT current_user").Scan(&who); err != nil {
		t.Fatal(err)
	}
	if who != "api" {
		t.Fatalf("current_user = %q, нужен api", who)
	}
	// служебная таблица мигратора закрыта для api grants.sql — значит, права применены
	_, err := p.As.Exec(ctx, "SELECT 1 FROM goose_db_version")
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Fatalf("api читает goose_db_version: %v, нужен отказ 42501", err)
	}
	if _, err := p.Owner.Exec(ctx, "SELECT 1 FROM goose_db_version"); err != nil {
		t.Fatalf("Owner: %v", err)
	}
	// разрешённое api по grants.sql — работает
	if _, err := p.As.Exec(ctx, "SELECT 1 FROM feature_flags"); err != nil {
		t.Fatalf("api читает feature_flags: %v", err)
	}
}
```

- [ ] **Step 3: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/grants/... ./internal/platform/testkit/dbtest/... -count=1`
Expected: FAIL — `relation "rate_limits" does not exist` и т. п.; `NewPoolsAs` не определена.

- [ ] **Step 4: Миграция**

`backend/migrations/0018_platform_edge.sql`:

```sql
-- 0018_platform_edge.sql
-- Край платформы (спека бэкенда §6.4, §6.6, §6.7, §11): rate limit, израсходованные решения
-- антибота, заголовки сохранённого ответа идемпотентного запроса.

-- +goose Up

-- GCRA: одна строка на ключ, tat — «теоретическое время прихода» следующего запроса.
-- UNLOGGED: без WAL — быстрее; после сбоя сервера таблица пуста, и это допустимо: худшее —
-- короткое окно без лимита. Строки с tat в прошлом ничего не ограничивают — их чистит воркер.
CREATE UNLOGGED TABLE rate_limits (
    key text        PRIMARY KEY,
    tat timestamptz NOT NULL
);

CREATE INDEX rate_limits_tat_idx ON rate_limits (tat);

-- Использованные решения PoW (протокол ALTCHA) — до истечения срока задачи: повтор решения
-- не проходит. UNLOGGED: после сбоя повтор возможен до конца срока задачи (минуты) — допустимо.
CREATE UNLOGGED TABLE humancheck_spent (
    signature  text        PRIMARY KEY,
    expires_at timestamptz NOT NULL
);

CREATE INDEX humancheck_spent_expiry_idx ON humancheck_spent (expires_at);

-- Заголовки сохранённого ответа (Content-Type, Location, ETag): повтор запроса получает тот же
-- ответ, включая адрес созданного ресурса.
ALTER TABLE idempotency_keys ADD COLUMN response_headers jsonb;

-- +goose Down

ALTER TABLE idempotency_keys DROP COLUMN response_headers;
DROP TABLE humancheck_spent;
DROP TABLE rate_limits;
```

- [ ] **Step 5: Владельцы и README миграций**

В `backend/internal/archtest/ownership.go` в блок `// platform` после `"idempotency_keys"`:

```go
	"rate_limits":      table("platform"),
	"humancheck_spent": table("platform"),
```

(выровнять колонку значений блока, как отформатирует `gofmt`).

В `backend/migrations/README.md` в список файлов после строки `0017_platform_events.sql`:

```markdown
0018_platform_edge.sql   край платформы: rate limit (GCRA), решения антибота, заголовки ответа идемпотентности
```

и после абзаца «**События (спека бэкенда §6.5).** …» абзац:

```markdown
**Край платформы (спека бэкенда §6.4, §6.6, §6.7).** `rate_limits` и `humancheck_spent` — `UNLOGGED`:
без WAL, после сбоя сервера пусты (короткое окно без лимита и повтора PoW допустимо). Ключ лимита —
`<вид>:<класс>:<значение>` (`ip:auth:203.0.113.7`), строка одна на ключ, `tat` — GCRA. Решение PoW
хранится до `expires_at` задачи. `idempotency_keys` хранит ответ повтора: статус, тело (`jsonb`) и
заголовки `Content-Type`, `Location`, `ETag` (`response_headers`); ключ живёт 24 часа.
```

В `backend/cmd/migrate/main_test.go` комментарий над `TestDownAppliesGrants` заменить на:

```go
// down откатывает последнюю миграцию, а Down может пересоздать таблицу (0017 возвращает прежний
// outbox) — без применения прав она осталась бы только у владельца. Права применяются после
// любого down; проверка — по outbox, который есть в схеме при любой последней миграции.
```

- [ ] **Step 6: Запросы**

`backend/internal/platform/ratelimit/queries/ratelimit.sql`:

```sql
-- Rate limit по GCRA (спека бэкенда §6.6). Владелец rate_limits — platform. Время — из часов
-- приложения (@now): тесты двигают их без ожидания.

-- Запрос влезает в всплеск — tat сдвигается, строка возвращается; не влезает — 0 строк,
-- tat не меняется. Один оператор: гонок между проверкой и записью нет.
-- name: Take :one
INSERT INTO rate_limits AS r (key, tat)
VALUES (@key, @now::timestamptz + make_interval(secs => @interval_s::float8))
ON CONFLICT (key) DO UPDATE
SET tat = greatest(r.tat, @now::timestamptz) + make_interval(secs => @interval_s::float8)
WHERE greatest(r.tat, @now::timestamptz) + make_interval(secs => @interval_s::float8)
      <= @now::timestamptz + make_interval(secs => @window_s::float8)
RETURNING tat;

-- Счётчик сигнала риска: расходуется всегда, даже сверх порога.
-- name: Add :exec
INSERT INTO rate_limits AS r (key, tat)
VALUES (@key, @now::timestamptz + make_interval(secs => @interval_s::float8))
ON CONFLICT (key) DO UPDATE
SET tat = greatest(r.tat, @now::timestamptz) + make_interval(secs => @interval_s::float8);

-- name: GetTAT :one
SELECT tat FROM rate_limits WHERE key = @key;

-- Строка с tat в прошлом ничего не ограничивает. Пачками — короткие транзакции.
-- name: DeleteExpired :execrows
DELETE FROM rate_limits
WHERE key IN (SELECT key FROM rate_limits WHERE tat < @now::timestamptz LIMIT @batch);
```

`backend/internal/platform/humancheck/queries/humancheck.sql`:

```sql
-- Израсходованные решения PoW (спека бэкенда §6.7). Владелец humancheck_spent — platform.

-- 0 строк — решение уже предъявляли: повтор.
-- name: Spend :execrows
INSERT INTO humancheck_spent (signature, expires_at) VALUES (@signature, @expires_at)
ON CONFLICT (signature) DO NOTHING;

-- name: DeleteExpired :execrows
DELETE FROM humancheck_spent
WHERE signature IN (SELECT signature FROM humancheck_spent WHERE expires_at < @now::timestamptz LIMIT @batch);
```

`backend/internal/platform/idempotency/queries/idempotency.sql`:

```sql
-- Идемпотентность мутирующих запросов (спека бэкенда §6.4). Владелец idempotency_keys — platform.
-- Время — время базы: срок ключа (24 ч) задаёт DEFAULT таблицы.

-- name: Get :one
SELECT request_hash, response_status, response_headers, response_body
FROM idempotency_keys
WHERE user_id = @user_id AND key = @key AND expires_at > now();

-- Вставка ключа внутри бизнес-транзакции. Просроченный ключ переиспользуется; живой — 0 строк.
-- Параллельный дубль ждёт здесь на уникальном индексе, пока первая транзакция не закончится
-- (не дольше lock_timeout).
-- name: Claim :one
INSERT INTO idempotency_keys AS k (user_id, key, endpoint, request_hash)
VALUES (@user_id, @key, @endpoint, @request_hash)
ON CONFLICT (user_id, key) DO UPDATE
SET endpoint = excluded.endpoint,
    request_hash = excluded.request_hash,
    response_status = NULL,
    response_headers = NULL,
    response_body = NULL,
    created_at = now(),
    expires_at = now() + interval '24 hours'
WHERE k.expires_at <= now()
RETURNING created_at;

-- Ответ сохраняется после коммита бизнес-транзакции; 0 строк — транзакция откатилась (ключа нет).
-- name: SaveResponse :execrows
UPDATE idempotency_keys
SET response_status = @status, response_headers = @headers, response_body = @body
WHERE user_id = @user_id AND key = @key AND request_hash = @request_hash AND response_status IS NULL;

-- name: CurrentLockTimeout :one
SELECT current_setting('lock_timeout')::text;

-- Только до конца текущей транзакции (is_local = true).
-- name: SetLockTimeout :exec
SELECT set_config('lock_timeout', @value::text, true);

-- name: DeleteExpired :execrows
DELETE FROM idempotency_keys
WHERE (user_id, key) IN (SELECT user_id, key FROM idempotency_keys WHERE expires_at <= now() LIMIT @batch);
```

`backend/internal/platform/flags/queries/flags.sql` — дописать в конец:

```sql

-- Включён ли флаг в городе: глобально или город в списке. 0 строк — флага нет.
-- name: IsEnabled :one
SELECT (enabled_globally OR @city_id::uuid = ANY(enabled_city_ids))::boolean AS enabled
FROM feature_flags WHERE key = @key;
```

- [ ] **Step 7: sqlc**

В `backend/sqlc.yaml`:
1. У элемента `flagsdb` в `overrides` добавить перед `geography` четыре записи `uuid`/`timestamptz` — те же, что у `eventsdb`.
2. В конец списка `sql` добавить три элемента по образцу `auditdb` (те же `overrides`, `emit_interface`, `emit_pointers_for_null_types`, `omit_unused_structs`):

```yaml
  - engine: postgresql
    schema: migrations/
    queries: internal/platform/ratelimit/queries/
    gen:
      go:
        package: ratelimitdb
        out: internal/platform/ratelimit/ratelimitdb
        # sql_package, emit_*, omit_unused_structs, overrides — как у auditdb
  - engine: postgresql
    schema: migrations/
    queries: internal/platform/humancheck/queries/
    gen:
      go:
        package: humancheckdb
        out: internal/platform/humancheck/humancheckdb
  - engine: postgresql
    schema: migrations/
    queries: internal/platform/idempotency/queries/
    gen:
      go:
        package: idempotencydb
        out: internal/platform/idempotency/idempotencydb
```

(в файле — полные блоки, без сокращений: скопировать `go:`-настройки `auditdb` целиком).

Из `backend/` (только sqlc — не `./task gen`: бандлы контрактов в этот момент правит Task 5):

```bash
go tool -modfile=tools/go.mod sqlc generate
```

Expected: появились `ratelimitdb`, `humancheckdb`, `idempotencydb`, обновился `flagsdb`; `git status` не показывает изменений вне этих каталогов. Сверить имена полей с блоком **Interfaces**; расхождение — записать в отчёт.

Проверить, что `flags.go` компилируется с новыми типами `flagsdb`: `go test ./internal/platform/flags/... -count=1 -run '^$'`.

- [ ] **Step 8: Пулы под ролью**

В `backend/internal/platform/testkit/dbtest/dbtest.go` добавить импорты `github.com/jackc/pgx/v5` и `wf/backend/internal/platform/grants`, затем:

```go
// Pools — два пула на одной чистой базе: Owner — суперпользователь (подготовка данных и
// проверки), As — соединения под ролью приложения с правами из grants.sql.
type Pools struct {
	Owner *pgxpool.Pool
	As    *pgxpool.Pool
}

// NewPoolsAs — код платформы проверяется с теми же правами, что в проде (спека §10.1, страж
// §12.7): каждое соединение As делает SET ROLE <role>. Роли api, admin, worker создаёт
// deploy/dev/initdb/20_wf.sql (в CI — шаг psql перед тестами).
func NewPoolsAs(t testing.TB, role string) Pools {
	t.Helper()
	url := NewURL(t)
	ctx := context.Background()
	owner, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(owner.Close)
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := grants.Apply(ctx, sqldb); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	}
	as, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool под ролью %s: %v", role, err)
	}
	t.Cleanup(as.Close)
	return Pools{Owner: owner, As: as}
}
```

Если `go test` сообщит о цикле импортов (`grants` ← `dbtest`), остановиться и сообщить контроллеру: `grants` не должен импортировать `dbtest` (импортирует только `grants_test`).

- [ ] **Step 9: Тесты проходят**

Из `backend/`: `go test ./internal/platform/grants/... ./internal/platform/testkit/dbtest/... ./internal/archtest/... ./cmd/migrate/... -count=1`
Expected: PASS (страж владения видит новые таблицы с владельцем).

Подсадка бага (страж владения): временно удалить строку `"rate_limits"` из `ownership.go` → `go test ./internal/archtest/... -count=1` падает на `rate_limits` без владельца; вернуть.

- [ ] **Step 10: Commit**

```bash
git add backend/migrations/0018_platform_edge.sql backend/migrations/README.md backend/internal/archtest/ownership.go \
  backend/sqlc.yaml backend/internal/platform/ratelimit backend/internal/platform/humancheck backend/internal/platform/idempotency \
  backend/internal/platform/flags backend/internal/platform/testkit/dbtest backend/internal/platform/grants/grants_test.go \
  backend/cmd/migrate/main_test.go
git commit -m "Платформа: схема rate limit, антибота и ответа идемпотентности, запросы sqlc, тестовые пулы под ролью" -- backend/migrations/0018_platform_edge.sql backend/migrations/README.md backend/internal/archtest/ownership.go backend/sqlc.yaml backend/internal/platform/ratelimit backend/internal/platform/humancheck backend/internal/platform/idempotency backend/internal/platform/flags backend/internal/platform/testkit/dbtest backend/internal/platform/grants/grants_test.go backend/cmd/migrate/main_test.go
```

---

### Task 4: Персональные данные не попадают в логи

**Files:**
- Create: `backend/internal/platform/logx/mask.go`, `backend/internal/platform/logx/mask_test.go`
- Modify: `backend/internal/platform/logx/logx.go`

**Interfaces:**
- Consumes: `config.Log` (`internal/platform/config`).
- Produces: `logx.Mask(s string) string`; `logx.New` маскирует строки и ошибки во всех атрибутах и в сообщении.

Закрывает остаток плана 1/3: `httpx.RequestErrorHandler` логирует ошибку разбора целиком, а в ней — значения параметров (почта, телефон из query). Маскирование на уровне обработчика `slog` закрывает это для любых логов.

- [ ] **Step 1: Падающие тесты**

`backend/internal/platform/logx/mask_test.go`:

```go
package logx_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/logx"
)

func TestMask(t *testing.T) {
	cases := []struct{ in, want string }{
		{"письмо на ivan.petrov@mail.ru не ушло", "письмо на ***@mail.ru не ушло"},
		{"query email=Иван@почта.рф", "query email=***@почта.рф"},
		{"phone=+7 (916) 123-45-67", "phone=***67"},
		{`value "79161234567" invalid`, `value "***67" invalid`},
		{"/v1/users/89161234567/x", "/v1/users/***67/x"},
		// не телефоны: UUID, дата, адрес с портом, короткое число, число внутри слова
		{"id 0192f6d4-8f8e-7c3a-9d2b-426614174000", "id 0192f6d4-8f8e-7c3a-9d2b-426614174000"},
		{"at 2026-10-01T17:00:00Z", "at 2026-10-01T17:00:00Z"},
		{"dial 10.0.0.1:5432", "dial 10.0.0.1:5432"},
		{"order 12345", "order 12345"},
		{"token abc79161234567def", "token abc79161234567def"},
		{"", ""},
	}
	for _, c := range cases {
		if got := logx.Mask(c.in); got != c.want {
			t.Errorf("Mask(%q) = %q, нужно %q", c.in, got, c.want)
		}
	}
}

// Маскируются и сообщение, и строковые атрибуты, и ошибки — в обоих форматах.
func TestLoggerMasksPersonalData(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			log := logx.New(&buf, config.Log{Format: format})
			log.Info("вход ivan@mail.ru",
				"err", errors.New(`parameter "phone" value "79161234567": bad`),
				"path", "/v1/x?email=a.b@c.co",
				"request_id", "0192f6d4-8f8e-7c3a-9d2b-426614174000")
			out := buf.String()
			for _, leak := range []string{"ivan@", "a.b@", "1234567"} {
				if strings.Contains(out, leak) {
					t.Errorf("в логе осталось %q: %s", leak, out)
				}
			}
			if !strings.Contains(out, "0192f6d4-8f8e-7c3a-9d2b-426614174000") {
				t.Errorf("UUID испорчен маскированием: %s", out)
			}
		})
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/logx/... -count=1`
Expected: FAIL — `logx.Mask` не определена.

- [ ] **Step 3: Реализация**

`backend/internal/platform/logx/mask.go`:

```go
package logx

import (
	"log/slog"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// local@домен: локальная часть скрывается, домен остаётся для разбора инцидентов
	emailRe = regexp.MustCompile(`[\p{L}\p{N}._%+\-]+@(?:[\p{L}\p{N}\-]+\.)+\p{L}{2,}`)
	// кандидат в телефон: 10–15 цифр, между ними — пробелы, скобки, дефисы
	phoneRe = regexp.MustCompile(`\+?\d(?:[\s()\-]{0,2}\d){9,14}`)
)

// Mask скрывает персональные данные в строке лога (спека §6.9): почту — до «***@домен»,
// номер телефона — до «***» и двух последних цифр. Цифры, приклеенные к букве, точке,
// двоеточию или дефису (UUID, хеш, адрес, дата), — не телефон и не трогаются.
func Mask(s string) string {
	if s == "" {
		return s
	}
	s = emailRe.ReplaceAllStringFunc(s, func(m string) string {
		return "***" + m[strings.LastIndex(m, "@"):]
	})
	return maskPhones(s)
}

func maskPhones(s string) string {
	locs := phoneRe.FindAllStringIndex(s, -1)
	if locs == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, l := range locs {
		start, end := l[0], l[1]
		before, _ := utf8.DecodeLastRuneInString(s[:start])
		after, _ := utf8.DecodeRuneInString(s[end:])
		if glued(before) || glued(after) {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString("***")
		b.WriteString(lastDigits(s[start:end], 2))
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// glued — символ продолжает «слово»: совпадение — часть UUID, хеша, даты или адреса.
func glued(r rune) bool {
	if r == utf8.RuneError {
		return false // начало или конец строки
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-.:_", r)
}

func lastDigits(s string, n int) string {
	out := make([]byte, 0, n)
	for i := len(s) - 1; i >= 0 && len(out) < n; i-- {
		if s[i] >= '0' && s[i] <= '9' {
			out = append([]byte{s[i]}, out...)
		}
	}
	return string(out)
}

// maskAttr — ReplaceAttr обработчика slog: строки и ошибки проходят через Mask. Сообщение
// (ключ msg) — тоже строковый атрибут.
func maskAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(Mask(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(Mask(err.Error()))
		}
	}
	return a
}
```

В `backend/internal/platform/logx/logx.go` заменить тело `New`:

```go
// New — логгер бинарника. Персональные данные (почта, телефон) маскируются в сообщении,
// строковых атрибутах и ошибках (спека §6.9) — в том числе в текстах ошибок разбора запроса.
func New(w io.Writer, c config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.Level, ReplaceAttr: maskAttr}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
```

- [ ] **Step 4: Тесты проходят**

Из `backend/`: `go test ./internal/platform/logx/... ./internal/platform/httpx/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/platform/logx
git commit -m "Логи: маскирование почты и телефонов в сообщениях, атрибутах и ошибках" -- backend/internal/platform/logx
```

---

### Task 5: Ошибки механизмов платформы, коды и контракт

**Files:**
- Modify: `backend/internal/platform/httpx/problem.go`, `backend/internal/platform/httpx/codes.go`, `backend/internal/platform/httpx/codes_test.go`
- Create: `backend/internal/platform/httpx/problem_test.go`
- Modify: `contracts/openapi/public/root.yaml`, `contracts/openapi/admin/root.yaml`, `contracts/openapi/public/platform/schemas.yaml`, `contracts/openapi/admin/platform/schemas.yaml`
- Modify: `contracts/src/conventions.mjs`, `contracts/test/conventions.test.mjs`
- Generated: `contracts/openapi/{public,admin}.yaml`, `backend/internal/httpapi/{public,admin}/oapi/*.go`, `apps/web/src/api/gen/public.d.ts`, `apps/admin/src/api/gen/admin.d.ts`
- Modify: `apps/web/messages/{ru,en}.json`, `apps/admin/messages/{ru,en}.json`

**Interfaces:**
- Consumes: —
- Produces:
  - `httpx.Problem` + поля `Challenge any` (JSON `challenge`) и `RetryAfter int` (не в JSON — заголовок `Retry-After`).
  - `httpx.ProblemError interface { error; Problem() Problem }`; `httpx.NewError(status int, code string) *httpx.Error` (реализует `ProblemError`).
  - `httpx.WriteProblemValue(w, r, p Problem)`; `httpx.WriteError(log *slog.Logger, w, r, err error)` — `ProblemError` → её ответ, иное → 500 `internal` с ошибкой в логе. `ResponseErrorHandler` зовёт `WriteError`.
  - Константы кодов: `CodeUnauthenticated = "auth.unauthenticated"` (401), `CodeRateLimited = "ratelimit.exceeded"` (429), `CodeHumancheckRequired = "humancheck.required"` (403), `CodeIdempotencyKeyReused = "idempotency.key_reused"` (422), `CodeIdempotencyInProgress = "idempotency.in_progress"` (409), `CodeIdempotencyReplayUnavailable = "idempotency.replay_unavailable"` (409), `CodeFeatureDisabled = "feature.disabled"` (403) — все в `PlatformCodes`.
  - Страж кодов-литералов покрывает все пакеты `internal/platform/*`: вызовы `WriteProblem`/`httpx.WriteProblem` (код — 4-й аргумент), `NewError`/`httpx.NewError` (2-й), литералы `Problem{Code: …}`/`httpx.Problem{Code: …}`.
  - Конвенция контракта: у каждого `enum` — `x-extensible-enum: true`.

- [ ] **Step 1: Падающие тесты ответа на ошибку**

`backend/internal/platform/httpx/problem_test.go`:

```go
package httpx_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wf/backend/internal/platform/httpx"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != httpx.ContentTypeProblem {
		t.Fatalf("Content-Type = %q", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Ошибка механизма платформы, даже обёрнутая, отвечает своей Problem и не пишется в лог как сбой.
func TestWriteErrorProblemError(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	err := fmt.Errorf("teams: создать: %w", httpx.NewError(http.StatusConflict, httpx.CodeIdempotencyInProgress))
	rec := httptest.NewRecorder()
	httpx.WriteError(log, rec, httptest.NewRequest(http.MethodPost, "/x", nil), err)
	if rec.Code != http.StatusConflict {
		t.Fatalf("статус = %d", rec.Code)
	}
	if m := decode(t, rec); m["code"] != httpx.CodeIdempotencyInProgress || m["status"] != float64(409) {
		t.Fatalf("тело: %v", m)
	}
	if logs.Len() != 0 {
		t.Fatalf("ожидаемый ответ попал в лог: %s", logs.String())
	}
}

func TestWriteErrorUnknownIsInternal(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	rec := httptest.NewRecorder()
	httpx.WriteError(log, rec, httptest.NewRequest(http.MethodGet, "/x", nil), errors.New("pgx: сломалось"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("статус = %d", rec.Code)
	}
	m := decode(t, rec)
	if m["code"] != httpx.CodeInternal || strings.Contains(rec.Body.String(), "pgx") {
		t.Fatalf("тело раскрывает причину или код не тот: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "pgx: сломалось") {
		t.Fatalf("причины нет в логе: %s", logs.String())
	}
}

// Retry-After — заголовком, не полем тела; задача антибота — в теле.
func TestWriteProblemValueRetryAfterAndChallenge(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteProblemValue(rec, httptest.NewRequest(http.MethodGet, "/x", nil), httpx.Problem{
		Status: http.StatusTooManyRequests, Code: httpx.CodeRateLimited, RetryAfter: 7,
	})
	if got := rec.Header().Get("Retry-After"); got != "7" {
		t.Fatalf("Retry-After = %q", got)
	}
	m := decode(t, rec)
	for _, k := range []string{"retry_after", "RetryAfter", "challenge"} {
		if _, ok := m[k]; ok {
			t.Fatalf("лишнее поле %s: %v", k, m)
		}
	}

	rec = httptest.NewRecorder()
	httpx.WriteProblemValue(rec, httptest.NewRequest(http.MethodGet, "/x", nil), httpx.Problem{
		Status: http.StatusForbidden, Code: httpx.CodeHumancheckRequired, Challenge: map[string]any{"salt": "s"},
	})
	if m := decode(t, rec); m["challenge"].(map[string]any)["salt"] != "s" {
		t.Fatalf("challenge: %v", m)
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatal("Retry-After без RetryAfter")
	}
}
```

- [ ] **Step 2: Падающий тест расширенного стража литералов**

В `backend/internal/platform/httpx/codes_test.go`:
1. `TestProblemCodesAreConstants` обходит не `"."`, а все каталоги `internal/platform/*` (кроме `testkit` и сгенерированных `*db`), пропуская `_test.go`:

```go
// Страж: коды ошибок платформы выдаются только константами из codes.go — иначе сверка
// PlatformCodes с контрактом (x-error-codes-common) не увидит новый код. Проверяются все
// пакеты платформы: rate limit, антибот, идемпотентность отвечают теми же кодами.
func TestProblemCodesAreConstants(t *testing.T) {
	fset := token.NewFileSet()
	seen := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testkit" || strings.HasSuffix(name, "db") && name != "db" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, lit := range codeLiterals(f) {
			t.Errorf("%s: код ошибки строкой — нужна константа из httpx/codes.go", fset.Position(lit.Pos()))
		}
		seen++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 10 {
		t.Fatalf("просмотрено %d файлов платформы — страж проверяет вхолостую", seen)
	}
}
```

(импорты: `io/fs`, `path/filepath`; `os` больше не нужен).

2. В `TestCodeLiterals` расширить исходник подсадки и ожидаемое число:

```go
	src := `package x
func f() {
	WriteProblem(w, r, 400, "request.invalid", "")
	WriteProblem(w, r, 400, CodeRequestInvalid, "")
	httpx.WriteProblem(w, r, 400, "request.invalid", "")
	_ = Problem{Status: 400, Code: "validation.failed"}
	_ = Problem{Status: 400, Code: CodeValidationFailed}
	_ = httpx.Problem{Status: 429, Code: "ratelimit.exceeded"}
	_ = NewError(409, "idempotency.in_progress")
	_ = httpx.NewError(409, "idempotency.in_progress")
	_ = httpx.NewError(409, httpx.CodeIdempotencyInProgress)
	_ = FieldError{Field: "query.limit", Code: "type"} // правило схемы, не код ошибки
}`
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(codeLiterals(f)); got != 6 {
		t.Fatalf("найдено %d литералов, ожидалось 6", got)
	}
```

3. Заменить `codeLiterals`:

```go
// codeLiterals — строковые литералы на месте кода ошибки: аргумент code у WriteProblem (4-й) и
// NewError (2-й), с префиксом пакета и без, и поле Code у Problem{…}/httpx.Problem{…}
// (Code у FieldError — правило схемы, не код ошибки).
func codeLiterals(f *ast.File) []*ast.BasicLit {
	var out []*ast.BasicLit
	str := func(e ast.Expr) {
		if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			out = append(out, lit)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			switch name := funcName(v.Fun); {
			case name == "WriteProblem" && len(v.Args) > 3:
				str(v.Args[3])
			case name == "NewError" && len(v.Args) > 1:
				str(v.Args[1])
			}
		case *ast.CompositeLit:
			if funcName(v.Type) != "Problem" {
				return true
			}
			for _, el := range v.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Code" {
						str(kv.Value)
					}
				}
			}
		}
		return true
	})
	return out
}

// funcName — имя без пакета: WriteProblem и httpx.WriteProblem одинаковы.
func funcName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}
```

- [ ] **Step 3: Падающий тест конвенции открытых enum'ов**

В `contracts/test/conventions.test.mjs` в конец:

```js
// Спека §8.3: enum'ы открытые — клиент обязан пережить незнакомое значение; генераторам (в том
// числе Dart) это сообщает x-extensible-enum.
test('enum без x-extensible-enum — нарушение', () => {
  const d = base();
  d.components.schemas.Team = {
    type: 'object',
    properties: { status: { type: 'string', enum: ['active', 'paused'] } },
  };
  assert.deepEqual(conventionViolations(d), [
    'components.schemas.Team.properties.status: enum без x-extensible-enum: true',
  ]);
  d.components.schemas.Team.properties.status['x-extensible-enum'] = true;
  assert.deepEqual(conventionViolations(d), []);
});

test('enum в параметре операции тоже помечается', () => {
  const d = base();
  post(d).parameters.push({ name: 'sort', in: 'query', schema: { type: 'string', enum: ['a'] } });
  assert.deepEqual(conventionViolations(d), [
    'paths./v1/teams.post.parameters.1.schema: enum без x-extensible-enum: true',
  ]);
});
```

- [ ] **Step 4: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/httpx/... -count=1` — FAIL (нет `WriteError`, `NewError`, полей `Problem`).
Из корня: `pnpm --filter @wf/contracts test` — FAIL на двух новых тестах.

- [ ] **Step 5: Реализация в httpx**

`backend/internal/platform/httpx/codes.go` — в блок констант после `CodeMethodNotAllowed`:

```go
	CodeUnauthenticated              = "auth.unauthenticated"           // 401: нет действующего access-токена или сессия отозвана
	CodeRateLimited                  = "ratelimit.exceeded"             // 429: лимит частоты, когда повторить — Retry-After
	CodeHumancheckRequired           = "humancheck.required"            // 403: нужна проверка «человек ли», задача — в challenge
	CodeIdempotencyKeyReused         = "idempotency.key_reused"         // 422: тот же Idempotency-Key с другим запросом
	CodeIdempotencyInProgress        = "idempotency.in_progress"        // 409: запрос с этим ключом ещё выполняется
	CodeIdempotencyReplayUnavailable = "idempotency.replay_unavailable" // 409: действие выполнено, ответ не сохранился — перечитать состояние
	CodeFeatureDisabled              = "feature.disabled"               // 403: функция выключена флагом (в городе)
```

и `PlatformCodes`:

```go
var PlatformCodes = []string{
	CodeInternal, CodeRequestInvalid, CodeRequestTooLarge,
	CodeValidationFailed, CodeNotFound, CodeMethodNotAllowed,
	CodeUnauthenticated, CodeRateLimited, CodeHumancheckRequired,
	CodeIdempotencyKeyReused, CodeIdempotencyInProgress, CodeIdempotencyReplayUnavailable,
	CodeFeatureDisabled,
}
```

`backend/internal/platform/httpx/problem.go`:
1. В `Problem` после `Errors`:

```go
	// Challenge — задача антибота у humancheck.required (протокол ALTCHA): клиент решает её и
	// повторяет запрос с решением в заголовке X-WF-Humancheck.
	Challenge any `json:"challenge,omitempty"`
	// RetryAfter — через сколько секунд повторить (ratelimit.exceeded); уходит заголовком
	// Retry-After, не в теле.
	RetryAfter int `json:"-"`
```

2. После `WriteValidationProblem`:

```go
// WriteProblemValue — готовая Problem: с задачей антибота, Retry-After.
func WriteProblemValue(w http.ResponseWriter, r *http.Request, p Problem) {
	writeProblem(w, r, p)
}

// ProblemError — ошибка, которая сама знает ответ клиенту: rate limit, антибот,
// идемпотентность, выключенная функция. Хендлер или механизм платформы возвращает её как
// обычную ошибку (можно обёрнутой через %w) — WriteError отвечает её Problem, а не 500.
type ProblemError interface {
	error
	Problem() Problem
}

// Error — ProblemError по статусу и коду.
type Error struct{ p Problem }

// NewError — ProblemError с кодом из codes.go (литерал ловит страж TestProblemCodesAreConstants).
func NewError(status int, code string) *Error {
	return &Error{p: Problem{Status: status, Code: code}}
}

func (e *Error) Error() string { return "httpx: " + e.p.Code }

func (e *Error) Problem() Problem { return e.p }

// WriteError — ответ на ошибку хендлера или middleware: ProblemError — её Problem (в лог не
// пишется: это ожидаемый ответ), иное — 500 internal, причина — только в лог.
func WriteError(log *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	if pe, ok := errors.AsType[ProblemError](err); ok {
		writeProblem(w, r, pe.Problem())
		return
	}
	log.ErrorContext(r.Context(), "handler error", "err", err, "request_id", middleware.GetReqID(r.Context()))
	WriteProblem(w, r, http.StatusInternalServerError, CodeInternal, "")
}
```

3. В `writeProblem` перед `w.Header().Set("Content-Type", …)`:

```go
	if p.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(p.RetryAfter))
	}
```

4. `ResponseErrorHandler`:

```go
// ResponseErrorHandler — хендлер вернул ошибку: WriteError.
func ResponseErrorHandler(log *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) { WriteError(log, w, r, err) }
}
```

(импорты `errors`, `strconv`).

- [ ] **Step 6: Конвенция enum'ов**

В `contracts/src/conventions.mjs` перед `for (const ref of refs(doc))`:

```js
  // Спека §8.3: enum'ы открытые — клиент обязан пережить незнакомое значение, добавление значения
  // не ломает контракт. Генераторам (в том числе Dart) это сообщает x-extensible-enum.
  for (const where of closedEnums(doc)) out.push(`${where}: enum без x-extensible-enum: true`);
```

и функцию после `refs`:

```js
function closedEnums(node, path = [], out = []) {
  if (Array.isArray(node)) node.forEach((n, i) => closedEnums(n, [...path, i], out));
  else if (node && typeof node === 'object') {
    if (Array.isArray(node.enum) && node['x-extensible-enum'] !== true) out.push(path.join('.'));
    for (const [k, v] of Object.entries(node)) {
      if (k !== 'enum') closedEnums(v, [...path, k], out);
    }
  }
  return out;
}
```

- [ ] **Step 7: Контракты**

В `contracts/openapi/public/platform/schemas.yaml` и `contracts/openapi/admin/platform/schemas.yaml` у `Health.properties.status` после `enum: [ok]`:

```yaml
      x-extensible-enum: true
```

В **обоих** `root.yaml` (`public` и `admin`, одинаково — тест бандлов сверяет копии `Problem`):

1. `x-error-codes-common` — дописать после `http.method_not_allowed`:

```yaml
  - auth.unauthenticated
  - ratelimit.exceeded
  - humancheck.required
  - idempotency.key_reused
  - idempotency.in_progress
  - idempotency.replay_unavailable
  - feature.disabled
```

2. В `info.description`, пункт «**Ошибки**», после списка общих кодов дописать (тем же стилем, перенос строк ≤ 100 символов):

```markdown
      Механизмы платформы: `auth.unauthenticated` (401 — нет действующего входа, обновите токен
      или войдите заново), `ratelimit.exceeded` (429 — повторите через `Retry-After` секунд),
      `humancheck.required` (403 — решите задачу из поля `challenge` и повторите запрос с
      решением в заголовке `X-WF-Humancheck`), `idempotency.key_reused` (422 — ключ уже
      использован с другим запросом), `idempotency.in_progress` (409 — запрос с этим ключом ещё
      выполняется), `idempotency.replay_unavailable` (409 — действие выполнено, перечитайте
      состояние), `feature.disabled` (403 — функция выключена в вашем городе).
```

(в админском описании — без фразы про город, если там иначе сформулировано; смысл тот же).

3. `components.responses.Problem` — заголовок:

```yaml
    Problem:
      description: Ошибка в формате RFC 9457
      headers:
        Retry-After:
          description: Через сколько секунд повторить — только у 429 ratelimit.exceeded
          schema:
            type: integer
            minimum: 1
          example: 30
      content:
        # как было
```

4. `components.schemas.Problem.properties` — после `errors`:

```yaml
        challenge:
          type: object
          description: >-
            Задача антибота — только у humancheck.required. Формат протокола ALTCHA, поля в
            camelCase, как их ждёт виджет ALTCHA: найдите число от 0 до maxNumber, при котором
            SHA-256(salt + число) в hex равен challenge, и повторите запрос с решением в заголовке
            X-WF-Humancheck (base64 от JSON с полями algorithm, challenge, number, salt, signature)
          required: [algorithm, challenge, maxNumber, salt, signature]
          additionalProperties: false
          properties:
            algorithm:
              type: string
              description: Хеш-функция задачи; сейчас всегда SHA-256
              example: SHA-256
            challenge:
              type: string
              description: SHA-256 от salt и искомого числа, hex
              example: 4d5b2c0e7f9a1b3c5d7e9f0a2b4c6d8e0f1a3b5c7d9e1f2a4b6c8d0e2f4a6b8c
            maxNumber:
              type: integer
              format: int64
              description: Верхняя граница перебора
              example: 100000
            salt:
              type: string
              description: Соль с параметрами срока задачи (expires, Unix-время) и ключа подписи (kid)
              example: 'b1946ac92492d2347c62?expires=1790000000&kid=Hx3k9QpLm2s&'
            signature:
              type: string
              description: HMAC-SHA-256 задачи на ключе сервера, hex
              example: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
```

Пример `salt` — в форме, которую выдаёт сервер (Task 11): `<hex соли>?expires=…&kid=…&`.

- [ ] **Step 8: Тексты ошибок в локалях фронтов**

В `apps/web/messages/ru.json` и `apps/admin/messages/ru.json` в объект `errors` (вложенно по точкам кода):

```json
    "auth": { "unauthenticated": "Войдите в аккаунт заново" },
    "ratelimit": { "exceeded": "Слишком много запросов. Подождите немного" },
    "humancheck": { "required": "Подтвердите, что вы не робот" },
    "idempotency": {
      "key_reused": "Это действие уже выполнялось с другими данными. Обновите страницу",
      "in_progress": "Действие ещё выполняется. Подождите",
      "replay_unavailable": "Действие выполнено. Обновите страницу"
    },
    "feature": { "disabled": "Эта функция пока недоступна" }
```

В `en.json` обоих приложений — те же ключи с пустыми строками (как у существующих `errors.*`).

- [ ] **Step 9: Кодогенерация (только контракты)**

Из корня, по одной команде (один pnpm-процесс за раз):

```bash
pnpm --filter @wf/contracts bundle
pnpm --filter @wf/contracts gen:web
pnpm --filter @wf/contracts gen:admin
```

Из `backend/`:

```bash
go tool -modfile=tools/go.mod oapi-codegen -config internal/httpapi/public/oapi-codegen.yaml ../contracts/openapi/public.yaml
go tool -modfile=tools/go.mod oapi-codegen -config internal/httpapi/admin/oapi-codegen.yaml ../contracts/openapi/admin.yaml
```

Expected: изменились бандлы, `oapi/*.go`, `*.d.ts`; sqlc не запускается (его ведёт Task 3).

- [ ] **Step 10: Тесты проходят**

Из корня: `pnpm --filter @wf/contracts test`, затем `./task web:test`, затем `./task admin:test` (страж «каждый код контракта есть в `errors.*` локалей»).
Из `backend/`: `go test ./internal/platform/httpx/... ./internal/httpapi/... -count=1` (`TestPlatformCodesDocumented` сверяет `PlatformCodes` с `x-error-codes-common`).
Из корня: `CONTRACTS_BASE=origin/main ./task contracts:breaking` — Expected: без ломающих изменений (добавлены коды, необязательное поле ответа, заголовок, расширение).
Expected: всё PASS.

Подсадка бага: в `backend/internal/platform/flags/flags.go` временно добавить `var _ = httpx.NewError(403, "feature.disabled")` (с импортом httpx) → `go test ./internal/platform/httpx/... -run TestProblemCodesAreConstants` падает с позицией в `flags.go`; вернуть.

- [ ] **Step 11: Commit**

```bash
git add backend/internal/platform/httpx backend/internal/httpapi/public/oapi backend/internal/httpapi/admin/oapi \
  contracts/openapi contracts/src/conventions.mjs contracts/test/conventions.test.mjs \
  apps/web/messages apps/admin/messages apps/web/src/api/gen apps/admin/src/api/gen
git commit -m "Платформа: ошибки механизмов как Problem, коды auth/ratelimit/humancheck/idempotency/feature, открытые enum'ы" -- backend/internal/platform/httpx backend/internal/httpapi/public/oapi backend/internal/httpapi/admin/oapi contracts/openapi contracts/src/conventions.mjs contracts/test/conventions.test.mjs apps/web/messages apps/admin/messages apps/web/src/api/gen apps/admin/src/api/gen
```

---

### Task 6: Маршрут контракта в `ctx`, требование аутентификации, таймаут

**Files:**
- Create: `backend/internal/platform/httpx/route.go`, `backend/internal/platform/httpx/route_test.go`, `backend/internal/platform/httpx/timeout.go`
- Modify: `backend/internal/platform/httpx/validate.go`, `backend/internal/platform/httpx/validate_test.go`
- Modify: `backend/internal/httpapi/public/handler.go`, `backend/internal/httpapi/admin/handler.go`

**Interfaces:**
- Consumes: `httpx.CodeUnauthenticated` (Task 5).
- Produces:
  - `httpx.Routes(spec *openapi3.T) (func(http.Handler) http.Handler, error)` — кладёт `*httpx.Route` в `ctx`; маршрут не из контракта — без `Route`.
  - `httpx.RouteFrom(ctx) (*httpx.Route, bool)`; методы `(*Route).Operation() *openapi3.Operation`, `(*Route).Extension(name string) string`.
  - `httpx.ValidateOptions{ Authenticated func(context.Context) bool; WWWAuthenticate string }`; `httpx.ValidateRequests(o ValidateOptions) func(http.Handler) http.Handler` — читает маршрут из `ctx` (ставится после `Routes`); операция с `security`, а `Authenticated` ложно или nil, — `401 auth.unauthenticated` (раньше нарушений схемы).
  - `httpx.Timeout(d time.Duration) func(http.Handler) http.Handler` — крайний срок в `ctx`.

- [ ] **Step 1: Падающие тесты маршрута и таймаута**

`backend/internal/platform/httpx/route_test.go`:

```go
package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/httpx"
)

const routesSpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /things/{id}:
    get:
      operationId: getThing
      x-rate-limit: auth
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses: {"200": {description: ok}}
`

func TestRoutesPutsOperationInContext(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData([]byte(routesSpec))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(spec)
	if err != nil {
		t.Fatal(err)
	}
	var got *httpx.Route
	var found bool
	h := routes(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, found = httpx.RouteFrom(r.Context())
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/things/42", nil))
	if !found || got.Operation().OperationID != "getThing" {
		t.Fatalf("маршрут: found=%v %+v", found, got)
	}
	if got.Extension("x-rate-limit") != "auth" || got.Extension("x-nope") != "" {
		t.Fatalf("расширения: %q %q", got.Extension("x-rate-limit"), got.Extension("x-nope"))
	}

	// маршрут не из контракта и чужой метод — дальше без Route: ответит роутер (404/405)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/docs", nil),
		httptest.NewRequest(http.MethodDelete, "/things/42", nil),
	} {
		found = true
		h.ServeHTTP(httptest.NewRecorder(), req)
		if found {
			t.Fatalf("%s %s: Route в ctx", req.Method, req.URL.Path)
		}
	}
}

func TestTimeoutSetsDeadline(t *testing.T) {
	var left time.Duration
	var ok bool
	h := httpx.Timeout(3 * time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var dl time.Time
		dl, ok = r.Context().Deadline()
		left = time.Until(dl)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	if !ok || left <= 0 || left > 3*time.Second {
		t.Fatalf("крайний срок: ok=%v осталось %v", ok, left)
	}
}
```

- [ ] **Step 2: Падающие тесты требования аутентификации**

В `backend/internal/platform/httpx/validate_test.go`:
1. В `thingsSpec` в `paths` добавить анонимную операцию (после `/things`):

```yaml
  /public:
    get:
      operationId: getPublic
      security: []
      parameters:
        - name: limit
          in: query
          schema: {type: integer, maximum: 100}
      responses:
        "204": {description: ok}
```

2. `validated` собирает маршрут и валидатор с опциями; старые случаи идут «аутентифицированными»:

```go
// validated — лимит тела, маршрут и валидация по thingsSpec поверх next.
func validated(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	return validatedWith(t, httpx.ValidateOptions{Authenticated: func(context.Context) bool { return true }}, next)
}

func validatedWith(t *testing.T, o httpx.ValidateOptions, next http.Handler) http.Handler {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromData([]byte(thingsSpec))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(spec)
	if err != nil {
		t.Fatal(err)
	}
	return httpx.LimitBody(64)(routes(httpx.ValidateRequests(o)(next)))
}
```

3. Случай `{name: "без Authorization запрос доходит до хендлера", …}` удалить (теперь это решает `Authenticated`).

4. Новый тест:

```go
// Требование входа — из security контракта (спека §6.1): нет Principal — 401 раньше нарушений
// схемы (неаутентифицированному не раскрываем правила полей); анонимная операция — без входа.
func TestValidateRequestsRequiresAuthentication(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	nobody := httpx.ValidateOptions{WWWAuthenticate: "Bearer"}
	cases := []struct {
		name   string
		o      httpx.ValidateOptions
		method string
		url    string
		body   string
		status int
	}{
		{"операция с security без входа", nobody, http.MethodPost, "/things", `{"name":"ab","kind":"a"}`, 401},
		{"без входа и с невалидным телом — всё равно 401", nobody, http.MethodPost, "/things", `{"name":"a"}`, 401},
		{"Authenticated не задана — никто не вошёл", httpx.ValidateOptions{}, http.MethodPost, "/things", `{"name":"ab","kind":"a"}`, 401},
		{"анонимная операция без входа", nobody, http.MethodGet, "/public", "", 204},
		{"анонимная операция: схема проверяется", nobody, http.MethodGet, "/public?limit=500", "", 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := validatedWith(t, c.o, ok)
			var body io.Reader
			if c.body != "" {
				body = strings.NewReader(c.body)
			}
			req := httptest.NewRequestWithContext(context.Background(), c.method, c.url, body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", strings.Repeat("k", 16))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, c.status, rec.Body.String())
			}
			if c.status != http.StatusUnauthorized {
				return
			}
			var p httpx.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != httpx.CodeUnauthenticated {
				t.Fatalf("тело: %s", rec.Body.String())
			}
			if want := c.o.WWWAuthenticate; rec.Header().Get("WWW-Authenticate") != want {
				t.Fatalf("WWW-Authenticate = %q, want %q", rec.Header().Get("WWW-Authenticate"), want)
			}
		})
	}
}
```

- [ ] **Step 3: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/httpx/... -count=1`
Expected: FAIL — нет `Routes`, `RouteFrom`, `Timeout`, `ValidateOptions`.

- [ ] **Step 4: Реализация**

`backend/internal/platform/httpx/route.go`:

```go
package httpx

import (
	"context"
	"fmt"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// Route — операция контракта, на которую пришёл запрос. Кладёт Routes; читают валидатор,
// rate limit (x-rate-limit) и идемпотентность — маршрут ищется один раз на запрос.
type Route struct {
	route  *routers.Route
	params map[string]string
}

func (r *Route) Operation() *openapi3.Operation { return r.route.Operation }

// Extension — строковое расширение операции (x-rate-limit); нет или не строка — "".
func (r *Route) Extension(name string) string {
	s, _ := r.route.Operation.Extensions[name].(string)
	return s
}

type routeKey struct{}

func RouteFrom(ctx context.Context) (*Route, bool) {
	rt, ok := ctx.Value(routeKey{}).(*Route)
	return rt, ok
}

// Routes находит операцию контракта по методу и пути. Маршрут не из контракта (/docs, 404,
// чужой метод) идёт дальше без Route: следующие слои его пропускают, отвечает роутер.
func Routes(spec *openapi3.T) (func(http.Handler) http.Handler, error) {
	spec.Servers = nil // иначе FindRoute сверяет хост и префикс из servers
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("httpx: роутер контракта: %w", err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if route, params, err := router.FindRoute(r); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), routeKey{}, &Route{route: route, params: params}))
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}
```

`backend/internal/platform/httpx/timeout.go`:

```go
package httpx

import (
	"context"
	"net/http"
	"time"
)

// Timeout — крайний срок запроса в ctx (спека §6.1): запросы к базе и исходящие вызовы
// прерываются по нему. Ответ не подменяется — хендлер сам вернёт ошибку отмены.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

`backend/internal/platform/httpx/validate.go` — заменить `ValidateRequests`:

```go
// ValidateOptions — настройки проверки запроса.
type ValidateOptions struct {
	// Authenticated — есть ли в ctx аутентифицированный пользователь (Principal кладёт слой
	// аутентификации раньше, спека §6.1). nil — никто не вошёл: операции с security — 401.
	Authenticated func(context.Context) bool
	// WWWAuthenticate — схема в заголовке ответа 401 (RFC 9110 §11.6.1), например "Bearer".
	WWWAuthenticate string
}

var errUnauthenticated = errors.New("httpx: нет аутентификации")

// ValidateRequests проверяет запрос по контракту до strict-хендлера: strict-сервер
// oapi-codegen схему сам не валидирует (minLength, enum, format, обязательные заголовки).
// Маршрут берётся из ctx (Routes); запрос без маршрута пропускается — его ответит роутер.
// Требование входа — из security операции: нет Principal — 401 auth.unauthenticated раньше
// нарушений схемы.
func ValidateRequests(o ValidateOptions) func(http.Handler) http.Handler {
	// SkipSettingDefaults: валидатор только проверяет. Иначе kin-openapi дописывает default
	// в query и заголовки и перекодирует тело — хендлер получил бы не то, что прислал клиент.
	opts := &openapi3filter.Options{
		AuthenticationFunc: func(ctx context.Context, _ *openapi3filter.AuthenticationInput) error {
			if o.Authenticated != nil && o.Authenticated(ctx) {
				return nil
			}
			return errUnauthenticated
		},
		MultiError:          true,
		SkipSettingDefaults: true,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rt, ok := RouteFrom(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			// … чтение тела и 413 — как было …
			in := &openapi3filter.RequestValidationInput{Request: r, PathParams: rt.params, Route: rt.route, Options: opts}
			if verr := openapi3filter.ValidateRequest(r.Context(), in); verr != nil {
				if _, unauth := errors.AsType[*openapi3filter.SecurityRequirementsError](verr); unauth {
					if o.WWWAuthenticate != "" {
						w.Header().Set("WWW-Authenticate", o.WWWAuthenticate)
					}
					WriteProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "")
					return
				}
				// … fieldErrors и request.invalid — как было …
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

Блоки «как было» перенести из текущего тела без изменений (чтение тела в `body`, `MaxBytesError` → 413, возврат тела в запрос; `fieldErrors` → `WriteValidationProblem`, иначе `request.invalid`). Импорт `gorillamux` из `validate.go` уходит в `route.go`; добавить `context`. Поиск `SecurityRequirementsError` через `errors.AsType` работает и внутри `openapi3.MultiError` (у неё есть метод `As`).

- [ ] **Step 5: Хендлеры бинарников на новый конвейер**

В `backend/internal/httpapi/public/handler.go` вместо `validate, err := httpx.ValidateRequests(spec)`:

```go
	routes, err := httpx.Routes(spec)
	if err != nil {
		return nil, err
	}
	// аутентификация (Task 9) встанет между routes и validate; пока Principal нет ни у кого —
	// операции с security отвечают 401
	validate := httpx.ValidateRequests(httpx.ValidateOptions{WWWAuthenticate: "Bearer"})
```

и `router := httpx.NewRouter(log, httpx.LimitBody(httpx.MaxBodyBytes), routes, validate)`; комментарий «порядок по спеке §6.1: аутентификация (план 3/3) встаёт между LimitBody и validate» заменить на «порядок — спека §6.1; полный конвейер собирается в Task 19». То же в `backend/internal/httpapi/admin/handler.go`, но `ValidateOptions{}` (вход сотрудников — cookie, спека identity; заголовок `WWW-Authenticate` у админки не Bearer).

- [ ] **Step 6: Тесты проходят**

Из `backend/`: `go test ./internal/platform/httpx/... ./internal/httpapi/... -count=1`
Expected: PASS (`/v1/health` анонимна — `TestHealthMatchesContract` проходит без входа).

Подсадка бага: в `AuthenticationFunc` временно вернуть `nil` всегда → `TestValidateRequestsRequiresAuthentication` падает на трёх случаях 401; вернуть.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/platform/httpx backend/internal/httpapi/public/handler.go backend/internal/httpapi/admin/handler.go
git commit -m "HTTP: маршрут контракта в ctx, вход по security контракта (401), крайний срок запроса" -- backend/internal/platform/httpx backend/internal/httpapi/public/handler.go backend/internal/httpapi/admin/handler.go
```

---

### Task 7: Реальный IP клиента и доверие к BFF

**Files:**
- Create: `backend/internal/platform/peer/peer.go`, `backend/internal/platform/peer/peer_test.go`

**Interfaces:**
- Consumes: —
- Produces:
  - `peer.Info{ IP netip.Addr; ViaBFF bool; Device string }`.
  - `peer.Config{ TrustedProxies, BFFNets []netip.Prefix; BFFSecrets []string }`; `(peer.Config).Validate() error` — секреты не короче 32 символов, BFF без секретов не настраивается.
  - `peer.Middleware(c peer.Config) func(http.Handler) http.Handler`; `peer.From(ctx) peer.Info` (нет — нулевой `Info`); `peer.With(ctx, peer.Info) context.Context` (для тестов других пакетов).
  - Заголовки: `peer.HeaderClientIP = "X-WF-Client-IP"`, `peer.HeaderDevice = "X-WF-Device"`, `peer.HeaderBFFSecret = "X-WF-BFF-Secret"`.

- [ ] **Step 1: Падающие тесты**

`backend/internal/platform/peer/peer_test.go`:

```go
package peer_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"wf/backend/internal/platform/peer"
)

var (
	secret = strings.Repeat("s", 32)
	cfg    = peer.Config{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		BFFNets:        []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
		BFFSecrets:     []string{strings.Repeat("o", 32), secret}, // ротация: старый и новый
	}
)

type req struct {
	remote  string
	headers map[string]string
}

func resolve(t *testing.T, c peer.Config, r req) (peer.Info, http.Header) {
	t.Helper()
	var got peer.Info
	var seen http.Header
	h := peer.Middleware(c)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, seen = peer.From(r.Context()), r.Header.Clone()
	}))
	hr := httptest.NewRequest(http.MethodGet, "/", nil)
	hr.RemoteAddr = r.remote
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), hr)
	return got, seen
}

func TestResolve(t *testing.T) {
	bff := map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7", peer.HeaderDevice: "web-dev-1"}
	cases := []struct {
		name   string
		r      req
		ip     string
		viaBFF bool
		device string
	}{
		{"прямой клиент", req{"203.0.113.5:4000", nil}, "203.0.113.5", false, ""},
		// Review Focus 1: подделка заголовков прямым клиентом не работает
		{"прямой клиент с X-WF-Client-IP", req{"203.0.113.5:4000", map[string]string{peer.HeaderClientIP: "1.1.1.1"}}, "203.0.113.5", false, ""},
		{"прямой клиент с X-Forwarded-For", req{"203.0.113.5:4000", map[string]string{"X-Forwarded-For": "1.1.1.1"}}, "203.0.113.5", false, ""},
		{"за прокси хостинга", req{"10.1.2.3:4000", map[string]string{"X-Forwarded-For": "203.0.113.9"}}, "203.0.113.9", false, ""},
		// клиент дописал левые адреса слева — берём правый недоверенный
		{"левые адреса в X-Forwarded-For", req{"10.1.2.3:4000", map[string]string{"X-Forwarded-For": "1.1.1.1, 203.0.113.9, 10.9.9.9"}}, "203.0.113.9", false, ""},
		{"прокси без X-Forwarded-For", req{"10.1.2.3:4000", nil}, "10.1.2.3", false, ""},
		{"мусор в X-Forwarded-For — до мусора", req{"10.1.2.3:4000", map[string]string{"X-Forwarded-For": "203.0.113.9, nonsense"}}, "10.1.2.3", false, ""},
		{"BFF с верным секретом", req{"192.0.2.10:5000", bff}, "198.51.100.7", true, "web-dev-1"},
		{"BFF за прокси хостинга", req{"10.1.2.3:4000", map[string]string{"X-Forwarded-For": "192.0.2.10",
			peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7"}}, "198.51.100.7", true, ""},
		{"BFF с неверным секретом", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: strings.Repeat("x", 32), peer.HeaderClientIP: "198.51.100.7"}}, "192.0.2.10", false, ""},
		{"верный секрет не с адреса BFF", req{"203.0.113.5:4000", bff}, "203.0.113.5", false, ""},
		{"BFF с битым X-WF-Client-IP", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "nope"}}, "192.0.2.10", false, ""},
		{"IPv4 в IPv6-обёртке", req{"[::ffff:203.0.113.5]:4000", nil}, "203.0.113.5", false, ""},
		{"IPv6", req{"[2001:db8::1]:4000", nil}, "2001:db8::1", false, ""},
		{"метка устройства длиннее 128 — отброшена", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret,
			peer.HeaderClientIP: "198.51.100.7", peer.HeaderDevice: strings.Repeat("d", 129)}}, "198.51.100.7", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := resolve(t, cfg, c.r)
			if got.IP != netip.MustParseAddr(c.ip) || got.ViaBFF != c.viaBFF || got.Device != c.device {
				t.Fatalf("got %+v, want ip=%s viaBFF=%v device=%q", got, c.ip, c.viaBFF, c.device)
			}
		})
	}
}

// Секрет BFF не уходит дальше по конвейеру (логи, хендлеры) ни при каком исходе.
func TestSecretHeaderRemoved(t *testing.T) {
	_, h := resolve(t, cfg, req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7"}})
	if h.Get(peer.HeaderBFFSecret) != "" {
		t.Fatal("секрет BFF дошёл до хендлера")
	}
}

// Без BFF в конфиге заголовки BFF не действуют вообще.
func TestNoBFFConfigured(t *testing.T) {
	got, _ := resolve(t, peer.Config{}, req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7"}})
	if got.ViaBFF || got.IP != netip.MustParseAddr("192.0.2.10") {
		t.Fatalf("got %+v", got)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	short := cfg
	short.BFFSecrets = []string{"short"}
	if short.Validate() == nil {
		t.Fatal("короткий секрет принят")
	}
	noSecret := peer.Config{BFFNets: cfg.BFFNets}
	if noSecret.Validate() == nil {
		t.Fatal("адреса BFF без секрета приняты")
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/peer/... -count=1`
Expected: FAIL — пакета нет.

- [ ] **Step 3: Реализация**

`backend/internal/platform/peer/peer.go`:

```go
// Package peer — кто на том конце запроса (спека бэкенда §8.4): IP клиента и метка устройства
// браузера. Заголовкам верим только от доверенных: X-Forwarded-For — от прокси хостинга
// (TrustedProxies), X-WF-Client-IP и X-WF-Device — от BFF (адрес из BFFNets и верный секрет).
// Прямой клиент подделать свой IP не может: его заголовки не читаются.
package peer

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

const (
	HeaderClientIP  = "X-WF-Client-IP"
	HeaderDevice    = "X-WF-Device"
	HeaderBFFSecret = "X-WF-BFF-Secret"
	headerForwarded = "X-Forwarded-For"

	minSecretLen = 32
	maxDeviceLen = 128
)

// Info — собеседник запроса.
type Info struct {
	IP     netip.Addr // IP клиента; нулевой — адрес не разобран
	ViaBFF bool       // запрос пришёл от BFF с верным секретом
	Device string     // метка устройства браузера из cookie BFF; только от BFF
}

type Config struct {
	TrustedProxies []netip.Prefix // балансировщик хостинга: им верим X-Forwarded-For
	BFFNets        []netip.Prefix // адреса BFF (Next.js)
	BFFSecrets     []string       // общий секрет BFF ↔ API, списком для ротации
}

// Validate — до старта: слабый секрет или BFF без секрета — конфигурация ошибочна.
func (c Config) Validate() error {
	var errs []error
	if len(c.BFFNets) > 0 && len(c.BFFSecrets) == 0 {
		errs = append(errs, errors.New("peer: адреса BFF заданы, а секрета нет"))
	}
	for i, s := range c.BFFSecrets {
		if len(s) < minSecretLen {
			errs = append(errs, fmt.Errorf("peer: секрет BFF №%d короче %d символов", i+1, minSecretLen))
		}
	}
	return errors.Join(errs...)
}

type infoKey struct{}

func With(ctx context.Context, i Info) context.Context { return context.WithValue(ctx, infoKey{}, i) }

func From(ctx context.Context) Info {
	i, _ := ctx.Value(infoKey{}).(Info)
	return i
}

func Middleware(c Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := resolve(c, r)
			// секрет дальше не нужен никому — и в логи не попадёт
			r.Header.Del(HeaderBFFSecret)
			next.ServeHTTP(w, r.WithContext(With(r.Context(), info)))
		})
	}
}

func resolve(c Config, r *http.Request) Info {
	var addr netip.Addr
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		addr = ap.Addr().Unmap()
	}
	if contains(c.TrustedProxies, addr) {
		addr = forwarded(r.Header.Values(headerForwarded), c.TrustedProxies, addr)
	}
	info := Info{IP: addr}
	if !contains(c.BFFNets, addr) || !secretOK(r.Header.Get(HeaderBFFSecret), c.BFFSecrets) {
		return info
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(HeaderClientIP)))
	if err != nil {
		return info
	}
	return Info{IP: ip.Unmap(), ViaBFF: true, Device: device(r.Header.Get(HeaderDevice))}
}

// forwarded — справа налево до первого недоверенного адреса: левее него клиент мог дописать
// что угодно. Неразбираемая запись обрывает поиск — верим последнему разобранному.
func forwarded(values []string, trusted []netip.Prefix, peer netip.Addr) netip.Addr {
	var hops []string
	for _, v := range values {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return peer
		}
		peer = ip.Unmap()
		if !contains(trusted, peer) {
			return peer
		}
	}
	return peer
}

func contains(nets []netip.Prefix, a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	for _, n := range nets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

func secretOK(got string, secrets []string) bool {
	if got == "" {
		return false
	}
	ok := 0
	for _, s := range secrets {
		ok |= subtle.ConstantTimeCompare([]byte(got), []byte(s))
	}
	return ok == 1
}

// device — метка устройства: печатный ASCII до 128 символов, иначе пусто.
func device(s string) string {
	if len(s) > maxDeviceLen {
		return ""
	}
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7e {
			return ""
		}
	}
	return s
}
```

Проверить случай «мусор в X-Forwarded-For — до мусора»: справа `nonsense` не разбирается → возвращается адрес прокси `10.1.2.3`. Так и задумано: цепочка испорчена — верим только тому, кого видим.

- [ ] **Step 4: Тесты проходят**

Из `backend/`: `go test ./internal/platform/peer/... -count=1`
Expected: PASS.

Подсадка бага: в `resolve` убрать проверку `contains(c.BFFNets, addr)` → падает «верный секрет не с адреса BFF»; вернуть.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/platform/peer
git commit -m "Платформа: реальный IP клиента — прокси хостинга и BFF по адресу и секрету" -- backend/internal/platform/peer
```

---

## Фаза B. Кто пришёл

### Task 8: Ключи из окружения, access-токены, примитивы refresh

**Files:**
- Create: `backend/internal/platform/keys/keys.go`, `backend/internal/platform/keys/keys_test.go`
- Create: `backend/internal/platform/token/token.go`, `backend/internal/platform/token/token_test.go`
- Create: `backend/internal/platform/token/refresh.go`, `backend/internal/platform/token/refresh_test.go`

**Interfaces:**
- Consumes: `clock.Clock`, `clocktest.Fake` (Task 2).
- Produces:
  - `keys.Decode(s string) ([]byte, error)` — base64 стандартный или URL-алфавит, с паддингом или без; `keys.ID(material []byte) string` — первые 8 байт SHA-256 в base64url (11 символов); `keys.Generate() (string, error)` — 32 случайных байта в base64 (тесты, скрипт `.env`).
  - `token.ParseKeys(seeds []string) (*token.Keys, error)` — сиды Ed25519 (32 байта), первый подписывает, все проверяют.
  - `token.NewIssuer(k *token.Keys, c clock.Clock) *token.Issuer`; `(*Issuer).Access(userID, sessionID uuid.UUID) (string, time.Time, error)` — JWT и срок; `(*Issuer).Verify(raw string) (token.Claims, error)`; `token.Claims{UserID, SessionID uuid.UUID; IssuedAt, ExpiresAt time.Time}`; `token.ErrInvalid`; `token.AccessTTL = 10 * time.Minute`.
  - `token.NewRefresh() (raw string, hash []byte, err error)`; `token.HashRefresh(raw string) []byte`; `token.SealSuccessor(presented, successor string) ([]byte, error)`; `token.OpenSuccessor(presented string, sealed []byte) (string, error)`.

- [ ] **Step 1: Падающие тесты ключей**

`backend/internal/platform/keys/keys_test.go`:

```go
package keys_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"wf/backend/internal/platform/keys"
)

func TestDecodeAcceptsAllBase64Forms(t *testing.T) {
	raw := bytes.Repeat([]byte{0xfb, 0xff, 0x01}, 11) // 33 байта: есть символы + / и - _
	for name, enc := range map[string]*base64.Encoding{
		"std": base64.StdEncoding, "raw std": base64.RawStdEncoding,
		"url": base64.URLEncoding, "raw url": base64.RawURLEncoding,
	} {
		got, err := keys.Decode(enc.EncodeToString(raw))
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("%s: %v %x", name, err, got)
		}
	}
	if _, err := keys.Decode("не base64!"); err == nil {
		t.Fatal("мусор принят")
	}
	if _, err := keys.Decode(""); err == nil {
		t.Fatal("пустой ключ принят")
	}
}

func TestIDIsStableShortAndDistinct(t *testing.T) {
	a, b := []byte("ключ-а"), []byte("ключ-б")
	if keys.ID(a) != keys.ID(a) || keys.ID(a) == keys.ID(b) || len(keys.ID(a)) != 11 {
		t.Fatalf("ID: %q %q", keys.ID(a), keys.ID(b))
	}
}

func TestGenerate(t *testing.T) {
	s, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := keys.Decode(s)
	if err != nil || len(b) != 32 {
		t.Fatalf("Generate: %v, %d байт", err, len(b))
	}
}
```

- [ ] **Step 2: Падающие тесты access-токенов**

`backend/internal/platform/token/token_test.go`:

```go
package token_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/token"
)

func seed(t *testing.T) string {
	t.Helper()
	s, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func issuer(t *testing.T, c *clocktest.Fake, seeds ...string) *token.Issuer {
	t.Helper()
	k, err := token.ParseKeys(seeds)
	if err != nil {
		t.Fatal(err)
	}
	return token.NewIssuer(k, c)
}

var start = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func TestAccessRoundTrip(t *testing.T) {
	c := clocktest.New(start)
	iss := issuer(t, c, seed(t))
	uid, sid := uuid.New(), uuid.New()
	raw, exp, err := iss.Access(uid, sid)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(start.Add(token.AccessTTL)) {
		t.Fatalf("срок = %v", exp)
	}
	got, err := iss.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != uid || got.SessionID != sid || !got.ExpiresAt.Equal(exp) || !got.IssuedAt.Equal(start) {
		t.Fatalf("claims: %+v", got)
	}
}

// Ротация: токен старого ключа проверяется, пока старый сид в списке; подписывает новый.
func TestRotation(t *testing.T) {
	c := clocktest.New(start)
	oldSeed, newSeed := seed(t), seed(t)
	old := issuer(t, c, oldSeed)
	raw, _, err := old.Access(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	rotated := issuer(t, c, newSeed, oldSeed)
	if _, err := rotated.Verify(raw); err != nil {
		t.Fatalf("токен старого ключа отвергнут при ротации: %v", err)
	}
	fresh, _, _ := rotated.Access(uuid.New(), uuid.New())
	if _, err := old.Verify(fresh); err == nil {
		t.Fatal("подписал не первый ключ списка")
	}
	if _, err := issuer(t, c, newSeed).Verify(raw); !errors.Is(err, token.ErrInvalid) {
		t.Fatalf("ключ выведен из ротации, а токен принят: %v", err)
	}
}

// Review Focus 4: ни одна подделка не проходит.
func TestVerifyRejects(t *testing.T) {
	c := clocktest.New(start)
	s := seed(t)
	iss := issuer(t, c, s)
	good, _, _ := iss.Access(uuid.New(), uuid.New())
	sd, _ := keys.Decode(s)
	priv := ed25519.NewKeyFromSeed(sd)
	kid := keys.ID(priv.Public().(ed25519.PublicKey))

	sign := func(method jwt.SigningMethod, key any, claims jwt.MapClaims, kidHeader string) string {
		t.Helper()
		tk := jwt.NewWithClaims(method, claims)
		if kidHeader != "" {
			tk.Header["kid"] = kidHeader
		}
		raw, err := tk.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	valid := func() jwt.MapClaims {
		return jwt.MapClaims{"sub": uuid.NewString(), "sid": uuid.NewString(), "aud": "public",
			"iat": start.Unix(), "exp": start.Add(time.Minute).Unix()}
	}
	b64 := base64.RawURLEncoding.EncodeToString
	parts := strings.Split(good, ".")

	cases := map[string]string{
		"alg=none": b64([]byte(`{"alg":"none","kid":"` + kid + `"}`)) + "." + parts[1] + ".",
		// HS256 с открытым ключом как секретом — классическая подмена алгоритма
		"HS256 на открытом ключе": sign(jwt.SigningMethodHS256, []byte(priv.Public().(ed25519.PublicKey)), valid(), kid),
		"чужой aud": sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims {
			m := valid()
			m["aud"] = "admin"
			return m
		}(), kid),
		"неизвестный kid":      sign(jwt.SigningMethodEdDSA, priv, valid(), "unknownkid0"),
		"без kid":              sign(jwt.SigningMethodEdDSA, priv, valid(), ""),
		"без exp":              sign(jwt.SigningMethodEdDSA, priv, jwt.MapClaims{"sub": uuid.NewString(), "sid": uuid.NewString(), "aud": "public", "iat": start.Unix()}, kid),
		"sub не UUID":          sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims { m := valid(); m["sub"] = "42"; return m }(), kid),
		"без sid":              sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims { m := valid(); delete(m, "sid"); return m }(), kid),
		"iat в будущем":        sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims { m := valid(); m["iat"] = start.Add(time.Hour).Unix(); return m }(), kid),
		"подменённый payload":  parts[0] + "." + b64([]byte(`{"sub":"`+uuid.NewString()+`","sid":"`+uuid.NewString()+`","aud":"public","exp":9999999999}`)) + "." + parts[2],
		"мусор":                "abc",
		"пусто":                "",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := iss.Verify(raw); !errors.Is(err, token.ErrInvalid) {
				t.Fatalf("принят или не ErrInvalid: %v", err)
			}
		})
	}

	t.Run("просрочен", func(t *testing.T) {
		c.Set(start.Add(token.AccessTTL + time.Minute))
		defer c.Set(start)
		if _, err := iss.Verify(good); !errors.Is(err, token.ErrInvalid) {
			t.Fatalf("просроченный принят: %v", err)
		}
	})
}

func TestParseKeys(t *testing.T) {
	if _, err := token.ParseKeys(nil); err == nil {
		t.Fatal("пустой список принят")
	}
	if _, err := token.ParseKeys([]string{base64.StdEncoding.EncodeToString([]byte("short"))}); err == nil {
		t.Fatal("сид не 32 байта принят")
	}
	s := seed(t)
	if _, err := token.ParseKeys([]string{s, s}); err == nil {
		t.Fatal("повтор ключа в списке принят")
	}
}
```

- [ ] **Step 3: Падающие тесты refresh**

`backend/internal/platform/token/refresh_test.go`:

```go
package token_test

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"wf/backend/internal/platform/token"
)

func TestNewRefresh(t *testing.T) {
	raw, hash, err := token.NewRefresh()
	if err != nil {
		t.Fatal(err)
	}
	// 256 бит в base64url без паддинга — 43 символа
	if len(raw) != 43 {
		t.Fatalf("длина = %d", len(raw))
	}
	sum := sha256.Sum256([]byte(raw))
	if !bytes.Equal(hash, sum[:]) || !bytes.Equal(token.HashRefresh(raw), hash) {
		t.Fatal("хэш не SHA-256 токена")
	}
	other, _, _ := token.NewRefresh()
	if other == raw {
		t.Fatal("два одинаковых токена")
	}
}

// Окно гонки (§6.2): преемника открывает только предъявивший тот же токен.
func TestSealSuccessor(t *testing.T) {
	presented, _, _ := token.NewRefresh()
	successor, _, _ := token.NewRefresh()
	sealed, err := token.SealSuccessor(presented, successor)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(successor)) {
		t.Fatal("преемник лежит открытым")
	}
	got, err := token.OpenSuccessor(presented, sealed)
	if err != nil || got != successor {
		t.Fatalf("открыт %q, %v", got, err)
	}
	stranger, _, _ := token.NewRefresh()
	if _, err := token.OpenSuccessor(stranger, sealed); err == nil {
		t.Fatal("чужой токен открыл преемника")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := token.OpenSuccessor(presented, sealed); err == nil {
		t.Fatal("испорченный шифротекст открылся")
	}
	if _, err := token.OpenSuccessor(presented, []byte{1, 2}); err == nil {
		t.Fatal("обрезок открылся")
	}
}
```

- [ ] **Step 4: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/keys/... ./internal/platform/token/... -count=1`
Expected: FAIL — пакетов нет.

- [ ] **Step 5: Реализация ключей**

`backend/internal/platform/keys/keys.go`:

```go
// Package keys — секреты из окружения (спека §6.9: ключи списком для ротации): декодирование
// base64 и имя ключа (kid). Имя выводится из материала — в окружении только сами ключи.
package keys

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

var ErrBadKey = errors.New("keys: ключ не в base64")

// Decode — base64 со стандартным или URL-алфавитом, с паддингом или без: так ключи выдают
// и `openssl rand -base64 32`, и generateValue Render.
func Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrBadKey
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, ErrBadKey
}

// ID — имя ключа: первые 8 байт SHA-256 материала в base64url (11 символов). По имени
// находится ключ проверки при ротации; сам ключ из имени не восстановить.
func ID(material []byte) string {
	sum := sha256.Sum256(material)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// Generate — новый ключ: 32 байта из CSPRNG в base64.
func Generate() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
```

- [ ] **Step 6: Реализация access-токенов**

`backend/internal/platform/token/token.go`:

```go
// Package token — access-токены публичного API и примитивы refresh (спека бэкенда §6.2).
// Access — JWT EdDSA (Ed25519) на 10 минут: sub — пользователь, sid — сессия, aud=public.
// Алгоритм зафиксирован на сервере, ключ проверки выбирается по kid из своего набора.
// Сессию на каждый запрос проверяет auth — токен сам по себе не доказывает, что она жива.
package token

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/keys"
)

const (
	AccessTTL = 10 * time.Minute
	audience  = "public"
	// leeway — расхождение часов инстансов API: iat чуть в будущем не повод для 401
	leeway = 5 * time.Second
)

// ErrInvalid — токен не принят: подпись, алгоритм, aud, срок, kid, claims.
var ErrInvalid = errors.New("token: недействительный access-токен")

// Keys — ключи подписи: первый подписывает, все проверяют (ротация списком).
type Keys struct {
	signKID string
	sign    ed25519.PrivateKey
	verify  map[string]ed25519.PublicKey
}

// ParseKeys — сиды Ed25519 (32 байта, base64) из окружения; kid — keys.ID открытого ключа.
func ParseKeys(seeds []string) (*Keys, error) {
	if len(seeds) == 0 {
		return nil, errors.New("token: нет ни одного ключа подписи")
	}
	k := &Keys{verify: make(map[string]ed25519.PublicKey, len(seeds))}
	for i, s := range seeds {
		seed, err := keys.Decode(s)
		if err != nil {
			return nil, fmt.Errorf("token: ключ №%d: %w", i+1, err)
		}
		if len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("token: ключ №%d — %d байт, нужен сид Ed25519 из %d", i+1, len(seed), ed25519.SeedSize)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		pub := priv.Public().(ed25519.PublicKey)
		kid := keys.ID(pub)
		if _, dup := k.verify[kid]; dup {
			return nil, fmt.Errorf("token: ключ №%d повторяется", i+1)
		}
		k.verify[kid] = pub
		if i == 0 {
			k.signKID, k.sign = kid, priv
		}
	}
	return k, nil
}

type Claims struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type accessClaims struct {
	jwt.RegisteredClaims
	SID string `json:"sid"`
}

type Issuer struct {
	keys   *Keys
	clock  clock.Clock
	parser *jwt.Parser
}

func NewIssuer(k *Keys, c clock.Clock) *Issuer {
	return &Issuer{keys: k, clock: c, parser: jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(leeway),
		jwt.WithTimeFunc(c.Now),
	)}
}

// Access — access-токен сессии и момент его истечения.
func (i *Issuer) Access(userID, sessionID uuid.UUID) (string, time.Time, error) {
	now := i.clock.Now().Truncate(time.Second) // NumericDate — секунды
	exp := now.Add(AccessTTL)
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		SID: sessionID.String(),
	})
	t.Header["kid"] = i.keys.signKID
	raw, err := t.SignedString(i.keys.sign)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token: подпись: %w", err)
	}
	return raw, exp, nil
}

// Verify — проверка подписи, алгоритма, aud, срока и claims. Любой отказ — ErrInvalid.
func (i *Issuer) Verify(raw string) (Claims, error) {
	var c accessClaims
	_, err := i.parser.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if pub, ok := i.keys.verify[kid]; ok {
			return pub, nil
		}
		return nil, errors.New("неизвестный kid")
	})
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	uid, err := uuid.Parse(c.Subject)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: sub", ErrInvalid)
	}
	sid, err := uuid.Parse(c.SID)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: sid", ErrInvalid)
	}
	if c.IssuedAt == nil {
		return Claims{}, fmt.Errorf("%w: iat", ErrInvalid)
	}
	return Claims{UserID: uid, SessionID: sid, IssuedAt: c.IssuedAt.Time.UTC(), ExpiresAt: c.ExpiresAt.Time.UTC()}, nil
}
```

Если тест «просрочен» проходит (токен принят) из-за `leeway`, проверить: срок `start+10m`, часы `start+11m` — за пределами 5 с. Тест `TestAccessRoundTrip` сравнивает `IssuedAt` с `start` — `start` без долей секунды.

- [ ] **Step 7: Реализация refresh**

`backend/internal/platform/token/refresh.go`:

```go
package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// refreshBytes — 256 бит из CSPRNG (спека §6.2).
const refreshBytes = 32

// successorInfo — контекст HKDF: ключ окна гонки не совпадёт ни с каким другим ключом из
// того же токена.
const successorInfo = "wf refresh successor v1"

var ErrSealed = errors.New("token: преемник не открывается")

// NewRefresh — refresh-токен (base64url без паддинга) и его SHA-256 — в sessions хранится хэш.
func NewRefresh() (string, []byte, error) {
	b := make([]byte, refreshBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, HashRefresh(raw), nil
}

func HashRefresh(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// SealSuccessor — преемник для окна гонки 15 с (§6.2), зашифрованный AES-256-GCM ключом из
// HKDF-SHA256 от предъявленного refresh: открыть его может только тот, у кого есть
// предъявленный токен; в базе преемник открытым не лежит. Формат: nonce || шифротекст.
func SealSuccessor(presented, successor string) ([]byte, error) {
	aead, err := successorAEAD(presented)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(successor), nil), nil
}

func OpenSuccessor(presented string, sealed []byte) (string, error) {
	aead, err := successorAEAD(presented)
	if err != nil {
		return "", err
	}
	if len(sealed) < aead.NonceSize() {
		return "", ErrSealed
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], nil)
	if err != nil {
		return "", ErrSealed
	}
	return string(plain), nil
}

func successorAEAD(presented string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, []byte(presented), nil, successorInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("token: hkdf: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
```

- [ ] **Step 8: Тесты проходят**

Из `backend/`: `go test ./internal/platform/keys/... ./internal/platform/token/... -count=1`
Expected: PASS.

Подсадка бага: убрать `jwt.WithValidMethods(…)` из `NewIssuer` → падает «HS256 на открытом ключе» (или «alg=none»); убрать `jwt.WithAudience` → падает «чужой aud»; вернуть.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/platform/keys backend/internal/platform/token
git commit -m "Платформа: ключи из окружения с kid, access-токены EdDSA, refresh и преемник окна гонки" -- backend/internal/platform/keys backend/internal/platform/token
```

---

### Task 9: Principal, загрузка сессии с кэшем, слой аутентификации

**Files:**
- Create: `backend/internal/platform/auth/principal.go`, `backend/internal/platform/auth/loader.go`, `backend/internal/platform/auth/middleware.go`
- Create: `backend/internal/platform/auth/principal_test.go`, `backend/internal/platform/auth/loader_test.go`, `backend/internal/platform/auth/middleware_test.go`

**Interfaces:**
- Consumes: `token.Claims`, `token.ErrInvalid` (Task 8); `clock.Clock`, `clocktest` (Task 2); `httpx.WriteProblem`, `httpx.WriteError`, `httpx.CodeUnauthenticated` (Task 5).
- Produces:
  - `auth.Role{Name, ScopeType string; ScopeID uuid.UUID}`; `auth.Principal{UserID, SessionID, DeviceID uuid.UUID; Status string; Restrictions []string; Roles []Role}` с методами `HasRole(name, scopeType string, scopeID uuid.UUID) bool`, `Restricted(capability string) bool`.
  - `auth.With(ctx, *Principal) context.Context`; `auth.From(ctx) (*Principal, bool)`; `auth.Authenticated(ctx) bool` — для `httpx.ValidateOptions.Authenticated`.
  - `auth.SessionLoader interface { LoadSession(ctx, sessionID uuid.UUID) (*Principal, error) }`; `auth.SessionLoaderFunc`; `auth.ErrSessionInvalid`; `auth.NoSessions SessionLoader` (любая сессия недействительна — до спеки identity).
  - `auth.NewCachedLoader(next SessionLoader, ttl time.Duration, c clock.Clock, max int) SessionLoader` — успех кэшируется не дольше `ttl`, промахи по одной сессии схлопываются, ошибки не кэшируются.
  - `auth.Verifier interface { Verify(raw string) (token.Claims, error) }` (его реализует `*token.Issuer`); `auth.Middleware(v Verifier, l SessionLoader, log *slog.Logger) func(http.Handler) http.Handler`.

Principal не меняется после загрузки: кэш отдаёт один и тот же указатель всем запросам сессии.

- [ ] **Step 1: Падающие тесты Principal**

`backend/internal/platform/auth/principal_test.go`:

```go
package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
)

func TestPrincipalRolesAndRestrictions(t *testing.T) {
	moscow := uuid.New()
	p := &auth.Principal{
		Roles: []auth.Role{
			{Name: "admin", ScopeType: "global"},
			{Name: "captain", ScopeType: "city", ScopeID: moscow},
		},
		Restrictions: []string{"comment"},
	}
	if !p.HasRole("admin", "global", uuid.Nil) || !p.HasRole("captain", "city", moscow) {
		t.Fatal("выданные роли не видны")
	}
	// скоуп точный: капитан в Москве — не капитан в другом городе; глобальная роль не
	// подразумевает городскую
	if p.HasRole("captain", "city", uuid.New()) || p.HasRole("city_moderator", "city", moscow) {
		t.Fatal("роль вне скоупа")
	}
	if !p.Restricted("comment") || p.Restricted("create_match") {
		t.Fatal("ограничения")
	}
}

func TestContext(t *testing.T) {
	ctx := context.Background()
	if _, ok := auth.From(ctx); ok || auth.Authenticated(ctx) {
		t.Fatal("Principal в пустом ctx")
	}
	p := &auth.Principal{UserID: uuid.New()}
	ctx = auth.With(ctx, p)
	if got, ok := auth.From(ctx); !ok || got != p || !auth.Authenticated(ctx) {
		t.Fatal("Principal не найден")
	}
}
```

- [ ] **Step 2: Падающие тесты загрузчика с кэшем**

`backend/internal/platform/auth/loader_test.go`:

```go
package auth_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/testkit/clocktest"
)

// counting — загрузчик-заглушка: считает обращения, отвечает тем, что задал тест.
type counting struct {
	calls atomic.Int32
	mu    sync.Mutex
	err   error
	gate  chan struct{} // не nil — загрузка ждёт закрытия
}

func (c *counting) LoadSession(_ context.Context, sid uuid.UUID) (*auth.Principal, error) {
	c.calls.Add(1)
	if c.gate != nil {
		<-c.gate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	return &auth.Principal{UserID: uuid.New(), SessionID: sid}, nil
}

// Review Focus 3: отозванная сессия перестаёт действовать не позже ttl.
func TestCachedLoaderTTL(t *testing.T) {
	c := clocktest.New(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	next := &counting{}
	l := auth.NewCachedLoader(next, 5*time.Second, c, 100)
	sid := uuid.New()
	ctx := context.Background()

	first, err := l.LoadSession(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(4 * time.Second)
	if again, _ := l.LoadSession(ctx, sid); again != first || next.calls.Load() != 1 {
		t.Fatalf("в пределах ttl — не из кэша: обращений %d", next.calls.Load())
	}

	// сессию отозвали: через ttl загрузчик спрашивается снова и отвечает отказом
	next.mu.Lock()
	next.err = auth.ErrSessionInvalid
	next.mu.Unlock()
	c.Advance(time.Second + time.Millisecond)
	if _, err := l.LoadSession(ctx, sid); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("после ttl отозванная сессия жива: %v", err)
	}
	// отказ не кэшируется: следующее обращение снова идёт в загрузчик
	_, _ = l.LoadSession(ctx, sid)
	if next.calls.Load() != 3 {
		t.Fatalf("обращений %d, нужно 3", next.calls.Load())
	}
}

// Десять параллельных запросов одной сессии — одно обращение к базе.
func TestCachedLoaderSingleflight(t *testing.T) {
	c := clocktest.New(time.Now())
	next := &counting{gate: make(chan struct{})}
	l := auth.NewCachedLoader(next, 5*time.Second, c, 100)
	sid := uuid.New()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := l.LoadSession(context.Background(), sid); err != nil {
				t.Error(err)
			}
		})
	}
	time.Sleep(50 * time.Millisecond) // все десять встали в ожидание
	close(next.gate)
	wg.Wait()
	if n := next.calls.Load(); n != 1 {
		t.Fatalf("обращений %d, нужно 1", n)
	}
}

// Переполнение: кэш не растёт без предела.
func TestCachedLoaderBounded(t *testing.T) {
	c := clocktest.New(time.Now())
	next := &counting{}
	l := auth.NewCachedLoader(next, time.Minute, c, 3)
	ctx := context.Background()
	for range 10 {
		if _, err := l.LoadSession(ctx, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	if n := auth.CacheLen(l); n > 3 {
		t.Fatalf("в кэше %d записей при пределе 3", n)
	}
}

func TestNoSessions(t *testing.T) {
	if _, err := auth.NoSessions.LoadSession(context.Background(), uuid.New()); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("NoSessions: %v", err)
	}
}
```

`backend/internal/platform/auth/export_test.go`:

```go
package auth

// CacheLen — число записей кэша (только тесты).
func CacheLen(l SessionLoader) int {
	c := l.(*cachedLoader)
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
```

- [ ] **Step 3: Падающие тесты слоя аутентификации**

`backend/internal/platform/auth/middleware_test.go`:

```go
package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/token"
)

type fakeVerifier map[string]token.Claims

func (f fakeVerifier) Verify(raw string) (token.Claims, error) {
	if c, ok := f[raw]; ok {
		return c, nil
	}
	return token.Claims{}, token.ErrInvalid
}

func TestMiddleware(t *testing.T) {
	uid, sid, otherUser := uuid.New(), uuid.New(), uuid.New()
	v := fakeVerifier{
		"good":     {UserID: uid, SessionID: sid},
		"revoked":  {UserID: uid, SessionID: uuid.New()},
		"mismatch": {UserID: otherUser, SessionID: sid},
		"broken":   {UserID: uid, SessionID: uuid.Nil},
	}
	loader := auth.SessionLoaderFunc(func(_ context.Context, s uuid.UUID) (*auth.Principal, error) {
		switch s {
		case sid:
			return &auth.Principal{UserID: uid, SessionID: sid, Status: "active"}, nil
		case uuid.Nil:
			return nil, errors.New("база недоступна")
		}
		return nil, auth.ErrSessionInvalid
	})
	cases := []struct {
		name    string
		header  string
		status  int
		code    string
		withWho bool
	}{
		{"без Authorization — дальше без Principal", "", 204, "", false},
		{"действующий токен", "Bearer good", 204, "", true},
		{"схема в другом регистре", "bearer good", 204, "", true},
		{"не Bearer", "Basic Zm9vOmJhcg==", 401, httpx.CodeUnauthenticated, false},
		{"Bearer без токена", "Bearer ", 401, httpx.CodeUnauthenticated, false},
		{"подделка", "Bearer forged", 401, httpx.CodeUnauthenticated, false},
		{"сессия отозвана", "Bearer revoked", 401, httpx.CodeUnauthenticated, false},
		{"sub не совпал с владельцем сессии", "Bearer mismatch", 401, httpx.CodeUnauthenticated, false},
		{"сбой загрузки — 500, не 401", "Bearer broken", 500, httpx.CodeInternal, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			var got *auth.Principal
			h := auth.Middleware(v, loader, slog.New(slog.NewTextHandler(&logs, nil)))(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got, _ = auth.From(r.Context())
					w.WriteHeader(http.StatusNoContent)
				}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.status {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if c.code != "" {
				var p httpx.Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != c.code {
					t.Fatalf("тело: %s", rec.Body.String())
				}
			}
			if c.status == 401 && rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatal("нет WWW-Authenticate: Bearer")
			}
			if c.withWho != (got != nil) || (got != nil && got.UserID != uid) {
				t.Fatalf("Principal: %+v", got)
			}
		})
	}
}
```

- [ ] **Step 4: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/auth/... -count=1`
Expected: FAIL — пакета нет.

- [ ] **Step 5: Реализация**

`backend/internal/platform/auth/principal.go`:

```go
// Package auth — кто делает запрос (спека бэкенда §6.2, §6.3): Principal из сессии в ctx,
// загрузка сессии с кэшем, слой аутентификации публичного API. Права на ресурс проверяет
// app/ модуля по Principal, а не хендлер.
package auth

import (
	"context"
	"slices"

	"github.com/google/uuid"
)

// Role — глобальная или территориальная роль из role_assignments (§4.5).
type Role struct {
	Name      string    // admin, city_moderator, captain
	ScopeType string    // global, country, city
	ScopeID   uuid.UUID // uuid.Nil у global
}

// Principal — пользователь запроса. Загружается одним запросом по sid (identity) и не
// меняется: кэш отдаёт один указатель всем запросам сессии.
type Principal struct {
	UserID       uuid.UUID
	SessionID    uuid.UUID
	DeviceID     uuid.UUID // uuid.Nil — сессия без устройства
	Status       string    // user_status: active, suspended
	Restrictions []string  // закрытые санкцией возможности: create_match, comment, captain…
	Roles        []Role
}

// HasRole — роль с точным скоупом: глобальная роль не подразумевает городскую.
func (p *Principal) HasRole(name, scopeType string, scopeID uuid.UUID) bool {
	return slices.Contains(p.Roles, Role{Name: name, ScopeType: scopeType, ScopeID: scopeID})
}

// Restricted — возможность закрыта санкцией (403 identity.restricted решает модуль).
func (p *Principal) Restricted(capability string) bool {
	return slices.Contains(p.Restrictions, capability)
}

type principalKey struct{}

func With(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func From(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok && p != nil
}

// Authenticated — для httpx.ValidateOptions: вход нужен операциям с security контракта.
func Authenticated(ctx context.Context) bool {
	_, ok := From(ctx)
	return ok
}
```

`backend/internal/platform/auth/loader.go`:

```go
package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"wf/backend/internal/platform/clock"
)

// ErrSessionInvalid — сессии нет, она отозвана или истекла, аккаунт забанен или удалён:
// клиенту — 401 (решает загрузчик identity).
var ErrSessionInvalid = errors.New("auth: сессия недействительна")

// SessionLoader — Principal по id сессии одним запросом (статус, revoked_at, ограничения,
// роли). Реализует identity: таблица sessions — его.
type SessionLoader interface {
	LoadSession(ctx context.Context, sessionID uuid.UUID) (*Principal, error)
}

type SessionLoaderFunc func(ctx context.Context, sessionID uuid.UUID) (*Principal, error)

func (f SessionLoaderFunc) LoadSession(ctx context.Context, id uuid.UUID) (*Principal, error) {
	return f(ctx, id)
}

// NoSessions — до спеки identity: сессий нет, любой токен — 401.
var NoSessions SessionLoader = SessionLoaderFunc(func(context.Context, uuid.UUID) (*Principal, error) {
	return nil, ErrSessionInvalid
})

type entry struct {
	p       *Principal
	expires time.Time
}

type cachedLoader struct {
	next  SessionLoader
	ttl   time.Duration
	clock clock.Clock
	max   int
	group singleflight.Group

	mu      sync.Mutex
	entries map[uuid.UUID]entry
}

// NewCachedLoader — кэш в процессе не дольше ttl (§6.2: 5 с): бан, «выйти везде» и смена
// пароля действуют в пределах ttl. Кэшируется только успех; одновременные промахи одной
// сессии — одно обращение к next. Больше max записей — просроченные выбрасываются, а если
// и это не помогло — кэш очищается целиком (дешевле, чем LRU, и не растёт без предела).
func NewCachedLoader(next SessionLoader, ttl time.Duration, c clock.Clock, max int) SessionLoader {
	return &cachedLoader{next: next, ttl: ttl, clock: c, max: max, entries: make(map[uuid.UUID]entry)}
}

func (l *cachedLoader) LoadSession(ctx context.Context, sid uuid.UUID) (*Principal, error) {
	now := l.clock.Now()
	l.mu.Lock()
	e, ok := l.entries[sid]
	l.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.p, nil
	}
	v, err, _ := l.group.Do(sid.String(), func() (any, error) {
		// загрузка не должна оборваться из-за отмены одного из ждущих запросов
		p, err := l.next.LoadSession(context.WithoutCancel(ctx), sid)
		if err != nil {
			return nil, err
		}
		l.put(sid, p, l.clock.Now())
		return p, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Principal), nil
}

func (l *cachedLoader) put(sid uuid.UUID, p *Principal, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) >= l.max {
		for k, e := range l.entries {
			if !now.Before(e.expires) {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= l.max {
			clear(l.entries)
		}
	}
	l.entries[sid] = entry{p: p, expires: now.Add(l.ttl)}
}
```

`backend/internal/platform/auth/middleware.go`:

```go
package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/token"
)

// Verifier — проверка access-токена; реализует *token.Issuer.
type Verifier interface {
	Verify(raw string) (token.Claims, error)
}

// Middleware — необязательная аутентификация (спека §6.1): нет Authorization — запрос идёт
// дальше без Principal (нужен ли вход, решает security контракта в валидаторе). Заголовок
// есть, но токен битый, просрочен или сессия недействительна — 401 сразу. Сбой загрузки
// сессии — 500: недоступная база — не повод выкидывать пользователя из приложения.
func Middleware(v Verifier, l SessionLoader, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if h == "" {
				next.ServeHTTP(w, r)
				return
			}
			scheme, raw, ok := strings.Cut(h, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(raw) == "" {
				unauthorized(w, r)
				return
			}
			claims, err := v.Verify(strings.TrimSpace(raw))
			if err != nil {
				unauthorized(w, r)
				return
			}
			p, err := l.LoadSession(r.Context(), claims.SessionID)
			switch {
			case errors.Is(err, ErrSessionInvalid):
				unauthorized(w, r)
				return
			case err != nil:
				httpx.WriteError(log, w, r, err)
				return
			case p.UserID != claims.UserID || p.SessionID != claims.SessionID:
				unauthorized(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(With(r.Context(), p)))
		})
	}
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthenticated, "")
}
```

- [ ] **Step 6: Тесты проходят**

Из `backend/`: `go test ./internal/platform/auth/... ./internal/platform/httpx/... -count=1`
Expected: PASS (страж литералов видит пакет `auth` — кодов строкой там нет).

Подсадка бага: в `cachedLoader.LoadSession` заменить `now.Before(e.expires)` на `true` → `TestCachedLoaderTTL` падает («после ttl отозванная сессия жива»); вернуть.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/platform/auth
git commit -m "Платформа: Principal из сессии, кэш сессий на 5 с со схлопыванием, слой аутентификации Bearer" -- backend/internal/platform/auth
```

---

## Фаза C. Защита края

### Task 10: Rate limit

**Files:**
- Create: `backend/internal/platform/ratelimit/limiter.go`, `rules.go`, `middleware.go`, `cleanup.go`
- Create: `backend/internal/platform/ratelimit/limiter_test.go`, `rules_test.go`, `middleware_test.go`, `cleanup_test.go`

**Interfaces:**
- Consumes: `ratelimitdb` (Task 3), `dbtest.NewPoolsAs` (Task 3), `clock`, `clocktest` (Task 2), `httpx.RouteFrom`, `httpx.Routes` (Task 6), `httpx.WriteProblemValue`, `httpx.CodeRateLimited` (Task 5), `peer.From`/`peer.With` (Task 7), `auth.From`/`auth.With` (Task 9), `queue.Maintenance` (2/3).
- Produces:
  - `ratelimit.Policy{Limit int; Period time.Duration; Burst int}` (Burst 0 — равен Limit; нулевая `Limit` — ключ не ограничивается); `(Policy).Validate() error`.
  - `ratelimit.Result{Allowed bool; RetryAfter time.Duration}`.
  - `ratelimit.Limiter interface { Allow(ctx, key string, p Policy) (Result, error); Add(ctx, key string, p Policy) error; Over(ctx, key string, p Policy) (bool, error) }` — за интерфейсом (переезд на Valkey — замена реализации, §6.6).
  - `ratelimit.NewPG(db ratelimitdb.DBTX, c clock.Clock) *ratelimit.PG` (реализует `Limiter`); `ratelimit.Unlimited` — `Limiter`, который всё пропускает (тесты хендлеров).
  - `ratelimit.ClassPolicy{IP, User, Device Policy}`; `ratelimit.Rules map[string]ClassPolicy`; `ratelimit.DefaultRules() Rules`; `ratelimit.ParseRules(s string, base Rules) (Rules, error)`; `ratelimit.UnknownClasses(spec *openapi3.T, r Rules) []string`.
  - `ratelimit.Middleware(l Limiter, r Rules, log *slog.Logger) func(http.Handler) http.Handler`.
  - `ratelimit.CleanupArgs`, `ratelimit.NewCleanupWorker(db ratelimitdb.DBTX, c clock.Clock) *ratelimit.CleanupWorker`, `ratelimit.CleanupJob() *river.PeriodicJob` (каждые 10 минут).

GCRA: интервал `T = Period/Limit`, окно `T·Burst`. Запрос проходит, если `max(tat, now) + T − now ≤ T·Burst`; тогда `tat` сдвигается. `Over` — следующий `Allow` не пройдёт: `tat − now > T·(Burst−1)`.

- [ ] **Step 1: Падающие тесты лимитера (под ролью api)**

`backend/internal/platform/ratelimit/limiter_test.go`:

```go
package ratelimit_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

var start = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func limiter(t *testing.T) (*ratelimit.PG, *clocktest.Fake, dbtest.Pools) {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api") // права прода: api считает лимиты
	c := clocktest.New(start)
	return ratelimit.NewPG(pools.As, c), c, pools
}

func TestAllowBurstThenRate(t *testing.T) {
	l, c, _ := limiter(t)
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 60, Period: time.Minute, Burst: 3} // 1 в секунду, всплеск 3
	for i := range 3 {
		if r, err := l.Allow(ctx, "k", p); err != nil || !r.Allowed {
			t.Fatalf("запрос %d всплеска: %+v %v", i+1, r, err)
		}
	}
	r, err := l.Allow(ctx, "k", p)
	if err != nil || r.Allowed {
		t.Fatalf("четвёртый подряд прошёл: %+v %v", r, err)
	}
	if r.RetryAfter <= 0 || r.RetryAfter > time.Second {
		t.Fatalf("RetryAfter = %v, нужно (0, 1с]", r.RetryAfter)
	}
	c.Advance(time.Second)
	if r, _ := l.Allow(ctx, "k", p); !r.Allowed {
		t.Fatal("через интервал не прошёл")
	}
	// другой ключ — свой счёт
	if r, _ := l.Allow(ctx, "other", p); !r.Allowed {
		t.Fatal("чужой ключ ограничен")
	}
}

// Отказ не сдвигает tat: долбить в закрытую дверь не продлевает блокировку.
func TestDeniedDoesNotExtend(t *testing.T) {
	l, c, _ := limiter(t)
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 1, Period: 10 * time.Second}
	_, _ = l.Allow(ctx, "k", p)
	for range 5 {
		_, _ = l.Allow(ctx, "k", p)
	}
	c.Advance(10 * time.Second)
	if r, _ := l.Allow(ctx, "k", p); !r.Allowed {
		t.Fatal("отказы продлили блокировку")
	}
}

// Гонка: 20 параллельных запросов при всплеске 5 — проходят ровно 5.
func TestAllowConcurrent(t *testing.T) {
	l, _, _ := limiter(t)
	p := ratelimit.Policy{Limit: 5, Period: time.Hour}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			r, err := l.Allow(context.Background(), "race", p)
			if err != nil {
				t.Error(err)
			}
			if r.Allowed {
				ok.Add(1)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 5 {
		t.Fatalf("прошло %d, нужно 5", ok.Load())
	}
}

// Счётчик сигнала риска: Add расходует всегда, Over — порог без расхода.
func TestAddAndOver(t *testing.T) {
	l, c, _ := limiter(t)
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 5, Period: 15 * time.Minute}
	for i := range 5 {
		if over, _ := l.Over(ctx, "login:a", p); over {
			t.Fatalf("порог превышен после %d неудач", i)
		}
		if err := l.Add(ctx, "login:a", p); err != nil {
			t.Fatal(err)
		}
	}
	if over, _ := l.Over(ctx, "login:a", p); !over {
		t.Fatal("после 5 неудач порог не превышен")
	}
	c.Advance(3 * time.Minute) // одна неудача «выветрилась»
	if over, _ := l.Over(ctx, "login:a", p); over {
		t.Fatal("порог не отпускает со временем")
	}
	if over, _ := l.Over(ctx, "never", p); over {
		t.Fatal("ключ без истории превышен")
	}
}

func TestPolicyValidate(t *testing.T) {
	for _, p := range []ratelimit.Policy{{Limit: -1, Period: time.Second}, {Limit: 1}, {Limit: 1, Period: time.Second, Burst: -1}} {
		if p.Validate() == nil {
			t.Fatalf("принята %+v", p)
		}
	}
	if (ratelimit.Policy{}).Validate() != nil {
		t.Fatal("нулевая политика (без лимита) отвергнута")
	}
}
```

- [ ] **Step 2: Падающие тесты правил**

`backend/internal/platform/ratelimit/rules_test.go`:

```go
package ratelimit_test

import (
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/ratelimit"
)

func TestDefaultRulesValid(t *testing.T) {
	r := ratelimit.DefaultRules()
	for _, class := range []string{"default", "auth"} {
		cp, ok := r[class]
		if !ok {
			t.Fatalf("нет класса %s", class)
		}
		for _, p := range []ratelimit.Policy{cp.IP, cp.User, cp.Device} {
			if err := p.Validate(); err != nil {
				t.Fatalf("%s: %v", class, err)
			}
		}
	}
	if r["default"].IP.Limit == 0 {
		t.Fatal("default без лимита по IP")
	}
}

func TestParseRules(t *testing.T) {
	got, err := ratelimit.ParseRules("auth.ip=10/1m:5, default.device=off, upload.user=30/1h", ratelimit.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	if got["auth"].IP != (ratelimit.Policy{Limit: 10, Period: time.Minute, Burst: 5}) {
		t.Fatalf("auth.ip = %+v", got["auth"].IP)
	}
	if got["default"].Device != (ratelimit.Policy{}) {
		t.Fatalf("off не выключил: %+v", got["default"].Device)
	}
	if got["upload"].User != (ratelimit.Policy{Limit: 30, Period: time.Hour}) {
		t.Fatalf("новый класс: %+v", got["upload"])
	}
	if got["default"].IP != ratelimit.DefaultRules()["default"].IP {
		t.Fatal("незатронутое правило изменилось")
	}
	// база не меняется
	if ratelimit.DefaultRules()["auth"].IP.Limit == 10 {
		t.Fatal("ParseRules изменил базу")
	}
	if got, _ := ratelimit.ParseRules("", ratelimit.DefaultRules()); len(got) != len(ratelimit.DefaultRules()) {
		t.Fatal("пустая строка — должны остаться умолчания")
	}
	for _, bad := range []string{"auth.ip", "auth.cookie=1/1m", "auth.ip=x/1m", "auth.ip=1/xx", "auth.ip=0/1m", "Auth.ip=1/1m", "auth.ip=1/1m:-2"} {
		if _, err := ratelimit.ParseRules(bad, ratelimit.DefaultRules()); err == nil {
			t.Errorf("%q принято", bad)
		}
	}
}

// Страж: класс x-rate-limit в контракте без политики — ошибка конфигурации (проверяет тест
// хендлеров на настоящих контрактах, Task 19).
func TestUnknownClasses(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /a:
    get: {operationId: a, x-rate-limit: auth, responses: {"200": {description: ok}}}
  /b:
    get: {operationId: b, x-rate-limit: nope, responses: {"200": {description: ok}}}
  /c:
    get: {operationId: c, responses: {"200": {description: ok}}}
`))
	if err != nil {
		t.Fatal(err)
	}
	got := ratelimit.UnknownClasses(spec, ratelimit.DefaultRules())
	if len(got) != 1 || got[0] != "GET /b: x-rate-limit nope" {
		t.Fatalf("got %v", got)
	}
}
```

- [ ] **Step 3: Падающие тесты middleware и чистки**

`backend/internal/platform/ratelimit/middleware_test.go`:

```go
package ratelimit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
)

const spec = `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /login:
    post: {operationId: login, x-rate-limit: auth, responses: {"204": {description: ok}}}
  /feed:
    get: {operationId: feed, responses: {"204": {description: ok}}}
`

func pipeline(t *testing.T, l ratelimit.Limiter, rules ratelimit.Rules, logs *bytes.Buffer) http.Handler {
	t.Helper()
	s, err := openapi3.NewLoader().LoadFromData([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(s)
	if err != nil {
		t.Fatal(err)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return routes(ratelimit.Middleware(l, rules, slog.New(slog.NewTextHandler(logs, nil)))(ok))
}

func do(h http.Handler, method, path, ip string, p *auth.Principal) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	ctx := peer.With(r.Context(), peer.Info{IP: netip.MustParseAddr(ip)})
	if p != nil {
		ctx = auth.With(ctx, p)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r.WithContext(ctx))
	return rec
}

func TestMiddleware(t *testing.T) {
	l, c, _ := limiter(t)
	rules := ratelimit.Rules{
		"default": {IP: ratelimit.Policy{Limit: 100, Period: time.Minute}, User: ratelimit.Policy{Limit: 2, Period: time.Minute}},
		"auth":    {IP: ratelimit.Policy{Limit: 2, Period: time.Minute}},
	}
	var logs bytes.Buffer
	h := pipeline(t, l, rules, &logs)

	// класс из x-rate-limit: два входа с IP, третий — 429
	for range 2 {
		if rec := do(h, http.MethodPost, "/login", "203.0.113.1", nil); rec.Code != 204 {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	rec := do(h, http.MethodPost, "/login", "203.0.113.1", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("третий вход: %d", rec.Code)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != httpx.CodeRateLimited {
		t.Fatalf("тело: %s", rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra != "30" {
		t.Fatalf("Retry-After = %q, нужно 30", ra)
	}
	// Review Focus 1: соседний IP не задет — счёт по реальному IP из peer
	if rec := do(h, http.MethodPost, "/login", "203.0.113.2", nil); rec.Code != 204 {
		t.Fatalf("другой IP: %d", rec.Code)
	}
	// классы не делят счёт: лента с того же IP идёт по default
	if rec := do(h, http.MethodGet, "/feed", "203.0.113.1", nil); rec.Code != 204 {
		t.Fatalf("лента: %d", rec.Code)
	}

	// по пользователю — с разных IP
	who := &auth.Principal{UserID: uuid.New()}
	for i, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		want := 204
		if i == 2 {
			want = 429
		}
		if rec := do(h, http.MethodGet, "/feed", ip, who); rec.Code != want {
			t.Fatalf("пользователь, запрос %d: %d", i+1, rec.Code)
		}
	}
	c.Advance(time.Minute)
	if rec := do(h, http.MethodGet, "/feed", "198.51.100.4", who); rec.Code != 204 {
		t.Fatalf("через минуту: %d", rec.Code)
	}
}

// Маршрут не из контракта лимитом не считается — его ответит роутер.
func TestMiddlewareSkipsUnknownRoute(t *testing.T) {
	var logs bytes.Buffer
	h := pipeline(t, failing{}, ratelimit.DefaultRules(), &logs)
	if rec := do(h, http.MethodGet, "/docs", "203.0.113.1", nil); rec.Code != 204 {
		t.Fatalf("status = %d", rec.Code)
	}
}

type failing struct{}

func (failing) Allow(context.Context, string, ratelimit.Policy) (ratelimit.Result, error) {
	return ratelimit.Result{}, errors.New("база недоступна")
}
func (failing) Add(context.Context, string, ratelimit.Policy) error { return errors.New("x") }
func (failing) Over(context.Context, string, ratelimit.Policy) (bool, error) {
	return false, errors.New("x")
}

// Хранилище лимитов недоступно — запрос проходит, ошибка — в лог (fail-open).
func TestMiddlewareFailOpen(t *testing.T) {
	var logs bytes.Buffer
	h := pipeline(t, failing{}, ratelimit.DefaultRules(), &logs)
	if rec := do(h, http.MethodGet, "/feed", "203.0.113.1", nil); rec.Code != 204 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(logs.String(), "база недоступна") {
		t.Fatalf("ошибки нет в логе: %s", logs.String())
	}
}
```

`backend/internal/platform/ratelimit/cleanup_test.go`:

```go
package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/testkit/clocktest"
)

// Чистит воркер — под ролью worker.
func TestCleanupDeletesOnlyExpired(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO rate_limits (key, tat) VALUES
		('old', $1), ('live', $2)`, start.Add(-time.Minute), start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	w := ratelimit.NewCleanupWorker(pools.As, clocktest.New(start))
	if err := w.Work(ctx, &river.Job[ratelimit.CleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	var keys []string
	rows, _ := pools.Owner.Query(ctx, "SELECT key FROM rate_limits ORDER BY key")
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		keys = append(keys, k)
	}
	if len(keys) != 1 || keys[0] != "live" {
		t.Fatalf("осталось %v", keys)
	}
	if ratelimit.CleanupJob() == nil || (ratelimit.CleanupArgs{}).InsertOpts().Queue != "maintenance" {
		t.Fatal("чистка не в очереди maintenance")
	}
}
```

- [ ] **Step 4: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/ratelimit/... -count=1`
Expected: FAIL — нет `ratelimit.NewPG` и остального.

- [ ] **Step 5: Реализация лимитера**

`backend/internal/platform/ratelimit/limiter.go`:

```go
// Package ratelimit — ограничение частоты запросов (спека бэкенда §6.6): GCRA, одна строка
// rate_limits на ключ, один UPSERT на проверку вне бизнес-транзакции. Ключи IP, пользователь,
// устройство — в Middleware; почта — в app/ модуля (тело запроса middleware не видит).
// Подсеть — только сигнал риска (пакет risk), не жёсткий 429: за CGNAT тысячи людей.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/ratelimit/ratelimitdb"
)

// Policy — Limit запросов за Period, подряд — до Burst (0 — равен Limit). Нулевая Policy
// (Limit 0) — ключ не ограничивается.
type Policy struct {
	Limit  int
	Period time.Duration
	Burst  int
}

func (p Policy) Validate() error {
	switch {
	case p == (Policy{}):
		return nil
	case p.Limit < 1:
		return fmt.Errorf("ratelimit: лимит %d — нужен ≥ 1", p.Limit)
	case p.Period <= 0:
		return fmt.Errorf("ratelimit: период %s — нужен > 0", p.Period)
	case p.Burst < 0:
		return fmt.Errorf("ratelimit: всплеск %d — нужен ≥ 0", p.Burst)
	case p.Period/time.Duration(p.Limit) <= 0:
		return fmt.Errorf("ratelimit: %d за %s — интервал меньше наносекунды", p.Limit, p.Period)
	}
	return nil
}

func (p Policy) off() bool { return p.Limit == 0 }

func (p Policy) interval() time.Duration { return p.Period / time.Duration(p.Limit) }

func (p Policy) burst() int {
	if p.Burst > 0 {
		return p.Burst
	}
	return p.Limit
}

type Result struct {
	Allowed    bool
	RetryAfter time.Duration // у отказа — когда следующий запрос пройдёт
}

// Limiter — за интерфейсом: переезд на Valkey — замена реализации.
type Limiter interface {
	// Allow — расходует, если запрос влезает в лимит.
	Allow(ctx context.Context, key string, p Policy) (Result, error)
	// Add — расходует всегда: счётчик сигнала риска (неудачный вход).
	Add(ctx context.Context, key string, p Policy) error
	// Over — порог превышен (следующий Allow не пройдёт), без расхода.
	Over(ctx context.Context, key string, p Policy) (bool, error)
}

type PG struct {
	q     *ratelimitdb.Queries
	clock clock.Clock
}

func NewPG(db ratelimitdb.DBTX, c clock.Clock) *PG { return &PG{q: ratelimitdb.New(db), clock: c} }

func (l *PG) Allow(ctx context.Context, key string, p Policy) (Result, error) {
	if p.off() {
		return Result{Allowed: true}, nil
	}
	now := l.clock.Now()
	t, w := p.interval(), p.interval()*time.Duration(p.burst())
	_, err := l.q.Take(ctx, ratelimitdb.TakeParams{Key: key, Now: now, IntervalS: t.Seconds(), WindowS: w.Seconds()})
	if err == nil {
		return Result{Allowed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, fmt.Errorf("ratelimit: %w", err)
	}
	tat, err := l.q.GetTAT(ctx, key)
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: %w", err)
	}
	// следующий запрос пройдёт, когда max(tat, now) + T − now ≤ окно
	base := tat
	if base.Before(now) {
		base = now
	}
	retry := base.Sub(now) + t - w
	if retry < time.Millisecond {
		retry = time.Millisecond
	}
	return Result{RetryAfter: retry}, nil
}

func (l *PG) Add(ctx context.Context, key string, p Policy) error {
	if p.off() {
		return nil
	}
	if err := l.q.Add(ctx, ratelimitdb.AddParams{Key: key, Now: l.clock.Now(), IntervalS: p.interval().Seconds()}); err != nil {
		return fmt.Errorf("ratelimit: %w", err)
	}
	return nil
}

func (l *PG) Over(ctx context.Context, key string, p Policy) (bool, error) {
	if p.off() {
		return false, nil
	}
	tat, err := l.q.GetTAT(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("ratelimit: %w", err)
	}
	return tat.Sub(l.clock.Now()) > p.interval()*time.Duration(p.burst()-1), nil
}

type unlimited struct{}

func (unlimited) Allow(context.Context, string, Policy) (Result, error) { return Result{Allowed: true}, nil }
func (unlimited) Add(context.Context, string, Policy) error             { return nil }
func (unlimited) Over(context.Context, string, Policy) (bool, error)    { return false, nil }

// Unlimited — всё пропускает: тесты хендлеров без базы.
var Unlimited Limiter = unlimited{}
```

Если sqlc назвал поля параметров иначе (Task 3 записал в отчёт) — использовать фактические имена.

- [ ] **Step 6: Реализация правил**

`backend/internal/platform/ratelimit/rules.go`:

```go
package ratelimit

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// ClassPolicy — политики класса ручек по ключам; нулевая — ключ не ограничивается.
type ClassPolicy struct {
	IP, User, Device Policy
}

// Rules — политики по классам: класс операции — x-rate-limit контракта, без него — default.
type Rules map[string]ClassPolicy

const DefaultClass = "default"

// DefaultRules — стартовые политики. IP щедрее пользователя: за одним адресом мобильного
// оператора (CGNAT) — многие. auth — вход, регистрация, коды: перебор паролей и рассылки.
func DefaultRules() Rules {
	return Rules{
		DefaultClass: {
			IP:     Policy{Limit: 600, Period: time.Minute, Burst: 120},
			User:   Policy{Limit: 300, Period: time.Minute, Burst: 60},
			Device: Policy{Limit: 300, Period: time.Minute, Burst: 60},
		},
		"auth": {
			IP:     Policy{Limit: 60, Period: time.Minute, Burst: 20},
			Device: Policy{Limit: 20, Period: time.Minute, Burst: 10},
		},
	}
}

var (
	classRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	ruleRe  = regexp.MustCompile(`^(\d+)/([0-9a-z]+)(?::(\d+))?$`)
)

// ParseRules — переопределения из окружения поверх base, через запятую:
// <класс>.<ip|user|device>=<лимит>/<период>[:<всплеск>] или =off. Пример:
// "auth.ip=30/1m:10,default.device=off". base не меняется.
func ParseRules(s string, base Rules) (Rules, error) {
	out := maps.Clone(base)
	for item := range strings.SplitSeq(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, value, ok := strings.Cut(item, "=")
		class, key, ok2 := strings.Cut(name, ".")
		if !ok || !ok2 || !classRe.MatchString(class) {
			return nil, fmt.Errorf("ratelimit: %q — нужен <класс>.<ключ>=<лимит>/<период>[:<всплеск>]", item)
		}
		var p Policy
		if value != "off" {
			m := ruleRe.FindStringSubmatch(value)
			if m == nil {
				return nil, fmt.Errorf("ratelimit: %q — значение не <лимит>/<период>[:<всплеск>] и не off", item)
			}
			p.Limit, _ = strconv.Atoi(m[1])
			period, err := time.ParseDuration(m[2])
			if err != nil {
				return nil, fmt.Errorf("ratelimit: %q — период: %w", item, err)
			}
			p.Period = period
			if m[3] != "" {
				p.Burst, _ = strconv.Atoi(m[3])
			}
			if p.Limit == 0 {
				return nil, fmt.Errorf("ratelimit: %q — лимит 0; выключить ключ — off", item)
			}
			if err := p.Validate(); err != nil {
				return nil, fmt.Errorf("ratelimit: %q: %w", item, err)
			}
		}
		cp := out[class]
		switch key {
		case "ip":
			cp.IP = p
		case "user":
			cp.User = p
		case "device":
			cp.Device = p
		default:
			return nil, fmt.Errorf("ratelimit: %q — ключ %q, нужен ip, user или device", item, key)
		}
		out[class] = cp
	}
	return out, nil
}

// UnknownClasses — операции, чей x-rate-limit не описан в правилах: «<МЕТОД> <путь>: x-rate-limit <класс>».
func UnknownClasses(spec *openapi3.T, r Rules) []string {
	var out []string
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			class, _ := op.Extensions["x-rate-limit"].(string)
			if class == "" {
				continue
			}
			if _, ok := r[class]; !ok {
				out = append(out, fmt.Sprintf("%s %s: x-rate-limit %s", method, path, class))
			}
		}
	}
	slices.Sort(out)
	return out
}
```

`"Auth.ip=1/1m"` отвергается `classRe`; `"auth.ip=1/1m:-2"` — `ruleRe` (минус не цифра); `"auth.ip=1/xx"` — `ParseDuration`.

- [ ] **Step 7: Реализация middleware**

`backend/internal/platform/ratelimit/middleware.go`:

```go
package ratelimit

import (
	"log/slog"
	"math"
	"net/http"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/peer"
)

// Middleware — лимит после валидации (спека §6.1). Класс — x-rate-limit операции (без него —
// default); ключи — IP из peer, пользователь из Principal, устройство (сессии или метка BFF).
// Маршрут не из контракта не считается. Хранилище недоступно — запрос проходит, ошибка — в
// лог: без базы не работает и остальное, а 500 на каждый запрос ничего не защищает.
func Middleware(l Limiter, r Rules, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			rt, ok := httpx.RouteFrom(req.Context())
			if !ok {
				next.ServeHTTP(w, req)
				return
			}
			class := rt.Extension("x-rate-limit")
			if class == "" {
				class = DefaultClass
			}
			cp, ok := r[class]
			if !ok {
				// страж UnknownClasses в тестах хендлеров не даёт сюда попасть
				log.ErrorContext(req.Context(), "ratelimit: класса нет в правилах", "class", class)
				cp = r[DefaultClass]
			}
			ctx := req.Context()
			info := peer.From(ctx)
			type check struct {
				key string
				p   Policy
			}
			var checks []check
			if info.IP.IsValid() {
				checks = append(checks, check{"ip:" + class + ":" + info.IP.String(), cp.IP})
			}
			device := info.Device
			if p, ok := auth.From(ctx); ok {
				checks = append(checks, check{"user:" + class + ":" + p.UserID.String(), cp.User})
				if p.DeviceID != uuid.Nil {
					device = p.DeviceID.String()
				}
			}
			if device != "" {
				checks = append(checks, check{"device:" + class + ":" + device, cp.Device})
			}
			for _, c := range checks {
				res, err := l.Allow(ctx, c.key, c.p)
				if err != nil {
					log.ErrorContext(ctx, "ratelimit: хранилище недоступно, запрос пропущен", "err", err)
					break
				}
				if !res.Allowed {
					httpx.WriteProblemValue(w, req, httpx.Problem{
						Status:     http.StatusTooManyRequests,
						Code:       httpx.CodeRateLimited,
						RetryAfter: int(math.Ceil(res.RetryAfter.Seconds())),
					})
					return
				}
			}
			next.ServeHTTP(w, req)
		})
	}
}
```

Проверка `Retry-After = 30` в тесте: политика auth 2/мин — интервал 30 с, всплеск 2; после двух запросов `tat = now+60s`, третий: `retry = 60 + 30 − 60 = 30 с`.

- [ ] **Step 8: Реализация чистки**

`backend/internal/platform/ratelimit/cleanup.go`:

```go
package ratelimit

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit/ratelimitdb"
)

const cleanupBatch = 1000

// CleanupArgs — чистка строк rate_limits с tat в прошлом (спека §9.3): они ничего не ограничивают.
type CleanupArgs struct {
	V int `json:"v"`
}

func (CleanupArgs) Kind() string { return "ratelimit.cleanup" }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: 10 * time.Minute}}
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q     *ratelimitdb.Queries
	clock clock.Clock
}

func NewCleanupWorker(db ratelimitdb.DBTX, c clock.Clock) *CleanupWorker {
	return &CleanupWorker{q: ratelimitdb.New(db), clock: c}
}

func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 5 * time.Minute }

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	for {
		n, err := w.q.DeleteExpired(ctx, ratelimitdb.DeleteExpiredParams{Now: w.clock.Now(), Batch: cleanupBatch})
		if err != nil {
			return err
		}
		if n < cleanupBatch {
			return nil
		}
	}
}

// CleanupJob — каждые 10 минут и при старте воркера.
func CleanupJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: "ratelimit.cleanup", RunOnStart: true})
}
```

- [ ] **Step 9: Тесты проходят**

Из `backend/`: `go test ./internal/platform/ratelimit/... ./internal/platform/httpx/... -count=1`
Expected: PASS.

Подсадка бага: в запросе `Take` (`queries/ratelimit.sql`) временно убрать `WHERE …` у `DO UPDATE` и перегенерировать **только** sqlc (`go tool -modfile=tools/go.mod sqlc generate` из `backend/` — Task 3 к этому моменту закрыта) → `TestAllowBurstThenRate` и `TestAllowConcurrent` падают; вернуть запрос и перегенерировать, `git status` — без изменений в `ratelimitdb`.

- [ ] **Step 10: Commit**

```bash
git add backend/internal/platform/ratelimit
git commit -m "Платформа: rate limit GCRA на Postgres — классы из контракта, ключи IP/пользователь/устройство, чистка" -- backend/internal/platform/ratelimit
```

---

### Task 11: Антибот — PoW по протоколу ALTCHA и сигналы риска

**Files:**
- Create: `backend/internal/platform/humancheck/humancheck.go`, `pow.go`, `cleanup.go`
- Create: `backend/internal/platform/humancheck/pow_test.go`, `humancheck_test.go`, `cleanup_test.go`
- Create: `backend/internal/platform/risk/risk.go`, `backend/internal/platform/risk/risk_test.go`

**Interfaces:**
- Consumes: `humancheckdb` (Task 3), `dbtest.NewPoolsAs` (Task 3), `clock`, `clocktest` (Task 2), `keys.Decode`, `keys.ID` (Task 8), `httpx.Problem`, `httpx.CodeHumancheckRequired` (Task 5), `ratelimit.Limiter`, `ratelimit.Policy`, `ratelimit.NewPG` (Task 10), `queue.Maintenance`.
- Produces:
  - `humancheck.Challenge{Algorithm, Challenge string; MaxNumber int64; Salt, Signature string}` (JSON — поля протокола ALTCHA в camelCase).
  - `humancheck.Verifier interface { NewChallenge(ctx) (Challenge, error); Verify(ctx, solution string) error }`; `humancheck.ErrFailed`.
  - `humancheck.PoWConfig{Keys []string; TTL time.Duration; MaxNumber int64}`; `humancheck.NewPoW(db humancheckdb.DBTX, c clock.Clock, cfg PoWConfig) (*humancheck.PoW, error)` (реализует `Verifier`).
  - `humancheck.Header = "X-WF-Humancheck"`; `humancheck.Middleware() func(http.Handler) http.Handler` — решение из заголовка в `ctx`; `humancheck.WithSolution(ctx, s string) context.Context`.
  - `humancheck.Require(ctx, v Verifier) error` — `nil` или `*humancheck.RequiredError` (реализует `httpx.ProblemError`: 403 `humancheck.required` с задачей).
  - `humancheck.CleanupArgs`, `humancheck.NewCleanupWorker(db humancheckdb.DBTX, c clock.Clock)`, `humancheck.CleanupJob()` (раз в час).
  - `risk.Signal` (`LoginFailed`, `Signup`, `NewDevice`, `Activity`); `risk.DefaultPolicies() map[Signal]ratelimit.Policy`; `risk.NewAssessor(l ratelimit.Limiter, p map[Signal]ratelimit.Policy) (*risk.Assessor, error)`; `(*Assessor).Record(ctx, s Signal, key string) error`; `(*Assessor).Suspicious(ctx, s Signal, keys ...string) (bool, error)`; `risk.Subnet(ip netip.Addr) string`.

Сценарий модуля (identity, вход): `if bad, _ := assessor.Suspicious(ctx, risk.LoginFailed, accountKey, ipKey); bad { if err := humancheck.Require(ctx, pow); err != nil { return err } }`; неудачный вход — `assessor.Record(ctx, risk.LoginFailed, …)`.

Протокол ALTCHA: соль `<24 hex>?expires=<Unix>&kid=<kid>&`, число `n ∈ [0, MaxNumber]`, `challenge = hex(SHA-256(соль + n))`, `signature = hex(HMAC-SHA-256(ключ, challenge))`. Решение — base64 от JSON `{algorithm, challenge, number, salt, signature}`.

- [ ] **Step 1: Падающие тесты PoW**

`backend/internal/platform/humancheck/pow_test.go`:

```go
package humancheck_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go"

	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

var start = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func newKey(t *testing.T) string {
	t.Helper()
	k, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newPoW(t *testing.T, pools dbtest.Pools, c *clocktest.Fake, ks ...string) *humancheck.PoW {
	t.Helper()
	p, err := humancheck.NewPoW(pools.As, c, humancheck.PoWConfig{Keys: ks, TTL: 5 * time.Minute, MaxNumber: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// solve — решает задачу официальной библиотекой ALTCHA, как виджет в браузере.
func solve(t *testing.T, ch humancheck.Challenge) string {
	t.Helper()
	sol, err := altcha.SolveChallenge(ch.Challenge, ch.Salt, altcha.SHA256, int(ch.MaxNumber), 0, nil)
	if err != nil || sol == nil {
		t.Fatalf("задача не решается: %v", err)
	}
	b, err := json.Marshal(altcha.Payload{Algorithm: ch.Algorithm, Challenge: ch.Challenge,
		Number: int64(sol.Number), Salt: ch.Salt, Signature: ch.Signature})
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// Совместимость с протоколом ALTCHA в обе стороны: задачу решает библиотека, а наше решение
// принимает её проверка (закрывает §14 спеки).
func TestPoWCompatibleWithALTCHA(t *testing.T) {
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	key := newKey(t)
	pow := newPoW(t, pools, clocktest.New(start), key)
	ch, err := pow.NewChallenge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Algorithm != "SHA-256" || ch.MaxNumber != 1000 {
		t.Fatalf("задача: %+v", ch)
	}
	s := solve(t, ch)
	raw, _ := keys.Decode(key)
	if ok, err := altcha.VerifySolution(s, string(raw), false); err != nil || !ok {
		t.Fatalf("библиотека ALTCHA не приняла: %v", err)
	}
	if err := pow.Verify(ctx, s); err != nil {
		t.Fatalf("решение не принято: %v", err)
	}
}

// Review Focus 5.
func TestPoWRejects(t *testing.T) {
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	c := clocktest.New(start)
	oldK, nextK := newKey(t), newKey(t)
	pow := newPoW(t, pools, c, oldK)

	ch, _ := pow.NewChallenge(ctx)
	s := solve(t, ch)
	if err := pow.Verify(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := pow.Verify(ctx, s); !errors.Is(err, humancheck.ErrFailed) {
		t.Fatalf("повтор решения принят: %v", err)
	}

	ch, _ = pow.NewChallenge(ctx)
	late := solve(t, ch)
	c.Advance(5*time.Minute + time.Second)
	if err := pow.Verify(ctx, late); !errors.Is(err, humancheck.ErrFailed) {
		t.Fatalf("просроченное принято: %v", err)
	}
	c.Set(start)

	// ротация: старый ключ ещё в списке — принимается; выведен — нет
	ch, _ = pow.NewChallenge(ctx)
	byOld := solve(t, ch)
	ch2, _ := pow.NewChallenge(ctx)
	byOld2 := solve(t, ch2)
	if err := newPoW(t, pools, c, nextK, oldK).Verify(ctx, byOld); err != nil {
		t.Fatalf("ротация: старый ключ отвергнут: %v", err)
	}
	if err := newPoW(t, pools, c, nextK).Verify(ctx, byOld2); !errors.Is(err, humancheck.ErrFailed) {
		t.Fatalf("ключ выведен, а решение принято: %v", err)
	}

	tamper := func(f func(p *altcha.Payload)) string {
		ch, _ := pow.NewChallenge(ctx)
		raw, _ := base64.StdEncoding.DecodeString(solve(t, ch))
		var p altcha.Payload
		_ = json.Unmarshal(raw, &p)
		f(&p)
		b, _ := json.Marshal(p)
		return base64.StdEncoding.EncodeToString(b)
	}
	cases := map[string]string{
		"чужое число":      tamper(func(p *altcha.Payload) { p.Number++ }),
		"алгоритм SHA-1":   tamper(func(p *altcha.Payload) { p.Algorithm = "SHA-1" }),
		"подпись подменена": tamper(func(p *altcha.Payload) {
			flip := byte('0')
			if p.Signature[0] == '0' {
				flip = '1'
			}
			p.Signature = string(flip) + p.Signature[1:]
		}),
		"срок в соли продлён": tamper(func(p *altcha.Payload) {
			p.Salt = p.Salt[:24] + "?expires=9999999999&" + p.Salt[strings.Index(p.Salt, "kid="):]
		}),
		"отрицательное число": tamper(func(p *altcha.Payload) { p.Number = -1 }),
		"не base64":           "%%%",
		"не JSON":             base64.StdEncoding.EncodeToString([]byte("nope")),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if err := pow.Verify(ctx, s); !errors.Is(err, humancheck.ErrFailed) {
				t.Fatalf("принято: %v", err)
			}
		})
	}
}

func TestNewPoWValidatesConfig(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "api")
	c := clocktest.New(start)
	short := base64.StdEncoding.EncodeToString([]byte("short"))
	for name, cfg := range map[string]humancheck.PoWConfig{
		"нет ключей":       {TTL: time.Minute, MaxNumber: 1000},
		"короткий ключ":    {Keys: []string{short}, TTL: time.Minute, MaxNumber: 1000},
		"нулевой срок":     {Keys: []string{newKey(t)}, MaxNumber: 1000},
		"нулевая сложность": {Keys: []string{newKey(t)}, TTL: time.Minute},
	} {
		if _, err := humancheck.NewPoW(pools.As, c, cfg); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
}
```

(в импорты — `strings`).

- [ ] **Step 2: Падающие тесты требования проверки и чистки**

`backend/internal/platform/humancheck/humancheck_test.go`:

```go
package humancheck_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestRequire(t *testing.T) {
	ctx := context.Background()
	pow := newPoW(t, dbtest.NewPoolsAs(t, "api"), clocktest.New(start), newKey(t))

	// решения нет — 403 humancheck.required с задачей
	err := humancheck.Require(ctx, pow)
	req, ok := errors.AsType[*humancheck.RequiredError](err)
	if !ok {
		t.Fatalf("ждали RequiredError: %v", err)
	}
	p := req.Problem()
	if p.Status != http.StatusForbidden || p.Code != httpx.CodeHumancheckRequired || p.Challenge == nil {
		t.Fatalf("Problem: %+v", p)
	}
	if _, ok := errors.AsType[httpx.ProblemError](err); !ok {
		t.Fatal("RequiredError не ProblemError — хендлер ответит 500")
	}

	// решение верное — проходит; то же второй раз — снова задача
	s := solve(t, req.Challenge)
	if err := humancheck.Require(humancheck.WithSolution(ctx, s), pow); err != nil {
		t.Fatalf("верное решение: %v", err)
	}
	if _, ok := errors.AsType[*humancheck.RequiredError](humancheck.Require(humancheck.WithSolution(ctx, s), pow)); !ok {
		t.Fatal("повтор решения пропущен")
	}
}

func TestMiddlewareCarriesSolution(t *testing.T) {
	pow := newPoW(t, dbtest.NewPoolsAs(t, "api"), clocktest.New(start), newKey(t))
	ch, _ := pow.NewChallenge(context.Background())
	s := solve(t, ch)
	var got error
	h := humancheck.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = humancheck.Require(r.Context(), pow)
	}))
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(humancheck.Header, s)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != nil {
		t.Fatalf("решение из заголовка не дошло: %v", got)
	}
}
```

`backend/internal/platform/humancheck/cleanup_test.go`:

```go
package humancheck_test

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestCleanupDeletesOnlyExpired(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO humancheck_spent (signature, expires_at) VALUES
		('old', $1), ('live', $2)`, start.Add(-time.Second), start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	w := humancheck.NewCleanupWorker(pools.As, clocktest.New(start))
	if err := w.Work(ctx, &river.Job[humancheck.CleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pools.Owner.QueryRow(ctx, "SELECT count(*) FROM humancheck_spent WHERE signature = 'live'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("живое удалено: %d %v", n, err)
	}
	if err := pools.Owner.QueryRow(ctx, "SELECT count(*) FROM humancheck_spent").Scan(&n); err != nil || n != 1 {
		t.Fatalf("строк %d", n)
	}
}
```

- [ ] **Step 3: Падающие тесты риска**

`backend/internal/platform/risk/risk_test.go`:

```go
package risk_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestAssessor(t *testing.T) {
	ctx := context.Background()
	c := clocktest.New(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	l := ratelimit.NewPG(dbtest.NewPoolsAs(t, "api").As, c)
	a, err := risk.NewAssessor(l, map[risk.Signal]ratelimit.Policy{
		risk.LoginFailed: {Limit: 3, Period: 15 * time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := a.Record(ctx, risk.LoginFailed, "user:a"); err != nil {
			t.Fatal(err)
		}
	}
	if bad, err := a.Suspicious(ctx, risk.LoginFailed, "user:a"); err != nil || !bad {
		t.Fatalf("3 неудачи из 3 — не подозрительно: %v", err)
	}
	// любой из ключей: аккаунт чист, а IP — нет
	if bad, _ := a.Suspicious(ctx, risk.LoginFailed, "ip:x", "user:a"); !bad {
		t.Fatal("подозрительный ключ среди нескольких не замечен")
	}
	if bad, _ := a.Suspicious(ctx, risk.LoginFailed, "user:b"); bad {
		t.Fatal("чистый аккаунт подозрителен")
	}
	c.Advance(15 * time.Minute)
	if bad, _ := a.Suspicious(ctx, risk.LoginFailed, "user:a"); bad {
		t.Fatal("подозрение не проходит со временем")
	}
	if _, err := a.Suspicious(ctx, risk.Signup, "x"); err == nil {
		t.Fatal("сигнал без политики принят")
	}
	if err := a.Record(ctx, risk.Signup, "x"); err == nil {
		t.Fatal("сигнал без политики записан")
	}
}

func TestDefaultPolicies(t *testing.T) {
	p := risk.DefaultPolicies()
	for _, s := range []risk.Signal{risk.LoginFailed, risk.Signup, risk.NewDevice, risk.Activity} {
		if p[s].Limit == 0 {
			t.Errorf("нет политики %s", s)
		}
	}
	if _, err := risk.NewAssessor(ratelimit.Unlimited, p); err != nil {
		t.Fatal(err)
	}
	if _, err := risk.NewAssessor(ratelimit.Unlimited, map[risk.Signal]ratelimit.Policy{risk.Signup: {Limit: -1}}); err == nil {
		t.Fatal("битая политика принята")
	}
}

func TestSubnet(t *testing.T) {
	cases := map[string]string{
		"203.0.113.77":        "203.0.113.0/24",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"::ffff:203.0.113.77": "203.0.113.0/24",
	}
	for in, want := range cases {
		if got := risk.Subnet(netip.MustParseAddr(in)); got != want {
			t.Errorf("Subnet(%s) = %q, нужно %q", in, got, want)
		}
	}
	if risk.Subnet(netip.Addr{}) != "" {
		t.Error("пустой адрес")
	}
}
```

- [ ] **Step 4: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/humancheck/... ./internal/platform/risk/... -count=1`
Expected: FAIL — пакетов нет.

- [ ] **Step 5: Реализация проверки**

`backend/internal/platform/humancheck/humancheck.go`:

```go
// Package humancheck — проверка «человек ли» (спека бэкенда §6.7): за интерфейсом Verifier.
// Реализация по умолчанию — PoW по протоколу ALTCHA (PoW); сторонний провайдер (Turnstile,
// SmartCaptcha, hCaptcha) — ещё одна реализация, включается флагом по стране. Когда требовать
// проверку, решает модуль по сигналам пакета risk.
package humancheck

import (
	"context"
	"errors"
	"net/http"

	"wf/backend/internal/platform/httpx"
)

// Header — решение задачи; клиент повторяет запрос с ним после 403 humancheck.required.
const Header = "X-WF-Humancheck"

// ErrFailed — решение неверное, просрочено, подписано неизвестным ключом или уже предъявлено.
var ErrFailed = errors.New("humancheck: решение не принято")

// Challenge — задача ALTCHA, уходит клиенту как есть (поля протокола в camelCase).
type Challenge struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	MaxNumber int64  `json:"maxNumber"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
}

type Verifier interface {
	NewChallenge(ctx context.Context) (Challenge, error)
	// Verify — ErrFailed для любого непринятого решения; иная ошибка — сбой (база).
	Verify(ctx context.Context, solution string) error
}

type solutionKey struct{}

func WithSolution(ctx context.Context, s string) context.Context {
	return context.WithValue(ctx, solutionKey{}, s)
}

// Middleware — решение из заголовка в ctx: сценарий модуля проверит его через Require, если
// риск потребует; без требования решение не тратится.
func Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s := r.Header.Get(Header); s != "" {
				r = r.WithContext(WithSolution(r.Context(), s))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequiredError — 403 humancheck.required с новой задачей.
type RequiredError struct {
	Challenge Challenge
}

func (e *RequiredError) Error() string { return "humancheck: нужна проверка" }

func (e *RequiredError) Problem() httpx.Problem {
	return httpx.Problem{Status: http.StatusForbidden, Code: httpx.CodeHumancheckRequired, Challenge: e.Challenge}
}

// Require — проверка там, где её потребовал риск: решение из ctx принято — nil; решения нет
// или оно не принято — *RequiredError с новой задачей; сбой проверки — ошибка как есть.
func Require(ctx context.Context, v Verifier) error {
	if s, _ := ctx.Value(solutionKey{}).(string); s != "" {
		err := v.Verify(ctx, s)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrFailed) {
			return err
		}
	}
	ch, err := v.NewChallenge(ctx)
	if err != nil {
		return err
	}
	return &RequiredError{Challenge: ch}
}
```

`backend/internal/platform/humancheck/pow.go`:

```go
package humancheck

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/humancheck/humancheckdb"
	"wf/backend/internal/platform/keys"
)

const (
	algorithm  = "SHA-256"
	saltBytes  = 12
	minKeySize = 32
)

type PoWConfig struct {
	Keys      []string      // ключи HMAC (base64, ≥ 32 байт): первый подписывает, все проверяют
	TTL       time.Duration // срок жизни задачи
	MaxNumber int64         // сложность: верхняя граница перебора
}

// PoW — серверная часть ALTCHA на своих часах: только SHA-256, kid ключа в соли, ротация
// списком. Использованное решение лежит в humancheck_spent до срока задачи.
type PoW struct {
	signKID string
	verify  map[string][]byte
	ttl     time.Duration
	max     int64
	clock   clock.Clock
	q       *humancheckdb.Queries
}

func NewPoW(db humancheckdb.DBTX, c clock.Clock, cfg PoWConfig) (*PoW, error) {
	if len(cfg.Keys) == 0 {
		return nil, errors.New("humancheck: нет ключей HMAC")
	}
	if cfg.TTL <= 0 || cfg.MaxNumber < 1 {
		return nil, fmt.Errorf("humancheck: срок %s и сложность %d — нужны больше нуля", cfg.TTL, cfg.MaxNumber)
	}
	p := &PoW{verify: map[string][]byte{}, ttl: cfg.TTL, max: cfg.MaxNumber, clock: c, q: humancheckdb.New(db)}
	for i, s := range cfg.Keys {
		k, err := keys.Decode(s)
		if err != nil {
			return nil, fmt.Errorf("humancheck: ключ №%d: %w", i+1, err)
		}
		if len(k) < minKeySize {
			return nil, fmt.Errorf("humancheck: ключ №%d — %d байт, нужно ≥ %d", i+1, len(k), minKeySize)
		}
		kid := keys.ID(k)
		p.verify[kid] = k
		if i == 0 {
			p.signKID = kid
		}
	}
	return p, nil
}

func (p *PoW) NewChallenge(context.Context) (Challenge, error) {
	raw := make([]byte, saltBytes)
	if _, err := rand.Read(raw); err != nil {
		return Challenge{}, err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(p.max+1))
	if err != nil {
		return Challenge{}, err
	}
	// параметры — как у ALTCHA: url.Values.Encode (ключи по алфавиту) и «&» в конце
	params := url.Values{"expires": {strconv.FormatInt(p.clock.Now().Add(p.ttl).Unix(), 10)}, "kid": {p.signKID}}
	salt := hex.EncodeToString(raw) + "?" + params.Encode() + "&"
	ch := hashHex(salt + n.String())
	return Challenge{Algorithm: algorithm, Challenge: ch, MaxNumber: p.max, Salt: salt,
		Signature: hmacHex(p.verify[p.signKID], ch)}, nil
}

type payload struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	Number    int64  `json:"number"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
}

func (p *PoW) Verify(ctx context.Context, solution string) error {
	raw, err := keys.Decode(solution)
	if err != nil {
		return ErrFailed
	}
	var s payload
	if json.Unmarshal(raw, &s) != nil || s.Algorithm != algorithm || s.Number < 0 {
		return ErrFailed
	}
	_, query, ok := strings.Cut(s.Salt, "?")
	if !ok {
		return ErrFailed
	}
	params, err := url.ParseQuery(strings.TrimSuffix(query, "&"))
	if err != nil {
		return ErrFailed
	}
	expires, err := strconv.ParseInt(params.Get("expires"), 10, 64)
	if err != nil || p.clock.Now().Unix() > expires {
		return ErrFailed
	}
	key, ok := p.verify[params.Get("kid")]
	if !ok {
		return ErrFailed
	}
	// соль с параметрами входит в хеш, а хеш — под подписью: подделать срок или kid нельзя
	if !equal(hashHex(s.Salt+strconv.FormatInt(s.Number, 10)), s.Challenge) || !equal(hmacHex(key, s.Challenge), s.Signature) {
		return ErrFailed
	}
	n, err := p.q.Spend(ctx, humancheckdb.SpendParams{Signature: s.Signature, ExpiresAt: time.Unix(expires, 0).UTC()})
	if err != nil {
		return fmt.Errorf("humancheck: %w", err)
	}
	if n == 0 {
		return ErrFailed // повтор
	}
	return nil
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacHex(key []byte, s string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return hex.EncodeToString(m.Sum(nil))
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
```

`backend/internal/platform/humancheck/cleanup.go`:

```go
package humancheck

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/humancheck/humancheckdb"
	"wf/backend/internal/platform/queue"
)

const cleanupBatch = 1000

// CleanupArgs — чистка решений PoW с истёкшим сроком задачи (спека §9.3).
type CleanupArgs struct {
	V int `json:"v"`
}

func (CleanupArgs) Kind() string { return "humancheck.cleanup" }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q     *humancheckdb.Queries
	clock clock.Clock
}

func NewCleanupWorker(db humancheckdb.DBTX, c clock.Clock) *CleanupWorker {
	return &CleanupWorker{q: humancheckdb.New(db), clock: c}
}

func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 5 * time.Minute }

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	for {
		n, err := w.q.DeleteExpired(ctx, humancheckdb.DeleteExpiredParams{Now: w.clock.Now(), Batch: cleanupBatch})
		if err != nil {
			return err
		}
		if n < cleanupBatch {
			return nil
		}
	}
}

func CleanupJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: "humancheck.cleanup", RunOnStart: true})
}
```

- [ ] **Step 6: Реализация риска**

`backend/internal/platform/risk/risk.go`:

```go
// Package risk — когда требовать проверку «человек ли» (спека бэкенда §6.7): мягкие пороги на
// сигналы — неудачные входы на аккаунт или IP, всплеск регистраций из подсети, новое
// устройство, аномальная частота действий. Превышение — повод для PoW, а не бан: аномалии —
// в очередь модерации, не автобан.
package risk

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"wf/backend/internal/platform/ratelimit"
)

type Signal string

const (
	LoginFailed Signal = "login_failed" // неудачный вход — ключи: аккаунт, IP
	Signup      Signal = "signup"       // регистрация — ключ: подсеть (Subnet)
	NewDevice   Signal = "new_device"   // вход с нового устройства — ключ: аккаунт
	Activity    Signal = "activity"     // частота действий — ключ: пользователь
)

// DefaultPolicies — стартовые пороги; меняются в спеке identity по опыту.
func DefaultPolicies() map[Signal]ratelimit.Policy {
	return map[Signal]ratelimit.Policy{
		LoginFailed: {Limit: 5, Period: 15 * time.Minute},
		Signup:      {Limit: 20, Period: time.Hour},
		NewDevice:   {Limit: 3, Period: 24 * time.Hour},
		Activity:    {Limit: 120, Period: time.Minute},
	}
}

var ErrUnknownSignal = errors.New("risk: у сигнала нет политики")

type Assessor struct {
	l        ratelimit.Limiter
	policies map[Signal]ratelimit.Policy
}

func NewAssessor(l ratelimit.Limiter, policies map[Signal]ratelimit.Policy) (*Assessor, error) {
	for s, p := range policies {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("risk: %s: %w", s, err)
		}
	}
	return &Assessor{l: l, policies: policies}, nil
}

func (a *Assessor) policy(s Signal) (ratelimit.Policy, error) {
	p, ok := a.policies[s]
	if !ok || p.Limit == 0 {
		return ratelimit.Policy{}, fmt.Errorf("%w: %s", ErrUnknownSignal, s)
	}
	return p, nil
}

// Record — учесть событие сигнала (неудачный вход, регистрацию).
func (a *Assessor) Record(ctx context.Context, s Signal, key string) error {
	p, err := a.policy(s)
	if err != nil {
		return err
	}
	return a.l.Add(ctx, "risk:"+string(s)+":"+key, p)
}

// Suspicious — порог сигнала превышен хотя бы по одному ключу.
func (a *Assessor) Suspicious(ctx context.Context, s Signal, keys ...string) (bool, error) {
	p, err := a.policy(s)
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		over, err := a.l.Over(ctx, "risk:"+string(s)+":"+k, p)
		if err != nil || over {
			return over, err
		}
	}
	return false, nil
}

// Subnet — ключ подсети для сигналов: /24 у IPv4, /64 у IPv6. Только сигнал, не 429 (§6.6).
func Subnet(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	ip = ip.Unmap()
	bits := 64
	if ip.Is4() {
		bits = 24
	}
	p, err := ip.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.String()
}
```

- [ ] **Step 7: Тесты проходят**

Из `backend/`: `go test ./internal/platform/humancheck/... ./internal/platform/risk/... ./internal/platform/httpx/... -count=1`
Expected: PASS.

Подсадка бага: в `Verify` убрать вызов `Spend` (вернуть `nil` сразу после проверки подписи) → падает «повтор решения принят» и `TestRequire`; вернуть.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/platform/humancheck backend/internal/platform/risk
git commit -m "Платформа: антибот — PoW по протоколу ALTCHA с ротацией ключей, сигналы риска, чистка решений" -- backend/internal/platform/humancheck backend/internal/platform/risk
```

---

### Task 12: Идемпотентность мутирующих запросов

**Files:**
- Modify: `backend/internal/platform/db/tx.go`, `backend/internal/platform/db/tx_test.go`
- Create: `backend/internal/platform/idempotency/idempotency.go`, `buffer.go`, `cleanup.go`
- Create: `backend/internal/platform/idempotency/idempotency_test.go`, `cleanup_test.go`

**Interfaces:**
- Consumes: `idempotencydb` (Task 3), `dbtest.NewPoolsAs` (Task 3), `auth.From`/`auth.With` (Task 9), `httpx.WriteError`, `httpx.NewError`, `httpx.WriteProblem`, коды `idempotency.*` и `internal` (Task 5), `db.InTx` (2/3), `queue.Maintenance`.
- Produces:
  - `db.TxHook func(ctx context.Context, tx pgx.Tx) error`; `db.WithTxHook(ctx, h TxHook) context.Context` — `db.InTx` зовёт хук из `ctx` в начале каждой транзакции, до `fn`; ошибка хука откатывает транзакцию, `fn` не вызывается.
  - `idempotency.Header = "Idempotency-Key"`; `idempotency.Config{LockTimeout time.Duration}` (0 — 3 с); `idempotency.Middleware(db idempotencydb.DBTX, cfg Config, log *slog.Logger) func(http.Handler) http.Handler`.
  - `idempotency.RequestHash(method, requestURI string, body []byte) string` — SHA-256 hex от метода, фактического пути с query и тела.
  - `idempotency.CleanupArgs`, `idempotency.NewCleanupWorker(db idempotencydb.DBTX) *CleanupWorker`, `idempotency.CleanupJob()` (раз в час).

Поток (спека §6.4): ключ и пользователь есть, метод мутирующий → по `(user_id, key)` ищется живой ключ. Нашёлся: другой запрос — `422 key_reused`; ответ сохранён — тот же ответ (`Idempotency-Replayed: true`); не сохранён — `409 replay_unavailable`. Не нашёлся: хук вставляет ключ в первой транзакции запроса (`lock_timeout` — только на эту вставку); дубль, ждущий на уникальном индексе дольше `LockTimeout`, — `409 in_progress`; ответ буферизуется и после коммита сохраняется (статус < 500, заголовки `Content-Type`, `Location`, `ETag`, тело — JSON или пусто).

- [ ] **Step 1: Падающие тесты хука транзакции**

В `backend/internal/platform/db/tx_test.go` дописать (используя хелперы файла — пул `dbtest.NewPool(t)` и таблицу, которую уже создают тесты `InTx`; если общей таблицы нет — `CREATE TABLE hook_probe (v int)` через пул в начале теста):

```go
// Хук идемпотентности исполняется в той же транзакции до fn: его запись живёт и умирает с ней.
func TestInTxRunsHookFirstInSameTx(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	if _, err := pool.Exec(ctx, "CREATE TABLE hook_probe (v int)"); err != nil {
		t.Fatal(err)
	}
	var order []string
	hook := func(ctx context.Context, tx pgx.Tx) error {
		order = append(order, "hook")
		if got, ok := db.TxFrom(ctx); !ok || got != tx {
			t.Error("хук получил не ту транзакцию")
		}
		_, err := tx.Exec(ctx, "INSERT INTO hook_probe VALUES (1)")
		return err
	}
	hctx := db.WithTxHook(ctx, hook)

	errBusiness := errors.New("бизнес-ошибка")
	err := db.InTx(hctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		order = append(order, "fn")
		return errBusiness
	})
	if !errors.Is(err, errBusiness) || len(order) != 2 || order[0] != "hook" {
		t.Fatalf("err=%v порядок=%v", err, order)
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM hook_probe").Scan(&n)
	if n != 0 {
		t.Fatal("запись хука пережила откат транзакции")
	}

	if err := db.InTx(hctx, pool, func(context.Context, pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM hook_probe").Scan(&n)
	if n != 1 {
		t.Fatalf("после коммита строк %d", n)
	}
}

func TestInTxHookErrorSkipsFn(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	errHook := errors.New("ключ занят")
	called := false
	err := db.InTx(db.WithTxHook(ctx, func(context.Context, pgx.Tx) error { return errHook }), pool,
		func(context.Context, pgx.Tx) error { called = true; return nil })
	if !errors.Is(err, errHook) || called {
		t.Fatalf("err=%v fn вызвана=%v", err, called)
	}
}
```

- [ ] **Step 2: Падающие тесты идемпотентности (под ролью api)**

`backend/internal/platform/idempotency/idempotency_test.go`:

```go
package idempotency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/testkit/dbtest"
)

var errBusiness = errors.New("бизнес-ошибка")

// probe — ручка «создать»: пишет строку в своей транзакции и отвечает 201 с Location.
type probe struct {
	pool    *pgxpool.Pool
	log     *slog.Logger
	calls   atomic.Int32
	fail    atomic.Bool    // бизнес-ошибка 422 в транзакции
	noTx    bool           // успех без транзакции — нарушение §6.4
	twoTx   bool           // две транзакции в одном запросе
	entered chan struct{}  // не nil — сигнал «ключ вставлен, транзакция открыта»
	gate    chan struct{}  // не nil — транзакция ждёт закрытия
}

func (p *probe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.calls.Add(1)
	if p.noTx {
		w.WriteHeader(http.StatusCreated)
		return
	}
	work := func(ctx context.Context, tx pgx.Tx) error {
		if p.entered != nil {
			p.entered <- struct{}{}
			<-p.gate
		}
		if _, err := tx.Exec(ctx, "INSERT INTO feature_flags (key) VALUES ($1)", "probe-"+uuid.NewString()); err != nil {
			return err
		}
		if p.fail.Load() {
			return errBusiness
		}
		return nil
	}
	err := db.InTx(r.Context(), p.pool, work)
	if err == nil && p.twoTx {
		err = db.InTx(r.Context(), p.pool, work)
	}
	switch {
	case errors.Is(err, errBusiness):
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	case err != nil:
		httpx.WriteError(p.log, w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/things/1")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"id": 1, "name": "x"}`))
}

type env struct {
	pools dbtest.Pools
	probe *probe
	h     http.Handler
	logs  *bytes.Buffer
	user  uuid.UUID
}

func setup(t *testing.T) *env {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api")
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	p := &probe{pool: pools.As, log: log}
	return &env{pools: pools, probe: p, logs: logs, user: uuid.New(),
		h: idempotency.Middleware(pools.As, idempotency.Config{LockTimeout: 200 * time.Millisecond}, log)(p)}
}

func (e *env) do(method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set(idempotency.Header, key)
	}
	r = r.WithContext(auth.With(r.Context(), &auth.Principal{UserID: e.user}))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *env) rows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pools.Owner.QueryRow(context.Background(), "SELECT count(*) FROM feature_flags WHERE key LIKE 'probe-%'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("не Problem: %s", rec.Body.String())
	}
	return p.Code
}

// без энтропии — gitleaks не примет за секрет
var key = strings.Repeat("k", 20)

func TestReplaySameRequest(t *testing.T) {
	e := setup(t)
	first := e.do(http.MethodPost, "/v1/things?x=1", key, `{"name":"x"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("первый: %d %s", first.Code, first.Body.String())
	}
	again := e.do(http.MethodPost, "/v1/things?x=1", key, `{"name":"x"}`)
	if again.Code != http.StatusCreated || again.Header().Get("Location") != "/v1/things/1" ||
		again.Header().Get("Content-Type") != "application/json" || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("повтор: %d %v", again.Code, again.Header())
	}
	var a, b map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(again.Body.Bytes(), &b)
	if a["id"] != b["id"] || a["name"] != b["name"] {
		t.Fatalf("тело повтора: %s", again.Body.String())
	}
	if e.probe.calls.Load() != 1 || e.rows(t) != 1 {
		t.Fatalf("исполнено %d раз, строк %d", e.probe.calls.Load(), e.rows(t))
	}
}

func TestKeyReusedWithOtherRequest(t *testing.T) {
	e := setup(t)
	e.do(http.MethodPost, "/v1/things", key, `{"name":"x"}`)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"другое тело":  e.do(http.MethodPost, "/v1/things", key, `{"name":"y"}`),
		"другой путь":  e.do(http.MethodPost, "/v1/teams", key, `{"name":"x"}`),
		"другой query": e.do(http.MethodPost, "/v1/things?a=1", key, `{"name":"x"}`),
	} {
		if rec.Code != http.StatusUnprocessableEntity || code(t, rec) != httpx.CodeIdempotencyKeyReused {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if e.probe.calls.Load() != 1 {
		t.Fatalf("исполнено %d раз", e.probe.calls.Load())
	}
}

// Бизнес-ошибка откатывает ключ вместе с транзакцией — повтор исполняется заново (§6.4).
func TestBusinessErrorRollsBackKey(t *testing.T) {
	e := setup(t)
	e.probe.fail.Store(true)
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("первый: %d", rec.Code)
	}
	e.probe.fail.Store(false)
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("повтор после 4xx не исполнен заново: %d", rec.Code)
	}
	if e.probe.calls.Load() != 2 || e.rows(t) != 1 {
		t.Fatalf("исполнено %d, строк %d", e.probe.calls.Load(), e.rows(t))
	}
}

// Review Focus 2: дубль, пока первый в транзакции, — 409 in_progress; после коммита — повтор.
func TestParallelDuplicate(t *testing.T) {
	e := setup(t)
	e.probe.entered, e.probe.gate = make(chan struct{}, 2), make(chan struct{})
	var first *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Go(func() { first = e.do(http.MethodPost, "/v1/things", key, `{}`) })
	<-e.probe.entered // первый вставил ключ и держит транзакцию

	start := time.Now()
	dup := e.do(http.MethodPost, "/v1/things", key, `{}`)
	if dup.Code != http.StatusConflict || code(t, dup) != httpx.CodeIdempotencyInProgress {
		t.Fatalf("дубль: %d %s", dup.Code, dup.Body.String())
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("дубль ждал %v — lock_timeout не сработал", waited)
	}
	close(e.probe.gate)
	wg.Wait()
	if first.Code != http.StatusCreated {
		t.Fatalf("первый: %d %s", first.Code, first.Body.String())
	}
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("после коммита: %d", rec.Code)
	}
	if e.rows(t) != 1 {
		t.Fatalf("строк %d — действие исполнено дважды", e.rows(t))
	}
}

// Закоммичено, а ответ не сохранился (обрыв между коммитом и записью ответа).
func TestReplayUnavailable(t *testing.T) {
	e := setup(t)
	hash := idempotency.RequestHash(http.MethodPost, "/v1/things", []byte(`{}`))
	if _, err := e.pools.Owner.Exec(context.Background(),
		`INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash) VALUES ($1, $2, '/v1/things', $3)`,
		e.user, key, hash); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodPost, "/v1/things", key, `{}`)
	if rec.Code != http.StatusConflict || code(t, rec) != httpx.CodeIdempotencyReplayUnavailable {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if e.probe.calls.Load() != 0 {
		t.Fatal("действие исполнено повторно")
	}
}

// Просроченный ключ (24 ч) переиспользуется.
func TestExpiredKeyReclaimed(t *testing.T) {
	e := setup(t)
	if _, err := e.pools.Owner.Exec(context.Background(),
		`INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash, response_status, expires_at)
		 VALUES ($1, $2, '/old', 'other', 200, now() - interval '1 second')`, e.user, key); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("новый ответ не сохранён")
	}
}

// Ручка, требующая идемпотентности, обязана выполняться в одной транзакции (§6.4).
func TestHandlerContractViolations(t *testing.T) {
	t.Run("успех без транзакции — 500", func(t *testing.T) {
		e := setup(t)
		e.probe.noTx = true
		rec := e.do(http.MethodPost, "/v1/things", key, `{}`)
		if rec.Code != http.StatusInternalServerError || code(t, rec) != httpx.CodeInternal {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(e.logs.String(), "без транзакции") {
			t.Fatalf("нарушение не в логе: %s", e.logs.String())
		}
	})
	t.Run("вторая транзакция — ошибка", func(t *testing.T) {
		e := setup(t)
		e.probe.twoTx = true
		if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%d", rec.Code)
		}
		if e.rows(t) != 1 {
			t.Fatalf("строк %d — вторая транзакция закоммитилась", e.rows(t))
		}
	})
}

func TestPassthrough(t *testing.T) {
	e := setup(t)
	// GET, без ключа, без пользователя — механизм не включается
	if rec := e.do(http.MethodGet, "/v1/things", key, ""); rec.Code != http.StatusCreated {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/v1/things", "", `{}`); rec.Code != http.StatusCreated {
		t.Fatalf("без ключа: %d", rec.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/things", strings.NewReader(`{}`))
	r.Header.Set(idempotency.Header, key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("без пользователя: %d", rec.Code)
	}
	var n int
	_ = e.pools.Owner.QueryRow(context.Background(), "SELECT count(*) FROM idempotency_keys").Scan(&n)
	if n != 0 {
		t.Fatalf("ключей %d", n)
	}
}
```

`backend/internal/platform/idempotency/cleanup_test.go`:

```go
package idempotency_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestCleanupDeletesOnlyExpired(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash, expires_at) VALUES
		($1, 'old', '/x', 'h', now() - interval '1 second'), ($1, 'live', '/x', 'h', now() + interval '1 hour')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := idempotency.NewCleanupWorker(pools.As).Work(ctx, &river.Job[idempotency.CleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	var keys []string
	rows, _ := pools.Owner.Query(ctx, "SELECT key FROM idempotency_keys")
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		keys = append(keys, k)
	}
	if len(keys) != 1 || keys[0] != "live" {
		t.Fatalf("осталось %v", keys)
	}
}
```

- [ ] **Step 3: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/db/... ./internal/platform/idempotency/... -count=1`
Expected: FAIL — нет `db.WithTxHook`, пакета `idempotency`.

- [ ] **Step 4: Хук в `db.InTx`**

В `backend/internal/platform/db/tx.go`:

```go
// TxHook — действие платформы в начале каждой транзакции db.InTx этого запроса: идемпотентность
// вставляет ключ в бизнес-транзакцию (спека §6.4). Ошибка хука откатывает транзакцию.
type TxHook func(ctx context.Context, tx pgx.Tx) error

type hookKey struct{}

// WithTxHook — хук для всех db.InTx с этим ctx.
func WithTxHook(ctx context.Context, h TxHook) context.Context {
	return context.WithValue(ctx, hookKey{}, h)
}
```

В `InTx` заменить строку `err = fn(context.WithValue(ctx, txKey{}, tx), tx)` на:

```go
	txCtx := context.WithValue(ctx, txKey{}, tx)
	if h, ok := ctx.Value(hookKey{}).(TxHook); ok && h != nil {
		if err = h(txCtx, tx); err != nil {
			completed = true
			return err
		}
	}
	err = fn(txCtx, tx)
```

и в комментарий `InTx` добавить: «Хук из ctx (WithTxHook) исполняется первым в той же транзакции».

- [ ] **Step 5: Буфер ответа**

`backend/internal/platform/idempotency/buffer.go`:

```go
package idempotency

import (
	"bytes"
	"net/http"
)

// buffer — ответ хендлера целиком в памяти: решение «отдать, заменить на 409/500 или сохранить
// для повтора» принимается после того, как хендлер закончил.
type buffer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBuffer() *buffer { return &buffer{header: http.Header{}} }

func (b *buffer) Header() http.Header { return b.header }

func (b *buffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *buffer) Write(p []byte) (int, error) {
	b.WriteHeader(http.StatusOK)
	return b.body.Write(p)
}

func (b *buffer) code() int {
	if b.status == 0 {
		return http.StatusOK
	}
	return b.status
}

func (b *buffer) flushTo(w http.ResponseWriter) {
	for k, v := range b.header {
		w.Header()[k] = v
	}
	w.WriteHeader(b.code())
	_, _ = w.Write(b.body.Bytes())
}
```

- [ ] **Step 6: Middleware**

`backend/internal/platform/idempotency/idempotency.go`:

```go
// Package idempotency — повтор мутирующего запроса не исполняет действие дважды (спека бэкенда
// §6.4). Ключ (user_id, key) вставляется хуком в бизнес-транзакцию ручки: дубль не закоммитится
// и при обрыве связи. Ответ сохраняется после коммита; повтор с тем же запросом получает его.
// Одна транзакция на идемпотентный запрос: вторая db.InTx — ошибка.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/idempotency/idempotencydb"
)

const (
	Header             = "Idempotency-Key"
	headerReplayed     = "Idempotency-Replayed"
	defaultLockTimeout = 3 * time.Second
	codeLockTimeout    = "55P03" // lock_not_available
)

// сохраняемые заголовки ответа: повтор отдаёт тот же тип, адрес созданного и версию
var savedHeaders = []string{"Content-Type", "Location", "ETag"}

var errSecondTx = errors.New("idempotency: вторая транзакция в идемпотентном запросе — нужна одна (спека §6.4)")

type Config struct {
	// LockTimeout — сколько дубль ждёт первый запрос на уникальном индексе, прежде чем
	// получить 409 idempotency.in_progress.
	LockTimeout time.Duration
}

// RequestHash — SHA-256 от метода, фактического пути с query и тела.
func RequestHash(method, requestURI string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method + "\n" + requestURI + "\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func Middleware(dbtx idempotencydb.DBTX, cfg Config, log *slog.Logger) func(http.Handler) http.Handler {
	if cfg.LockTimeout <= 0 {
		cfg.LockTimeout = defaultLockTimeout
	}
	q := idempotencydb.New(dbtx)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(Header)
			p, ok := auth.From(r.Context())
			if key == "" || !ok || !mutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeRequestInvalid, "")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			c := &claim{userID: p.UserID, key: key, endpoint: r.URL.Path,
				hash: RequestHash(r.Method, r.URL.RequestURI(), body), lockTimeout: cfg.LockTimeout}

			row, err := q.Get(r.Context(), idempotencydb.GetParams{UserID: c.userID, Key: key})
			switch {
			case err == nil:
				replay(w, r, row, c.hash)
				return
			case !errors.Is(err, pgx.ErrNoRows):
				httpx.WriteError(log, w, r, err)
				return
			}

			buf := newBuffer()
			next.ServeHTTP(buf, r.WithContext(db.WithTxHook(r.Context(), c.hook)))

			claimed, conflict := c.state()
			switch {
			case conflict != nil:
				httpx.WriteError(log, w, r, conflict)
			case !claimed && buf.code() < http.StatusBadRequest:
				log.ErrorContext(r.Context(), "idempotency: успешный ответ без транзакции — ручка обязана выполняться в db.InTx",
					"method", r.Method, "path", r.URL.Path)
				httpx.WriteProblem(w, r, http.StatusInternalServerError, httpx.CodeInternal, "")
			default:
				if claimed && buf.code() < http.StatusInternalServerError {
					c.save(context.WithoutCancel(r.Context()), q, buf, log)
				}
				buf.flushTo(w)
			}
		})
	}
}

func mutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func replay(w http.ResponseWriter, r *http.Request, row idempotencydb.GetRow, hash string) {
	switch {
	case row.RequestHash != hash:
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity, httpx.CodeIdempotencyKeyReused, "")
	case row.ResponseStatus == nil:
		httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeIdempotencyReplayUnavailable, "")
	default:
		var h map[string]string
		_ = json.Unmarshal(row.ResponseHeaders, &h)
		for k, v := range h {
			w.Header().Set(k, v)
		}
		w.Header().Set(headerReplayed, "true")
		w.WriteHeader(int(*row.ResponseStatus))
		_, _ = w.Write(row.ResponseBody)
	}
}

// claim — ключ этого запроса: вставляется хуком первой транзакции.
type claim struct {
	userID      uuid.UUID
	key         string
	endpoint    string
	hash        string
	lockTimeout time.Duration

	mu       sync.Mutex
	claimed  bool
	conflict *httpx.Error
}

func (c *claim) state() (bool, *httpx.Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.claimed, c.conflict
}

func (c *claim) hook(ctx context.Context, tx pgx.Tx) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.claimed || c.conflict != nil {
		return errSecondTx
	}
	q := idempotencydb.New(tx)
	prev, err := q.CurrentLockTimeout(ctx)
	if err != nil {
		return err
	}
	if err := q.SetLockTimeout(ctx, fmt.Sprintf("%dms", c.lockTimeout.Milliseconds())); err != nil {
		return err
	}
	_, err = q.Claim(ctx, idempotencydb.ClaimParams{UserID: c.userID, Key: c.key, Endpoint: c.endpoint, RequestHash: c.hash})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeLockTimeout {
		c.conflict = httpx.NewError(http.StatusConflict, httpx.CodeIdempotencyInProgress)
		return c.conflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// ключ занял запрос, закоммиченный между проверкой и вставкой
		c.conflict = httpx.NewError(http.StatusConflict, httpx.CodeIdempotencyInProgress)
		if row, err := q.Get(ctx, idempotencydb.GetParams{UserID: c.userID, Key: c.key}); err == nil && row.RequestHash != c.hash {
			c.conflict = httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeIdempotencyKeyReused)
		}
		return c.conflict
	}
	if err != nil {
		return err
	}
	c.claimed = true
	// lock_timeout — только для вставки ключа, не для бизнес-запросов транзакции
	return q.SetLockTimeout(ctx, prev)
}

func (c *claim) save(ctx context.Context, q *idempotencydb.Queries, buf *buffer, log *slog.Logger) {
	body := buf.body.Bytes()
	if len(body) == 0 {
		body = nil
	} else if !json.Valid(body) {
		log.WarnContext(ctx, "idempotency: ответ не JSON — повтор получит replay_unavailable")
		return
	}
	h := map[string]string{}
	for _, k := range savedHeaders {
		if v := buf.header.Get(k); v != "" {
			h[k] = v
		}
	}
	headers, _ := json.Marshal(h)
	status := int32(buf.code()) //nolint:gosec // HTTP-статус
	if _, err := q.SaveResponse(ctx, idempotencydb.SaveResponseParams{Status: &status, Headers: headers, Body: body,
		UserID: c.userID, Key: c.key, RequestHash: c.hash}); err != nil {
		log.ErrorContext(ctx, "idempotency: ответ не сохранён — повтор получит replay_unavailable", "err", err)
	}
}
```

Если имена полей `idempotencydb` (Task 3, отчёт) отличаются — использовать фактические.

- [ ] **Step 7: Чистка**

`backend/internal/platform/idempotency/cleanup.go`:

```go
package idempotency

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/idempotency/idempotencydb"
	"wf/backend/internal/platform/queue"
)

const cleanupBatch = 1000

// CleanupArgs — чистка ключей старше 24 часов (спека §6.4, §9.3).
type CleanupArgs struct {
	V int `json:"v"`
}

func (CleanupArgs) Kind() string { return "idempotency.cleanup" }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q *idempotencydb.Queries
}

func NewCleanupWorker(db idempotencydb.DBTX) *CleanupWorker {
	return &CleanupWorker{q: idempotencydb.New(db)}
}

func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 10 * time.Minute }

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	for {
		n, err := w.q.DeleteExpired(ctx, cleanupBatch)
		if err != nil {
			return err
		}
		if n < cleanupBatch {
			return nil
		}
	}
}

func CleanupJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: "idempotency.cleanup", RunOnStart: true})
}
```

- [ ] **Step 8: Тесты проходят**

Из `backend/`: `go test ./internal/platform/db/... ./internal/platform/idempotency/... ./internal/platform/events/... ./internal/platform/audit/... ./internal/platform/httpx/... -count=1`
Expected: PASS (события и аудит не задеты хуком: без `WithTxHook` поведение прежнее).

Подсадки багов:
1. В `hook` убрать `SetLockTimeout` перед `Claim` → дубль ждёт первый, а первый — ворота теста: `go test ./internal/platform/idempotency/... -run TestParallelDuplicate -count=1 -timeout 30s` падает по таймауту; вернуть.
2. В `Middleware` убрать ветку `!claimed && buf.code() < 400` → падает «успех без транзакции — 500»; вернуть.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/platform/db backend/internal/platform/idempotency
git commit -m "Платформа: идемпотентность — ключ в бизнес-транзакции через хук InTx, повтор ответа, 409/422, чистка" -- backend/internal/platform/db backend/internal/platform/idempotency
```

---

## Фаза D. Остальные механизмы платформы

### Task 13: Пароли — argon2id под семафором, политика и список распространённых

**Files:**
- Create: `backend/internal/platform/password/password.go`, `policy.go`, `common.txt`
- Create: `backend/internal/platform/password/password_test.go`, `policy_test.go`, `export_test.go`

**Interfaces:**
- Consumes: —
- Produces:
  - `password.Params{MemoryKiB, Iterations uint32; Parallelism uint8}`; `password.DefaultParams` = `{19456, 2, 1}` (минимум OWASP, §7.1).
  - `password.NewHasher(p Params, concurrency int) (*password.Hasher, error)` — параметры ниже минимума или `concurrency < 1` — ошибка.
  - `(*Hasher).Hash(ctx, pw string) (string, error)` — строка PHC `$argon2id$v=19$m=…,t=…,p=…$<соль>$<хэш>`.
  - `(*Hasher).Verify(ctx, pw, encoded string) (ok, needsRehash bool, err error)`; `password.ErrMalformed`.
  - `(*Hasher).VerifyDummy(ctx, pw string) error` — тот же argon2 на фиктивном хэше (выравнивание времени при «нет почты», §7.1).
  - `password.Check(pw string) error` — `ErrTooShort` (< 10 символов), `ErrTooLong` (> 128), `ErrCommon` (из списка); `password.MinLength = 10`, `password.MaxLength = 128`.

Хэширование ограничено семафором: argon2 берёт 19 МиБ на вызов, анонимный поток входов без ограничения съел бы память (§6.9). Ожидание семафора прерывается `ctx`.

- [ ] **Step 1: Список распространённых паролей**

Из корня (временные файлы — в `.tools/tmp/`):

```bash
mkdir -p .tools/tmp
curl -fsSL https://raw.githubusercontent.com/danielmiessler/SecLists/master/Passwords/Common-Credentials/100k-most-used-passwords-NCSC.txt -o .tools/tmp/ncsc.txt
wc -l .tools/tmp/ncsc.txt
```

Expected: ≈ 99 840 строк. Собрать `backend/internal/platform/password/common.txt` — заголовок, затем строки ≥ 10 символов в нижнем регистре, без повторов, плюс русские раскладочные:

```bash
{
  printf '%s\n' \
    '# Распространённые пароли от 10 символов (спека бэкенда §7.1): пароль из списка не принимается.' \
    '# Источник: SecLists, Passwords/Common-Credentials/100k-most-used-passwords-NCSC.txt' \
    '# https://github.com/danielmiessler/SecLists — MIT License, Copyright (c) 2018 Daniel Miessler.' \
    '# Отбор: строки длиной от 10 символов (короче не пропустит правило длины), нижний регистр,' \
    '# без повторов; в конце — русские раскладочные последовательности. Пересобрать — Task 13 плана 3/3.'
  { tr -d '\r' < .tools/tmp/ncsc.txt | LC_ALL=C.UTF-8 awk 'length($0) >= 10 { print tolower($0) }'
    printf '%s\n' 'йцукенгшщз' 'йцукенгшщзх' 'йцукенгшщзхъ' 'фывапролдж' 'фывапролджэ' 'пароль1234' 'пароль12345' 'пароль123456'
  } | LC_ALL=C sort -u
} > backend/internal/platform/password/common.txt
grep -vc '^#' backend/internal/platform/password/common.txt
```

Expected: ≈ 9 250 строк без заголовка. Если `awk` в Git Bash считает длину в байтах (кириллица из SecLists длиннее) — это лишь добавит в список несколько строк, проверка от этого не слабеет.

- [ ] **Step 2: Падающие тесты политики**

`backend/internal/platform/password/policy_test.go`:

```go
package password_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"wf/backend/internal/platform/password"
)

func TestCheck(t *testing.T) {
	cases := map[string]error{
		"short":                        password.ErrTooShort,
		"девять!!!":                    password.ErrTooShort, // 9 символов, хотя байт больше 10
		"correct horse battery staple": nil,
		"мой длинный пароль":           nil,
		"1234567890":                   password.ErrCommon,
		"QWERTYUIOP":                   password.ErrCommon, // регистр не спасает
		"ЙЦУКЕНГШЩЗ":                   password.ErrCommon,
		"пароль1234":                   password.ErrCommon,
		strings.Repeat("я", 129):       password.ErrTooLong,
		strings.Repeat("я", 128):       nil,
	}
	for pw, want := range cases {
		if got := password.Check(pw); !errors.Is(got, want) {
			t.Errorf("Check(%q) = %v, нужно %v", pw, got, want)
		}
	}
}

// Список вшит целиком и соответствует правилу отбора: от 10 символов, нижний регистр.
func TestCommonListIntegrity(t *testing.T) {
	n := 0
	for _, line := range password.CommonList() {
		if utf8.RuneCountInString(line) < password.MinLength && len(line) < password.MinLength {
			t.Errorf("короткая строка %q", line)
		}
		if line != strings.ToLower(line) {
			t.Errorf("не нижний регистр: %q", line)
		}
		n++
	}
	if n < 9000 {
		t.Fatalf("в списке %d паролей — файл обрезан?", n)
	}
}
```

- [ ] **Step 3: Падающие тесты хэширования**

`backend/internal/platform/password/export_test.go`:

```go
package password

// CommonList — строки вшитого списка (только тесты).
func CommonList() []string {
	out := make([]string, 0, len(commonSet()))
	for k := range commonSet() {
		out = append(out, k)
	}
	return out
}

// Occupy — занять все места семафора; возвращает освобождение (только тесты).
func Occupy(h *Hasher) func() {
	for range cap(h.sem) {
		h.sem <- struct{}{}
	}
	return func() {
		for range cap(h.sem) {
			<-h.sem
		}
	}
}
```

`backend/internal/platform/password/password_test.go`:

```go
package password_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/password"
)

func hasher(t *testing.T, p password.Params) *password.Hasher {
	t.Helper()
	h, err := password.NewHasher(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHashAndVerify(t *testing.T) {
	ctx := context.Background()
	h := hasher(t, password.DefaultParams)
	enc, err := h.Hash(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("формат: %s", enc)
	}
	if ok, rehash, err := h.Verify(ctx, "correct horse battery staple", enc); err != nil || !ok || rehash {
		t.Fatalf("верный пароль: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, err := h.Verify(ctx, "wrong horse battery staple", enc); err != nil || ok {
		t.Fatalf("неверный пароль принят: %v", err)
	}
	other, _ := h.Hash(ctx, "correct horse battery staple")
	if other == enc {
		t.Fatal("одинаковая соль у двух хэшей")
	}
}

// Параметры выросли — при входе хэш пересчитывается (§7.1).
func TestNeedsRehash(t *testing.T) {
	ctx := context.Background()
	enc, _ := hasher(t, password.DefaultParams).Hash(ctx, "correct horse battery staple")
	stronger := hasher(t, password.Params{MemoryKiB: 19456, Iterations: 3, Parallelism: 1})
	ok, rehash, err := stronger.Verify(ctx, "correct horse battery staple", enc)
	if err != nil || !ok || !rehash {
		t.Fatalf("ok=%v rehash=%v err=%v", ok, rehash, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	h := hasher(t, password.DefaultParams)
	for _, enc := range []string{"", "plain", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=x,t=2,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=19456,t=2,p=1$!!$aGFzaA",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdA$aGFzaA"} {
		if _, _, err := h.Verify(context.Background(), "x", enc); !errors.Is(err, password.ErrMalformed) {
			t.Errorf("%q: %v", enc, err)
		}
	}
}

func TestNewHasherRejectsWeakParams(t *testing.T) {
	for _, p := range []password.Params{
		{MemoryKiB: 8192, Iterations: 2, Parallelism: 1},
		{MemoryKiB: 19456, Iterations: 1, Parallelism: 1},
		{MemoryKiB: 19456, Iterations: 2, Parallelism: 0},
	} {
		if _, err := password.NewHasher(p, 1); err == nil {
			t.Errorf("слабые %+v приняты", p)
		}
	}
	if _, err := password.NewHasher(password.DefaultParams, 0); err == nil {
		t.Error("семафор 0 принят")
	}
}

// Все места заняты — запрос ждёт не дольше своего ctx.
func TestSemaphoreRespectsContext(t *testing.T) {
	h := hasher(t, password.DefaultParams)
	release := password.Occupy(h)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.Hash(ctx, "correct horse battery staple"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Hash: %v", err)
	}
	if err := h.VerifyDummy(ctx, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("VerifyDummy: %v", err)
	}
}

// Фиктивная проверка стоит столько же, сколько настоящая: «нет почты» и «неверный пароль» по
// времени неразличимы (§7.1). Допуск грубый — тест ловит пропуск argon2, а не микросекунды.
func TestVerifyDummyCostsLikeVerify(t *testing.T) {
	ctx := context.Background()
	h := hasher(t, password.DefaultParams)
	enc, _ := h.Hash(ctx, "correct horse battery staple")
	start := time.Now()
	_, _, _ = h.Verify(ctx, "wrong", enc)
	real := time.Since(start)
	start = time.Now()
	if err := h.VerifyDummy(ctx, "wrong"); err != nil {
		t.Fatal(err)
	}
	if dummy := time.Since(start); dummy < real/3 {
		t.Fatalf("фиктивная проверка %v против настоящей %v — argon2 пропущен", dummy, real)
	}
}
```

- [ ] **Step 4: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/password/... -count=1`
Expected: FAIL — нет пакета.

- [ ] **Step 5: Реализация политики**

`backend/internal/platform/password/policy.go`:

```go
package password

import (
	_ "embed"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	MinLength = 10  // NIST SP 800-63B: длина вместо правил состава (§7.1)
	MaxLength = 128 // защита от гигантских паролей; argon2 съел бы и их, но смысла нет
)

var (
	ErrTooShort = errors.New("password: короче 10 символов")
	ErrTooLong  = errors.New("password: длиннее 128 символов")
	ErrCommon   = errors.New("password: из списка распространённых")
)

//go:embed common.txt
var commonRaw string

var commonSet = sync.OnceValue(func() map[string]struct{} {
	set := make(map[string]struct{}, 10000)
	for line := range strings.SplitSeq(commonRaw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		set[line] = struct{}{}
	}
	return set
})

// Check — правила пароля без правил состава: длина в символах и список распространённых
// (без учёта регистра).
func Check(pw string) error {
	switch n := utf8.RuneCountInString(pw); {
	case n < MinLength:
		return ErrTooShort
	case n > MaxLength:
		return ErrTooLong
	}
	if _, ok := commonSet()[strings.ToLower(pw)]; ok {
		return ErrCommon
	}
	return nil
}
```

- [ ] **Step 6: Реализация хэширования**

`backend/internal/platform/password/password.go`:

```go
// Package password — хэширование паролей (спека бэкенда §6.9, §7.1): argon2id в формате PHC,
// параметры не ниже OWASP (m = 19 МиБ, t = 2, p = 1) из конфига, пересчёт хэша при входе,
// если параметры выросли; число одновременных хэширований ограничено семафором.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
}

// DefaultParams — минимум OWASP для argon2id.
var DefaultParams = Params{MemoryKiB: 19456, Iterations: 2, Parallelism: 1}

const (
	saltLen = 16
	keyLen  = 32
)

var ErrMalformed = errors.New("password: хэш не в формате argon2id PHC")

type Hasher struct {
	params Params
	sem    chan struct{}
	dummy  string // хэш случайного пароля для VerifyDummy
}

func NewHasher(p Params, concurrency int) (*Hasher, error) {
	if p.MemoryKiB < DefaultParams.MemoryKiB || p.Iterations < DefaultParams.Iterations || p.Parallelism < 1 {
		return nil, fmt.Errorf("password: параметры %+v ниже минимума OWASP %+v", p, DefaultParams)
	}
	if concurrency < 1 {
		return nil, fmt.Errorf("password: одновременных хэширований %d — нужно ≥ 1", concurrency)
	}
	h := &Hasher{params: p, sem: make(chan struct{}, concurrency)}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	dummy, err := h.Hash(context.Background(), base64.RawStdEncoding.EncodeToString(random))
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

func (h *Hasher) acquire(ctx context.Context) (func(), error) {
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := h.params
	key := argon2.IDKey([]byte(pw), salt, p.Iterations, p.MemoryKiB, p.Parallelism, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify — совпадает ли пароль; needsRehash — хэш сделан с параметрами слабее текущих.
func (h *Hasher) Verify(ctx context.Context, pw, encoded string) (bool, bool, error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, false, err
	}
	defer release()
	got := argon2.IDKey([]byte(pw), salt, p.Iterations, p.MemoryKiB, p.Parallelism, uint32(len(key))) //nolint:gosec // длина ключа из своего хэша
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	weaker := p.MemoryKiB < h.params.MemoryKiB || p.Iterations < h.params.Iterations ||
		p.Parallelism < h.params.Parallelism || len(key) != keyLen
	return true, weaker, nil
}

// VerifyDummy — та же работа на фиктивном хэше: ответ «нет такой почты» по времени не
// отличается от «неверный пароль» (§7.1). Ошибка — только отмена ctx.
func (h *Hasher) VerifyDummy(ctx context.Context, pw string) error {
	_, _, err := h.Verify(ctx, pw, h.dummy)
	return err
}

func decode(s string) (Params, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrMalformed
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return Params{}, nil, nil, ErrMalformed
	}
	var p Params
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Iterations, &p.Parallelism); err != nil || n != 3 ||
		p.MemoryKiB == 0 || p.Iterations == 0 || p.Parallelism == 0 {
		return Params{}, nil, nil, ErrMalformed
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	key, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(salt) == 0 || len(key) == 0 {
		return Params{}, nil, nil, ErrMalformed
	}
	return p, salt, key, nil
}
```

В `TestCommonListIntegrity` условие «короткая строка» допускает байтовую длину ≥ 10 (см. Step 1 про `awk`); если `awk` считал символы — проверка строгая по символам.

- [ ] **Step 7: Тесты проходят**

Из `backend/`: `go test ./internal/platform/password/... -count=1`
Expected: PASS.

Подсадка бага: в `VerifyDummy` вернуть `nil` сразу → падает `TestVerifyDummyCostsLikeVerify` (и `TestSemaphoreRespectsContext`); вернуть.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/platform/password
git commit -m "Платформа: пароли — argon2id PHC под семафором, пересчёт при росте параметров, список NCSC от 10 символов" -- backend/internal/platform/password
```

---

### Task 14: Серверные локали — тексты писем и пушей

**Files:**
- Create: `backend/locales/embed.go`, `backend/locales/ru.json`, `backend/locales/en.json`
- Create: `backend/internal/platform/i18n/i18n.go`, `backend/internal/platform/i18n/i18n_test.go`

**Interfaces:**
- Consumes: —
- Produces:
  - `locales.FS embed.FS` (`wf/backend/locales`) — `ru.json`, `en.json`.
  - `i18n.Default = "ru"`; `i18n.Supported = []string{"ru", "en"}`; `i18n.Load(fsys fs.FS) (*i18n.Catalog, error)` — компилирует все сообщения (ICU MessageFormat v1, вложенные ключи — через точку); `(*Catalog).Text(locale, key string, args map[string]any) (string, error)` — неизвестный язык → `ru`; неизвестный ключ — `i18n.ErrUnknownKey`; недостающий аргумент — ошибка; `(*Catalog).Keys(locale string) []string`.

Язык письма — `users.locale` (§6.9); `Text` зовёт `mail.Composer` модуля. Страж §12.5 «одинаковые ключи ru/en» — тест этой задачи на настоящих файлах.

- [ ] **Step 1: Файлы локалей**

`backend/locales/embed.go`:

```go
// Package locales — тексты писем и пушей бэкенда (спека §6.9, hard rule 6 CLAUDE.md):
// ICU MessageFormat, ключи в ru и en одинаковы (страж — internal/platform/i18n).
package locales

import "embed"

//go:embed *.json
var FS embed.FS
```

`backend/locales/ru.json`:

```json
{
  "common": {
    "minutes": "{count, plural, one {# минуту} few {# минуты} many {# минут} other {# минуты}}"
  },
  "mail": {
    "footer": "Письмо отправлено автоматически — отвечать на него не нужно."
  }
}
```

`backend/locales/en.json`:

```json
{
  "common": {
    "minutes": "{count, plural, one {# minute} other {# minutes}}"
  },
  "mail": {
    "footer": "This email was sent automatically, please do not reply."
  }
}
```

- [ ] **Step 2: Падающие тесты**

`backend/internal/platform/i18n/i18n_test.go`:

```go
package i18n_test

import (
	"errors"
	"regexp"
	"slices"
	"testing"
	"testing/fstest"

	"wf/backend/internal/platform/i18n"
	"wf/backend/locales"
)

func catalog(t *testing.T) *i18n.Catalog {
	t.Helper()
	c, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Страж §12.5: ключи ru и en одинаковы.
func TestLocalesHaveSameKeys(t *testing.T) {
	c := catalog(t)
	ru, en := c.Keys("ru"), c.Keys("en")
	if len(ru) == 0 || !slices.Equal(ru, en) {
		t.Fatalf("ключи расходятся:\nru: %v\nen: %v", ru, en)
	}
}

var argRe = regexp.MustCompile(`\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*[,}]`)

// Аргументы сообщения одинаковы в обоих языках: письмо не теряет подстановку при переводе.
func TestLocalesHaveSameArguments(t *testing.T) {
	raw := map[string]map[string]string{}
	for _, loc := range i18n.Supported {
		m, err := i18n.Flatten(locales.FS, loc)
		if err != nil {
			t.Fatal(err)
		}
		raw[loc] = m
	}
	args := func(s string) []string {
		var out []string
		for _, m := range argRe.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	for k, ru := range raw["ru"] {
		if a, b := args(ru), args(raw["en"][k]); !slices.Equal(a, b) {
			t.Errorf("%s: аргументы ru %v, en %v", k, a, b)
		}
	}
}

func TestPlurals(t *testing.T) {
	c := catalog(t)
	cases := []struct {
		loc   string
		count int
		want  string
	}{
		{"ru", 1, "1 минуту"}, {"ru", 2, "2 минуты"}, {"ru", 5, "5 минут"}, {"ru", 21, "21 минуту"}, {"ru", 15, "15 минут"},
		{"en", 1, "1 minute"}, {"en", 15, "15 minutes"},
	}
	for _, c2 := range cases {
		got, err := c.Text(c2.loc, "common.minutes", map[string]any{"count": c2.count})
		if err != nil || got != c2.want {
			t.Errorf("%s %d: %q %v, нужно %q", c2.loc, c2.count, got, err, c2.want)
		}
	}
}

func TestTextErrorsAndFallback(t *testing.T) {
	c := catalog(t)
	ru, _ := c.Text("ru", "mail.footer", nil)
	if de, err := c.Text("de", "mail.footer", nil); err != nil || de != ru {
		t.Fatalf("неизвестный язык не ушёл в ru: %q %v", de, err)
	}
	if _, err := c.Text("ru", "mail.nope", nil); !errors.Is(err, i18n.ErrUnknownKey) {
		t.Fatalf("неизвестный ключ: %v", err)
	}
	if _, err := c.Text("ru", "common.minutes", nil); err == nil {
		t.Fatal("недостающий аргумент не ошибка — письмо ушло бы с дырой")
	}
}

func TestLoadRejectsBrokenCatalogs(t *testing.T) {
	ok := `{"a": "x"}`
	for name, fsys := range map[string]fstest.MapFS{
		"нет en":            {"ru.json": {Data: []byte(ok)}},
		"не строка":         {"ru.json": {Data: []byte(`{"a": 1}`)}, "en.json": {Data: []byte(ok)}},
		"битый ICU":         {"ru.json": {Data: []byte(`{"a": "{count, plural, one {x}"}`)}, "en.json": {Data: []byte(ok)}},
		"не JSON":           {"ru.json": {Data: []byte(`{`)}, "en.json": {Data: []byte(ok)}},
	} {
		if _, err := i18n.Load(fsys); err == nil {
			t.Errorf("%s: загружено", name)
		}
	}
}
```

- [ ] **Step 3: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/i18n/... -count=1`
Expected: FAIL — нет пакета.

- [ ] **Step 4: Реализация**

`backend/internal/platform/i18n/i18n.go`:

```go
// Package i18n — серверные тексты (письма, пуши) из backend/locales (спека §6.9): ICU
// MessageFormat v1, как в клиентах; язык — users.locale, неизвестный — ru. Сообщения
// компилируются при загрузке: битое сообщение — отказ старта, а не письмо с дырой.
package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/kaptinlin/messageformat-go/mf1"
)

const Default = "ru"

var Supported = []string{"ru", "en"}

var ErrUnknownKey = errors.New("i18n: нет такого ключа")

type Catalog struct {
	msgs map[string]map[string]*mf1.CompiledMessage
}

func Load(fsys fs.FS) (*Catalog, error) {
	c := &Catalog{msgs: map[string]map[string]*mf1.CompiledMessage{}}
	for _, loc := range Supported {
		flat, err := Flatten(fsys, loc)
		if err != nil {
			return nil, err
		}
		mf, err := mf1.New(loc, &mf1.MessageFormatOptions{RequireAllArguments: true})
		if err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", loc, err)
		}
		c.msgs[loc] = make(map[string]*mf1.CompiledMessage, len(flat))
		for k, src := range flat {
			m, err := mf.Compile(src)
			if err != nil {
				return nil, fmt.Errorf("i18n: %s.json %s: %w", loc, k, err)
			}
			c.msgs[loc][k] = m
		}
	}
	return c, nil
}

// Flatten — <язык>.json в плоские ключи через точку; значения — только строки.
func Flatten(fsys fs.FS, locale string) (map[string]string, error) {
	data, err := fs.ReadFile(fsys, locale+".json")
	if err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("i18n: %s.json: %w", locale, err)
	}
	out := map[string]string{}
	var walk func(prefix string, node map[string]any) error
	walk = func(prefix string, node map[string]any) error {
		for k, v := range node {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			switch v := v.(type) {
			case string:
				out[key] = v
			case map[string]any:
				if err := walk(key, v); err != nil {
					return err
				}
			default:
				return fmt.Errorf("i18n: %s.json %s: значение не строка", locale, key)
			}
		}
		return nil
	}
	return out, walk("", tree)
}

func (c *Catalog) Keys(locale string) []string {
	return slices.Sorted(maps.Keys(c.msgs[locale]))
}

func (c *Catalog) Text(locale, key string, args map[string]any) (string, error) {
	msgs, ok := c.msgs[locale]
	if !ok {
		msgs = c.msgs[Default]
	}
	m, ok := msgs[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	s, err := m.Format(args)
	if err != nil {
		return "", fmt.Errorf("i18n: %s: %w", key, err)
	}
	return s, nil
}
```

Если `mf1` форматирует `#` с разделителем групп или иначе, чем в тесте (`21 минуту`), — проверить по документации `mf1/docs/api-reference.md`, а не подгонять ожидание; отличие в правилах plural для `ru` (one/few/many) — дефект, сообщить контроллеру.

- [ ] **Step 5: Тесты проходят**

Из `backend/`: `go test ./internal/platform/i18n/... ./locales/... -count=1`
Expected: PASS.

Подсадка бага (страж ключей): временно удалить `mail.footer` из `en.json` → `TestLocalesHaveSameKeys` падает; вернуть.

- [ ] **Step 6: Commit**

```bash
git add backend/locales backend/internal/platform/i18n
git commit -m "Платформа: серверные локали ru/en на ICU MessageFormat, страж одинаковых ключей и аргументов" -- backend/locales backend/internal/platform/i18n
```

---

### Task 15: Почта — отправка SMTP задачей очереди `mail`

**Files:**
- Create: `backend/internal/platform/mail/mail.go`, `smtp.go`, `job.go`
- Create: `backend/internal/platform/mail/smtp_test.go`, `job_test.go`

**Interfaces:**
- Consumes: `queue.Mail` (2/3).
- Produces:
  - `mail.Message{To, Subject, Text, HTML string}`; `mail.Sender interface { Send(ctx, Message) error }`.
  - `mail.SMTPConfig{Addr, From, Username, Password, TLS string}` (`TLS`: `mandatory` | `opportunistic` | `none`); `mail.NewSMTP(c SMTPConfig) (*mail.SMTP, error)`.
  - `mail.Memory` — `Sender` в памяти: `Send`, `Messages() []Message` (тесты модулей).
  - `mail.Composer interface { Compose(ctx, ref uuid.UUID) (Message, error) }`; `mail.ErrSkip` — письмо больше не нужно (код истёк, аккаунт удалён).
  - `mail.Registry`: `mail.NewRegistry()`, `(*Registry).Add(kind string, c Composer) error` (вид — `<модуль>.<письмо>`, без повторов).
  - `mail.SendArgs{V int; Mail string; Ref uuid.UUID}` — `Kind() "mail.send"`, очередь `mail`, 10 попыток; в аргументах — вид письма и id получателя или кода, не адрес (§6.9).
  - `mail.AddWorkers(w *river.Workers, r *Registry, s Sender)`; `mail.SendWorker`.

Сценарий модуля (identity): в своей транзакции `riverClient.InsertTx(ctx, tx, mail.SendArgs{V: 1, Mail: "identity.signup_code", Ref: codeID}, nil)`; при старте воркера — `registry.Add("identity.signup_code", composer)`; composer по `ref` находит адрес и язык, текст — `i18n.Catalog.Text`.

- [ ] **Step 1: Падающие тесты SMTP**

`backend/internal/platform/mail/smtp_test.go`:

```go
package mail_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/mail"
)

// smtpServer — минимальный SMTP-сервер теста: принимает письма и отдаёт DATA в канал.
func smtpServer(t *testing.T) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		write := func(s string) { _, _ = fmt.Fprintf(conn, "%s\r\n", s) }
		write("220 test")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					got <- data.String()
					write("250 ok")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"):
				write("250-test")
				write("250 8BITMIME")
			case cmd == "DATA":
				inData = true
				write("354 go")
			case cmd == "QUIT":
				write("221 bye")
				return
			default:
				write("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSMTPSends(t *testing.T) {
	addr, got := smtpServer(t)
	s, err := mail.NewSMTP(mail.SMTPConfig{Addr: addr, From: "WF <noreply@example.com>", TLS: "none"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Send(ctx, mail.Message{To: "user@example.com", Subject: "Code", Text: "code 123456", HTML: "<b>code 123456</b>"}); err != nil {
		t.Fatal(err)
	}
	data := <-got
	for _, want := range []string{"To: <user@example.com>", "Subject: Code", "code 123456", "text/html", "noreply@example.com"} {
		if !strings.Contains(data, want) {
			t.Errorf("в письме нет %q:\n%s", want, data)
		}
	}
}

func TestNewSMTPValidates(t *testing.T) {
	for name, c := range map[string]mail.SMTPConfig{
		"нет порта":       {Addr: "localhost", From: "a@b.co", TLS: "none"},
		"порт не число":   {Addr: "localhost:x", From: "a@b.co", TLS: "none"},
		"нет отправителя": {Addr: "localhost:25", TLS: "none"},
		"битый From":      {Addr: "localhost:25", From: "nope", TLS: "none"},
		"неизвестный TLS": {Addr: "localhost:25", From: "a@b.co", TLS: "maybe"},
		"пароль без имени": {Addr: "localhost:25", From: "a@b.co", TLS: "none", Password: "x"},
	} {
		if _, err := mail.NewSMTP(c); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
}

func TestMemory(t *testing.T) {
	var m mail.Memory
	_ = m.Send(context.Background(), mail.Message{To: "a@b.co"})
	if msgs := m.Messages(); len(msgs) != 1 || msgs[0].To != "a@b.co" {
		t.Fatalf("%+v", msgs)
	}
}
```

- [ ] **Step 2: Падающие тесты задачи**

`backend/internal/platform/mail/job_test.go`:

```go
package mail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/mail"
)

type composer func(context.Context, uuid.UUID) (mail.Message, error)

func (f composer) Compose(ctx context.Context, ref uuid.UUID) (mail.Message, error) { return f(ctx, ref) }

func TestRegistry(t *testing.T) {
	r := mail.NewRegistry()
	c := composer(func(context.Context, uuid.UUID) (mail.Message, error) { return mail.Message{}, nil })
	if err := r.Add("identity.signup_code", c); err != nil {
		t.Fatal(err)
	}
	if r.Add("identity.signup_code", c) == nil {
		t.Fatal("повтор вида принят")
	}
	for _, bad := range []string{"", "signup", "Identity.code", "identity."} {
		if r.Add(bad, c) == nil {
			t.Errorf("вид %q принят", bad)
		}
	}
}

func TestSendWorker(t *testing.T) {
	ctx := context.Background()
	ref := uuid.New()
	var sent mail.Memory
	r := mail.NewRegistry()
	_ = r.Add("identity.signup_code", composer(func(_ context.Context, got uuid.UUID) (mail.Message, error) {
		if got != ref {
			t.Errorf("ref = %v", got)
		}
		return mail.Message{To: "user@example.com", Subject: "Код"}, nil
	}))
	_ = r.Add("identity.expired", composer(func(context.Context, uuid.UUID) (mail.Message, error) {
		return mail.Message{}, mail.ErrSkip
	}))
	workers := river.NewWorkers()
	mail.AddWorkers(workers, r, &sent)
	w := mail.NewSendWorker(r, &sent)

	if err := w.Work(ctx, &river.Job[mail.SendArgs]{Args: mail.SendArgs{V: 1, Mail: "identity.signup_code", Ref: ref}}); err != nil {
		t.Fatal(err)
	}
	if msgs := sent.Messages(); len(msgs) != 1 || msgs[0].To != "user@example.com" {
		t.Fatalf("отправлено: %+v", msgs)
	}
	// письмо больше не нужно — задача отменяется, а не повторяется
	if err := w.Work(ctx, &river.Job[mail.SendArgs]{Args: mail.SendArgs{V: 1, Mail: "identity.expired", Ref: ref}}); !errors.Is(err, mail.ErrSkip) {
		t.Fatalf("ErrSkip: %v", err)
	}
	// вид не зарегистрирован этим воркером (старый релиз) — обычная ошибка: River повторит
	if err := w.Work(ctx, &river.Job[mail.SendArgs]{Args: mail.SendArgs{V: 1, Mail: "identity.new_kind", Ref: ref}}); err == nil || errors.Is(err, mail.ErrSkip) {
		t.Fatalf("неизвестный вид: %v", err)
	}
	if opts := (mail.SendArgs{}).InsertOpts(); opts.Queue != "mail" || opts.MaxAttempts != 10 {
		t.Fatalf("InsertOpts: %+v", opts)
	}
}
```

- [ ] **Step 3: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/mail/... -count=1`
Expected: FAIL — нет пакета.

- [ ] **Step 4: Реализация**

`backend/internal/platform/mail/mail.go`:

```go
// Package mail — транзакционные письма (спека бэкенда §6.9): отправка только задачей River в
// очереди mail; в аргументах — вид письма и id получателя или кода, адрес находит Composer
// модуля. Dev и прод — одна реализация SMTP (в dev — Mailpit, ./task mail).
package mail

import (
	"context"
	"sync"
)

type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string // необязательная HTML-версия
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Memory — письма в памяти: тесты сценариев модулей.
type Memory struct {
	mu   sync.Mutex
	sent []Message
}

func (m *Memory) Send(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *Memory) Messages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message(nil), m.sent...)
}
```

`backend/internal/platform/mail/smtp.go`:

```go
package mail

import (
	"context"
	"errors"
	"fmt"
	"net"
	netmail "net/mail"
	"strconv"
	"time"

	gomail "github.com/wneessen/go-mail"
)

type SMTPConfig struct {
	Addr     string // host:port
	From     string // «Имя <адрес>» отправителя
	Username string // пусто — без аутентификации (Mailpit)
	Password string
	TLS      string // mandatory | opportunistic | none
}

type SMTP struct {
	host string
	port int
	from string
	user string
	pass string
	tls  gomail.TLSPolicy
}

func NewSMTP(c SMTPConfig) (*SMTP, error) {
	host, portRaw, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return nil, fmt.Errorf("mail: адрес SMTP %q: %w", c.Addr, err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("mail: порт SMTP %q", portRaw)
	}
	if _, err := netmail.ParseAddress(c.From); err != nil {
		return nil, fmt.Errorf("mail: отправитель %q: %w", c.From, err)
	}
	if c.Password != "" && c.Username == "" {
		return nil, errors.New("mail: пароль SMTP без имени пользователя")
	}
	tls := map[string]gomail.TLSPolicy{"mandatory": gomail.TLSMandatory, "opportunistic": gomail.TLSOpportunistic, "none": gomail.NoTLS}
	policy, ok := tls[c.TLS]
	if !ok {
		return nil, fmt.Errorf("mail: TLS %q — нужен mandatory, opportunistic или none", c.TLS)
	}
	return &SMTP{host: host, port: port, from: c.From, user: c.Username, pass: c.Password, tls: policy}, nil
}

// Send — одно соединение на письмо: транзакционных писем немного, пул соединений не нужен.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	msg := gomail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return fmt.Errorf("mail: отправитель: %w", err)
	}
	if err := msg.To(m.To); err != nil {
		return fmt.Errorf("mail: получатель: %w", err)
	}
	msg.Subject(m.Subject)
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}
	msg.SetMessageID()
	msg.SetDate()
	opts := []gomail.Option{gomail.WithPort(s.port), gomail.WithTLSPolicy(s.tls), gomail.WithTimeout(20 * time.Second)}
	if s.user != "" {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthPlain), gomail.WithUsername(s.user), gomail.WithPassword(s.pass))
	}
	c, err := gomail.NewClient(s.host, opts...)
	if err != nil {
		return fmt.Errorf("mail: клиент SMTP: %w", err)
	}
	if err := c.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("mail: отправка: %w", err)
	}
	return nil
}
```

`backend/internal/platform/mail/job.go`:

```go
package mail

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/queue"
)

// ErrSkip — письмо больше не нужно (код истёк, аккаунт удалён): задача отменяется без повторов.
var ErrSkip = errors.New("mail: письмо больше не нужно")

// Composer — письмо одного вида: модуль по ref находит адрес и язык получателя (адресов в
// задаче нет, §6.9) и собирает текст из серверных локалей (i18n).
type Composer interface {
	Compose(ctx context.Context, ref uuid.UUID) (Message, error)
}

var kindRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Registry — виды писем этого воркера; модули добавляют свои при старте.
type Registry struct {
	m map[string]Composer
}

func NewRegistry() *Registry { return &Registry{m: map[string]Composer{}} }

func (r *Registry) Add(kind string, c Composer) error {
	if !kindRe.MatchString(kind) {
		return fmt.Errorf("mail: вид %q — нужен <модуль>.<письмо>", kind)
	}
	if _, dup := r.m[kind]; dup {
		return fmt.Errorf("mail: вид %q уже зарегистрирован", kind)
	}
	r.m[kind] = c
	return nil
}

// SendArgs — задача очереди mail: вид письма и id получателя или кода — не адрес.
type SendArgs struct {
	V    int       `json:"v"`
	Mail string    `json:"mail"`
	Ref  uuid.UUID `json:"ref"`
}

func (SendArgs) Kind() string { return "mail.send" }

func (SendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Mail, MaxAttempts: 10}
}

type SendWorker struct {
	river.WorkerDefaults[SendArgs]
	reg    *Registry
	sender Sender
}

func NewSendWorker(r *Registry, s Sender) *SendWorker { return &SendWorker{reg: r, sender: s} }

func (*SendWorker) Timeout(*river.Job[SendArgs]) time.Duration { return time.Minute }

func (w *SendWorker) Work(ctx context.Context, job *river.Job[SendArgs]) error {
	c, ok := w.reg.m[job.Args.Mail]
	if !ok {
		// воркер прежнего релиза при перекрытии выкатки: повтор возьмёт воркер нового
		return fmt.Errorf("mail: вид %q не зарегистрирован в этом воркере", job.Args.Mail)
	}
	m, err := c.Compose(ctx, job.Args.Ref)
	if errors.Is(err, ErrSkip) {
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	return w.sender.Send(ctx, m)
}

func AddWorkers(w *river.Workers, r *Registry, s Sender) {
	river.AddWorker(w, NewSendWorker(r, s))
}
```

Если `errors.Is(river.JobCancel(ErrSkip), ErrSkip)` в River 0.47 ложно (ошибка отмены не раскрывает причину через `Unwrap`), в тесте проверять отмену способом River (тип ошибки отмены из `rivertype`), а `ErrSkip` — внутри неё; записать в отчёт.

- [ ] **Step 5: Тесты проходят**

Из `backend/`: `go test ./internal/platform/mail/... -count=1`
Expected: PASS.

Подсадка бага: в `Work` убрать ветку `ErrSkip` → падает проверка отмены; вернуть.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/platform/mail
git commit -m "Платформа: почта — SMTP (Mailpit в dev), задача очереди mail без адресов в аргументах, реестр видов писем" -- backend/internal/platform/mail
```

---

### Task 16: Файловое хранилище

**Files:**
- Create: `backend/internal/platform/blob/blob.go`, `fs.go`, `memory.go`, `blob_test.go`

**Interfaces:**
- Consumes: —
- Produces:
  - `blob.Store interface { Put(ctx, key string, r io.Reader, contentType string) error; Get(ctx, key string) (io.ReadCloser, blob.Info, error); Delete(ctx, key string) error }`; `blob.Info{Size int64; ContentType string}`; `blob.ErrNotFound`, `blob.ErrBadKey`.
  - `blob.ValidKey(key string) error` — ключ `[a-z0-9]` и `/ . _ -`, без `..`, без ведущего `/`, до 512 символов.
  - `blob.NewFS(root string) (*blob.FS, error)` (dev: `.tools/blob`); `blob.NewMemory() *blob.Memory` (тесты).

S3-совместимая реализация — в спеке `media` (провайдер выбирается отдельно, §16). `Delete` несуществующего — не ошибка (повтор задачи удаления идемпотентен).

- [ ] **Step 1: Падающие тесты — один набор на обе реализации**

`backend/internal/platform/blob/blob_test.go`:

```go
package blob_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"wf/backend/internal/platform/blob"
)

func stores(t *testing.T) map[string]blob.Store {
	t.Helper()
	fs, err := blob.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]blob.Store{"fs": fs, "memory": blob.NewMemory()}
}

func TestStoreContract(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			key := "avatars/2026/10/a1b2.webp"
			if err := s.Put(ctx, key, strings.NewReader("image-bytes"), "image/webp"); err != nil {
				t.Fatal(err)
			}
			rc, info, err := s.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(rc)
			_ = rc.Close()
			if string(data) != "image-bytes" || info.Size != 11 || info.ContentType != "image/webp" {
				t.Fatalf("%q %+v", data, info)
			}
			// перезапись — новое содержимое целиком
			_ = s.Put(ctx, key, strings.NewReader("v2"), "image/png")
			rc, info, _ = s.Get(ctx, key)
			data, _ = io.ReadAll(rc)
			_ = rc.Close()
			if string(data) != "v2" || info.ContentType != "image/png" {
				t.Fatalf("после перезаписи: %q %+v", data, info)
			}
			if err := s.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Get(ctx, key); !errors.Is(err, blob.ErrNotFound) {
				t.Fatalf("после удаления: %v", err)
			}
			if err := s.Delete(ctx, key); err != nil {
				t.Fatalf("повторное удаление: %v", err)
			}
			for _, bad := range []string{"", "/abs", "../escape", "a/../../b", "UPPER", "a b", "a\\b", strings.Repeat("a", 513)} {
				if err := s.Put(ctx, bad, strings.NewReader("x"), "text/plain"); !errors.Is(err, blob.ErrBadKey) {
					t.Errorf("ключ %q: %v", bad, err)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/blob/... -count=1`
Expected: FAIL — нет пакета.

- [ ] **Step 3: Реализация**

`backend/internal/platform/blob/blob.go`:

```go
// Package blob — хранилище файлов (спека бэкенда §6.9): dev — файловая система, прод —
// S3-совместимое (реализация — в спеке media). Модули видят только Store.
package blob

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
)

var (
	ErrNotFound = errors.New("blob: нет такого файла")
	ErrBadKey   = errors.New("blob: недопустимый ключ")
)

type Info struct {
	Size        int64
	ContentType string
}

type Store interface {
	Put(ctx context.Context, key string, r io.Reader, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, Info, error)
	// Delete — несуществующий файл не ошибка: повтор задачи удаления идемпотентен.
	Delete(ctx context.Context, key string) error
}

var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._\-/]*$`)

const maxKeyLen = 512

// ValidKey — ключ одинаково безопасен для файловой системы и S3: нижний регистр, цифры,
// «/ . _ -», без «..» и ведущего «/».
func ValidKey(key string) error {
	if len(key) > maxKeyLen || !keyRe.MatchString(key) || strings.Contains(key, "..") || strings.Contains(key, "//") {
		return ErrBadKey
	}
	return nil
}
```

`backend/internal/platform/blob/fs.go`:

```go
package blob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FS — файлы в каталоге (dev): данные в data/<ключ>, тип — в meta/<ключ>.json. Запись атомарна:
// временный файл и переименование.
type FS struct{ root string }

func NewFS(root string) (*FS, error) {
	for _, d := range []string{"data", "meta"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			return nil, fmt.Errorf("blob: %w", err)
		}
	}
	return &FS{root: root}, nil
}

func (s *FS) path(kind, key string) string { return filepath.Join(s.root, kind, filepath.FromSlash(key)) }

func (s *FS) Put(_ context.Context, key string, r io.Reader, contentType string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	meta, err := json.Marshal(Info{ContentType: contentType})
	if err != nil {
		return err
	}
	if err := writeAtomic(s.path("data", key), r); err != nil {
		return err
	}
	return writeAtomic(s.path("meta", key)+".json", bytes.NewReader(meta))
}

func (s *FS) Get(_ context.Context, key string) (io.ReadCloser, Info, error) {
	if err := ValidKey(key); err != nil {
		return nil, Info{}, err
	}
	f, err := os.Open(s.path("data", key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Info{}, ErrNotFound
	}
	if err != nil {
		return nil, Info{}, fmt.Errorf("blob: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Info{}, err
	}
	var info Info
	if raw, err := os.ReadFile(s.path("meta", key) + ".json"); err == nil {
		_ = json.Unmarshal(raw, &info)
	}
	info.Size = st.Size()
	return f, info, nil
}

func (s *FS) Delete(_ context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	for _, p := range []string{s.path("data", key), s.path("meta", key) + ".json"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("blob: %w", err)
		}
	}
	return nil
}

func writeAtomic(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}
```

`backend/internal/platform/blob/memory.go`:

```go
package blob

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// Memory — хранилище в памяти: тесты модулей.
type Memory struct {
	mu    sync.Mutex
	files map[string]memFile
}

type memFile struct {
	data []byte
	info Info
}

func NewMemory() *Memory { return &Memory{files: map[string]memFile{}} }

func (m *Memory) Put(_ context.Context, key string, r io.Reader, contentType string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[key] = memFile{data: data, info: Info{Size: int64(len(data)), ContentType: contentType}}
	return nil
}

func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, Info, error) {
	if err := ValidKey(key); err != nil {
		return nil, Info{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[key]
	if !ok {
		return nil, Info{}, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(f.data)), f.info, nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, key)
	return nil
}
```

На Windows `os.Rename` поверх существующего файла работает (Go использует `MoveFileEx` с заменой); если тест перезаписи падает на открытом дескрипторе — убедиться, что тест закрывает `rc` до `Put` (так и написано).

- [ ] **Step 4: Тесты проходят**

Из `backend/`: `go test ./internal/platform/blob/... -count=1`
Expected: PASS.

Подсадка бага: в `ValidKey` убрать проверку `..` → падают ключи `../escape` и `a/../../b`; вернуть.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/platform/blob
git commit -m "Платформа: файловое хранилище — интерфейс, файловая система для dev, память для тестов" -- backend/internal/platform/blob
```

---

### Task 17: Фича-флаги по городам — `403 feature.disabled`

**Files:**
- Modify: `backend/internal/platform/flags/flags.go`
- Create: `backend/internal/platform/flags/flags_test.go` (если файла нет; иначе дописать)

**Interfaces:**
- Consumes: `flagsdb.IsEnabled` (Task 3), `dbtest.NewPoolsAs` (Task 3), `httpx.NewError`, `httpx.CodeFeatureDisabled` (Task 5).
- Produces:
  - `(*flags.Store).Enabled(ctx, key string, cityID uuid.UUID) (bool, error)` — глобально или в городе; нет флага — `flags.ErrUnknownFlag`.
  - `(*flags.Store).Require(ctx, key string, cityID uuid.UUID) error` — `nil` или `flags.ErrDisabled` (ProblemError: 403 `feature.disabled`) или `ErrUnknownFlag`.

- [ ] **Step 1: Падающие тесты**

`backend/internal/platform/flags/flags_test.go`:

```go
package flags_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestEnabledByCity(t *testing.T) {
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	moscow, kazan := uuid.New(), uuid.New()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO feature_flags (key, enabled_globally, enabled_city_ids) VALUES
		('chat', false, ARRAY[$1::uuid]), ('ads', true, '{}'), ('off', false, '{}')`, moscow); err != nil {
		t.Fatal(err)
	}
	s := flags.NewStore(pools.As)
	cases := []struct {
		key  string
		city uuid.UUID
		want bool
	}{
		{"chat", moscow, true}, {"chat", kazan, false}, {"ads", kazan, true}, {"off", moscow, false},
	}
	for _, c := range cases {
		got, err := s.Enabled(ctx, c.key, c.city)
		if err != nil || got != c.want {
			t.Errorf("%s в %v: %v %v", c.key, c.city, got, err)
		}
	}
	if _, err := s.Enabled(ctx, "nope", moscow); !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("неизвестный флаг: %v", err)
	}

	if err := s.Require(ctx, "chat", moscow); err != nil {
		t.Fatal(err)
	}
	err := s.Require(ctx, "chat", kazan)
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !errors.Is(err, flags.ErrDisabled) || !ok || pe.Problem().Status != http.StatusForbidden || pe.Problem().Code != httpx.CodeFeatureDisabled {
		t.Fatalf("выключенный флаг: %v", err)
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Из `backend/`: `go test ./internal/platform/flags/... -count=1`
Expected: FAIL — нет `Enabled`, `Require`, `ErrDisabled`.

- [ ] **Step 3: Реализация**

В `backend/internal/platform/flags/flags.go` (импорты `net/http`, `github.com/google/uuid`, `wf/backend/internal/platform/httpx`):

```go
// ErrDisabled — функция выключена в городе: клиенту 403 feature.disabled (спека §6.9).
var ErrDisabled = httpx.NewError(http.StatusForbidden, httpx.CodeFeatureDisabled)

// Enabled — флаг включён глобально или в городе.
func (s *Store) Enabled(ctx context.Context, key string, cityID uuid.UUID) (bool, error) {
	on, err := s.q.IsEnabled(ctx, flagsdb.IsEnabledParams{Key: key, CityID: cityID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUnknownFlag
	}
	return on, err
}

// Require — для app/ модуля: выключено — ErrDisabled (403), флага нет — ErrUnknownFlag (500:
// флаг в коде без строки в базе — ошибка выкатки).
func (s *Store) Require(ctx context.Context, key string, cityID uuid.UUID) error {
	on, err := s.Enabled(ctx, key, cityID)
	if err != nil {
		return err
	}
	if !on {
		return ErrDisabled
	}
	return nil
}
```

Тип результата `IsEnabled` — как сгенерировал sqlc (`bool`); если `*bool` или иное — привести, записать в отчёт.

- [ ] **Step 4: Тест проходит**

Из `backend/`: `go test ./internal/platform/flags/... ./internal/platform/httpx/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/platform/flags
git commit -m "Фича-флаги: включение по городу и 403 feature.disabled" -- backend/internal/platform/flags
```

---

### Task 18: Курсор keyset-пагинации

**Files:**
- Create: `backend/internal/platform/page/page.go`, `backend/internal/platform/page/page_test.go`

**Interfaces:**
- Consumes: `httpx.Problem`, `httpx.FieldError`, `httpx.CodeValidationFailed` (Task 5).
- Produces:
  - `page.Cursor{CreatedAt time.Time; ID uuid.UUID}`; `page.Encode(c Cursor) string`; `page.Decode(s string) (Cursor, error)`; `page.ErrBadCursor` — `httpx.ProblemError`: 400 `validation.failed` с полем `query.cursor`, правило `format`.
  - `page.DefaultLimit = 20`, `page.MaxLimit = 100`; `page.Limit(requested *int) int`.

Курсор непрозрачный (§6.9): клиент не собирает его сам и не полагается на формат. Время — микросекунды (точность `timestamptz`). Запрос модуля: `WHERE (created_at, id) < (@created_at, @id) ORDER BY created_at DESC, id DESC LIMIT @limit + 1` — лишняя строка говорит, что есть следующая страница.

- [ ] **Step 1: Падающие тесты**

`backend/internal/platform/page/page_test.go`:

```go
package page_test

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/page"
)

func TestCursorRoundTrip(t *testing.T) {
	c := page.Cursor{CreatedAt: time.Date(2026, 10, 2, 12, 0, 0, 123456789, time.UTC), ID: uuid.New()}
	s := page.Encode(c)
	if strings.ContainsAny(s, "+/=") {
		t.Fatalf("курсор не годится для query без экранирования: %q", s)
	}
	got, err := page.Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	// точность базы — микросекунды: наносекунды курсор не переносит
	if !got.CreatedAt.Equal(c.CreatedAt.Truncate(time.Microsecond)) || got.ID != c.ID {
		t.Fatalf("%+v != %+v", got, c)
	}
}

func TestDecodeRejects(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	for name, s := range map[string]string{
		"мусор":        "%%%",
		"не JSON":      enc([]byte("nope")),
		"чужая версия": enc([]byte(`{"v":2,"t":1,"id":"` + uuid.NewString() + `"}`)),
		"без id":       enc([]byte(`{"v":1,"t":1}`)),
		"пусто":        "",
	} {
		_, err := page.Decode(s)
		if !errors.Is(err, page.ErrBadCursor) {
			t.Errorf("%s: %v", name, err)
		}
	}
	pe, ok := errors.AsType[httpx.ProblemError](page.ErrBadCursor)
	if !ok {
		t.Fatal("ErrBadCursor не ProblemError")
	}
	p := pe.Problem()
	if p.Status != http.StatusBadRequest || p.Code != httpx.CodeValidationFailed ||
		len(p.Errors) != 1 || p.Errors[0] != (httpx.FieldError{Field: "query.cursor", Code: "format"}) {
		t.Fatalf("%+v", p)
	}
}

func TestLimit(t *testing.T) {
	n := func(v int) *int { return &v }
	cases := []struct {
		in   *int
		want int
	}{{nil, 20}, {n(0), 20}, {n(-5), 20}, {n(1), 1}, {n(100), 100}, {n(500), 100}}
	for _, c := range cases {
		if got := page.Limit(c.in); got != c.want {
			t.Errorf("Limit(%v) = %d, нужно %d", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/page/... -count=1`
Expected: FAIL — нет пакета.

- [ ] **Step 3: Реализация**

`backend/internal/platform/page/page.go`:

```go
// Package page — keyset-пагинация по (created_at, id) (спека бэкенда §6.9): непрозрачный
// курсор, limit по умолчанию 20, максимум 100.
package page

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/httpx"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
	version      = 1
)

// Cursor — последняя строка страницы.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type wire struct {
	V  int       `json:"v"`
	T  int64     `json:"t"` // created_at, микросекунды Unix
	ID uuid.UUID `json:"id"`
}

type badCursor struct{}

func (badCursor) Error() string { return "page: недействительный курсор" }

func (badCursor) Problem() httpx.Problem {
	return httpx.Problem{Status: http.StatusBadRequest, Code: httpx.CodeValidationFailed,
		Errors: []httpx.FieldError{{Field: "query.cursor", Code: "format"}}}
}

// ErrBadCursor — курсор не разбирается: 400 validation.failed по полю query.cursor.
var ErrBadCursor httpx.ProblemError = badCursor{}

func Encode(c Cursor) string {
	b, _ := json.Marshal(wire{V: version, T: c.CreatedAt.UnixMicro(), ID: c.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func Decode(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) == 0 {
		return Cursor{}, ErrBadCursor
	}
	var w wire
	if json.Unmarshal(raw, &w) != nil || w.V != version || w.ID == uuid.Nil {
		return Cursor{}, ErrBadCursor
	}
	return Cursor{CreatedAt: time.UnixMicro(w.T).UTC(), ID: w.ID}, nil
}

// Limit — размер страницы из запроса: нет или ≤ 0 — 20, больше 100 — 100.
func Limit(requested *int) int {
	switch {
	case requested == nil || *requested <= 0:
		return DefaultLimit
	case *requested > MaxLimit:
		return MaxLimit
	}
	return *requested
}
```

`errors.Is(err, page.ErrBadCursor)` работает: `badCursor{}` — сравнимое значение.

- [ ] **Step 4: Тесты проходят**

Из `backend/`: `go test ./internal/platform/page/... ./internal/platform/httpx/... -count=1`
Expected: PASS (страж литералов: `Code: httpx.CodeValidationFailed` — константа).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/platform/page
git commit -m "Платформа: непрозрачный курсор keyset-пагинации и лимит страницы 20/100" -- backend/internal/platform/page
```

---

## Фаза E. Сборка и документы

### Task 19: Сборка конвейеров, конфиг, бинарники, `/readyz`, деплой

**Files:**
- Modify: `backend/internal/platform/config/config.go`, `backend/internal/platform/config/config_test.go` (создать, если нет)
- Modify: `backend/internal/platform/health/health.go`, `backend/internal/platform/health/health_test.go`
- Modify: `backend/internal/platform/server/server.go`
- Modify: `backend/internal/httpapi/public/handler.go`, `backend/internal/httpapi/public/handler_test.go`
- Modify: `backend/internal/httpapi/admin/handler.go`, `backend/internal/httpapi/admin/handler_test.go`
- Modify: `backend/cmd/api/main.go`, `backend/cmd/admin-api/main.go`, `backend/cmd/worker/main.go`, `backend/cmd/worker/main_test.go`
- Modify: `backend/.env.example`; локальный `backend/.env` (скриптом)
- Modify: `render.yaml`, `.github/workflows/backend.yml`

**Interfaces:**
- Consumes: всё из Task 2–17.
- Produces:
  - `config.HTTP.RequestTimeout` (`REQUEST_TIMEOUT`, 15 с); `config.Auth{JWTSeeds []string}` (`JWT_SEEDS`, required); `config.Peer{TrustedProxies, BFFNets []netip.Prefix; BFFSecrets []string}` (`TRUSTED_PROXIES`, `BFF_NETS`, `BFF_SECRETS`); `config.Humancheck{Keys []string; TTL time.Duration; MaxNumber int64}` (`HUMANCHECK_KEYS` required, `HUMANCHECK_TTL` 5m, `HUMANCHECK_MAX_NUMBER` 100000); `config.API` + `Auth`, `Peer`, `RateLimits string` (`RATE_LIMITS`), `Humancheck`; `config.Admin` + `Peer`, `RateLimits`; `config.Mail{SMTPAddr, From, Username, Password, TLS}` (`MAIL_SMTP_ADDR`, `MAIL_FROM` required; `MAIL_SMTP_USERNAME`, `MAIL_SMTP_PASSWORD`; `MAIL_SMTP_TLS` = mandatory); `config.Worker` + `Mail`.
  - `public.Options{Docs bool; RequestTimeout time.Duration; Peer peer.Config; DB *pgxpool.Pool; Tokens auth.Verifier; Sessions auth.SessionLoader; Limiter ratelimit.Limiter; RateRules ratelimit.Rules}` — `NewHandler` отказывает, если `DB`, `Tokens`, `Sessions`, `Limiter` или `RateRules` не заданы или в контракте есть класс `x-rate-limit` без правила.
  - `admin.Options{Docs; RequestTimeout; Peer; DB; Limiter; RateRules}` — то же без токенов.
  - `health.Cached(ready func(context.Context) error, ttl time.Duration, now func() time.Time) func(context.Context) error`.

Конвейер публичного API (порядок — спека §6.1): `peer` → `LimitBody` → `Timeout` → `Routes` → `auth.Middleware` → `ValidateRequests{Authenticated: auth.Authenticated, WWWAuthenticate: "Bearer"}` → `ratelimit.Middleware` → `humancheck.Middleware` → `idempotency.Middleware` → strict-хендлер. Админка: `peer` → `LimitBody` → `Timeout` → `Routes` → (вход сотрудников — спека identity) → `ValidateRequests{}` → `ratelimit` → `idempotency`.

- [ ] **Step 1: Падающие тесты конфига**

`backend/internal/platform/config/config_test.go` (создать или дописать):

```go
package config_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/config"
)

func TestAPIConfig(t *testing.T) {
	env := []string{
		"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://x",
		"API_JWT_SEEDS=a,b", "API_HUMANCHECK_KEYS=k",
		"API_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12",
		"API_BFF_NETS=192.0.2.0/24", "API_BFF_SECRETS=s1,s2",
		"API_RATE_LIMITS=auth.ip=10/1m",
	}
	c, err := config.Load[config.API]("API_", env)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Auth.JWTSeeds) != 2 || c.Humancheck.TTL != 5*time.Minute || c.Humancheck.MaxNumber != 100000 ||
		c.HTTP.RequestTimeout != 15*time.Second || c.RateLimits != "auth.ip=10/1m" {
		t.Fatalf("%+v", c)
	}
	if len(c.Peer.TrustedProxies) != 2 || c.Peer.TrustedProxies[1] != netip.MustParsePrefix("172.16.0.0/12") ||
		len(c.Peer.BFFSecrets) != 2 {
		t.Fatalf("peer: %+v", c.Peer)
	}
}

func TestAPIConfigRequiresKeys(t *testing.T) {
	_, err := config.Load[config.API]("API_", []string{"API_HTTP_ADDR=x", "API_DATABASE_URL=y"})
	if err == nil || !strings.Contains(err.Error(), "API_JWT_SEEDS") || !strings.Contains(err.Error(), "API_HUMANCHECK_KEYS") {
		t.Fatalf("err = %v", err)
	}
}

func TestWorkerMailConfig(t *testing.T) {
	c, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x",
		"WORKER_MAIL_SMTP_ADDR=127.0.0.1:11025", "WORKER_MAIL_FROM=WF <noreply@wf.local>"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Mail.TLS != "mandatory" || c.Mail.SMTPAddr != "127.0.0.1:11025" {
		t.Fatalf("%+v", c.Mail)
	}
	if _, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x"}); err == nil ||
		!strings.Contains(err.Error(), "WORKER_MAIL_SMTP_ADDR") {
		t.Fatalf("без почты: %v", err)
	}
}
```

- [ ] **Step 2: Падающие тесты `/readyz`**

В `backend/internal/platform/health/health_test.go` дописать:

```go
// Открытый вопрос плана 2/3: /readyz публичный — поток запросов не должен превращаться в поток
// пингов базы. Результат кэшируется на ttl, одновременные пробы схлопываются.
func TestCachedReady(t *testing.T) {
	var calls atomic.Int32
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	ready := health.Cached(func(context.Context) error { calls.Add(1); time.Sleep(20 * time.Millisecond); return nil }, time.Second, clock)
	h := health.Handler(ready, next)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if rec := do(h, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
				t.Errorf("status = %d", rec.Code)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("пингов %d на 50 проб", calls.Load())
	}
	now = now.Add(time.Second + time.Millisecond)
	do(h, http.MethodGet, "/readyz")
	if calls.Load() != 2 {
		t.Fatalf("после ttl пингов %d, нужно 2", calls.Load())
	}
}

func TestReadyzExactBodiesAndHead(t *testing.T) {
	down := health.Handler(func(context.Context) error { return errors.New("x") }, next)
	if rec := do(down, http.MethodGet, "/readyz"); rec.Body.String() != `{"status":"unavailable"}` {
		t.Fatalf("тело 503: %q", rec.Body.String())
	}
	up := health.Handler(func(context.Context) error { return nil }, next)
	if rec := do(up, http.MethodHead, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("HEAD /readyz: %d", rec.Code)
	}
}
```

(импорты `sync`, `sync/atomic`, `time`; переменная `clock` не должна конфликтовать с пакетом — пакет `clock` здесь не импортируется).

- [ ] **Step 3: Падающие тесты сборки хендлеров**

В `backend/internal/httpapi/public/handler_test.go`:
1. Хелпер опций (все прежние вызовы `public.NewHandler(…, public.Options{})` заменить на `testOptions(t)`, а `public.Options{Docs: true}` — на `o := testOptions(t); o.Docs = true`):

```go
// testOptions — рабочий набор зависимостей: лимиты пропускают всё, сессий нет, ключ токенов —
// новый на каждый тест, база — чистая (идемпотентность).
func testOptions(t *testing.T) public.Options {
	t.Helper()
	seed, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	k, err := token.ParseKeys([]string{seed})
	if err != nil {
		t.Fatal(err)
	}
	return public.Options{
		DB:        dbtest.NewPool(t),
		Tokens:    token.NewIssuer(k, clock.System),
		Sessions:  auth.NoSessions,
		Limiter:   ratelimit.Unlimited,
		RateRules: ratelimit.DefaultRules(),
	}
}
```

2. Новые тесты:

```go
// Конвейер собран: слой аутентификации стоит и на анонимной операции (битый токен — 401),
// rate limit — до хендлера.
func TestPipelineWired(t *testing.T) {
	o := testOptions(t)
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/health", nil)
	r.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"auth.unauthenticated"`) {
		t.Fatalf("битый токен: %d %s", rec.Code, rec.Body.String())
	}

	o.Limiter = denyAll{}
	h, _ = public.NewHandler(slog.New(slog.DiscardHandler), o)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/health", nil))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("лимит: %d %v", rec.Code, rec.Header())
	}
}

type denyAll struct{}

func (denyAll) Allow(context.Context, string, ratelimit.Policy) (ratelimit.Result, error) {
	return ratelimit.Result{RetryAfter: 5 * time.Second}, nil
}
func (denyAll) Add(context.Context, string, ratelimit.Policy) error            { return nil }
func (denyAll) Over(context.Context, string, ratelimit.Policy) (bool, error)   { return true, nil }

// Без зависимостей хендлер не собирается: забытый в main лимитер или токены — отказ старта.
func TestNewHandlerRequiresDependencies(t *testing.T) {
	full := testOptions(t)
	for name, mutate := range map[string]func(*public.Options){
		"без базы":     func(o *public.Options) { o.DB = nil },
		"без токенов":  func(o *public.Options) { o.Tokens = nil },
		"без сессий":   func(o *public.Options) { o.Sessions = nil },
		"без лимитера": func(o *public.Options) { o.Limiter = nil },
		"без правил":   func(o *public.Options) { o.RateRules = nil },
		"класс x-rate-limit без правила": func(o *public.Options) {
			o.RateRules = ratelimit.Rules{"auth": ratelimit.DefaultRules()["auth"]} // нет default
		},
	} {
		o := full
		mutate(&o)
		if _, err := public.NewHandler(slog.New(slog.DiscardHandler), o); err == nil {
			t.Errorf("%s: собрано", name)
		}
	}
}

// Страж: каждый класс x-rate-limit контракта описан в правилах по умолчанию.
func TestRateLimitClassesKnown(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range ratelimit.UnknownClasses(spec, ratelimit.DefaultRules()) {
		t.Error(v)
	}
}
```

Последний случай «класс без правила»: в правилах нет `default`, а им пользуется каждая операция без `x-rate-limit` — `NewHandler` обязан проверить и наличие `default` (`DefaultClass`).

То же для `backend/internal/httpapi/admin/handler_test.go`: хелпер `testOptions` без токенов и сессий, тесты «без зависимостей» и `TestRateLimitClassesKnown`.

- [ ] **Step 4: Падающий тест воркера с почтой**

В `backend/cmd/worker/main_test.go` все списки окружения дополнить почтой (общая переменная):

```go
// mailEnv — почта воркера обязательна; в тестах — адрес, на котором никто не слушает: письма не
// отправляются, пока модули не поставят задачу.
var mailEnv = []string{"WORKER_MAIL_SMTP_ADDR=127.0.0.1:1", "WORKER_MAIL_FROM=WF <noreply@wf.local>", "WORKER_MAIL_SMTP_TLS=none"}
```

(`append(mailEnv, "WORKER_DATABASE_URL="+url)` и т. п.) и тест:

```go
func TestRunRejectsInvalidMailConfig(t *testing.T) {
	env := []string{"WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1",
		"WORKER_MAIL_SMTP_ADDR=nohost", "WORKER_MAIL_FROM=WF <noreply@wf.local>"}
	err := run(context.Background(), nil, env, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "mail") {
		t.Fatalf("err = %v — битый адрес SMTP должен остановить старт до подключения к базе", err)
	}
}
```

- [ ] **Step 5: Убедиться, что тесты падают**

Из `backend/`: `go test ./internal/platform/config/... ./internal/platform/health/... ./internal/httpapi/... ./cmd/... -count=1`
Expected: FAIL — нет полей конфига, `health.Cached`, новых `Options`.

- [ ] **Step 6: Конфиг**

В `backend/internal/platform/config/config.go` (импорт `net/netip`):

```go
type HTTP struct {
	Addr            string        `env:"HTTP_ADDR,required"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
	// RequestTimeout — крайний срок запроса (спека §6.1): запросы к базе прерываются по нему.
	RequestTimeout time.Duration `env:"REQUEST_TIMEOUT" envDefault:"15s"`
	// DocsEnabled — … (как было)
	DocsEnabled bool `env:"DOCS_ENABLED" envDefault:"false"`
}

// Auth — access-токены публичного API (спека §6.2).
type Auth struct {
	// JWTSeeds — сиды Ed25519 (base64, 32 байта) через запятую: первый подписывает, все
	// проверяют. Ротация: новый ключ первым, старый — следом на время жизни access (10 мин).
	JWTSeeds []string `env:"JWT_SEEDS,required" envSeparator:","`
}

// Peer — кому верить заголовкам IP (спека §8.4).
type Peer struct {
	TrustedProxies []netip.Prefix `env:"TRUSTED_PROXIES" envSeparator:","` // балансировщик хостинга
	BFFNets        []netip.Prefix `env:"BFF_NETS" envSeparator:","`        // адреса BFF (Next.js)
	BFFSecrets     []string       `env:"BFF_SECRETS" envSeparator:","`     // секрет BFF ↔ API, ≥ 32 символов
}

// Humancheck — PoW антибота (спека §6.7).
type Humancheck struct {
	Keys      []string      `env:"HUMANCHECK_KEYS,required" envSeparator:","` // HMAC, base64, ≥ 32 байт
	TTL       time.Duration `env:"HUMANCHECK_TTL" envDefault:"5m"`
	MaxNumber int64         `env:"HUMANCHECK_MAX_NUMBER" envDefault:"100000"`
}

type API struct {
	Log        Log
	HTTP       HTTP
	DB         DB
	Auth       Auth
	Peer       Peer
	RateLimits string `env:"RATE_LIMITS"` // переопределения ratelimit.DefaultRules: auth.ip=30/1m:10,…
	Humancheck Humancheck
}

// Admin — вход сотрудников (сессия, TOTP, аллоулист) добавит спека identity.
type Admin struct {
	Log        Log
	HTTP       HTTP
	DB         DB
	Peer       Peer
	RateLimits string `env:"RATE_LIMITS"`
}

// Mail — SMTP транзакционных писем (спека §6.9); в dev — Mailpit.
type Mail struct {
	SMTPAddr string `env:"MAIL_SMTP_ADDR,required"`
	From     string `env:"MAIL_FROM,required"`
	Username string `env:"MAIL_SMTP_USERNAME"`
	Password string `env:"MAIL_SMTP_PASSWORD"`
	TLS      string `env:"MAIL_SMTP_TLS" envDefault:"mandatory"` // mandatory | opportunistic | none
}
```

и в `Worker` поле `Mail Mail`. Если `caarlos0/env` не разбирает срез `netip.Prefix` (тест `TestAPIConfig` упадёт на разборе) — хранить `[]string` и разбирать `netip.ParsePrefix` в `cmd/api` до старта, записать в отчёт.

- [ ] **Step 7: Кэш `/readyz`**

В `backend/internal/platform/health/health.go` (импорты `sync`, `golang.org/x/sync/singleflight`):

```go
// Cached — результат ready не чаще раза в ttl, одновременные пробы ждут одну проверку: поток
// запросов к открытому /readyz не превращается в поток пингов базы. Ошибка тоже кэшируется на
// ttl. Проверка не обрывается отменой одной из проб, но ограничена readyTimeout.
func Cached(ready func(context.Context) error, ttl time.Duration, now func() time.Time) func(context.Context) error {
	var (
		mu      sync.Mutex
		last    error
		expires time.Time
		group   singleflight.Group
	)
	return func(ctx context.Context) error {
		mu.Lock()
		if now().Before(expires) {
			err := last
			mu.Unlock()
			return err
		}
		mu.Unlock()
		_, err, _ := group.Do("ready", func() (any, error) {
			// проба могла опоздать к общей проверке, которая только что закончилась, — её
			// результат уже в кэше
			mu.Lock()
			if now().Before(expires) {
				err := last
				mu.Unlock()
				return nil, err
			}
			mu.Unlock()
			c, cancel := context.WithTimeout(context.WithoutCancel(ctx), readyTimeout)
			defer cancel()
			err := ready(c)
			mu.Lock()
			last, expires = err, now().Add(ttl)
			mu.Unlock()
			return nil, err
		})
		return err
	}
}
```

В `backend/internal/platform/server/server.go`: `h = health.Handler(health.Cached(pool.Ping, time.Second, time.Now), h)` (импорт `time`).

- [ ] **Step 8: Хендлеры**

`backend/internal/httpapi/public/handler.go` — `Options` и сборка:

```go
// Options — зависимости публичного API; cmd/api собирает их из конфига.
type Options struct {
	Docs           bool          // Swagger UI и контракт на /docs (config.HTTP.DocsEnabled)
	RequestTimeout time.Duration // крайний срок запроса; 0 — 15 с
	Peer           peer.Config
	DB             *pgxpool.Pool // ключи идемпотентности
	Tokens         auth.Verifier
	Sessions       auth.SessionLoader
	Limiter        ratelimit.Limiter
	RateRules      ratelimit.Rules
}

const defaultRequestTimeout = 15 * time.Second

func NewHandler(log *slog.Logger, o Options) (http.Handler, error) {
	if o.DB == nil || o.Tokens == nil || o.Sessions == nil || o.Limiter == nil || o.RateRules == nil {
		return nil, errors.New("public: не заданы зависимости (DB, Tokens, Sessions, Limiter, RateRules)")
	}
	if _, ok := o.RateRules[ratelimit.DefaultClass]; !ok {
		return nil, errors.New("public: в правилах rate limit нет класса default")
	}
	if err := o.Peer.Validate(); err != nil {
		return nil, err
	}
	spec, err := oapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("public: контракт: %w", err)
	}
	if unknown := ratelimit.UnknownClasses(spec, o.RateRules); len(unknown) > 0 {
		return nil, fmt.Errorf("public: классы rate limit без правил: %v", unknown)
	}
	title := spec.Info.Title
	routes, err := httpx.Routes(spec)
	if err != nil {
		return nil, err
	}
	timeout := o.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	requestErr := httpx.RequestErrorHandler(log)
	strict := oapi.NewStrictHandlerWithOptions(Server{}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  requestErr,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	// конвейер — спека §6.1
	router := httpx.NewRouter(log,
		peer.Middleware(o.Peer),
		httpx.LimitBody(httpx.MaxBodyBytes),
		httpx.Timeout(timeout),
		routes,
		auth.Middleware(o.Tokens, o.Sessions, log),
		httpx.ValidateRequests(httpx.ValidateOptions{Authenticated: auth.Authenticated, WWWAuthenticate: "Bearer"}),
		ratelimit.Middleware(o.Limiter, o.RateRules, log),
		humancheck.Middleware(),
		idempotency.Middleware(o.DB, idempotency.Config{}, log),
	)
	if o.Docs {
		// … как было: apidocs.Mount
	}
	return oapi.HandlerWithOptions(strict, oapi.ChiServerOptions{BaseRouter: router, ErrorHandlerFunc: requestErr}), nil
}
```

`backend/internal/httpapi/admin/handler.go` — `Options{Docs, RequestTimeout, Peer, DB, Limiter, RateRules}`, те же проверки (без токенов и сессий), конвейер:

```go
	router := httpx.NewRouter(log,
		peer.Middleware(o.Peer),
		httpx.LimitBody(httpx.MaxBodyBytes),
		httpx.Timeout(timeout),
		routes,
		// вход сотрудников (cookie-сессия, TOTP) — спека identity; до неё операции с security — 401
		httpx.ValidateRequests(httpx.ValidateOptions{}),
		ratelimit.Middleware(o.Limiter, o.RateRules, log),
		idempotency.Middleware(o.DB, idempotency.Config{}, log),
	)
```

- [ ] **Step 9: Бинарники**

`backend/cmd/api/main.go` — внутри `run` после загрузки конфига:

```go
	keys, err := token.ParseKeys(cfg.Auth.JWTSeeds)
	if err != nil {
		return err
	}
	rules, err := ratelimit.ParseRules(cfg.RateLimits, ratelimit.DefaultRules())
	if err != nil {
		return err
	}
	pc := peer.Config{TrustedProxies: cfg.Peer.TrustedProxies, BFFNets: cfg.Peer.BFFNets, BFFSecrets: cfg.Peer.BFFSecrets}
	if err := pc.Validate(); err != nil {
		return err
	}
	return server.RunHTTP(ctx, "api", server.HTTPConfig{Log: cfg.Log, HTTP: cfg.HTTP, DB: cfg.DB}, logOut,
		func(log *slog.Logger, pool *pgxpool.Pool) (http.Handler, error) {
			// ключи PoW проверяются на старте; сценарии identity получат этот же PoW
			if _, err := humancheck.NewPoW(pool, clock.System, humancheck.PoWConfig{
				Keys: cfg.Humancheck.Keys, TTL: cfg.Humancheck.TTL, MaxNumber: cfg.Humancheck.MaxNumber,
			}); err != nil {
				return nil, err
			}
			return public.NewHandler(log, public.Options{
				Docs:           cfg.HTTP.DocsEnabled,
				RequestTimeout: cfg.HTTP.RequestTimeout,
				Peer:           pc,
				DB:             pool,
				Tokens:         token.NewIssuer(keys, clock.System),
				// сессий нет до спеки identity: любой токен — 401; identity подставит свой загрузчик
				Sessions:  auth.NewCachedLoader(auth.NoSessions, 5*time.Second, clock.System, 100_000),
				Limiter:   ratelimit.NewPG(pool, clock.System),
				RateRules: rules,
			})
		})
```

`server.HTTPConfig(cfg)` — преобразование типа больше не компилируется (у `config.API` новые поля): собирать `server.HTTPConfig{Log, HTTP, DB}` явно — и в `cmd/admin-api`.

`backend/cmd/admin-api/main.go` — так же: `ParseRules(cfg.RateLimits, …)`, `peer.Config{TrustedProxies: cfg.Peer.TrustedProxies}` (BFF у админки нет), `admin.Options{Docs, RequestTimeout, Peer, DB: pool, Limiter: ratelimit.NewPG(pool, clock.System), RateRules}`.

`backend/cmd/worker/main.go`:
1. После `validateRelay` — до подключения к базе:

```go
	sender, err := mail.NewSMTP(mail.SMTPConfig{Addr: cfg.Mail.SMTPAddr, From: cfg.Mail.From,
		Username: cfg.Mail.Username, Password: cfg.Mail.Password, TLS: cfg.Mail.TLS})
	if err != nil {
		return err
	}
```

2. Регистрация воркеров и периодических задач:

```go
	workers := queue.NewWorkers()
	events.AddWorkers(workers, pool, reg)
	river.AddWorker(workers, ratelimit.NewCleanupWorker(pool, clock.System))
	river.AddWorker(workers, humancheck.NewCleanupWorker(pool, clock.System))
	river.AddWorker(workers, idempotency.NewCleanupWorker(pool))
	mail.AddWorkers(workers, mails(), sender)
	periodic := []*river.PeriodicJob{events.CleanupJob(), ratelimit.CleanupJob(), humancheck.CleanupJob(), idempotency.CleanupJob()}
	client, err := queue.NewClient(pool, workers, cfg.Queues, periodic, log)
```

3. Рядом с `subscriptions()`:

```go
// mails — виды писем модулей. Пусто, пока нет модулей: спека identity добавит письма с кодами
// (mail.Composer по id кода).
func mails() *mail.Registry {
	return mail.NewRegistry()
}
```

- [ ] **Step 10: Окружение**

`backend/.env.example` — после блока `API_DOCS_ENABLED`:

```bash
# Крайний срок запроса (по умолчанию 15s)
# API_REQUEST_TIMEOUT=15s
# Сиды Ed25519 access-токенов (base64, 32 байта) через запятую: первый подписывает, все проверяют.
# Сгенерировать: openssl rand -base64 32
API_JWT_SEEDS=<base64-32-байта>
# Ключи HMAC задач антибота (PoW по протоколу ALTCHA), base64 ≥ 32 байт, через запятую
API_HUMANCHECK_KEYS=<base64-32-байта>
# Срок задачи и сложность перебора (по умолчанию 5m и 100000)
# API_HUMANCHECK_TTL=5m
# API_HUMANCHECK_MAX_NUMBER=100000
# Прокси, которым верим X-Forwarded-For (CIDR через запятую); локально прокси нет
API_TRUSTED_PROXIES=
# BFF (Next.js): адреса и общий секрет (≥ 32 символов) — от него API принимает X-WF-Client-IP
API_BFF_NETS=127.0.0.1/32
API_BFF_SECRETS=<секрет-не-короче-32-символов>
# Переопределения лимитов: <класс>.<ip|user|device>=<лимит>/<период>[:<всплеск>] или =off
API_RATE_LIMITS=
```

после блока `ADMIN_DOCS_ENABLED`:

```bash
ADMIN_TRUSTED_PROXIES=
ADMIN_RATE_LIMITS=
```

после блока `WORKER_RELAY_POLL`:

```bash
# Почта: Mailpit (./task mail, UI http://127.0.0.1:18025); в проде — SMTP провайдера с TLS
WORKER_MAIL_SMTP_ADDR=127.0.0.1:11025
WORKER_MAIL_FROM=WhereFootball <noreply@wherefootball.local>
WORKER_MAIL_SMTP_TLS=none
# WORKER_MAIL_SMTP_USERNAME=
# WORKER_MAIL_SMTP_PASSWORD=
```

Локальный `backend/.env` — скриптом (значения не выводить), файл `<scratchpad>/env-3of3.sh`:

```bash
#!/usr/bin/env bash
# Дописать в backend/.env ключи плана 3/3, которых там ещё нет. Значения не выводятся.
set -euo pipefail
f=backend/.env
add() { grep -q "^$1=" "$f" || printf '%s=%s\n' "$1" "$2" >> "$f"; }
add API_JWT_SEEDS "$(openssl rand -base64 32)"
add API_HUMANCHECK_KEYS "$(openssl rand -base64 32)"
add API_TRUSTED_PROXIES ""
add API_BFF_NETS "127.0.0.1/32"
add API_BFF_SECRETS "$(openssl rand -base64 32)"
add API_RATE_LIMITS ""
add ADMIN_TRUSTED_PROXIES ""
add ADMIN_RATE_LIMITS ""
add WORKER_MAIL_SMTP_ADDR "127.0.0.1:11025"
add WORKER_MAIL_FROM "WhereFootball <noreply@wherefootball.local>"
add WORKER_MAIL_SMTP_TLS "none"
echo "backend/.env: ключи плана 3/3 на месте"
```

Из корня: `bash <scratchpad>/env-3of3.sh`. Секрет BFF понадобится и вебу (`apps/web/.env.local`) — со спекой веба; сейчас веб в API не ходит.

- [ ] **Step 11: Render и CI**

`render.yaml` — в `envVars` сервиса `wf-api` после `API_LOG_FORMAT`:

```yaml
      # Ключи access-токенов (сиды Ed25519) и задач антибота: Render генерирует base64 от 256 бит
      # при синхронизации Blueprint. Ротация — новый ключ первым через запятую (docs/deploy-dev.md).
      - key: API_JWT_SEEDS
        generateValue: true
      - key: API_HUMANCHECK_KEYS
        generateValue: true
      # Балансировщик Render — из частных сетей: только им верим X-Forwarded-For
      - key: API_TRUSTED_PROXIES
        value: 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
```

`.github/workflows/backend.yml`, задание `image`, шаг `migrate-then-api`: в `docker run` добавить

```bash
            -e API_JWT_SEEDS="$(openssl rand -base64 32)" \
            -e API_HUMANCHECK_KEYS="$(openssl rand -base64 32)" \
```

и закрыть отложенное из 2/3 — проверки `/readyz` и `/docs` при падении тоже печатают логи контейнера:

```bash
              curl -fsS http://127.0.0.1:18080/readyz || { docker logs wf-api; exit 1; }
              curl -fsS -o /dev/null http://127.0.0.1:18080/docs/openapi.json || { docker logs wf-api; exit 1; }
```

Комментарий «готовность: API видит базу под своей ролью (права из grants.sql)» заменить на «готовность: API подключается к базе под своей ролью (пинг; права проверяют тесты grants)» — отложенное замечание 2/3 (пинг проверяет вход, а не права).

- [ ] **Step 12: Тесты проходят**

Из `backend/`: `go test ./... -count=1` (весь бэкенд — сборка затронула бинарники).
Expected: PASS.

Ручная проверка сборки (тесты разрешены, запуск — нет): `./task dev` не запускать без просьбы пользователя.

Подсадка бага: в `public.NewHandler` убрать `auth.Middleware(…)` из конвейера → `TestPipelineWired` падает (битый токен проходит на `/v1/health`); вернуть.

- [ ] **Step 13: Commit**

```bash
git add backend/internal/platform/config backend/internal/platform/health backend/internal/platform/server \
  backend/internal/httpapi backend/cmd backend/.env.example render.yaml .github/workflows/backend.yml
git commit -m "Сборка края платформы: конвейеры api и admin-api, конфиг ключей и лимитов, почта и чистки воркера, кэш /readyz" -- backend/internal/platform/config backend/internal/platform/health backend/internal/platform/server backend/internal/httpapi backend/cmd backend/.env.example render.yaml .github/workflows/backend.yml
```

---

### Task 20: Документы, правила, финальная проверка

**Files:**
- Modify: `docs/superpowers/specs/2026-10-01-backend-architecture-design.md`
- Modify: `docs/02-архитектура.md`, `docs/deploy-dev.md`, `docs/versions.md`
- Modify: `.claude/rules/backend-platform.md`, `.claude/rules/backend-http.md`
- Modify: `backend/go.mod`, `backend/go.sum` (только `go mod tidy`)

**Interfaces:**
- Consumes: итог Task 1–18.
- Produces: документы совпадают с кодом; `go mod tidy` без изменений после него.

- [ ] **Step 1: Спека**

В `docs/superpowers/specs/2026-10-01-backend-architecture-design.md`:

1. §6.1 — точный порядок: «recover → `request_id` → логи → реальный IP (`platform/peer`, §8.4) → лимит тела → таймаут → маршрут контракта в `ctx` → аутентификация (необязательная, кладёт Principal в `ctx`) → валидация запроса по контракту (требование входа — из `security` операции, `401 auth.unauthenticated`) → rate limit → решение антибота в `ctx` → идемпотентность → strict-хендлер. Ошибки механизмов — `httpx.ProblemError`: хендлер возвращает их как обычные ошибки, ответ — их `problem+json`».
2. §6.2 — после «Ключи — из окружения списком…»: «`kid` — первые 8 байт SHA-256 открытого ключа (`platform/keys.ID`): в окружении только сиды».
3. §6.4 — добавить: «Одна транзакция на идемпотентный запрос: ключ вставляет хук `db.InTx` (`db.WithTxHook`) в первой транзакции; вторая `db.InTx` того же запроса — ошибка; успешный ответ без транзакции — 500 и запись в лог. Сохраняются ответы со статусом < 500, тело (`jsonb`) и заголовки `Content-Type`, `Location`, `ETag` (`response_headers`); повтор помечен `Idempotency-Replayed: true`».
4. §6.6 — «Политики — в конфиге по классам ручек» заменить на: «Класс ручки — расширение `x-rate-limit` операции контракта (без него — `default`); политики классов по ключам IP/пользователь/устройство — `ratelimit.DefaultRules` с переопределением строкой `API_RATE_LIMITS`; класс контракта без правила — отказ старта. Хранилище недоступно — запрос проходит, ошибка в лог (fail-open)».
5. §6.7 — после первого пункта: «Серверная часть ALTCHA своя (`platform/humancheck.PoW`: часы приложения, только SHA-256, `kid` ключа в соли, ротация списком); `altcha-lib-go` — только в тестах совместимости с виджетом».
6. §6.9, строка «Почта»: «`platform/mail.Sender`: одна реализация SMTP — в dev Mailpit (`./task mail`), в проде — провайдер (выбирается отдельно); отправка только задачей River (`mail.send`, очередь `mail`); в аргументах — вид письма и id получателя или кода, не адрес; письмо собирает `mail.Composer` модуля».
7. §6.9, строка «Файлы»: дописать «S3-совместимая реализация — в спеке `media`».
8. §6.9, строка «Наблюдаемость»: дописать «маскирование ПД — в обработчике `slog` (`logx.Mask`: почта, телефоны) для сообщений, атрибутов и ошибок; `/readyz` кэширует результат на 1 с со схлопыванием проб».
9. §8.4, «IP клиента»: дописать «Перед API может стоять прокси хостинга: `X-Forwarded-For` читается только от адресов `TRUSTED_PROXIES`, справа налево до первого недоверенного; секрет BFF — заголовок `X-WF-BFF-Secret`».
10. §14 — у пунктов «Go-реализация серверной части ALTCHA» и «Источник и лицензия списка распространённых паролей» дописать «— проверено в плане 3/3: …» (решения из раздела «Решения контроллера» плана).

- [ ] **Step 2: Архитектура и деплой**

`docs/02-архитектура.md`, раздел «Безопасность → Аутентификация»: абзац о реализации — access JWT EdDSA 10 мин с проверкой сессии на каждый запрос (кэш 5 с), необязательный слой аутентификации и требование входа из `security` контракта, rate limit по классам `x-rate-limit`, антибот PoW (ALTCHA) по сигналам риска, заголовки BFF (`X-WF-Client-IP`, `X-WF-Device`, `X-WF-BFF-Secret`) и доверие к ним (адрес + секрет), ссылка на спеку §6.1–6.7. Раздел «API и реалтайм → Идемпотентность»: одна транзакция на запрос, `409 in_progress`, `409 replay_unavailable`, `422 key_reused`.

`docs/deploy-dev.md`, раздел «Переменные окружения сервиса `wf-api`»: строки `API_JWT_SEEDS`, `API_HUMANCHECK_KEYS` (`generateValue` — Render генерирует сам; ротация: в настройках сервиса новый ключ первым через запятую, старый — следом; JWT — убрать старый через 10 минут, PoW — через срок задачи), `API_TRUSTED_PROXIES` (частные сети балансировщика Render), `API_RATE_LIMITS` (пусто — умолчания), `API_BFF_*` (пока не заданы — BFF на стенде нет). Отдельной строкой: воркер на стенде не запущен — `WORKER_MAIL_*` на Render не нужны.

- [ ] **Step 3: Правила для агентов**

`.claude/rules/backend-platform.md` — в конец раздела:

```markdown
## Край платформы (план 3/3)

- Время — только `clock.Clock` (в тестах `clocktest.Fake`), не `time.Now` в коде платформы и модулей.
- Ошибка, которую должен увидеть клиент (лимит, антибот, идемпотентность, флаг), — `httpx.ProblemError`
  (`httpx.NewError(статус, httpx.Code…)`); код строкой — падение стража `TestProblemCodesAreConstants`.
- Идемпотентная ручка — ровно одна `db.InTx`; чтения до неё — без транзакции.
- Почта — только задача `mail.SendArgs{Mail: "<модуль>.<письмо>", Ref: id}`; адрес находит `mail.Composer`
  модуля, текст — `i18n.Catalog.Text` (`backend/locales`, ключи ru = en).
- Пароли — `password.Check` + `password.Hasher` (argon2id под семафором); «нет почты» — `VerifyDummy`.
- Когда требовать PoW — `risk.Assessor`; требование — `humancheck.Require` (403 с задачей).
- Тесты механизмов с SQL — под ролью прода: `dbtest.NewPoolsAs(t, "api"|"worker")`.
```

`.claude/rules/backend-http.md` — в конец раздела:

```markdown
## Конвейер (спека §6.1)

peer → лимит тела → таймаут → `httpx.Routes` → `auth.Middleware` → `httpx.ValidateRequests` (вход — из
`security` операции) → `ratelimit.Middleware` (класс — `x-rate-limit` операции) → `humancheck.Middleware`
→ `idempotency.Middleware` → strict-хендлер. Principal — `auth.From(ctx)`; IP — `peer.From(ctx)`; права
на ресурс — в `app/` модуля. Новый класс `x-rate-limit` — сначала правило в `ratelimit.DefaultRules`,
иначе API не стартует.
```

- [ ] **Step 4: Версии и `go mod tidy`**

`docs/versions.md`: строка `altcha-lib-go` — уточнить «только тесты»; проверить, что все строки Task 1 на месте.

Из `backend/`: `go mod tidy && git diff --stat go.mod go.sum`
Expected: изменения только в пометках `// indirect` (их не должно быть: все шесть модулей импортируются кодом или тестами). Если `tidy` удалил модуль — значит, задача, которая его использует, не выполнена: остановиться и сообщить.

- [ ] **Step 5: Финальная проверка всего**

Из корня, по очереди:

```bash
./task backend:test
pnpm --filter @wf/contracts test
./task web:test
./task admin:test
CONTRACTS_BASE=origin/main ./task contracts:breaking
```

Expected: всё PASS, ломающих изменений контракта нет. Кодогенерация сходится: из `backend/` по очереди `pnpm --filter @wf/contracts bundle`, `go tool -modfile=tools/go.mod sqlc generate`, оба `oapi-codegen` (Task 5, Step 9) и из корня `pnpm --filter @wf/contracts gen:web`, `gen:admin` — `git status` чистый.

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/specs/2026-10-01-backend-architecture-design.md docs/02-архитектура.md docs/deploy-dev.md docs/versions.md \
  .claude/rules/backend-platform.md .claude/rules/backend-http.md backend/go.mod backend/go.sum
git commit -m "Документы плана 3/3: конвейер, токены, лимиты, антибот, идемпотентность, почта; правила агентов" -- docs/superpowers/specs/2026-10-01-backend-architecture-design.md docs/02-архитектура.md docs/deploy-dev.md docs/versions.md .claude/rules/backend-platform.md .claude/rules/backend-http.md backend/go.mod backend/go.sum
```

---

## После плана (контроллер, не исполнитель)

1. Финальное ревью ветки (`wf-reviewer`, opus) по диффу `main..foundation/auth-edge`.
2. Пуш и PR — только по команде пользователя (глобальный CLAUDE.md).
3. После merge: Render синхронизирует Blueprint и сгенерирует `API_JWT_SEEDS`, `API_HUMANCHECK_KEYS` — проверить `list_deploys` (live), `/healthz`, `/readyz`, `/docs`, на Neon `select max(version_id) from goose_db_version` = 18; API не стартует без ключей — если деплой упал на конфиге, ключи не сгенерировались (проверить настройки сервиса, при необходимости задать вручную через MCP `update_environment_variables` — значения не выводить).
4. Память: `project-setup-order` (фундамент закрыт, следующий — спека `geo`), `foundation-1-leftovers` и `foundation-2-leftovers` (закрытые пункты убрать, оставшиеся — спекам identity, деплоя, наблюдаемости).

