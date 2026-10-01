---
paths:
  - "backend/**"
---

# Бэкенд

Узкие правила грузятся вместе с этим по своим путям: схема и стражи — `backend-schema.md`, платформа и
воркер — `backend-platform.md`, HTTP-слой — `backend-http.md`.

- Архитектура — `docs/superpowers/specs/2026-10-01-backend-architecture-design.md`: устройство модуля (§3: корневой пакет = API, `internal/{domain,app,store}`, `httpapi`, `admin`, `subscribers`, `jobs`, `intx`), владение таблицами (§4.1), граф слоёв (§4.3).
- Общее — только в `internal/platform/*`. Чужую таблицу не писать никогда; читать чужое — только через экспортированные представления владельца `<модуль>_read_*` (§4.2). Синхронно — интерфейс корневого пакета; реакции — события outbox; транзакция между модулями — только `intx` из списка §4.4, транзакция явным параметром.
- Новый модуль: `internal/<модуль>/`, SQL в `internal/<модуль>/queries/`, новый элемент списка `sql` в `backend/sqlc.yaml` со своими `queries`/`out` и обязательно `omit_unused_structs: true`; строка в `Layers` (`internal/archtest/modules.go`).
- Граница админки: `cmd/api` и `cmd/worker` не тянут (и транзитивно) ни один пакет `wf/backend/...` с сегментом пути `admin` и `cmd/admin-api` — `internal/archtest`. Админские сценарии модуля — в `internal/<модуль>/admin`, страж их увидит. depguard дублирует только прямые импорты `internal/httpapi/admin` и `cmd/admin-api`: пакеты он сравнивает по префиксу, «любой сегмент» не выразит.
- Возраст не хранится: дата рождения + `age_years()`; пороги — поля `countries.min_signup_age` и `countries.age_of_majority`, не константы.
- Что уже гарантирует база — проверки в коде нужны ради понятной ошибки, а не как единственная защита:
  два матча на одном поле в пересекающееся время (`EXCLUDE` + зазор 10 минут); одинаковые названия
  команд в городе и клоны латиницей (`normalize_text()`); один капитан — одна команда в городе;
  платный матч без суммы и бесплатный с суммой; прогнозы только за заработанные очки, не за купленные;
  больше трёх снятых броней позиции у игрока в команде; изменение записи аудит-лога.
- Цикл TDD — `./task backend:test:watch`; `./task backend:test` всегда прогоняет заново.
- Тесты с БД — `internal/platform/testkit/dbtest` (клон базы на тест); HTTP — `testkit/apitest` (проверка по контракту).
- Кодогенерация бэкенда — `./task backend:gen` (sqlc + oapi-codegen).
- `go tool -modfile=...` запускать с cwd внутри `backend/`, иначе `cannot find main module`.
