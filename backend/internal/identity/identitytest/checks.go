// Package identitytest — тестовые порты условий капитанства (спека identity §10.3, К1). Импортируют только тесты
// identity/** и файлы cmd/* с тегом сборки wf_e2e: прод-сборка подключает identity.UnavailableCheck (страж —
// TestCaptainChecksProdBuild в cmd/api и cmd/admin-api).
package identitytest

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
)

type check struct {
	name, reason string // reason "" — условие выполнено
}

func (c check) Name() string { return c.name }

func (c check) Check(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (string, bool, error) {
	return c.reason, c.reason == "", nil
}

// AllowCheck — порт name, условие всегда выполнено.
func AllowCheck(name string) identity.CaptainCheck { return check{name: name} }

// DenyCheck — порт name, условие не выполнено с причиной reason (CaptainIneligibility).
func DenyCheck(name, reason string) identity.CaptainCheck { return check{name: name, reason: reason} }

// AllowAll — оба порта RequiredCaptainChecks выполнены.
func AllowAll() []identity.CaptainCheck {
	out := make([]identity.CaptainCheck, 0, len(identity.RequiredCaptainChecks))
	for _, n := range identity.RequiredCaptainChecks {
		out = append(out, AllowCheck(n))
	}
	return out
}
