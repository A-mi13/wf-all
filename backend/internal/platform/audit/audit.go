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

// ErrInvalidEntry — запись не прошла проверку (действие не в формате <модуль>.<действие>,
// нет типа объекта, before/after не сериализуются); в базу ничего не попало.
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
