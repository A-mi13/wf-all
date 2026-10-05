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
- `normalize_text` в индексе по выражению не работает (PG17: maintenance-операции идут с `search_path = pg_catalog, pg_temp`) — индекс строится по генерируемой колонке `*_normalized … STORED`.
- Ловушка sqlc: `@x::type + …` разбирается как префиксный оператор `@` над всей суммой — перед арифметикой параметр пишется `sqlc.arg(x)` (пример — `internal/platform/ratelimit/queries/ratelimit.sql`).
- Стражи — `internal/archtest`: новая пара `intx` — только правкой спеки §4.4 и `IntxPairs`; новая таблица, представление или функция → строка с владельцем в `Schema` (`ownership.go`), экспортированное представление называется `<модуль>_read_<имя>`. Каталог `queries` в `backend/sqlc.yaml` — только `internal/<модуль>/queries/` или `internal/platform/<пакет>/queries/`: иное страж владения не увидит, `TestSqlcQueriesCoveredByOwnershipGuard` падает. Немодульные `httpapi`, `platform`, `archtest` видят у модуля только корневой пакет, `httpapi` и `admin`; `platform` не импортирует `httpapi` и `archtest`.
- SQL модуля разбирает `archtest/sqlscan`: разрешены только DML-операторы верхнего уровня (SELECT/INSERT/UPDATE/DELETE/MERGE/TRUNCATE/COPY); остальное, `SELECT … INTO` и незнакомая форма дерева разбора (отношение без `relname`) — ошибка стража.
- Права ролей БД — `internal/platform/grants/grants.sql` (применяет `migrate up`, `down` и `reset`: `REVOKE ALL`, затем ровно нужное; у `worker` на `river_job` — `MAINTAIN` для ежедневной переиндексации River); новая таблица получает DML всем ролям по умолчанию — особые права (только чтение, только воркер) — новой веткой CASE там же и случаем в `grants_test.go`.
- `./task backend:db:reset` отказывает на нелокальном хосте (`cmd/migrate/reset.go`) — защиту не ослаблять.
