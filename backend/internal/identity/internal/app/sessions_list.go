package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
)

// «Где вы вошли» (спека §3.7) — Task 12. Способы входа — methods.go.

var errSessionNotFound = httpx.NewError(http.StatusNotFound, identity.CodeSessionNotFound)

// SessionView — сессия в списке. IP не отдаётся и в вид не попадает (наружу — только город); поля устройства —
// у мобильного, Browser и OS — у веба (domain.ParseUserAgent); пустая строка — null контракта.
type SessionView struct {
	ID                                     uuid.UUID
	ClientKind                             domain.ClientKind
	Platform, Model, OSVersion, AppVersion string
	Browser, OS, AuthMethod, City          string
	CreatedAt, LastSeenAt                  time.Time
	Current                                bool
}

// ListSessions — живые сессии аккаунта (не отозваны, сроки не истекли по часам приложения), новые сверху по
// last_seen_at; current — сессия запроса. Только сессии userID (субъект запроса).
func (a *App) ListSessions(ctx context.Context, userID, current uuid.UUID) ([]SessionView, error) {
	rows, err := identitydb.New(a.pool).ListLiveSessions(ctx, identitydb.ListLiveSessionsParams{UserID: userID,
		Now: a.clock.Now()})
	if err != nil {
		return nil, fmt.Errorf("identity: сессии: %w", err)
	}
	out := make([]SessionView, 0, len(rows))
	for _, r := range rows {
		v := SessionView{ID: r.ID, ClientKind: domain.ClientKind(r.ClientKind), AuthMethod: string(r.AuthMethod),
			City: deref(r.IpCity), CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, Current: r.ID == current}
		if v.ClientKind == domain.ClientWeb {
			v.Browser, v.OS = domain.ParseUserAgent(deref(r.UserAgent))
		} else {
			v.Platform, v.Model, v.OSVersion, v.AppVersion = r.Platform, deref(r.Model), deref(r.OsVersion), deref(r.AppVersion)
		}
		out = append(out, v)
	}
	return out, nil
}

// RevokeSession — «выйти здесь» для своей сессии (и текущей — клиент выходит): revoked_reason = revoked, событие,
// аудит sessions_revoked. Сначала под замком аккаунта перепроверяется сессия запроса current (requireCurrent: кэш
// загрузчика ≤ 5 с пропускает уже отозванную) — мёртвая — 401. Чужая, несуществующая или уже отозванная цель — 404
// identity.session_not_found (одинаково: revokeByIDs отбирает по user_id субъекта). Одна транзакция — под
// Idempotency-Key ручки.
func (a *App) RevokeSession(ctx context.Context, userID, current, sessionID uuid.UUID) error {
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := a.requireCurrent(ctx, tx, userID, current); err != nil {
			return err
		}
		done, err := a.revokeSession(ctx, tx, userID, sessionID, domain.RevokedByUser)
		if err != nil {
			return err
		}
		if !done {
			return errSessionNotFound
		}
		return a.auditUser(ctx, ActionSessionsRevoked, userID, nil,
			map[string]any{"reason": string(domain.RevokedByUser), "session_count": 1})
	})
}

// RevokeOthers — «выйти везде, кроме этого»: все неотозванные, кроме current (revoked_others), событие и аудит — если
// было что отзывать; живые токены шагов аккаунта гаснут всегда; ответ — сколько живых отозвано. current мёртвая под замком аккаунта (requireCurrent) — 401 без
// отзыва: отозванная сессия не выкидывает владельца.
func (a *App) RevokeOthers(ctx context.Context, userID, current uuid.UUID) (int, error) {
	var n int
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		n = 0
		if err := a.requireCurrent(ctx, tx, userID, current); err != nil {
			return err
		}
		ids, err := a.revokeSessions(ctx, tx, userID, current, domain.RevokedOthers)
		if err != nil {
			return err
		}
		// «выйти везде» закрывает и недоведённые входы (mfa_token) — даже когда других сессий нет
		if err := a.revokeChallenges(ctx, tx, userID); err != nil {
			return err
		}
		n = len(ids)
		if n == 0 {
			return nil
		}
		return a.auditUser(ctx, ActionSessionsRevoked, userID, nil,
			map[string]any{"reason": string(domain.RevokedOthers), "session_count": n})
	})
	return n, err
}

// requireCurrent — сессия запроса жива под замком аккаунта (R30: users первым, затем строка sessions FOR UPDATE):
// есть, принадлежит userID, не отозвана, сроки не истекли по часам приложения (как в загрузчике). Иначе — 401
// auth.unauthenticated (errSessionGone), как конвейер на мёртвую сессию; аккаунта нет — тоже 401.
func (a *App) requireCurrent(ctx context.Context, tx pgx.Tx, userID, current uuid.UUID) error {
	if _, err := a.lockUser(ctx, tx, userID); err != nil {
		if errors.Is(err, errUserGone) {
			return errSessionGone
		}
		return err
	}
	s, err := identitydb.New(tx).LockSession(ctx, current)
	if errors.Is(err, pgx.ErrNoRows) {
		return errSessionGone
	}
	if err != nil {
		return fmt.Errorf("identity: текущая сессия: %w", err)
	}
	now := a.clock.Now()
	if s.UserID != userID || s.RevokedAt != nil || !now.Before(s.IdleExpiresAt) || !now.Before(s.AbsoluteExpiresAt) {
		return errSessionGone
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
