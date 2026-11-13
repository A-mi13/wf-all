---
paths:
  - "backend/migrations/**"
  - "backend/sqlc.yaml"
  - "backend/internal/**/queries/**"
  - "backend/internal/archtest/**"
  - "backend/internal/platform/grants/**"
  - "backend/cmd/migrate/**"
---

# Схема, SQL и стражи

- Перед изменением схемы читать `backend/migrations/README.md` (порядок миграций, решения, полная таблица гарантий).
- sqlc разбирает грамматику PostgreSQL 17: синтаксис PG18 в миграциях и запросах не использовать.
- Индекс по нормализованному тексту — по генерируемой колонке `*_normalized … STORED`, не по выражению с `normalize_text` (соглашение: смена функции пересчитывает колонки в одном месте, `ALTER COLUMN … SET EXPRESSION`). Исторически: до `0023` `normalize_text` звала `immutable_unaccent` без схемы и в индексе по выражению падала на узком `search_path = pg_catalog, pg_temp` maintenance-операций PG17; с `0023` вызов со схемой.
- Смена `normalize_text` — новой миграцией с пересчётом всех `*_normalized` (`ALTER COLUMN … SET EXPRESSION`; STORED-колонка сама не пересчитывается) и хранимых нормализованных значений, проверкой столкновений уникальных индексов на стенде до мержа (`docs/deploy-dev.md`) и своими стражами по образцу `migrations/platform_0023_test.go`: каталог генерируемых колонок и уникальных индексов на `*_normalized` сверяется на версии ДО новой миграции (`DownTo`), поэтому страж `0023` колонку или индекс из `0024+` не видит — у следующей смены функции свой страж на своей версии. С `0023` регистр не влияет ни на одну букву (`lower`, затем строчные латинские двойники — в кириллицу).
- Ловушка sqlc: `@x::type + …` разбирается как префиксный оператор `@` над всей суммой — перед арифметикой параметр пишется `sqlc.arg(x)` (пример — `internal/platform/ratelimit/queries/ratelimit.sql`).
- Стражи — `internal/archtest`: новая пара `intx` — только правкой спеки §4.4 и `IntxPairs`; новая таблица, представление или функция → строка с владельцем в `Schema` (`ownership.go`), экспортированное представление называется `<модуль>_read_<имя>`. Каталог `queries` в `backend/sqlc.yaml` — только `internal/<модуль>/queries/` или `internal/platform/<пакет>/queries/`: иное страж владения не увидит, `TestSqlcQueriesCoveredByOwnershipGuard` падает. Немодульные `httpapi`, `platform`, `archtest` видят у модуля только корневой пакет, `httpapi` и `admin`; `platform` не импортирует `httpapi` и `archtest`.
- SQL модуля разбирает `archtest/sqlscan`: разрешены только DML-операторы верхнего уровня (SELECT/INSERT/UPDATE/DELETE/MERGE/TRUNCATE/COPY); остальное, `SELECT … INTO` и незнакомая форма дерева разбора (отношение без `relname`) — ошибка стража.
- Права ролей БД — `internal/platform/grants/grants.sql` (применяет `migrate up`, `down` и `reset`: `REVOKE ALL`, затем ровно нужное; у `worker` и `admin` (River в admin-api: исполнение очереди `staff` и роль лидера) на `river_job` — DML и `MAINTAIN` для ежедневной переиндексации River, на прочих `river_*` — DML; у `api` — только `SELECT, INSERT, UPDATE` на `river_job`; `staff_*` (включая `staff_idempotency_keys` и `staff_rate_limits`), `approvals` и их последовательности — только `admin`; на `idempotency_keys` и `rate_limits` игроков у `admin` прав нет — DML у `api` и `worker`; `city_slug_history` — у `admin` и `DELETE` (возврат к своему старому slug), города, страны и районы `admin` не удаляет; `goose_db_version` — только `SELECT` у `api`, `admin`, `worker` для ожидания схемы `migrate.WaitSchema`, запись — только мигратору); новая таблица получает DML всем ролям по умолчанию — особые права (только чтение, только воркер) — новой веткой CASE там же и случаем в `grants_test.go`.
- `./task backend:db:reset` отказывает на нелокальном хосте (`cmd/migrate/reset.go`) — защиту не ослаблять.
