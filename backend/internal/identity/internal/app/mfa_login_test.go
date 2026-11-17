package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/httpx"
)

// mfaLogin — аккаунт с паролем «secret» и 2FA; вход паролем — mfa_token.
func mfaLogin(t *testing.T, e *env, nick string, recovery ...string) (uuid.UUID, []byte, string) {
	t.Helper()
	uid := loginUser(t, e, nick)
	secret := seedMfa(t, e, uid, recovery...)
	return uid, secret, passwordStep(t, e, nick)
}

// passwordStep — новый вход паролем аккаунта с 2FA.
func passwordStep(t *testing.T, e *env, nick string) string {
	t.Helper()
	out, err := e.a.Login(context.Background(), nick+"@example.com", "secret", iphone)
	if err != nil || out.Status != StatusMfaRequired || out.MfaToken == "" {
		t.Fatalf("вход: %+v %v", out, err)
	}
	return out.MfaToken
}

func complete(e *env, tok string, f SecondFactor) (AuthOutcome, error) {
	return e.a.CompleteMfa(context.Background(), tok, f, nil, iphone)
}

// §3.3: login → mfa_required → completeMfa TOTP → сессия (mfa_at = now, способ первого шага — password, документы
// посчитаны); резервным кодом — письмо recovery_code_used; без device у мобильного — 422; форма — ровно одно поле;
// passkey в I-2 — 400 passkey_invalid.
func TestCompleteMfaFlow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, secret, tok := mfaLogin(t, e, "mfa_flow", "abcde-fghij")
	if _, err := e.a.CompleteMfa(context.Background(), tok, SecondFactor{TOTP: totpNow(e, secret)}, nil,
		Client{Kind: domain.ClientMobile}); problemCode(err) != identity.CodeDeviceRequired {
		t.Fatalf("без device: %v", err)
	}
	for name, f := range map[string]SecondFactor{"ни одного": {}, "два": {TOTP: "123456", Recovery: "abcde-fghij"}} {
		if _, err := complete(e, tok, f); problemCode(err) != httpx.CodeValidationFailed {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := e.a.CompleteMfa(context.Background(), tok, SecondFactor{TOTP: "123456"}, &PasskeyInput{}, iphone); problemCode(err) != httpx.CodeValidationFailed {
		t.Fatalf("код и passkey: %v", err)
	}
	if _, err := e.a.CompleteMfa(context.Background(), tok, SecondFactor{}, &PasskeyInput{}, iphone); problemCode(err) != identity.CodePasskeyInvalid {
		t.Fatalf("passkey: %v", err)
	}
	out, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)})
	if err != nil || out.Status != StatusAuthenticated || out.Issued == nil || out.User.ID != uid || !out.DocumentsRequired {
		t.Fatalf("TOTP: %+v %v", out, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND mfa_at = $2 AND auth_method = 'password'
		AND device_id IS NOT NULL`, out.Issued.SessionID, e.clock.Now()); n != 1 {
		t.Fatal("сессия: mfa_at, способ или устройство")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_challenges WHERE user_id = $1 AND consumed_at IS NOT NULL`, uid); n != 1 {
		t.Fatal("mfa_token не погашен")
	}
	tok2 := passwordStep(t, e, "mfa_flow")
	out, err = complete(e, tok2, SecondFactor{Recovery: "ABCDE FGHIJ"})
	if err != nil || out.Status != StatusAuthenticated {
		t.Fatalf("резервный код: %+v %v", out, err)
	}
	if n := mailJobs(t, e, MailRecoveryCodeUsed); n != 1 {
		t.Fatalf("писем recovery_code_used: %d", n)
	}
}

// 4 неверных — attempts_left 4…1, пятый — 410 (токен погашен); тот же токен после успеха — 410; ровно через 5 минут —
// 410. Неудачи коммитятся и при отменённом ctx запроса.
func TestCompleteMfaAttempts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, secret, tok := mfaLogin(t, e, "mfa_attempts")
	bad := SecondFactor{TOTP: wrongCode(totpNow(e, secret))}
	for want := 4; want >= 1; want-- {
		_, err := complete(e, tok, bad)
		if p := problemOfErr(err); p.Code != identity.CodeMfaInvalid || p.Status != 400 || p.AttemptsLeft == nil || *p.AttemptsLeft != want {
			t.Fatalf("ждали attempts_left %d: %+v", want, p)
		}
	}
	if _, err := complete(e, tok, bad); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("пятая: %v", err)
	}
	if _, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("верный код после пятой: %v", err)
	}
	tok = passwordStep(t, e, "mfa_attempts")
	if _, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)}); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(otpStep)
	if _, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("повтор после успеха: %v", err)
	}
	tok = passwordStep(t, e, "mfa_attempts")
	e.clock.Advance(5 * time.Minute)
	if _, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("ровно 5 минут: %v", err)
	}
	// отмена клиента не делает попытку бесплатной
	tok = passwordStep(t, e, "mfa_attempts")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.a.CompleteMfa(ctx, tok, bad, nil, iphone); problemCode(err) != identity.CodeMfaInvalid {
		t.Fatalf("отменённый ctx: %v", err)
	}
	if n := count(t, e.owner, `SELECT attempts FROM auth_challenges WHERE consumed_at IS NULL AND expires_at > $1`, e.clock.Now()); n != 1 {
		t.Fatalf("попытка при отмене не засчитана: %d", n)
	}
}

// otpStep — шаг TOTP: новый код после успеха (last_used_step отвергает тот же шаг).
const otpStep = 30 * time.Second

// R16: 10 неудач второго фактора за час на разных mfa_token — дальше 429 с Retry-After до проверки фактора.
func TestCompleteMfaUserLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, pgLimiters)
	uid, secret, tok := mfaLogin(t, e, "mfa_limit")
	bad := SecondFactor{TOTP: wrongCode(totpNow(e, secret))}
	for i := range 10 {
		if i == 5 {
			tok = passwordStep(t, e, "mfa_limit")
		}
		if _, err := complete(e, tok, bad); err == nil {
			t.Fatalf("неудача %d прошла", i+1)
		}
	}
	tok = passwordStep(t, e, "mfa_limit")
	_, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)})
	if p := problemOfErr(err); p.Code != httpx.CodeRateLimited || p.Status != 429 || p.RetryAfter < 1 {
		t.Fatalf("11-я: %+v", p)
	}
	if n := count(t, e.owner, `SELECT failures FROM user_mfa WHERE user_id = $1`, uid); n != 10 {
		t.Fatalf("429 засчитан неудачей фактора: %d", n)
	}
}

// I1 итогового ревью: user_mfa.failures — smallint; без насыщения неудача на 32767 давала 22003 и откат tx — лимит
// user:mfa, попытка mfa_token и письмо mfa_failures не тратились (перебор без учёта). Счётчик насыщается.
func TestCompleteMfaFailuresSaturate(t *testing.T) {
	t.Parallel()
	e := newEnv(t, pgLimiters)
	uid, secret, tok := mfaLogin(t, e, "mfa_saturate")
	acctExec(t, e, `UPDATE user_mfa SET failures = 32767 WHERE user_id = $1`, uid)
	_, err := complete(e, tok, SecondFactor{TOTP: wrongCode(totpNow(e, secret))})
	if p := problemOfErr(err); p.Code != identity.CodeMfaInvalid || p.AttemptsLeft == nil || *p.AttemptsLeft != 4 {
		t.Fatalf("неудача на пределе счётчика: %+v %v", p, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM rate_limits WHERE key = $1`, mfaFailureKey(uid)); n != 1 {
		t.Fatal("неудача не учтена в лимите user:mfa")
	}
	if n := count(t, e.owner, `SELECT failures FROM user_mfa WHERE user_id = $1`, uid); n != 32767 {
		t.Fatalf("счётчик: %d", n)
	}
	if n := mailJobs(t, e, MailMfaFailures); n != 1 {
		t.Fatalf("писем mfa_failures: %d", n)
	}
}

// §3.11, R6: аккаунт в периоде удаления после второго фактора — deletion_pending с restore_token, без сессии.
func TestCompleteMfaPendingDeletion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, secret, tok := mfaLogin(t, e, "mfa_pending")
	setStatus(t, e, uid, "pending_deletion")
	out, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)})
	if err != nil || out.Status != StatusDeletionPending || out.RestoreToken == "" || out.Issued != nil {
		t.Fatalf("%+v %v", out, err)
	}
	if n := sessions(t, e, uid); n != 0 {
		t.Fatalf("сессий %d", n)
	}
}

// R29: бан между login и completeMfa — 410 mfa_expired до проверки фактора (failures не растёт, письма нет), токен
// погашен: повтор — 410.
func TestCompleteMfaBannedBurnsToken(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, secret, tok := mfaLogin(t, e, "mfa_banned")
	setStatus(t, e, uid, "banned")
	// бан по R42 — со строкой login (без неё бан истёк, R12)
	acctExec(t, e, `INSERT INTO user_restrictions (id, user_id, capability, source_id, created_at)
		VALUES (gen_random_uuid(), $1, 'login', gen_random_uuid(), now())`, uid)
	if _, err := complete(e, tok, SecondFactor{TOTP: wrongCode(totpNow(e, secret))}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("бан: %v", err)
	}
	if n := count(t, e.owner, `SELECT failures FROM user_mfa WHERE user_id = $1`, uid); n != 0 {
		t.Fatalf("failures: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_challenges WHERE user_id = $1 AND consumed_at IS NOT NULL`, uid); n != 1 {
		t.Fatal("токен не погашен")
	}
	setStatus(t, e, uid, "active")
	if _, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("повтор: %v", err)
	}
}

// Два completeMfa разом одним токеном и верными способами (TOTP и резервный код) — сессия ровно одна, второй — 410.
func TestCompleteMfaParallel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, secret, tok := mfaLogin(t, e, "mfa_race", "abcde-fghij")
	fs := []SecondFactor{{TOTP: totpNow(e, secret)}, {Recovery: "abcde-fghij"}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			<-start
			_, errs[i] = complete(e, tok, fs[i])
		})
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case problemCode(err) != identity.CodeMfaExpired:
			t.Fatalf("второй: %v", err)
		}
	}
	if ok != 1 || sessions(t, e, uid) != 1 {
		t.Fatalf("успехов %d, сессий %d", ok, sessions(t, e, uid))
	}
}

// m2 ревью W5-А: mfa_token привязан к клиенту первого шага — другой вид клиента, другое устройство мобильного или
// другая платформа с тем же install_id — 410 mfa_expired без записей (попытка не тратится, фактор не проверяется:
// нет оракула кода на чужом клиенте); с исходного устройства токен по-прежнему работает.
func TestCompleteMfaClientMismatch(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, secret, tok := mfaLogin(t, e, "mfa_client")
	upsert(t, e, mobile("ios", "11111111-2222-3333-4444-555555555555")) // чужое устройство уже известно базе
	for name, c := range map[string]Client{
		"веб":               web("a"),
		"другое устройство": mobile("ios", "11111111-2222-3333-4444-555555555555"),
		"другая платформа":  mobile("android", "6F9619FF-8B86-4D01-B42D-00CF4FC964FF"),
	} {
		if _, err := e.a.CompleteMfa(context.Background(), tok, SecondFactor{TOTP: totpNow(e, secret)}, nil, c); problemCode(err) != identity.CodeMfaExpired {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_challenges WHERE user_id = $1 AND attempts = 0 AND consumed_at IS NULL`, uid); n != 1 {
		t.Fatal("несовпадение клиента потратило попытку или погасило токен")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1`, uid); n != 0 {
		t.Fatal("сессия на чужом клиенте")
	}
	if out, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)}); err != nil || out.Status != StatusAuthenticated {
		t.Fatalf("исходное устройство: %+v %v", out, err)
	}
}

// Непредусмотренное 3 ревью W5-Б: между первым шагом и completeMfa воркер исполнил сброс 2FA (user_mfa нет) — токен
// гасится, 410 mfa_expired (клиент начинает вход заново и проходит без второго шага), не 400 и не 500; попытка и
// лимит user:mfa не тратятся.
func TestCompleteMfaAfterResetExecuted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, secret, tok := mfaLogin(t, e, "mfa_reset_done", "abcde-fghij")
	acctExec(t, e, `DELETE FROM user_recovery_codes WHERE user_id = $1`, uid)
	acctExec(t, e, `DELETE FROM user_mfa WHERE user_id = $1`, uid)
	if _, err := complete(e, tok, SecondFactor{TOTP: totpNow(e, secret)}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("после сброса: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_challenges WHERE user_id = $1 AND attempts = 0 AND consumed_at IS NOT NULL`, uid); n != 1 {
		t.Fatal("токен не погашен или попытка потрачена")
	}
	if _, err := complete(e, tok, SecondFactor{Recovery: "abcde-fghij"}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("повтор: %v", err)
	}
	if out, err := e.a.Login(context.Background(), "mfa_reset_done@example.com", "secret", iphone); err != nil || out.Status != StatusAuthenticated {
		t.Fatalf("вход заново: %+v %v", out, err)
	}
}
