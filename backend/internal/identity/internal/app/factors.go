package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/otp"
)

// SecondFactor — второй фактор: ровно одно поле.
type SecondFactor struct {
	TOTP, Recovery string
}

// Способы второго фактора (methods шага 2FA и результат verifySecondFactor).
const (
	FactorTOTP     = "totp"
	FactorRecovery = "recovery_code"
	FactorPasskey  = "passkey"
)

// mfaFailuresNotify — неудач второго фактора подряд до письма mfa_failures; письмо — не чаще mfaFailuresNotifyEvery
// (§3.3 п. 3). Аккаунт неудачи не закрывают.
const (
	mfaFailuresNotify      = 5
	mfaFailuresNotifyEvery = 24 * time.Hour
)

// errFactorInvalid — фактор не принят; код ошибки ставит вызывающий (mfa_invalid, reauth_invalid).
var errFactorInvalid = errors.New("identity: второй фактор не принят")

// errFactorKeyGone — ключ секрета TOTP убран из кольца раньше rekey: фактор не проверить по вине сервера —
// отказ без засчитанной неудачи и без письма mfa_failures, Warn в журнал (m6 ревью W3).
var errFactorKeyGone = errors.New("identity: ключ секрета TOTP вне кольца")

// errFactorShape — ни одного или оба поля (R11 плана I-1: одна схема, правило «ровно одно» — в сценарии).
var errFactorShape = httpx.NewFieldError("body", "oneOf")

// verifySecondFactor — второй фактор аккаунта (users уже под замком вызывающего; здесь — строка user_mfa FOR UPDATE,
// R30). TOTP — otp.Verify со сдвигом last_used_step (повтор того же или старшего шага отвергается); резервный код —
// HMAC ключом строки, гасится в tx (used_at), письмо recovery_code_used с остатком. Неудача — failures + 1, пятая
// подряд — security_events (mfa_failures) и письмо не чаще раза в сутки; ответ errFactorInvalid.
//
// Неудача (счётчик, журнал, письмо) обязана сохраниться: вызывающий коммитит tx с неудачей и возвращает ошибку
// ПОСЛЕ коммита (образец — withFactor staff; страж TestFactorFailurePersists). 2FA не включена — errFactorInvalid.
func (a *App) verifySecondFactor(ctx context.Context, tx pgx.Tx, userID uuid.UUID, f SecondFactor) (method string, err error) {
	if (f.TOTP == "") == (f.Recovery == "") {
		return "", errFactorShape
	}
	q := identitydb.New(tx)
	m, err := q.LockUserMfa(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errFactorInvalid
	}
	if err != nil {
		return "", fmt.Errorf("identity: 2FA: %w", err)
	}
	if m.ConfirmedAt == nil {
		return "", errFactorInvalid
	}
	var ok bool
	if f.TOTP != "" {
		method = FactorTOTP
		ok, err = a.acceptTOTP(ctx, q, m, f.TOTP)
		if errors.Is(err, errFactorKeyGone) {
			a.log.WarnContext(ctx, "identity: ключ секрета TOTP вне кольца — второй фактор не проверить", "key_id", m.KeyID)
			return "", errFactorInvalid
		}
	} else {
		method = FactorRecovery
		ok, err = a.acceptRecovery(ctx, tx, q, userID, f.Recovery)
	}
	if err != nil {
		return "", err
	}
	if !ok {
		return "", a.factorFailed(ctx, tx, q, userID)
	}
	return method, nil
}

// acceptTOTP — код TOTP шага новее last_used_step; принятый шаг записывается, счётчик неудач обнуляется.
func (a *App) acceptTOTP(ctx context.Context, q *identitydb.Queries, m identitydb.UserMfa, code string) (bool, error) {
	if !a.secrets.Has(m.KeyID) {
		return false, errFactorKeyGone // ключ убран из кольца раньше rekey — фактор не проверить, не 500
	}
	secret, err := a.secrets.Open(m.SecretEnc, m.KeyID, m.UserID[:])
	if err != nil {
		return false, fmt.Errorf("identity: секрет TOTP: %w", err)
	}
	last := int64(-1)
	if m.LastUsedStep != nil {
		last = *m.LastUsedStep
	}
	step, ok := otp.Verify(secret, code, a.clock.Now(), last)
	if !ok {
		return false, nil
	}
	return true, q.MfaAccepted(ctx, identitydb.MfaAcceptedParams{Step: &step, UserID: m.UserID})
}

// acceptRecovery — резервный код гасится (used_at) по HMAC ключом каждой живой строки; ключ вне кольца — пропуск.
func (a *App) acceptRecovery(ctx context.Context, tx pgx.Tx, q *identitydb.Queries, userID uuid.UUID, code string) (bool, error) {
	norm, ok := otp.NormalizeRecoveryCode(code)
	if !ok {
		return false, nil
	}
	keyIDs, err := q.LockRecoveryKeys(ctx, userID)
	if err != nil {
		return false, err
	}
	now := a.clock.Now()
	for _, k := range keyIDs {
		if !a.secrets.Has(k) {
			continue
		}
		mac, err := a.recoveryMAC(k, userID, norm)
		if err != nil {
			return false, err
		}
		n, err := q.BurnRecoveryCode(ctx, identitydb.BurnRecoveryCodeParams{Now: &now, UserID: userID, CodeMac: mac, KeyID: k})
		if err != nil {
			return false, err
		}
		if n == 0 {
			continue
		}
		if err := q.MfaAccepted(ctx, identitydb.MfaAcceptedParams{UserID: userID}); err != nil {
			return false, err
		}
		left, err := q.RecoveryCodesLeft(ctx, userID)
		if err != nil {
			return false, err
		}
		ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: userID, Kind: domain.SecRecoveryCodeUsed,
			Meta: map[string]any{"left": left}})
		if err != nil {
			return false, err
		}
		return true, a.enqueueMail(ctx, tx, MailRecoveryCodeUsed, ref)
	}
	return false, nil
}

// factorFailed — неудача второго фактора: счётчик подряд, на пятой — журнал и письмо не чаще раза в сутки.
func (a *App) factorFailed(ctx context.Context, tx pgx.Tx, q *identitydb.Queries, userID uuid.UUID) error {
	row, err := q.MfaFailed(ctx, userID)
	if err != nil {
		return err
	}
	now := a.clock.Now()
	if row.Failures >= mfaFailuresNotify &&
		(row.FailuresNotifiedAt == nil || now.Sub(*row.FailuresNotifiedAt) >= mfaFailuresNotifyEvery) {
		ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: userID, Kind: domain.SecMfaFailures,
			Meta: map[string]any{"failures": row.Failures}})
		if err != nil {
			return err
		}
		if err := a.enqueueMail(ctx, tx, MailMfaFailures, ref); err != nil {
			return err
		}
		if err := q.MfaFailuresNotified(ctx, identitydb.MfaFailuresNotifiedParams{Now: &now, UserID: userID}); err != nil {
			return err
		}
	}
	return errFactorInvalid
}

// recoveryMAC — HMAC резервного кода: ключ key_id строки, метка identity-recovery, сообщение — id аккаунта ‖
// нормализованный код (otp.NormalizeRecoveryCode). Одна функция для выдачи (Task 16) и проверки.
func (a *App) recoveryMAC(keyID string, userID uuid.UUID, norm string) ([]byte, error) {
	msg := make([]byte, 0, len(userID)+len(norm))
	msg = append(msg, userID[:]...)
	msg = append(msg, norm...)
	return a.secrets.MAC(keyID, domain.LabelRecovery, msg)
}
