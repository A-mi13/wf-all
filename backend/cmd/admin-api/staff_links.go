package main

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/staff"
)

// staffLinks — порт identity.StaffLinks над staff.LinkedPlayerTx (R38 плана I-4): identity staff не импортирует,
// связь сотрудника с игроком в транзакции решения по заявке читается через сборку.
type staffLinks struct{}

func (staffLinks) LinkedUser(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) (uuid.UUID, int64, error) {
	return staff.LinkedPlayerTx(ctx, tx, staffID)
}
