package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
)

// Исполнение сброса 2FA по сроку (спека §3.15 п. 5; план I-2, Task 18) — воркер, роль worker (UPDATE mfa_resets есть,
// INSERT нет; users — UPDATE; user_mfa и user_recovery_codes — DELETE и колонка user_id).

const (
	// rowMfaResetDue — метка hooks.afterRowFound: кандидаты прочитаны без замка (гонка с отменой владельцем).
	rowMfaResetDue = "mfa_reset_due"
	// mfaResetMaxBatch — потолок пачки кандидатов.
	mfaResetMaxBatch = 1000
)

// ExecuteDueMfaResets — живые сбросы с execute_at <= now, кроме skip: кандидаты без замка, затем каждый — своей
// короткой транзакцией (executeMfaReset). closed — закрытые сбросы (исполненные и отменённые системой); аккаунт под
// замком сценария игрока пропускается (SKIP LOCKED) — его возьмёт следующий запуск. Строка, на которой исполнение
// упало, откатывается одна, уходит в журнал (id сброса и ошибка, без ПД) и в failed — остальные исполняются (m2 ревью
// W5; воркер держит упавшие в skip до конца прогона). Отмена ctx — ошибка сразу. Повтор безопасен.
func (a *App) ExecuteDueMfaResets(ctx context.Context, batch int, skip []uuid.UUID) (closed int, failed []uuid.UUID, err error) {
	rows, err := identitydb.New(a.pool).MfaResetDue(ctx, identitydb.MfaResetDueParams{Now: a.clock.Now(), Skip: skip,
		Batch: int32(min(max(batch, 1), mfaResetMaxBatch))}) //nolint:gosec // G115: пачка ≤ 1000
	if err != nil {
		return 0, nil, fmt.Errorf("identity: сбросы 2FA к исполнению: %w", err)
	}
	a.rowFound(rowMfaResetDue)
	for _, r := range rows {
		ok, rowErr := a.executeMfaReset(ctx, r)
		if rowErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return closed, failed, errors.Join(ctxErr, rowErr)
			}
			a.log.ErrorContext(ctx, "identity: исполнение сброса 2FA не удалось — строка пропущена до следующего запуска",
				"reset_id", r.ID.String(), "err", rowErr)
			failed = append(failed, r.ID)
			continue
		}
		if ok {
			closed++
		}
	}
	return closed, failed, nil
}

// executeMfaReset — одна строка, порядок замков R30 (N5): users FOR NO KEY UPDATE SKIP LOCKED (занят — пропуск), затем
// есть ли user_mfa (нет строки — 2FA нет; без FOR UPDATE: у worker нет UPDATE на user_mfa, а писатели строки — сценарии
// api — сперва берут тот же замок users), затем mfa_resets FOR UPDATE с перепроверкой «живой» (закрыт отменой или
// предыдущим запуском — пропуск). Аккаунт pending_deletion/deleted или 2FA нет — cancelled_by = system, событие
// mfa_reset_cancelled (версия 2), без писем. Иначе удаляются user_mfa и user_recovery_codes (ключи доступа остаются),
// все сессии отзываются (revoked; версия и событие sessions_revoked), executed_at, событие mfa_changed {enabled:
// false}, аудит mfa_reset_executed от system, письмо mfa_reset_done.
func (a *App) executeMfaReset(ctx context.Context, r identitydb.MfaResetDueRow) (closed bool, err error) {
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		closed = false
		q := identitydb.New(tx)
		u, err := q.MfaResetLockUserSkip(ctx, r.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // занят сценарием игрока или удалён (сброс уйдёт каскадом)
		}
		if err != nil {
			return fmt.Errorf("identity: замок аккаунта сброса 2FA: %w", err)
		}
		a.firstLock("execute_mfa_reset")
		hasMfa := true
		if _, err := q.MfaResetUserMfa(ctx, r.UserID); errors.Is(err, pgx.ErrNoRows) {
			hasMfa = false
		} else if err != nil {
			return fmt.Errorf("identity: замок 2FA сброса: %w", err)
		}
		live, err := q.MfaResetLockLive(ctx, r.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // отменён или исполнен, пока ждали
		}
		if err != nil {
			return fmt.Errorf("identity: замок сброса 2FA: %w", err)
		}
		now := a.clock.Now()
		if !hasMfa || u.Status == statusPendingDeletion || u.Status == statusDeleted {
			by := roleSystem
			if err := q.MfaResetCancel(ctx, identitydb.MfaResetCancelParams{Now: &now, CancelledBy: &by, ID: live.ID}); err != nil {
				return fmt.Errorf("identity: отмена сброса 2FA системой: %w", err)
			}
			if _, err := events.Publish(ctx, identity.MfaResetCancelled(identity.MfaResetRef{ID: live.ID, Version: 2},
				live.UserID, live.ExecuteAt)); err != nil {
				return err
			}
			closed = true
			return nil
		}
		if err := q.DeletionDeleteMfa(ctx, live.UserID); err != nil {
			return fmt.Errorf("identity: снятие 2FA: %w", err)
		}
		if err := q.DeletionDeleteRecoveryCodes(ctx, live.UserID); err != nil {
			return fmt.Errorf("identity: резервные коды: %w", err)
		}
		if _, err := a.revokeSessions(ctx, tx, live.UserID, uuid.Nil, domain.RevokedByUser); err != nil {
			return err
		}
		if err := q.MfaResetMarkExecuted(ctx, identitydb.MfaResetMarkExecutedParams{Now: &now, ID: live.ID}); err != nil {
			return fmt.Errorf("identity: исполнение сброса 2FA: %w", err)
		}
		ver, err := q.BumpUserVersion(ctx, live.UserID)
		if err != nil {
			return fmt.Errorf("identity: версия аккаунта: %w", err)
		}
		if _, err := events.Publish(ctx, identity.MfaChanged(identity.UserRef{ID: live.UserID, Version: ver}, false)); err != nil {
			return err
		}
		if err := audit.Write(ctx, audit.Entry{ActorRole: roleSystem, ActorLabel: roleSystem, Action: ActionMfaResetExecuted,
			ObjectType: objectUser, ObjectID: live.UserID, After: map[string]any{"reset_id": live.ID, "mfa_enabled": false}}); err != nil {
			return err
		}
		if err := a.enqueueMail(ctx, tx, MailMfaResetDone, live.ID); err != nil {
			return err
		}
		closed = true
		return nil
	})
	return closed, err
}
