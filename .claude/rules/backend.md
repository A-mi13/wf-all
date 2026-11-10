---
paths:
  - "backend/**"
---

# Бэкенд

Узкие правила грузятся вместе с этим по своим путям: схема и стражи — `backend-schema.md`, платформа и
воркер — `backend-platform.md`, HTTP-слой — `backend-http.md`, модуль сотрудников — `backend-staff.md`.

- Архитектура — `docs/superpowers/specs/2026-10-01-backend-architecture-design.md`: устройство модуля (§3: корневой пакет = API, `internal/{domain,app,store}`, `httpapi`, `admin`, `subscribers`, `jobs`, `intx`), владение таблицами (§4.1), граф слоёв (§4.3).
- Общее — только в `internal/platform/*`. Чужую таблицу не писать никогда; читать чужое — только через экспортированные представления владельца `<модуль>_read_*` (§4.2). Синхронно — интерфейс корневого пакета; реакции — события outbox; транзакция между модулями — только `intx` из списка §4.4, транзакция явным параметром.
- Новый модуль: `internal/<модуль>/`, SQL в `internal/<модуль>/queries/`, новый элемент списка `sql` в `backend/sqlc.yaml` со своими `queries`/`out` и обязательно `omit_unused_structs: true`; строка в `Layers` (`internal/archtest/modules.go`).
- Сотрудники админки — модуль `staff` (слой 0; спека `docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md`, правила — `backend-staff.md`): учётки `staff_*`, не `users`; роли игроков — только `role_assignments` (`captain`/`city`). Права сотрудника модуль проверяет через `auth.StaffFrom(ctx).Decide(право, cityID)`, не импортируя `staff`; новое право — строка в `internal/platform/perm/catalog.go` своим коммитом, затем `./task gen` (`docs/05-ролевая-модель.md` и `contracts/openapi/admin/permissions.json`). Опасное действие модуля — действие `platform/approvals` (`Registry.Register(approvals.Action{…})`), а не своя таблица запросов. `cmd/api` и `cmd/worker` не зависят от `staff` даже транзитивно (`TestPublicBinariesDoNotDependOnStaff`).
- Граница админки: `cmd/api` и `cmd/worker` не тянут (и транзитивно) ни один пакет `wf/backend/...` с сегментом пути `admin` и `cmd/admin-api` — `internal/archtest`. Админские сценарии модуля — в `internal/<модуль>/admin`, страж их увидит. depguard дублирует только прямые импорты `internal/httpapi/admin` и `cmd/admin-api`: пакеты он сравнивает по префиксу, «любой сегмент» не выразит.
- Возраст не хранится: дата рождения + `age_years()`; пороги — поля `countries.min_signup_age` и `countries.age_of_majority`, не константы.
- Что уже гарантирует база — проверки в коде нужны ради понятной ошибки, а не как единственная защита:
  два матча на одном поле в пересекающееся время (`EXCLUDE` + зазор 10 минут); одинаковые названия
  команд в городе и клоны латиницей (`normalize_text()`); один капитан — одна команда в городе;
  платный матч без суммы и бесплатный с суммой; прогнозы только за заработанные очки, не за купленные;
  больше трёх снятых броней позиции у игрока в команде; изменение записи аудит-лога.
- Цикл TDD — `./task backend:test:watch`; `./task backend:test` всегда прогоняет заново.
- Тесты с БД — `internal/platform/testkit/dbtest`; HTTP — `testkit/apitest` (проверка по контракту). Чтения сценариев
  (без HTTP-конвейера) — `dbtest.SharedAs(t, роль, посев)`: один засеянный клон на пакет под ролью прода
  (`api`/`admin`/`worker`), только на чтение, пул владельца тесту не отдаётся; посев — одна функция верхнего уровня
  на пакет и роль (замыкание и значение метода — Fatalf: посев узнаётся по коду функции), ожидания списков учитывают
  весь посев; пакету нужен `func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }` (уборка клона — при любом исходе
  тестов; падение по `-timeout` или панике оставляет клон). HTTP-конвейер (пишет лимиты, `last_seen_at`, ключи идемпотентности и на `GET`) и запись —
  `dbtest.New*` (клон на тест).
- Тесты пакетов с базой (каталог, где `*_test.go` импортирует `dbtest`) — параллельные: каждая `Test*` первой строкой
  зовёт `t.Parallel()`, нельзя (пишет переменную пакета вроде порции чистки, `t.Setenv`, порог реального времени) —
  комментарий `// serial: <причина>` над функцией; табличный подтест со своим окружением — тоже `t.Parallel()`. Пакет
  объявляет `func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }`. Страж — `archtest.TestDBTestsAreParallel`.
  Сколько разом: `dbtest.Main` ставит `-parallel` = `dbtest.DefaultParallel` (4), если флаг не задан явно (умолчание
  go — GOMAXPROCS = 24: клоны и DROP DATABASE душат Postgres, пакет вдвое медленнее последовательного);
  `./task backend:test` и `test:watch` — явно `-p 4 -parallel 1 -timeout 20m` (предел пакета: `httpapi/admin` идёт ~10 мин; job CI — 30 мин): весь набор упирается в CREATE/DROP DATABASE
  (запись клона ~21 МБ, checkpoint и барьер каждого DROP) — параллельность полному набору выигрыша не даёт (S1,
  03.11, одиночные прогоны: 359 с у `-parallel 1` против 686 с у `-parallel 8`). Несколько пакетов одной командой
  (`go test ./...`, IDE, список пакетов) — только с `-p 4`: без него GOMAXPROCS (24) пакетов × 4 = до 96 тестов разом
  и `53300 too many clients`. Соединения: тест ≤ 14 (до трёх пулов по `PoolMaxConns` + служебные pgtestdb и grants),
  процесс + общий клон 4–8; `backend:test` ≤ 4×14 + 4×8 = 88 < `max_connections` 100 (локально и в CI); замер пика —
  43 соединения при 32 тестах разом (пулы ленивые). Не хватает — `ALTER SYSTEM SET max_connections = 200` и
  `bash scripts/pg.sh stop` + `start`.
- Кодогенерация бэкенда — `./task backend:gen` (sqlc + oapi-codegen).
- `go tool -modfile=...` запускать с cwd внутри `backend/`, иначе `cannot find main module`.
