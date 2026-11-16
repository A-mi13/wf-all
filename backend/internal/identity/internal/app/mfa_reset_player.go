package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
)

// Сброс 2FA поддержкой — отмена владельцем (спека §3.15 п. 4; план I-2, Task 18): вход вторым фактором
// (onSecondFactorLogin, mfa_reset_hook.go) или явная отмена cancelMyMfaReset. Исполнение по сроку — mfa_reset_exec.go.

// opCancelMyMfaReset — метка hooks.afterFirstLock явной отмены (гонка с исполнением, TestExecuteVsOwnerCancelRace).
const opCancelMyMfaReset = "cancel_my_mfa_reset"

// cancelResetsByOwner — живой сброс аккаунта отменяет владелец (users уже под замком вызывающего, R30: mfa_resets —
// после users и sessions): cancelled_by = owner, событие mfa_reset_cancelled (версия агрегата 2), письмо
// mfa_reset_cancelled, аудит от игрока. Число отменённых (живой — не больше одного, mfa_resets_live_key).
func (a *App) cancelResetsByOwner(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (int, error) {
	now := a.clock.Now()
	rows, err := identitydb.New(tx).MfaResetCancelByOwner(ctx, identitydb.MfaResetCancelByOwnerParams{Now: &now, UserID: userID})
	if err != nil {
		return 0, fmt.Errorf("identity: отмена сброса 2FA владельцем: %w", err)
	}
	for _, r := range rows {
		if _, err := events.Publish(ctx, identity.MfaResetCancelled(identity.MfaResetRef{ID: r.ID, Version: 2}, userID,
			r.ExecuteAt)); err != nil {
			return 0, err
		}
		if err := a.enqueueMail(ctx, tx, MailMfaResetCancelled, r.ID); err != nil {
			return 0, err
		}
		if err := a.auditUser(ctx, ActionMfaResetCancelled, userID, nil,
			map[string]any{"reset_id": r.ID, "cancelled_by": "owner"}); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

// CancelMyMfaReset — cancelMyMfaReset (m3): lockUser (R30; гонку с исполнением по сроку решает этот замок — воркер
// берёт users SKIP LOCKED); сессия запроса отозвана после загрузки (кэш до 5 с) — 401; сессия не проходила второй
// фактор (sessions.mfa_at NULL) — при живом сбросе 403 reauth_required, без него 404; живой сброс — отмена владельцем;
// живого нет, но последний сброс отменён владельцем за жизнь этой сессии (не раньше sessions.created_at; свежий вход
// вторым фактором после отмены не мешает) — тоже успех (204: вход или прошлый вызов этой сессии уже отменил); иначе
// 404 identity.mfa_reset_not_found.
func (a *App) CancelMyMfaReset(ctx context.Context, userID, sessionID uuid.UUID) error {
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := a.lockUser(ctx, tx, userID); errors.Is(err, errUserGone) {
			return errSessionGone
		} else if err != nil {
			return err
		}
		a.firstLock(opCancelMyMfaReset)
		q := identitydb.New(tx)
		sess, err := q.MfaResetSessionLive(ctx, identitydb.MfaResetSessionLiveParams{ID: sessionID, UserID: userID})
		if err != nil {
			return fmt.Errorf("identity: сессия отмены сброса 2FA: %w", err)
		}
		if !sess.Live {
			return errSessionGone
		}
		if !sess.SecondFactor {
			// явная отмена — только сессией, прошедшей второй фактор (решение пользователя 16.11): вошедший одним
			// паролем при живом сбросе получает 403 reauth_required (reauthenticate вторым фактором отменит сброс сам)
			if _, err := q.LiveMfaResetAt(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
				return errMfaResetNotFound
			} else if err != nil {
				return fmt.Errorf("identity: живой сброс 2FA: %w", err)
			}
			return errReauthRequired
		}
		n, err := a.cancelResetsByOwner(ctx, tx, userID)
		if err != nil || n > 0 {
			return err
		}
		done, err := q.MfaResetCancelledBySession(ctx, identitydb.MfaResetCancelledBySessionParams{UserID: userID,
			SessionID: sessionID})
		if err != nil {
			return fmt.Errorf("identity: отмена сброса 2FA этой сессией: %w", err)
		}
		if done {
			return nil
		}
		return errMfaResetNotFound
	})
}
