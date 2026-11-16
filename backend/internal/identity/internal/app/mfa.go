package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/otp"
)

// 2FA игрока: включение и управление (спека §3.3) — владелец Task 16; второй шаг входа — mfa_login.go.
// Секрет TOTP — keys.Ring.Seal (aad = id аккаунта), резервные коды — HMAC (recoveryMAC); ни секрет, ни коды не
// попадают в журнал, события, аудит и ответы идемпотентности (x-idempotency: no-store у setup, confirm, regenerate).
// Каждая мутация — одна db.InTx: users первым (requireFresh или lockUser), затем строки user_mfa и кодов (R30).

// MfaStatusView — getMyMfa.
type MfaStatusView struct {
	TOTPEnabled       bool
	ConfirmedAt       *time.Time
	RecoveryCodesLeft int
	ResetScheduledAt  *time.Time
}

var (
	errMfaAlreadyEnabled = httpx.NewError(http.StatusConflict, identity.CodeMfaAlreadyEnabled)
	errMfaNotEnabled     = httpx.NewError(http.StatusConflict, identity.CodeMfaNotEnabled)
	errMfaSetupMissing   = httpx.NewError(http.StatusConflict, identity.CodeMfaSetupMissing)
	errMfaInvalid        = httpx.NewError(http.StatusBadRequest, identity.CodeMfaInvalid)
)

// MfaStatus — состояние 2FA одним чтением пулом: начатая, но не подтверждённая настройка — «выключена» (кодов 0).
func (a *App) MfaStatus(ctx context.Context, userID uuid.UUID) (MfaStatusView, error) {
	row, err := identitydb.New(a.pool).MfaStatus(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MfaStatusView{}, errSessionGone
	}
	if err != nil {
		return MfaStatusView{}, fmt.Errorf("identity: состояние 2FA: %w", err)
	}
	v := MfaStatusView{ConfirmedAt: row.ConfirmedAt, ResetScheduledAt: row.ResetAt}
	if row.ConfirmedAt != nil {
		v.TOTPEnabled, v.RecoveryCodesLeft = true, int(row.CodesLeft)
	}
	return v, nil
}

// SetupTotp — новый секрет при свежем входе (requireFresh: users, затем sessions); подтверждённая 2FA — 409
// mfa_already_enabled; повтор до подтверждения выпускает новый секрет (прежний перестаёт подходить); живой сброс 2FA
// поддержкой отменяется владельцем (cancelResetsByOwner). Ответ —
// секрет в base32 и otpauth URI (издатель — API_IDENTITY_TOTP_ISSUER, аккаунт — ник).
func (a *App) SetupTotp(ctx context.Context, sessionID uuid.UUID) (secret, uri string, err error) {
	raw, err := otp.NewSecret(rand.Reader)
	if err != nil {
		return "", "", err
	}
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		s, err := a.requireFresh(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		u, err := a.lockUser(ctx, tx, s.UserID) // уже под замком requireFresh — без ожидания, нужен ник
		if err != nil {
			return err
		}
		enc, keyID, err := a.secrets.Seal(raw, u.ID[:])
		if err != nil {
			return fmt.Errorf("identity: секрет TOTP: %w", err)
		}
		n, err := identitydb.New(tx).SetupMfa(ctx, identitydb.SetupMfaParams{UserID: u.ID, SecretEnc: enc, KeyID: keyID,
			Now: a.clock.Now()})
		if err != nil {
			return fmt.Errorf("identity: настройка 2FA: %w", err)
		}
		if n == 0 {
			return errMfaAlreadyEnabled
		}
		// живой сброс у аккаунта без подтверждённой 2FA (гонка одобрения с DisableTotp, m1 ревью 3) отменяет владелец:
		// иначе воркер в срок стёр бы новую 2FA (R30: mfa_resets после user_mfa)
		if _, err := a.cancelResetsByOwner(ctx, tx, u.ID); err != nil {
			return err
		}
		uri = otp.URI(a.totpIssuer, u.Nickname, raw)
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return otp.Base32(raw), uri, nil
}

// ConfirmTotp — включение 2FA кодом от начатой настройки: сессия запроса мертва под замком аккаунта (requireCurrent) —
// 401 auth.unauthenticated; нет неподтверждённого секрета (не начата или уже
// включена) — 409 mfa_setup_missing; неверный код — 400 mfa_invalid (без записей); верный — confirmed_at и
// last_used_step (этот код больше не примут), 10 резервных кодов (прежние строки удаляются), журнал, письмо
// mfa_enabled, событие mfa_changed {enabled: true}, аудит; живой сброс 2FA поддержкой отменяется владельцем. Другие
// сессии не гасятся (§3.3 п. 3).
func (a *App) ConfirmTotp(ctx context.Context, userID, sessionID uuid.UUID, code string) ([]string, error) {
	var codes []string
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		// users первым, затем сессия запроса под замком (m1 ревью W5-А): кэш загрузчика ≤ 5 с пропускает отозванную
		if err := a.requireCurrent(ctx, tx, userID, sessionID); err != nil {
			return err
		}
		q := identitydb.New(tx)
		m, err := q.LockUserMfa(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errMfaSetupMissing
		}
		if err != nil {
			return fmt.Errorf("identity: 2FA: %w", err)
		}
		if m.ConfirmedAt != nil {
			return errMfaSetupMissing
		}
		if !a.secrets.Has(m.KeyID) {
			// ключ убран из кольца раньше подтверждения — настройку начать заново
			return errMfaSetupMissing
		}
		secret, err := a.secrets.Open(m.SecretEnc, m.KeyID, userID[:])
		if err != nil {
			return fmt.Errorf("identity: секрет TOTP: %w", err)
		}
		step, ok := otp.Verify(secret, code, a.clock.Now(), -1)
		if !ok {
			return errMfaInvalid
		}
		now := a.clock.Now()
		if n, err := q.ConfirmMfa(ctx, identitydb.ConfirmMfaParams{Now: &now, Step: &step, UserID: userID}); err != nil {
			return fmt.Errorf("identity: включение 2FA: %w", err)
		} else if n != 1 {
			return errMfaSetupMissing
		}
		if codes, err = a.replaceRecoveryCodes(ctx, q, userID); err != nil {
			return err
		}
		// сброс, назначенный между setup и confirm, не должен снять включаемую 2FA (m1 ревью 3)
		if _, err := a.cancelResetsByOwner(ctx, tx, userID); err != nil {
			return err
		}
		ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: userID, Kind: domain.SecMfaEnabled, SessionID: sessionID})
		if err != nil {
			return err
		}
		if err := a.enqueueMail(ctx, tx, MailMfaEnabled, ref); err != nil {
			return err
		}
		return a.mfaChanged(ctx, q, userID, true, ActionMfaEnabled)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTotp — выключение 2FA: requireFresh; 2FA не включена — 409 mfa_not_enabled (раньше проверки второго фактора:
// без 2FA подтвердить свежесть вторым фактором нечем — иначе тупик 403); сессия не подтвердила свежесть вторым
// фактором (sessions.mfa_at старше domain.ReauthFresh, R27) — 403 reauth_required; иначе живой сброс 2FA поддержкой
// отменяется владельцем, секрет и коды удаляются, журнал, письмо mfa_disabled, событие mfa_changed {enabled: false}, аудит.
func (a *App) DisableTotp(ctx context.Context, sessionID uuid.UUID) error {
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		s, err := a.requireFresh(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		q := identitydb.New(tx)
		if err := a.requireMfaOn(ctx, q, s.UserID); err != nil {
			return err
		}
		// живой сброс 2FA поддержкой отменяется владельцем (I1 ревью W5-Б): иначе воркер в срок стёр бы 2FA, включённую
		// заново после выключения; 403 ниже откатывает и отмену
		if _, err := a.cancelResetsByOwner(ctx, tx, s.UserID); err != nil {
			return err
		}
		if s.MfaAt == nil || a.clock.Now().Sub(*s.MfaAt) > domain.ReauthFresh {
			return errReauthRequired
		}
		if _, err := q.DeleteMfa(ctx, s.UserID); err != nil {
			return fmt.Errorf("identity: выключение 2FA: %w", err)
		}
		if err := q.DeleteRecoveryCodes(ctx, s.UserID); err != nil {
			return fmt.Errorf("identity: резервные коды: %w", err)
		}
		ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: s.UserID, Kind: domain.SecMfaDisabled, SessionID: s.ID})
		if err != nil {
			return err
		}
		if err := a.enqueueMail(ctx, tx, MailMfaDisabled, ref); err != nil {
			return err
		}
		return a.mfaChanged(ctx, q, s.UserID, false, ActionMfaDisabled)
	})
}

// RegenerateRecoveryCodes — новые 10 кодов при свежем входе: 2FA не включена — 409 mfa_not_enabled; все прежние
// строки (и использованные) удаляются; журнал и письмо recovery_codes_regenerated.
func (a *App) RegenerateRecoveryCodes(ctx context.Context, sessionID uuid.UUID) ([]string, error) {
	var codes []string
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		s, err := a.requireFresh(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		q := identitydb.New(tx)
		if err := a.requireMfaOn(ctx, q, s.UserID); err != nil {
			return err
		}
		if codes, err = a.replaceRecoveryCodes(ctx, q, s.UserID); err != nil {
			return err
		}
		ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: s.UserID, Kind: domain.SecRecoveryCodesRegenerated,
			SessionID: s.ID})
		if err != nil {
			return err
		}
		return a.enqueueMail(ctx, tx, MailRecoveryCodesRegenerated, ref)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// requireMfaOn — подтверждённая 2FA под замком строки user_mfa (R30: после users и sessions), иначе 409 mfa_not_enabled.
func (a *App) requireMfaOn(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) error {
	m, err := q.LockUserMfa(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errMfaNotEnabled
	}
	if err != nil {
		return fmt.Errorf("identity: 2FA: %w", err)
	}
	if m.ConfirmedAt == nil {
		return errMfaNotEnabled
	}
	return nil
}

// replaceRecoveryCodes — 10 новых резервных кодов (otp.NewRecoveryCodes) вместо всех прежних строк аккаунта; в базе —
// HMAC текущим ключом кольца (recoveryMAC по нормализованному коду — та же форма, что при проверке).
func (a *App) replaceRecoveryCodes(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) ([]string, error) {
	codes, err := otp.NewRecoveryCodes(rand.Reader)
	if err != nil {
		return nil, err
	}
	keyID := a.secrets.CurrentID()
	macs := make([][]byte, 0, len(codes))
	for _, c := range codes {
		norm, ok := otp.NormalizeRecoveryCode(c)
		if !ok {
			return nil, fmt.Errorf("identity: резервный код не в форме otp")
		}
		mac, err := a.recoveryMAC(keyID, userID, norm)
		if err != nil {
			return nil, err
		}
		macs = append(macs, mac)
	}
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return nil, fmt.Errorf("identity: резервные коды: %w", err)
	}
	if err := q.InsertRecoveryCodes(ctx, identitydb.InsertRecoveryCodesParams{UserID: userID, KeyID: keyID,
		Now: a.clock.Now(), Macs: macs}); err != nil {
		return nil, fmt.Errorf("identity: резервные коды: %w", err)
	}
	return codes, nil
}

// mfaChanged — событие identity.mfa_changed (версия аккаунта + 1) и аудит action без секретов.
func (a *App) mfaChanged(ctx context.Context, q *identitydb.Queries, userID uuid.UUID, enabled bool, action string) error {
	ver, err := q.BumpUserVersion(ctx, userID)
	if err != nil {
		return fmt.Errorf("identity: версия аккаунта: %w", err)
	}
	if _, err := events.Publish(ctx, identity.MfaChanged(identity.UserRef{ID: userID, Version: ver}, enabled)); err != nil {
		return err
	}
	return a.auditUser(ctx, action, userID, nil, map[string]any{"mfa_enabled": enabled})
}

// PasskeyInput — утверждение WebAuthn второго шага (I-3; в I-2 — passkey_invalid, R3).
type PasskeyInput struct {
	Credential []byte
}
