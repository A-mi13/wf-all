package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/httpx"
)

// staleSession — сессия аккаунта с reauth_at старше окна свежести.
func staleSession(t *testing.T, e *env, uid uuid.UUID) uuid.UUID {
	t.Helper()
	s := issue(t, e, uid, web("reauth"))
	if _, err := e.owner.Exec(context.Background(), `UPDATE sessions SET reauth_at = $2 WHERE id = $1`, s.SessionID,
		e.clock.Now().Add(-domain.ReauthFresh-time.Second)); err != nil {
		t.Fatal(err)
	}
	return s.SessionID
}

// §3.13: без 2FA пароль — ок; с 2FA пароль — 422 reauth_method_not_allowed (без засчитанной неудачи); TOTP — ок и
// mfa_at = now; резервный код — сгорел; два поля или ни одного — validation.failed; passkey и social — reauth_invalid.
func TestReauthMethods(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	plain := seedUser(t, e.owner, "reauth_plain", "ru")
	setPassword(t, e, plain, "h:secret")
	sid := staleSession(t, e, plain)
	until, err := e.a.Reauthenticate(ctx, sid, ReauthInput{Password: "secret"})
	if err != nil || !until.Equal(e.clock.Now().Add(domain.ReauthFresh)) {
		t.Fatalf("пароль без 2FA: %v %v", until, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND reauth_at = $2 AND mfa_at IS NULL`, sid,
		e.clock.Now()); n != 1 {
		t.Fatal("reauth_at не обновлён")
	}
	strong := seedUser(t, e.owner, "reauth_strong", "ru")
	setPassword(t, e, strong, "h:secret")
	secret := seedMfa(t, e, strong, "abcde-fghij")
	ssid := staleSession(t, e, strong)
	if _, err := e.a.Reauthenticate(ctx, ssid, ReauthInput{Password: "secret"}); problemCode(err) != identity.CodeReauthMethodNotAllowed {
		t.Fatalf("пароль при 2FA: %v", err)
	}
	if n := count(t, e.owner, `SELECT reauth_failures FROM sessions WHERE id = $1`, ssid); n != 0 {
		t.Fatalf("пароль при 2FA засчитан неудачей: %d", n)
	}
	if _, err := e.a.Reauthenticate(ctx, ssid, ReauthInput{TOTPCode: totpNow(e, secret)}); err != nil {
		t.Fatalf("TOTP: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND mfa_at = $2 AND reauth_at = $2`, ssid,
		e.clock.Now()); n != 1 {
		t.Fatal("TOTP: mfa_at не поставлен")
	}
	if _, err := e.a.Reauthenticate(ctx, ssid, ReauthInput{RecoveryCode: "abcde-fghij"}); err != nil {
		t.Fatalf("резервный код: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_recovery_codes WHERE user_id = $1 AND used_at IS NOT NULL`, strong); n != 1 {
		t.Fatal("резервный код не сгорел")
	}
	for name, in := range map[string]ReauthInput{"ни одного": {}, "два поля": {Password: "secret", TOTPCode: "123456"}} {
		if _, err := e.a.Reauthenticate(ctx, sid, in); problemCode(err) != httpx.CodeValidationFailed {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for name, in := range map[string]ReauthInput{"passkey": {Passkey: true}, "social": {Social: true}} {
		_, err := e.a.Reauthenticate(ctx, sid, in)
		if p := problemOfErr(err); p.Code != identity.CodeReauthInvalid || p.AttemptsLeft == nil {
			t.Fatalf("%s: %+v", name, p)
		}
	}
	if _, err := e.a.Reauthenticate(ctx, uuid.New(), ReauthInput{Password: "secret"}); problemCode(err) != httpx.CodeUnauthenticated {
		t.Fatalf("нет сессии: %v", err)
	}
}

// R16, I8: 5 неудач — 429 с Retry-After до конца окна; через 15 мин 1 с от первой неудачи — счёт с нуля
// (attempts_left = 4 после новой неудачи); успех обнуляет счёт.
func TestReauthWindow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "reauth_window", "ru")
	setPassword(t, e, uid, "h:secret")
	sid := staleSession(t, e, uid)
	first := e.clock.Now()
	for i := range domain.ReauthFailures {
		_, err := e.a.Reauthenticate(ctx, sid, ReauthInput{Password: "wrong"})
		if p := problemOfErr(err); p.Code != identity.CodeReauthInvalid || p.AttemptsLeft == nil || *p.AttemptsLeft != 4-i {
			t.Fatalf("неудача %d: %+v", i+1, p)
		}
		e.clock.Advance(time.Minute)
	}
	_, err := e.a.Reauthenticate(ctx, sid, ReauthInput{Password: "secret"})
	if p := problemOfErr(err); p.Code != httpx.CodeRateLimited || p.RetryAfter != int((domain.ReauthFailuresWindow-5*time.Minute)/time.Second) {
		t.Fatalf("шестая: %+v", p)
	}
	e.clock.Set(first.Add(domain.ReauthFailuresWindow + time.Second))
	_, err = e.a.Reauthenticate(ctx, sid, ReauthInput{Password: "wrong"})
	if p := problemOfErr(err); p.Code != identity.CodeReauthInvalid || p.AttemptsLeft == nil || *p.AttemptsLeft != 4 {
		t.Fatalf("после окна: %+v", p)
	}
	if _, err := e.a.Reauthenticate(ctx, sid, ReauthInput{Password: "secret"}); err != nil {
		t.Fatalf("успех: %v", err)
	}
	if n := count(t, e.owner, `SELECT reauth_failures FROM sessions WHERE id = $1 AND reauth_failed_at IS NULL`, sid); n != 0 {
		t.Fatalf("успех не обнулил счёт: %d", n)
	}
}

// §3.10: reauth_at старше 10 минут — 403 reauth_required, ничего не изменено; свежий — пароль сменён, другие сессии
// отозваны, текущая жива; письмо, событие, аудит. Аккаунт без пароля — пароль задан.
func TestChangePasswordNeedsFresh(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "change_pw", "ru")
	setPassword(t, e, uid, "h:old")
	other := issue(t, e, uid, web("other"))
	stale := staleSession(t, e, uid)
	if err := e.a.ChangePassword(ctx, stale, resetNewPassword, web("req")); problemCode(err) != identity.CodeReauthRequired {
		t.Fatalf("несвежая: %v", err)
	}
	if passwordHash(t, e, uid) != "h:old" || sessions(t, e, uid) != count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid) {
		t.Fatal("несвежая: изменения")
	}
	if err := e.a.ChangePassword(ctx, stale, "short", web("req")); problemCode(err) != identity.CodePasswordWeak {
		t.Fatalf("слабый: %v", err)
	}
	cur := issue(t, e, uid, web("current"))
	if err := e.a.ChangePassword(ctx, cur.SessionID, resetNewPassword, web("req")); err != nil {
		t.Fatal(err)
	}
	if passwordHash(t, e, uid) != "h:"+resetNewPassword {
		t.Fatal("пароль не сменён")
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_reason = 'password_changed'`:                  2,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`:                                   1,
		`SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'password_changed'`:                     1,
		`SELECT count(*) FROM outbox WHERE event_type = 'identity.password_changed' AND aggregate_id = $1`:          1,
		`SELECT count(*) FROM audit_log WHERE action = 'identity.password_changed' AND actor_user_id = $1`:          1,
		`SELECT count(*) FROM river_job WHERE args->>'mail' = 'identity.password_changed' AND $1::uuid IS NOT NULL`: 1,
	} {
		if n := count(t, e.owner, q, uid); n != want {
			t.Errorf("%s: %d, ждали %d", q, n, want)
		}
	}
	_ = other
	social := seedUser(t, e.owner, "change_social", "ru")
	s := issue(t, e, social, web("s"))
	if err := e.a.ChangePassword(ctx, s.SessionID, resetNewPassword, web("req")); err != nil || passwordHash(t, e, social) != "h:"+resetNewPassword {
		t.Fatalf("без пароля: %v", err)
	}
}

// I1 ревью W5-Б: свежий вход вторым фактором доказывает, что факторы при владельце, — живой сброс 2FA поддержкой
// отменяется в той же транзакции (§3.15 п. 4); неудачная попытка сброс не трогает.
func TestReauthSecondFactorCancelsReset(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	ctx := context.Background()
	uid := seedUser(t, r.owner, "reauth_cancel", "ru")
	secret := seedMfa(t, r.env, uid, "abcde-fghij")
	rid := r.liveReset(t, uid)
	sid := staleSession(t, r.env, uid)
	if _, err := r.a.Reauthenticate(ctx, sid, ReauthInput{TOTPCode: wrongCode(totpNow(r.env, secret))}); problemCode(err) != identity.CodeReauthInvalid {
		t.Fatalf("неверный TOTP: %v", err)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_at IS NULL`, rid); n != 1 {
		t.Fatal("неудача отменила сброс")
	}
	if _, err := r.a.Reauthenticate(ctx, sid, ReauthInput{RecoveryCode: "abcde-fghij"}); err != nil {
		t.Fatal(err)
	}
	ownerCancelled(t, r.env, uid, rid)
}
