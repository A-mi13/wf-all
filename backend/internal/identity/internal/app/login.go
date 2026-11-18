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

// Login — вход по почте и паролю. Порядок: устройство мобильного (422) → форма почты (400 по полю) → лимит почты
// 30/ч (429, ключ — HMAC «login ‖ почта», одинаков для несуществующих) → риск risk.LoginFailed по почте или IP —
// humancheck.Require (403 с задачей) → аккаунт пулом без замка → пароль вне транзакции (checkPassword; «нет
// аккаунта» и deleted — dummyPassword, та же работа argon2). Неудача — risk.Record по почте и IP, у существующего
// аккаунта — security_events (login_failed), у любой — аудит anonymous без object_id (коммитятся до ответа), ответ — один
// 400 identity.invalid_credentials. Успех — транзакция: lockUser (R30), хэш тот же, что проверен (иначе — неудача),
// устройство, finishLogin (banned → 403, 2FA → mfa_required, pending_deletion → deletion_pending, иначе сессия и
// documents_acceptance_required по согласиям в той же tx).
//
// Сбой хранилища лимитов (лимит почты и риск) — ошибка (500) до поиска аккаунта и argon2: одна политика с кодами и
// сбросом пароля (m5 ревью W4-Б). Пропуск открыл бы перебор паролей без счётчиков, а лимиты живут в той же базе, что
// и аккаунт: при их сбое вход всё равно не завершится.
func (a *App) Login(ctx context.Context, email, pw string, c Client) (AuthOutcome, error) {
	if err := a.requireDevice(c); err != nil {
		return AuthOutcome{}, err
	}
	norm, ok := emailaddr.Normalize(email)
	if !ok {
		return AuthOutcome{}, errEmailShape
	}
	mac, err := a.secrets.MAC(a.secrets.CurrentID(), domain.LabelRateLimit, []byte(classLogin+norm))
	if err != nil {
		return AuthOutcome{}, err
	}
	emailKey := ratelimit.EmailKey(classLogin, mac)
	res, err := a.limiter.Allow(ctx, emailKey, loginEmailPolicy)
	if err != nil {
		return AuthOutcome{}, fmt.Errorf("identity: лимит входа по почте: %w", err)
	}
	if !res.Allowed {
		return AuthOutcome{}, httpx.NewError(http.StatusTooManyRequests, httpx.CodeRateLimited).WithRetryAfter(res.RetryAfter)
	}
	riskKeys := []string{emailKey}
	if c.IP.IsValid() {
		riskKeys = append(riskKeys, ratelimit.IPKey(classLogin, c.IP))
	}
	suspicious, err := a.risk.Suspicious(ctx, risk.LoginFailed, riskKeys...)
	if err != nil {
		return AuthOutcome{}, fmt.Errorf("identity: риск входа: %w", err)
	}
	if suspicious {
		if err := humancheck.Require(ctx, a.human); err != nil {
			return AuthOutcome{}, err
		}
	}

	user, err := identitydb.New(a.pool).LoginFindUser(ctx, norm)
	switch {
	case errors.Is(err, pgx.ErrNoRows) || (err == nil && (user.Status == identitydb.UserStatusDeleted ||
		a.purgeDue(user.Status, user.PurgeAt))):
		// нет аккаунта, анонимизированный и с вышедшим сроком отмены удаления (m4 ревью 2) — одна ветка: та же работа
		// argon2 и та же транзакция неудачи с аудитом без object_id (§9.1, loginFailed)
		if err := a.dummyPassword(ctx, pw); err != nil {
			return AuthOutcome{}, err
		}
		return AuthOutcome{}, a.loginFailed(ctx, uuid.Nil, riskKeys, c)
	case err != nil:
		return AuthOutcome{}, fmt.Errorf("identity: аккаунт по почте: %w", err)
	}
	ok, seenHash, err := a.checkPassword(ctx, user.ID, pw)
	if err != nil {
		return AuthOutcome{}, err
	}
	if !ok {
		return AuthOutcome{}, a.loginFailed(ctx, user.ID, riskKeys, c)
	}
	var out AuthOutcome
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
		out = AuthOutcome{}
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
		return AuthOutcome{}, a.loginFailed(ctx, user.ID, riskKeys, c)
	case errors.Is(err, errAccountDeleted):
		// анонимизирован между проверкой и замком — как «нет аккаунта»
		return AuthOutcome{}, a.loginFailed(ctx, uuid.Nil, riskKeys, c)
	case err != nil:
		return AuthOutcome{}, err
	}
	return out, nil
}

// loginFailed — неудача входа: risk.LoginFailed по почте и IP (лимитер, вне транзакции; сбой — в журнал, ответ не
// меняется), затем одна транзакция: аудит anonymous без object_id — у любой почты, security_events (login_failed) — у
// существующего аккаунта. Транзакция с коммитом — в обеих ветках (§9.1): без неё у «нет почты» коммит с fsync выдавал
// существующую почту по времени ответа (TestLoginFailureSameWork, CI main 18.11); журнал не оракул — запись одна и та
// же. Ответ — всегда errInvalidCredentials.
func (a *App) loginFailed(ctx context.Context, userID uuid.UUID, riskKeys []string, c Client) error {
	for _, k := range riskKeys {
		if err := a.risk.Record(ctx, risk.LoginFailed, k); err != nil {
			a.log.ErrorContext(ctx, "identity: учёт неудачного входа", "err", err)
		}
	}
	if err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		if userID == uuid.Nil {
			return a.auditAnonymous(ctx, ActionLoginFailed)
		}
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

// documentsRequired — DocumentsRequired запросом q (пул или tx вызывающего). Правило одно с me (m2 ревью 2):
// последняя принятая версия документа (latestConsents) ≠ текущей — принять нужно (consentsOutdated).
func (a *App) documentsRequired(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) (bool, error) {
	accepted, err := a.latestConsents(ctx, q, userID)
	if err != nil {
		return false, err
	}
	return a.consentsOutdated(accepted), nil
}

// consentsOutdated — хотя бы у одного документа API_LEGAL_DOCUMENTS последняя принятая версия не текущая (или не
// принята никакая): вход, сброс пароля, второй шаг, отмена удаления и me считают одним правилом.
func (a *App) consentsOutdated(accepted map[domain.Document]string) bool {
	for _, d := range a.legal.Items() {
		if ver, ok := accepted[d.Document]; !ok || ver != d.Version {
			return true
		}
	}
	return false
}
