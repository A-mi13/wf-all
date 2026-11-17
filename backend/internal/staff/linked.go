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
func LinkedPlayerTx(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) (uuid.UUID, error) {
	id, err := staffdb.New(tx).LinkedPlayerForShare(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("staff: связь с игроком: %w", err)
	}
	if id == nil {
		return uuid.Nil, nil
	}
	return *id, nil
}
