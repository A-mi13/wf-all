package app_test

import (
	"context"
	"encoding/base32"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/domain"
)

// decodeSecret — секрет TOTP из base32 ответа MFASetup.
func decodeSecret(t *testing.T, b32 string) []byte {
	t.Helper()
	s, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32)
	if err != nil {
		t.Fatalf("секрет %q: %v", b32, err)
	}
	return s
}

// completeSetup — настройка 2FA сессии setup до конца (setup → confirm → ack): сотрудник с секретом
// и резервными кодами и токен активной сессии.
func (e *env) completeSetup(s *auth.Staff) (member, string) {
	e.t.Helper()
	ctx := context.Background()
	b32, _, err := e.app.MFASetup(ctx, s)
	if err != nil {
		e.t.Fatalf("MFASetup: %v", err)
	}
	m := member{ID: s.StaffID, Secret: decodeSecret(e.t, b32)}
	if m.Codes, err = e.app.MFAConfirm(ctx, s, e.totp(m), metaWeb); err != nil {
		e.t.Fatalf("MFAConfirm: %v", err)
	}
	tok, err := e.app.RecoveryAck(ctx, s, metaWeb)
	if err != nil {
		e.t.Fatalf("RecoveryAck: %v", err)
	}
	return m, tok
}

// setupSession — принятое приглашение: сотрудник invited и его сессия setup.
func (e *env) setupSession(email string) (member, *auth.Staff) {
	e.t.Helper()
	m, code := e.invitee(email, memberOpts{Role: "support", Name: "Иван П."}, purposeInvite)
	_, s := e.accept(m, code)
	return m, s
}

// MFASetup (§4.5): секрет 20 байт в base32 и otpauth-ссылка (издатель — хост ADMIN_ORIGIN, аккаунт —
// почта); повторный вызов до подтверждения выпускает новый секрет; только стадия setup.
func TestMFASetup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, s := e.setupSession("ivan.p@wf.example")
	b1, uri, err := e.app.MFASetup(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(decodeSecret(t, b1)) != domain.TOTPSecretBytes {
		t.Fatalf("секрет %q", b1)
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/admin.wf.example:"+m.Email ||
		u.Query().Get("secret") != b1 || u.Query().Get("issuer") != "admin.wf.example" {
		t.Fatalf("ссылка %q", uri)
	}
	b2, _, err := e.app.MFASetup(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if b2 == b1 {
		t.Fatal("повторный вызов не сменил секрет")
	}
	if n := e.count(`SELECT count(*) FROM staff_mfa WHERE staff_id = $1 AND confirmed_at IS NULL AND key_id = $2`, m.ID, e.ring.CurrentID()); n != 1 {
		t.Fatal("строка staff_mfa")
	}
	// действует последний секрет
	old := member{Secret: decodeSecret(t, b1)}
	if _, err := e.app.MFAConfirm(ctx, s, e.totp(old), metaWeb); !errors.Is(err, staff.ErrMFAInvalid) {
		t.Fatalf("код прежнего секрета: %v", err)
	}
	m.Secret = decodeSecret(t, b2)
	if _, err := e.app.MFAConfirm(ctx, s, e.totp(m), metaWeb); err != nil {
		t.Fatal(err)
	}
	// после подтверждения секрет не перевыпускается
	if _, _, err := e.app.MFASetup(ctx, s); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("после confirm: %v", err)
	}
	// не стадия setup
	active := e.addMember("active@wf.example", memberOpts{})
	if _, _, err := e.app.MFASetup(ctx, e.load(e.signIn(active))); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("сессия active: %v", err)
	}
}

// MFAConfirm: неверный код — staff.mfa_invalid без attempts_left, счётчика и блокировки; до
// MFASetup и повторно после подтверждения — wrong_stage; успех — 10 кодов, confirmed_at и принятый
// шаг в last_used_step.
func TestMFAConfirm(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, s := e.setupSession("ivan.p@wf.example")
	if _, err := e.app.MFAConfirm(ctx, s, "123456", metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("до MFASetup: %v", err)
	}
	b32, _, err := e.app.MFASetup(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	m.Secret = decodeSecret(t, b32)
	wrong := "000000"
	if e.totp(m) == wrong {
		wrong = "111111"
	}
	for range 7 {
		_, err := e.app.MFAConfirm(ctx, s, wrong, metaWeb)
		if !errors.Is(err, staff.ErrMFAInvalid) {
			t.Fatalf("неверный код: %v", err)
		}
		if _, ok := errors.AsType[*staff.MFAError](err); ok {
			t.Fatal("attempts_left в настройке")
		}
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND mfa_failures = 0 AND locked_until IS NULL`, m.ID); n != 1 {
		t.Fatal("неудача настройки посчитана")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action IN ('staff.factor_failed', 'staff.login_failed', 'staff.locked')`); n != 0 {
		t.Fatalf("аудит неудач %d", n)
	}
	codes, err := e.app.MFAConfirm(ctx, s, e.totp(m), metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 || len(slices.Compact(slices.Sorted(slices.Values(codes)))) != 10 {
		t.Fatalf("коды %v", codes)
	}
	for _, c := range codes {
		if _, ok := domain.NormalizeRecoveryCode(c); !ok || len(c) != 11 || c[5] != '-' {
			t.Fatalf("код %q", c)
		}
	}
	step := domain.TOTPStep(e.clock.Now())
	if n := e.count(`SELECT count(*) FROM staff_mfa WHERE staff_id = $1 AND confirmed_at = $2 AND last_used_step = $3`, m.ID, t0, step); n != 1 {
		t.Fatal("confirmed_at или last_used_step")
	}
	if n := e.count(`SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1 AND used_at IS NULL AND key_id = $2`, m.ID, e.ring.CurrentID()); n != 10 {
		t.Fatalf("кодов в базе %d", n)
	}
	e.clock.Advance(30 * time.Second)
	if _, err := e.app.MFAConfirm(ctx, s, e.totp(m), metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("повтор после подтверждения: %v", err)
	}
}

// RecoveryAck (§4.5, D9): без подтверждённого TOTP — wrong_stage; успех — новый токен активной
// сессии (старый — replaced), учётка active, приглашение погашено, mfa_at; аудит staff.mfa_enabled.
// Коды из confirm принимает вход 6a.
func TestRecoveryAck(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support", Name: "Иван П."}, purposeInvite)
	setupTok, s := e.accept(m, code)
	if _, err := e.app.RecoveryAck(ctx, s, metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("без секрета: %v", err)
	}
	b32, _, err := e.app.MFASetup(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.RecoveryAck(ctx, s, metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("без подтверждения: %v", err)
	}
	m.Secret = decodeSecret(t, b32)
	if m.Codes, err = e.app.MFAConfirm(ctx, s, e.totp(m), metaWeb); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(2 * time.Minute)
	tok, err := e.app.RecoveryAck(ctx, s, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if tok == setupTok {
		t.Fatal("токен не сменился")
	}
	if _, err := e.app.LoadStaff(ctx, setupTok, ipHome, false); !errors.Is(err, auth.ErrSessionReplaced) {
		t.Fatalf("старый токен: %v", err)
	}
	if e.revokedReason(s.SessionID) != "replaced" {
		t.Fatal("старая сессия не replaced")
	}
	active := e.load(tok)
	if active.Stage != auth.StageActive || active.Reauth || len(active.Grants) == 0 {
		t.Fatalf("сессия %+v", active)
	}
	now := e.clock.Now()
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE id = $1 AND mfa_at = $2 AND created_at = $3 AND expires_at = $4`,
		active.SessionID, now, t0, t0.Add(8*time.Hour)); n != 1 {
		t.Fatal("mfa_at или срок сессии")
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND status = 'active' AND last_login_at = $2`, m.ID, now); n != 1 {
		t.Fatal("учётка не active")
	}
	all := e.invites(m.ID)
	if len(all) != 1 || all[0].Live || all[0].HasCode ||
		e.count(`SELECT count(*) FROM staff_invites WHERE staff_id = $1 AND consumed_at = $2`, m.ID, now) != 1 {
		t.Fatalf("приглашение не погашено: %+v", all)
	}
	rows := e.audits("staff.mfa_enabled")
	if len(rows) != 1 || rows[0].StaffID == nil || *rows[0].StaffID != m.ID || rows[0].Role != "support" {
		t.Fatalf("аудит %+v", rows)
	}
	e.wantAuditMeta("staff.mfa_enabled")
	// после ack — ни setup, ни повторного ack
	if _, err := e.app.RecoveryAck(ctx, active, metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("повтор ack: %v", err)
	}
	if _, err := e.app.AcceptInvite(ctx, m.Email, code, newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("приглашение после ack: %v", err)
	}

	// вход 6a: пароль из принятия и резервный код из confirm
	m.Email = "ivan.p@wf.example"
	res, err := e.app.Login(ctx, m.Email, newPassword, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.VerifyMFA(ctx, e.load(res.Token), "", m.Codes[3], metaWeb); err != nil {
		t.Fatalf("резервный код из confirm: %v", err)
	}
}

// confirmedSetup — принятое приглашение (через after после выпуска) и подтверждённый TOTP: сессия
// setup перед ack.
func (e *env) confirmedSetup(email string, after time.Duration) (member, *auth.Staff) {
	e.t.Helper()
	m, code := e.invitee(email, memberOpts{Role: "support"}, purposeInvite)
	e.clock.Advance(after)
	_, s := e.accept(m, code)
	b32, _, err := e.app.MFASetup(context.Background(), s)
	if err != nil {
		e.t.Fatal(err)
	}
	m.Secret = decodeSecret(e.t, b32)
	if _, err := e.app.MFAConfirm(context.Background(), s, e.totp(m), metaWeb); err != nil {
		e.t.Fatal(err)
	}
	return m, s
}

// Решение контроллера (раунд 0): ack не проверяет срок приглашения — владение кодом доказано на
// accept, у сессии setup свой срок; иначе тупик на границе 24 ч (TOTP подтверждён, «Готово»
// отказывает). Истёкшее, но не погашенное приглашение гасится, учётка active.
func TestRecoveryAckExpiredInvite(t *testing.T) {
	e := newEnv(t)
	m, s := e.confirmedSetup("ivan.p@wf.example", 23*time.Hour)
	e.clock.Advance(2 * time.Hour) // за expires_at, сессия setup ещё жива
	tok, err := e.app.RecoveryAck(context.Background(), s, metaWeb)
	if err != nil {
		t.Fatalf("истёкшее приглашение: %v", err)
	}
	if e.load(tok).Stage != auth.StageActive {
		t.Fatal("сессия не active")
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND status = 'active'`, m.ID); n != 1 {
		t.Fatal("учётка не активирована")
	}
	if n := e.count(`SELECT count(*) FROM staff_invites WHERE staff_id = $1 AND consumed_at = $2 AND code_enc IS NULL`,
		m.ID, e.clock.Now()); n != 1 {
		t.Fatal("приглашение не погашено")
	}
}

// Нет живого приглашения (погашено) — staff.wrong_stage, учётка остаётся invited.
func TestRecoveryAckRevokedInvite(t *testing.T) {
	e := newEnv(t)
	m, s := e.confirmedSetup("ivan.p@wf.example", 0)
	e.exec(`UPDATE staff_invites SET revoked_at = $2, code_enc = NULL, code_key_id = NULL WHERE staff_id = $1`, m.ID, t0)
	if _, err := e.app.RecoveryAck(context.Background(), s, metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("погашенное приглашение: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND status = 'invited'`, m.ID); n != 1 {
		t.Fatal("учётка активирована")
	}
}

// Сессия настройки погашена, заменена или чужая — отказ сессии, а не wrong_stage.
func TestSetupRechecksSession(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	_, s := e.accept(m, code)
	e.clock.Advance(time.Minute)
	_, fresh := e.accept(m, code) // повторное принятие заменило сессию s
	if _, _, err := e.app.MFASetup(ctx, s); !errors.Is(err, auth.ErrSessionReplaced) {
		t.Fatalf("заменённая сессия: %v", err)
	}
	if err := e.app.Logout(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.app.MFASetup(ctx, fresh); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("погашенная сессия: %v", err)
	}
	if _, err := e.app.MFAConfirm(ctx, fresh, "123456", metaWeb); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("confirm на погашенной: %v", err)
	}
	if _, err := e.app.RecoveryAck(ctx, fresh, metaWeb); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("ack на погашенной: %v", err)
	}
}

// Перевыпуск кодов (§4.7, M9): только TOTP; успех — 10 новых кодов, старые не работают, счётчик
// неудач 0, шаг TOTP сгорает; аудит staff.recovery_regenerated.
func TestRegenerateRecovery(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	e.exec(`UPDATE staff_accounts SET mfa_failures = 3 WHERE id = $1`, m.ID)
	e.clock.Advance(30 * time.Second) // шаг входа уже использован
	code := e.totp(m)
	fresh, err := e.app.RegenerateRecovery(ctx, s, code, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 10 || slices.ContainsFunc(fresh, func(c string) bool { return slices.Contains(m.Codes, c) }) {
		t.Fatalf("коды %v", fresh)
	}
	if n := e.count(`SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1`, m.ID); n != 10 {
		t.Fatalf("кодов в базе %d", n)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND mfa_failures = 0`, m.ID); n != 1 {
		t.Fatal("счётчик не обнулён")
	}
	rows := e.audits("staff.recovery_regenerated")
	if len(rows) != 1 || rows[0].StaffID == nil || *rows[0].StaffID != m.ID {
		t.Fatalf("аудит %+v", rows)
	}
	e.wantAuditMeta("staff.recovery_regenerated")
	// тот же код TOTP второй раз не принимается
	if _, err := e.app.RegenerateRecovery(ctx, s, code, metaWeb); err == nil {
		t.Fatal("повтор кода TOTP принят")
	}
	// старый резервный код не работает, новый — работает
	if _, err := e.app.VerifyMFA(ctx, e.pending(m), "", m.Codes[0], metaWeb); !errors.Is(err, staff.ErrMFAInvalid) {
		t.Fatalf("старый код: %v", err)
	}
	if _, err := e.app.VerifyMFA(ctx, e.pending(m), "", fresh[0], metaWeb); err != nil {
		t.Fatalf("новый код: %v", err)
	}
	// MFAStatus: подтверждён, 9 неиспользованных
	st, err := e.app.MFAStatus(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConfirmedAt == nil || !st.ConfirmedAt.Equal(t0.Add(-23*time.Hour)) || st.RecoveryCodesLeft != 9 {
		t.Fatalf("статус %+v", st)
	}
}

// Резервный код вместо TOTP — неудача второго фактора (счётчик), коды не меняются.
func TestRegenerateRecoveryRejectsRecoveryCode(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	_, err := e.app.RegenerateRecovery(context.Background(), s, m.Codes[0], metaWeb)
	wantMFAInvalid(t, err, 4)
	if n := e.count(`SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1 AND used_at IS NULL`, m.ID); n != 10 {
		t.Fatalf("кодов %d", n)
	}
	rows := e.audits("staff.factor_failed")
	if len(rows) != 1 || !strings.Contains(rows[0].After, `"step": "recovery_regenerate"`) {
		t.Fatalf("аудит неудачи %+v", rows)
	}
}

// Неверный TOTP пять раз подряд — блокировка через withFactor; параллельные неверные не обходят
// порог.
func TestRegenerateRecoveryLockout(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	const tries = 8
	var (
		wg   sync.WaitGroup
		errs [tries]error
	)
	for i := range tries {
		wg.Go(func() { _, errs[i] = e.app.RegenerateRecovery(context.Background(), s, "000000", metaWeb) })
	}
	wg.Wait()
	var left []int
	for i, err := range errs {
		if me, ok := errors.AsType[*staff.MFAError](err); ok && errors.Is(err, staff.ErrMFAInvalid) {
			left = append(left, me.AttemptsLeft)
			continue
		}
		if !errors.Is(err, auth.ErrSessionInvalid) {
			t.Fatalf("попытка %d: %v", i, err)
		}
	}
	slices.Sort(left)
	if !slices.Equal(left, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("attempts_left %v", left)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND mfa_failures = 5 AND locked_until IS NOT NULL`, m.ID); n != 1 {
		t.Fatal("учётка не закрыта")
	}
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = 'staff.locked'`); n != 1 {
		t.Fatalf("событий staff.locked %d", n)
	}
}

// ⚠️ 6b: погашенная сессия — отказ без записи неудачи (и без смены кодов).
func TestRegenerateRecoveryRevokedSession(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	if err := e.app.Logout(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.RegenerateRecovery(context.Background(), s, "000000", metaWeb); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("погашенная сессия: %v", err)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 0 {
		t.Fatalf("счётчик %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.factor_failed'`); n != 0 {
		t.Fatalf("аудит неудачи %d", n)
	}
}

// Перевыпуск не из стадии active — wrong_stage без записи неудачи.
func TestRegenerateRecoveryWrongStage(t *testing.T) {
	e := newEnv(t)
	m, s := e.setupSession("ivan.p@wf.example")
	if _, err := e.app.RegenerateRecovery(context.Background(), s, "000000", metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("setup: %v", err)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 0 {
		t.Fatalf("счётчик %d", n)
	}
}

// MFAStatus учётки без второго фактора — не подтверждён, 0 кодов.
func TestMFAStatusNotConfigured(t *testing.T) {
	e := newEnv(t)
	_, s := e.setupSession("ivan.p@wf.example")
	st, err := e.app.MFAStatus(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConfirmedAt != nil || st.RecoveryCodesLeft != 0 {
		t.Fatalf("статус %+v", st)
	}
}
