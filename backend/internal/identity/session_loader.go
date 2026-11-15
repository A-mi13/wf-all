package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
)

// Роль и скоуп допуска капитана в Principal.Roles (role_assignments после 0020 — только они, спека бэкенда §4.5).
const (
	roleCaptain = "captain"
	scopeCity   = "city"
)

// NewSessionLoader — загрузчик сессии публичного API (спека identity §6.1; план I-2, Task 11): одним запросом
// сессия, статус аккаунта, действующие ограничения и живые допуски капитана → *auth.Principal. Сессии нет, она
// отозвана, срок бездействия или абсолютный истёк (now ≥ срока, часы приложения), аккаунт не active|suspended —
// auth.ErrSessionInvalid (401). cmd/api оборачивает его в auth.NewCachedLoader (5 с): отзыв действует ≤ 5 с.
// Корень импортирует identitydb (не app — цикл, N2): загрузчик — не сценарий, а чтение одной строки.
func NewSessionLoader(pool *pgxpool.Pool, c clock.Clock) auth.SessionLoader {
	return auth.SessionLoaderFunc(func(ctx context.Context, sid uuid.UUID) (*auth.Principal, error) {
		row, err := identitydb.New(pool).LoaderSession(ctx, identitydb.LoaderSessionParams{Now: c.Now(), ID: sid})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, auth.ErrSessionInvalid
		}
		if err != nil {
			return nil, fmt.Errorf("identity: загрузка сессии: %w", err)
		}
		p := &auth.Principal{UserID: row.UserID, SessionID: sid, Status: row.Status, Restrictions: row.Restrictions}
		if row.DeviceID != nil {
			p.DeviceID = *row.DeviceID
		}
		for _, city := range row.CaptainCities {
			p.Roles = append(p.Roles, auth.Role{Name: roleCaptain, ScopeType: scopeCity, ScopeID: city})
		}
		return p, nil
	})
}
