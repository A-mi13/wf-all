package staff

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/staff/internal/store/staffdb"
)

// LinkedPlayerTx — текущая связь сотрудника с учёткой игрока и версия его учётки в транзакции вызывающего (реализация
// порта identity.StaffLinks, план I-4 R38): строка staff_accounts сотрудника — FOR SHARE до конца tx (решение по
// заявке сериализуется с внесением и снятием связи, сменой роли и городов, выключением), затем версия учётки и
// staff_player_links. Связи нет — uuid.Nil; сотрудника нет — ErrNotFound. Версию вызывающий сверяет с
// auth.Staff.Version загрузки сессии: не совпала — права снимка устарели (m1 ревью Task 4). Межмодульная транзакция
// вне списка intx спеки бэкенда §4.4 — исключение по прецеденту хука LockActors подтверждений (R38).
//
// Замок и чтения — разными операторами, а не один с JOIN: при READ COMMITTED оператор с FOR SHARE после ожидания замка
// перечитывает только заблокированную строку, а соединённую связь берёт из снимка до ожидания — связь, заведённую
// PutPlayerLink, пока решение ждало, он бы не увидел. Следующие операторы берут новый снимок уже под замком.
func LinkedPlayerTx(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) (uuid.UUID, int64, error) {
	q := staffdb.New(tx)
	if _, err := q.LockAccountForShare(ctx, staffID); errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, ErrNotFound
	} else if err != nil {
		return uuid.Nil, 0, fmt.Errorf("staff: замок учётки для связи с игроком: %w", err)
	}
	version, err := q.LockedAccountVersion(ctx, staffID)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("staff: версия учётки: %w", err)
	}
	id, err := q.LinkedPlayerOf(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, version, nil
	}
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("staff: связь с игроком: %w", err)
	}
	return id, version, nil
}
