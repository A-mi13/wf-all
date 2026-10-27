package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/domain"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// lockSetup — учётка и сессия настройки 2FA под замками (порядок: учётка → сессия): сессии или
// учётки нет, сессия чужая, погашена, истекла или учётка выключена — auth.ErrSessionInvalid;
// заменена (повторное принятие, ack в другой вкладке) — auth.ErrSessionReplaced; сессия не в стадии
// setup или учётка уже не invited — staff.ErrWrongStage. lockLiveSession здесь не подходит: он
// требует учётку active, а в setup она invited.
func (a *App) lockSetup(ctx context.Context, q *staffdb.Queries, s *auth.Staff) (staffdb.LockAccountRow, staffdb.LockSessionRow, time.Time, error) {
	var sess staffdb.LockSessionRow
	if s.Stage != auth.StageSetup {
		return staffdb.LockAccountRow{}, sess, time.Time{}, staff.ErrWrongStage
	}
	acct, err := q.LockAccount(ctx, s.StaffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return acct, sess, time.Time{}, auth.ErrSessionInvalid
	}
	if err != nil {
		return acct, sess, time.Time{}, err
	}
	now := a.clock.Now() // после замка: параллельный запрос мог держать учётку
	sess, err = q.LockSession(ctx, s.SessionID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return acct, sess, now, auth.ErrSessionInvalid
	case err != nil:
		return acct, sess, now, err
	case sess.StaffID != s.StaffID:
		return acct, sess, now, auth.ErrSessionInvalid
	case sess.RevokedReason != nil && *sess.RevokedReason == reasonReplaced:
		return acct, sess, now, auth.ErrSessionReplaced
	case sess.RevokedReason != nil || !now.Before(sess.ExpiresAt) || acct.Status == statusDisabled:
		return acct, sess, now, auth.ErrSessionInvalid
	case sess.Stage != auth.StageSetup || acct.Status != statusInvited:
		return acct, sess, now, staff.ErrWrongStage
	}
	return acct, sess, now, nil
}

// MFASetup — первый шаг настройки 2FA (§4.5), только в стадии setup: новый секрет TOTP (20 байт),
// зашифрованный кольцом (aad — id сотрудника, как при входе), заменяет прежний неподтверждённый.
// Подтверждённый секрет не перевыпускается — staff.ErrWrongStage. Возвращает секрет в base32 и
// otpauth-ссылку: издатель — хост ADMIN_ORIGIN без порта (двоеточие разделяет издателя и аккаунт в
// метке), аккаунт — почта.
func (a *App) MFASetup(ctx context.Context, s *auth.Staff) (secretBase32, otpauthURI string, err error) {
	issuer, err := a.issuer()
	if err != nil {
		return "", "", err
	}
	var secret []byte
	var email string
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, _, _, err := a.lockSetup(ctx, q, s)
		if err != nil {
			return err
		}
		mfa, err := q.LockMFA(ctx, s.StaffID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case mfa.ConfirmedAt != nil:
			return staff.ErrWrongStage
		}
		if secret, err = domain.NewTOTPSecret(a.rand); err != nil {
			return err
		}
		ct, keyID, err := a.sealTOTP(s.StaffID, secret)
		if err != nil {
			return fmt.Errorf("staff: шифрование секрета TOTP: %w", err)
		}
		email = acct.Email
		return q.UpsertPendingMFA(ctx, staffdb.UpsertPendingMFAParams{StaffID: s.StaffID, SecretEnc: ct, KeyID: keyID})
	})
	if err != nil {
		return "", "", err
	}
	return domain.Base32Secret(secret), domain.OTPAuthURI(issuer, email, secret), nil
}

// issuer — издатель в otpauth-ссылке: хост ADMIN_ORIGIN (бренд в код не вшивается).
func (a *App) issuer() (string, error) {
	u, err := url.Parse(a.origin)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("staff: ADMIN_ORIGIN %q без хоста", a.origin)
	}
	return u.Hostname(), nil
}

// MFAConfirm — второй шаг настройки (§4.5), только в стадии setup и при выпущенном, ещё не
// подтверждённом секрете (иначе staff.ErrWrongStage): код сверяется с секретом (VerifyTOTP против
// last_used_step), принятый шаг пишется в last_used_step, ставится confirmed_at, прежние резервные
// коды удаляются, выдаются 10 новых (показываются один раз). Неверный код — staff.ErrMFAInvalid без
// attempts_left: в настройке неудача в блокировку не считается (учётка ещё invited, §4.6 п.3 её не
// перечисляет), withFactor не используется. Аудит настройки — на recovery-codes/ack
// (staff.mfa_enabled); Meta — для единообразия сценариев ручек.
func (a *App) MFAConfirm(ctx context.Context, s *auth.Staff, code string, _ staff.Meta) ([]string, error) {
	var codes []string
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		_, _, now, err := a.lockSetup(ctx, q, s)
		if err != nil {
			return err
		}
		mfa, err := q.LockMFA(ctx, s.StaffID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return staff.ErrWrongStage
		case err != nil:
			return err
		case mfa.ConfirmedAt != nil:
			return staff.ErrWrongStage
		}
		secret, err := a.openTOTP(s.StaffID, mfa.SecretEnc, mfa.KeyID)
		if err != nil {
			return fmt.Errorf("staff: секрет TOTP: %w", err)
		}
		step, ok := domain.VerifyTOTP(secret, code, now, mfa.LastUsedStep)
		if !ok {
			return staff.ErrMFAInvalid
		}
		if err := q.ConfirmMFA(ctx, staffdb.ConfirmMFAParams{Now: now, Step: step, StaffID: s.StaffID}); err != nil {
			return err
		}
		codes, err = a.issueRecoveryCodes(ctx, q, s.StaffID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// issueRecoveryCodes — новое поколение резервных кодов (10 штук) под уже взятым замком учётки:
// прежние удаляются, в базу — HMAC нормализованного кода текущим ключом (recoveryMAC — тот же
// расчёт, что при входе). Коды возвращаются вызывающему один раз.
func (a *App) issueRecoveryCodes(ctx context.Context, q *staffdb.Queries, staffID uuid.UUID) ([]string, error) {
	codes, err := domain.NewRecoveryCodes(a.rand)
	if err != nil {
		return nil, err
	}
	keyID := a.keys.CurrentID()
	macs := make([][]byte, 0, len(codes))
	for _, c := range codes {
		norm, ok := domain.NormalizeRecoveryCode(c)
		if !ok {
			return nil, fmt.Errorf("staff: выданный резервный код не нормализуется")
		}
		mac, err := a.recoveryMAC(keyID, staffID, norm)
		if err != nil {
			return nil, fmt.Errorf("staff: HMAC резервного кода: %w", err)
		}
		macs = append(macs, mac)
	}
	if err := q.DeleteRecoveryCodes(ctx, staffID); err != nil {
		return nil, err
	}
	if err := q.InsertRecoveryCodes(ctx, staffdb.InsertRecoveryCodesParams{StaffID: staffID, CodeMacs: macs, KeyID: keyID}); err != nil {
		return nil, err
	}
	return codes, nil
}

// RecoveryAck — последний шаг настройки (§4.5, D9, D12): только в стадии setup с подтверждённым TOTP
// (иначе staff.ErrWrongStage). Гасит приглашение, если оно не погашено и не принято-закрыто, даже
// когда срок уже прошёл: владение кодом доказано на accept, у сессии setup свой срок, а проверка срока
// здесь оставляла бы тупик на границе 24 ч (TOTP подтверждён, «Готово» отказывает). Живого
// приглашения нет — staff.ErrWrongStage (на практике сюда не попасть: сброс доступа и выключение
// гасят сессии). Учётка — active (вход — сейчас, счётчик неудач 0), сессия — active с новым токеном через
// replaceSession (старая — replaced), mfa_at — сейчас; абсолютный срок — от принятия приглашения (как у
// unlock — от входа). Аудит staff.mfa_enabled; события §4.12 для этого шага нет. Одна транзакция —
// ровно одна закоммиченная под Idempotency-Key.
func (a *App) RecoveryAck(ctx context.Context, s *auth.Staff, m staff.Meta) (string, error) {
	var raw string
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, sess, now, err := a.lockSetup(ctx, q, s)
		if err != nil {
			return err
		}
		mfa, err := q.LockMFA(ctx, s.StaffID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return staff.ErrWrongStage
		case err != nil:
			return err
		case mfa.ConfirmedAt == nil:
			return staff.ErrWrongStage
		}
		// срок приглашения не проверяется: владение кодом доказано на accept, у сессии setup свой срок
		inv, err := q.LockLiveInvite(ctx, s.StaffID)
		if errors.Is(err, pgx.ErrNoRows) {
			return staff.ErrWrongStage
		}
		if err != nil {
			return err
		}
		if err := q.ConsumeInvite(ctx, staffdb.ConsumeInviteParams{Now: now, ID: inv.ID}); err != nil {
			return err
		}
		if err := q.ActivateAccount(ctx, staffdb.ActivateAccountParams{Now: now, ID: s.StaffID}); err != nil {
			return err
		}
		if raw, err = a.replaceSession(ctx, q, s.SessionID, newSession{
			staffID: s.StaffID, stage: auth.StageActive, created: sess.CreatedAt, expires: sess.ExpiresAt, mfaAt: &now,
		}, now, m); err != nil {
			return err
		}
		return audit.Write(ctx, selfEntry(acct, actionMFAEnabled, nil, map[string]any{"purpose": inv.Purpose}, m))
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// RegenerateRecovery — новые резервные коды (§4.7, M9): подтверждение только кодом TOTP (резервный
// код не принимается — он и есть неверный код), только в активной сессии (Reauth отсекает конвейер:
// x-permission self). Проверка — через withFactor (шаг recovery_regenerate): сессия погашена или
// учётка закрыта — auth.ErrSessionInvalid без записи неудачи, заменена — auth.ErrSessionReplaced;
// второй фактор не настроен — staff.ErrWrongStage; неверный код — неудача второго фактора
// (*staff.MFAError, пятая подряд закрывает учётку). Успех: шаг TOTP сгорает, счётчик неудач 0, 10 новых
// кодов вместо всех прежних, аудит staff.recovery_regenerated.
func (a *App) RegenerateRecovery(ctx context.Context, s *auth.Staff, code string, m staff.Meta) ([]string, error) {
	if s.Stage != auth.StageActive {
		return nil, staff.ErrWrongStage
	}
	var codes []string
	err := a.withFactor(ctx, s, stepRecoveryRegenerate, m, func(ctx context.Context, _ pgx.Tx, q *staffdb.Queries, acct staffdb.LockAccountRow, now time.Time) (error, error) {
		sess, err := lockLiveSession(ctx, q, s, acct, now)
		if err != nil {
			return nil, err
		}
		if sess.Stage != auth.StageActive {
			return nil, staff.ErrWrongStage
		}
		mfa, err := q.LockMFA(ctx, s.StaffID)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && mfa.ConfirmedAt == nil {
			return nil, staff.ErrWrongStage
		}
		if err != nil {
			return nil, err
		}
		ok, err := a.checkFactor(ctx, q, s.StaffID, code, "", now)
		if err != nil {
			return nil, err
		}
		if !ok {
			return staff.ErrMFAInvalid, nil
		}
		if err := q.FactorSucceeded(ctx, staffdb.FactorSucceededParams{ID: s.StaffID}); err != nil {
			return nil, err
		}
		if codes, err = a.issueRecoveryCodes(ctx, q, s.StaffID); err != nil {
			return nil, err
		}
		return nil, audit.Write(ctx, selfEntry(acct, actionRecoveryRegenerated, nil, map[string]any{"codes_count": len(codes)}, m))
	})
	// токен сессии только что заменён (unlock в другой вкладке): 401 без стирания новой cookie
	if err := replacedAsSession(err); err != nil {
		return nil, err
	}
	return codes, nil
}

// MFAStatus — состояние второго фактора («Мой доступ», GET /me/mfa): когда подтверждён (nil — не
// настроен) и сколько резервных кодов не использовано. Секрет и коды не возвращаются.
func (a *App) MFAStatus(ctx context.Context, s *auth.Staff) (staff.MFAView, error) {
	row, err := staffdb.New(a.pool).GetMFAStatus(ctx, s.StaffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return staff.MFAView{}, auth.ErrSessionInvalid
	}
	if err != nil {
		return staff.MFAView{}, err
	}
	return staff.MFAView{ConfirmedAt: row.ConfirmedAt, RecoveryCodesLeft: int(row.CodesLeft)}, nil
}
