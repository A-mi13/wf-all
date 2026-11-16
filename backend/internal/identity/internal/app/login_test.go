package app

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
)

// withPGLimiter — настоящий лимитер игроков на базе теста (счётчики лимитов и риска — в rate_limits) на часах теста.
func withPGLimiter(t *testing.T) envOpt {
	return func(d *identity.Deps) {
		d.Limiter = ratelimit.NewPG(d.Pool, d.Clock, ratelimit.Players)
		r, err := risk.NewAssessor(d.Limiter, risk.DefaultPolicies())
		if err != nil {
			t.Fatal(err)
		}
		d.Risk = r
	}
}

// withArgon — настоящий argon2 (время и предел длины пароля).
func withArgon(t *testing.T) (envOpt, *password.Hasher) {
	h, err := password.NewHasher(password.DefaultParams(), 2)
	if err != nil {
		t.Fatal(err)
	}
	return func(d *identity.Deps) { d.Hasher = h }, h
}

// loginUser — аккаунт с паролем «secret» (хэш fakeHasher).
func loginUser(t *testing.T, e *env, nick string) uuid.UUID {
	t.Helper()
	uid := seedUser(t, e.owner, nick, "ru")
	setPassword(t, e, uid, "h:secret")
	return uid
}

func problemOf(t *testing.T, err error) httpx.Problem {
	t.Helper()
	var pe httpx.ProblemError
	if !errors.As(err, &pe) {
		t.Fatalf("не Problem: %v", err)
	}
	return pe.Problem()
}

var iphone = mobile("ios", "6F9619FF-8B86-4D01-B42D-00CF4FC964FF")

// §3.2, §9.1: нет почты, нет пароля, неверный пароль, deleted — одна и та же Problem; неудача существующего аккаунта
// — security_events (login_failed) и аудит anonymous без object_id; у несуществующего — ничего.
func TestLoginInvalidCredentialsUniform(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	loginUser(t, e, "wrong_pw")
	seedUser(t, e.owner, "no_pw", "ru")
	gone := loginUser(t, e, "gone_user")
	setStatus(t, e, gone, "deleted")
	ctx := context.Background()
	var probs []httpx.Problem
	for _, c := range []struct{ email, pw string }{
		{"nobody@example.com", "secret"},
		{"no_pw@example.com", "secret"},
		{"wrong_pw@example.com", "не тот"},
		{"gone_user@example.com", "secret"},
		{"  WRONG_PW@Example.com ", "не тот"},
	} {
		_, err := e.a.Login(ctx, c.email, c.pw, iphone)
		p := problemOf(t, err)
		if p.Status != 400 || p.Code != identity.CodeInvalidCredentials {
			t.Fatalf("%s: %+v", c.email, p)
		}
		probs = append(probs, p)
	}
	for _, p := range probs[1:] {
		if !reflect.DeepEqual(p, probs[0]) {
			t.Fatalf("ответы различаются: %+v и %+v", p, probs[0])
		}
	}
	// существующие с паролем и без: 3 записи (wrong_pw дважды, no_pw); нет почты и deleted — без записей
	if n := count(t, e.owner, `SELECT count(*) FROM security_events WHERE kind = 'login_failed'`); n != 3 {
		t.Fatalf("login_failed в журнале: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM security_events WHERE kind = 'login_failed' AND user_id = $1`, gone); n != 0 {
		t.Fatalf("deleted в журнале: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND actor_role = 'anonymous'
		AND object_id IS NULL AND actor_user_id IS NULL`, ActionLoginFailed); n != 3 {
		t.Fatalf("аудит login_failed: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND (before::text LIKE '%example%'
		OR after::text LIKE '%example%')`, ActionLoginFailed); n != 0 {
		t.Fatal("почта в аудите")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("сессий %d", n)
	}
}

// Почта не по форме — 400 validation.failed по body.email до поиска; мобильный без device — 422 device_required.
func TestLoginInputErrors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	loginUser(t, e, "input_user")
	_, err := e.a.Login(context.Background(), "не почта", "secret", iphone)
	if p := problemOf(t, err); p.Code != httpx.CodeValidationFailed || len(p.Errors) != 1 || p.Errors[0].Field != "body.email" {
		t.Fatalf("форма почты: %+v", p)
	}
	noDev := iphone
	noDev.Device = nil
	_, err = e.a.Login(context.Background(), "input_user@example.com", "secret", noDev)
	if got := problemCode(err); got != identity.CodeDeviceRequired {
		t.Fatalf("без device: %q", got)
	}
}

// Верный пароль: сессия, связь с устройством, security_events (login); pending_deletion — deletion_pending без сессии.
func TestLoginSuccess(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := loginUser(t, e, "good_user")
	out, err := e.a.Login(context.Background(), "Good_User@example.com", "secret", iphone)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusAuthenticated || out.Issued == nil || out.User.ID != uid || out.Issued.Tokens.Refresh == "" {
		t.Fatalf("%+v", out)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions s JOIN device_accounts d ON d.device_id = s.device_id
		AND d.user_id = s.user_id WHERE s.id = $1 AND s.auth_method = 'password'`, out.Issued.SessionID); n != 1 {
		t.Fatalf("сессия с устройством: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM security_events WHERE kind = 'login' AND user_id = $1`, uid); n != 1 {
		t.Fatalf("login в журнале: %d", n)
	}
	pend := loginUser(t, e, "pending_user")
	setStatus(t, e, pend, "pending_deletion")
	out, err = e.a.Login(context.Background(), "pending_user@example.com", "secret", iphone)
	if err != nil || out.Status != StatusDeletionPending || out.RestoreToken == "" || out.Issued != nil {
		t.Fatalf("pending_deletion: %+v %v", out, err)
	}
	if n := sessions(t, e, pend); n != 0 {
		t.Fatalf("сессий у pending_deletion: %d", n)
	}
}

// documents_acceptance_required: принятые не текущие версии — true; текущие — false.
func TestDocumentsRequired(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "docs_user", "ru")
	req, err := e.a.DocumentsRequired(context.Background(), uid)
	if err != nil || !req {
		t.Fatalf("без согласий: %v %v", req, err)
	}
	for _, d := range []string{"terms", "privacy"} {
		if _, err := e.owner.Exec(context.Background(), `INSERT INTO consents (user_id, document, version, accepted_at, client_kind)
			VALUES ($1, $2, '2026-11-01', now(), 'mobile')`, uid, d); err != nil {
			t.Fatal(err)
		}
	}
	if req, err = e.a.DocumentsRequired(context.Background(), uid); err != nil || req {
		t.Fatalf("текущие приняты: %v %v", req, err)
	}
}

// §3.2 п. 5: banned с верным паролем — 403 account_blocked с blocked_until; с неверным — 400.
func TestLoginBlocked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := loginUser(t, e, "banned_login")
	setStatus(t, e, uid, "banned")
	until := e.clock.Now().Add(48 * time.Hour)
	if _, err := e.owner.Exec(context.Background(), `INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at)
		VALUES (gen_random_uuid(), $1, 'login', $2, gen_random_uuid(), now())`, uid, until); err != nil {
		t.Fatal(err)
	}
	_, err := e.a.Login(context.Background(), "banned_login@example.com", "secret", iphone)
	p := problemOf(t, err)
	if p.Status != 403 || p.Code != identity.CodeAccountBlocked || p.BlockedUntil == nil || !p.BlockedUntil.Equal(until) {
		t.Fatalf("%+v", p)
	}
	_, err = e.a.Login(context.Background(), "banned_login@example.com", "не тот", iphone)
	if got := problemCode(err); got != identity.CodeInvalidCredentials {
		t.Fatalf("неверный пароль у banned: %q", got)
	}
}

// §3.2 п. 3: 5 неудач по почте (или с IP) — следующий вход без решения PoW → 403 humancheck.required; с решением —
// проходит.
func TestLoginRiskRequiresPoW(t *testing.T) {
	t.Parallel()
	e := newEnv(t, withPGLimiter(t))
	loginUser(t, e, "risky")
	ctx := context.Background()
	for range 5 {
		if _, err := e.a.Login(ctx, "risky@example.com", "не тот", iphone); problemCode(err) != identity.CodeInvalidCredentials {
			t.Fatal(err)
		}
	}
	other := iphone
	other.IP = netip.MustParseAddr("198.51.100.9") // другой IP — срабатывает ключ почты
	_, err := e.a.Login(ctx, "risky@example.com", "secret", other)
	var req *humancheck.RequiredError
	if !errors.As(err, &req) {
		t.Fatalf("по почте: %v", err)
	}
	if out, err := e.a.Login(humancheck.WithSolution(ctx, "решение"), "risky@example.com", "secret", other); err != nil ||
		out.Status != StatusAuthenticated {
		t.Fatalf("с решением: %+v %v", out, err)
	}
	// IP: та же сеть 203.0.113.7 набрала 5 неудач — вход на другую почту тоже требует PoW
	loginUser(t, e, "calm")
	if _, err := e.a.Login(ctx, "calm@example.com", "secret", iphone); !errors.As(err, &req) {
		t.Fatalf("по IP: %v", err)
	}
}

// Пароль длиннее предела хэшера (1024 байта) — 400 invalid_credentials, и неудача засчитана в risk.LoginFailed.
func TestLoginTooLongCountsAsFailure(t *testing.T) {
	t.Parallel()
	opt, h := withArgon(t)
	e := newEnv(t, withPGLimiter(t), opt)
	uid := seedUser(t, e.owner, "long_pw", "ru")
	hash, err := h.Hash(context.Background(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	setPassword(t, e, uid, hash)
	long := strings.Repeat("я", 600)
	for range 5 {
		if _, err := e.a.Login(context.Background(), "long_pw@example.com", long, iphone); problemCode(err) != identity.CodeInvalidCredentials {
			t.Fatal(err)
		}
	}
	var req *humancheck.RequiredError
	if _, err := e.a.Login(context.Background(), "long_pw@example.com", "secret", iphone); !errors.As(err, &req) {
		t.Fatalf("после 5 длинных: %v", err)
	}
}

// §3.2 п. 2: 31-я попытка за час по почте — 429 ratelimit.exceeded с Retry-After, одинаково для существующей и
// несуществующей почты.
func TestLoginEmailLimitUniform(t *testing.T) {
	t.Parallel()
	e := newEnv(t, withPGLimiter(t))
	loginUser(t, e, "limited")
	ctx := humancheck.WithSolution(context.Background(), "решение") // PoW по риску решён — меряем только лимит почты
	for _, email := range []string{"limited@example.com", "ghost@example.com"} {
		for i := range 30 {
			if _, err := e.a.Login(ctx, email, "не тот", iphone); problemCode(err) != identity.CodeInvalidCredentials {
				t.Fatalf("%s %d: %v", email, i, err)
			}
		}
		_, err := e.a.Login(ctx, email, "secret", iphone)
		p := problemOf(t, err)
		if p.Status != 429 || p.Code != httpx.CodeRateLimited || p.RetryAfter < 1 {
			t.Fatalf("%s: %+v", email, p)
		}
	}
}

// §9.1: медианы 50 входов «нет почты» и «неверный пароль» на настоящем argon2 расходятся меньше чем на 15 %.
//
// serial: порог реального времени.
func TestLoginTiming(t *testing.T) {
	opt, h := withArgon(t)
	e := newEnv(t, opt)
	uid := seedUser(t, e.owner, "timing_login", "ru")
	hash, err := h.Hash(context.Background(), "настоящий пароль")
	if err != nil {
		t.Fatal(err)
	}
	setPassword(t, e, uid, hash)
	timed := func(email string) time.Duration {
		start := time.Now()
		if _, err := e.a.Login(context.Background(), email, "неверный пароль", iphone); problemCode(err) != identity.CodeInvalidCredentials {
			t.Fatal(err)
		}
		return time.Since(start)
	}
	var known, unknown []time.Duration
	for range 50 {
		known = append(known, timed("timing_login@example.com"))
		unknown = append(unknown, timed("nobody_timing@example.com"))
	}
	median := func(ds []time.Duration) time.Duration {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[len(ds)/2]
	}
	k, u := median(known), median(unknown)
	diff := float64(k-u) / float64(k)
	if diff < 0 {
		diff = -diff
	}
	if diff >= 0.15 {
		t.Fatalf("медианы: неверный пароль %v, нет почты %v — расхождение %.0f%%", k, u, diff*100)
	}
}

// m1 ревью W4-Б: documents_acceptance_required считается в транзакции входа — ответ после коммита базу не читает.
// Без согласий — true; текущие версии приняты — false.
func TestLoginDocumentsInResult(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := loginUser(t, e, "docs_login")
	out, err := e.a.Login(context.Background(), "docs_login@example.com", "secret", iphone)
	if err != nil || out.Status != StatusAuthenticated || !out.DocumentsRequired {
		t.Fatalf("без согласий: %+v %v", out, err)
	}
	for _, d := range []string{"terms", "privacy"} {
		if _, err := e.owner.Exec(context.Background(), `INSERT INTO consents (user_id, document, version, accepted_at, client_kind)
			VALUES ($1, $2, '2026-11-01', now(), 'mobile')`, uid, d); err != nil {
			t.Fatal(err)
		}
	}
	out, err = e.a.Login(context.Background(), "docs_login@example.com", "secret", iphone)
	if err != nil || out.Status != StatusAuthenticated || out.DocumentsRequired {
		t.Fatalf("текущие приняты: %+v %v", out, err)
	}
}

// brokenLimiter — хранилище лимитов недоступно: каждый вызов — ошибка.
type brokenLimiter struct{}

var errLimiterDown = errors.New("лимитер недоступен")

func (brokenLimiter) Allow(context.Context, string, ratelimit.Policy) (ratelimit.Result, error) {
	return ratelimit.Result{}, errLimiterDown
}
func (brokenLimiter) Add(context.Context, string, ratelimit.Policy) error { return errLimiterDown }
func (brokenLimiter) Over(context.Context, string, ratelimit.Policy) (bool, error) {
	return false, errLimiterDown
}

// m5 ревью W4-Б: одна политика сбоя лимитера и риска во входе — отказ (500) до поиска аккаунта и argon2, как у
// кодов и сброса пароля: ни сессии, ни журнала. Лимит почты и риск — каждый по отдельности.
func TestLoginLimiterFailureFailsClosed(t *testing.T) {
	t.Parallel()
	for name, opt := range map[string]envOpt{
		"лимит почты": func(d *identity.Deps) { d.Limiter = brokenLimiter{} },
		"риск": func(d *identity.Deps) {
			r, err := risk.NewAssessor(brokenLimiter{}, risk.DefaultPolicies())
			if err != nil {
				t.Fatal(err)
			}
			d.Risk = r
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, opt)
			uid := loginUser(t, e, "limit_down")
			_, err := e.a.Login(context.Background(), "limit_down@example.com", "secret", iphone)
			if !errors.Is(err, errLimiterDown) {
				t.Fatalf("err = %v", err)
			}
			if n := sessions(t, e, uid); n != 0 {
				t.Fatalf("сессий %d", n)
			}
			if n := count(t, e.owner, `SELECT count(*) FROM security_events WHERE user_id = $1`, uid); n != 0 {
				t.Fatalf("журнал %d", n)
			}
		})
	}
}

// m2 ревью 2: «документы нужно принять» — одно правило для входа и me: последняя принятая версия (accepted_at).
// terms принят в текущей 2026-11-01, позже — в другой версии (откат конфига): и вход, и me требуют принятия.
func TestDocumentsRequiredSameAsMe(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := loginUser(t, e, "docs_same")
	t1 := e.clock.Now().Add(-48 * time.Hour)
	for _, c := range []struct {
		doc, ver string
		at       time.Time
	}{
		{"terms", "2026-11-01", t1}, {"terms", "2026-12-01", t1.Add(time.Hour)}, {"privacy", "2026-11-01", t1},
	} {
		if _, err := e.owner.Exec(context.Background(), `INSERT INTO consents (user_id, document, version, accepted_at, client_kind)
			VALUES ($1, $2, $3, $4, 'mobile')`, uid, c.doc, c.ver, c.at); err != nil {
			t.Fatal(err)
		}
	}
	out, err := e.a.Login(context.Background(), "docs_same@example.com", "secret", iphone)
	if err != nil || out.Status != StatusAuthenticated {
		t.Fatalf("%+v %v", out, err)
	}
	me, err := e.a.me(context.Background(), identitydb.New(e.a.pool), uid)
	if err != nil {
		t.Fatal(err)
	}
	if !me.DocumentsAcceptanceRequired || out.DocumentsRequired != me.DocumentsAcceptanceRequired {
		t.Fatalf("вход %v, me %v — правила разошлись", out.DocumentsRequired, me.DocumentsAcceptanceRequired)
	}
	if req, err := e.a.DocumentsRequired(context.Background(), uid); err != nil || !req {
		t.Fatalf("DocumentsRequired: %v %v", req, err)
	}
}
