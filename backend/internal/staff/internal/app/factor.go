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

// factorCheck — проверка второго фактора (или текущего пароля, 6b) под замком учётки acct, который
// уже взял withFactor, в его транзакции. fail — вид неудачи (staff.ErrMFAInvalid или
// staff.ErrPasswordInvalid; nil — успех): withFactor запишет её в этой же транзакции. Ошибка err —
// откат без записи неудачи (сессия погашена, не та стадия, сбой).
type factorCheck func(ctx context.Context, tx pgx.Tx, q *staffdb.Queries, acct staffdb.LockAccountRow, now time.Time) (fail, err error)

// withFactor — шаг с проверкой второго фактора (вход, unlock, смена пароля; 7 — перевыпуск кодов):
// одна транзакция, замок учётки, затем check. Неудача пишется recordFailure в той же транзакции под
// тем же замком и коммитится до ответа (D8, раунд 1): параллельные попытки идут строго по очереди,
// порог «5 подряд» не обойти, а под Idempotency-Key закоммичена ровно одна транзакция. Транзакция
// идёт на ctx без отмены (detached): обрыв соединения не теряет неудачу. Неудача —
// *staff.MFAError{AttemptsLeft, вид от check}; засчитываются только виды ErrMFAInvalid и
// ErrPasswordInvalid, иной fail — ошибка без записи неудачи. Сессию и закрытость учётки check проверяет сам
// (lockLiveSession) — withFactor их не навязывает: путь неудачи на уже закрытой учётке должен
// оставаться без повторной блокировки (TestNoRelockWhileLocked).
func (a *App) withFactor(ctx context.Context, s *auth.Staff, step string, m staff.Meta, check factorCheck) error {
	dctx, cancel := detached(ctx)
	defer cancel()
	var (
		fail error
		left int
	)
	err := db.InTx(dctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, err := q.LockAccount(ctx, s.StaffID)
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.ErrSessionInvalid
		}
		if err != nil {
			return err
		}
		now := a.clock.Now() // после замка: параллельная попытка могла ждать его
		if fail, err = check(ctx, tx, q, acct, now); err != nil || fail == nil {
			return err
		}
		if !errors.Is(fail, staff.ErrMFAInvalid) && !errors.Is(fail, staff.ErrPasswordInvalid) {
			// не вид неудачи, а ошибка сценария: откат без записи неудачи
			return fail
		}
		left, err = a.recordFailure(ctx, q, tx, acct, s, step, m, now)
		return err
	})
	if err != nil {
		return err
	}
	if fail != nil {
		return &staff.MFAError{AttemptsLeft: left, Err: fail}
	}
	return nil
}

// lockLiveSession — сессия запроса под замком учётки acct (порядок замков: учётка → сессия) и
// проверка, что по ней ещё можно действовать: нет, чужая, погашена, истекла или учётка закрыта
// (не active, locked_until в будущем) — auth.ErrSessionInvalid; заменена новой (вторая вкладка
// успела раньше) — errReplaced. Стадию проверяет вызывающий. Первый шаг каждого factorCheck: отказ
// здесь — без записи неудачи.
func lockLiveSession(ctx context.Context, q *staffdb.Queries, s *auth.Staff, acct staffdb.LockAccountRow, now time.Time) (staffdb.LockSessionRow, error) {
	sess, err := q.LockSession(ctx, s.SessionID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return sess, auth.ErrSessionInvalid
	case err != nil:
		return sess, err
	case sess.StaffID != s.StaffID:
		return sess, auth.ErrSessionInvalid
	case sess.RevokedReason != nil && *sess.RevokedReason == reasonReplaced:
		return sess, errReplaced
	case sess.RevokedReason != nil || !now.Before(sess.ExpiresAt):
		return sess, auth.ErrSessionInvalid
	case acct.Status != statusActive || acct.LockedUntil != nil && now.Before(*acct.LockedUntil):
		return sess, auth.ErrSessionInvalid
	}
	return sess, nil
}

// recordFailure — неудача второго фактора под уже взятым замком учётки acct: счётчик подряд +1,
// аудит от учётки (шаг mfa входа — staff.login_failed, прочие — staff.factor_failed); счётчик
// дошёл до порога, а учётка ещё не закрыта — блокировка (lockOut). Возвращает attempts_left.
func (a *App) recordFailure(ctx context.Context, q *staffdb.Queries, tx pgx.Tx, acct staffdb.LockAccountRow, s *auth.Staff, step string, m staff.Meta, now time.Time) (int, error) {
	n, err := q.AddFactorFailure(ctx, acct.ID)
	if err != nil {
		return 0, err
	}
	left := attemptsLeft(n)
	action := actionFactorFailed
	if step == stepMFA {
		action = actionLoginFailed
	}
	if err := audit.Write(ctx, audit.Entry{
		ActorStaffID: acct.ID, ActorRole: acct.Role, ActorLabel: label(acct.DisplayName),
		Action: action, ObjectType: objectAccount, ObjectID: acct.ID,
		After: map[string]any{"step": step, "attempts_left": left},
		IP:    m.IP, UserAgent: m.UserAgent,
	}); err != nil {
		return 0, err
	}
	locked := acct.LockedUntil != nil && now.Before(*acct.LockedUntil)
	if n >= maxFactorFailures && !locked {
		if err := a.lockOut(ctx, q, tx, acct, s.SessionID, now); err != nil {
			return 0, err
		}
	}
	return left, nil
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
// (active, снимает Reauth) — через withFactor. Под замком учётки сначала сессия: погашена, истекла,
// учётка закрыта — auth.ErrSessionInvalid без записи неудачи; заменена (вторая вкладка успела
// раньше тем же кодом) — staff.mfa_invalid без записи неудачи. Успех — новый токен: старая строка
// гасится как replaced (её токен ещё минуту отвечает «заменён», не стирая новую cookie), новая —
// active с mfa_at и новым IP; аудит с method = totp | recovery.
func (a *App) passFactor(ctx context.Context, s *auth.Staff, code, recovery string, m staff.Meta, step string) (string, error) {
	wantStage, action := auth.StageMFAPending, actionLogin
	if step == stepUnlock {
		wantStage, action = auth.StageActive, actionUnlock
	}
	if s.Stage != wantStage {
		return "", staff.ErrWrongStage
	}
	var (
		raw      string
		failures int32
	)
	err := a.withFactor(ctx, s, step, m, func(ctx context.Context, _ pgx.Tx, q *staffdb.Queries, acct staffdb.LockAccountRow, now time.Time) (error, error) {
		failures = acct.MfaFailures
		sess, err := lockLiveSession(ctx, q, s, acct, now)
		if err != nil {
			return nil, err
		}
		if sess.Stage != wantStage {
			return nil, staff.ErrWrongStage
		}
		ok, err := a.checkFactor(ctx, q, s.StaffID, code, recovery, now)
		if err != nil {
			return nil, err
		}
		if !ok {
			return staff.ErrMFAInvalid, nil
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
			return nil, err
		}
		if err := q.FactorSucceeded(ctx, staffdb.FactorSucceededParams{LoginAt: loginAt, ID: s.StaffID}); err != nil {
			return nil, err
		}
		return nil, audit.Write(ctx, audit.Entry{
			ActorStaffID: s.StaffID, ActorRole: acct.Role, ActorLabel: label(acct.DisplayName),
			Action: action, ObjectType: objectAccount, ObjectID: s.StaffID,
			After: map[string]any{"method": factorMethod(code)},
			IP:    m.IP, UserAgent: m.UserAgent,
		})
	})
	if errors.Is(err, errReplaced) {
		return "", &staff.MFAError{AttemptsLeft: attemptsLeft(failures), Err: staff.ErrMFAInvalid}
	}
	if err != nil {
		return "", err
	}
	return raw, nil
}

// factorMethod — способ второго фактора для аудита: код из приложения (totp) или резервный код.
func factorMethod(code string) string {
	if code != "" {
		return methodTOTP
	}
	return methodRecovery
}

// detached — ctx записи, которая должна дойти до базы и при обрыве соединения клиента (неудача
// второго фактора, аудит неудачи пароля): без отмены родителя, но со своим сроком.
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), detachedTimeout)
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
