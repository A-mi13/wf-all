---
paths:
  - "backend/internal/httpapi/**"
  - "backend/internal/*/httpapi/**"
  - "backend/internal/*/admin/**"
  - "backend/internal/platform/httpx/**"
  - "backend/internal/platform/apidocs/**"
  - "backend/internal/platform/health/**"
  - "backend/internal/platform/server/**"
  - "backend/cmd/api/**"
  - "backend/cmd/admin-api/**"
---

# HTTP-слой

- Сгенерированные типы API — `internal/httpapi/{public,admin}/oapi`; `httpapi/{public,admin}.Server` встраивает хендлеры модулей; операции платформы (тег `platform`) реализует сам. Тег операции в контракте = модуль-реализатор (`apitest.TagViolations`).
- Ошибки клиенту — `httpx.WriteProblem` с кодом `<модуль>.<ошибка>`; клиенты переводят по `code`, `title` — техническая сводка, не для показа. Код ошибки платформы — константа в `internal/platform/httpx/codes.go` (и в `PlatformCodes`) вместе с контрактом и локалями (`.claude/rules/contracts.md`); литерал кода ловит `TestProblemCodesAreConstants` — он обходит все пакеты `internal/platform/*`; вне httpx код ошибки — только константа `httpx.Code…`. Коды модуля (`<модуль>.<ошибка>`) — константы в корневом пакете модуля (`internal/geo/codes.go`: `Code…` и список `Codes`), перечисляются в `x-error-codes` его операций; сверка в обе стороны — `TestGeoCodesDocumented` (`internal/httpapi/public`), следующий модуль добавляет такой же тест. Проверка хендлера сверх схемы (`q` из одних знаков, явная проверка точки до PostGIS) — `httpx.NewFieldError(поле, правило)`: 400 `validation.failed` с полем, как у валидатора.
- Swagger UI — `internal/platform/apidocs` на `/docs` (контракт — `/docs/openapi.json`), включается `API_DOCS_ENABLED` / `ADMIN_DOCS_ENABLED` (`config.HTTP.DocsEnabled`, по умолчанию выкл.); `/docs` вне контракта — валидатор пропускает его к роутеру. Swagger UI грузится с jsDelivr; версия и SRI-хэши — в `apidocs.go`, обновлять вместе.
- Пробы — `/healthz`, `/readyz` (`platform/health`) вне контракта; в admin-api они стоят до IP-аллоулиста намеренно (health-check хостинга ходит не из сетей админки). `/v1/health` удалён из обоих контрактов (public — спека `geo` §4.4, admin — план сотрудников R13; исключения — `contracts/oasdiff-err-ignore-{public,admin}.txt`). Проверка связи клиентов — запрос контракта (веб — `listCities` с `limit=1`, админка — `GET /v1/staff/me`).

## Конвейер public (спека §6.1)

peer → склейка строк заголовка `Accept-Language` (`httpx.CombineHeaders`: сгенерированный код принимает у заголовка-параметра одну строку) → лимит тела → таймаут → `httpx.Routes` → `auth.Middleware` (на операции без bearer в `security` —
`Route.AcceptsBearer` — `Authorization` не читает) → `httpx.ValidateRequests` (вход — из
`security` операции) → `ratelimit.Middleware` (класс — `x-rate-limit` операции) → `humancheck.Middleware`
→ `idempotency.Middleware` → strict-хендлер. Principal — `auth.From(ctx)`; IP — `peer.From(ctx)`; права
на ресурс — в `app/` модуля. Новый класс `x-rate-limit` — сначала правило в `ratelimit.DefaultRules`,
иначе API не стартует.

- Перед peer `httpx.NewRouter` ставит `request_id`, лог доступа и recover; пробы — до роутера.
- Нестроковый или пустой `x-rate-limit` — ошибка старта (`ratelimit.UnknownClasses`), а не тихий `default`.

## Конвейер admin-api (спека сотрудников §5.1)

peer → IP-аллоулист (`ADMIN_IP_ALLOWLIST`; пусто — выключен; вне списка — `403 auth.ip_not_allowed` на всё, включая
статику и `/docs`) → склейка `Accept-Language` → лимит тела → таймаут → `httpx.Routes` → `Cache-Control: no-store` на
ответах маршрутов контракта → сессия сотрудника по cookie (только маршруты контракта) → CSRF → `httpx.ValidateRequests`
(вход — `auth.AuthenticatedStaff`) → права `x-permission` → `ratelimit.Middleware` → `idempotency.Middleware` →
strict-хендлер. Без `auth.Middleware` и `humancheck`. Сборка — `internal/httpapi/admin` (`handler.go`, звенья — по
файлам `allowlist.go`, `session.go`, `csrf.go`, `permission.go`, `static.go`).

- **Сессия** (P7, R9): имя cookie — только `admin.CookieName(secure)` (`__Host-wf_admin_session`; без
  `ADMIN_COOKIE_SECURE` — dev-имя `wf_admin_session`). Cookie есть → сначала `Limiter.Over(ratelimit.IPKey("staff_auth",
  ip))`: исчерпан — `429 ratelimit.exceeded` без проверки cookie. Невалидный токен (`auth.ErrSessionInvalid` или
  негативный кэш: 5 с по SHA-256 токена, не больше 10 000 записей) → `Limiter.Add` по тому же ключу, ответ стирает
  cookie (`Max-Age=0`), запрос идёт дальше анонимно (валидатор даст 401 там, где нужен вход). Токен только что
  заменён новым (`auth.ErrSessionReplaced`: параллельный запрос со старым токеном вернулся после второго фактора,
  unlock или recovery-codes/ack) — тоже дальше анонимно, но cookie **не** стирается (в браузере уже новая), отказ
  не считается в лимите и не кладётся в негативный кэш (страж `TestReplacedTokenKeepsCookie`). Сбой загрузчика — 500.
  Положительного кэша нет. `X-WF-Background: 1` → `LoadStaff(…, background=true)`. Сотрудник — `auth.WithStaff` до
  лимита и идемпотентности.
- **CSRF**: любой метод, кроме GET/HEAD/OPTIONS/TRACE, — ровно один `Origin`, равный `ADMIN_ORIGIN`, и `X-WF-Admin: 1`,
  иначе `403 auth.csrf_failed` до разбора тела; анонимные мутации тоже. `ADMIN_ORIGIN` — в форме заголовка `Origin`
  (`admin.ValidateOrigin`: схема http/https, нижний регистр, без пути и стандартного порта), аллоулист без `/0`
  (`admin.ValidateAllowlist`) — проверка на старте, до подключения к базе.
- **Права** (`x-permission`): правила строятся из контракта на старте — операция без `x-permission`, ключ вне каталога
  или черновой, `handler` без `x-permission-any` — отказ старта; маршрут без правила в работе — 500 в лог (fail-closed).
  `anonymous` — пропуск; `session` — любая стадия; `self`, ключ и `handler` — только `active`: `mfa_pending` → `401
  auth.unauthenticated`, `setup` → `403 auth.setup_required`, `Reauth` → `401 auth.reauth_required`. Ключ: `{cityId}` в
  пути — `Staff.Decide(ключ, город) != Deny` (`Route.Param`); без города — `Grant != Deny` (scoped-список фильтрует
  ручка; `Decide(ключ, uuid.Nil)` дал бы Deny). `handler` — хоть одно право из `x-permission-any` не `Deny`, точно решает
  сценарий. Нет права — `403 auth.forbidden`.
- **Статика** (`ADMIN_STATIC_DIR`; пусто — не отдаётся): `NotFound` роутера — вне `/v1/*` файл каталога или
  `index.html` (SPA), нет файла в `assets/` — 404; `/v1/*` — 404 Problem; только GET/HEAD. Файл — через
  `os.OpenInRoot` (выход за каталог невозможен). Заголовки: CSP, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`; `index.html` — `no-store`, `assets/` — `immutable`, прочее — `no-cache`.
- **Защита в глубину**: chi сопоставляет `{param}` с пустым сегментом (`PUT /v1/staff/accounts//role`), маршрутизатор
  контракта — нет, и `httpx.Route` не ставится. В `ChiServerOptions` обоих API стоят `httpx.RequireRoute` (на хендлере) и
  `httpx.NotFoundWithoutRoute` (на ошибке биндинга): запрос без `Route` — `404 http.not_found`, хендлер не вызывается.
- `admin.Server` встраивает `*staff/admin.Handler` и `*geo/admin.Handler` указателями через псевдонимы (`staffHandler`,
  `geoHandler`: оба типа называются `Handler`; методы — с указателем-получателем); `Options.Staff`/`Options.Geo`
  обязательны. Операция контракта, которую ещё не исполняет сервер, — заглушка `501 http.not_implemented`
  (`httpx.CodeNotImplemented`; проверка — `apitest.NotImplemented`). Strict-middleware — `o.Staff.Middleware()`
  (операции тега staff: перевод ошибок `staffadmin.Problem`, User-Agent и токен cookie в ctx, нулевой хендлер —
  501; `.claude/rules/backend-staff.md`); заглушки сервера проверяются через него (`TestServerStubsNotImplemented`).
- Тесты: cookie в запросе ставится руками (`net/http/cookiejar` не шлёт Secure-cookie по http, P24); проход конвейера
  — 501 заглушки; `{cityId}` — на подменённом контракте (`admin.Permissions` в `export_test.go`).
