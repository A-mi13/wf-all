package idempotency

import (
	"context"
	"fmt"

	"wf/backend/internal/platform/idempotency/idempotencydb"
)

// Table — таблица ключей. У admin-api своя (финальное ревью ветки, I-1; спека сотрудников §3.3): ответы
// сотрудников несут ПД (почты, имена, комментарии подтверждений), а роль api на idempotency_keys имеет
// DML — читала бы их, подменяла сохранённый ответ и снимала ключ. На staff_idempotency_keys права
// только у роли admin (grants.sql, ветка staff_*). Запросы двух таблиц одинаковы (queries/idempotency.sql,
// пары Get/StaffGet и т. д.).
type Table int

const (
	// Players — idempotency_keys: публичный API (нулевое значение Config.Table). Чистит воркер
	// (CleanupJob в platform/periodic).
	Players Table = iota
	// Staff — staff_idempotency_keys: admin-api. Чистит тикер admin-api (DeleteExpired из
	// staff/jobs.Cleaner): у воркера прав на таблицу нет.
	Staff
)

func (t Table) String() string {
	switch t {
	case Players:
		return "idempotency_keys"
	case Staff:
		return "staff_idempotency_keys"
	}
	return fmt.Sprintf("idempotency.Table(%d)", int(t))
}

// store — запросы ключей над одной таблицей; db — пул (чтение до транзакции, сохранение ответа,
// чистка) или транзакция ручки (вставка ключа в хуке).
type store interface {
	get(ctx context.Context, p idempotencydb.GetParams) (idempotencydb.GetRow, error)
	claim(ctx context.Context, p idempotencydb.ClaimParams) error
	saveResponse(ctx context.Context, p idempotencydb.SaveResponseParams) (int64, error)
	deleteExpired(ctx context.Context, batch int32) (int64, error)
	// общие для таблиц: lock_timeout транзакции
	currentLockTimeout(ctx context.Context) (string, error)
	setLockTimeout(ctx context.Context, v string) error
}

// storeOf — запросы таблицы t; неизвестная таблица — паника (ошибка сборки, ловится на старте).
func storeOf(t Table, db idempotencydb.DBTX) store {
	q := idempotencydb.New(db)
	switch t {
	case Players:
		return playersStore{q}
	case Staff:
		return staffStore{q}
	}
	panic(fmt.Sprintf("idempotency: неизвестная таблица ключей %v", t))
}

type playersStore struct{ q *idempotencydb.Queries }

func (s playersStore) get(ctx context.Context, p idempotencydb.GetParams) (idempotencydb.GetRow, error) {
	return s.q.Get(ctx, p)
}

func (s playersStore) claim(ctx context.Context, p idempotencydb.ClaimParams) error {
	_, err := s.q.Claim(ctx, p)
	return err
}

func (s playersStore) saveResponse(ctx context.Context, p idempotencydb.SaveResponseParams) (int64, error) {
	return s.q.SaveResponse(ctx, p)
}

func (s playersStore) deleteExpired(ctx context.Context, batch int32) (int64, error) {
	return s.q.DeleteExpired(ctx, batch)
}

func (s playersStore) currentLockTimeout(ctx context.Context) (string, error) {
	return s.q.CurrentLockTimeout(ctx)
}

func (s playersStore) setLockTimeout(ctx context.Context, v string) error {
	return s.q.SetLockTimeout(ctx, v)
}

// staffStore — запросы staff_idempotency_keys. playersStore не встраивается: новый метод store, не
// написанный здесь, молча ушёл бы в таблицу игроков — без встраивания это ошибка компиляции.
type staffStore struct{ q *idempotencydb.Queries }

func (s staffStore) currentLockTimeout(ctx context.Context) (string, error) {
	return s.q.CurrentLockTimeout(ctx)
}

func (s staffStore) setLockTimeout(ctx context.Context, v string) error {
	return s.q.SetLockTimeout(ctx, v)
}

func (s staffStore) get(ctx context.Context, p idempotencydb.GetParams) (idempotencydb.GetRow, error) {
	r, err := s.q.StaffGet(ctx, idempotencydb.StaffGetParams(p))
	return idempotencydb.GetRow(r), err
}

func (s staffStore) claim(ctx context.Context, p idempotencydb.ClaimParams) error {
	_, err := s.q.StaffClaim(ctx, idempotencydb.StaffClaimParams(p))
	return err
}

func (s staffStore) saveResponse(ctx context.Context, p idempotencydb.SaveResponseParams) (int64, error) {
	return s.q.StaffSaveResponse(ctx, idempotencydb.StaffSaveResponseParams(p))
}

func (s staffStore) deleteExpired(ctx context.Context, batch int32) (int64, error) {
	return s.q.StaffDeleteExpired(ctx, batch)
}

// DeleteExpired удаляет просроченные ключи таблицы t порциями по 1000 (каждая — отдельный оператор вне
// транзакции: строки под замком параллельного Claim не теряются, см. запрос DeleteExpired) и возвращает
// число удалённых. db — пул роли с правами на таблицу: Players — воркер (CleanupWorker), Staff — admin-api
// (тикер staff/jobs.Cleaner). Неизвестная таблица — паника.
func DeleteExpired(ctx context.Context, db idempotencydb.DBTX, t Table) (int64, error) {
	s := storeOf(t, db)
	var total int64
	for {
		n, err := s.deleteExpired(ctx, cleanupBatch)
		total += n
		if err != nil {
			return total, fmt.Errorf("idempotency: чистка %s: %w", t, err)
		}
		if n < cleanupBatch {
			return total, nil
		}
	}
}
