package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
)

// Удаление аккаунта и его отмена (спека §3.11 п. 1–2) — владелец Task 15; анонимизация — anonymize.go.

var (
	errConfirmMismatch     = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeConfirmMismatch)
	errRestoreTokenInvalid = httpx.NewError(http.StatusGone, identity.CodeRestoreTokenInvalid)
)

// DeleteAccount — запрос удаления (§3.11 п. 1): свежий вход (requireFresh: users, затем sessions — R30), ник
// подтверждения (NFKC, без учёта регистра) ≠ текущему → 422 confirm_mismatch без изменений. Одна tx: статус
// pending_deletion (прежний статус — в status_before_deletion) с purge_at = now + 30 дней, событие
// user_deletion_scheduled; все сессии (account_deleted); открытая заявка в капитаны — withdrawn; живой сброс 2FA —
// cancelled_by = system (событие mfa_reset_cancelled, без письма); живые коды аккаунта — revoked; живые restore_token
// прошлых периодов — погашены (m3); журнал с адресом в meta_enc и письмо account_deletion_scheduled; аудит.
func (a *App) DeleteAccount(ctx context.Context, sessionID uuid.UUID, confirmNickname string) (purgeAt time.Time, err error) {
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		s, err := a.requireFresh(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		u, err := a.lockUser(ctx, tx, s.UserID)
		if errors.Is(err, errUserGone) {
			return errSessionGone
		}
		if err != nil {
			return err
		}
		if !sameNickname(confirmNickname, u.Nickname) {
			return errConfirmMismatch
		}
		q := identitydb.New(tx)
		now := a.clock.Now()
		purgeAt = now.Add(domain.DeletionGrace)
		ver, err := q.DeletionSchedule(ctx, identitydb.DeletionScheduleParams{Now: &now, PurgeAt: &purgeAt, ID: u.ID})
		if err != nil {
			return fmt.Errorf("identity: запрос удаления: %w", err)
		}
		if _, err := events.Publish(ctx, identity.UserDeletionScheduled(identity.UserRef{ID: u.ID, Version: ver}, purgeAt)); err != nil {
			return err
		}
		if _, err := a.revokeSessions(ctx, tx, u.ID, uuid.Nil, domain.RevokedAccountDeleted); err != nil {
			return err
		}
		if err := q.DeletionWithdrawApplications(ctx, identitydb.DeletionWithdrawApplicationsParams{Now: &now, UserID: u.ID}); err != nil {
			return fmt.Errorf("identity: заявка в капитаны: %w", err)
		}
		if err := a.cancelMfaResetsBySystem(ctx, q, u.ID); err != nil {
			return err
		}
		if err := q.DeletionRevokeCodes(ctx, identitydb.DeletionRevokeCodesParams{Now: &now, UserID: &u.ID}); err != nil {
			return fmt.Errorf("identity: коды аккаунта: %w", err)
		}
		if err := q.DeletionRevokeRestoreTokens(ctx, identitydb.DeletionRevokeRestoreTokensParams{Now: &now, UserID: &u.ID}); err != nil {
			return fmt.Errorf("identity: токены отмены удаления: %w", err)
		}
		ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: u.ID, Kind: domain.SecAccountDeletionScheduled,
			SessionID: sessionID, Address: u.Email, Meta: map[string]any{"purge_at": purgeAt.UTC().Format(time.RFC3339)}})
		if err != nil {
			return err
		}
		if err := a.enqueueMail(ctx, tx, MailAccountDeletionScheduled, ref); err != nil {
			return err
		}
		return a.auditUser(ctx, ActionUserDeletionRequested, u.ID, nil, map[string]any{"purge_at": purgeAt.UTC()})
	})
	if err != nil {
		return time.Time{}, err
	}
	return purgeAt, nil
}

// sameNickname — ник подтверждения совпадает с ником аккаунта: NFKC, пробелы по краям, без учёта регистра.
func sameNickname(confirm, nick string) bool {
	f := func(s string) string { return strings.TrimFunc(norm.NFKC.String(s), unicode.IsSpace) }
	return strings.EqualFold(f(confirm), f(nick))
}

// cancelMfaResetsBySystem — живой сброс 2FA поддержкой отменяется системой (m4): событие mfa_reset_cancelled
// (версия агрегата 2) и аудит identity.mfa_reset_cancelled с автором system (§8.3, m4 ревью 3), письма нет. Строки
// mfa_resets — после users и sessions (R30).
func (a *App) cancelMfaResetsBySystem(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) error {
	now := a.clock.Now()
	rows, err := q.DeletionCancelMfaResets(ctx, identitydb.DeletionCancelMfaResetsParams{Now: &now, UserID: userID})
	if err != nil {
		return fmt.Errorf("identity: отмена сброса 2FA: %w", err)
	}
	for _, r := range rows {
		if _, err := events.Publish(ctx, identity.MfaResetCancelled(identity.MfaResetRef{ID: r.ID, Version: 2}, userID,
			r.ExecuteAt)); err != nil {
			return err
		}
		if err := audit.Write(ctx, audit.Entry{ActorRole: roleSystem, ActorLabel: roleSystem,
			Action: ActionMfaResetCancelled, ObjectType: objectUser, ObjectID: userID,
			After: map[string]any{"reset_id": r.ID, "cancelled_by": roleSystem}}); err != nil {
			return err
		}
	}
	return nil
}

// CancelDeletion — отмена удаления по restore_token (§3.11 п. 2): takeChallenge(deletion_restore) приёмом R30 (users
// раньше FOR UPDATE вызова) — нет, истёк или погашен → 410 restore_token_invalid; мобильный без device → 422; аккаунт
// уже не pending_deletion (анонимизирован) или purge_at наступил (срок вышел, m4 ревью 2) → 410; клиент не тот, что
// на первом шаге (stepClientMatches: вид клиента, устройство мобильного) → 410 без записей. Иначе прежний статус (active | suspended — решение пользователя
// 15.11), поля удаления — NULL, событие
// user_deletion_cancelled, журнал и письмо account_restored, аудит, токен погашен и сессия через finishLogin (способ и
// второй фактор — из data вызова); documents_acceptance_required — в той же транзакции (m1).
func (a *App) CancelDeletion(ctx context.Context, raw string, c Client) (AuthOutcome, error) {
	var out AuthOutcome
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		ch, err := a.takeChallenge(ctx, tx, domain.ChallengeDeletionRestore, raw)
		if errors.Is(err, errChallengeGone) {
			return errRestoreTokenInvalid
		}
		if err != nil {
			return err
		}
		a.firstLock("cancel_deletion")
		if err := a.requireDevice(c); err != nil {
			return err
		}
		if ch.UserID == nil {
			return errRestoreTokenInvalid
		}
		u, err := a.lockUser(ctx, tx, *ch.UserID)
		if errors.Is(err, errUserGone) {
			return errRestoreTokenInvalid
		}
		if err != nil {
			return err
		}
		if u.Status != identitydb.UserStatusPendingDeletion || a.purgeDue(u.Status, u.PurgeAt) {
			return errRestoreTokenInvalid // m4 ревью 2: ровно purge_at и позже — срок отмены вышел
		}
		var data challengeData
		if err := json.Unmarshal(ch.Data, &data); err != nil {
			return fmt.Errorf("identity: данные вызова: %w", err)
		}
		if same, err := a.stepClientMatches(ctx, tx, data, c); err != nil {
			return err
		} else if !same {
			return errRestoreTokenInvalid // m2 ревью W5-А: без записей, токен остаётся у клиента первого шага
		}
		q := identitydb.New(tx)
		ver, err := q.DeletionRestore(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("identity: отмена удаления: %w", err)
		}
		if err := a.consumeChallenge(ctx, tx, ch.ID); err != nil {
			return err
		}
		if _, err := events.Publish(ctx, identity.UserDeletionCancelled(identity.UserRef{ID: u.ID, Version: ver})); err != nil {
			return err
		}
		deviceID, err := a.upsertDevice(ctx, tx, c)
		if err != nil {
			return err
		}
		ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: u.ID, Kind: domain.SecAccountRestored,
			DeviceID: deviceID, IP: c.IP, UserAgent: c.UserAgent, Meta: map[string]any{"client_kind": string(c.Kind)}})
		if err != nil {
			return err
		}
		if err := a.enqueueMail(ctx, tx, MailAccountRestored, ref); err != nil {
			return err
		}
		if err := a.auditUser(ctx, ActionUserDeletionCancelled, u.ID, nil, nil); err != nil {
			return err
		}
		method := data.Method
		if method == "" {
			method = domain.MethodPassword
		}
		out.LoginOutcome, err = a.finishLogin(ctx, tx, u, c, deviceID, method, data.MfaDone, false)
		if err != nil || out.Status != StatusAuthenticated {
			return err
		}
		// m1: ответ собирается в транзакции — после коммита база не читается
		me, err := a.me(ctx, q, u.ID)
		if err != nil {
			return err
		}
		out.DocumentsRequired = me.DocumentsAcceptanceRequired
		return nil
	})
	return out, err
}
