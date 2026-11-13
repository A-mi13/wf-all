package ratelimit

import (
	"context"
	"fmt"
	"time"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/ratelimit/ratelimitdb"
)

// Table — таблица счётчиков. У admin-api своя (план B, R2; спека сотрудников §3.2–3.3): ключи входа
// сотрудников (email:<hmac>, ip:staff_auth), субъекта (staff:<класс>:<id>) и потолок аудита приглашений, а у
// роли api на rate_limits DML — взлом публичного API стирал бы счётчики перебора или закрывал вход всем
// сотрудникам большим tat. На staff_rate_limits права только у роли admin (grants.sql, ветка staff_*), у admin
// на rate_limits — ничего. Запросы двух таблиц одинаковы (queries/ratelimit.sql, пары Take/StaffTake и т. д.).
type Table int

const (
	// Players — rate_limits: публичный API. Чистит воркер (CleanupJob в platform/periodic).
	Players Table = iota
	// Staff — staff_rate_limits: admin-api (сценарии, конвейер, команды оператора — одна сборка). Чистит тикер
	// admin-api (DeleteExpired из staff/jobs.Cleaner): у воркера прав на таблицу нет.
	Staff
)

func (t Table) String() string {
	switch t {
	case Players:
		return "rate_limits"
	case Staff:
		return "staff_rate_limits"
	}
	return fmt.Sprintf("ratelimit.Table(%d)", int(t))
}

// store — запросы счётчиков над одной таблицей.
type store interface {
	take(ctx context.Context, p ratelimitdb.TakeParams) (time.Time, error)
	add(ctx context.Context, p ratelimitdb.AddParams) error
	getTAT(ctx context.Context, key string) (time.Time, error)
	deleteExpired(ctx context.Context, p ratelimitdb.DeleteExpiredParams) (int64, error)
}

// storeOf — запросы таблицы t; неизвестная таблица — паника (ошибка сборки, ловится на старте).
func storeOf(t Table, db ratelimitdb.DBTX) store {
	q := ratelimitdb.New(db)
	switch t {
	case Players:
		return playersStore{q}
	case Staff:
		return staffStore{q}
	}
	panic(fmt.Sprintf("ratelimit: неизвестная таблица счётчиков %v", t))
}

type playersStore struct{ q *ratelimitdb.Queries }

func (s playersStore) take(ctx context.Context, p ratelimitdb.TakeParams) (time.Time, error) {
	return s.q.Take(ctx, p)
}

func (s playersStore) add(ctx context.Context, p ratelimitdb.AddParams) error { return s.q.Add(ctx, p) }

func (s playersStore) getTAT(ctx context.Context, key string) (time.Time, error) {
	return s.q.GetTAT(ctx, key)
}

func (s playersStore) deleteExpired(ctx context.Context, p ratelimitdb.DeleteExpiredParams) (int64, error) {
	return s.q.DeleteExpired(ctx, p)
}

// staffStore — запросы staff_rate_limits. playersStore не встраивается: новый метод store, не написанный
// здесь, молча ушёл бы в таблицу игроков — без встраивания это ошибка компиляции.
type staffStore struct{ q *ratelimitdb.Queries }

func (s staffStore) take(ctx context.Context, p ratelimitdb.TakeParams) (time.Time, error) {
	return s.q.StaffTake(ctx, ratelimitdb.StaffTakeParams(p))
}

func (s staffStore) add(ctx context.Context, p ratelimitdb.AddParams) error {
	return s.q.StaffAdd(ctx, ratelimitdb.StaffAddParams(p))
}

func (s staffStore) getTAT(ctx context.Context, key string) (time.Time, error) {
	return s.q.StaffGetTAT(ctx, key)
}

func (s staffStore) deleteExpired(ctx context.Context, p ratelimitdb.DeleteExpiredParams) (int64, error) {
	return s.q.StaffDeleteExpired(ctx, ratelimitdb.StaffDeleteExpiredParams(p))
}

// DeleteExpired удаляет строки таблицы t с tat в прошлом (по часам c) порциями по 1000 — каждая отдельным
// оператором вне транзакции (условие повторяется на внешнем DELETE: свежий tat параллельного Take не
// теряется) — и возвращает число удалённых. db — пул роли с правами на таблицу: Players — воркер
// (CleanupWorker), Staff — admin-api (тикер staff/jobs.Cleaner). Неизвестная таблица — паника.
func DeleteExpired(ctx context.Context, db ratelimitdb.DBTX, c clock.Clock, t Table) (int64, error) {
	s := storeOf(t, db)
	var total int64
	for {
		n, err := s.deleteExpired(ctx, ratelimitdb.DeleteExpiredParams{Now: c.Now(), Batch: cleanupBatch})
		total += n
		if err != nil {
			return total, fmt.Errorf("ratelimit: чистка %s: %w", t, err)
		}
		if n < cleanupBatch {
			return total, nil
		}
	}
}
