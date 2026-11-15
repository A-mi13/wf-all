package app

import (
	"bytes"
	"context"
	"encoding/base32"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/otp"
)

// freshSession — живая сессия аккаунта (issueSession ставит reauth_at = now, mfa_at — NULL).
func freshSession(t *testing.T, e *env, uid uuid.UUID) uuid.UUID {
	t.Helper()
	return issue(t, e, uid, web("mfa")).SessionID
}

// setupSecret — SetupTotp и сырой секрет из base32 ответа.
func setupSecret(t *testing.T, e *env, sid uuid.UUID) ([]byte, string) {
	t.Helper()
	b32, uri, err := e.a.SetupTotp(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32)
	if err != nil {
		t.Fatal(err)
	}
	return secret, uri
}

var recoveryShape = regexp.MustCompile(`^[a-z2-7]{5}-[a-z2-7]{5}$`)

// §3.3 «Включение»: секрет в базе только шифротекстом, uri — издатель и ник; неверный код — 400 mfa_invalid;
// верный — 10 кодов xxxxx-xxxxx (в базе HMAC), письмо, событие {enabled: true}, аудит без секретов, другие сессии
// живы; повтор setupTotp после включения — 409 mfa_already_enabled; confirm без начатой настройки — 409.
func TestSetupConfirmTotp(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "totp_on", "ru")
	issue(t, e, uid, web("other")) // другая сессия — включение 2FA её не гасит
	sid := freshSession(t, e, uid)
	if _, err := e.a.ConfirmTotp(ctx, uid, sid, "123456"); problemCode(err) != identity.CodeMfaSetupMissing {
		t.Fatalf("confirm без setup: %v", err)
	}
	secret, uri := setupSecret(t, e, sid)
	if !strings.HasPrefix(uri, "otpauth://totp/WF%20test:totp_on?") || !strings.Contains(uri, "issuer=WF%20test") ||
		!strings.Contains(uri, "secret="+otp.Base32(secret)) {
		t.Fatalf("uri: %s", uri)
	}
	var enc []byte
	var confirmed *time.Time
	if err := e.owner.QueryRow(ctx, `SELECT secret_enc, confirmed_at FROM user_mfa WHERE user_id = $1`, uid).Scan(&enc,
		&confirmed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, secret) || confirmed != nil {
		t.Fatalf("секрет открытым текстом или уже подтверждён: %v", confirmed)
	}
	if _, err := e.a.ConfirmTotp(ctx, uid, sid, wrongCode(totpNow(e, secret))); problemCode(err) != identity.CodeMfaInvalid {
		t.Fatalf("неверный код: %v", err)
	}
	codes, err := e.a.ConfirmTotp(ctx, uid, sid, totpNow(e, secret))
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("кодов %d", len(codes))
	}
	for _, c := range codes {
		if !recoveryShape.MatchString(c) {
			t.Fatalf("форма кода %q", c)
		}
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM user_mfa WHERE user_id = $1 AND confirmed_at IS NOT NULL AND last_used_step IS NOT NULL`:                 1,
		`SELECT count(*) FROM user_recovery_codes WHERE user_id = $1 AND used_at IS NULL AND octet_length(code_mac) = 32`:              10,
		`SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'mfa_enabled'`:                                             1,
		`SELECT count(*) FROM outbox WHERE event_type = 'identity.mfa_changed' AND aggregate_id = $1 AND payload->>'enabled' = 'true'`: 1,
		`SELECT count(*) FROM audit_log WHERE action = 'identity.mfa_enabled' AND object_id = $1`:                                      1,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`:                                                      2,
		`SELECT count(*) FROM river_job WHERE args->>'mail' = 'identity.mfa_enabled' AND $1::uuid IS NOT NULL`:                         1,
	} {
		if n := count(t, e.owner, q, uid); n != want {
			t.Errorf("%s: %d, ждали %d", q, n, want)
		}
	}
	// секреты и коды — ни в событии, ни в аудите
	var blob string
	if err := e.owner.QueryRow(ctx, `SELECT coalesce((SELECT string_agg(payload::text, '') FROM outbox), '') ||
		coalesce((SELECT string_agg(coalesce(before::text, '') || coalesce(after::text, ''), '') FROM audit_log), '')`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	for _, s := range append([]string{otp.Base32(secret)}, codes...) {
		if strings.Contains(blob, s) {
			t.Fatalf("секрет %q в событии или аудите", s)
		}
	}
	if _, _, err := e.a.SetupTotp(ctx, sid); problemCode(err) != identity.CodeMfaAlreadyEnabled {
		t.Fatalf("setup после включения: %v", err)
	}
	if _, err := e.a.ConfirmTotp(ctx, uid, sid, totpNow(e, secret)); problemCode(err) != identity.CodeMfaSetupMissing {
		t.Fatalf("confirm после включения: %v", err)
	}
	// несвежая сессия — 403 до любых изменений
	stale := staleSession(t, e, seedUser(t, e.owner, "totp_stale", "ru"))
	if _, _, err := e.a.SetupTotp(ctx, stale); problemCode(err) != identity.CodeReauthRequired {
		t.Fatalf("несвежая: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_mfa m JOIN sessions s ON s.user_id = m.user_id WHERE s.id = $1`, stale); n != 0 {
		t.Fatal("несвежая сессия записала секрет")
	}
}

// Повтор setupTotp до подтверждения выпускает новый секрет: код от первого не подходит.
func TestSetupTwiceReissues(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "totp_twice", "ru")
	sid := freshSession(t, e, uid)
	first, _ := setupSecret(t, e, sid)
	second, _ := setupSecret(t, e, sid)
	if bytes.Equal(first, second) {
		t.Fatal("тот же секрет")
	}
	if _, err := e.a.ConfirmTotp(context.Background(), uid, sid, totpNow(e, first)); problemCode(err) != identity.CodeMfaInvalid {
		t.Fatalf("код первого секрета: %v", err)
	}
	if _, err := e.a.ConfirmTotp(context.Background(), uid, sid, totpNow(e, second)); err != nil {
		t.Fatalf("код второго: %v", err)
	}
}

// Два confirmTotp разом с верным кодом: коды получает ровно один, второй — 409 mfa_setup_missing; кодов в базе 10.
func TestConfirmTotpParallel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "totp_race", "ru")
	sid := freshSession(t, e, uid)
	secret, _ := setupSecret(t, e, sid)
	code := totpNow(e, secret)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	got := make([][]string, 2)
	for i := range 2 {
		wg.Go(func() {
			<-start
			got[i], errs[i] = e.a.ConfirmTotp(context.Background(), uid, sid, code)
		})
	}
	close(start)
	wg.Wait()
	ok := 0
	for i := range 2 {
		switch {
		case errs[i] == nil && len(got[i]) == 10:
			ok++
		case problemCode(errs[i]) != identity.CodeMfaSetupMissing:
			t.Fatalf("второй: %v", errs[i])
		}
	}
	if ok != 1 {
		t.Fatalf("успехов %d", ok)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_recovery_codes WHERE user_id = $1`, uid); n != 10 {
		t.Fatalf("кодов в базе %d", n)
	}
}

// R27: reauth_at свежий, mfa_at старше 10 минут — 403 reauth_required; после reauthenticate TOTP — выключено (секрет
// и коды удалены, письмо, событие {enabled: false}, аудит); повтор — 409 mfa_not_enabled; без 2FA — 409, а не 403.
func TestDisableNeedsSecondFactorFresh(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "totp_off", "ru")
	secret := seedMfa(t, e, uid, "abcde-fghij")
	sid := freshSession(t, e, uid)
	if _, err := e.owner.Exec(ctx, `UPDATE sessions SET mfa_at = $2 WHERE id = $1`, sid,
		e.clock.Now().Add(-domain.ReauthFresh-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := e.a.DisableTotp(ctx, sid); problemCode(err) != identity.CodeReauthRequired {
		t.Fatalf("mfa_at старый: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_mfa WHERE user_id = $1`, uid); n != 1 {
		t.Fatal("выключено без второго фактора")
	}
	if _, err := e.a.Reauthenticate(ctx, sid, ReauthInput{TOTPCode: totpNow(e, secret)}); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(domain.ReauthFresh) // ровно 10 минут — ещё свежо
	if err := e.a.DisableTotp(ctx, sid); err != nil {
		t.Fatalf("выключение: %v", err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM user_mfa WHERE user_id = $1`:                                                                              0,
		`SELECT count(*) FROM user_recovery_codes WHERE user_id = $1`:                                                                   0,
		`SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'mfa_disabled'`:                                             1,
		`SELECT count(*) FROM outbox WHERE event_type = 'identity.mfa_changed' AND aggregate_id = $1 AND payload->>'enabled' = 'false'`: 1,
		`SELECT count(*) FROM audit_log WHERE action = 'identity.mfa_disabled' AND object_id = $1`:                                      1,
		`SELECT count(*) FROM river_job WHERE args->>'mail' = 'identity.mfa_disabled' AND $1::uuid IS NOT NULL`:                         1,
	} {
		if n := count(t, e.owner, q, uid); n != want {
			t.Errorf("%s: %d, ждали %d", q, n, want)
		}
	}
	if err := e.a.DisableTotp(ctx, sid); problemCode(err) != identity.CodeMfaNotEnabled {
		t.Fatalf("повтор: %v", err)
	}
	plain := seedUser(t, e.owner, "totp_off_plain", "ru")
	if err := e.a.DisableTotp(ctx, freshSession(t, e, plain)); problemCode(err) != identity.CodeMfaNotEnabled {
		t.Fatalf("без 2FA: %v", err)
	}
	if err := e.a.DisableTotp(ctx, staleSession(t, e, plain)); problemCode(err) != identity.CodeReauthRequired {
		t.Fatalf("несвежая: %v", err)
	}
}

// Перевыпуск: старые (и использованные) коды удалены, старый не проходит, новый — да; письмо; без 2FA — 409.
func TestRegenerateCodes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "codes_regen", "ru")
	seedMfa(t, e, uid, "abcde-fghij", "bcdef-ghijk")
	if _, err := verify(t, e, uid, SecondFactor{Recovery: "bcdef-ghijk"}); err != nil {
		t.Fatal(err)
	}
	sid := freshSession(t, e, uid)
	codes, err := e.a.RegenerateRecoveryCodes(ctx, sid)
	if err != nil || len(codes) != 10 {
		t.Fatalf("%v %d", err, len(codes))
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, uid); n != 10 {
		t.Fatalf("живых кодов %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_recovery_codes WHERE user_id = $1`, uid); n != 10 {
		t.Fatalf("старые не удалены: %d", n)
	}
	if _, err := verify(t, e, uid, SecondFactor{Recovery: "abcde-fghij"}); err == nil {
		t.Fatal("старый код прошёл")
	}
	if m, err := verify(t, e, uid, SecondFactor{Recovery: strings.ToUpper(codes[3])}); err != nil || m != FactorRecovery {
		t.Fatalf("новый код: %v", err)
	}
	if n := mailJobs(t, e, MailRecoveryCodesRegenerated); n != 1 {
		t.Fatalf("писем %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'recovery_codes_regenerated'`, uid); n != 1 {
		t.Fatal("журнал")
	}
	plain := seedUser(t, e.owner, "codes_plain", "ru")
	if _, err := e.a.RegenerateRecoveryCodes(ctx, freshSession(t, e, plain)); problemCode(err) != identity.CodeMfaNotEnabled {
		t.Fatalf("без 2FA: %v", err)
	}
	if _, err := e.a.RegenerateRecoveryCodes(ctx, staleSession(t, e, uid)); problemCode(err) != identity.CodeReauthRequired {
		t.Fatalf("несвежая: %v", err)
	}
}

// getMyMfa: выключено — нули; начатая настройка — всё ещё выключено; включено — confirmed_at и остаток кодов;
// живой сброс поддержкой — reset_scheduled_at.
func TestMfaStatus(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "mfa_status", "ru")
	st, err := e.a.MfaStatus(ctx, uid)
	if err != nil || st.TOTPEnabled || st.ConfirmedAt != nil || st.RecoveryCodesLeft != 0 || st.ResetScheduledAt != nil {
		t.Fatalf("выключено: %+v %v", st, err)
	}
	setupSecret(t, e, freshSession(t, e, uid))
	if st, err = e.a.MfaStatus(ctx, uid); err != nil || st.TOTPEnabled || st.ConfirmedAt != nil {
		t.Fatalf("начатая настройка: %+v %v", st, err)
	}
	on := seedUser(t, e.owner, "mfa_status_on", "ru")
	seedMfa(t, e, on, "abcde-fghij", "bcdef-ghijk")
	if _, err := verify(t, e, on, SecondFactor{Recovery: "abcde-fghij"}); err != nil {
		t.Fatal(err)
	}
	at := e.clock.Now().Add(domain.MfaResetDelay)
	if _, err := e.owner.Exec(ctx, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label,
		reason, execute_at, created_at) VALUES ($1, $2, $3, $4, 'Оператор', 'потерял телефон', $5, now())`,
		uuid.New(), on, uuid.New(), uuid.New(), at); err != nil {
		t.Fatal(err)
	}
	st, err = e.a.MfaStatus(ctx, on)
	if err != nil || !st.TOTPEnabled || st.ConfirmedAt == nil || st.RecoveryCodesLeft != 1 || st.ResetScheduledAt == nil ||
		!st.ResetScheduledAt.Equal(at) {
		t.Fatalf("включено: %+v %v", st, err)
	}
}

// I1 ревью W5-Б: выключение 2FA (свежий второй фактор) отменяет живой сброс владельцем — иначе воркер через 7 дней
// стёр бы новую 2FA, включённую после выключения. mfa_at ставится напрямую: свежий вход вторым фактором сам отменяет
// сброс (TestReauthSecondFactorCancelsReset) и замаскировал бы отмену в DisableTotp.
func TestDisableTotpCancelsReset(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	w := roleApp(t, r.env, "worker", true)
	ctx := context.Background()
	uid := seedUser(t, r.owner, "disable_cancel", "ru")
	seedMfa(t, r.env, uid)
	rid := r.liveReset(t, uid)
	sid := freshSession(t, r.env, uid)
	acctExec(t, r.env, `UPDATE sessions SET mfa_at = $2 WHERE id = $1`, sid, r.clock.Now())
	if err := r.a.DisableTotp(ctx, sid); err != nil {
		t.Fatal(err)
	}
	ownerCancelled(t, r.env, uid, rid)
	secret, _ := setupSecret(t, r.env, sid)
	if _, err := r.a.ConfirmTotp(ctx, uid, sid, totpNow(r.env, secret)); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(domain.MfaResetDelay)
	if n, _, err := w.ExecuteDueMfaResets(ctx, 10, nil); err != nil || n != 0 {
		t.Fatalf("воркер: %d %v", n, err)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM user_mfa WHERE user_id = $1 AND confirmed_at IS NOT NULL`, uid); n != 1 {
		t.Fatal("новая 2FA снята")
	}
	if n := count(t, r.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, sid); n != 1 {
		t.Fatal("сессия отозвана")
	}
}

// m1 ревью W5-А: confirmTotp перечитывает сессию запроса под замком аккаунта — отозванная (кэш загрузчика ≤ 5 с ещё
// пропускает её) получает 401 без кодов и без включения.
func TestConfirmTotpRevokedSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "confirm_revoked", "ru")
	sid := freshSession(t, e, uid)
	secret, _ := setupSecret(t, e, sid)
	other := freshSession(t, e, seedUser(t, e.owner, "confirm_other", "ru"))
	if _, err := e.a.ConfirmTotp(ctx, uid, other, totpNow(e, secret)); problemCode(err) != httpx.CodeUnauthenticated {
		t.Fatalf("чужая сессия: %v", err)
	}
	if err := e.a.RevokeSession(ctx, uid, sid, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.ConfirmTotp(ctx, uid, sid, totpNow(e, secret)); problemCode(err) != httpx.CodeUnauthenticated {
		t.Fatalf("отозванная: %v", err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM user_mfa WHERE user_id = $1 AND confirmed_at IS NULL`: 1,
		`SELECT count(*) FROM user_recovery_codes WHERE user_id = $1`:               0,
	} {
		if n := count(t, e.owner, q, uid); n != want {
			t.Errorf("%s: %d, ждали %d", q, n, want)
		}
	}
}
