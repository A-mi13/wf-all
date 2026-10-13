package app

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/domain"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// LoginResult — итог шага пароля: токен новой сессии (в cookie) и её стадия (mfa_pending).
type LoginResult struct {
	Token string
	Stage string
}

// Login — шаг пароля (§4.6, D7). Сначала лимит по почте (10 за 15 минут, одинаково для
// несуществующих адресов) — 429 RateLimitError. Нет почты, учётка не active, без подтверждённого
// второго фактора или закрыта (locked_until в будущем) — пароль не проверяется, время выравнивает
// VerifyDummy; эти отказы, неверный и слишком длинный пароль — один ErrInvalidCredentials и аудит
// без автора (anonymous, почта маскируется). Неудачи пароля учётку не трогают. Успех — новая
// сессия mfa_pending на 5 минут; сессия старой cookie (сотрудник в ctx от конвейера) гасится как
// заменённая.
func (a *App) Login(ctx context.Context, email, pw string, m staff.Meta) (LoginResult, error) {
	norm, valid := domain.NormalizeEmail(email)
	keyMaterial := norm
	if !valid {
		keyMaterial = email // кривая почта тоже упирается в лимит — по строке как есть
	}
	key, err := a.loginEmailKey(keyMaterial)
	if err != nil {
		return LoginResult{}, err
	}
	res, err := a.limiter.Allow(ctx, key, loginEmailPolicy)
	switch {
	case err != nil:
		// хранилище лимитов недоступно — пропуск, как в ratelimit.Middleware (IP-лимит конвейера
		// и argon2 под семафором по-прежнему сдерживают перебор)
		a.log.ErrorContext(ctx, "staff: лимит входа по почте недоступен", "err", err)
	case !res.Allowed:
		return LoginResult{}, &staff.RateLimitError{RetryAfter: res.RetryAfter}
	}

	var acct *staffdb.GetLoginAccountRow
	if valid {
		row, err := staffdb.New(a.pool).GetLoginAccount(ctx, norm)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return LoginResult{}, err
		default:
			acct = &row
		}
	}
	now := a.clock.Now()
	usable := acct != nil && acct.Status == staffdb.StaffStatusActive && acct.MfaConfirmed &&
		acct.PasswordHash != nil && (acct.LockedUntil == nil || !now.Before(*acct.LockedUntil))
	if !usable {
		if err := a.hasher.VerifyDummy(ctx, pw); err != nil && !errors.Is(err, password.ErrTooLong) {
			return LoginResult{}, err
		}
		return LoginResult{}, a.passwordFailed(ctx, acct, keyMaterial, m)
	}
	ok, _, err := a.hasher.Verify(ctx, pw, *acct.PasswordHash)
	switch {
	case errors.Is(err, password.ErrTooLong):
		return LoginResult{}, a.passwordFailed(ctx, acct, keyMaterial, m)
	case err != nil:
		return LoginResult{}, err
	case !ok:
		return LoginResult{}, a.passwordFailed(ctx, acct, keyMaterial, m)
	}

	var old uuid.UUID
	if s, ok := auth.StaffFrom(ctx); ok {
		old = s.SessionID
	}
	var raw string
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		raw, err = a.replaceSession(ctx, staffdb.New(tx), old, newSession{
			staffID: acct.ID, stage: auth.StageMFAPending, created: now, expires: now.Add(mfaPendingTTL),
		}, now, m)
		return err
	})
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: raw, Stage: auth.StageMFAPending}, nil
}

// passwordFailed — аудит неудачи пароля без автора (anonymous): почта в After маскируется
// audit.Mask, объект — учётка, если почта нашлась. Ответ — ErrInvalidCredentials.
func (a *App) passwordFailed(ctx context.Context, acct *staffdb.GetLoginAccountRow, email string, m staff.Meta) error {
	var object uuid.UUID
	if acct != nil {
		object = acct.ID
	}
	// обрыв соединения клиента не теряет запись неудачи
	dctx, cancel := detached(ctx)
	defer cancel()
	err := db.InTx(dctx, a.pool, func(ctx context.Context, _ pgx.Tx) error {
		return audit.Write(ctx, audit.Entry{
			ActorRole: roleAnonymous, Action: actionLoginFailed, ObjectType: objectAccount, ObjectID: object,
			After: map[string]any{"email": email, "step": "password"},
			IP:    m.IP, UserAgent: m.UserAgent,
		})
	})
	if err != nil {
		return err
	}
	return staff.ErrInvalidCredentials
}

// VerifyMFA — шаг второго фактора входа (стадия mfa_pending): TOTP или резервный код. Успех —
// новый токен (стадия active, mfa_at, last_login_at, счётчик неудач 0, аудит staff.login); неудача —
// *staff.MFAError с attempts_left, пятая подряд закрывает учётку на час.
func (a *App) VerifyMFA(ctx context.Context, s *auth.Staff, code, recovery string, m staff.Meta) (string, error) {
	return a.passFactor(ctx, s, code, recovery, m, stepMFA)
}

// Unlock — разблокировка сессии в Reauth (бездействие или смена сети): второй фактор → новый токен,
// mfa_at, новый IP; абсолютный срок сессии не продлевается. Неудача считается в блокировку.
func (a *App) Unlock(ctx context.Context, s *auth.Staff, code, recovery string, m staff.Meta) (string, error) {
	return a.passFactor(ctx, s, code, recovery, m, stepUnlock)
}

// Logout — гасит текущую сессию (logout); повтор — без ошибки.
func (a *App) Logout(ctx context.Context, s *auth.Staff) error {
	now := a.clock.Now()
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := staffdb.New(tx).RevokeSession(ctx, staffdb.RevokeSessionParams{Now: now, Reason: reasonLogout, ID: s.SessionID})
		return err
	})
}
