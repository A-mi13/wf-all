package app_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/staff"
)

// Успех пароля — новая сессия mfa_pending на 5 минут; через 5 минут она недействительна.
func TestLoginStartsMFAPending(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	ctx := context.Background()
	res, err := e.app.Login(ctx, "  Olga.V@WF.example ", testPassword, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stage != auth.StageMFAPending || res.Token == "" {
		t.Fatalf("итог %+v", res)
	}
	s := e.load(res.Token)
	if s.StaffID != m.ID || s.Stage != auth.StageMFAPending || s.Reauth {
		t.Fatalf("сессия %+v", s)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE staff_id = $1 AND stage = 'mfa_pending'
		AND expires_at = $2 AND mfa_at IS NULL AND ip = '203.0.113.10' AND user_agent = 'Mozilla/5.0 test'`,
		m.ID, t0.Add(5*time.Minute)); n != 1 {
		t.Fatalf("строк сессии %d", n)
	}
	e.clock.Advance(5 * time.Minute)
	if _, err := e.app.LoadStaff(ctx, res.Token, ipHome, false); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("mfa_pending через 5 минут: %v", err)
	}
}

// Каждый вид отказа пароля — один и тот же ErrInvalidCredentials (существование учётки не
// раскрывается), учётку они не трогают; аудит — без автора (anonymous), почта маскирована.
func TestLoginRejectionsLookTheSame(t *testing.T) {
	e := newEnv(t)
	e.addMember("ok@wf.example", memberOpts{})
	e.addMember("invited@wf.example", memberOpts{Status: "invited"})
	e.addMember("disabled@wf.example", memberOpts{Status: "disabled"})
	e.addMember("nomfa@wf.example", memberOpts{NoMFA: true})
	locked := e.addMember("locked@wf.example", memberOpts{})
	e.exec(`UPDATE staff_accounts SET locked_until = $2 WHERE id = $1`, locked.ID, t0.Add(30*time.Minute))
	e.exec(`UPDATE staff_mfa SET confirmed_at = NULL WHERE staff_id = (SELECT id FROM staff_accounts WHERE email = 'nomfa@wf.example')`)
	cases := []struct{ name, email, pw string }{
		{"нет почты", "ghost@wf.example", testPassword},
		{"неверный пароль", "ok@wf.example", testPassword + "!"},
		{"слишком длинный пароль", "ok@wf.example", strings.Repeat("я", 600)},
		{"приглашена", "invited@wf.example", testPassword},
		{"выключена", "disabled@wf.example", testPassword},
		{"без 2FA", "nomfa@wf.example", testPassword},
		{"закрыта (верный пароль)", "locked@wf.example", testPassword},
		{"кривая почта", "not-an-email", testPassword},
	}
	for _, c := range cases {
		_, err := e.app.Login(context.Background(), c.email, c.pw, metaWeb)
		if !errors.Is(err, staff.ErrInvalidCredentials) {
			t.Errorf("%s: %v", c.name, err)
		}
		if code, _ := staff.CodeOf(err); code != staff.CodeInvalidCredentials {
			t.Errorf("%s: код %q", c.name, code)
		}
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions`); n != 0 {
		t.Fatalf("сессий после отказов %d", n)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE mfa_failures <> 0`); n != 0 {
		t.Fatal("неудача пароля тронула счётчик второго фактора")
	}
	rows := e.audits("staff.login_failed")
	if len(rows) != len(cases) {
		t.Fatalf("записей аудита %d, ждали %d", len(rows), len(cases))
	}
	for _, r := range rows {
		if r.Role != "anonymous" || r.StaffID != nil || r.Label != nil || !strings.Contains(r.After, `"email"`) {
			t.Fatalf("неудача пароля в аудите: %+v", r)
		}
		for _, c := range cases {
			if strings.Contains(r.After, `"`+c.email+`"`) {
				t.Fatalf("почта %s в аудите не маскирована: %s", c.email, r.After)
			}
		}
	}
}

// D7: закрытая учётка пароль не проверяет вовсе. Хэш в базе битый — Verify вернул бы ErrMalformed
// (сбой, 500); закрытая учётка отвечает ErrInvalidCredentials, а открытая с тем же хэшем — сбоем.
func TestLockedAccountSkipsVerify(t *testing.T) {
	e := newEnv(t)
	broken := "$argon2id$v=19$m=999999999,t=1,p=1$c2FsdA$a2V5"
	locked := e.addMember("locked@wf.example", memberOpts{PasswordHash: broken})
	e.exec(`UPDATE staff_accounts SET locked_until = $2 WHERE id = $1`, locked.ID, t0.Add(time.Minute))
	if _, err := e.app.Login(context.Background(), locked.Email, testPassword, metaWeb); !errors.Is(err, staff.ErrInvalidCredentials) {
		t.Fatalf("закрытая учётка: %v", err)
	}
	e.clock.Advance(time.Minute) // блокировка истекла — пароль проверяется, хэш битый
	_, err := e.app.Login(context.Background(), locked.Email, testPassword, metaWeb)
	if err == nil || errors.Is(err, staff.ErrInvalidCredentials) {
		t.Fatalf("открытая учётка с битым хэшем: %v — Verify не вызван?", err)
	}
}

// Лимит по почте (D7): 10 попыток за 15 минут → 429, одинаково для существующей и несуществующей
// почты; почта считается после нормализации (регистр и пробелы — та же почта).
func TestLoginEmailRateLimit(t *testing.T) {
	e := newEnv(t)
	e.addMember("olga.v@wf.example", memberOpts{})
	for _, email := range []string{"Olga.V@wf.example", "ghost@wf.example"} {
		for i := range 10 {
			variant := email
			if i%2 == 1 {
				variant = "  " + strings.ToUpper(email) + " "
			}
			if _, err := e.app.Login(context.Background(), variant, "неверный пароль 1", metaWeb); !errors.Is(err, staff.ErrInvalidCredentials) {
				t.Fatalf("%s попытка %d: %v", email, i+1, err)
			}
		}
		_, err := e.app.Login(context.Background(), email, testPassword, metaWeb)
		if !errors.Is(err, staff.ErrRateLimited) {
			t.Fatalf("%s: 11-я попытка — %v, ждали лимит", email, err)
		}
		pe, ok := errors.AsType[httpx.ProblemError](err)
		if !ok || pe.Problem().Status != http.StatusTooManyRequests || pe.Problem().RetryAfter < 1 {
			t.Fatalf("%s: ответ лимита %v", email, err)
		}
	}
	// ключ — HMAC почты, самой почты в rate_limits нет
	key, err := e.app.LoginEmailKey("ghost@wf.example")
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM rate_limits WHERE key = $1`, key); n != 1 {
		t.Fatalf("строк лимита по почте %d", n)
	}
	if n := e.count(`SELECT count(*) FROM rate_limits WHERE key LIKE '%wf.example%'`); n != 0 {
		t.Fatal("почта в ключе лимита открытым текстом")
	}
	// через 15 минут — снова можно
	e.clock.Advance(15 * time.Minute)
	if _, err := e.app.Login(context.Background(), "olga.v@wf.example", testPassword, metaWeb); err != nil {
		t.Fatalf("после окна лимита: %v", err)
	}
}

// D9: вход со старой cookie (сотрудник в ctx от конвейера) гасит старую сессию как заменённую.
func TestLoginReplacesCookieSession(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	oldTok := e.signIn(m)
	old := e.load(oldTok)
	ctx := auth.WithStaff(context.Background(), old)
	res, err := e.app.Login(ctx, m.Email, testPassword, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.LoadStaff(context.Background(), oldTok, ipHome, false); !errors.Is(err, auth.ErrSessionReplaced) {
		t.Fatalf("старая сессия: %v", err)
	}
	if s := e.load(res.Token); s.Stage != auth.StageMFAPending {
		t.Fatalf("новая сессия %+v", s)
	}
}

// Logout гасит сессию (logout); повтор — без ошибки.
func TestLogout(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	tok := e.signIn(m)
	s := e.load(tok)
	for range 2 {
		if err := e.app.Logout(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.app.LoadStaff(context.Background(), tok, ipHome, false); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("после выхода: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE id = $1 AND revoked_reason = 'logout'`, s.SessionID); n != 1 {
		t.Fatal("сессия не погашена как logout")
	}
}
