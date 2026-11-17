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
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/password"
)

// Ядро входа (R6, Task 9): checkPassword, verifySecondFactor, finishLogin — ими пользуются регистрация, вход, сброс
// пароля, отмена удаления, шаг 2FA и свежий вход. Ни один путь не создаёт сессию в обход finishLogin.

// Статусы результата входа (AuthResult.status).
const (
	StatusAuthenticated   = "authenticated"
	StatusMfaRequired     = "mfa_required"
	StatusDeletionPending = "deletion_pending"
)

// banCapability — ограничение бана в user_restrictions: его until — blocked_until ответа account_blocked (нет
// строки или until NULL — бессрочно). Список возможностей — спека moderation; до неё бан — capability login.
const banCapability = "login"

// flagPasskeysLogin — аварийный флаг входа ключом доступа (methods шага 2FA, §3.3).
const flagPasskeysLogin = "auth_passkeys_login" //nolint:gosec // G101: ключ флага функций, не учётные данные

// UserBrief — аккаунт в результате входа.
type UserBrief struct {
	ID       uuid.UUID
	Nickname string
	Status   string
	Locale   string
}

// AuthOutcome — итог сценария, выдающего AuthResult (вход, регистрация, отмена удаления, сброс пароля, второй шаг):
// DocumentsRequired считается в транзакции сценария — после коммита база не читается.
type AuthOutcome struct {
	LoginOutcome
	DocumentsRequired bool
}

// LoginOutcome — итог завершения входа: сессия (authenticated), шаг 2FA (mfa_required) или период отмены удаления
// (deletion_pending). Токены шагов — сырые только здесь, в базе — SHA-256.
type LoginOutcome struct {
	Status         string
	Issued         *Issued
	MfaToken       string
	MfaExpires     time.Time
	Methods        []string
	ResetAt        *time.Time
	RestoreToken   string
	RestoreExpires time.Time
	PurgeAt        time.Time
	User           UserBrief
}

var (
	// errBlockedAsExpired — аккаунт забанен, а у операции нет account_blocked в контракте (R29: completeMfa,
	// confirmPasswordReset): вызывающий отвечает 410 своего токена или кода.
	errBlockedAsExpired = errors.New("identity: аккаунт заблокирован — ответ «истёк» операции")
	// errAccountDeleted — аккаунт анонимизирован: вызывающий отвечает как «нет аккаунта».
	errAccountDeleted = errors.New("identity: аккаунт удалён")
)

// challengeData — несекретные данные первого шага в auth_challenges.data (device_id — строка devices, не install_id).
type challengeData struct {
	Method     domain.AuthMethod `json:"method"`
	MfaDone    bool              `json:"mfa_done,omitempty"`
	DeviceID   *uuid.UUID        `json:"device_id,omitempty"`
	ClientKind domain.ClientKind `json:"client_kind"`
}

// checkPassword — пароль аккаунта без открытой транзакции и без занятого соединения во время argon2 (I10, R31): хэш
// читается пулом, затем Verify. Пароля у аккаунта нет — VerifyDummy и false (время как у настоящей проверки).
// Пароль длиннее предела хэшера (password.ErrTooLong) — неверный пароль, не 500: вызывающий отвечает
// invalid_credentials и засчитывает неудачу; argon2 на приманке выравнивает время (tooLong).
// Устаревшие параметры — Hash вне tx и условная запись WHERE password_hash = seenHash (пароль сменили между Verify и
// записью — не затирается). Запись не прошла (0 строк: параллельный верный вход уже пересчитал или пароль сменили) —
// хэш перечитывается и пароль один раз проверяется по нему (I2 ревью W3: два верных входа разом оба успешны).
// seenHash — хэш, который сейчас в базе по мнению проверки: вызывающий в своей tx после lockUser сверяет его с
// текущим (passwordUnchanged) и при расхождении отвечает как на неверный пароль (R6). Сбой хэшера (отменённый ctx у
// семафора) — ошибка (500), не «неверный пароль».
func (a *App) checkPassword(ctx context.Context, userID uuid.UUID, pw string) (ok bool, seenHash string, err error) {
	q := identitydb.New(a.pool)
	hash, err := q.GetPasswordHash(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", a.passwordDummy(ctx, pw)
	}
	if err != nil {
		return false, "", fmt.Errorf("identity: хэш пароля: %w", err)
	}
	ok, rehash, err := a.passwordVerify(ctx, pw, hash)
	if err != nil || !ok {
		return false, hash, err
	}
	if !rehash {
		return true, hash, nil
	}
	fresh, err := a.hasher.Hash(ctx, pw)
	if err != nil {
		return false, "", err
	}
	n, err := q.RehashPassword(ctx, identitydb.RehashPasswordParams{NewHash: fresh, Now: a.clock.Now(),
		UserID: userID, SeenHash: hash})
	if err != nil {
		return false, "", fmt.Errorf("identity: пересчёт хэша: %w", err)
	}
	if n == 1 {
		return true, fresh, nil
	}
	cur, err := q.GetPasswordHash(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, hash, nil // пароль сняли — passwordUnchanged вызывающего ответит «неверный»
	}
	if err != nil {
		return false, "", fmt.Errorf("identity: хэш пароля: %w", err)
	}
	if cur == hash {
		return true, hash, nil
	}
	// один повтор без нового пересчёта: needsRehash второго хэша не важен — его записал параллельный вход
	ok, _, err = a.passwordVerify(ctx, pw, cur)
	if err != nil || !ok {
		return false, cur, err
	}
	return true, cur, nil
}

// passwordVerify — Verify хэшера; пароль длиннее предела (password.ErrTooLong) — «не подходит» с той же работой argon2.
func (a *App) passwordVerify(ctx context.Context, pw, encoded string) (ok, rehash bool, err error) {
	ok, rehash, err = a.hasher.Verify(ctx, pw, encoded)
	if errors.Is(err, password.ErrTooLong) {
		return false, false, a.tooLong(ctx)
	}
	return ok, rehash, err
}

// passwordDummy — VerifyDummy хэшера; пароль длиннее предела — та же работа argon2 и nil.
func (a *App) passwordDummy(ctx context.Context, pw string) error {
	err := a.hasher.VerifyDummy(ctx, pw)
	if errors.Is(err, password.ErrTooLong) {
		return a.tooLong(ctx)
	}
	return err
}

// tooLong — argon2 на приманке для пароля длиннее предела хэшера: хэшер отказывает до argon2, а ответ по времени не
// должен отличаться от неверного пароля (§9.1). Ошибка — только отмена ctx у семафора.
func (a *App) tooLong(ctx context.Context) error {
	return a.hasher.VerifyDummy(ctx, "")
}

// passwordUnchanged — хэш в базе тот же, что видела checkPassword (вызывающий держит lockUser: смена пароля тоже
// берёт его, поэтому после ответа «тот же» пароль не сменится до конца tx).
func (a *App) passwordUnchanged(ctx context.Context, tx pgx.Tx, userID uuid.UUID, seenHash string) (bool, error) {
	hash, err := identitydb.New(tx).GetPasswordHash(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return hash == seenHash, nil
}

// dummyPassword — та же работа, что checkPassword, для «нет аккаунта» (§9.1): тот же запрос хэша пулом (строки нет)
// и argon2 VerifyDummy (пароль длиннее предела — та же работа на приманке, nil). Равенство времени тестом проверено
// только для argon2 (TestCheckPasswordNoAccountCostsLikeReal): выравнивающий запрос хэша много меньше шума argon2, и
// его пропуск тест не ловит — запрос держится этим комментарием.
func (a *App) dummyPassword(ctx context.Context, pw string) error {
	if _, err := identitydb.New(a.pool).GetPasswordHash(ctx, uuid.Nil); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("identity: хэш пароля: %w", err)
	}
	return a.passwordDummy(ctx, pw)
}

// finishLogin — завершение входа по статусу аккаунта (§3.2, §3.11; порядок R6): заблокирован (R36) → 403
// identity.account_blocked (blocked_until из ограничения бана) или errBlockedAsExpired; 2FA включена и не пройдена →
// mfa_required (mfa_token на 5 минут, methods, reset_at); pending_deletion → deletion_pending (restore_token на 10
// минут, без сессии); иначе — сессия (mfa_at = now при mfaDone), связь с устройством, security_events (login), хук
// отмены сброса 2FA при mfaDone. deleted и pending_deletion с наступившим purge_at (срок отмены вышел, анонимизация
// ещё не прошла — §3.11 п. 2, m4 ревью 2) сюда не доходят (errAccountDeleted). Вызывающий держит lockUser; строка
// перечитывается под замком — статус между шагами мог смениться.
func (a *App) finishLogin(ctx context.Context, tx pgx.Tx, u userRow, c Client, deviceID uuid.UUID,
	method domain.AuthMethod, mfaDone, blockedAsExpired bool) (LoginOutcome, error) {
	return a.finishLoginAs(ctx, tx, u, c, deviceID, method, mfaDone, blockedAsExpired, journalLogin)
}

// loginJournal — запись журнала безопасности о выданной сессии: вход (login, meta с признаком второго фактора) или
// регистрация (signup: устройство отмечается signed_up, meta без mfa — как до перевода регистрации на ядро, m1 ревью 2).
type loginJournal int

const (
	journalLogin loginJournal = iota
	journalSignup
)

// finishLoginAs — finishLogin с видом журнала; единственное место, где выдаётся сессия (страж
// TestIssueSessionOnlyInFinishLogin).
func (a *App) finishLoginAs(ctx context.Context, tx pgx.Tx, u userRow, c Client, deviceID uuid.UUID,
	method domain.AuthMethod, mfaDone, blockedAsExpired bool, journal loginJournal) (LoginOutcome, error) {
	u, err := a.lockUser(ctx, tx, u.ID)
	if err != nil {
		return LoginOutcome{}, err
	}
	q := identitydb.New(tx)
	brief := UserBrief{ID: u.ID, Nickname: u.Nickname, Status: string(u.Status), Locale: u.Locale}
	blocked, err := a.blocked(ctx, q, u, a.clock.Now())
	if err != nil {
		return LoginOutcome{}, err
	}
	switch {
	case blocked: // R36: banned или бан поверх периода удаления (R11) — не deletion_pending
		if blockedAsExpired {
			return LoginOutcome{}, errBlockedAsExpired
		}
		return LoginOutcome{}, a.accountBlocked(ctx, q, u.ID)
	case u.Status == identitydb.UserStatusDeleted || a.purgeDue(u.Status, u.PurgeAt):
		return LoginOutcome{}, errAccountDeleted
	}
	var dev *uuid.UUID
	if deviceID != uuid.Nil {
		dev = &deviceID
	}
	data := challengeData{Method: method, MfaDone: mfaDone, DeviceID: dev, ClientKind: c.Kind}
	if !mfaDone {
		on, err := q.MfaConfirmed(ctx, u.ID)
		if err != nil {
			return LoginOutcome{}, err
		}
		if on {
			return a.mfaStep(ctx, tx, q, brief, data)
		}
	}
	if u.Status == identitydb.UserStatusPendingDeletion {
		raw, exp, err := a.newChallenge(ctx, tx, domain.ChallengeDeletionRestore, u.ID, data)
		if err != nil {
			return LoginOutcome{}, err
		}
		out := LoginOutcome{Status: StatusDeletionPending, RestoreToken: raw, RestoreExpires: exp, User: brief}
		if u.PurgeAt != nil {
			out.PurgeAt = *u.PurgeAt
		}
		return out, nil
	}
	var mfaAt *time.Time
	if mfaDone {
		now := a.clock.Now()
		mfaAt = &now
	}
	issued, err := a.issueSession(ctx, tx, u.ID, deviceID, c, method, mfaAt)
	if err != nil {
		return LoginOutcome{}, err
	}
	signup := journal == journalSignup
	if err := a.linkDevice(ctx, tx, deviceID, u.ID, signup); err != nil {
		return LoginOutcome{}, err
	}
	ev := SecurityEvent{UserID: u.ID, Kind: domain.SecLogin, SessionID: issued.SessionID, DeviceID: deviceID, IP: c.IP,
		UserAgent: c.UserAgent, Meta: map[string]any{"method": string(method), "mfa": mfaDone, "client_kind": string(c.Kind)}}
	if signup {
		ev.Kind, ev.Meta = domain.SecSignup, map[string]any{"method": string(method), "client_kind": string(c.Kind)}
	}
	if _, err := a.securityEvent(ctx, tx, ev); err != nil {
		return LoginOutcome{}, err
	}
	if mfaDone {
		if err := a.onSecondFactorLogin(ctx, tx, u.ID); err != nil {
			return LoginOutcome{}, err
		}
	}
	return LoginOutcome{Status: StatusAuthenticated, Issued: &issued, User: brief}, nil
}

// purgeDue — срок отмены удаления вышел: pending_deletion и purge_at ≤ now (ровно purge_at — уже нельзя, §3.11 п. 2).
// Такой аккаунт до прогона анонимизации для входа и отмены — как удалённый.
func (a *App) purgeDue(status identitydb.UserStatus, purgeAt *time.Time) bool {
	return status == identitydb.UserStatusPendingDeletion && purgeAt != nil && !a.clock.Now().Before(*purgeAt)
}

// mfaStep — шаг 2FA: mfa_token, способы (passkey — при ключах у аккаунта и флаге auth_passkeys_login) и срок живого
// сброса 2FA поддержкой.
func (a *App) mfaStep(ctx context.Context, tx pgx.Tx, q *identitydb.Queries, brief UserBrief, data challengeData) (LoginOutcome, error) {
	methods := []string{FactorTOTP, FactorRecovery}
	has, err := q.HasPasskeys(ctx, brief.ID)
	if err != nil {
		return LoginOutcome{}, err
	}
	if has {
		// флаг — в tx входа: чтение пулом заняло бы второе соединение при удержанном первом (m3 ревью W3)
		on, err := flags.NewStore(tx).EnabledGlobally(ctx, flagPasskeysLogin)
		if err != nil {
			return LoginOutcome{}, err
		}
		if on {
			methods = append(methods, FactorPasskey)
		}
	}
	raw, exp, err := a.newChallenge(ctx, tx, domain.ChallengeMfa, brief.ID, data)
	if err != nil {
		return LoginOutcome{}, err
	}
	out := LoginOutcome{Status: StatusMfaRequired, MfaToken: raw, MfaExpires: exp, Methods: methods, User: brief}
	at, err := q.LiveMfaResetAt(ctx, brief.ID)
	switch {
	case err == nil:
		out.ResetAt = &at
	case !errors.Is(err, pgx.ErrNoRows):
		return LoginOutcome{}, err
	}
	return out, nil
}

// accountBlocked — 403 identity.account_blocked: blocked_until — самое позднее until живого ограничения бана; есть
// бессрочное или ограничений нет — без поля (бессрочно).
func (a *App) accountBlocked(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) error {
	untils, err := q.BanRestrictions(ctx, identitydb.BanRestrictionsParams{UserID: userID, Capability: banCapability,
		Now: a.clock.Now()})
	if err != nil {
		return err
	}
	e := httpx.NewError(http.StatusForbidden, identity.CodeAccountBlocked)
	var latest *time.Time
	for _, u := range untils {
		if u == nil {
			return e
		}
		if latest == nil || u.After(*latest) {
			latest = u
		}
	}
	if latest == nil {
		return e
	}
	return e.WithBlockedUntil(*latest)
}
