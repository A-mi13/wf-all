package staff

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/staff/internal/store/staffdb"
)

// LinkedPlayerTx — текущая связь сотрудника с учёткой игрока в транзакции вызывающего (реализация порта
// identity.StaffLinks, план I-4 R38): строка staff_accounts сотрудника — FOR SHARE до конца tx (решение по заявке
// сериализуется с внесением и снятием связи), затем staff_player_links. Связи нет — uuid.Nil; сотрудника нет —
// ErrNotFound. Межмодульная транзакция вне списка intx спеки бэкенда §4.4 — исключение по прецеденту хука LockActors
// подтверждений (R38).
//
// Два оператора, а не один с JOIN: при READ COMMITTED оператор с FOR SHARE после ожидания замка перечитывает только
// заблокированную строку, а соединённую связь берёт из снимка до ожидания — связь, заведённую PutPlayerLink, пока
// решение ждало, он бы не увидел. Второй оператор берёт новый снимок уже под замком.
func LinkedPlayerTx(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) (uuid.UUID, error) {
	q := staffdb.New(tx)
	if _, err := q.LockAccountForShare(ctx, staffID); errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	} else if err != nil {
		return uuid.Nil, fmt.Errorf("staff: замок учётки для связи с игроком: %w", err)
	}
	id, err := q.LinkedPlayerOf(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("staff: связь с игроком: %w", err)
	}
	return id, nil
}
