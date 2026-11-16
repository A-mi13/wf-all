package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
)

// Сброс 2FA поддержкой — сторона admin-api (спека §3.15 п. 2–4, §7.1, §7.6; план I-2, Task 17): запрос сотрудником
// (только запрос approvals, право dual), исполнитель identity.mfa_reset при подтверждении вторым сотрудником (строка
// mfa_resets с execute_at = now + 7 д, журнал безопасности, событие, аудит, письмо) и отмена сотрудником сразу.
// Роль admin: users, sessions, user_mfa не пишутся и не берутся под замок (у admin нет UPDATE, К2 — FOR … дал бы
// 42501); «два сброса» закрывает частичный UNIQUE mfa_resets_live_key (23505 → ErrStale), «сброс и отмена» — замок
// строки mfa_resets; статус и 2FA при исполнении по сроку перепроверяет воркер (Task 18). Строка approvals — раньше
// строк модуля (её берёт платформа).

const (
	// ActionMfaReset — действие «вдвоём» в реестре подтверждений.
	ActionMfaReset = "identity.mfa_reset"
	// permMfaReset — право запроса, подтверждения и отмены (dual у владельца и админа, §7.2).
	permMfaReset = "identity.mfa.reset"
	// rowMfaResetState — вид строки для hooks.afterRowFound: состояние игрока прочитано без замка (гонка двух
	// подтверждений, TestMfaResetConcurrentApprovals).
	rowMfaResetState = "mfa_reset_state"

	mfaResetReasonMin = 3
	mfaResetReasonMax = 500
	mfaResetTicketMax = 100

	statusDeleted         = "deleted"
	statusPendingDeletion = "pending_deletion"
	// mfaResetsLiveKey — частичный UNIQUE «один живой сброс на аккаунт» (0025).
	mfaResetsLiveKey  = "mfa_resets_live_key"
	pgUniqueViolation = "23505"
)

var (
	errUserNotFound      = httpx.NewError(http.StatusNotFound, identity.CodeUserNotFound)
	errSelfAction        = httpx.NewError(http.StatusConflict, identity.CodeSelfAction)
	errMfaResetNotEnable = httpx.NewError(http.StatusConflict, identity.CodeMfaNotEnabled)
	errMfaResetPending   = httpx.NewError(http.StatusConflict, identity.CodeMfaResetPending)
	errMfaResetNotFound  = httpx.NewError(http.StatusNotFound, identity.CodeMfaResetNotFound)
	errStaffForbidden    = httpx.NewError(http.StatusForbidden, httpx.CodeForbidden)
	// errBadMfaResetPayload — payload не той формы или версии: сбой (откат, запрос pending), не бизнес-отказ.
	errBadMfaResetPayload = errors.New("identity: payload identity.mfa_reset не той формы")
)

// MfaResetInput — запрос сброса: игрок, причина (3–500 символов) и номер обращения (≤ 100, необязателен).
type MfaResetInput struct {
	UserID uuid.UUID
	Reason string
	Ticket *string
}

// MfaResetCancelled — итог отмены сотрудником (IdentityMfaResetCancelled).
type MfaResetCancelled struct {
	ID          uuid.UUID
	CancelledAt time.Time
}

// mfaResetPayload — payload identity.mfa_reset (v = 1): инициатор (R5), игрок, причина и обращение. Причина — только
// в mfa_resets.reason, в аудит идёт её длина (свободный текст в Before/After не кладётся, §8.3).
type mfaResetPayload struct {
	V int `json:"v"`
	approvals.Initiator
	// Label — подпись инициатора на момент запроса (mfa_resets.requested_by_label)
	Label  string    `json:"init_label"`
	UserID uuid.UUID `json:"user_id"`
	Reason string    `json:"reason"`
	Ticket *string   `json:"ticket,omitempty"`
}

// MfaResetAction — действие identity.mfa_reset для реестра подтверждений (регистрирует identity/admin в сборке
// admin-api до reg.Validate): право identity.mfa.reset, цель — игрок, учётки инициатора и подтверждающего
// перепроверяет хук staff (LockActors, R21).
func (a *App) MfaResetAction() approvals.Action {
	return approvals.Action{Name: ActionMfaReset, Permission: permMfaReset, LockActors: true, Exec: a.execMfaReset,
		Target: func(raw json.RawMessage) (uuid.UUID, uuid.UUID) {
			var p mfaResetPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				return uuid.Nil, uuid.Nil
			}
			return p.UserID, uuid.Nil
		}}
}

// checkMfaResetInput — пределы причины и обращения (символы, как char_length в CHECK mfa_resets); NUL — отказ по
// полю (pattern): text и jsonb approvals.payload его не хранят (22021/22P05 — был бы 500), валидатор контракта его
// пропускает.
func checkMfaResetInput(in MfaResetInput) error {
	if n := utf8.RuneCountInString(in.Reason); n < mfaResetReasonMin {
		return httpx.NewFieldError("body.reason", "minLength")
	} else if n > mfaResetReasonMax {
		return httpx.NewFieldError("body.reason", "maxLength")
	} else if strings.ContainsRune(in.Reason, 0) {
		return httpx.NewFieldError("body.reason", "pattern")
	}
	if in.Ticket != nil {
		if n := utf8.RuneCountInString(*in.Ticket); n < 1 {
			return httpx.NewFieldError("body.ticket", "minLength")
		} else if n > mfaResetTicketMax {
			return httpx.NewFieldError("body.ticket", "maxLength")
		} else if strings.ContainsRune(*in.Ticket, 0) {
			return httpx.NewFieldError("body.ticket", "pattern")
		}
	}
	return nil
}

// mfaResetState — состояние игрока без замка (у admin нет UPDATE на users и user_mfa). Нет строки — ok = false.
func (a *App) mfaResetState(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (identitydb.MfaResetUserStateRow, bool, error) {
	s, err := identitydb.New(tx).MfaResetUserState(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, fmt.Errorf("identity: состояние игрока для сброса 2FA: %w", err)
	}
	a.rowFound(rowMfaResetState)
	return s, true, nil
}

// RequestMfaReset — requestMfaReset (§3.15 п. 2): право identity.mfa.reset не Deny; причина и обращение в пределах;
// игрока нет или он удалён — 404 user_not_found; своя учётка — 409 self_action; нет подтверждённой 2FA —
// mfa_not_enabled; живой сброс — mfa_reset_pending; иначе запрос approvals в одной транзакции (аудит
// platform.approval_requested пишет платформа). Сброс сам не исполняется — только подтверждением вторым.
func (a *App) RequestMfaReset(ctx context.Context, svc *approvals.Service, st *auth.Staff, m approvals.Meta,
	in MfaResetInput) (approvals.Approval, error) {
	if st == nil || st.Decide(permMfaReset, uuid.Nil) == auth.Deny {
		return approvals.Approval{}, errStaffForbidden
	}
	if err := checkMfaResetInput(in); err != nil {
		return approvals.Approval{}, err
	}
	var ap approvals.Approval
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		s, ok, err := a.mfaResetState(ctx, tx, in.UserID)
		switch {
		case err != nil:
			return err
		case !ok || s.Status == statusDeleted:
			return errUserNotFound
		case st.LinkedUserID == in.UserID:
			return errSelfAction
		case !s.MfaEnabled:
			return errMfaResetNotEnable
		case s.ResetPending:
			return errMfaResetPending
		}
		ap, err = svc.Request(ctx, tx, st, st.Label, m, ActionMfaReset, mfaResetPayload{
			V: 1, Initiator: approvals.InitiatorOf(st), Label: st.Label, UserID: in.UserID, Reason: in.Reason,
			Ticket: in.Ticket})
		return err
	})
	return ap, err
}

// execMfaReset — исполнитель identity.mfa_reset (§3.15 п. 3) под savepoint платформы, approver — подтверждающий с
// подписью решения. Подтверждающий связан с игроком — Fail(self_action); игрока нет, он удалён или в удалении, 2FA
// нет, живой сброс уже есть (в том числе вставленный параллельным подтверждением — 23505 mfa_resets_live_key) —
// Fail(ErrStale). Иначе mfa_resets (execute_at = now + 7 д), security_events, событие mfa_reset_scheduled (агрегат
// mfa_reset, версия 1), аудит от подтверждающего (инициатор и approval_id — в after), письмо mfa_reset_scheduled.
func (a *App) execMfaReset(ctx context.Context, tx pgx.Tx, raw json.RawMessage, approver *auth.Staff) error {
	var p mfaResetPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.V != 1 || p.UserID == uuid.Nil || p.Label == "" {
		return fmt.Errorf("%w: v%d: %w", errBadMfaResetPayload, p.V, err)
	}
	if approver.LinkedUserID == p.UserID {
		return approvals.Fail(errSelfAction)
	}
	s, ok, err := a.mfaResetState(ctx, tx, p.UserID)
	if err != nil {
		return err
	}
	if !ok || s.Status == statusDeleted || s.Status == statusPendingDeletion || !s.MfaEnabled || s.ResetPending {
		return approvals.Fail(approvals.ErrStale)
	}
	if checkMfaResetInput(MfaResetInput{Reason: p.Reason, Ticket: p.Ticket}) != nil {
		return approvals.Fail(approvals.ErrStale)
	}
	approvalID, _ := approvals.ApprovalIDFrom(ctx)
	now := a.clock.Now()
	executeAt := now.Add(domain.MfaResetDelay)
	resetID := id.New()
	if err := identitydb.New(tx).MfaResetInsert(ctx, identitydb.MfaResetInsertParams{
		ID: resetID, UserID: p.UserID, ApprovalID: approvalID, RequestedByStaffID: p.ID,
		RequestedByLabel: p.Label, Reason: p.Reason, Ticket: p.Ticket, ExecuteAt: executeAt,
		CreatedAt: now,
	}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgUniqueViolation &&
			pgErr.ConstraintName == mfaResetsLiveKey {
			return approvals.Fail(approvals.ErrStale)
		}
		return fmt.Errorf("identity: строка сброса 2FA: %w", err)
	}
	if _, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: p.UserID, Kind: domain.SecMfaResetScheduled,
		Meta: map[string]any{"reset_id": resetID, "approval_id": approvalID}}); err != nil {
		return err
	}
	if _, err := events.Publish(ctx, identity.MfaResetScheduled(identity.MfaResetRef{ID: resetID, Version: 1}, p.UserID,
		executeAt)); err != nil {
		return err
	}
	after := map[string]any{"reset_id": resetID, "execute_at": executeAt, "approval_id": approvalID,
		"initiator_id": p.ID, "reason_length": utf8.RuneCountInString(p.Reason), "ticket_present": p.Ticket != nil}
	if err := audit.Write(ctx, audit.Entry{ActorStaffID: approver.StaffID, ActorRole: approver.Role,
		ActorLabel: approver.Label, Action: ActionMfaResetRequested, ObjectType: objectUser, ObjectID: p.UserID,
		After: after}); err != nil {
		return err
	}
	return a.enqueueMail(ctx, tx, MailMfaResetScheduled, resetID)
}

// CancelMfaResetByStaff — cancelMfaReset (§3.15 п. 4): право identity.mfa.reset не Deny (NeedsApproval — разрешение:
// отмена привилегий не даёт, §7.2); живой сброс под замком строки, нет или закрыт — 404 mfa_reset_not_found; своя
// учётка — 409 self_action; иначе cancelled_at, cancelled_by = staff:<подпись>, событие mfa_reset_cancelled (версия
// 2), письмо mfa_reset_cancelled, аудит от сотрудника. Одна транзакция (Idempotency-Key).
func (a *App) CancelMfaResetByStaff(ctx context.Context, st *auth.Staff, m approvals.Meta, resetID uuid.UUID) (MfaResetCancelled, error) {
	if st == nil || st.Decide(permMfaReset, uuid.Nil) == auth.Deny {
		return MfaResetCancelled{}, errStaffForbidden
	}
	var out MfaResetCancelled
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := identitydb.New(tx)
		r, err := q.MfaResetLockLive(ctx, resetID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errMfaResetNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: замок сброса 2FA: %w", err)
		}
		if st.LinkedUserID == r.UserID {
			return errSelfAction
		}
		now := a.clock.Now()
		by := "staff:" + st.Label
		if err := q.MfaResetCancel(ctx, identitydb.MfaResetCancelParams{Now: &now, CancelledBy: &by, ID: r.ID}); err != nil {
			return fmt.Errorf("identity: отмена сброса 2FA: %w", err)
		}
		if _, err := events.Publish(ctx, identity.MfaResetCancelled(identity.MfaResetRef{ID: r.ID, Version: 2}, r.UserID,
			r.ExecuteAt)); err != nil {
			return err
		}
		if err := audit.Write(ctx, audit.Entry{ActorStaffID: st.StaffID, ActorRole: st.Role, ActorLabel: st.Label,
			Action: ActionMfaResetCancelled, ObjectType: objectUser, ObjectID: r.UserID,
			After: map[string]any{"reset_id": r.ID, "cancelled_by": "staff"}, IP: m.IP, UserAgent: m.UserAgent}); err != nil {
			return err
		}
		if err := a.enqueueMail(ctx, tx, MailMfaResetCancelled, r.ID); err != nil {
			return err
		}
		out = MfaResetCancelled{ID: r.ID, CancelledAt: now}
		return nil
	})
	return out, err
}
