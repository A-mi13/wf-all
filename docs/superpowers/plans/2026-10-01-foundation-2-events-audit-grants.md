# Фундамент 2/3: транзакции, события, очереди, аудит, гранты — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Платформа, на которой модули согласуются надёжно: транзакция, видимая платформе через `ctx`; события через outbox → relay → River → inbox с отбрасыванием устаревших версий и переигровкой; семь очередей воркера и чистка; аудит с маскированием; наименьшие права ролей БД; пробы `/healthz` и `/readyz` вне контракта.

**Architecture:** Всё — в `backend/internal/platform/*`, SQL платформы — через sqlc (как `flags`). `db.InTx` кладёт транзакцию в `ctx`; читают её только `events.Publish`, `audit.Write` (и идемпотентность в плане 3/3). Relay живёт в воркере: просыпается по `NOTIFY` от триггера на `outbox`, страхуется опросом, берёт пачку `FOR UPDATE SKIP LOCKED` и в той же транзакции ставит по задаче River на каждого подписчика. Подписчик исполняется в транзакции вместе с записью `event_inbox` (повтор не задваивает) и, если он следит за состоянием агрегата, — с курсором версии (`event_cursors`). Права ролей задаёт идемпотентный `grants.sql`, который `cmd/migrate` применяет после миграций.

**Tech Stack:** Go 1.27.1, pgx v5.11.0, River v0.47.0, sqlc 1.31.1, goose 3.28.0, github.com/google/uuid v1.6.0 (UUIDv7), chi v5.3.2.

**Spec:** `docs/superpowers/specs/2026-10-01-backend-architecture-design.md` — §1 (критерий готовности п. 4), §4.4 (транзакция в `ctx`), §6.5 (события), §6.9 (ID, аудит, наблюдаемость: `/healthz`, `/readyz`), §9.1 (очереди), §9.3 (чистка), §9.5 (конвенции задач), §10.1 (роли БД), §11 (миграция platform), §12 (тесты событий, страж 7 «роли БД»). Второй из трёх планов «фундамента» (§13 п. 0); 3/3 — токены, Principal, IP, rate limit, антибот, идемпотентность, почта, файлы, серверные локали, часы.

## Global Constraints

- Go 1.27.1; модуль `wf/backend`; sqlc разбирает грамматику PostgreSQL 17 — синтаксис PG18 в SQL не использовать.
- TDD: сначала падающий тест, потом код; каждый страж проверяется подсадкой бага.
- Комментарии в коде — по-русски, идентификаторы — по-английски; переводы строк LF.
- ID — UUIDv7, генерируются в Go (`platform/id`); строки из SQL получают v4 по `DEFAULT` (§6.9).
- Время событий — время транзакции из базы (`now()`), не часы инстанса (§6.5).
- События — только id и непрофильные значения, без персональных данных (§4.6); имя — `<модуль>.<факт>`.
- Аргументы задач River — версионированы и без персональных данных, у задачи есть очередь, таймаут и лимит попыток (§9.5).
- Обычные методы не присоединяются к транзакции из `ctx`: `db.InTx` всегда открывает новую (§4.4).
- `audit_log` — только `INSERT` (и `SELECT` для `admin`); `UPDATE`/`DELETE` запрещены всем (§10.1).
- Тесты бэкенда — `./task backend:test` (нужен Postgres: `bash scripts/pg.sh start` в фоне — команда не возвращает управление); один пакет — `go test ./internal/platform/<пакет>/... -count=1` из `backend/`.
- Кодогенерация — `./task backend:gen` (sqlc + oapi-codegen); сгенерированное коммитится.
- `typecheck`, `lint`, `build` не запускать без явной просьбы пользователя; тесты — всегда.
- Новый env-ключ — в том же коммите: поле в `internal/platform/config`, строка с комментарием в `backend/.env.example`, значение в локальном `backend/.env` (писать скриптом, значения не выводить — `.claude/rules/env.md`).
- Коммиты: одна строка по-русски, без тела; только свои пути: `git add <пути> && git commit -m "…" -- <пути>`. Никогда `git add -A`, `git add .`, `git commit -a`, `git stash`, `git reset`, `git clean`, `git checkout -- .`.
- `rm -rf` вне проекта запрещён; временные файлы — в `.tools/tmp/`.
- Тексты, где есть обратные кавычки, не передавать через `node -e "…"`/двойные кавычки bash — только Edit/Write или heredoc `<<'EOF'`.

## Решения контроллера (до исполнения)

- **Переигровка — `worker events replay`, а не `cmd/migrate events replay` (§6.5).** Подписчиков знает только воркер: их реестр собирается в `cmd/worker`. Команда в `cmd/migrate` не смогла бы проверить, что подписчик существует, и молча поставила бы задачи, которые никто не исполнит. Спека §6.5 правится в Task 11. Цена ошибки — перенос подкоманды между бинарниками.
- **Режим доставки задаётся подписчиком явно** (`events.EveryEvent` | `events.LatestState`), нулевое значение — ошибка регистрации. §6.5 говорит «подписчик отбрасывает устаревшие события» — это верно для подписчиков, синхронизирующих состояние (`notify` о составе), и неверно для счётных (`stats` считает каждое `member_joined`/`member_left`: отбросить старое — испортить счёт). Курсор ведётся только в `LatestState`. Отбрасывается событие со **строго меньшей** версией: два события одной транзакции агрегата имеют одну версию и оба применяются; повтор того же события отсекает `event_inbox`.
- **Трассировка в конверте:** колонка `outbox.trace jsonb` создаётся (§11), но пишется `NULL` — OTel в проекте ещё нет; заполнение — вместе с OTel (наблюдаемость — отдельная работа, как и метрика лага outbox).
- **Часы (`platform/clock`) — в плане 3/3:** в этом плане время берётся из базы (`now()`), часы нужны токенам.
- **`/v1/health` остаётся в контракте** до первой операции модуля: на ней держатся страж «тег = модуль» (≥ 1 операции), smoke-тесты клиентов веба и админки и тест бандла. В этом плане появляются `/healthz` и `/readyz` вне контракта, Render переключается на `/healthz`.
- **Render — на `/healthz`, не на `/readyz`:** Render опрашивает пробу постоянно, пинг базы не давал бы Neon засыпать и съедал бы бесплатные часы.
- **Права ролей — по владельцу:** `grants.sql` обходит только объекты схемы `public`, которыми владеет текущая роль, и не трогает объекты расширений (`spatial_ref_sys` PostGIS). Отсутствующая роль (на Neon пока нет `admin` и `worker`) пропускается с `NOTICE`.

## Review Focus

1. **Relay упал между постановкой задач и отметкой `published_at`** — не должно быть ни потерянного события, ни задвоения: постановка и отметка — одна транзакция (Task 5, тест «ошибка вставки задач откатывает отметку»).
2. **Два relay параллельно** (два инстанса воркера) — каждое событие ставится подписчику ровно один раз (Task 5, тест «два relay, 200 событий»).
3. **Обработчик подписчика вернул ошибку** — запись `event_inbox` откатывается вместе с его изменениями, повтор задачи обрабатывает событие заново, а не считает его обработанным (Task 4, тест «ошибка обработчика не оставляет inbox»).
4. **`Publish` вне транзакции или в откаченной транзакции** — ошибка `ErrNoTx` в первом случае, ни строки outbox, ни доставки — во втором (Task 3, Task 5).
5. **Событие вставлено, пока relay ждёт** — доставка по `NOTIFY` за доли секунды, а не через интервал опроса; после обрыва `LISTEN`-соединения relay переподключается и не теряет событий (Task 5, тесты «будит NOTIFY» и «переподключение»).

## Карта файлов

```
backend/
  go.mod, go.sum                                    + github.com/google/uuid v1.6.0 (Task 1)
  internal/platform/id/id.go, id_test.go            Task 1 — UUIDv7
  internal/platform/db/tx.go, tx_test.go            Task 1 — InTx, TxFrom, ErrNoTx
  migrations/0017_platform_events.sql               Task 2 — outbox v2, event_inbox, event_cursors, NOTIFY
  migrations/README.md                              Task 2
  internal/archtest/ownership.go                    Task 2 — владельцы новых объектов
  sqlc.yaml                                         Task 3, Task 8 — eventsdb, auditdb
  internal/platform/events/
    queries/events.sql                              Task 3 (все запросы событий)
    eventsdb/*.go                                   сгенерировано
    events.go, publish_test.go                      Task 3 — Event, Envelope, Publish
    registry.go, registry_test.go                   Task 3 — Subscription, Delivery, Registry
    deliver.go, deliver_test.go                     Task 4 — DeliverArgs, DeliverWorker
    relay.go, relay_test.go                         Task 5 — Relay
    e2e_test.go                                     Task 5 — критерий готовности §1 п. 4
    replay.go, replay_test.go                       Task 7 — Replay
    cleanup.go, cleanup_test.go                     Task 6 — CleanupArgs, CleanupJob
  internal/platform/config/config.go, config_test.go   Task 6 — Queues, Relay
  internal/platform/queue/queue.go, queue_test.go      Task 6 — семь очередей, периодические задачи
  cmd/worker/main.go, main_test.go                  Task 6 — relay, подписчики; Task 7 — `events replay`
  internal/platform/audit/
    queries/audit.sql, auditdb/*.go                 Task 8
    audit.go, audit_test.go, mask.go, mask_test.go  Task 8
  internal/platform/grants/grants.go, grants.sql, grants_test.go   Task 9 — роли БД, страж §12.7
  cmd/migrate/main.go, main_test.go                 Task 9 — grants после up/reset
  internal/platform/health/health.go, health_test.go   Task 10 — /healthz, /readyz
  internal/platform/server/server.go, server_test.go   Task 10
  .env.example                                      Task 6
deploy/dev/initdb/20_wf.sql                         Task 9 — умолчания сужены
render.yaml, .github/workflows/backend.yml, README.md   Task 10
docs/deploy-dev.md                                  Task 9, Task 10
docs/superpowers/specs/2026-10-01-backend-architecture-design.md   Task 11 — §6.5 replay
docs/versions.md, .claude/rules/backend.md, docs/04-принципы-архитектуры.md   Task 11
```

---

## Фаза A. Основа: ID, транзакции, схема событий

### Task 1: UUIDv7 и транзакция в `ctx`

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`
- Create: `backend/internal/platform/id/id.go`, `backend/internal/platform/id/id_test.go`
- Create: `backend/internal/platform/db/tx.go`, `backend/internal/platform/db/tx_test.go`

**Interfaces:**
- Consumes: `dbtest.NewPool(t) *pgxpool.Pool` (`internal/platform/testkit/dbtest`).
- Produces:
  - `id.New() uuid.UUID` — UUIDv7 (`github.com/google/uuid`).
  - `db.TxStarter interface { Begin(ctx context.Context) (pgx.Tx, error) }` — его реализует `*pgxpool.Pool`.
  - `db.InTx(ctx context.Context, s TxStarter, fn func(ctx context.Context, tx pgx.Tx) error) error` — открывает новую транзакцию всегда (и когда в `ctx` уже есть другая), кладёт её в `ctx` для `fn`, коммитит при `nil`, откатывает при ошибке и панике (паника пробрасывается дальше).
  - `db.TxFrom(ctx context.Context) (pgx.Tx, bool)`.
  - `db.ErrNoTx` — ошибка «нужна транзакция из `db.InTx`»; её возвращают `events.Publish` и `audit.Write`.

- [ ] **Step 1: Зависимость**

Из `backend/`:

```bash
go get github.com/google/uuid@v1.6.0
```

Expected: в `go.mod` прямая зависимость `github.com/google/uuid v1.6.0`.

- [ ] **Step 2: Падающие тесты ID**

`backend/internal/platform/id/id_test.go`:

```go
package id_test

import (
	"bytes"
	"testing"

	"wf/backend/internal/platform/id"
)

func TestNewIsVersion7(t *testing.T) {
	if v := id.New().Version(); v != 7 {
		t.Fatalf("версия = %d, нужна 7", v)
	}
}

// Ключи UUIDv7 растут во времени: вставки в индекс идут в конец, keyset-пагинация по id
// совпадает с порядком создания внутри одного процесса.
func TestNewIsMonotonic(t *testing.T) {
	prev := id.New()
	for range 10_000 {
		next := id.New()
		if bytes.Compare(prev[:], next[:]) >= 0 {
			t.Fatalf("%s не меньше %s", prev, next)
		}
		prev = next
	}
}
```

- [ ] **Step 3: Убедиться, что падает**

Run: `go test ./internal/platform/id/ -count=1`
Expected: FAIL — `no non-test Go files`.

- [ ] **Step 4: Реализация ID**

`backend/internal/platform/id/id.go`:

```go
// Package id — идентификаторы сущностей: UUIDv7, генерируются в Go (спека бэкенда §6.9).
// Строки, вставленные SQL (сиды, триггеры), получают v4 по DEFAULT, поэтому порядок по
// времени для них — только по created_at.
package id

import "github.com/google/uuid"

// New — новый UUIDv7. Ошибка генерации возможна только при отказе crypto/rand — тогда
// продолжать нельзя, поэтому паника.
func New() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}
```

- [ ] **Step 5: Тесты ID проходят**

Run: `go test ./internal/platform/id/ -count=1`
Expected: PASS.

- [ ] **Step 6: Падающие тесты транзакции**

`backend/internal/platform/db/tx_test.go`:

```go
package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/testkit/dbtest"
)

func count(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) int {
	t.Helper()
	var n int
	if err := q.QueryRow(context.Background(), "SELECT count(*) FROM tx_probe").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func probe(t *testing.T) (context.Context, db.TxStarter, func() int) {
	t.Helper()
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "CREATE TABLE tx_probe (n int)"); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, func() int { return count(t, pool) }
}

func TestInTxCommitsAndExposesTx(t *testing.T) {
	ctx, pool, rows := probe(t)
	err := db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		got, ok := db.TxFrom(ctx)
		if !ok || got != tx {
			t.Fatal("TxFrom не вернул транзакцию InTx")
		}
		_, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)")
		return err
	})
	if err != nil || rows() != 1 {
		t.Fatalf("err = %v, строк = %d", err, rows())
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	ctx, pool, rows := probe(t)
	boom := errors.New("boom")
	err := db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || rows() != 0 {
		t.Fatalf("err = %v, строк = %d", err, rows())
	}
}

func TestInTxRollsBackOnPanicAndRepanics(t *testing.T) {
	ctx, pool, rows := probe(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("паника проглочена")
			}
		}()
		_ = db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if rows() != 0 {
		t.Fatalf("строк = %d после паники", rows())
	}
}

// Спека §4.4: вложенный вызов не присоединяется к транзакции из ctx, а открывает свою —
// её откат не трогает внешнюю, и TxFrom внутри видит внутреннюю.
func TestNestedInTxOpensOwnTransaction(t *testing.T) {
	ctx, pool, rows := probe(t)
	err := db.InTx(ctx, pool, func(ctx context.Context, outer pgx.Tx) error {
		if _, err := outer.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
			return err
		}
		_ = db.InTx(ctx, pool, func(ctx context.Context, inner pgx.Tx) error {
			if got, _ := db.TxFrom(ctx); got != inner || inner == outer {
				t.Fatal("вложенный InTx не открыл свою транзакцию")
			}
			_, _ = inner.Exec(ctx, "INSERT INTO tx_probe VALUES (2)")
			return errors.New("откатить внутреннюю")
		})
		return nil
	})
	if err != nil || rows() != 1 {
		t.Fatalf("err = %v, строк = %d — внутренний откат задел внешнюю", err, rows())
	}
}

// Отменённый ctx не должен оставлять транзакцию висеть: откат идёт и после отмены.
func TestInTxRollsBackWhenContextCancelled(t *testing.T) {
	_, pool, rows := probe(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || rows() != 0 {
		t.Fatalf("err = %v, строк = %d", err, rows())
	}
}

func TestTxFromEmptyContext(t *testing.T) {
	if _, ok := db.TxFrom(context.Background()); ok {
		t.Fatal("в пустом ctx нашлась транзакция")
	}
}
```

- [ ] **Step 7: Убедиться, что падает**

Run: `go test ./internal/platform/db/ -count=1`
Expected: FAIL — `undefined: db.InTx`.

- [ ] **Step 8: Реализация транзакции**

`backend/internal/platform/db/tx.go`:

```go
package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrNoTx — механизм платформы, которому нужна транзакция из ctx (events.Publish,
// audit.Write), вызван вне db.InTx.
var ErrNoTx = errors.New("db: нужна транзакция из db.InTx")

// TxStarter открывает транзакцию; его реализует *pgxpool.Pool.
type TxStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type txKey struct{}

// InTx исполняет fn в новой транзакции и кладёт её в ctx — оттуда её читают только
// механизмы платформы (события, аудит, идемпотентность), спека §4.4. Транзакция открывается
// всегда новая, даже если в ctx уже есть другая: невидимого присоединения к чужой транзакции
// нет. nil — коммит; ошибка или паника — откат (паника пробрасывается).
func InTx(ctx context.Context, s TxStarter, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			// откат и после отмены ctx: иначе соединение вернётся в пул с открытой транзакцией
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(context.WithValue(ctx, txKey{}, tx), tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TxFrom — транзакция, которую положил db.InTx.
func TxFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
```

- [ ] **Step 9: Тесты проходят**

Run: `go test ./internal/platform/db/ ./internal/platform/id/ -count=1`
Expected: PASS.

- [ ] **Step 10: Подсадка бага**

Временно замени в `tx.go` `fn(context.WithValue(ctx, txKey{}, tx), tx)` на `fn(ctx, tx)`. Run: `go test ./internal/platform/db/ -count=1`. Expected: FAIL — `TestInTxCommitsAndExposesTx` и `TestNestedInTxOpensOwnTransaction` («TxFrom не вернул транзакцию»). Верни код — PASS.

- [ ] **Step 11: Commit**

```bash
git add backend/go.mod backend/go.sum backend/internal/platform/id backend/internal/platform/db/tx.go backend/internal/platform/db/tx_test.go
git commit -m "Платформа: UUIDv7 и транзакция в ctx (db.InTx)" -- backend/go.mod backend/go.sum backend/internal/platform/id backend/internal/platform/db/tx.go backend/internal/platform/db/tx_test.go
```

### Task 2: Миграция платформы событий

**Files:**
- Create: `backend/migrations/0017_platform_events.sql`
- Modify: `backend/internal/archtest/ownership.go` (карта `Schema`, блок `// platform`)
- Modify: `backend/migrations/README.md` (раздел «Порядок» и «Решения»)

**Interfaces:**
- Produces: таблицы `outbox` (новая форма), `event_inbox`, `event_cursors`; функция `outbox_notify()`; триггер `outbox_notify` (`AFTER INSERT … FOR EACH STATEMENT`, канал `wf_outbox`). Колонки, на которые опираются Task 3–6:
  - `outbox(id uuid PK, event_type text, schema_version int, aggregate_type text, aggregate_id uuid, aggregate_version bigint, occurred_at timestamptz DEFAULT now(), payload jsonb, trace jsonb NULL, published_at timestamptz NULL)`;
  - `event_inbox(subscriber text, event_id uuid, processed_at timestamptz DEFAULT now(), PK (subscriber, event_id))`;
  - `event_cursors(subscriber text, aggregate_type text, aggregate_id uuid, version bigint, updated_at timestamptz, PK (subscriber, aggregate_type, aggregate_id))`.

Старый `outbox` (из `0001`) пуст во всех окружениях — продюсеров ещё нет, поэтому миграция пересоздаёт таблицу, а не переносит данные. Применённые миграции не правятся.

- [ ] **Step 1: Падающий страж**

Run: `go test ./internal/archtest/ -run TestEverySchemaObjectHasOwner -count=1`
Expected: PASS (пока миграции нет). Создай миграцию (Step 2) и запусти снова — Expected: FAIL с упоминанием `event_inbox`, `event_cursors`, `outbox_notify` без владельца. Это и есть падающий тест задачи.

- [ ] **Step 2: Миграция**

`backend/migrations/0017_platform_events.sql`:

```sql
-- 0017_platform_events.sql
-- События платформы (спека бэкенда §6.5, §11): outbox с конвертом события, inbox подписчиков
-- и курсоры версий агрегатов; NOTIFY будит relay в воркере.
-- Старый outbox из 0001 пуст во всех окружениях (продюсеров не было) — пересоздаём.

-- +goose Up

DROP TABLE outbox;

CREATE TABLE outbox (
    id                uuid PRIMARY KEY,          -- UUIDv7 из Go (platform/id)
    event_type        text        NOT NULL,      -- <модуль>.<факт>
    schema_version    int         NOT NULL CHECK (schema_version > 0),
    aggregate_type    text        NOT NULL,
    aggregate_id      uuid        NOT NULL,
    aggregate_version bigint      NOT NULL CHECK (aggregate_version > 0),
    -- время транзакции из базы, не часы инстанса
    occurred_at       timestamptz NOT NULL DEFAULT now(),
    payload           jsonb       NOT NULL,
    trace             jsonb,                     -- контекст трассировки; заполнится с OTel
    published_at      timestamptz
);

-- relay берёт неопубликованное по порядку; таких строк — доли процента таблицы
CREATE INDEX outbox_unpublished_idx ON outbox (occurred_at, id) WHERE published_at IS NULL;
-- переигровка: события типа начиная с момента
CREATE INDEX outbox_type_idx ON outbox (event_type, occurred_at);
-- чистка опубликованного старше 30 дней
CREATE INDEX outbox_published_idx ON outbox (published_at) WHERE published_at IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION outbox_notify()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  -- один сигнал на оператор: relay сам заберёт всю пачку
  PERFORM pg_notify('wf_outbox', '');
  RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER outbox_notify
    AFTER INSERT ON outbox
    FOR EACH STATEMENT EXECUTE FUNCTION outbox_notify();

-- Подписчик отмечает обработанное событие в своей транзакции: конфликт — уже обработано.
CREATE TABLE event_inbox (
    subscriber   text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber, event_id)
);

CREATE INDEX event_inbox_processed_idx ON event_inbox (processed_at);

-- Последняя применённая версия агрегата у подписчика, следящего за состоянием: событие со
-- строго меньшей версией — устаревшее и отбрасывается.
CREATE TABLE event_cursors (
    subscriber     text        NOT NULL,
    aggregate_type text        NOT NULL,
    aggregate_id   uuid        NOT NULL,
    version        bigint      NOT NULL CHECK (version >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber, aggregate_type, aggregate_id)
);

-- +goose Down

DROP TABLE IF EXISTS event_cursors;
DROP TABLE IF EXISTS event_inbox;
DROP TRIGGER IF EXISTS outbox_notify ON outbox;
DROP FUNCTION IF EXISTS outbox_notify();
DROP TABLE IF EXISTS outbox;

CREATE TABLE outbox (
    id             bigserial PRIMARY KEY,
    aggregate_type text        NOT NULL,
    aggregate_id   uuid        NOT NULL,
    event_type     text        NOT NULL,
    payload        jsonb       NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    published_at   timestamptz,
    attempts       int         NOT NULL DEFAULT 0,
    last_error     text
);

CREATE INDEX outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;
```

- [ ] **Step 3: Владельцы новых объектов**

В `backend/internal/archtest/ownership.go`, блок `// platform` карты `Schema`, после строки `"outbox":           table("platform"),` добавь:

```go
	"event_inbox":      table("platform"),
	"event_cursors":    table("platform"),
```

и после строки `"audit_log_is_append_only": {Function, "platform", false},`:

```go
	"outbox_notify":            {Function, "platform", false},
```

(выравнивание — `gofmt -w internal/archtest/ownership.go`).

- [ ] **Step 4: Страж и миграции проходят**

Run: `go test ./internal/archtest/ ./cmd/migrate/ -count=1`
Expected: PASS (`TestStatusListsMigrations` видит 0017, страж владения доволен).

Проверь откат: `go test ./internal/platform/migrate/ -run TestMigrationsRoundTrip -count=1` — Expected: PASS (тест накатывает и откатывает все миграции, Down из Step 2 восстанавливает старый outbox).

- [ ] **Step 5: README миграций**

В `backend/migrations/README.md`, раздел «Порядок», в блок кода после строки `0011–0016_river_v2..v7.sql  схема очереди River, по файлу на версию` добавь:

```
0017_platform_events.sql события: outbox с конвертом (UUIDv7, версии), inbox, курсоры, NOTIFY
```

В раздел «Решения, которые стоит знать» добавь пункт:

```markdown
- **События (спека бэкенда §6.5).** `outbox.id` — UUIDv7 из Go, `occurred_at` — время транзакции.
  Триггер `outbox_notify` (на оператор, канал `wf_outbox`) будит relay воркера; relay ставит задачу
  River на каждого подписчика и отмечает `published_at` в одной транзакции. Подписчик пишет
  `event_inbox (subscriber, event_id)` в своей транзакции — повтор доставки не задваивает;
  подписчик состояния ведёт `event_cursors` и отбрасывает событие со строго меньшей версией.
  Опубликованный outbox и inbox чистятся через 30 дней.
```

- [ ] **Step 6: Commit**

```bash
git add backend/migrations/0017_platform_events.sql backend/migrations/README.md backend/internal/archtest/ownership.go
git commit -m "Миграция событий: outbox с конвертом, inbox, курсоры, NOTIFY" -- backend/migrations/0017_platform_events.sql backend/migrations/README.md backend/internal/archtest/ownership.go
```

## Фаза B. События

### Task 3: Публикация события и реестр подписчиков

**Files:**
- Modify: `backend/sqlc.yaml` — элемент `eventsdb`
- Create: `backend/internal/platform/events/queries/events.sql`
- Create (сгенерировано): `backend/internal/platform/events/eventsdb/{db.go,models.go,events.sql.go,querier.go}`
- Create: `backend/internal/platform/events/events.go`, `publish_test.go`
- Create: `backend/internal/platform/events/registry.go`, `registry_test.go`

**Interfaces:**
- Consumes: `db.InTx`, `db.TxFrom`, `db.ErrNoTx` (Task 1); `id.New()` (Task 1); таблицы Task 2.
- Produces:
  - `events.Event{Type string; SchemaVersion int; AggregateType string; AggregateID uuid.UUID; AggregateVersion int64; Payload any}`.
  - `events.Envelope{ID uuid.UUID; Type string; SchemaVersion int; AggregateType string; AggregateID uuid.UUID; AggregateVersion int64; OccurredAt time.Time; Payload json.RawMessage}`.
  - `events.Publish(ctx context.Context, e Event) (uuid.UUID, error)` — пишет в `outbox` транзакцией из `ctx`; без неё — `db.ErrNoTx`; невалидное событие — ошибка, обёртывающая `events.ErrInvalidEvent`.
  - `events.Delivery` (`EveryEvent`, `LatestState`), `events.Handler func(ctx context.Context, tx pgx.Tx, e Envelope) error`, `events.Subscription{Subscriber, EventType string; Delivery Delivery; Handle Handler}`.
  - `events.NewRegistry(subs ...Subscription) (*Registry, error)`; `(*Registry).For(eventType string) []Subscription`; `(*Registry).Get(subscriber, eventType string) (Subscription, bool)`.
  - Запросы `eventsdb` (точные имена и типы — после генерации; на них опираются Task 4–6): `InsertEvent(ctx, InsertEventParams) error`, `LockUnpublished(ctx, batch int32) ([]Outbox, error)`, `MarkPublished(ctx, ids []uuid.UUID) error`, `GetEvent(ctx, id uuid.UUID) (Outbox, error)`, `EventIDsForReplay(ctx, EventIDsForReplayParams{EventType string; Since time.Time}) ([]uuid.UUID, error)`, `InsertInbox(ctx, InsertInboxParams{Subscriber string; EventID uuid.UUID}) (int64, error)`, `EnsureCursor(ctx, EnsureCursorParams) error`, `LockCursor(ctx, LockCursorParams) (int64, error)`, `AdvanceCursor(ctx, AdvanceCursorParams) error`, `DeleteOldPublished(ctx, batch int32) (int64, error)`, `DeleteOldInbox(ctx, batch int32) (int64, error)`. Модель `Outbox{ID uuid.UUID; EventType string; SchemaVersion int32; AggregateType string; AggregateID uuid.UUID; AggregateVersion int64; OccurredAt time.Time; Payload []byte; Trace []byte; PublishedAt *time.Time}`.

- [ ] **Step 1: SQL платформы событий**

`backend/internal/platform/events/queries/events.sql`:

```sql
-- События платформы (спека бэкенда §6.5). Владелец outbox, event_inbox, event_cursors — platform.

-- name: InsertEvent :exec
INSERT INTO outbox (id, event_type, schema_version, aggregate_type, aggregate_id, aggregate_version, payload)
VALUES (@id, @event_type, @schema_version, @aggregate_type, @aggregate_id, @aggregate_version, @payload);

-- Пачка для relay; SKIP LOCKED — несколько воркеров берут разные строки.
-- name: LockUnpublished :many
SELECT * FROM outbox
WHERE published_at IS NULL
ORDER BY occurred_at, id
LIMIT @batch
FOR UPDATE SKIP LOCKED;

-- name: MarkPublished :exec
UPDATE outbox SET published_at = now() WHERE id = ANY(@ids::uuid[]);

-- name: GetEvent :one
SELECT * FROM outbox WHERE id = @id;

-- name: EventIDsForReplay :many
SELECT id FROM outbox
WHERE event_type = @event_type AND occurred_at >= @since
ORDER BY occurred_at, id;

-- 0 строк — событие этим подписчиком уже обработано.
-- name: InsertInbox :execrows
INSERT INTO event_inbox (subscriber, event_id) VALUES (@subscriber, @event_id)
ON CONFLICT DO NOTHING;

-- Курсор создаётся заранее, чтобы параллельные события одного агрегата встали в очередь на
-- блокировке строки, а не разошлись на «строки ещё нет».
-- name: EnsureCursor :exec
INSERT INTO event_cursors (subscriber, aggregate_type, aggregate_id, version)
VALUES (@subscriber, @aggregate_type, @aggregate_id, 0)
ON CONFLICT DO NOTHING;

-- name: LockCursor :one
SELECT version FROM event_cursors
WHERE subscriber = @subscriber AND aggregate_type = @aggregate_type AND aggregate_id = @aggregate_id
FOR UPDATE;

-- name: AdvanceCursor :exec
UPDATE event_cursors SET version = @version, updated_at = now()
WHERE subscriber = @subscriber AND aggregate_type = @aggregate_type AND aggregate_id = @aggregate_id
  AND version < @version;

-- Чистка (спека §6.5, §9.3): опубликованное и обработанное старше 30 дней, пачками.
-- name: DeleteOldPublished :execrows
DELETE FROM outbox WHERE id IN (
  SELECT id FROM outbox WHERE published_at < now() - interval '30 days' LIMIT @batch
);

-- name: DeleteOldInbox :execrows
DELETE FROM event_inbox WHERE (subscriber, event_id) IN (
  SELECT subscriber, event_id FROM event_inbox WHERE processed_at < now() - interval '30 days' LIMIT @batch
);
```

- [ ] **Step 2: Элемент sqlc**

В `backend/sqlc.yaml` в конец списка `sql:` добавь:

```yaml
  - engine: postgresql
    schema: migrations/
    queries: internal/platform/events/queries/
    gen:
      go:
        package: eventsdb
        out: internal/platform/events/eventsdb
        sql_package: pgx/v5
        emit_interface: true
        emit_pointers_for_null_types: true
        omit_unused_structs: true
        overrides:
          - db_type: uuid
            go_type: github.com/google/uuid.UUID
          - db_type: uuid
            nullable: true
            go_type: { import: github.com/google/uuid, type: UUID, pointer: true }
          - db_type: timestamptz
            go_type: time.Time
          - db_type: timestamptz
            nullable: true
            go_type: { type: time.Time, pointer: true }
          - db_type: geography
            go_type: string
          - db_type: geography
            nullable: true
            go_type: { type: string, pointer: true }
```

(`geography` — как у `flagsdb`: sqlc видит всю схему.) Run: `./task backend:gen` из корня. Expected: появился `internal/platform/events/eventsdb/`; сверь имена и типы с блоком Interfaces — если sqlc назвал поле иначе (например, `Ids` вместо `IDs`), используй сгенерированное имя во всех задачах и отметь это в отчёте. Run: `go test ./internal/archtest/ -count=1` — Expected: PASS (страж владения видит SQL платформы, пишет он только свои таблицы).

- [ ] **Step 3: Падающие тесты реестра**

`backend/internal/platform/events/registry_test.go`:

```go
package events_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/events"
)

func noop(context.Context, pgx.Tx, events.Envelope) error { return nil }

func sub(name, typ string, d events.Delivery) events.Subscription {
	return events.Subscription{Subscriber: name, EventType: typ, Delivery: d, Handle: noop}
}

func TestRegistryRoutesByType(t *testing.T) {
	r, err := events.NewRegistry(
		sub("stats.count_members", "teams.member_joined", events.EveryEvent),
		sub("notify.roster", "teams.member_joined", events.LatestState),
		sub("notify.roster", "teams.member_left", events.LatestState),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.For("teams.member_joined")); got != 2 {
		t.Fatalf("подписчиков member_joined = %d", got)
	}
	if got := len(r.For("teams.unknown")); got != 0 {
		t.Fatalf("подписчиков неизвестного типа = %d", got)
	}
	if s, ok := r.Get("notify.roster", "teams.member_left"); !ok || s.Delivery != events.LatestState {
		t.Fatalf("Get = %+v, %v", s, ok)
	}
	if _, ok := r.Get("notify.roster", "teams.captain_changed"); ok {
		t.Fatal("Get нашёл несуществующую подписку")
	}
}

// Подсадка ошибок регистрации: каждая ловится своим правилом.
func TestRegistryRejectsInvalid(t *testing.T) {
	cases := map[string]struct {
		subs []events.Subscription
		want string
	}{
		"режим доставки не задан": {[]events.Subscription{sub("stats.x", "teams.member_joined", 0)}, "режим доставки"},
		"имя подписчика без модуля": {[]events.Subscription{sub("count", "teams.member_joined", events.EveryEvent)}, "подписчик"},
		"тип события без модуля":    {[]events.Subscription{sub("stats.x", "member_joined", events.EveryEvent)}, "тип события"},
		"нет обработчика": {[]events.Subscription{{Subscriber: "stats.x", EventType: "teams.member_joined", Delivery: events.EveryEvent}}, "обработчик"},
		"дубль подписки": {[]events.Subscription{
			sub("stats.x", "teams.member_joined", events.EveryEvent),
			sub("stats.x", "teams.member_joined", events.LatestState),
		}, "дважды"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := events.NewRegistry(c.subs...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, ждали «%s»", err, c.want)
			}
		})
	}
}
```

- [ ] **Step 4: Падающие тесты публикации**

`backend/internal/platform/events/publish_test.go`:

```go
package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

func joined(agg uuid.UUID, version int64) events.Event {
	return events.Event{
		Type: "teams.member_joined", SchemaVersion: 1,
		AggregateType: "team", AggregateID: agg, AggregateVersion: version,
		Payload: map[string]any{"team_id": agg.String(), "user_id": id.New().String()},
	}
}

func outboxCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM outbox").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPublishWritesEnvelopeInTransaction(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	agg := id.New()
	var eventID uuid.UUID
	err := db.InTx(ctx, pool, func(ctx context.Context, _ pgx.Tx) error {
		var err error
		eventID, err = events.Publish(ctx, joined(agg, 3))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if eventID.Version() != 7 {
		t.Fatalf("id события не UUIDv7: %s", eventID)
	}
	var (
		typ, aggType string
		aggID        uuid.UUID
		schema       int
		version      int64
		payload      []byte
		occurredSet  bool
		published    *string
	)
	err = pool.QueryRow(ctx, `SELECT event_type, aggregate_type, aggregate_id, schema_version,
		aggregate_version, payload, occurred_at IS NOT NULL, published_at::text FROM outbox WHERE id = $1`, eventID).
		Scan(&typ, &aggType, &aggID, &schema, &version, &payload, &occurredSet, &published)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "teams.member_joined" || aggType != "team" || aggID != agg || schema != 1 || version != 3 ||
		!occurredSet || published != nil {
		t.Fatalf("строка outbox: %s %s %s %d %d %v %v", typ, aggType, aggID, schema, version, occurredSet, published)
	}
	var p map[string]string
	if err := json.Unmarshal(payload, &p); err != nil || p["team_id"] != agg.String() {
		t.Fatalf("payload = %s (%v)", payload, err)
	}
}

func TestPublishOutsideTransactionFails(t *testing.T) {
	_, err := events.Publish(context.Background(), joined(id.New(), 1))
	if !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("err = %v, ждали db.ErrNoTx", err)
	}
}

// Review Focus 4: откат бизнес-транзакции уносит и событие.
func TestPublishRolledBackWithTransaction(t *testing.T) {
	pool := dbtest.NewPool(t)
	_ = db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		if _, err := events.Publish(ctx, joined(id.New(), 1)); err != nil {
			t.Fatal(err)
		}
		return errors.New("бизнес-ошибка")
	})
	if n := outboxCount(t, pool); n != 0 {
		t.Fatalf("после отката в outbox %d строк", n)
	}
}

func TestPublishRejectsInvalidEvent(t *testing.T) {
	pool := dbtest.NewPool(t)
	bad := map[string]func(e *events.Event){
		"тип без модуля":           func(e *events.Event) { e.Type = "member_joined" },
		"нулевая версия схемы":     func(e *events.Event) { e.SchemaVersion = 0 },
		"нулевая версия агрегата":  func(e *events.Event) { e.AggregateVersion = 0 },
		"пустой тип агрегата":      func(e *events.Event) { e.AggregateType = "" },
		"нулевой id агрегата":      func(e *events.Event) { e.AggregateID = uuid.Nil },
		"payload не объект":        func(e *events.Event) { e.Payload = []int{1, 2} },
		"payload не сериализуется": func(e *events.Event) { e.Payload = map[string]any{"f": func() {}} },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			e := joined(id.New(), 1)
			mutate(&e)
			err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
				_, err := events.Publish(ctx, e)
				return err
			})
			if !errors.Is(err, events.ErrInvalidEvent) {
				t.Fatalf("err = %v, ждали ErrInvalidEvent", err)
			}
		})
	}
}
```

- [ ] **Step 5: Убедиться, что падает**

Run: `go test ./internal/platform/events/ -count=1`
Expected: FAIL — `undefined: events.NewRegistry` и др.

- [ ] **Step 6: Реализация**

`backend/internal/platform/events/events.go`:

```go
// Package events — события модулей: outbox → relay → River → inbox (спека бэкенда §6.5).
// Модуль публикует событие в своей бизнес-транзакции (Publish), relay воркера раскладывает его
// подписчикам, подписчик обрабатывает его ровно один раз (event_inbox) и, если следит за
// состоянием агрегата, отбрасывает устаревшие версии (event_cursors).
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events/eventsdb"
	"wf/backend/internal/platform/id"
)

// ErrInvalidEvent — событие не прошло проверку конверта.
var ErrInvalidEvent = errors.New("events: невалидное событие")

// <модуль>.<имя> — у событий (факт в прошедшем времени) и у подписчиков
var dotted = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Event — то, что публикует модуль. Payload — только идентификаторы и непрофильные значения,
// без персональных данных (§4.6); сериализуется в JSON-объект.
type Event struct {
	Type             string
	SchemaVersion    int
	AggregateType    string
	AggregateID      uuid.UUID
	AggregateVersion int64
	Payload          any
}

// Envelope — событие, как его получает подписчик.
type Envelope struct {
	ID               uuid.UUID
	Type             string
	SchemaVersion    int
	AggregateType    string
	AggregateID      uuid.UUID
	AggregateVersion int64
	OccurredAt       time.Time
	Payload          json.RawMessage
}

// Publish пишет событие в outbox транзакцией из ctx (db.InTx): событие уходит, только если
// закоммитились данные. occurred_at — время транзакции из базы.
func Publish(ctx context.Context, e Event) (uuid.UUID, error) {
	tx, ok := db.TxFrom(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoTx
	}
	payload, err := validate(e)
	if err != nil {
		return uuid.Nil, err
	}
	eventID := id.New()
	err = eventsdb.New(tx).InsertEvent(ctx, eventsdb.InsertEventParams{
		ID:               eventID,
		EventType:        e.Type,
		SchemaVersion:    int32(e.SchemaVersion), //nolint:gosec // проверено validate: 1..MaxInt32
		AggregateType:    e.AggregateType,
		AggregateID:      e.AggregateID,
		AggregateVersion: e.AggregateVersion,
		Payload:          payload,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("events: outbox: %w", err)
	}
	return eventID, nil
}

func validate(e Event) ([]byte, error) {
	switch {
	case !dotted.MatchString(e.Type):
		return nil, fmt.Errorf("%w: тип %q — нужен <модуль>.<факт>", ErrInvalidEvent, e.Type)
	case e.SchemaVersion < 1 || e.SchemaVersion > 1<<31-1:
		return nil, fmt.Errorf("%w: версия схемы %d", ErrInvalidEvent, e.SchemaVersion)
	case e.AggregateType == "":
		return nil, fmt.Errorf("%w: пустой тип агрегата", ErrInvalidEvent)
	case e.AggregateID == uuid.Nil:
		return nil, fmt.Errorf("%w: нулевой id агрегата", ErrInvalidEvent)
	case e.AggregateVersion < 1:
		return nil, fmt.Errorf("%w: версия агрегата %d", ErrInvalidEvent, e.AggregateVersion)
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrInvalidEvent, err) //nolint:errorlint // причина — текстом, тип ошибки — ErrInvalidEvent
	}
	if len(payload) == 0 || payload[0] != '{' {
		return nil, fmt.Errorf("%w: payload — не JSON-объект", ErrInvalidEvent)
	}
	return payload, nil
}

func envelope(o eventsdb.Outbox) Envelope {
	return Envelope{
		ID: o.ID, Type: o.EventType, SchemaVersion: int(o.SchemaVersion),
		AggregateType: o.AggregateType, AggregateID: o.AggregateID, AggregateVersion: o.AggregateVersion,
		OccurredAt: o.OccurredAt, Payload: o.Payload,
	}
}
```

`backend/internal/platform/events/registry.go`:

```go
package events

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Delivery — как подписчик относится к порядку событий. Порядок доставки между задачами не
// гарантирован даже внутри агрегата (§6.5), поэтому режим выбирается явно.
type Delivery int

const (
	_ Delivery = iota
	// EveryEvent — каждое событие применяется (счётчики, начисления): старое не отбрасывается.
	EveryEvent
	// LatestState — подписчик синхронизирует состояние агрегата: событие со строго меньшей
	// версией, чем уже применённая, отбрасывается (event_cursors). События одной версии
	// (одна транзакция агрегата) применяются все.
	LatestState
)

// Handler обрабатывает событие в транзакции вместе с отметкой event_inbox: ошибка откатывает
// и изменения подписчика, и отметку — задача River повторится. tx лежит и в ctx (db.TxFrom),
// поэтому обработчик может публиковать свои события.
type Handler func(ctx context.Context, tx pgx.Tx, e Envelope) error

// Subscription — подписчик (<модуль>.<имя>) на один тип события.
type Subscription struct {
	Subscriber string
	EventType  string
	Delivery   Delivery
	Handle     Handler
}

type subKey struct{ subscriber, eventType string }

// Registry — подписки воркера. Собирается один раз при старте; после — только читается.
type Registry struct {
	byType map[string][]Subscription
	byKey  map[subKey]Subscription
}

func NewRegistry(subs ...Subscription) (*Registry, error) {
	r := &Registry{byType: map[string][]Subscription{}, byKey: map[subKey]Subscription{}}
	var errs []error
	for _, s := range subs {
		k := subKey{s.Subscriber, s.EventType}
		switch {
		case !dotted.MatchString(s.Subscriber):
			errs = append(errs, fmt.Errorf("подписчик %q — нужен <модуль>.<имя>", s.Subscriber))
		case !dotted.MatchString(s.EventType):
			errs = append(errs, fmt.Errorf("%s: тип события %q — нужен <модуль>.<факт>", s.Subscriber, s.EventType))
		case s.Delivery != EveryEvent && s.Delivery != LatestState:
			errs = append(errs, fmt.Errorf("%s: не задан режим доставки (EveryEvent или LatestState)", s.Subscriber))
		case s.Handle == nil:
			errs = append(errs, fmt.Errorf("%s: нет обработчика", s.Subscriber))
		default:
			if _, dup := r.byKey[k]; dup {
				errs = append(errs, fmt.Errorf("%s подписан на %s дважды", s.Subscriber, s.EventType))
				continue
			}
			r.byKey[k] = s
			r.byType[s.EventType] = append(r.byType[s.EventType], s)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("events: реестр: %w", err)
	}
	return r, nil
}

// For — подписчики типа события.
func (r *Registry) For(eventType string) []Subscription { return r.byType[eventType] }

// Get — подписка подписчика на тип события.
func (r *Registry) Get(subscriber, eventType string) (Subscription, bool) {
	s, ok := r.byKey[subKey{subscriber, eventType}]
	return s, ok
}
```

- [ ] **Step 7: Тесты проходят**

Run: `go test ./internal/platform/events/ ./internal/archtest/ -count=1`
Expected: PASS. Если линтер-комментарий `//nolint:gosec` окажется лишним — оставь: lint запускается в CI.

- [ ] **Step 8: Подсадка бага**

Временно убери в `registry.go` ветку `case s.Delivery != EveryEvent && s.Delivery != LatestState:` → Run: `go test ./internal/platform/events/ -run TestRegistryRejectsInvalid -count=1` → Expected: FAIL «режим доставки не задан». Верни.

- [ ] **Step 9: Commit**

```bash
git add backend/sqlc.yaml backend/internal/platform/events
git commit -m "События: Publish в outbox из транзакции ctx и реестр подписчиков" -- backend/sqlc.yaml backend/internal/platform/events
```

### Task 4: Доставка подписчику — inbox и курсор версии

**Files:**
- Create: `backend/internal/platform/events/deliver.go`, `deliver_test.go`

**Interfaces:**
- Consumes: `eventsdb.*` (Task 3), `Registry`, `Envelope`, `envelope()` (Task 3), `db.InTx` (Task 1).
- Produces:
  - `events.QueueEvents = "events"` — очередь доставки (§9.1).
  - `events.DeliverArgs{V int; EventID uuid.UUID; Subscriber string}` — `Kind() == "events.deliver"`, `InsertOpts()` — очередь `events`, `MaxAttempts: 20`, уникальность по аргументам; конструктор `events.NewDeliverArgs(eventID uuid.UUID, subscriber string) DeliverArgs` ставит `V: 1`.
  - `events.NewDeliverWorker(pool db.TxStarter, q eventsdb.DBTX, reg *Registry) *DeliverWorker` — `river.Worker[DeliverArgs]`; `q` — пул для чтения события вне транзакции.
  - `events.AddWorkers(w *river.Workers, pool *pgxpool.Pool, reg *Registry)` — регистрирует доставку (чистку добавит Task 6).

- [ ] **Step 1: Падающие тесты доставки**

`backend/internal/platform/events/deliver_test.go`:

```go
package events_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

// effects — побочный эффект подписчика в его транзакции: по нему видно, сколько раз он сработал.
func effects(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"CREATE TABLE test_effects (subscriber text, event_id uuid, version bigint)"); err != nil {
		t.Fatal(err)
	}
}

func record(name string) events.Handler {
	return func(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
		_, err := tx.Exec(ctx, "INSERT INTO test_effects VALUES ($1, $2, $3)", name, e.ID, e.AggregateVersion)
		return err
	}
}

func effectCount(t *testing.T, pool *pgxpool.Pool, subscriber string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM test_effects WHERE subscriber = $1", subscriber).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func publish(t *testing.T, pool *pgxpool.Pool, e events.Event) uuid.UUID {
	t.Helper()
	var eventID uuid.UUID
	if err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		var err error
		eventID, err = events.Publish(ctx, e)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return eventID
}

func job(eventID uuid.UUID, subscriber string) *river.Job[events.DeliverArgs] {
	return &river.Job[events.DeliverArgs]{JobRow: &rivertype.JobRow{}, Args: events.NewDeliverArgs(eventID, subscriber)}
}

func TestDeliverArgsConventions(t *testing.T) {
	a := events.NewDeliverArgs(id.New(), "stats.count")
	opts := a.InsertOpts()
	if a.Kind() != "events.deliver" || a.V != 1 || opts.Queue != events.QueueEvents ||
		!opts.UniqueOpts.ByArgs || opts.MaxAttempts == 0 {
		t.Fatalf("аргументы доставки: %+v, опции: %+v", a, opts)
	}
}

// Повтор доставки (River повторил задачу, relay переразложил) не задваивает обработку.
func TestRedeliveryIsProcessedOnce(t *testing.T) {
	pool := dbtest.NewPool(t)
	effects(t, pool)
	reg, err := events.NewRegistry(events.Subscription{
		Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: record("stats.count"),
	})
	if err != nil {
		t.Fatal(err)
	}
	w := events.NewDeliverWorker(pool, pool, reg)
	eventID := publish(t, pool, joined(id.New(), 1))
	for range 3 {
		if err := w.Work(context.Background(), job(eventID, "stats.count")); err != nil {
			t.Fatal(err)
		}
	}
	if n := effectCount(t, pool, "stats.count"); n != 1 {
		t.Fatalf("обработано %d раз", n)
	}
}

// Review Focus 3: ошибка обработчика откатывает и отметку inbox — повтор обрабатывает заново.
func TestHandlerErrorLeavesNoInbox(t *testing.T) {
	pool := dbtest.NewPool(t)
	effects(t, pool)
	var fail atomic.Bool
	fail.Store(true)
	h := func(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
		if err := record("stats.count")(ctx, tx, e); err != nil {
			return err
		}
		if fail.Load() {
			return errors.New("временный сбой")
		}
		return nil
	}
	reg, _ := events.NewRegistry(events.Subscription{
		Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: h,
	})
	w := events.NewDeliverWorker(pool, pool, reg)
	eventID := publish(t, pool, joined(id.New(), 1))
	if err := w.Work(context.Background(), job(eventID, "stats.count")); err == nil {
		t.Fatal("ошибка обработчика проглочена")
	}
	if n := effectCount(t, pool, "stats.count"); n != 0 {
		t.Fatalf("эффект остался после ошибки: %d", n)
	}
	fail.Store(false)
	if err := w.Work(context.Background(), job(eventID, "stats.count")); err != nil {
		t.Fatal(err)
	}
	if n := effectCount(t, pool, "stats.count"); n != 1 {
		t.Fatalf("после повтора обработано %d раз", n)
	}
}

// LatestState отбрасывает событие со строго меньшей версией; EveryEvent применяет всё;
// события одной версии (одна транзакция агрегата) применяются оба (Решения контроллера).
func TestDeliveryModesAndStaleVersions(t *testing.T) {
	pool := dbtest.NewPool(t)
	effects(t, pool)
	reg, err := events.NewRegistry(
		events.Subscription{Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: record("stats.count")},
		events.Subscription{Subscriber: "notify.roster", EventType: "teams.member_joined", Delivery: events.LatestState, Handle: record("notify.roster")},
	)
	if err != nil {
		t.Fatal(err)
	}
	w := events.NewDeliverWorker(pool, pool, reg)
	agg := id.New()
	v2 := publish(t, pool, joined(agg, 2))
	v2bis := publish(t, pool, joined(agg, 2))
	v1 := publish(t, pool, joined(agg, 1))
	// доставка в «неправильном» порядке: сначала новое, потом старое
	for _, eventID := range []uuid.UUID{v2, v2bis, v1} {
		for _, s := range []string{"stats.count", "notify.roster"} {
			if err := w.Work(context.Background(), job(eventID, s)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n := effectCount(t, pool, "stats.count"); n != 3 {
		t.Fatalf("EveryEvent применил %d из 3", n)
	}
	if n := effectCount(t, pool, "notify.roster"); n != 2 {
		t.Fatalf("LatestState применил %d, ждали 2 (v2 и v2bis, без устаревшего v1)", n)
	}
	var cursor int64
	if err := pool.QueryRow(context.Background(),
		"SELECT version FROM event_cursors WHERE subscriber = 'notify.roster' AND aggregate_id = $1", agg).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 2 {
		t.Fatalf("курсор = %d", cursor)
	}
	var stale int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM event_inbox WHERE subscriber = 'notify.roster' AND event_id = $1", v1).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 1 {
		t.Fatal("устаревшее событие не отмечено в inbox — его доставка повторялась бы")
	}
}

// Неизвестный подписчик (убран в новом релизе) и удалённое чисткой событие — отмена задачи,
// а не 20 бессмысленных повторов.
func TestUnknownSubscriberOrEventCancelsJob(t *testing.T) {
	pool := dbtest.NewPool(t)
	reg, _ := events.NewRegistry()
	w := events.NewDeliverWorker(pool, pool, reg)
	eventID := publish(t, pool, joined(id.New(), 1))
	for name, j := range map[string]*river.Job[events.DeliverArgs]{
		"подписчик":  job(eventID, "stats.gone"),
		"событие":    job(id.New(), "stats.count"),
	} {
		var cancel *river.JobCancelError
		if err := w.Work(context.Background(), j); !errors.As(err, &cancel) {
			t.Errorf("%s: err = %v, ждали river.JobCancel", name, err)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что падает**

Run: `go test ./internal/platform/events/ -run "Deliver|Redelivery|HandlerError|Stale|Unknown" -count=1`
Expected: FAIL — `undefined: events.NewDeliverWorker`.

Проверь имя типа ошибки отмены в River 0.47: `grep -n "type JobCancelError" $(go env GOMODCACHE)/github.com/riverqueue/river@v0.47.0/*.go`. Если тип называется иначе — используй его в тесте (в 0.47 — `river.JobCancelError`, конструктор `river.JobCancel(err)`).

- [ ] **Step 3: Реализация**

`backend/internal/platform/events/deliver.go`:

```go
package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events/eventsdb"
)

// QueueEvents — очередь доставки событий подписчикам (спека §9.1).
const QueueEvents = "events"

// DeliverArgs — доставить событие одному подписчику. Только id: событие читается из outbox
// (хранится 30 дней), персональных данных в аргументах нет (§9.5). V — версия аргументов.
type DeliverArgs struct {
	V          int       `json:"v"`
	EventID    uuid.UUID `json:"event_id"`
	Subscriber string    `json:"subscriber"`
}

func NewDeliverArgs(eventID uuid.UUID, subscriber string) DeliverArgs {
	return DeliverArgs{V: 1, EventID: eventID, Subscriber: subscriber}
}

func (DeliverArgs) Kind() string { return "events.deliver" }

// InsertOpts: уникальность по аргументам — relay и переигровка не ставят одну доставку дважды.
func (DeliverArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEvents, MaxAttempts: 20, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

type DeliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	pool db.TxStarter
	q    *eventsdb.Queries
	reg  *Registry
}

func NewDeliverWorker(pool db.TxStarter, q eventsdb.DBTX, reg *Registry) *DeliverWorker {
	return &DeliverWorker{pool: pool, q: eventsdb.New(q), reg: reg}
}

// Timeout — одна доставка не держит слот очереди дольше минуты.
func (*DeliverWorker) Timeout(*river.Job[DeliverArgs]) time.Duration { return time.Minute }

func (w *DeliverWorker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	row, err := w.q.GetEvent(ctx, job.Args.EventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return river.JobCancel(fmt.Errorf("events: событие %s удалено чисткой", job.Args.EventID))
	}
	if err != nil {
		return err
	}
	sub, ok := w.reg.Get(job.Args.Subscriber, row.EventType)
	if !ok {
		return river.JobCancel(fmt.Errorf("events: нет подписки %s на %s", job.Args.Subscriber, row.EventType))
	}
	e := envelope(row)
	return db.InTx(ctx, w.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := eventsdb.New(tx)
		fresh, err := q.InsertInbox(ctx, eventsdb.InsertInboxParams{Subscriber: sub.Subscriber, EventID: e.ID})
		if err != nil || fresh == 0 {
			return err // 0 — уже обработано этим подписчиком
		}
		if sub.Delivery == LatestState {
			key := eventsdb.EnsureCursorParams{Subscriber: sub.Subscriber, AggregateType: e.AggregateType, AggregateID: e.AggregateID}
			if err := q.EnsureCursor(ctx, key); err != nil {
				return err
			}
			applied, err := q.LockCursor(ctx, eventsdb.LockCursorParams(key))
			if err != nil {
				return err
			}
			if e.AggregateVersion < applied {
				return nil // устаревшее: отметка inbox остаётся, эффекта нет
			}
		}
		if err := sub.Handle(ctx, tx, e); err != nil {
			return fmt.Errorf("events: %s: %w", sub.Subscriber, err)
		}
		if sub.Delivery == LatestState {
			return q.AdvanceCursor(ctx, eventsdb.AdvanceCursorParams{
				Version: e.AggregateVersion, Subscriber: sub.Subscriber,
				AggregateType: e.AggregateType, AggregateID: e.AggregateID,
			})
		}
		return nil
	})
}

// AddWorkers регистрирует задачи платформы событий в реестре воркера.
func AddWorkers(w *river.Workers, pool *pgxpool.Pool, reg *Registry) {
	river.AddWorker(w, NewDeliverWorker(pool, pool, reg))
}
```

(`eventsdb.LockCursorParams(key)` — преобразование типа: у `EnsureCursorParams` и `LockCursorParams` одинаковые поля в одном порядке. Если sqlc сгенерировал разный порядок полей — собери `LockCursorParams` явно.)

- [ ] **Step 4: Тесты проходят**

Run: `go test ./internal/platform/events/ -count=1`
Expected: PASS.

- [ ] **Step 5: Подсадка бага**

Временно замени `if e.AggregateVersion < applied {` на `if e.AggregateVersion <= applied {` → `TestDeliveryModesAndStaleVersions` FAIL («применил 1, ждали 2»). Затем верни и замени `if err != nil || fresh == 0 {` на `if err != nil {` → `TestRedeliveryIsProcessedOnce` FAIL. Верни — PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/platform/events
git commit -m "События: доставка подписчику через inbox и курсор версии агрегата" -- backend/internal/platform/events
```

### Task 5: Relay и сквозной тест событий

**Files:**
- Create: `backend/internal/platform/events/relay.go`, `relay_test.go`
- Create: `backend/internal/platform/events/e2e_test.go`

**Interfaces:**
- Consumes: `eventsdb.LockUnpublished`, `eventsdb.MarkPublished` (Task 3); `NewDeliverArgs`, `AddWorkers`, `QueueEvents` (Task 4); `db.InTx` (Task 1).
- Produces:
  - `events.Channel = "wf_outbox"` — канал `NOTIFY` (Task 2).
  - `events.Inserter interface { InsertManyTx(ctx context.Context, tx pgx.Tx, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) }` — его реализует `*river.Client[pgx.Tx]`.
  - `events.RelayConfig{Batch int; Poll time.Duration}`.
  - `events.NewRelay(pool *pgxpool.Pool, ins Inserter, reg *Registry, log *slog.Logger, cfg RelayConfig) *Relay`.
  - `(*Relay).Drain(ctx) (int, error)` — публикует всё неопубликованное пачками, возвращает число событий.
  - `(*Relay).Run(ctx) error` — до отмены `ctx`: `Drain` при старте, по `NOTIFY` и по таймеру `Poll`; `LISTEN` — на отдельном соединении вне пула, с переподключением (1 с → 30 с); возвращает `nil` после отмены и выхода слушателя.

- [ ] **Step 1: Падающие тесты relay**

`backend/internal/platform/events/relay_test.go`:

```go
package events_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

var discard = slog.New(slog.DiscardHandler)

// inserter — клиент River только для вставки: задачи ложатся в river_job, никто их не исполняет.
func inserter(t *testing.T, pool *pgxpool.Pool) *river.Client[pgx.Tx] {
	t.Helper()
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: discard})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func deliveries(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM river_job WHERE kind = 'events.deliver'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func unpublished(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func twoSubscribers(t *testing.T) *events.Registry {
	t.Helper()
	reg, err := events.NewRegistry(
		events.Subscription{Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: noop},
		events.Subscription{Subscriber: "notify.roster", EventType: "teams.member_joined", Delivery: events.LatestState, Handle: noop},
	)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func left(agg uuid.UUID) events.Event {
	e := joined(agg, 1)
	e.Type = "teams.member_left"
	return e
}

func TestDrainEnqueuesPerSubscriberAndMarksPublished(t *testing.T) {
	pool := dbtest.NewPool(t)
	for range 3 {
		publish(t, pool, joined(id.New(), 1))
	}
	publish(t, pool, left(id.New())) // без подписчиков — просто отмечается
	r := events.NewRelay(pool, inserter(t, pool), twoSubscribers(t), discard, events.RelayConfig{Batch: 2, Poll: time.Hour})
	n, err := r.Drain(context.Background())
	if err != nil || n != 4 {
		t.Fatalf("Drain = %d, %v", n, err)
	}
	if got := deliveries(t, pool); got != 6 {
		t.Fatalf("задач доставки %d, ждали 3 события × 2 подписчика", got)
	}
	if got := unpublished(t, pool); got != 0 {
		t.Fatalf("неопубликованных %d", got)
	}
}

type failingInserter struct{}

func (failingInserter) InsertManyTx(context.Context, pgx.Tx, []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) {
	return nil, errors.New("river недоступен")
}

// Review Focus 1: постановка задач и отметка published_at — одна транзакция.
func TestInsertErrorRollsBackMarking(t *testing.T) {
	pool := dbtest.NewPool(t)
	publish(t, pool, joined(id.New(), 1))
	r := events.NewRelay(pool, failingInserter{}, twoSubscribers(t), discard, events.RelayConfig{Batch: 10, Poll: time.Hour})
	if _, err := r.Drain(context.Background()); err == nil {
		t.Fatal("ошибка вставки задач проглочена")
	}
	if got := unpublished(t, pool); got != 1 {
		t.Fatalf("событие отмечено опубликованным без задач: неопубликованных %d", got)
	}
}

// Review Focus 2: два relay (два инстанса воркера) не ставят одно событие дважды.
func TestTwoRelaysEnqueueEachEventOnce(t *testing.T) {
	pool := dbtest.NewPool(t)
	for range 200 {
		publish(t, pool, joined(id.New(), 1))
	}
	reg, _ := events.NewRegistry(events.Subscription{
		Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: noop,
	})
	ins := inserter(t, pool)
	var wg sync.WaitGroup
	for range 2 {
		r := events.NewRelay(pool, ins, reg, discard, events.RelayConfig{Batch: 7, Poll: time.Hour})
		wg.Go(func() {
			if _, err := r.Drain(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var total, distinct int
	if err := pool.QueryRow(context.Background(), `SELECT count(*), count(DISTINCT args->>'event_id')
		FROM river_job WHERE kind = 'events.deliver'`).Scan(&total, &distinct); err != nil {
		t.Fatal(err)
	}
	if total != 200 || distinct != 200 {
		t.Fatalf("задач %d, разных событий %d — ждали по 200", total, distinct)
	}
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("не дождались: %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func runRelay(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	r := events.NewRelay(pool, inserter(t, pool), twoSubscribers(t), discard, events.RelayConfig{Batch: 10, Poll: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
}

// Review Focus 5: опрос — раз в час, доставка — по NOTIFY.
func TestRunWakesOnNotify(t *testing.T) {
	pool := dbtest.NewPool(t)
	runRelay(t, pool)
	publish(t, pool, joined(id.New(), 1))
	waitFor(t, "задачи по NOTIFY", 5*time.Second, func() bool { return deliveries(t, pool) == 2 })
}

// Review Focus 5: обрыв LISTEN-соединения — relay переподключается и не теряет событий.
func TestRunSurvivesListenerDrop(t *testing.T) {
	pool := dbtest.NewPool(t)
	runRelay(t, pool)
	publish(t, pool, joined(id.New(), 1))
	waitFor(t, "первую доставку", 5*time.Second, func() bool { return deliveries(t, pool) == 2 })
	waitFor(t, "обрыв слушателя", 5*time.Second, func() bool {
		var killed int
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM (SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity WHERE query = 'LISTEN `+events.Channel+`' AND pid <> pg_backend_pid()) k`).Scan(&killed)
		return killed > 0
	})
	publish(t, pool, joined(id.New(), 1))
	waitFor(t, "доставку после переподключения", 10*time.Second, func() bool { return deliveries(t, pool) == 4 })
}
```

- [ ] **Step 2: Убедиться, что падает**

Run: `go test ./internal/platform/events/ -run "Drain|InsertError|TwoRelays|Run" -count=1`
Expected: FAIL — `undefined: events.NewRelay`.

- [ ] **Step 3: Реализация**

`backend/internal/platform/events/relay.go`:

```go
package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events/eventsdb"
)

// Channel — канал NOTIFY триггера outbox_notify (миграция 0017).
const Channel = "wf_outbox"

// Inserter ставит задачи River в транзакции; его реализует *river.Client[pgx.Tx].
type Inserter interface {
	InsertManyTx(ctx context.Context, tx pgx.Tx, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

type RelayConfig struct {
	Batch int           // событий за одну транзакцию
	Poll  time.Duration // страховочный опрос, если NOTIFY потерялся
}

// Relay раскладывает события outbox подписчикам: на каждого — задача River в той же
// транзакции, что и отметка published_at (спека §6.5). Инстансов воркера может быть сколько
// угодно: строки делятся через FOR UPDATE SKIP LOCKED, задачи уникальны по аргументам.
type Relay struct {
	pool *pgxpool.Pool
	ins  Inserter
	reg  *Registry
	log  *slog.Logger
	cfg  RelayConfig
}

func NewRelay(pool *pgxpool.Pool, ins Inserter, reg *Registry, log *slog.Logger, cfg RelayConfig) *Relay {
	return &Relay{pool: pool, ins: ins, reg: reg, log: log, cfg: cfg}
}

// Run работает до отмены ctx. LISTEN держится на отдельном соединении вне пула: с PgBouncer
// в transaction mode он несовместим (docs/deploy-dev.md).
func (r *Relay) Run(ctx context.Context) error {
	wake := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Go(func() { r.listen(ctx, wake) })
	defer wg.Wait()
	poll := time.NewTicker(r.cfg.Poll)
	defer poll.Stop()
	for {
		if _, err := r.Drain(ctx); err != nil && ctx.Err() == nil {
			r.log.ErrorContext(ctx, "events: relay", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-poll.C:
		}
	}
}

// Drain публикует всё неопубликованное пачками по cfg.Batch.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	total := 0
	for {
		n, err := r.publishBatch(ctx)
		total += n
		if err != nil || n < r.cfg.Batch {
			return total, err
		}
	}
}

func (r *Relay) publishBatch(ctx context.Context) (int, error) {
	var n int
	err := db.InTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := eventsdb.New(tx)
		rows, err := q.LockUnpublished(ctx, int32(r.cfg.Batch)) //nolint:gosec // размер пачки из конфига, малый
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]uuid.UUID, 0, len(rows))
		var params []river.InsertManyParams
		for _, row := range rows {
			ids = append(ids, row.ID)
			for _, s := range r.reg.For(row.EventType) {
				// опции берутся из DeliverArgs.InsertOpts: очередь events, уникальность по аргументам
				params = append(params, river.InsertManyParams{Args: NewDeliverArgs(row.ID, s.Subscriber)})
			}
		}
		if len(params) > 0 {
			if _, err := r.ins.InsertManyTx(ctx, tx, params); err != nil {
				return fmt.Errorf("events: задачи доставки: %w", err)
			}
		}
		if err := q.MarkPublished(ctx, ids); err != nil {
			return err
		}
		n = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// listen будит Run по NOTIFY; обрыв — переподключение с растущей паузой. После каждого
// подключения — сигнал: события, вставленные без слушателя, забираются сразу.
func (r *Relay) listen(ctx context.Context, wake chan<- struct{}) {
	backoff := time.Second
	for {
		connected, err := r.listenOnce(ctx, wake)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = time.Second
		}
		r.log.WarnContext(ctx, "events: LISTEN оборвался, переподключаюсь", "err", err, "pause", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (r *Relay) listenOnce(ctx context.Context, wake chan<- struct{}) (bool, error) {
	conn, err := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
	if err != nil {
		return false, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return false, err
	}
	signal(wake)
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return true, err
		}
		signal(wake)
	}
}

func signal(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default: // сигнал уже ждёт — Drain заберёт всё разом
	}
}
```

- [ ] **Step 4: Тесты relay проходят**

Run: `go test ./internal/platform/events/ -count=1`
Expected: PASS.

- [ ] **Step 5: Сквозной тест — критерий готовности §1 п. 4**

`backend/internal/platform/events/e2e_test.go`:

```go
package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Спека §1 п. 4: событие из outbox доходит до двух подписчиков через relay и River, повтор
// доставки не задваивает обработку, устаревшая версия агрегата отбрасывается.
func TestEventReachesSubscribersThroughRelayAndRiver(t *testing.T) {
	pool := dbtest.NewPool(t)
	effects(t, pool)
	reg, err := events.NewRegistry(
		events.Subscription{Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: record("stats.count")},
		events.Subscription{Subscriber: "notify.roster", EventType: "teams.member_joined", Delivery: events.LatestState, Handle: record("notify.roster")},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := worker(t, pool, reg)
	completed, stop := client.Subscribe(river.EventKindJobCompleted)
	defer stop()
	waitDone := func(n int) {
		t.Helper()
		for got := 0; got < n; {
			select {
			case e := <-completed:
				if e.Job.Kind == "events.deliver" {
					got++
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("выполнено %d доставок из %d", got, n)
			}
		}
	}
	runRelayWith(t, pool, client, reg)

	agg := id.New()
	publish(t, pool, joined(agg, 2))
	waitDone(2)
	publish(t, pool, joined(agg, 1)) // пришло позже, но версия старше
	waitDone(2)
	if s, n := effectCount(t, pool, "stats.count"), effectCount(t, pool, "notify.roster"); s != 2 || n != 1 {
		t.Fatalf("эффекты: stats.count = %d (ждали 2), notify.roster = %d (ждали 1 — v1 устарела)", s, n)
	}

	// повторная доставка тех же событий через всю цепочку: relay → River → подписчик
	if _, err := pool.Exec(context.Background(), "DELETE FROM river_job WHERE kind = 'events.deliver'"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "UPDATE outbox SET published_at = NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "SELECT pg_notify('"+events.Channel+"', '')"); err != nil {
		t.Fatal(err)
	}
	waitDone(4)
	if s, n := effectCount(t, pool, "stats.count"), effectCount(t, pool, "notify.roster"); s != 2 || n != 1 {
		t.Fatalf("повтор задвоил: stats.count = %d, notify.roster = %d", s, n)
	}
}

func worker(t *testing.T, pool *pgxpool.Pool, reg *events.Registry) *river.Client[pgx.Tx] {
	t.Helper()
	w := river.NewWorkers()
	events.AddWorkers(w, pool, reg)
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{events.QueueEvents: {MaxWorkers: 4}},
		Workers: w,
		Logger:  discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(ctx)
	})
	return client
}

func runRelayWith(t *testing.T, pool *pgxpool.Pool, ins events.Inserter, reg *events.Registry) {
	t.Helper()
	r := events.NewRelay(pool, ins, reg, discard, events.RelayConfig{Batch: 10, Poll: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}
```

`runRelay` из `relay_test.go` перепиши через `runRelayWith(t, pool, inserter(t, pool), twoSubscribers(t))`, чтобы не дублировать запуск.

Run: `go test ./internal/platform/events/ -count=1`
Expected: PASS. Порядок `t.Cleanup` — LIFO: relay останавливается раньше клиента River, а клиент — раньше, чем `dbtest` закроет пул.

- [ ] **Step 6: Подсадка бага**

1. Удали `FOR UPDATE SKIP LOCKED` из `LockUnpublished` в `queries/events.sql`, `./task backend:gen` → `TestTwoRelaysEnqueueEachEventOnce` FAIL (задач больше 200 или ошибка уникальности). Верни SQL и сгенерируй снова.
2. В `listenOnce` убери `signal(wake)` после `LISTEN` → `TestRunSurvivesListenerDrop` становится нестабильным или падает (событие, вставленное в паузе переподключения, ждёт часового опроса). Верни.

Все тесты пакета — PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/platform/events
git commit -m "События: relay по NOTIFY с опросом и сквозной тест доставки через River" -- backend/internal/platform/events
```

### Task 6: Очереди воркера, чистка событий и запуск relay

**Files:**
- Modify: `backend/internal/platform/config/config.go`, `config_test.go`
- Modify: `backend/internal/platform/queue/queue.go`, `queue_test.go`
- Create: `backend/internal/platform/events/cleanup.go`, `cleanup_test.go`
- Modify: `backend/internal/platform/events/deliver.go` (`QueueEvents`, `AddWorkers`)
- Modify: `backend/cmd/worker/main.go`, `main_test.go`
- Modify: `backend/.env.example`; локальный `backend/.env` — скриптом

**Interfaces:**
- Consumes: `events.NewRelay`, `events.RelayConfig`, `events.AddWorkers`, `events.NewRegistry` (Task 3–5).
- Produces:
  - `config.Queues{Events, Lifecycle, Notify, Mail, Stats, Media, Maintenance int}` (env `WORKER_QUEUE_<ИМЯ>`), `config.Relay{Batch int; Poll time.Duration}` (env `WORKER_RELAY_BATCH`, `WORKER_RELAY_POLL`); `config.Worker{Log; DB; Queues; Relay}` — поле `MaxWorkers` удаляется.
  - `queue.Events = "events"`, `queue.Lifecycle`, `queue.Notify`, `queue.Mail`, `queue.Stats`, `queue.Media`, `queue.Maintenance` — имена очередей §9.1; `queue.Config(c config.Queues) map[string]river.QueueConfig`; `queue.NewClient(pool *pgxpool.Pool, workers *river.Workers, c config.Queues, periodic []*river.PeriodicJob, log *slog.Logger) (*river.Client[pgx.Tx], error)`.
  - `events.QueueEvents` становится `= queue.Events`.
  - `events.CleanupArgs{V int}` (`Kind() == "events.cleanup"`, очередь `maintenance`, уникальность за час), `events.NewCleanupWorker(pool *pgxpool.Pool) *CleanupWorker`, `events.CleanupJob() *river.PeriodicJob` (раз в час, сразу при старте).

- [ ] **Step 1: Падающие тесты конфига и очередей**

В `backend/internal/platform/config/config_test.go` добавь:

```go
func TestLoadWorkerQueuesAndRelay(t *testing.T) {
	c, err := config.Load[config.Worker]("WORKER_", []string{
		"WORKER_DATABASE_URL=postgres://w@localhost/wf",
		"WORKER_QUEUE_MAIL=7",
		"WORKER_RELAY_POLL=2s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Queues.Mail != 7 || c.Queues.Events != 10 || c.Queues.Maintenance != 2 {
		t.Fatalf("очереди: %+v", c.Queues)
	}
	if c.Relay.Poll != 2*time.Second || c.Relay.Batch != 100 {
		t.Fatalf("relay: %+v", c.Relay)
	}
}
```

`backend/internal/platform/queue/queue_test.go` — замени содержимое (сохрани существующие тесты пакета, если они проверяют `PingWorker`, — перенеси их сюда):

```go
package queue_test

import (
	"testing"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/queue"
)

// Спека §9.1: семь очередей, конкурентность каждой — из конфига; очереди default нет —
// задача без очереди не должна молча повиснуть в никем не обслуживаемой default.
func TestConfigHasSpecQueues(t *testing.T) {
	c := config.Queues{Events: 1, Lifecycle: 2, Notify: 3, Mail: 4, Stats: 5, Media: 6, Maintenance: 7}
	got := queue.Config(c)
	want := map[string]int{
		queue.Events: 1, queue.Lifecycle: 2, queue.Notify: 3, queue.Mail: 4,
		queue.Stats: 5, queue.Media: 6, queue.Maintenance: 7,
	}
	if len(got) != len(want) {
		t.Fatalf("очередей %d, ждали %d: %v", len(got), len(want), got)
	}
	for name, n := range want {
		if got[name].MaxWorkers != n {
			t.Errorf("%s: MaxWorkers = %d, ждали %d", name, got[name].MaxWorkers, n)
		}
	}
}

func TestPingGoesToMaintenance(t *testing.T) {
	if q := (queue.PingArgs{}).InsertOpts().Queue; q != queue.Maintenance {
		t.Fatalf("ping в очереди %q", q)
	}
}
```

- [ ] **Step 2: Убедиться, что падает**

Run: `go test ./internal/platform/config/ ./internal/platform/queue/ -count=1`
Expected: FAIL — `c.Queues undefined`, `undefined: queue.Config`.

- [ ] **Step 3: Конфиг**

В `backend/internal/platform/config/config.go` замени тип `Worker`:

```go
// Queues — конкурентность очередей воркера (спека §9.1).
type Queues struct {
	Events      int `env:"QUEUE_EVENTS" envDefault:"10"`
	Lifecycle   int `env:"QUEUE_LIFECYCLE" envDefault:"5"`
	Notify      int `env:"QUEUE_NOTIFY" envDefault:"5"`
	Mail        int `env:"QUEUE_MAIL" envDefault:"2"`
	Stats       int `env:"QUEUE_STATS" envDefault:"2"`
	Media       int `env:"QUEUE_MEDIA" envDefault:"2"`
	Maintenance int `env:"QUEUE_MAINTENANCE" envDefault:"2"`
}

// Relay — раскладка событий outbox (internal/platform/events).
type Relay struct {
	Batch int           `env:"RELAY_BATCH" envDefault:"100"`
	Poll  time.Duration `env:"RELAY_POLL" envDefault:"5s"`
}

type Worker struct {
	Log    Log
	DB     DB
	Queues Queues
	Relay  Relay
}
```

- [ ] **Step 4: Очереди**

`backend/internal/platform/queue/queue.go` — замени целиком:

```go
// Package queue — фоновые задачи на River (очередь в Postgres).
// Worker масштабируется отдельно от API: инстансов сколько угодно, задачи не задвоятся.
package queue

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/platform/config"
)

// Очереди воркера (спека §9.1). Каждая задача объявляет свою в InsertOpts.
const (
	Events      = "events"      // доставка событий подписчикам
	Lifecycle   = "lifecycle"   // переходы матчей, окна протокола и голосования
	Notify      = "notify"      // пуши и письма-уведомления
	Mail        = "mail"        // транзакционные письма — отдельно от рассылок
	Stats       = "stats"       // ночные пересчёты
	Media       = "media"       // обработка загрузок
	Maintenance = "maintenance" // чистка, сроки, сверки, отложенные действия админки
)

// PingArgs — проверка живости очереди: мониторинг ставит задачу и ждёт её выполнения.
type PingArgs struct{}

func (PingArgs) Kind() string { return "platform.ping" }

func (PingArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{Queue: Maintenance} }

type PingWorker struct{ river.WorkerDefaults[PingArgs] }

func (*PingWorker) Work(context.Context, *river.Job[PingArgs]) error { return nil }

// NewWorkers — реестр воркеров платформы. Модули регистрируют свои в этом же реестре.
func NewWorkers() *river.Workers {
	w := river.NewWorkers()
	river.AddWorker(w, &PingWorker{})
	return w
}

// Config — очереди §9.1 с конкурентностью из конфига. Очереди default нет намеренно.
func Config(c config.Queues) map[string]river.QueueConfig {
	return map[string]river.QueueConfig{
		Events:      {MaxWorkers: c.Events},
		Lifecycle:   {MaxWorkers: c.Lifecycle},
		Notify:      {MaxWorkers: c.Notify},
		Mail:        {MaxWorkers: c.Mail},
		Stats:       {MaxWorkers: c.Stats},
		Media:       {MaxWorkers: c.Media},
		Maintenance: {MaxWorkers: c.Maintenance},
	}
}

func NewClient(pool *pgxpool.Pool, workers *river.Workers, c config.Queues,
	periodic []*river.PeriodicJob, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:       Config(c),
		Workers:      workers,
		PeriodicJobs: periodic,
		Logger:       log,
	})
}
```

В `backend/internal/platform/events/deliver.go` замени `const QueueEvents = "events"` на:

```go
// QueueEvents — очередь доставки событий подписчикам (спека §9.1).
const QueueEvents = queue.Events
```

с импортом `"wf/backend/internal/platform/queue"`.

- [ ] **Step 5: Падающий тест чистки**

`backend/internal/platform/events/cleanup_test.go`:

```go
package events_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Спека §6.5: опубликованный outbox и inbox старше 30 дней удаляются; неопубликованное —
// никогда, даже старое (relay ещё должен его разложить).
func TestCleanupRemovesOnlyOldProcessed(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	oldPublished := publish(t, pool, joined(id.New(), 1))
	freshPublished := publish(t, pool, joined(id.New(), 1))
	oldUnpublished := publish(t, pool, joined(id.New(), 1))
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE outbox SET published_at = now() - interval '31 days' WHERE id = $1", oldPublished)
	exec("UPDATE outbox SET published_at = now() - interval '1 day' WHERE id = $1", freshPublished)
	exec("UPDATE outbox SET occurred_at = now() - interval '40 days' WHERE id = $1", oldUnpublished)
	exec("INSERT INTO event_inbox (subscriber, event_id, processed_at) VALUES ('stats.count', $1, now() - interval '31 days')", oldPublished)
	exec("INSERT INTO event_inbox (subscriber, event_id) VALUES ('stats.count', $1)", freshPublished)

	w := events.NewCleanupWorker(pool)
	if err := w.Work(ctx, &river.Job[events.CleanupArgs]{JobRow: &rivertype.JobRow{}}); err != nil {
		t.Fatal(err)
	}
	var kept, total int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE id = ANY($1)",
		[]uuid.UUID{freshPublished, oldUnpublished}).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if kept != 2 || total != 2 {
		t.Fatalf("outbox: из нужных осталось %d из 2, всего %d — старое опубликованное не удалено или задето лишнее", kept, total)
	}
	var inboxLeft int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM event_inbox").Scan(&inboxLeft); err != nil {
		t.Fatal(err)
	}
	if inboxLeft != 1 {
		t.Fatalf("inbox: осталось %d, ждали 1", inboxLeft)
	}
}

func TestCleanupJobConventions(t *testing.T) {
	opts := (events.CleanupArgs{}).InsertOpts()
	if (events.CleanupArgs{}).Kind() != "events.cleanup" || opts.Queue != queue.Maintenance || opts.UniqueOpts.ByPeriod == 0 {
		t.Fatalf("опции чистки: %+v", opts)
	}
	if events.CleanupJob() == nil {
		t.Fatal("нет периодической задачи")
	}
}
```

- [ ] **Step 6: Реализация чистки**

`backend/internal/platform/events/cleanup.go`:

```go
package events

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/events/eventsdb"
	"wf/backend/internal/platform/queue"
)

// cleanupBatch — строк за один DELETE: короткие транзакции не держат блокировки.
const cleanupBatch = 1000

// CleanupArgs — чистка опубликованного outbox и старого inbox (спека §6.5, §9.3).
type CleanupArgs struct {
	V int `json:"v"`
}

func (CleanupArgs) Kind() string { return "events.cleanup" }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q *eventsdb.Queries
}

func NewCleanupWorker(pool *pgxpool.Pool) *CleanupWorker {
	return &CleanupWorker{q: eventsdb.New(pool)}
}

func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 10 * time.Minute }

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	for _, del := range []func(context.Context, int32) (int64, error){w.q.DeleteOldPublished, w.q.DeleteOldInbox} {
		for {
			n, err := del(ctx, cleanupBatch)
			if err != nil {
				return err
			}
			if n < cleanupBatch {
				break
			}
		}
	}
	return nil
}

// CleanupJob — раз в час и сразу при старте воркера.
func CleanupJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: "events.cleanup", RunOnStart: true})
}
```

В `AddWorkers` (`deliver.go`) добавь `river.AddWorker(w, NewCleanupWorker(pool))`.

- [ ] **Step 7: Воркер — relay и периодические задачи**

`backend/cmd/worker/main_test.go` — добавь:

```go
// Воркер поднимает relay: событие, лежавшее в outbox, становится опубликованным; по отмене
// ctx воркер останавливается без ошибки.
func TestRunPublishesPendingEventsAndStops(t *testing.T) {
	url := dbtest.NewURL(t)
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		_, err := events.Publish(ctx, events.Event{
			Type: "teams.member_joined", SchemaVersion: 1, AggregateType: "team",
			AggregateID: id.New(), AggregateVersion: 1, Payload: map[string]string{},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"WORKER_DATABASE_URL=" + url}, io.Discard) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var left int
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay воркера не опубликовал событие")
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка: %v", err)
	}
}
```

(импорты: `time`, `github.com/jackc/pgx/v5`, `github.com/jackc/pgx/v5/pgxpool`, `wf/backend/internal/platform/db`, `…/events`, `…/id`, `…/testkit/dbtest`.)

Run: `go test ./cmd/worker/ -count=1` → Expected: FAIL (не компилируется или событие не публикуется).

`backend/cmd/worker/main.go` — замени `run`:

```go
func run(ctx context.Context, environ []string, logOut io.Writer) error {
	cfg, err := config.Load[config.Worker]("WORKER_", environ)
	if err != nil {
		return err
	}
	log := logx.New(logOut, cfg.Log).With("service", "worker")
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	reg, err := subscriptions()
	if err != nil {
		return err
	}
	workers := queue.NewWorkers()
	events.AddWorkers(workers, pool, reg)
	client, err := queue.NewClient(pool, workers, cfg.Queues, []*river.PeriodicJob{events.CleanupJob()}, log)
	if err != nil {
		return err
	}
	if err := client.Start(ctx); err != nil {
		return err
	}
	relay := events.NewRelay(pool, client, reg, log, events.RelayConfig{Batch: cfg.Relay.Batch, Poll: cfg.Relay.Poll})
	var wg sync.WaitGroup
	wg.Go(func() { _ = relay.Run(ctx) })
	log.Info("воркер запущен", "queues", cfg.Queues)
	<-ctx.Done()
	wg.Wait()
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.Stop(stopCtx)
}

// subscriptions — подписчики модулей на события. Пусто, пока нет модулей: каждая спека
// модуля добавляет сюда свои Subscription (internal/<модуль>/subscribers).
func subscriptions() (*events.Registry, error) {
	return events.NewRegistry()
}
```

(импорты: `sync`, `github.com/riverqueue/river`, `wf/backend/internal/platform/events`.)

Run: `go test ./cmd/worker/ ./internal/platform/... -count=1` → Expected: PASS.

- [ ] **Step 8: Переменные окружения**

В `backend/.env.example` после строки `WORKER_LOG_FORMAT=text` добавь:

```
# Конкурентность очередей (спека §9.1); по умолчанию events 10, lifecycle 5, notify 5, остальные 2
WORKER_QUEUE_EVENTS=10
WORKER_QUEUE_MAINTENANCE=2
# Раскладка событий: пачка и страховочный опрос, если NOTIFY потерялся
WORKER_RELAY_BATCH=100
WORKER_RELAY_POLL=5s
```

Локальный `backend/.env` — скриптом (значения те же, не выводить; файл закрыт для инструментов):

```bash
cat > .tools/tmp/env-worker.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
f=backend/.env
for kv in WORKER_QUEUE_EVENTS=10 WORKER_QUEUE_MAINTENANCE=2 WORKER_RELAY_BATCH=100 WORKER_RELAY_POLL=5s; do
  grep -q "^${kv%%=*}=" "$f" || printf '%s\n' "$kv" >>"$f"
done
EOF
bash .tools/tmp/env-worker.sh && rm -f .tools/tmp/env-worker.sh
```

(из корня репо; `.tools/tmp/` создать `mkdir -p .tools/tmp`, если нет.)

- [ ] **Step 9: Подсадка бага**

В `queue.Config` удали строку `Mail:` → `TestConfigHasSpecQueues` FAIL. Верни. В `DeleteOldPublished` (`queries/events.sql`) замени условие `published_at < now() - interval '30 days'` на `published_at IS NOT NULL`, `./task backend:gen` → `TestCleanupRemovesOnlyOldProcessed` FAIL. Верни, `./task backend:gen` — PASS.

- [ ] **Step 10: Commit**

```bash
git add backend/internal/platform/config backend/internal/platform/queue backend/internal/platform/events backend/cmd/worker backend/.env.example
git commit -m "Воркер: семь очередей, relay событий, чистка outbox и inbox" -- backend/internal/platform/config backend/internal/platform/queue backend/internal/platform/events backend/cmd/worker backend/.env.example
```

### Task 7: Переигровка событий — `worker events replay`

**Files:**
- Create: `backend/internal/platform/events/replay.go`, `replay_test.go`
- Modify: `backend/cmd/worker/main.go`, `main_test.go`

**Interfaces:**
- Consumes: `eventsdb.EventIDsForReplay` (Task 3), `NewDeliverArgs`, `Inserter`, `Registry` (Task 4–5), `subscriptions()` (Task 6).
- Produces:
  - `events.ErrUnknownSubscription`.
  - `events.Replay(ctx context.Context, pool db.TxStarter, ins Inserter, reg *Registry, eventType, subscriber string, since time.Time) (int, error)` — заново ставит доставку событий типа начиная с `since` одному подписчику; возвращает число **новых** задач (уже стоящие или выполненные недавно — пропуск уникальностью River; уже обработанные подписчиком — отсечёт `event_inbox`).
  - `run(ctx, args, environ []string, out, logOut io.Writer) error` в `cmd/worker`: без аргументов — воркер; `events replay --type T --subscriber S --since RFC3339` — переигровка.

- [ ] **Step 1: Падающие тесты**

`backend/internal/platform/events/replay_test.go`:

```go
package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestReplayEnqueuesForOneSubscriber(t *testing.T) {
	pool := dbtest.NewPool(t)
	for range 3 {
		publish(t, pool, joined(id.New(), 1))
	}
	publish(t, pool, left(id.New())) // другой тип — не переигрывается
	reg := twoSubscribers(t)
	ins := inserter(t, pool)
	since := time.Now().Add(-time.Hour)

	n, err := events.Replay(context.Background(), pool, ins, reg, "teams.member_joined", "notify.roster", since)
	if err != nil || n != 3 {
		t.Fatalf("Replay = %d, %v", n, err)
	}
	var forSub int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job
		WHERE kind = 'events.deliver' AND args->>'subscriber' = 'notify.roster'`).Scan(&forSub); err != nil {
		t.Fatal(err)
	}
	if forSub != 3 || deliveries(t, pool) != 3 {
		t.Fatalf("задач подписчику %d, всего %d — переигровка задела чужих", forSub, deliveries(t, pool))
	}
	// повтор не плодит дублей
	if n, err := events.Replay(context.Background(), pool, ins, reg, "teams.member_joined", "notify.roster", since); err != nil || n != 0 {
		t.Fatalf("повторный Replay = %d, %v", n, err)
	}
	// since в будущем — ничего
	if n, _ := events.Replay(context.Background(), pool, ins, reg, "teams.member_joined", "notify.roster", time.Now().Add(time.Hour)); n != 0 {
		t.Fatalf("Replay из будущего = %d", n)
	}
}

func TestReplayRejectsUnknownSubscription(t *testing.T) {
	pool := dbtest.NewPool(t)
	_, err := events.Replay(context.Background(), pool, inserter(t, pool), twoSubscribers(t),
		"teams.member_left", "notify.roster", time.Now())
	if !errors.Is(err, events.ErrUnknownSubscription) {
		t.Fatalf("err = %v", err)
	}
}
```

В `backend/cmd/worker/main_test.go` замени вызовы `run(ctx, env, io.Discard)` на `run(ctx, nil, env, io.Discard, io.Discard)` и добавь:

```go
func TestReplayCommandValidatesArguments(t *testing.T) {
	env := []string{"WORKER_DATABASE_URL=" + dbtest.NewURL(t)}
	cases := map[string][]string{
		"неизвестная команда":  {"events", "rewind"},
		"нет --since":          {"events", "replay", "--type", "teams.member_joined", "--subscriber", "notify.roster"},
		"битое --since":        {"events", "replay", "--type", "teams.member_joined", "--subscriber", "notify.roster", "--since", "вчера"},
		"неизвестный подписчик": {"events", "replay", "--type", "teams.member_joined", "--subscriber", "notify.roster", "--since", "2026-10-01T00:00:00Z"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if err := run(context.Background(), args, env, io.Discard, io.Discard); err == nil {
				t.Fatal("ждали ошибку")
			}
		})
	}
}
```

- [ ] **Step 2: Убедиться, что падает**

Run: `go test ./internal/platform/events/ ./cmd/worker/ -count=1`
Expected: FAIL — `undefined: events.Replay`, неверное число аргументов `run`.

- [ ] **Step 3: Реализация**

`backend/internal/platform/events/replay.go`:

```go
package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events/eventsdb"
)

// ErrUnknownSubscription — подписчик не подписан на этот тип события в реестре воркера.
var ErrUnknownSubscription = errors.New("events: нет такой подписки")

// Replay заново раскладывает события типа eventType начиная с since одному подписчику (спека
// §6.5): подписчик появился позже события или пропустил его. Уже обработанное отсечёт
// event_inbox, уже стоящее в очереди — уникальность River. Outbox хранит 30 дней.
func Replay(ctx context.Context, pool db.TxStarter, ins Inserter, reg *Registry,
	eventType, subscriber string, since time.Time) (int, error) {
	if _, ok := reg.Get(subscriber, eventType); !ok {
		return 0, fmt.Errorf("%w: %s на %s", ErrUnknownSubscription, subscriber, eventType)
	}
	var inserted int
	err := db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		ids, err := eventsdb.New(tx).EventIDsForReplay(ctx, eventsdb.EventIDsForReplayParams{EventType: eventType, Since: since})
		if err != nil {
			return err
		}
		const chunk = 1000
		for start := 0; start < len(ids); start += chunk {
			params := make([]river.InsertManyParams, 0, chunk)
			for _, eventID := range ids[start:min(start+chunk, len(ids))] {
				params = append(params, river.InsertManyParams{Args: NewDeliverArgs(eventID, subscriber)})
			}
			res, err := ins.InsertManyTx(ctx, tx, params)
			if err != nil {
				return err
			}
			for _, r := range res {
				if !r.UniqueSkippedAsDuplicate {
					inserted++
				}
			}
		}
		return nil
	})
	return inserted, err
}
```

`backend/cmd/worker/main.go`: `main` передаёт `os.Args[1:]` и `os.Stdout`; `run` получает `args []string, environ []string, out, logOut io.Writer`, после `defer pool.Close()` и `reg, err := subscriptions()`:

```go
	if len(args) > 0 {
		return command(ctx, args, pool, reg, out)
	}
```

и новая функция:

```go
const usage = "без аргументов — воркер; events replay --type <модуль>.<факт> --subscriber <модуль>.<имя> --since <RFC 3339>"

// command — разовые команды воркера: им нужен реестр подписчиков, который знает только воркер.
func command(ctx context.Context, args []string, pool *pgxpool.Pool, reg *events.Registry, out io.Writer) error {
	if len(args) < 2 || args[0] != "events" || args[1] != "replay" {
		return fmt.Errorf("неизвестная команда %q: %s", strings.Join(args, " "), usage)
	}
	fs := flag.NewFlagSet("events replay", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	eventType := fs.String("type", "", "тип события")
	subscriber := fs.String("subscriber", "", "подписчик")
	sinceRaw := fs.String("since", "", "с какого момента, RFC 3339")
	if err := fs.Parse(args[2:]); err != nil {
		return fmt.Errorf("%w: %s", err, usage)
	}
	if *eventType == "" || *subscriber == "" || *sinceRaw == "" {
		return fmt.Errorf("нужны --type, --subscriber и --since: %s", usage)
	}
	since, err := time.Parse(time.RFC3339, *sinceRaw)
	if err != nil {
		return fmt.Errorf("--since: нужен RFC 3339, например 2026-10-01T00:00:00Z")
	}
	// клиент только для вставки: очереди обслуживает работающий воркер
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return err
	}
	n, err := events.Replay(ctx, pool, ins, reg, *eventType, *subscriber, since)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "поставлено задач доставки: %d\n", n)
	return nil
}
```

(импорты: `flag`, `strings`, `github.com/jackc/pgx/v5/pgxpool`, `github.com/riverqueue/river/riverdriver/riverpgxv5`.) Переигровка требует подключения к базе, поэтому проверка аргументов идёт после `db.Open` — тест передаёт рабочий URL.

- [ ] **Step 4: Тесты проходят**

Run: `go test ./internal/platform/events/ ./cmd/worker/ -count=1`
Expected: PASS.

- [ ] **Step 5: Подсадка бага**

В `Replay` убери проверку `reg.Get` → `TestReplayRejectsUnknownSubscription` и случай «неизвестный подписчик» FAIL. Верни — PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/platform/events backend/cmd/worker
git commit -m "События: переигровка подписчику командой worker events replay" -- backend/internal/platform/events backend/cmd/worker
```

## Фаза C. Аудит, права, пробы

### Task 8: Аудит с маскированием персональных данных

**Files:**
- Modify: `backend/sqlc.yaml` — элемент `auditdb`
- Create: `backend/internal/platform/audit/queries/audit.sql`, `auditdb/*` (сгенерировано)
- Create: `backend/internal/platform/audit/audit.go`, `audit_test.go`, `mask.go`, `mask_test.go`

**Interfaces:**
- Consumes: `db.TxFrom`, `db.ErrNoTx`, `db.InTx` (Task 1), `id.New()` (Task 1).
- Produces:
  - `audit.Entry{ActorUserID uuid.UUID; ActorRole, Action, ObjectType string; ObjectID uuid.UUID; Before, After any; Reason string; IP netip.Addr; UserAgent string}` — `uuid.Nil`/нулевой `netip.Addr`/пустая строка пишутся как `NULL`.
  - `audit.Write(ctx context.Context, e Entry) error` — изменение: транзакцией из `ctx`, без неё `db.ErrNoTx` (§6.9).
  - `audit.WriteRead(ctx context.Context, q auditdb.DBTX, e Entry) error` — аудит чтения: отдельной короткой вставкой.
  - `audit.ErrInvalidEntry`; `audit.Mask(v any) ([]byte, error)` — JSON с замаскированными значениями чувствительных ключей.

- [ ] **Step 1: SQL и sqlc**

`backend/internal/platform/audit/queries/audit.sql`:

```sql
-- Аудит-лог (спека бэкенда §6.9, §10). Только вставка: изменение и удаление запрещены триггером.

-- name: InsertAudit :exec
INSERT INTO audit_log (id, actor_user_id, actor_role, action, object_type, object_id, before, after, reason, ip, user_agent)
VALUES (@id, @actor_user_id, @actor_role, @action, @object_type, @object_id, @before, @after, @reason, @ip, @user_agent);
```

В `backend/sqlc.yaml` в конец списка `sql:` — элемент как у `eventsdb` (Task 3, Step 2), с `queries: internal/platform/audit/queries/`, `package: auditdb`, `out: internal/platform/audit/auditdb` и теми же `overrides` (uuid, timestamptz, geography). `./task backend:gen`. Ожидаемые поля `InsertAuditParams`: `ID uuid.UUID`, `ActorUserID *uuid.UUID`, `ActorRole *string`, `Action string`, `ObjectType string`, `ObjectID *uuid.UUID`, `Before []byte`, `After []byte`, `Reason *string`, `Ip *netip.Addr`, `UserAgent *string` — сверь со сгенерированным и используй фактические имена.

- [ ] **Step 2: Падающие тесты маскирования**

`backend/internal/platform/audit/mask_test.go`:

```go
package audit_test

import (
	"encoding/json"
	"testing"

	"wf/backend/internal/platform/audit"
)

func TestMaskHidesPersonalData(t *testing.T) {
	in := map[string]any{
		"nickname":      "Kolya",
		"email":         "ivan.petrov@mail.ru",
		"contact_phone": "+79181234567",
		"birth_date":    "2008-05-01",
		"password_hash": "$argon2id$...",
		"profile": map[string]any{
			"email": "x@y.ru",
			"tags":  []any{map[string]any{"refresh_token": "abc"}},
		},
		"phone": 79181234567,
	}
	raw, err := audit.Mask(in)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"nickname":      "Kolya",
		"email":         "i***@mail.ru",
		"contact_phone": "***67",
		"birth_date":    "***",
		"password_hash": "***",
		"phone":         "***",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, ждали %v", k, got[k], v)
		}
	}
	profile := got["profile"].(map[string]any)
	if profile["email"] != "x***@y.ru" {
		t.Errorf("вложенная почта: %v", profile["email"])
	}
	if tok := profile["tags"].([]any)[0].(map[string]any)["refresh_token"]; tok != "***" {
		t.Errorf("токен в массиве: %v", tok)
	}
}

func TestMaskNilIsNull(t *testing.T) {
	raw, err := audit.Mask(nil)
	if err != nil || raw != nil {
		t.Fatalf("Mask(nil) = %s, %v", raw, err)
	}
}
```

- [ ] **Step 3: Падающие тесты записи**

`backend/internal/platform/audit/audit_test.go`:

```go
package audit_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

func entry() audit.Entry {
	return audit.Entry{
		ActorUserID: id.New(), ActorRole: "city_moderator",
		Action: "identity.user_banned", ObjectType: "user", ObjectID: id.New(),
		Before: map[string]any{"status": "active", "email": "ivan@mail.ru"},
		After:  map[string]any{"status": "banned"},
		Reason: "spam", IP: netip.MustParseAddr("203.0.113.7"), UserAgent: "test",
	}
}

func rows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriteInTransactionMasksAndPersists(t *testing.T) {
	pool := dbtest.NewPool(t)
	if err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		return audit.Write(ctx, entry())
	}); err != nil {
		t.Fatal(err)
	}
	var before, action, ip string
	if err := pool.QueryRow(context.Background(),
		"SELECT before::text, action, host(ip) FROM audit_log").Scan(&before, &action, &ip); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "ivan@mail.ru") || !strings.Contains(before, "i***@mail.ru") ||
		action != "identity.user_banned" || ip != "203.0.113.7" {
		t.Fatalf("запись аудита: before=%s action=%s ip=%s", before, action, ip)
	}
}

func TestWriteNeedsTransactionAndRollsBackWithIt(t *testing.T) {
	pool := dbtest.NewPool(t)
	if err := audit.Write(context.Background(), entry()); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("вне транзакции: %v", err)
	}
	_ = db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		if err := audit.Write(ctx, entry()); err != nil {
			t.Fatal(err)
		}
		return errors.New("действие не удалось")
	})
	if n := rows(t, pool); n != 0 {
		t.Fatalf("аудит пережил откат действия: %d", n)
	}
}

// Аудит чтения чувствительного (почта, связанные аккаунты) — без транзакции действия.
func TestWriteReadWithoutTransaction(t *testing.T) {
	pool := dbtest.NewPool(t)
	e := entry()
	e.Action, e.Before, e.After = "identity.email_viewed", nil, nil
	if err := audit.WriteRead(context.Background(), pool, e); err != nil {
		t.Fatal(err)
	}
	if n := rows(t, pool); n != 1 {
		t.Fatalf("строк %d", n)
	}
}

func TestWriteRejectsInvalidEntry(t *testing.T) {
	pool := dbtest.NewPool(t)
	for name, mutate := range map[string]func(*audit.Entry){
		"действие без модуля": func(e *audit.Entry) { e.Action = "banned" },
		"нет типа объекта":    func(e *audit.Entry) { e.ObjectType = "" },
	} {
		t.Run(name, func(t *testing.T) {
			e := entry()
			mutate(&e)
			if err := audit.WriteRead(context.Background(), pool, e); !errors.Is(err, audit.ErrInvalidEntry) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
```

- [ ] **Step 4: Убедиться, что падает**

Run: `go test ./internal/platform/audit/ -count=1`
Expected: FAIL — `undefined: audit.Mask` и др.

- [ ] **Step 5: Реализация**

`backend/internal/platform/audit/mask.go`:

```go
package audit

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Ключи с персональными данными и секретами (§6.9: в before/after ПД маскируются). Ключ
// совпадает целиком или оканчивается на _<имя> (contact_email, refresh_token).
var sensitive = []string{"email", "phone", "birth_date", "password", "password_hash",
	"secret", "totp_secret", "token", "recovery_codes"}

func isSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitive {
		if k == s || strings.HasSuffix(k, "_"+s) {
			return true
		}
	}
	return false
}

// Mask сериализует v в JSON и маскирует значения чувствительных ключей на любой глубине.
// nil — nil (NULL в базе).
func Mask(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	return json.Marshal(walk(tree))
}

func walk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSensitive(k) {
				t[k] = maskValue(k, val)
			} else {
				t[k] = walk(val)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = walk(t[i])
		}
		return t
	default:
		return v
	}
}

func maskValue(key string, v any) any {
	s, ok := v.(string)
	if !ok || s == "" {
		return "***"
	}
	k := strings.ToLower(key)
	switch {
	case k == "email" || strings.HasSuffix(k, "_email"):
		local, domain, found := strings.Cut(s, "@")
		if !found || local == "" {
			return "***"
		}
		return string([]rune(local)[0]) + "***@" + domain
	case k == "phone" || strings.HasSuffix(k, "_phone"):
		if len(s) < 4 {
			return "***"
		}
		return "***" + s[len(s)-2:]
	default:
		return "***"
	}
}
```

`backend/internal/platform/audit/audit.go`:

```go
// Package audit — аудит-лог действий (спека бэкенда §6.9, §10): кто, роль, действие, объект,
// было/стало с маскированием ПД, причина, IP, user-agent. Изменение пишется в транзакции
// действия (Write), чтение чувствительного — отдельной вставкой (WriteRead).
package audit

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"

	"github.com/google/uuid"

	"wf/backend/internal/platform/audit/auditdb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
)

var ErrInvalidEntry = errors.New("audit: невалидная запись")

var action = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Entry — запись аудита. Нулевые значения (uuid.Nil, пустая строка, нулевой адрес) — NULL:
// действие системы (воркер, миграция) не имеет автора, IP и user-agent.
type Entry struct {
	ActorUserID uuid.UUID
	ActorRole   string
	Action      string // <модуль>.<действие>
	ObjectType  string
	ObjectID    uuid.UUID
	Before      any
	After       any
	Reason      string
	IP          netip.Addr
	UserAgent   string
}

// Write — аудит изменения: в транзакции из ctx, чтобы запись и действие жили и умирали вместе.
func Write(ctx context.Context, e Entry) error {
	tx, ok := db.TxFrom(ctx)
	if !ok {
		return db.ErrNoTx
	}
	return insert(ctx, auditdb.New(tx), e)
}

// WriteRead — аудит чтения чувствительного: отдельной короткой вставкой, без транзакции действия.
func WriteRead(ctx context.Context, q auditdb.DBTX, e Entry) error {
	return insert(ctx, auditdb.New(q), e)
}

func insert(ctx context.Context, q *auditdb.Queries, e Entry) error {
	if !action.MatchString(e.Action) {
		return fmt.Errorf("%w: действие %q — нужен <модуль>.<действие>", ErrInvalidEntry, e.Action)
	}
	if e.ObjectType == "" {
		return fmt.Errorf("%w: нет типа объекта", ErrInvalidEntry)
	}
	before, err := Mask(e.Before)
	if err != nil {
		return fmt.Errorf("%w: before: %v", ErrInvalidEntry, err) //nolint:errorlint // причина текстом
	}
	after, err := Mask(e.After)
	if err != nil {
		return fmt.Errorf("%w: after: %v", ErrInvalidEntry, err) //nolint:errorlint // причина текстом
	}
	return q.InsertAudit(ctx, auditdb.InsertAuditParams{
		ID:          id.New(),
		ActorUserID: optUUID(e.ActorUserID),
		ActorRole:   optString(e.ActorRole),
		Action:      e.Action,
		ObjectType:  e.ObjectType,
		ObjectID:    optUUID(e.ObjectID),
		Before:      before,
		After:       after,
		Reason:      optString(e.Reason),
		Ip:          optAddr(e.IP),
		UserAgent:   optString(e.UserAgent),
	})
}

func optUUID(v uuid.UUID) *uuid.UUID {
	if v == uuid.Nil {
		return nil
	}
	return &v
}

func optString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func optAddr(v netip.Addr) *netip.Addr {
	if !v.IsValid() {
		return nil
	}
	return &v
}
```

- [ ] **Step 6: Тесты проходят**

Run: `go test ./internal/platform/audit/ ./internal/archtest/ -count=1`
Expected: PASS.

- [ ] **Step 7: Подсадка бага**

В `walk` замени `t[k] = maskValue(k, val)` на `t[k] = walk(val)` → `TestMaskHidesPersonalData` и `TestWriteInTransactionMasksAndPersists` FAIL. Верни — PASS.

- [ ] **Step 8: Commit**

```bash
git add backend/sqlc.yaml backend/internal/platform/audit
git commit -m "Аудит: запись в транзакции действия, аудит чтения, маскирование ПД" -- backend/sqlc.yaml backend/internal/platform/audit
```

### Task 9: Права ролей БД — `grants.sql` и страж §12.7

**Files:**
- Create: `backend/internal/platform/grants/grants.go`, `grants.sql`, `grants_test.go`
- Modify: `backend/cmd/migrate/main.go`, `main_test.go`
- Modify: `deploy/dev/initdb/20_wf.sql`
- Modify: `docs/deploy-dev.md` (раздел «Роли базы на Neon»)

**Interfaces:**
- Consumes: таблицы схемы (миграции 0001–0017), `dbtest.New(t) *sql.DB`.
- Produces:
  - `grants.Apply(ctx context.Context, db Execer) error`, `grants.Execer interface { ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) }` — его реализуют `*sql.DB` и `*sql.Conn`.
  - `cmd/migrate up` и `reset` применяют права после миграций и печатают `grants: права ролей применены`.

Матрица (спека §10.1 + наименьшие привилегии для объектов, о которых спека молчит):

| Объект | api | admin | worker |
| --- | --- | --- | --- |
| обычные таблицы | DML | DML | DML |
| представления | SELECT | SELECT | SELECT |
| последовательности | USAGE, SELECT | USAGE, SELECT | USAGE, SELECT |
| `goose_db_version` | — | — | — |
| `audit_log` | INSERT | SELECT, INSERT | INSERT |
| `staff_*` | — | DML | — (чистку `staff_invites` даст спека `identity`) |
| `credentials` | DML | DML | DELETE (без SELECT) |
| `outbox` | INSERT | SELECT, INSERT | DML |
| `event_inbox`, `event_cursors` | — | — | DML |
| `river_job` | SELECT, INSERT, UPDATE (уникальная вставка River — `ON CONFLICT DO UPDATE`) | SELECT, INSERT, UPDATE | DML |
| прочие `river_*` | — | — | DML |

- [ ] **Step 1: Падающий страж ролей**

`backend/internal/platform/grants/grants_test.go`:

```go
package grants_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/grants"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Страж спеки §12.7: под api нельзя прочитать staff_* и изменить audit_log, под worker —
// прочитать credentials. Таблиц staff_* и credentials в схеме ещё нет (спека identity) —
// тест создаёт их сам, до применения прав: правило должно сработать по имени.
func TestRolePrivileges(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS staff_probe (id int)",
		"CREATE TABLE IF NOT EXISTS credentials (id int)",
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 { // идемпотентность: второй прогон не падает и не меняет итог
		if err := grants.Apply(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		role, sql string
		allowed   bool
	}{
		{"api", "SELECT 1 FROM staff_probe", false},
		{"api", "UPDATE audit_log SET reason = reason", false},
		{"api", "DELETE FROM audit_log", false},
		{"api", "SELECT 1 FROM audit_log", false},
		{"api", "INSERT INTO audit_log (action, object_type) VALUES ('test.probe', 'probe')", true},
		{"api", "SELECT 1 FROM event_inbox", false},
		{"api", "SELECT 1 FROM outbox", false},
		{"api", "SELECT 1 FROM goose_db_version", false},
		{"api", "SELECT 1 FROM feature_flags", true},
		{"admin", "SELECT 1 FROM audit_log", true},
		{"admin", "UPDATE audit_log SET reason = reason", false},
		{"admin", "SELECT 1 FROM staff_probe", true},
		{"worker", "SELECT 1 FROM credentials", false},
		{"worker", "DELETE FROM credentials", true},
		{"worker", "SELECT 1 FROM staff_probe", false},
		{"worker", "SELECT 1 FROM event_inbox", true},
	}
	for _, c := range cases {
		t.Run(c.role+": "+c.sql, func(t *testing.T) {
			err := asRole(ctx, t, db, c.role, c.sql)
			var pg *pgconn.PgError
			denied := errors.As(err, &pg) && pg.Code == "42501" // insufficient_privilege
			switch {
			case c.allowed && err != nil:
				t.Fatalf("ждали доступ, получили: %v", err)
			case !c.allowed && !denied:
				t.Fatalf("ждали отказ в правах (42501), получили: %v", err)
			}
		})
	}
}

// asRole исполняет запрос под ролью в транзакции и откатывает её: проверки не меняют данные.
func asRole(ctx context.Context, t *testing.T, db *sql.DB, role, query string) error {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+role); err != nil {
		t.Fatalf("SET ROLE %s: %v — роли создаёт deploy/dev/initdb/20_wf.sql", role, err)
	}
	_, err = tx.ExecContext(ctx, query)
	return err
}
```

Run: `go test ./internal/platform/grants/ -count=1` → Expected: FAIL — `no non-test Go files`.

- [ ] **Step 2: `grants.sql` и `Apply`**

`backend/internal/platform/grants/grants.sql`:

```sql
-- Права ролей БД (спека бэкенда §10.1). Применяет cmd/migrate после up и reset: права на
-- таблицы, созданные миграциями, иначе не выдать. Идемпотентно: на каждом объекте сначала
-- REVOKE ALL, потом ровно нужное. Обходятся только объекты public, которыми владеет текущая
-- роль, кроме объектов расширений (spatial_ref_sys PostGIS). Роли нет — пропуск с NOTICE
-- (на Neon пока есть только api). Правила по объектам — в ветках CASE ниже.
DO $grants$
DECLARE
  r     record;
  who   text;
  privs text;
BEGIN
  FOREACH who IN ARRAY ARRAY['api', 'admin', 'worker'] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = who) THEN
      RAISE NOTICE 'grants: роли % нет — пропуск', who;
      CONTINUE;
    END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA public TO %I', who);
    FOR r IN
      SELECT c.relname AS name, c.relkind AS kind
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE n.nspname = 'public'
        AND c.relkind IN ('r', 'p', 'v', 'm', 'S')
        AND c.relowner = (SELECT oid FROM pg_roles WHERE rolname = current_user)
        AND NOT EXISTS (SELECT 1 FROM pg_depend d
                        WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
    LOOP
      EXECUTE format('REVOKE ALL ON %I FROM %I', r.name, who);
      privs := CASE
        WHEN r.kind = 'S' THEN 'USAGE, SELECT'
        -- служебное мигратора
        WHEN r.name = 'goose_db_version' THEN NULL
        -- аудит только дописывается; читает — только админка
        WHEN r.name = 'audit_log' THEN CASE who WHEN 'admin' THEN 'SELECT, INSERT' ELSE 'INSERT' END
        -- вход сотрудников — только admin-api
        WHEN r.name LIKE 'staff\_%' THEN CASE who WHEN 'admin' THEN 'SELECT, INSERT, UPDATE, DELETE' END
        -- пароли: воркер только удаляет (удаление аккаунта), прочитать не может
        WHEN r.name = 'credentials' THEN CASE who WHEN 'worker' THEN 'DELETE' ELSE 'SELECT, INSERT, UPDATE, DELETE' END
        -- события: API и админка публикуют, воркер раскладывает и чистит
        WHEN r.name = 'outbox' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                             WHEN 'admin' THEN 'SELECT, INSERT' ELSE 'INSERT' END
        WHEN r.name IN ('event_inbox', 'event_cursors') THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE' END
        -- очередь: API ставит задачи (письма с кодами), админка смотрит и перезапускает, воркер
        -- исполняет; UPDATE у api — уникальная вставка River идёт через ON CONFLICT DO UPDATE
        WHEN r.name = 'river_job' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                                ELSE 'SELECT, INSERT, UPDATE' END
        WHEN r.name LIKE 'river\_%' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE' END
        WHEN r.kind IN ('v', 'm') THEN 'SELECT'
        ELSE 'SELECT, INSERT, UPDATE, DELETE'
      END;
      IF privs IS NOT NULL THEN
        EXECUTE format('GRANT %s ON %I TO %I', privs, r.name, who);
      END IF;
    END LOOP;
  END LOOP;
END
$grants$;
```

`backend/internal/platform/grants/grants.go`:

```go
// Package grants применяет права ролей БД (спека бэкенда §10.1) — идемпотентный grants.sql.
package grants

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed grants.sql
var script string

// Execer — *sql.DB или *sql.Conn.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Apply выдаёт права ролям api, admin, worker на объекты, которыми владеет текущая роль.
// Один оператор DO — атомарно: окна «права сняты, новые не выданы» снаружи не видно.
func Apply(ctx context.Context, db Execer) error {
	if _, err := db.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("grants: %w", err)
	}
	return nil
}
```

Run: `go test ./internal/platform/grants/ -count=1` → Expected: PASS. Если какой-то случай с `allowed: true` падает с 42501 из-за последовательности или представления, которое трогает запрос, — поправь правило в `grants.sql`, а не тест.

- [ ] **Step 3: Подсадка бага**

Удали в `grants.sql` ветку `WHEN r.name = 'audit_log' …` → случаи `api: UPDATE audit_log` и `api: SELECT 1 FROM audit_log` FAIL. Удали ветку `credentials` → `worker: SELECT 1 FROM credentials` FAIL. Верни — PASS.

- [ ] **Step 4: `cmd/migrate` применяет права**

В `backend/cmd/migrate/main_test.go` добавь:

```go
func TestUpAppliesGrants(t *testing.T) {
	url := dbtest.NewURL(t)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"up"}, []string{"MIGRATOR_DATABASE_URL=" + url}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "grants: права ролей применены") {
		t.Fatalf("вывод up: %s", out.String())
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var canUpdate, canInsert bool
	if err := db.QueryRowContext(context.Background(), `SELECT has_table_privilege('api', 'audit_log', 'UPDATE'),
		has_table_privilege('api', 'audit_log', 'INSERT')`).Scan(&canUpdate, &canInsert); err != nil {
		t.Fatal(err)
	}
	if canUpdate || !canInsert {
		t.Fatalf("api на audit_log: UPDATE=%v INSERT=%v", canUpdate, canInsert)
	}
}
```

(импорты: `database/sql`, `_ "github.com/jackc/pgx/v5/stdlib"`.) Run → FAIL. В `backend/cmd/migrate/main.go` в ветке `case "up":` после цикла печати результатов:

```go
	case "up":
		res, err := p.Up(ctx)
		for _, r := range res {
			fmt.Fprintln(out, r)
		}
		if err != nil {
			return err
		}
		return applyGrants(ctx, db, out)
```

в ветке `case "reset":` — `return applyGrants(ctx, db, out)` вместо `return err` после `p.Up`, проверив `err` до этого; и функция:

```go
// applyGrants — права ролей после миграций (спека §10.1): новые таблицы получают права сразу.
func applyGrants(ctx context.Context, db *sql.DB, out io.Writer) error {
	if err := grants.Apply(ctx, db); err != nil {
		return err
	}
	fmt.Fprintln(out, "grants: права ролей применены")
	return nil
}
```

(импорты: `database/sql`, `wf/backend/internal/platform/grants`.) Run: `go test ./cmd/migrate/ -count=1` → PASS.

- [ ] **Step 5: Умолчания dev-базы и Neon**

`deploy/dev/initdb/20_wf.sql` — замени последние строки (от `GRANT USAGE ON SCHEMA public …` до конца) на:

```sql
-- Права на таблицы выдаёт cmd/migrate после каждого наката (backend/internal/platform/grants):
-- широких умолчаний здесь нет, иначе каждая новая таблица до прогона grants была бы открыта
-- всем ролям (спека бэкенда §10.1).
```

`docs/deploy-dev.md`, раздел «Роли базы на Neon» — замени блок SQL и абзац под ним на:

````markdown
Выполняется один раз владельцем базы `wf`:

```sql
CREATE ROLE api LOGIN PASSWORD '<генерируется>';
```

Права на таблицы роль получает от `migrate up` — он применяет `backend/internal/platform/grants/grants.sql`
после миграций (матрица — в его комментариях и спеке бэкенда §10.1). Роли `admin` и `worker`
создаются так же, когда на стенд выйдут admin-api и воркер; до тех пор `grants.sql` их пропускает.
Умолчания `ALTER DEFAULT PRIVILEGES`, выданные на Neon при создании стенда, безвредны: после
каждого наката права пересчитываются заново.
````

- [ ] **Step 6: Тесты**

Run: `go test ./internal/platform/grants/ ./cmd/migrate/ ./internal/archtest/ -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/platform/grants backend/cmd/migrate deploy/dev/initdb/20_wf.sql docs/deploy-dev.md
git commit -m "Права ролей БД: grants.sql после миграций и страж ролей" -- backend/internal/platform/grants backend/cmd/migrate deploy/dev/initdb/20_wf.sql docs/deploy-dev.md
```

### Task 10: Пробы `/healthz` и `/readyz` вне контракта

**Files:**
- Create: `backend/internal/platform/health/health.go`, `health_test.go`
- Modify: `backend/internal/platform/server/server.go`, `server_test.go`
- Modify: `render.yaml`, `.github/workflows/backend.yml` (задание `image`), `README.md`, `docs/deploy-dev.md`

**Interfaces:**
- Consumes: `*pgxpool.Pool.Ping(ctx) error`.
- Produces: `health.Handler(ready func(context.Context) error, next http.Handler) http.Handler` — `GET`/`HEAD /healthz` → 200 `{"status":"ok"}` без обращения к базе (процесс жив); `GET`/`HEAD /readyz` → `ready` с таймаутом 2 с: 200 `{"status":"ok"}` или 503 `{"status":"unavailable"}` без текста ошибки; остальное — `next`. `server.RunHTTP` оборачивает хендлер бинарника: пробы есть у `api` и `admin-api`, не проходят через логи доступа и контракт.

Почему вне контракта: проба — для инфраструктуры (балансировщик, хостинг, оркестратор), а не для клиентов; ей не место в `/v1`, Swagger и сгенерированных клиентах. Liveness и readiness разделены: короткий сбой базы не должен перезапускать живые инстансы, а инстанс без базы не должен получать трафик. `/v1/health` в контракте остаётся до первой операции модуля (Решения контроллера).

- [ ] **Step 1: Падающие тесты**

`backend/internal/platform/health/health_test.go`:

```go
package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wf/backend/internal/platform/health"
)

var next = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, nil))
	return rec
}

func TestHealthzDoesNotTouchDependencies(t *testing.T) {
	called := false
	h := health.Handler(func(context.Context) error { called = true; return errors.New("база лежит") }, next)
	rec := do(h, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) || called {
		t.Fatalf("/healthz: %d %s, ready вызван: %v", rec.Code, rec.Body.String(), called)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestReadyzReflectsDependency(t *testing.T) {
	up := health.Handler(func(context.Context) error { return nil }, next)
	if rec := do(up, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("готов: %d", rec.Code)
	}
	down := health.Handler(func(context.Context) error { return errors.New("dial tcp 10.0.0.5:5432: секрет") }, next)
	rec := do(down, http.MethodGet, "/readyz")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Fatalf("не готов: %d %s — нужен 503 без текста ошибки", rec.Code, rec.Body.String())
	}
}

func TestReadyzHasDeadline(t *testing.T) {
	h := health.Handler(func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("нет дедлайна")
		}
		return nil
	}, next)
	if rec := do(h, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("ready без дедлайна: %d", rec.Code)
	}
}

func TestOtherRequestsGoToNext(t *testing.T) {
	h := health.Handler(func(context.Context) error { return nil }, next)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/health"},
		{http.MethodPost, "/healthz"},
		{http.MethodGet, "/healthz/x"},
	} {
		if rec := do(h, c.method, c.path); rec.Code != http.StatusTeapot {
			t.Errorf("%s %s: %d — ждали обработку next", c.method, c.path, rec.Code)
		}
	}
	if rec := do(h, http.MethodHead, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("HEAD /healthz: %d", rec.Code)
	}
}
```

В `backend/internal/platform/server/server_test.go` в `TestRunHTTPServesAndStops` после `waitStatus(t, "http://"+addr+"/", http.StatusNoContent)` добавь:

```go
	waitStatus(t, "http://"+addr+"/healthz", http.StatusOK)
	waitStatus(t, "http://"+addr+"/readyz", http.StatusOK)
```

Run: `go test ./internal/platform/health/ ./internal/platform/server/ -count=1` → Expected: FAIL (`no non-test Go files`; `/healthz` отвечает 204 хендлера бинарника).

- [ ] **Step 2: Реализация**

`backend/internal/platform/health/health.go`:

```go
// Package health — пробы для инфраструктуры (спека бэкенда §6.9), вне контракта API:
// /healthz — процесс жив (без обращения к зависимостям), /readyz — готов принимать трафик
// (база отвечает). Пробы обходят логи доступа: хостинг дёргает их постоянно.
package health

import (
	"context"
	"net/http"
	"time"
)

// readyTimeout — дольше проба не ждёт: зависшая база — «не готов», а не зависшая проба.
const readyTimeout = 2 * time.Second

func Handler(ready func(context.Context) error, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			write(w, http.StatusOK, "ok")
		case "/readyz":
			ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
			defer cancel()
			if err := ready(ctx); err != nil {
				// причину не раскрываем: в ней адреса и устройство инфраструктуры
				write(w, http.StatusServiceUnavailable, "unavailable")
				return
			}
			write(w, http.StatusOK, "ok")
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func write(w http.ResponseWriter, status int, s string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"status":"` + s + `"}`))
}
```

В `backend/internal/platform/server/server.go`, в `RunHTTP` после успешного `h, err := build(log, pool)`:

```go
	// пробы — до хендлера бинарника: без логов доступа и проверки по контракту
	h = health.Handler(pool.Ping, h)
```

(импорт `wf/backend/internal/platform/health`.)

Run: `go test ./internal/platform/health/ ./internal/platform/server/ ./cmd/... -count=1` → Expected: PASS.

- [ ] **Step 3: Подсадка бага**

В `Handler` в ветке `/healthz` вызови `ready(r.Context())` и ответь 503 при ошибке → `TestHealthzDoesNotTouchDependencies` FAIL. Верни — PASS.

- [ ] **Step 4: Render, CI, документация**

`render.yaml`: `healthCheckPath: /v1/health` → `healthCheckPath: /healthz`, над строкой комментарий:

```yaml
    # /healthz — процесс жив, базу не трогает: частые пробы Render не будят Neon (он засыпает
    # через 5 минут простоя — бесплатные часы). Готовность с базой — /readyz.
```

`.github/workflows/backend.yml`, задание `image`: замени цикл ожидания на

```yaml
          for _ in $(seq 1 30); do
            if curl -fsS http://127.0.0.1:18080/healthz; then
              # готовность: API видит базу под своей ролью (права из grants.sql)
              curl -fsS http://127.0.0.1:18080/readyz
              # Swagger dev-стенда: контракт вшит в образ и отдаётся
              curl -fsS -o /dev/null http://127.0.0.1:18080/docs/openapi.json
              exit 0
            fi
            sleep 2
          done
```

и комментарий над заданием: `миграции на чистую базу, затем API, затем /healthz, /readyz и /docs`.

`README.md`, строка таблицы `| api / admin-api | http://127.0.0.1:8080/v1/health, http://127.0.0.1:8081/v1/health |` → `| api / admin-api | http://127.0.0.1:8080/healthz и /readyz, http://127.0.0.1:8081/healthz и /readyz |`.

`docs/deploy-dev.md`: в строке таблицы «API» — `проверка живости /v1/health` → `проверка живости /healthz (процесс жив, без базы; готовность с базой — /readyz)`; в строке «Проверка образа» — `/v1/health` → `/healthz, /readyz, /docs`.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/platform/health backend/internal/platform/server render.yaml .github/workflows/backend.yml README.md docs/deploy-dev.md
git commit -m "Пробы /healthz и /readyz вне контракта; Render и CI на них" -- backend/internal/platform/health backend/internal/platform/server render.yaml .github/workflows/backend.yml README.md docs/deploy-dev.md
```

### Task 11: Документация и полный прогон

**Files:**
- Modify: `docs/superpowers/specs/2026-10-01-backend-architecture-design.md` (§6.5, §6.9)
- Modify: `.claude/rules/backend.md`, `docs/04-принципы-архитектуры.md`, `docs/versions.md`

**Interfaces:**
- Consumes: всё из Task 1–10.
- Produces: документация совпадает с кодом.

- [ ] **Step 1: Спека**

В §6.5 спеки замени фразу «**Переигровка:** команда `cmd/migrate events replay --type T --subscriber S --since X` (и кнопка в админке)» на «**Переигровка:** команда `worker events replay --type T --subscriber S --since X` — в воркере, потому что реестр подписчиков знает только он (и кнопка в админке)». После пункта «**Порядок:** …» добавь пункт:

```markdown
- **Режим доставки** подписчик выбирает явно: `EveryEvent` (счётчики, начисления — применяется
  каждое событие) или `LatestState` (синхронизация состояния — событие со строго меньшей версией,
  чем применённая, отбрасывается; события одной версии применяются все). Курсор ведётся только
  в `LatestState`.
```

В §6.9, строка «Наблюдаемость»: `/healthz` и `/readyz` → `/healthz` (процесс жив, без зависимостей) и `/readyz` (база отвечает) — вне контракта, у `api` и `admin-api`.

- [ ] **Step 2: Правила и принципы**

В `.claude/rules/backend.md` после строки про «События — только id …» добавь:

```markdown
- Транзакция платформы — `db.InTx(ctx, pool, fn)`: всегда новая, кладёт tx в ctx; оттуда её читают только `events.Publish`, `audit.Write` (и идемпотентность). Вне `InTx` они возвращают `db.ErrNoTx`.
- Подписчик события — `events.Subscription` с явным `Delivery` (`EveryEvent` | `LatestState`), регистрируется в `subscriptions()` в `cmd/worker/main.go`; обработчик идемпотентен по `event_inbox` сам собой, пишет только в своей транзакции `tx`. Переигровка — `worker events replay --type T --subscriber S --since RFC3339`.
- Задача River объявляет очередь из `platform/queue` (`events`, `lifecycle`, `notify`, `mail`, `stats`, `media`, `maintenance`) в `InsertOpts`; очереди `default` нет. Аргументы — с полем версии `V`, без ПД, уникальность — `UniqueOpts`.
- Аудит — `audit.Write(ctx, entry)` в транзакции действия; чтение чувствительного — `audit.WriteRead`. ПД в `Before`/`After` маскирует `audit.Mask` по имени ключа (`email`, `phone`, `*_token`, …) — новые чувствительные ключи добавлять в `mask.go`.
- Права ролей БД — `internal/platform/grants/grants.sql` (применяет `migrate up`); новая таблица получает DML всем ролям по умолчанию — особые права (только чтение, только воркер) — новой веткой CASE там же и случаем в `grants_test.go`.
- Пробы — `/healthz`, `/readyz` (`platform/health`) вне контракта; `/v1/health` убрать из контрактов вместе с первой операцией модуля.
```

В `docs/04-принципы-архитектуры.md` в разделе о стражах (или в конце, если раздела нет) добавь пункт «Роли БД (§12.7): `internal/platform/grants/grants_test.go` — под `api` нет доступа к `staff_*` и изменению `audit_log`, под `worker` — к чтению `credentials`; проверено подсадкой (снятие ветки CASE)».

`docs/versions.md`: в таблицу после строки `goose / pgtestdb / kin-openapi` добавь:

```markdown
| google/uuid | 1.6.0 | `backend/go.mod` (UUIDv7 — `internal/platform/id`) |
```

- [ ] **Step 3: Полный прогон**

Run из корня: `./task backend:test` → Expected: PASS всех пакетов. `./task backend:gen` → `git status --short backend/` — сгенерированное не меняется (иначе закоммить в задаче, где сменился SQL). Тесты контрактов и фронтов этот план не трогает; если менялся `README.md` или workflow — `node_modules/.bin/prettier --check README.md .github/workflows/backend.yml render.yaml docs`.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/specs/2026-10-01-backend-architecture-design.md .claude/rules/backend.md docs/04-принципы-архитектуры.md docs/versions.md
git commit -m "Доки фундамента 2/3: события, очереди, аудит, права ролей, пробы" -- docs/superpowers/specs/2026-10-01-backend-architecture-design.md .claude/rules/backend.md docs/04-принципы-архитектуры.md docs/versions.md
```
