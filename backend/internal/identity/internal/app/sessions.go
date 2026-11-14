package app

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/token"
)

// TokenPair — токены выданной или обновлённой сессии (тип app; oapi — в httpapi).
type TokenPair struct {
	Access             string
	AccessExpires      time.Time
	Refresh            string
	RefreshIdleExpires time.Time
	SessionID          uuid.UUID
}

// Issued — новая сессия.
type Issued struct {
	SessionID uuid.UUID
	Tokens    TokenPair
}

// sessionRow — строка сессии под замком.
type sessionRow = identitydb.Session

// issueSession — новая сессия аккаунта (вызывающий уже держит lockUser): строка sessions (reauth_at = now, сроки —
// domain.SessionTerms по виду клиента, город по IP на языке аккаунта), refresh_tokens (в базе — только SHA-256),
// users.last_login_at; живых сессий сверх domain.MaxLiveSessions — старшие по last_seen_at отзываются (revoked, R17);
// access — Tokens.Access(userID, sid). deviceID — uuid.Nil у веба без метки (R18).
func (a *App) issueSession(ctx context.Context, tx pgx.Tx, userID, deviceID uuid.UUID, c Client,
	method domain.AuthMethod, mfaAt *time.Time) (Issued, error) {
	u, err := a.lockUser(ctx, tx, userID)
	if err != nil {
		return Issued{}, err
	}
	q := identitydb.New(tx)
	now := a.clock.Now()
	idle, absolute := domain.SessionTerms(c.Kind)
	absAt := now.Add(absolute)
	idleAt := now.Add(idle)
	if idleAt.After(absAt) {
		idleAt = absAt
	}
	sid := id.New()
	var dev *uuid.UUID
	var appVer *string
	if deviceID != uuid.Nil {
		dev = &deviceID
		if appVer, err = q.DeviceAppVersion(ctx, deviceID); err != nil {
			return Issued{}, fmt.Errorf("identity: версия приложения устройства: %w", err)
		}
	}
	ip := c.IP
	if !ip.IsValid() {
		// адрес не разобран (peer) — колонка NOT NULL: неуказанный адрес вместо отказа во входе
		ip = netip.IPv4Unspecified()
	}
	var city *string
	if p, ok := a.ipgeo.Lookup(ip); ok {
		if n, ok := p.Name(u.Locale); ok {
			city = &n
		}
	}
	var ua *string
	// UA уже обрезан httpx.UserAgent (ClientFrom): не длиннее 512 байт, валидный UTF-8
	if c.UserAgent != "" {
		s := c.UserAgent
		ua = &s
	}
	if err := q.InsertSession(ctx, identitydb.InsertSessionParams{ID: sid, UserID: userID, DeviceID: dev,
		ClientKind: identitydb.ClientKind(c.Kind), AuthMethod: identitydb.AuthMethod(method), MfaAt: mfaAt,
		ReauthAt: &now, Ip: ip, IpCity: city, UserAgent: ua, AppVersion: appVer, Now: now,
		IdleExpiresAt: idleAt, AbsoluteExpiresAt: absAt}); err != nil {
		return Issued{}, fmt.Errorf("identity: сессия: %w", err)
	}
	raw, hash, err := token.NewRefresh()
	if err != nil {
		return Issued{}, err
	}
	if err := q.InsertRefreshToken(ctx, identitydb.InsertRefreshTokenParams{ID: id.New(), SessionID: sid,
		TokenHash: hash, Now: now}); err != nil {
		return Issued{}, fmt.Errorf("identity: refresh: %w", err)
	}
	if err := q.TouchUserLogin(ctx, identitydb.TouchUserLoginParams{Now: &now, ID: userID}); err != nil {
		return Issued{}, err
	}
	// новая сессия — одна из MaxLiveSessions: прочих живых остаётся на одну меньше (m4: новую не отзывать)
	excess, err := q.ExcessLiveSessions(ctx, identitydb.ExcessLiveSessionsParams{UserID: userID, Now: now,
		Current: sid, Keep: domain.MaxLiveSessions - 1})
	if err != nil {
		return Issued{}, err
	}
	if len(excess) > 0 {
		if _, err := a.revokeByIDs(ctx, q, userID, excess, domain.RevokedByUser); err != nil {
			return Issued{}, err
		}
	}
	access, accessExp, err := a.tokens.Access(userID, sid)
	if err != nil {
		return Issued{}, err
	}
	return Issued{SessionID: sid, Tokens: TokenPair{Access: access, AccessExpires: accessExp, Refresh: raw,
		RefreshIdleExpires: idleAt, SessionID: sid}}, nil
}

// revokeSessions — отзыв всех неотозванных сессий аккаунта, кроме except (uuid.Nil — все), включая истёкшие по часам
// этого сервера (n1: по часам отстающего они ещё живы): замок users первым (R30). Ответ, событие
// identity.sessions_revoked (≤ 100 id) и users.version + 1 — только по живым (m1); живых нет — без события.
func (a *App) revokeSessions(ctx context.Context, tx pgx.Tx, userID, except uuid.UUID, reason domain.RevokedReason) ([]uuid.UUID, error) {
	if _, err := a.lockUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	a.firstLock("revoke_sessions")
	q := identitydb.New(tx)
	now := a.clock.Now()
	r := string(reason)
	rows, err := q.RevokeUserSessions(ctx, identitydb.RevokeUserSessionsParams{Now: &now, Reason: &r, UserID: userID,
		KeepID: except})
	if err != nil {
		return nil, fmt.Errorf("identity: отзыв сессий: %w", err)
	}
	var ids []uuid.UUID
	for _, row := range rows {
		if row.Live {
			ids = append(ids, row.ID)
		}
	}
	if err := a.sessionsRevoked(ctx, q, userID, ids, reason); err != nil {
		return nil, err
	}
	return ids, nil
}

// revokeSession — отзыв одной сессии аккаунта тем же путём события и версии (m11: выход, refresh_reuse,
// revokeMySession); false — сессии нет, она чужая или уже отозвана.
func (a *App) revokeSession(ctx context.Context, tx pgx.Tx, userID, sessionID uuid.UUID, reason domain.RevokedReason) (bool, error) {
	if _, err := a.lockUser(ctx, tx, userID); err != nil {
		return false, err
	}
	a.firstLock("revoke_session")
	ids, err := a.revokeByIDs(ctx, identitydb.New(tx), userID, []uuid.UUID{sessionID}, reason)
	if err != nil {
		return false, err
	}
	return len(ids) == 1, nil
}

// revokeByIDs — отзыв перечисленных живых сессий аккаунта (users уже под замком) с событием и версией.
func (a *App) revokeByIDs(ctx context.Context, q *identitydb.Queries, userID uuid.UUID, ids []uuid.UUID, reason domain.RevokedReason) ([]uuid.UUID, error) {
	now := a.clock.Now()
	r := string(reason)
	done, err := q.RevokeSessionsByIDs(ctx, identitydb.RevokeSessionsByIDsParams{Now: &now, Reason: &r, UserID: userID, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("identity: отзыв сессий: %w", err)
	}
	if err := a.sessionsRevoked(ctx, q, userID, done, reason); err != nil {
		return nil, err
	}
	return done, nil
}

// sessionsRevoked — версия аккаунта + 1 и событие об отозванных (пусто — ничего).
func (a *App) sessionsRevoked(ctx context.Context, q *identitydb.Queries, userID uuid.UUID, ids []uuid.UUID, reason domain.RevokedReason) error {
	if len(ids) == 0 {
		return nil
	}
	ver, err := q.BumpUserVersion(ctx, userID)
	if err != nil {
		return fmt.Errorf("identity: версия аккаунта: %w", err)
	}
	if _, err := events.Publish(ctx, identity.SessionsRevoked(identity.UserRef{ID: userID, Version: ver}, ids, string(reason))); err != nil {
		return err
	}
	return nil
}
