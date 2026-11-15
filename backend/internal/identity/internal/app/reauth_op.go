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
	"wf/backend/internal/platform/httpx"
)

// Свежий вход (спека §3.13, R16, R27) — владелец Task 13.

// ReauthInput — reauthenticate: ровно одно поле (passkey и social в I-2 — reauth_invalid, R3).
type ReauthInput struct {
	Password, TOTPCode, RecoveryCode string
	Passkey, Social                  bool
}

var (
	errReauthInvalid    = httpx.NewError(http.StatusBadRequest, identity.CodeReauthInvalid)
	errReauthNotAllowed = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeReauthMethodNotAllowed)
)

// fields — сколько способов передано.
func (in ReauthInput) fields() int {
	n := 0
	for _, set := range []bool{in.Password != "", in.TOTPCode != "", in.RecoveryCode != "", in.Passkey, in.Social} {
		if set {
			n++
		}
	}
	return n
}

// passwordReauthAllowed — пароль годится для свежего входа только без 2FA (§3.13: при 2FA пароль известен
// атакующему) — иначе 422 reauth_method_not_allowed.
func passwordReauthAllowed(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) error {
	on, err := q.MfaConfirmed(ctx, userID)
	if err != nil {
		return err
	}
	if on {
		return errReauthNotAllowed
	}
	return nil
}

// lockReauthSession — строка сессии приёмом R30 (users сессии первым, затем sessions FOR UPDATE: её правят неудачи
// свежего входа); нет или отозвана — 401 auth.unauthenticated.
func (a *App) lockReauthSession(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (sessionRow, error) {
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
	return s, nil
}

// Reauthenticate — свежий вход сессии (§3.13): ровно одно поле (иначе 400 validation.failed по body); 2FA включена и
// пароль — 422 reauth_method_not_allowed (неудачей не считается); 5 неудач за окно 15 минут от первой — 429 с
// Retry-After до конца окна (R16: sessions.reauth_failures и reauth_failed_at; окно истекло — счёт с нуля); неудача —
// 400 reauth_invalid + attempts_left (коммитится); passkey и social в I-2 — неудача (R3). Пароль — argon2 вне tx
// (checkPassword, R31), в tx — сверка, что хэш тот же. Успех — reauth_at = now, счёт с нуля, при TOTP или резервном
// коде ещё mfa_at = now (R27); ответ — reauth_until.
func (a *App) Reauthenticate(ctx context.Context, sessionID uuid.UUID, in ReauthInput) (time.Time, error) {
	if in.fields() != 1 {
		return time.Time{}, errFactorShape
	}
	var pwOK bool
	var seen string
	if in.Password != "" {
		pool := identitydb.New(a.pool)
		uid, err := pool.SessionUser(ctx, sessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, errSessionGone
		}
		if err != nil {
			return time.Time{}, fmt.Errorf("identity: сессия: %w", err)
		}
		// argon2 не тратится на запрещённый способ
		if err := passwordReauthAllowed(ctx, pool, uid); err != nil {
			return time.Time{}, err
		}
		if pwOK, seen, err = a.checkPassword(ctx, uid, in.Password); err != nil {
			return time.Time{}, err
		}
	}
	var until time.Time
	err := a.inTxCommitFailure(ctx, func(ctx context.Context, tx pgx.Tx) error {
		s, err := a.lockReauthSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		now := a.clock.Now()
		failures := 0
		var started *time.Time
		if s.ReauthFailedAt != nil && now.Before(s.ReauthFailedAt.Add(domain.ReauthFailuresWindow)) {
			failures, started = int(s.ReauthFailures), s.ReauthFailedAt
		}
		if failures >= domain.ReauthFailures {
			return errRateLimited.WithRetryAfter(started.Add(domain.ReauthFailuresWindow).Sub(now))
		}
		q := identitydb.New(tx)
		ok := false
		var mfaAt *time.Time
		switch {
		case in.Password != "":
			if err := passwordReauthAllowed(ctx, q, s.UserID); err != nil {
				return err
			}
			if pwOK {
				if ok, err = a.passwordUnchanged(ctx, tx, s.UserID, seen); err != nil {
					return err
				}
			}
		case in.TOTPCode != "" || in.RecoveryCode != "":
			_, err := a.verifySecondFactor(ctx, tx, s.UserID, SecondFactor{TOTP: in.TOTPCode, Recovery: in.RecoveryCode})
			switch {
			case errors.Is(err, errFactorInvalid):
			case err != nil:
				return err
			default:
				ok, mfaAt = true, &now
			}
		}
		if !ok {
			failures++
			if started == nil {
				started = &now
			}
			if err := q.ReauthFailed(ctx, identitydb.ReauthFailedParams{Failures: int16(failures), FailedAt: started,
				ID: s.ID}); err != nil {
				return fmt.Errorf("identity: неудача свежего входа: %w", err)
			}
			return failureToCommit{errReauthInvalid.WithAttemptsLeft(domain.ReauthFailures - failures)}
		}
		if err := q.ReauthSucceeded(ctx, identitydb.ReauthSucceededParams{Now: &now, MfaAt: mfaAt, ID: s.ID}); err != nil {
			return fmt.Errorf("identity: свежий вход: %w", err)
		}
		until = now.Add(domain.ReauthFresh)
		return nil
	})
	return until, err
}
