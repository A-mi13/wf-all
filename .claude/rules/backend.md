---
paths:
  - "backend/**"
---

# Бэкенд

- Перед изменением схемы читать `backend/migrations/README.md` (порядок миграций, решения, полная таблица гарантий).
- sqlc разбирает грамматику PostgreSQL 17: синтаксис PG18 в миграциях и запросах не использовать.
- Новый модуль: `internal/<модуль>/`, SQL в `internal/<модуль>/queries/`, новый элемент списка `sql` в `backend/sqlc.yaml` со своими `queries`/`out` и обязательно `omit_unused_structs: true`.
- Архитектура — `docs/superpowers/specs/2026-10-01-backend-architecture-design.md`: устройство модуля (§3: корневой пакет = API, `internal/{domain,app,store}`, `httpapi`, `admin`, `subscribers`, `jobs`, `intx`), владение таблицами (§4.1), граф слоёв (§4.3).
- Общее — только в `internal/platform/*`. Чужую таблицу не писать никогда; читать чужое — только через экспортированные представления владельца `<модуль>_read_*` (§4.2). Синхронно — интерфейс корневого пакета; реакции — события outbox; транзакция между модулями — только `intx` из списка §4.4, транзакция явным параметром.
- Стражи — `internal/archtest`: новый модуль → строка в `Layers` (`modules.go`); новая пара `intx` — только правкой спеки §4.4 и `IntxPairs`; новая таблица, представление или функция → строка с владельцем в `Schema` (`ownership.go`), экспортированное представление называется `<модуль>_read_<имя>`. Каталог `queries` в `backend/sqlc.yaml` — только `internal/<модуль>/queries/` или `internal/platform/<пакет>/queries/`: иное страж владения не увидит, `TestSqlcQueriesCoveredByOwnershipGuard` падает. Немодульные `httpapi`, `platform`, `archtest` видят у модуля только корневой пакет, `httpapi` и `admin`; `platform` не импортирует `httpapi` и `archtest`.
- SQL модуля разбирает `archtest/sqlscan`: разрешены только DML-операторы верхнего уровня (SELECT/INSERT/UPDATE/DELETE/MERGE/TRUNCATE/COPY); остальное, `SELECT … INTO` и незнакомая форма дерева разбора (отношение без `relname`) — ошибка стража.
- Swagger UI — `internal/platform/apidocs` на `/docs` (контракт — `/docs/openapi.json`), включается `API_DOCS_ENABLED` / `ADMIN_DOCS_ENABLED` (`config.HTTP.DocsEnabled`, по умолчанию выкл.); `/docs` вне контракта — валидатор пропускает его к роутеру. Swagger UI грузится с jsDelivr; версия и SRI-хэши — в `apidocs.go`, обновлять вместе. Новая ручка — сначала контракт с полным описанием (`.claude/rules/contracts.md`).
- Сгенерированные типы API — `internal/httpapi/{public,admin}/oapi`; `httpapi/{public,admin}.Server` встраивает хендлеры модулей; операции платформы (тег `platform`) реализует сам. Тег операции в контракте = модуль-реализатор (`apitest.TagViolations`).
- События — только id и непрофильные значения, без персональных данных; у агрегата `version`, подписчик в режиме `LatestState` отбрасывает устаревшие версии (§6.5).
- Транзакция платформы — `db.InTx(ctx, pool, fn)`: всегда новая, кладёт tx в ctx; оттуда её читают только `events.Publish`, `audit.Write` (идемпотентность запросов — когда появится, тоже). Вне `InTx` они возвращают `db.ErrNoTx`.
- Подписчик события — `events.Subscription` с явным `Delivery` (`EveryEvent` | `LatestState`), регистрируется в `subscriptions()` в `cmd/worker/main.go`; обработчик идемпотентен по `event_inbox` сам собой, пишет только в своей транзакции `tx`. Доставка на подписку, которой нет в реестре этого воркера (воркер другого релиза при выкатке), — обычная ошибка с повтором, а затем «мёртвая» задача (видна, переигрывается), не отмена; отменяется только задача на событие, уже удалённое чисткой. Переигровка — `worker events replay --type T --subscriber S --since RFC3339`.
- Задача River объявляет очередь из `platform/queue` (`events`, `lifecycle`, `notify`, `mail`, `stats`, `media`, `maintenance`) в `InsertOpts`; очереди `default` нет. Аргументы — с полем версии `V`, без ПД, уникальность — `UniqueOpts`. River в воркере работает на неотменяемом ctx: по SIGTERM сначала останавливается relay, затем `queue.Stop` — мягкая остановка (25 с) и, не уложившись, `StopAndCancel` (5 с): сумма укладывается в 30 с между SIGTERM и SIGKILL. `WORKER_RELAY_BATCH` ≥ 1 и `WORKER_RELAY_POLL` > 0 проверяются на старте воркера.
- Аудит — `audit.Write(ctx, entry)` в транзакции действия; чтение чувствительного — `audit.WriteRead`. ПД в `Before`/`After` маскирует `audit.Mask` по словам ключа (camelCase/PascalCase делятся на слова: `contactEmail`, `contact_email`, `ContactEmail` — одно и то же); не маскируется ключ, оканчивающийся на `at`/`count`/`verified`/`enabled`/`required`. Маскирование по ключу, а не по содержимому: свободный текст с ПД (комментарии, описания) в `Before`/`After` класть нельзя. Новые чувствительные слова — в списки слов и пар в `audit/mask.go`.
- Права ролей БД — `internal/platform/grants/grants.sql` (применяет `migrate up`, `down` и `reset`: `REVOKE ALL`, затем ровно нужное; у `worker` на `river_job` — `MAINTAIN` для ежедневной переиндексации River); новая таблица получает DML всем ролям по умолчанию — особые права (только чтение, только воркер) — новой веткой CASE там же и случаем в `grants_test.go`.
- Пробы — `/healthz`, `/readyz` (`platform/health`) вне контракта; `/v1/health` убрать из контрактов вместе с первой операцией модуля.
- Граница админки: `cmd/api` и `cmd/worker` не тянут (и транзитивно) ни один пакет `wf/backend/...` с сегментом пути `admin` и `cmd/admin-api` — `internal/archtest`. Админские сценарии модуля — в `internal/<модуль>/admin`, страж их увидит. depguard дублирует только прямые импорты `internal/httpapi/admin` и `cmd/admin-api`: пакеты он сравнивает по префиксу, «любой сегмент» не выразит.
- Цикл TDD — `./task backend:test:watch`; `./task backend:test` всегда прогоняет заново.
- Тесты с БД — `internal/platform/testkit/dbtest` (клон базы на тест); HTTP — `testkit/apitest` (проверка по контракту).
- Ошибки клиенту — `httpx.WriteProblem` с кодом `<модуль>.<ошибка>`; клиенты переводят по `code`, `title` — техническая сводка, не для показа.
- Код ошибки платформы — константа в `internal/platform/httpx/codes.go` (и в `PlatformCodes`) + строка `x-error-codes-common` в `contracts/openapi/{public,admin}/root.yaml` + тексты `errors.<код>` в локалях приложений; сверку с контрактом держит `TestPlatformCodesDocumented`, литерал кода в httpx — `TestProblemCodesAreConstants`. Коды модуля (`<модуль>.<ошибка>`) объявляются в модуле и перечисляются в `x-error-codes` его операций (сверка по модулю — с первой спекой модуля).
- Возраст не хранится: дата рождения + `age_years()`; пороги — поля `countries.min_signup_age` и `countries.age_of_majority`, не константы.
- Что уже гарантирует база — проверки в коде нужны ради понятной ошибки, а не как единственная защита:
  два матча на одном поле в пересекающееся время (`EXCLUDE` + зазор 10 минут); одинаковые названия
  команд в городе и клоны латиницей (`normalize_text()`); один капитан — одна команда в городе;
  платный матч без суммы и бесплатный с суммой; прогнозы только за заработанные очки, не за купленные;
  больше трёх снятых броней позиции у игрока в команде; изменение записи аудит-лога.
- `./task backend:db:reset` отказывает на нелокальном хосте (`cmd/migrate/reset.go`) — защиту не ослаблять.
- Кодогенерация бэкенда — `./task backend:gen` (sqlc + oapi-codegen).
- `go tool -modfile=...` запускать с cwd внутри `backend/`, иначе `cannot find main module`.
