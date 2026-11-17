package main

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
)

// statusDeleted — статус удалённого аккаунта identity (identity_read_users.status).
const statusDeleted = "deleted"

// players — порт staff.PlayerDirectory над identity.Service (план I-4, R15): staff (слой 0) identity не импортирует,
// ник и существование учётки игрока для связи «сотрудник → игрок» отдаёт сборка. Нет аккаунта или он deleted —
// ok = false; pending_deletion — ok.
type players struct{ svc identity.Service }

func (p players) Player(ctx context.Context, userID uuid.UUID) (string, bool, error) {
	u, err := p.svc.User(ctx, userID)
	if errors.Is(err, identity.ErrUserNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if u.Status == statusDeleted {
		return "", false, nil
	}
	return u.Nickname, true, nil
}
