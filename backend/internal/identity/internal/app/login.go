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
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/emailaddr"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
)

// Вход по почте и паролю (спека §3.2, §9.1, §9.2) — Task 11.

// classLogin — класс ключа лимита входа по почте (ratelimit.EmailKey, R16) и назначение в сообщении HMAC (R15).
const classLogin = "login"

// loginEmailPolicy — попыток входа на почту в час, одинаково для несуществующих адресов (§3.2 п. 2).
var loginEmailPolicy = ratelimit.Policy{Limit: 30, Period: time.Hour}

var (
	errInvalidCredentials = httpx.NewError(http.StatusBadRequest, identity.CodeInvalidCredentials)
	// errEmailShape — почта не проходит emailaddr.Normalize: 400 по полю до поиска аккаунта (контракт login)
	errEmailShape = httpx.NewFieldError("body.email", "format")
	// errLoginStale — пароль сменили между проверкой argon2 и замком: ответ как на неверный пароль (R6)
	errLoginStale = errors.New("identity: пароль сменили во время входа")
)

// LoginResult — итог входа: LoginOutcome и documents_acceptance_required (у authenticated; считается в транзакции
// входа — ответ после коммита базу не читает, m1 ревью W4-Б).
type LoginResult struct {
	LoginOutcome
	DocumentsRequired bool
}

// Login — вход по почте и паролю. Порядок: устройство мобильного (422) → форма почты (400 по полю) → лимит почты
// 30/ч (429, ключ — HMAC «login ‖ почта», одинаков для несуществующих) → риск risk.LoginFailed по почте или IP —
// humancheck.Require (403 с задачей) → аккаунт пулом без замка → пароль вне транзакции (checkPassword; «нет
// аккаунта» и deleted — dummyPassword, та же работа argon2). Неудача — risk.Record по почте и IP, у существующего
// аккаунта — security_events (login_failed) и аудит anonymous без object_id (коммитятся до ответа), ответ — один
// 400 identity.invalid_credentials. Успех — транзакция: lockUser (R30), хэш тот же, что проверен (иначе — неудача),
// устройство, finishLogin (banned → 403, 2FA → mfa_required, pending_deletion → deletion_pending, иначе сессия и
// documents_acceptance_required по согласиям в той же tx).
//
// Сбой хранилища лимитов (лимит почты и риск) — ошибка (500) до поиска аккаунта и argon2: одна политика с кодами и
// сбросом пароля (m5 ревью W4-Б). Пропуск открыл бы перебор паролей без счётчиков, а лимиты живут в той же базе, что
// и аккаунт: при их сбое вход всё равно не завершится.
func (a *App) Login(ctx context.Context, email, pw string, c Client) (LoginResult, error) {
	if err := a.requireDevice(c); err != nil {
		return LoginResult{}, err
	}
	norm, ok := emailaddr.Normalize(email)
	if !ok {
		return LoginResult{}, errEmailShape
	}
	mac, err := a.secrets.MAC(a.secrets.CurrentID(), domain.LabelRateLimit, []byte(classLogin+norm))
	if err != nil {
		return LoginResult{}, err
	}
	emailKey := ratelimit.EmailKey(classLogin, mac)
	res, err := a.limiter.Allow(ctx, emailKey, loginEmailPolicy)
	if err != nil {
		return LoginResult{}, fmt.Errorf("identity: лимит входа по почте: %w", err)
	}
	if !res.Allowed {
		return LoginResult{}, httpx.NewError(http.StatusTooManyRequests, httpx.CodeRateLimited).WithRetryAfter(res.RetryAfter)
	}
	riskKeys := []string{emailKey}
	if c.IP.IsValid() {
		riskKeys = append(riskKeys, ratelimit.IPKey(classLogin, c.IP))
	}
	suspicious, err := a.risk.Suspicious(ctx, risk.LoginFailed, riskKeys...)
	if err != nil {
		return LoginResult{}, fmt.Errorf("identity: риск входа: %w", err)
	}
	if suspicious {
		if err := humancheck.Require(ctx, a.human); err != nil {
			return LoginResult{}, err
		}
	}

	user, err := identitydb.New(a.pool).LoginFindUser(ctx, norm)
	switch {
	case errors.Is(err, pgx.ErrNoRows) || (err == nil && user.Status == identitydb.UserStatusDeleted):
		// нет аккаунта и анонимизированный — одна ветка: та же работа argon2, журнала нет (§9.1)
		if err := a.dummyPassword(ctx, pw); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{}, a.loginFailed(ctx, uuid.Nil, riskKeys, c)
	case err != nil:
		return LoginResult{}, fmt.Errorf("identity: аккаунт по почте: %w", err)
	}
	ok, seenHash, err := a.checkPassword(ctx, user.ID, pw)
	if err != nil {
		return LoginResult{}, err
	}
	if !ok {
		return LoginResult{}, a.loginFailed(ctx, user.ID, riskKeys, c)
	}
	var out LoginResult
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		u, err := a.lockUser(ctx, tx, user.ID)
		if errors.Is(err, errUserGone) {
			return errLoginStale
		}
		if err != nil {
			return err
		}
		a.firstLock("login")
		same, err := a.passwordUnchanged(ctx, tx, u.ID, seenHash)
		if err != nil {
			return err
		}
		if !same {
			return errLoginStale
		}
		dev, err := a.upsertDevice(ctx, tx, c)
		if err != nil {
			return err
		}
		out = LoginResult{}
		if out.LoginOutcome, err = a.finishLogin(ctx, tx, u, c, dev, domain.MethodPassword, false, false); err != nil {
			return err
		}
		if out.Status == StatusAuthenticated {
			out.DocumentsRequired, err = a.documentsRequired(ctx, identitydb.New(tx), u.ID)
		}
		return err
	})
	switch {
	case errors.Is(err, errLoginStale):
		return LoginResult{}, a.loginFailed(ctx, user.ID, riskKeys, c)
	case errors.Is(err, errAccountDeleted):
		// анонимизирован между проверкой и замком — как «нет аккаунта»
		return LoginResult{}, a.loginFailed(ctx, uuid.Nil, riskKeys, c)
	case err != nil:
		return LoginResult{}, err
	}
	return out, nil
}

// loginFailed — неудача входа: risk.LoginFailed по почте и IP (лимитер, вне транзакции; сбой — в журнал, ответ не
// меняется), у существующего аккаунта — security_events (login_failed) и аудит anonymous без object_id в своей
// транзакции (журнал не оракул существования). Ответ — всегда errInvalidCredentials.
func (a *App) loginFailed(ctx context.Context, userID uuid.UUID, riskKeys []string, c Client) error {
	for _, k := range riskKeys {
		if err := a.risk.Record(ctx, risk.LoginFailed, k); err != nil {
			a.log.ErrorContext(ctx, "identity: учёт неудачного входа", "err", err)
		}
	}
	if userID == uuid.Nil {
		return errInvalidCredentials
	}
	if err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: userID, Kind: domain.SecLoginFailed, IP: c.IP,
			UserAgent: c.UserAgent, Meta: map[string]any{"method": string(domain.MethodPassword),
				"client_kind": string(c.Kind)}}); err != nil {
			return err
		}
		return a.auditAnonymous(ctx, ActionLoginFailed)
	}); err != nil {
		return err
	}
	return errInvalidCredentials
}

// DocumentsRequired — documents_acceptance_required (спека §3.16): игрок не принял текущую версию хотя бы одного
// документа из API_LEGAL_DOCUMENTS; чтение пулом. Вход считает его в своей транзакции (documentsRequired на tx).
func (a *App) DocumentsRequired(ctx context.Context, userID uuid.UUID) (bool, error) {
	return a.documentsRequired(ctx, identitydb.New(a.pool), userID)
}

// documentsRequired — DocumentsRequired запросом q (пул или tx вызывающего).
func (a *App) documentsRequired(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) (bool, error) {
	rows, err := q.LoginConsents(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("identity: согласия: %w", err)
	}
	for _, doc := range a.legal.Items() {
		found := false
		for _, r := range rows {
			if r.Document == string(doc.Document) && r.Version == doc.Version {
				found = true
				break
			}
		}
		if !found {
			return true, nil
		}
	}
	return false, nil
}
