package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/token"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/domain"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// errReplaced — сессия запроса уже заменена новой (вторая вкладка успела раньше тем же кодом):
// ответ — staff.mfa_invalid без записи неудачи (Review Focus 5).
var errReplaced = errors.New("staff: сессия заменена новой")

// checkFactor — подходит ли второй фактор: TOTP (code) или резервный код (recovery). Строки
// блокируются под уже взятым замком учётки: staff_mfa, затем резервные коды. Принятый шаг TOTP
// пишется в last_used_step (повтор и старые коды отвергаются), резервный код сгорает. Пустой или
// кривой ввод — false (неудача), не ошибка.
func (a *App) checkFactor(ctx context.Context, q *staffdb.Queries, staffID uuid.UUID, code, recovery string, now time.Time) (bool, error) {
	mfa, err := q.LockMFA(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if mfa.ConfirmedAt == nil {
		return false, nil
	}
	switch {
	case code != "":
		secret, err := a.openTOTP(staffID, mfa.SecretEnc, mfa.KeyID)
		if err != nil {
			return false, fmt.Errorf("staff: секрет TOTP: %w", err)
		}
		step, ok := domain.VerifyTOTP(secret, code, now, mfa.LastUsedStep)
		if !ok {
			return false, nil
		}
		return true, q.SetMFAStep(ctx, staffdb.SetMFAStepParams{Step: step, StaffID: staffID})
	case recovery != "":
		norm, ok := domain.NormalizeRecoveryCode(recovery)
		if !ok {
			return false, nil
		}
		return a.useRecoveryCode(ctx, q, staffID, norm, now)
	}
	return false, nil
}

// useRecoveryCode — сверка нормализованного резервного кода со всеми неиспользованными кодами
// сотрудника (HMAC ключом key_id каждой строки, сравнение за постоянное время); совпал — сгорает.
func (a *App) useRecoveryCode(ctx context.Context, q *staffdb.Queries, staffID uuid.UUID, norm string, now time.Time) (bool, error) {
	rows, err := q.LockUnusedRecoveryCodes(ctx, staffID)
	if err != nil {
		return false, err
	}
	macs := map[string][]byte{} // key_id → HMAC введённого кода этим ключом
	var match []byte
	for _, r := range rows {
		m, ok := macs[r.KeyID]
		if !ok {
			if m, err = a.recoveryMAC(r.KeyID, staffID, norm); err != nil {
				return false, fmt.Errorf("staff: HMAC резервного кода: %w", err)
			}
			macs[r.KeyID] = m
		}
		if macEqual(m, r.CodeMac) && match == nil {
			match = r.CodeMac
		}
	}
	if match == nil {
		return false, nil
	}
	n, err := q.UseRecoveryCode(ctx, staffdb.UseRecoveryCodeParams{Now: now, StaffID: staffID, CodeMac: match})
	return n == 1, err
}

// failure — неудача второго фактора своей транзакцией (D8): бизнес-ошибка не откатывает запись.
// Счётчик подряд +1, аудит от учётки (шаг mfa входа — staff.login_failed, прочие —
// staff.factor_failed), на пятой — блокировка (lockOut). kind — ErrMFAInvalid или
// ErrPasswordInvalid (неверный текущий пароль в «Моём доступе», 6b). Ответ — *staff.MFAError.
func (a *App) failure(ctx context.Context, s *auth.Staff, step string, m staff.Meta, kind error) error {
	now := a.clock.Now()
	left := 0
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, err := q.LockAccount(ctx, s.StaffID)
		if err != nil {
			return err
		}
		n, err := q.AddFactorFailure(ctx, s.StaffID)
		if err != nil {
			return err
		}
		left = attemptsLeft(n)
		action := actionFactorFailed
		if step == stepMFA {
			action = actionLoginFailed
		}
		if err := audit.Write(ctx, audit.Entry{
			ActorStaffID: s.StaffID, ActorRole: acct.Role, ActorLabel: label(acct.DisplayName),
			Action: action, ObjectType: objectAccount, ObjectID: s.StaffID,
			After: map[string]any{"step": step, "attempts_left": left},
			IP:    m.IP, UserAgent: m.UserAgent,
		}); err != nil {
			return err
		}
		if n >= maxFactorFailures {
			return a.lockOut(ctx, q, tx, acct, s.SessionID, now)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return &staff.MFAError{AttemptsLeft: left, Err: kind}
}

// lockOut — блокировка учётки на час под замком учётки: все сессии гасятся (locked), событие
// staff.locked, аудит от системы; письмо staff.locked владельцам и админам — не чаще раза в час на
// учётку (lock_notified_at). ref письма — сессия, на которой закрылась учётка.
func (a *App) lockOut(ctx context.Context, q *staffdb.Queries, tx pgx.Tx, acct staffdb.LockAccountRow, sessionID uuid.UUID, now time.Time) error {
	until := now.Add(lockDuration)
	notify := acct.LockNotifiedAt == nil || now.Sub(*acct.LockNotifiedAt) >= lockNotifyEvery
	var notifiedAt *time.Time
	if notify {
		notifiedAt = &now
	}
	version, err := q.LockOut(ctx, staffdb.LockOutParams{LockedUntil: until, NotifiedAt: notifiedAt, ID: acct.ID})
	if err != nil {
		return err
	}
	if _, err := q.RevokeStaffSessions(ctx, staffdb.RevokeStaffSessionsParams{Now: now, Reason: reasonLocked, StaffID: acct.ID}); err != nil {
		return err
	}
	if _, err := events.Publish(ctx, staff.LockedEvent(acct.ID, version, until)); err != nil {
		return err
	}
	if err := audit.Write(ctx, audit.Entry{
		ActorRole: roleSystem, ActorLabel: roleSystem,
		Action: actionLocked, ObjectType: objectAccount, ObjectID: acct.ID,
		After: map[string]any{"locked_until": until},
	}); err != nil {
		return err
	}
	if !notify {
		return nil
	}
	recipients, err := q.ListLockRecipients(ctx)
	if err != nil {
		return err
	}
	for _, r := range recipients {
		if _, err := a.river.InsertTx(ctx, tx, staff.NewMailArgs(staff.MailLocked, sessionID, r), nil); err != nil {
			return fmt.Errorf("staff: письмо staff.locked: %w", err)
		}
	}
	return nil
}

// passFactor — второй фактор для сессии s на шаге step: mfa (стадия mfa_pending → active) или unlock
// (active, снимает Reauth). Одна транзакция: замок учётки, затем сессии; фактор не подошёл —
// откат и запись неудачи своей транзакцией. Успех — новый токен: старая строка гасится как
// replaced (её токен ещё минуту отвечает «заменён», не стирая новую cookie), новая — active с
// mfa_at и новым IP.
func (a *App) passFactor(ctx context.Context, s *auth.Staff, code, recovery string, m staff.Meta, step string) (string, error) {
	wantStage, action := auth.StageMFAPending, actionLogin
	if step == stepUnlock {
		wantStage, action = auth.StageActive, actionSessionUnlock
	}
	if s.Stage != wantStage {
		return "", staff.ErrWrongStage
	}
	now := a.clock.Now()
	var (
		raw  string
		acct staffdb.LockAccountRow
	)
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		var err error
		if acct, err = q.LockAccount(ctx, s.StaffID); errors.Is(err, pgx.ErrNoRows) {
			return auth.ErrSessionInvalid
		} else if err != nil {
			return err
		}
		sess, err := q.LockSession(ctx, s.SessionID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return auth.ErrSessionInvalid
		case err != nil:
			return err
		case sess.StaffID != s.StaffID:
			return auth.ErrSessionInvalid
		case sess.RevokedReason != nil && *sess.RevokedReason == reasonReplaced:
			return errReplaced
		case sess.RevokedReason != nil || !now.Before(sess.ExpiresAt):
			return auth.ErrSessionInvalid
		case sess.Stage != wantStage:
			return staff.ErrWrongStage
		case acct.Status != statusActive || acct.LockedUntil != nil && now.Before(*acct.LockedUntil):
			return auth.ErrSessionInvalid
		}
		ok, err := a.checkFactor(ctx, q, s.StaffID, code, recovery, now)
		if err != nil {
			return err
		}
		if !ok {
			return errFactorFailed
		}
		created, expires := now, now.Add(sessionTTL)
		var loginAt *time.Time
		if step == stepUnlock {
			created, expires = sess.CreatedAt, sess.ExpiresAt // абсолютный срок — от входа
		} else {
			loginAt = &now
		}
		if raw, err = a.replaceSession(ctx, q, s.SessionID, newSession{
			staffID: s.StaffID, stage: auth.StageActive, created: created, expires: expires, mfaAt: &now,
		}, now, m); err != nil {
			return err
		}
		if err := q.FactorSucceeded(ctx, staffdb.FactorSucceededParams{LoginAt: loginAt, ID: s.StaffID}); err != nil {
			return err
		}
		return audit.Write(ctx, audit.Entry{
			ActorStaffID: s.StaffID, ActorRole: acct.Role, ActorLabel: label(acct.DisplayName),
			Action: action, ObjectType: objectAccount, ObjectID: s.StaffID,
			IP: m.IP, UserAgent: m.UserAgent,
		})
	})
	switch {
	case errors.Is(err, errFactorFailed):
		return "", a.failure(ctx, s, step, m, staff.ErrMFAInvalid)
	case errors.Is(err, errReplaced):
		return "", &staff.MFAError{AttemptsLeft: attemptsLeft(acct.MfaFailures), Err: staff.ErrMFAInvalid}
	case err != nil:
		return "", err
	}
	return raw, nil
}

// newSession — поля новой строки staff_sessions.
type newSession struct {
	staffID          uuid.UUID
	stage            string
	created, expires time.Time
	mfaAt            *time.Time
}

// replaceSession — смена токена (D9): старая сессия old (если задана) гасится как replaced, новая
// создаётся с новым токеном, текущим IP и User-Agent. Возвращает токен для cookie. Общая для
// второго фактора, unlock, входа со старой cookie и recovery-codes/ack (Task 7).
func (a *App) replaceSession(ctx context.Context, q *staffdb.Queries, old uuid.UUID, ns newSession, now time.Time, m staff.Meta) (string, error) {
	if old != uuid.Nil {
		if _, err := q.RevokeSession(ctx, staffdb.RevokeSessionParams{Now: now, Reason: reasonReplaced, ID: old}); err != nil {
			return "", err
		}
	}
	raw, hash, err := token.NewRefresh()
	if err != nil {
		return "", err
	}
	err = q.InsertSession(ctx, staffdb.InsertSessionParams{
		ID: id.New(), StaffID: ns.staffID, TokenHash: hash, Stage: staffdb.StaffSessionStage(ns.stage),
		CreatedAt: ns.created, LastSeenAt: now, MfaAt: ns.mfaAt, ExpiresAt: ns.expires,
		Ip: m.IP, UserAgent: m.UserAgent,
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// attemptsLeft — сколько неудач подряд осталось до блокировки при failures уже случившихся.
func attemptsLeft(failures int32) int {
	return max(0, maxFactorFailures-int(failures))
}

// label — снимок отображаемого имени для аудита (ActorLabel): без крайних пробелов.
func label(displayName string) string { return strings.TrimSpace(displayName) }
