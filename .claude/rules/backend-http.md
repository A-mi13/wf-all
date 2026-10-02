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
- Ошибки клиенту — `httpx.WriteProblem` с кодом `<модуль>.<ошибка>`; клиенты переводят по `code`, `title` — техническая сводка, не для показа. Код ошибки платформы — константа в `internal/platform/httpx/codes.go` (и в `PlatformCodes`) вместе с контрактом и локалями (`.claude/rules/contracts.md`); литерал кода ловит `TestProblemCodesAreConstants` — он обходит все пакеты `internal/platform/*`; вне httpx код ошибки — только константа `httpx.Code…`. Коды модуля (`<модуль>.<ошибка>`) объявляются в модуле и перечисляются в `x-error-codes` его операций (сверка по модулю — с первой спекой модуля).
- Swagger UI — `internal/platform/apidocs` на `/docs` (контракт — `/docs/openapi.json`), включается `API_DOCS_ENABLED` / `ADMIN_DOCS_ENABLED` (`config.HTTP.DocsEnabled`, по умолчанию выкл.); `/docs` вне контракта — валидатор пропускает его к роутеру. Swagger UI грузится с jsDelivr; версия и SRI-хэши — в `apidocs.go`, обновлять вместе.
- Пробы — `/healthz`, `/readyz` (`platform/health`) вне контракта; `/v1/health` убрать из контрактов вместе с первой операцией модуля.

## Конвейер (спека §6.1)

peer → лимит тела → таймаут → `httpx.Routes` → `auth.Middleware` → `httpx.ValidateRequests` (вход — из
`security` операции) → `ratelimit.Middleware` (класс — `x-rate-limit` операции) → `humancheck.Middleware`
→ `idempotency.Middleware` → strict-хендлер. Principal — `auth.From(ctx)`; IP — `peer.From(ctx)`; права
на ресурс — в `app/` модуля. Новый класс `x-rate-limit` — сначала правило в `ratelimit.DefaultRules`,
иначе API не стартует.

- Перед peer `httpx.NewRouter` ставит `request_id`, лог доступа и recover; пробы — до роутера.
- admin-api — тот же конвейер без `auth.Middleware` (вход сотрудников — спека `identity`) и без
  `humancheck.Middleware`.
