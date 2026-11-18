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

// mfaResetReasonErased — заглушка mfa_resets.reason анонимизированного аккаунта: колонка NOT NULL с CHECK 3–500
// символов, а причина — свободный текст сотрудника (может называть игрока).
const mfaResetReasonErased = "---"

// AnonymizeDue — необратимая анонимизация аккаунтов с наступившим purge_at (§3.11 п. 3; воркер, роль worker,
// права §5.5). Пачка — одна tx: строки users status = pending_deletion AND purge_at ≤ now под FOR NO KEY UPDATE SKIP
// LOCKED (R30: users первым; строку под замком отмены удаления пропустить — отмена решит её судьбу). Каждая строка —
// под своим SAVEPOINT: строка, на которой анонимизация упала, откатывается одна (с письмом, событием и аудитом) и
// уходит в журнал (id и ошибка), пачка идёт дальше — отравленная строка не останавливает анонимизацию остальных.
// Отмена ctx и сбой самой транзакции — откат пачки целиком. На строку: журнал account_deleted с адресом в meta_enc
// (Seal кольцом, aad = id записи) и письмо по нему — ДО замены почты (I3: meta_enc записи запроса чистка стирает
// раньше); все ПД аккаунта затираются (решение пользователя 15.11; список колонок — DeletionAnonymizeUser и
// DeletionScrubApplications; причина и тикет сбросов 2FA — DeletionScrubMfaResets), ник → domain.DeletedNickname (индекс ника частичный — коллизии и повтора при 23505 нет),
// прежний — в карантин 90 дней; удаляются секреты, привязки, согласия, коды с адресом, история ника, сессии и прочий
// журнал безопасности; допуски капитана — revoked (account_deleted); событие user_deleted, аудит system. Повтор на
// deleted — ничего (строка не выбирается). skip — строки, упавшие раньше в этом прогоне воркера (m8: не выбираются);
// selected — выбрано строк, failed — упавшие в этой пачке (анонимизировано selected − len(failed)); воркер идёт, пока
// выбрана полная пачка.
func (a *App) AnonymizeDue(ctx context.Context, batch int, skip []uuid.UUID) (selected int, failed []uuid.UUID, err error) {
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		selected, failed = 0, nil
		q := identitydb.New(tx)
		now := a.clock.Now()
		rows, err := q.DeletionDue(ctx, identitydb.DeletionDueParams{Now: &now, Skip: skip, Batch: int32(min(batch, 1000))}) //nolint:gosec // G115: пачка ≤ 1000
		if err != nil {
			return fmt.Errorf("identity: аккаунты к анонимизации: %w", err)
		}
		a.firstLock("anonymize")
		selected = len(rows)
		for _, r := range rows {
			ok, err := a.anonymizeSaved(ctx, tx, r)
			if err != nil {
				return err
			}
			if !ok {
				failed = append(failed, r.ID)
			}
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return selected, failed, nil
}

// anonymizeSaved — anonymize под SAVEPOINT: ошибка строки — откат до точки сохранения и журнал, ok = false; ошибка
// самой точки сохранения или отмена ctx — err (откат пачки).
func (a *App) anonymizeSaved(ctx context.Context, tx pgx.Tx, r identitydb.DeletionDueRow) (ok bool, err error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("identity: точка сохранения анонимизации: %w", err)
	}
	rowErr := a.anonymize(ctx, sp, identitydb.New(sp), r)
	if rowErr == nil {
		if err := sp.Commit(ctx); err != nil {
			return false, fmt.Errorf("identity: точка сохранения анонимизации: %w", err)
		}
		return true, nil
	}
	if err := sp.Rollback(ctx); err != nil {
		return false, errors.Join(rowErr, fmt.Errorf("identity: откат строки анонимизации: %w", err))
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, errors.Join(ctxErr, rowErr)
	}
	a.log.ErrorContext(ctx, "identity: анонимизация аккаунта не удалась — строка пропущена до следующего запуска",
		"user_id", r.ID.String(), "err", rowErr)
	return false, nil
}

// anonymize — одна строка пачки (users уже под замком DeletionDue).
func (a *App) anonymize(ctx context.Context, tx pgx.Tx, q *identitydb.Queries, r identitydb.DeletionDueRow) error {
	now := a.clock.Now()
	ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: r.ID, Kind: domain.SecAccountDeleted, Address: r.Email})
	if err != nil {
		return err
	}
	if err := a.enqueueMail(ctx, tx, MailAccountDeleted, ref); err != nil {
		return err
	}
	if r.NicknameNormalized != nil && *r.NicknameNormalized != "" {
		if err := q.DeletionDropReservation(ctx, *r.NicknameNormalized); err != nil {
			return fmt.Errorf("identity: карантин ника: %w", err)
		}
		if err := q.DeletionReserveNickname(ctx, identitydb.DeletionReserveNicknameParams{
			NicknameNormalized: *r.NicknameNormalized, UserID: &r.ID, Now: now,
			ExpiresAt: now.Add(domain.NicknameQuarantine)}); err != nil {
			return fmt.Errorf("identity: карантин ника: %w", err)
		}
	}
	ver, err := q.DeletionAnonymizeUser(ctx, identitydb.DeletionAnonymizeUserParams{Now: &now,
		Email: "deleted+" + r.ID.String() + "@invalid", Nickname: domain.DeletedNickname(r.ID), ID: r.ID})
	if err != nil {
		return fmt.Errorf("identity: анонимизация: %w", err)
	}
	if err := q.DeletionScrubApplications(ctx, r.ID); err != nil {
		return fmt.Errorf("identity: заявки в капитаны: %w", err)
	}
	for _, del := range []func(context.Context, *identitydb.Queries) error{
		func(ctx context.Context, q *identitydb.Queries) error { return q.DeletionDeleteCredentials(ctx, r.ID) },
		func(ctx context.Context, q *identitydb.Queries) error { return q.DeletionDeleteMfa(ctx, r.ID) },
		func(ctx context.Context, q *identitydb.Queries) error {
			return q.DeletionDeleteRecoveryCodes(ctx, r.ID)
		},
		func(ctx context.Context, q *identitydb.Queries) error { return q.DeletionDeletePasskeys(ctx, r.ID) },
		// привязки Apple и Google: отзыв токена Apple задачей после коммита — точка расширения I-3 (в I-2 привязок нет)
		func(ctx context.Context, q *identitydb.Queries) error { return q.DeletionDeleteIdentities(ctx, r.ID) },
		func(ctx context.Context, q *identitydb.Queries) error { return q.DeletionDeleteConsents(ctx, r.ID) },
		func(ctx context.Context, q *identitydb.Queries) error {
			return q.DeletionDeleteCodes(ctx, identitydb.DeletionDeleteCodesParams{UserID: &r.ID, Email: r.Email})
		},
		func(ctx context.Context, q *identitydb.Queries) error {
			return q.DeletionDeleteNicknameChanges(ctx, r.ID)
		},
		func(ctx context.Context, q *identitydb.Queries) error { return q.DeletionDeleteSessions(ctx, r.ID) },
		func(ctx context.Context, q *identitydb.Queries) error {
			return q.DeletionDeleteSecurityEvents(ctx, identitydb.DeletionDeleteSecurityEventsParams{UserID: &r.ID, Keep: ref})
		},
	} {
		if err := del(ctx, q); err != nil {
			return fmt.Errorf("identity: удаление секретов, привязок и следов: %w", err)
		}
	}
	// допуски капитана — revoked (account_deleted) с событием captain_permit_revoked на каждый (R24): тем же путём,
	// что RevokeRoleInTx (строка заявки раньше строки допуска, R7)
	if _, err := a.revokePermitsTx(ctx, tx, r.ID, nil, RevokeBy{System: true}, revokeReasonAccountDeleted, false); err != nil {
		return err
	}
	// сброс 2FA, назначенный в период отмены (admin-api не видит статус удаления), — отменён системой
	if err := a.cancelMfaResetsBySystem(ctx, q, r.ID); err != nil {
		return err
	}
	if err := q.DeletionScrubMfaResets(ctx, identitydb.DeletionScrubMfaResetsParams{ReasonErased: mfaResetReasonErased,
		UserID: r.ID}); err != nil {
		return fmt.Errorf("identity: причина сброса 2FA: %w", err)
	}
	if _, err := events.Publish(ctx, identity.UserDeleted(identity.UserRef{ID: r.ID, Version: ver})); err != nil {
		return err
	}
	return audit.Write(ctx, audit.Entry{ActorRole: roleSystem, ActorLabel: roleSystem, Action: ActionUserDeleted,
		ObjectType: objectUser, ObjectID: r.ID})
}
