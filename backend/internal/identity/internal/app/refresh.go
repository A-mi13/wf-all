package app

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/token"
)

// Обновление токенов (спека §3.7, бэкенда §6.2) — Task 11.

var errRefreshInvalid = httpx.NewError(http.StatusUnauthorized, identity.CodeRefreshInvalid)

// Refresh — ротация refresh (одна транзакция). Строка токена — приёмом R30 «аккаунт по строке» (R3-3): поиск по
// SHA-256 без замка → hooks.afterRowFound("refresh") → lockUser(user_id) → sessions FOR UPDATE → refresh_tokens FOR
// UPDATE с перепроверкой (порядок «сессия → users» на ветке повтора дал бы 40P01 с deleteAccount). Сессия отозвана,
// истекла (now ≥ idle или absolute, часы приложения) или аккаунт не active|suspended — 401 identity.refresh_invalid.
// Токен не использован — новый токен, used_at/successor_id/successor_enc (token.SealSuccessor)/successor_until =
// now + 15 с, продление только idle (не дальше absolute), last_seen_at сессии и устройства, новый access.
// Использован, now < successor_until и устройство то же (HMAC platform ‖ install_id мобильного — платформа из строки
// devices сессии, или метка BFF веба) — тот же преемник (token.OpenSuccessor). Иначе — повтор: отзыв сессии
// (refresh_reuse), security_events, письмо session_revoked_suspicious, коммит и 401.
func (a *App) Refresh(ctx context.Context, raw, installID string, c Client) (TokenPair, error) {
	var pair TokenPair
	// повтор вне окна или с другого устройства: отзыв коммитится, ответ 401 — после коммита (D8)
	reused := false
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		var found identitydb.RefreshFindRow
		var sess sessionRow
		var tok identitydb.RefreshToken
		err := a.lockByRow(ctx, tx, "refresh",
			func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error) {
				var err error
				found, err = q.RefreshFind(ctx, token.HashRefresh(raw))
				return &found.UserID, err
			},
			func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error) {
				var err error
				if sess, err = q.LockSession(ctx, found.SessionID); err != nil {
					return nil, err
				}
				if tok, err = q.RefreshLockToken(ctx, found.ID); err != nil {
					return nil, err
				}
				if tok.SessionID != sess.ID {
					return nil, errRowGone
				}
				return &sess.UserID, nil
			})
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errRowGone) {
			return errRefreshInvalid
		}
		if err != nil {
			return err
		}
		a.firstLock("refresh")
		u, err := a.lockUser(ctx, tx, sess.UserID) // уже под замком — статус аккаунта
		if err != nil {
			return err
		}
		now := a.clock.Now()
		if sess.RevokedAt != nil || !now.Before(sess.IdleExpiresAt) || !now.Before(sess.AbsoluteExpiresAt) ||
			(u.Status != identitydb.UserStatusActive && u.Status != identitydb.UserStatusSuspended) {
			return errRefreshInvalid
		}
		if tok.UsedAt == nil {
			pair, err = a.rotate(ctx, tx, sess, tok, raw)
			return err
		}
		same, err := a.sameDevice(ctx, tx, sess, installID, c)
		if err != nil {
			return err
		}
		if tok.SuccessorUntil != nil && now.Before(*tok.SuccessorUntil) && same {
			next, err := token.OpenSuccessor(raw, tok.SuccessorEnc)
			if err != nil {
				return fmt.Errorf("identity: преемник refresh: %w", err)
			}
			access, accessExp, err := a.tokens.Access(sess.UserID, sess.ID)
			if err != nil {
				return err
			}
			pair = TokenPair{Access: access, AccessExpires: accessExp, Refresh: next,
				RefreshIdleExpires: sess.IdleExpiresAt, SessionID: sess.ID}
			return nil
		}
		reused = true
		return a.refreshReused(ctx, tx, sess, c)
	})
	if err != nil {
		return TokenPair{}, err
	}
	if reused {
		return TokenPair{}, errRefreshInvalid
	}
	return pair, nil
}

// rotate — новый токен семьи и окно гонки у предъявленного; сессия и устройство — последний визит, idle продлён не
// дальше absolute.
func (a *App) rotate(ctx context.Context, tx pgx.Tx, sess sessionRow, tok identitydb.RefreshToken, raw string) (TokenPair, error) {
	q := identitydb.New(tx)
	now := a.clock.Now()
	next, hash, err := token.NewRefresh()
	if err != nil {
		return TokenPair{}, err
	}
	sealed, err := token.SealSuccessor(raw, next)
	if err != nil {
		return TokenPair{}, err
	}
	nextID := id.New()
	if err := q.InsertRefreshToken(ctx, identitydb.InsertRefreshTokenParams{ID: nextID, SessionID: sess.ID,
		TokenHash: hash, Now: now}); err != nil {
		return TokenPair{}, fmt.Errorf("identity: refresh: %w", err)
	}
	until := now.Add(domain.RefreshRaceWindow)
	if err := q.RefreshRotate(ctx, identitydb.RefreshRotateParams{Now: &now, SuccessorID: &nextID,
		SuccessorEnc: sealed, SuccessorUntil: &until, ID: tok.ID}); err != nil {
		return TokenPair{}, fmt.Errorf("identity: ротация refresh: %w", err)
	}
	idle, _ := domain.SessionTerms(domain.ClientKind(sess.ClientKind))
	idleAt := now.Add(idle)
	if idleAt.After(sess.AbsoluteExpiresAt) {
		idleAt = sess.AbsoluteExpiresAt
	}
	if err := q.RefreshTouchSession(ctx, identitydb.RefreshTouchSessionParams{IdleExpiresAt: idleAt, Now: now,
		ID: sess.ID}); err != nil {
		return TokenPair{}, fmt.Errorf("identity: продление сессии: %w", err)
	}
	if sess.DeviceID != nil {
		if err := q.RefreshTouchDevice(ctx, identitydb.RefreshTouchDeviceParams{Now: now, ID: *sess.DeviceID}); err != nil {
			return TokenPair{}, fmt.Errorf("identity: устройство: %w", err)
		}
	}
	access, accessExp, err := a.tokens.Access(sess.UserID, sess.ID)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{Access: access, AccessExpires: accessExp, Refresh: next, RefreshIdleExpires: idleAt,
		SessionID: sess.ID}, nil
}

// sameDevice — повтор пришёл с устройства сессии: вид клиента тот же, у сессии есть устройство, HMAC (ключом строки
// devices) от platform ‖ install_id тела (мобильный) или метки BFF (веб) равен install_mac. Сессия без устройства
// (веб без метки, R18) — сравнить не с чем: «другое» (BFF сам схлопывает обновления сессии).
func (a *App) sameDevice(ctx context.Context, tx pgx.Tx, sess sessionRow, installID string, c Client) (bool, error) {
	if sess.DeviceID == nil || domain.ClientKind(sess.ClientKind) != c.Kind {
		return false, nil
	}
	presented := installID
	if c.Kind == domain.ClientWeb {
		presented = c.BFFDevice
	}
	if presented == "" {
		return false, nil
	}
	dev, err := identitydb.New(tx).RefreshDevice(ctx, *sess.DeviceID)
	if err != nil {
		return false, fmt.Errorf("identity: устройство сессии: %w", err)
	}
	if !a.devices.Has(dev.KeyID) {
		return false, nil
	}
	mac, err := a.deviceMAC(dev.KeyID, dev.Platform, presented)
	if err != nil {
		return false, err
	}
	return hmac.Equal(mac, dev.InstallMac), nil
}

// refreshReused — повтор: сессия отозвана (refresh_reuse, событие и версия — revokeSession), журнал, письмо;
// вызывающий коммитит и отвечает 401.
func (a *App) refreshReused(ctx context.Context, tx pgx.Tx, sess sessionRow, c Client) error {
	if _, err := a.revokeSession(ctx, tx, sess.UserID, sess.ID, domain.RevokedRefreshReuse); err != nil {
		return err
	}
	var dev uuid.UUID
	if sess.DeviceID != nil {
		dev = *sess.DeviceID
	}
	ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: sess.UserID, Kind: domain.SecRefreshReuse,
		SessionID: sess.ID, DeviceID: dev, IP: c.IP, UserAgent: c.UserAgent,
		Meta: map[string]any{"client_kind": string(c.Kind)}})
	if err != nil {
		return err
	}
	if err := a.enqueueMail(ctx, tx, MailSessionRevokedSuspicious, ref); err != nil {
		return err
	}
	return nil
}
