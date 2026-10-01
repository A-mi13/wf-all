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
- Сгенерированные типы API — `internal/httpapi/{public,admin}/oapi`; `httpapi/{public,admin}.Server` встраивает хендлеры модулей; операции платформы (тег `platform`) реализует сам. Тег операции в контракте = модуль-реализатор (`apitest.TagViolations`).
- События — только id и непрофильные значения, без персональных данных; у агрегата `version`, подписчик отбрасывает устаревшие версии (§6.5).
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
