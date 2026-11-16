package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/emailaddr"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
)

// Сброс и смена пароля (спека §3.4, §3.10) — владелец Task 13. Свежий вход — reauth_op.go, смена почты —
// email_change.go.

// ResetInput — confirmPasswordReset.
type ResetInput struct {
	CodeID            uuid.UUID
	Code, NewPassword string
	MFA               *SecondFactor
}

// Лимит неудач второго фактора на аккаунт (R16, §6.4: user:mfa:<id> — 10 неудач в час), общий с completeMfa.
var mfaFailurePolicy = ratelimit.Policy{Limit: 10, Period: time.Hour}

const mfaFailureClass = "mfa"

var (
	errResetMfaRequired = httpx.NewError(http.StatusBadRequest, identity.CodeMfaRequired).WithMethods(FactorTOTP, FactorRecovery)
	errResetMfaInvalid  = httpx.NewError(http.StatusBadRequest, identity.CodeMfaInvalid)
)

// mfaFailureKey — ключ лимита неудач второго фактора аккаунта.
func mfaFailureKey(userID uuid.UUID) string {
	return ratelimit.SubjectKey(mfaFailureClass, auth.Subject{Kind: auth.SubjectUser, ID: userID})
}

// resetRiskKeys — ключи риска risk.LoginFailed сброса: те же, что у входа (почта «login ‖ почта», IP класса login).
func (a *App) resetRiskKeys(email string) ([]string, error) {
	mac, err := a.secrets.MAC(a.secrets.CurrentID(), domain.LabelRateLimit, []byte("login"+email))
	if err != nil {
		return nil, err
	}
	keys := []string{ratelimit.EmailKey("login", mac)}
	return keys, nil
}

// StartPasswordReset — код сброса (§3.4 п. 1): форма почты (400) → PoW по риску LoginFailed (почта и IP запроса) →
// лимит по почте → аккаунт active | suspended | pending_deletion до purge_at — настоящий код с user_id; нет, deleted,
// banned (R29) или pending_deletion с purge_at ≤ now (purgeDue) — приманка без user_id. Работа обеих веток одинакова (m12): строка auth_codes и задача письма
// password_reset_code (Composer на приманке отвечает mail.ErrSkip, Task 19). Ответ одинаков.
func (a *App) StartPasswordReset(ctx context.Context, email string) (CodeSent, error) {
	norm, ok := emailaddr.Normalize(email)
	if !ok {
		return CodeSent{}, errSignupEmailShape
	}
	ip := peer.From(ctx).IP
	keys, err := a.resetRiskKeys(norm)
	if err != nil {
		return CodeSent{}, err
	}
	if ip.IsValid() {
		keys = append(keys, ratelimit.IPKey("login", ip))
	}
	susp, err := a.risk.Suspicious(ctx, risk.LoginFailed, keys...)
	if err != nil {
		return CodeSent{}, err
	}
	if susp {
		if err := humancheck.Require(ctx, a.human); err != nil {
			return CodeSent{}, err
		}
	}
	if err := a.codeSendAllowed(ctx, a.limiter, identitydb.AuthCodePurposePasswordReset, norm); err != nil {
		return CodeSent{}, err
	}
	var sent CodeSent
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		u, err := identitydb.New(tx).LoginFindUser(ctx, norm)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("identity: аккаунт по почте: %w", err)
		}
		p := StartCode{Purpose: identitydb.AuthCodePurposePasswordReset, Email: norm, Decoy: true, Client: Client{IP: ip}}
		// pending_deletion с вышедшим сроком отмены (purgeDue) — как удалённый: приманка (добор F п. 4)
		if err == nil && !a.purgeDue(u.Status, u.PurgeAt) {
			switch u.Status {
			case identitydb.UserStatusActive, identitydb.UserStatusSuspended, identitydb.UserStatusPendingDeletion:
				p.Decoy, p.UserID = false, u.ID
			}
		}
		if sent, err = a.startCode(ctx, tx, p); err != nil {
			return err
		}
		return a.enqueueMail(ctx, tx, MailPasswordResetCode, sent.CodeID)
	})
	return sent, err
}

// resetBlocked — аккаунт не может сменить пароль сбросом кодом row (R29: banned; deleted — анонимизирован) или код
// выдан на почту, которая больше не почта аккаунта (I2: смена почты; защита в глубину к RevokeUserCodes — код,
// вставленный стартом сброса, прочитавшим аккаунт до коммита смены).
func resetBlocked(u userRow, row codeRow) bool {
	return u.Status == identitydb.UserStatusBanned || u.Status == identitydb.UserStatusDeleted || u.Email != row.Email
}

// ConfirmPasswordReset — новый пароль по коду (§3.4 п. 2; C1, N4, N10, R29, R31). До tx — устройство и
// password.Check (ответ одинаков для любого аккаунта и приманки, резервный код не сгорает на слабом пароле).
// tx1: сверка MAC кода первой (checkCode CodeVerifyOnly, неудача коммитится — одинаково для приманки, аккаунта с 2FA и
// без); только при верном коде — бан или почта аккаунта ≠ адресу кода (410 без записей), затем 2FA: нет mfa — 400
// mfa_required (код жив, попытка не считается); mfa — лимит неудач user:mfa (на соединении tx — a.limiterTx) и
// verifySecondFactor (неудача и её учёт в user:mfa коммитятся в tx1, код жив; успех — шаг TOTP или сгоревший
// резервный код — коммитится с tx1). После tx1 фактор может быть сожжён — Hash и tx2 идут на detached: обрыв запроса
// смену не прерывает (m3). tx2: lockUser → FOR UPDATE кода и перепроверка (тот же аккаунт, не закрыт, не banned, та
// же почта; иначе 410 без записей), погашение, пароль, отзыв всех сессий (password_reset), журнал, письмо
// password_changed, событие, аудит, finishLogin (сессия или deletion_pending) и флаг документов (в tx2 — после
// коммита база не читается).
//
// Retry-After у 429 лимита user:mfa — интервал политики (час / 10 = 6 мин; m10): Limiter.Over отдаёт только «да/нет»,
// а при Over у GCRA следующий проход наступает не позже одного интервала (tat − now ≤ окно: неудачи сверх Over не
// учитываются) — оценка сверху, без расширения интерфейса платформы.
func (a *App) ConfirmPasswordReset(ctx context.Context, in ResetInput, c Client) (AuthOutcome, error) {
	if err := a.requireDevice(c); err != nil {
		return AuthOutcome{}, err
	}
	if err := password.Check(in.NewPassword); err != nil {
		return AuthOutcome{}, weakPassword(err, "body.new_password")
	}
	if in.MFA != nil && (in.MFA.TOTP == "") == (in.MFA.Recovery == "") {
		return AuthOutcome{}, errFactorShape
	}
	var userID uuid.UUID
	var mfaDone bool
	err := a.inTxCommitFailure(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row, err := a.checkCode(ctx, tx, identitydb.AuthCodePurposePasswordReset, in.CodeID, in.Code, CodeVerifyOnly)
		if err != nil {
			return err
		}
		if row.UserID == nil {
			return errCodeExpired // настоящий код сброса всегда с аккаунтом; защита в глубину
		}
		u, err := a.lockUser(ctx, tx, *row.UserID)
		if errors.Is(err, errUserGone) {
			return errCodeExpired
		}
		if err != nil {
			return err
		}
		if resetBlocked(u, row) {
			return errCodeExpired
		}
		userID = u.ID
		on, err := identitydb.New(tx).MfaConfirmed(ctx, u.ID)
		if err != nil {
			return err
		}
		if !on {
			return nil
		}
		if in.MFA == nil {
			return errResetMfaRequired
		}
		// лимит — на соединении tx (C1): второе соединение пула под замком users при исчерпанном пуле ждало бы вечно
		limiter := a.limiterTx(tx)
		over, err := limiter.Over(ctx, mfaFailureKey(u.ID), mfaFailurePolicy)
		if err != nil {
			return fmt.Errorf("identity: лимит второго фактора: %w", err)
		}
		if over {
			return errRateLimited.WithRetryAfter(mfaFailurePolicy.Period / time.Duration(mfaFailurePolicy.Limit))
		}
		if _, err := a.verifySecondFactor(ctx, tx, u.ID, *in.MFA); errors.Is(err, errFactorInvalid) {
			// учёт неудачи — в той же tx1 (m2): коммитится вместе с ней и при оборванном запросе
			if err := limiter.Add(ctx, mfaFailureKey(u.ID), mfaFailurePolicy); err != nil {
				return fmt.Errorf("identity: учёт неудачи второго фактора: %w", err)
			}
			return failureToCommit{errResetMfaInvalid}
		} else if err != nil {
			return err
		}
		mfaDone = true
		return nil
	})
	if err != nil {
		return AuthOutcome{}, err
	}
	// tx1 закоммитила шаг TOTP или резервный код — смену доводим и при оборванном запросе (m3)
	dctx, cancel := detached(ctx)
	defer cancel()
	// argon2 — вне транзакции и без соединения пула (R31)
	hash, err := a.hasher.Hash(dctx, in.NewPassword)
	if err != nil {
		return AuthOutcome{}, err
	}
	var out AuthOutcome
	err = db.InTx(dctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		u, err := a.lockUser(ctx, tx, userID)
		if errors.Is(err, errUserGone) {
			return errCodeExpired
		}
		if err != nil {
			return err
		}
		row, err := a.lockCode(ctx, tx, identitydb.AuthCodePurposePasswordReset, in.CodeID)
		if err != nil {
			return err
		}
		if a.codeClosed(row) || row.UserID == nil || *row.UserID != userID || resetBlocked(u, row) {
			return errCodeExpired
		}
		if err := a.consumeCode(ctx, tx, row.ID); err != nil {
			return err
		}
		if err := a.setPassword(ctx, tx, u.ID, hash, uuid.Nil, domain.RevokedPasswordReset, domain.SecPasswordReset,
			ActionPasswordReset, c); err != nil {
			return err
		}
		dev, err := a.upsertDevice(ctx, tx, c)
		if err != nil {
			return err
		}
		out = AuthOutcome{}
		out.LoginOutcome, err = a.finishLogin(ctx, tx, u, c, dev, domain.MethodPassword, mfaDone, true)
		if errors.Is(err, errBlockedAsExpired) || errors.Is(err, errAccountDeleted) {
			return errCodeExpired
		}
		if err != nil {
			return err
		}
		if out.Status == StatusAuthenticated {
			out.DocumentsRequired, err = a.documentsRequired(ctx, identitydb.New(tx), u.ID)
		}
		return err
	})
	if err != nil {
		return AuthOutcome{}, err
	}
	return out, nil
}

// setPassword — новый хэш (users уже под замком): credentials upsert, отзыв сессий кроме keep (uuid.Nil — всех) и
// всех живых токенов шагов аккаунта, журнал безопасности, письмо password_changed, событие identity.password_changed (версия + 1), аудит action.
func (a *App) setPassword(ctx context.Context, tx pgx.Tx, userID uuid.UUID, hash string, keep uuid.UUID,
	reason domain.RevokedReason, kind domain.SecurityKind, action string, c Client) error {
	q := identitydb.New(tx)
	if err := q.UpsertPassword(ctx, identitydb.UpsertPasswordParams{UserID: userID, PasswordHash: hash,
		Now: a.clock.Now()}); err != nil {
		return fmt.Errorf("identity: пароль: %w", err)
	}
	revoked, err := a.revokeSessions(ctx, tx, userID, keep, reason)
	if err != nil {
		return err
	}
	// недоведённые шаги входа старым паролем (mfa_token, restore_token) — гаснут вместе с сессиями; у сброса — раньше
	// finishLogin, который может выдать новый restore_token
	if err := a.revokeChallenges(ctx, tx, userID); err != nil {
		return err
	}
	ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: userID, Kind: kind, SessionID: keep, IP: c.IP,
		UserAgent: c.UserAgent, Meta: map[string]any{"sessions_revoked_count": len(revoked)}})
	if err != nil {
		return err
	}
	if err := a.enqueueMail(ctx, tx, MailPasswordChanged, ref); err != nil {
		return err
	}
	ver, err := q.BumpUserVersion(ctx, userID)
	if err != nil {
		return fmt.Errorf("identity: версия аккаунта: %w", err)
	}
	if _, err := events.Publish(ctx, identity.PasswordChanged(identity.UserRef{ID: userID, Version: ver})); err != nil {
		return err
	}
	return a.auditUser(ctx, action, userID, nil, map[string]any{"sessions_revoked_count": len(revoked)})
}

// ChangePassword — смена или установка пароля при свежем входе (§3.10): password.Check и Hash до tx (N10, R31; неудача
// свежести тратит один argon2 — под лимитом account_secure); в tx — requireFresh (users первым), пароль, отзыв других
// сессий (password_changed), письмо, событие, аудит. IP и UA журнала — из текущего запроса c (m5), не из строки сессии.
func (a *App) ChangePassword(ctx context.Context, sessionID uuid.UUID, pw string, c Client) error {
	if err := password.Check(pw); err != nil {
		return weakPassword(err, "body.new_password")
	}
	hash, err := a.hasher.Hash(ctx, pw)
	if err != nil {
		return err
	}
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		s, err := a.requireFresh(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		return a.setPassword(ctx, tx, s.UserID, hash, s.ID, domain.RevokedPasswordChanged, domain.SecPasswordChanged,
			ActionPasswordChanged, c)
	})
}
