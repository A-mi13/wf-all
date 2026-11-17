package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/httpx"
)

var (
	errReauthRequired = httpx.NewError(http.StatusForbidden, identity.CodeReauthRequired)
	errSessionGone    = httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthenticated)
)

// requireFresh — операция «С» (§6.3): сессия вошла или прошла свежий вход не раньше domain.ReauthFresh назад,
// иначе 403 identity.reauth_required до любых изменений. Замки — приём R30: users сессии первым, затем строка
// sessions FOR UPDATE (её же правят неудачи свежего входа). Сессии нет или она отозвана (кэш загрузчика ≤ 5 с) —
// 401 auth.unauthenticated (как конвейер на мёртвую сессию); статус аккаунта под замком не active | suspended — тоже
// 401.
func (a *App) requireFresh(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (sessionRow, error) {
	var s sessionRow
	err := a.lockByRow(ctx, tx, "session",
		func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error) {
			u, err := q.SessionUser(ctx, sessionID)
			return &u, err
		},
		func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error) {
			var err error
			s, err = q.LockSession(ctx, sessionID)
			return &s.UserID, err
		})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errRowGone) {
		return sessionRow{}, errSessionGone
	}
	if err != nil {
		return sessionRow{}, err
	}
	if s.RevokedAt != nil {
		return sessionRow{}, errSessionGone
	}
	// §17: статус под замком (users уже взят lockByRow — без ожидания): бан или удаление в окне кэша загрузчика (≤ 5 с)
	// — как мёртвая сессия, а не запись, которую отвергнет CHECK (23514 → 500)
	u, err := a.lockUser(ctx, tx, s.UserID)
	if errors.Is(err, errUserGone) {
		return sessionRow{}, errSessionGone
	}
	if err != nil {
		return sessionRow{}, err
	}
	if u.Status != identitydb.UserStatusActive && u.Status != identitydb.UserStatusSuspended {
		return sessionRow{}, errSessionGone
	}
	if s.ReauthAt == nil || a.clock.Now().Sub(*s.ReauthAt) > domain.ReauthFresh {
		return sessionRow{}, errReauthRequired
	}
	return s, nil
}
