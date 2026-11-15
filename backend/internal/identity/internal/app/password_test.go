package app

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/identity"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/otp"
	"wf/backend/internal/platform/peer"
)

const resetNewPassword = "Свисток на 46-й минуте" //nolint:gosec // G101: пароль теста, не учётные данные

// resetCtx — запрос с адресом клиента (peer), как его видит сценарий за конвейером.
func resetCtx() context.Context {
	return peer.With(context.Background(), peer.Info{IP: netip.MustParseAddr("203.0.113.7")})
}

// startReset — старт сброса пароля по почте ника (seedUser: <ник>@example.com).
func startReset(t *testing.T, e *env, email string) CodeSent {
	t.Helper()
	sent, err := e.a.StartPasswordReset(resetCtx(), email)
	if err != nil {
		t.Fatalf("%s: %v", email, err)
	}
	return sent
}

func confirmReset(e *env, sent CodeSent, code string, mfa *SecondFactor, pw string) (AuthOutcome, error) {
	return e.a.ConfirmPasswordReset(context.Background(), ResetInput{CodeID: sent.CodeID, Code: code, NewPassword: pw,
		MFA: mfa}, web("reset-dev"))
}

func codeAttempts(t *testing.T, e *env, id uuid.UUID) int {
	t.Helper()
	return count(t, e.owner, `SELECT attempts FROM auth_codes WHERE id = $1`, id)
}

func codeAlive(t *testing.T, e *env, id uuid.UUID) bool {
	t.Helper()
	return count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, id) == 1
}

func passwordHash(t *testing.T, e *env, uid uuid.UUID) string {
	t.Helper()
	var h string
	if err := e.owner.QueryRow(context.Background(), `SELECT coalesce((SELECT password_hash FROM credentials WHERE user_id = $1), '')`,
		uid).Scan(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

// totpNow — код TOTP секрета на часах теста.
func totpNow(e *env, secret []byte) string { return otp.Code(secret, otp.Step(e.clock.Now())) }

// §3.4 п. 1, R29, m12: есть (active, suspended, pending_deletion) / нет / banned — одинаковый ответ, на каждый старт по
// одной строке auth_codes и одной задаче письма password_reset_code; настоящий код (с user_id) — только у первых трёх,
// у banned и несуществующей — приманка без user_id (Composer её пропустит).
func TestStartResetDecoy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, s := range []string{"active", "suspended", "pending_deletion", "banned"} {
		uid := seedUser(t, e.owner, "reset_"+s, "ru")
		setStatus(t, e, uid, s)
	}
	now := e.clock.Now()
	for _, tc := range []struct {
		email string
		real  bool
	}{{"reset_active@example.com", true}, {"reset_suspended@example.com", true},
		{"reset_pending_deletion@example.com", true}, {"reset_banned@example.com", false}, {"nobody@example.com", false}} {
		codes := count(t, e.owner, `SELECT count(*) FROM auth_codes`)
		jobs := mailJobs(t, e, MailPasswordResetCode)
		sent := startReset(t, e, tc.email)
		if sent.ExpiresAt.Sub(now) != 15*time.Minute || sent.ResendAvailable.Sub(now) != time.Minute {
			t.Fatalf("%s: %+v", tc.email, sent)
		}
		if count(t, e.owner, `SELECT count(*) FROM auth_codes`) != codes+1 || mailJobs(t, e, MailPasswordResetCode) != jobs+1 {
			t.Fatalf("%s: не одна строка и одна задача письма", tc.email)
		}
		genuine := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND NOT decoy AND user_id IS NOT NULL`, sent.CodeID) == 1
		if genuine != tc.real {
			t.Fatalf("%s: настоящий код %v, ждали %v", tc.email, genuine, tc.real)
		}
	}
	if _, err := e.a.StartPasswordReset(resetCtx(), "не почта"); problemCode(err) != httpx.CodeValidationFailed {
		t.Fatalf("неверная почта: %v", err)
	}
}

// C1: неверный код у аккаунта с 2FA (A), без 2FA (B) и у приманки (D) — без mfa, с неверным TOTP, с верным TOTP — у
// всех одинаковый ответ code_invalid и attempts 1, 2, 3; mfa_required — только у A и только при верном коде.
func TestConfirmResetNoOracle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := seedUser(t, e.owner, "oracle_a", "ru")
	secret := seedMfa(t, e, a)
	seedUser(t, e.owner, "oracle_b", "ru")
	sents := map[string]CodeSent{"A": startReset(t, e, "oracle_a@example.com"), "B": startReset(t, e, "oracle_b@example.com"),
		"D": startReset(t, e, "oracle_d@example.com")}
	codeA := plainCode(t, e, sents["A"].CodeID)
	wrong := wrongCode(codeA)
	for name, sent := range sents {
		for i, mfa := range []*SecondFactor{nil, {TOTP: "000000"}, {TOTP: totpNow(e, secret)}} {
			_, err := confirmReset(e, sent, wrong, mfa, resetNewPassword)
			p := problemOfErr(err)
			if p.Code != identity.CodeCodeInvalid || p.Status != 400 || p.AttemptsLeft == nil || *p.AttemptsLeft != 4-i ||
				len(p.Methods) != 0 {
				t.Fatalf("%s, попытка %d: %+v", name, i+1, p)
			}
			if n := codeAttempts(t, e, sent.CodeID); n != i+1 {
				t.Fatalf("%s: attempts %d", name, n)
			}
		}
	}
	_, err := confirmReset(e, sents["A"], codeA, nil, resetNewPassword)
	if p := problemOfErr(err); p.Code != identity.CodeMfaRequired {
		t.Fatalf("A, верный код: %+v", p)
	}
	if n := count(t, e.owner, `SELECT failures FROM user_mfa WHERE user_id = $1`, a); n != 0 {
		t.Fatalf("неверный код тратил второй фактор: failures %d", n)
	}
}

// §3.4 п. 2: 2FA и верный код без mfa — 400 mfa_required (methods без passkey), попытка не считается, код жив;
// неверный TOTP — mfa_invalid, пароль прежний, код жив; верный TOTP — пароль сменён, прежние сессии отозваны, новая
// выдана. 11-я неудача фактора за час — 429.
func TestConfirmResetRequiresMfa(t *testing.T) {
	t.Parallel()
	e := newEnv(t, pgLimiters)
	uid := seedUser(t, e.owner, "reset_mfa", "ru")
	setPassword(t, e, uid, "h:old")
	secret := seedMfa(t, e, uid)
	old := issue(t, e, uid, web("old"))
	sent := startReset(t, e, "reset_mfa@example.com")
	code := plainCode(t, e, sent.CodeID)
	_, err := confirmReset(e, sent, code, nil, resetNewPassword)
	if p := problemOfErr(err); p.Code != identity.CodeMfaRequired || p.Status != 400 ||
		!slices.Equal(p.Methods, []string{FactorTOTP, FactorRecovery}) {
		t.Fatalf("без mfa: %+v", p)
	}
	if codeAttempts(t, e, sent.CodeID) != 0 || !codeAlive(t, e, sent.CodeID) {
		t.Fatal("mfa_required потратил попытку или код")
	}
	_, err = confirmReset(e, sent, code, &SecondFactor{TOTP: wrongCode(totpNow(e, secret))}, resetNewPassword)
	if p := problemOfErr(err); p.Code != identity.CodeMfaInvalid || p.Status != 400 {
		t.Fatalf("неверный TOTP: %+v", p)
	}
	if passwordHash(t, e, uid) != "h:old" || !codeAlive(t, e, sent.CodeID) || codeAttempts(t, e, sent.CodeID) != 0 {
		t.Fatal("неверный TOTP: пароль сменён или код потрачен")
	}
	out, err := confirmReset(e, sent, code, &SecondFactor{TOTP: totpNow(e, secret)}, resetNewPassword)
	if err != nil || out.Status != StatusAuthenticated || out.Issued == nil {
		t.Fatalf("верный TOTP: %+v %v", out, err)
	}
	if passwordHash(t, e, uid) != "h:"+resetNewPassword {
		t.Fatal("пароль не сменён")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_reason = 'password_reset'`, old.SessionID); n != 1 {
		t.Fatal("прежняя сессия не отозвана")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL AND mfa_at IS NOT NULL`,
		out.Issued.SessionID); n != 1 {
		t.Fatal("новая сессия не выдана")
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'password_reset'`:                                              1,
		`SELECT count(*) FROM outbox WHERE event_type = 'identity.password_changed' AND aggregate_id = $1`:                                 1,
		`SELECT count(*) FROM outbox WHERE event_type = 'identity.sessions_revoked' AND aggregate_id = $1`:                                 1,
		`SELECT count(*) FROM audit_log WHERE action = 'identity.password_reset' AND actor_user_id = $1`:                                   1,
		`SELECT count(*) FROM auth_codes WHERE user_id = $1 AND consumed_at IS NOT NULL AND code_enc IS NULL`:                              1,
		`SELECT count(*) FROM river_job WHERE kind = 'mail.send' AND args->>'mail' = 'identity.password_changed' AND $1::uuid IS NOT NULL`: 1,
	} {
		if n := count(t, e.owner, q, uid); n != want {
			t.Errorf("%s: %d, ждали %d", q, n, want)
		}
	}
	if _, err := confirmReset(e, sent, code, &SecondFactor{TOTP: totpNow(e, secret)}, resetNewPassword); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("повтор после успеха: %v", err)
	}
	// 10 неудач фактора за час — 11-я попытка 429 до проверки
	brute := seedUser(t, e.owner, "reset_brute", "ru")
	bsecret := seedMfa(t, e, brute)
	bs := startReset(t, e, "reset_brute@example.com")
	bcode := plainCode(t, e, bs.CodeID)
	for i := range 10 {
		_, err := confirmReset(e, bs, bcode, &SecondFactor{TOTP: wrongCode(totpNow(e, bsecret))}, resetNewPassword)
		if problemCode(err) != identity.CodeMfaInvalid {
			t.Fatalf("неудача %d: %v", i+1, err)
		}
	}
	_, err = confirmReset(e, bs, bcode, &SecondFactor{TOTP: totpNow(e, bsecret)}, resetNewPassword)
	if p := problemOfErr(err); p.Code != httpx.CodeRateLimited || p.Status != 429 || p.RetryAfter < 1 {
		t.Fatalf("11-я: %+v", p)
	}
}

// R3-1: слабый пароль с резервным кодом — 422 password_weak до tx: код сброса и резервный код живы, attempts прежний.
func TestConfirmResetWeakPasswordKeepsFactor(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "reset_weak", "ru")
	seedMfa(t, e, uid, "abcde-fghij")
	sent := startReset(t, e, "reset_weak@example.com")
	_, err := confirmReset(e, sent, plainCode(t, e, sent.CodeID), &SecondFactor{Recovery: "abcde-fghij"}, "short")
	if p := problemOfErr(err); p.Code != identity.CodePasswordWeak || len(p.Reasons) != 1 || p.Reasons[0] != "too_short" {
		t.Fatalf("%+v", p)
	}
	if !codeAlive(t, e, sent.CodeID) || codeAttempts(t, e, sent.CodeID) != 0 {
		t.Fatal("код сброса потрачен")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, uid); n != 1 {
		t.Fatal("резервный код сгорел на слабом пароле")
	}
}

// N4, R29: забанен между стартом и подтверждением — верный код: 410 без записей (пароль, сессии, attempts не тронуты);
// неверный: 400 code_invalid, как у незабаненного (бан не виден до верного MAC).
func TestConfirmResetBannedAfterStart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "reset_banned", "ru")
	setPassword(t, e, uid, "h:old")
	live := issue(t, e, uid, web("old"))
	sent := startReset(t, e, "reset_banned@example.com")
	setStatus(t, e, uid, "banned")
	code := plainCode(t, e, sent.CodeID)
	if _, err := confirmReset(e, sent, code, nil, resetNewPassword); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("верный код: %v", err)
	}
	if passwordHash(t, e, uid) != "h:old" || codeAttempts(t, e, sent.CodeID) != 0 ||
		count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, live.SessionID) != 1 {
		t.Fatal("бан: записи после верного кода")
	}
	_, err := confirmReset(e, sent, wrongCode(code), nil, resetNewPassword)
	if p := problemOfErr(err); p.Code != identity.CodeCodeInvalid || p.AttemptsLeft == nil || *p.AttemptsLeft != 4 {
		t.Fatalf("неверный код у забаненного: %+v", p)
	}
}

// hashProbe — хэшер, который при Hash запоминает наибольшее число занятых соединений пула сценариев (argon2 вне tx, R31).
type hashProbe struct {
	fakeHasher
	pool     *pgxpool.Pool
	mu       sync.Mutex
	acquired int32
	hashed   atomic.Int32
}

func (h *hashProbe) Hash(ctx context.Context, pw string) (string, error) {
	h.hashed.Add(1)
	h.mu.Lock()
	h.acquired = max(h.acquired, h.pool.Stat().AcquiredConns())
	h.mu.Unlock()
	return h.fakeHasher.Hash(ctx, pw)
}

// N10: две tx — между ними Hash без занятого соединения; код отозвали между фазами (второй afterRowFound, tx2) — 410,
// пароль прежний.
func TestConfirmResetTwoPhase(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	probe := &hashProbe{pool: e.a.pool}
	e.a.hasher = probe
	uid := seedUser(t, e.owner, "reset_phase", "ru")
	setPassword(t, e, uid, "h:old")
	sent := startReset(t, e, "reset_phase@example.com")
	code := plainCode(t, e, sent.CodeID)
	var calls atomic.Int32
	var hookErr error
	var mu sync.Mutex
	e.a.hooks = &hooks{afterRowFound: func(kind string) {
		if kind != "code" || calls.Add(1) != 2 {
			return
		}
		if probe.hashed.Load() != 1 {
			mu.Lock()
			hookErr = errors.New("tx2 до хэша")
			mu.Unlock()
		}
		_, err := e.owner.Exec(context.Background(), `UPDATE auth_codes SET revoked_at = now(), code_enc = NULL WHERE id = $1`, sent.CodeID)
		mu.Lock()
		hookErr = errors.Join(hookErr, err)
		mu.Unlock()
	}}
	_, err := confirmReset(e, sent, code, nil, resetNewPassword)
	e.a.hooks = nil
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if problemCode(err) != identity.CodeCodeExpired || calls.Load() != 2 {
		t.Fatalf("код отозван между фазами: %v, вызовов afterRowFound %d", err, calls.Load())
	}
	if passwordHash(t, e, uid) != "h:old" {
		t.Fatal("пароль сменён по отозванному коду")
	}
	if probe.acquired != 0 {
		t.Fatalf("во время Hash занято соединений: %d", probe.acquired)
	}
}

// §3.11 п. 2: аккаунт ждёт удаления — пароль сменён, ответ deletion_pending без сессии.
func TestConfirmResetPendingDeletion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "reset_pending", "ru")
	setPassword(t, e, uid, "h:old")
	setStatus(t, e, uid, "pending_deletion")
	sent := startReset(t, e, "reset_pending@example.com")
	out, err := confirmReset(e, sent, plainCode(t, e, sent.CodeID), nil, resetNewPassword)
	if err != nil || out.Status != StatusDeletionPending || out.Issued != nil || out.RestoreToken == "" {
		t.Fatalf("%+v %v", out, err)
	}
	if passwordHash(t, e, uid) != "h:"+resetNewPassword || sessions(t, e, uid) != 0 {
		t.Fatal("пароль не сменён или выдана сессия")
	}
	// m6 ревью R1W5: выданный restore_token жив — гашение токенов шагов (revokeChallenges) идёт раньше finishLogin
	got, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web("reset-dev"))
	if err != nil || got.Status != StatusAuthenticated {
		t.Fatalf("отмена удаления выданным токеном: %+v %v", got, err)
	}
}

// Уборка W5 п. 1: флаг документов сброса считается в tx2 сценария (httpapi после коммита базу не читает): без
// согласий — true, с текущими версиями — false.
func TestConfirmResetDocumentsRequired(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, tc := range []struct {
		nick     string
		accepted bool
	}{{"reset_docs_none", false}, {"reset_docs_ok", true}} {
		uid := seedUser(t, e.owner, tc.nick, "ru")
		setPassword(t, e, uid, "h:old")
		if tc.accepted {
			for _, d := range []string{"terms", "privacy"} {
				if _, err := e.owner.Exec(context.Background(), `INSERT INTO consents (user_id, document, version, accepted_at, client_kind)
					VALUES ($1, $2, '2026-11-01', now(), 'mobile')`, uid, d); err != nil {
					t.Fatal(err)
				}
			}
		}
		sent := startReset(t, e, tc.nick+"@example.com")
		out, err := confirmReset(e, sent, plainCode(t, e, sent.CodeID), nil, resetNewPassword)
		if err != nil || out.Status != StatusAuthenticated {
			t.Fatalf("%s: %+v %v", tc.nick, out, err)
		}
		if out.DocumentsRequired == tc.accepted {
			t.Fatalf("%s: documents_acceptance_required = %v", tc.nick, out.DocumentsRequired)
		}
	}
}

// deadlineHasher — хэшер, запоминающий срок ctx хэша.
type deadlineHasher struct {
	fakeHasher
	ctx context.Context
}

func (h *deadlineHasher) Hash(ctx context.Context, pw string) (string, error) {
	h.ctx = ctx
	return h.fakeHasher.Hash(ctx, pw)
}

// m11: хэш нового пароля после tx1 — на ctx со сроком ≤ detachedTimeout, даже если у запроса срока нет.
func TestConfirmResetHashDeadline(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	h := &deadlineHasher{}
	e.a.hasher = h
	uid := seedUser(t, e.owner, "reset_deadline", "ru")
	setPassword(t, e, uid, "h:old")
	sent := startReset(t, e, "reset_deadline@example.com")
	if _, err := confirmReset(e, sent, plainCode(t, e, sent.CodeID), nil, resetNewPassword); err != nil {
		t.Fatal(err)
	}
	if h.ctx == nil {
		t.Fatal("Hash не вызван")
	}
	assertDetachedDeadline(h.ctx, t)
}

// §3.4: аккаунт без пароля (только соцвход) сбросом получает пароль.
func TestConfirmResetSocialOnlySetsPassword(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "reset_social", "ru")
	sent := startReset(t, e, "reset_social@example.com")
	out, err := confirmReset(e, sent, plainCode(t, e, sent.CodeID), nil, resetNewPassword)
	if err != nil || out.Status != StatusAuthenticated {
		t.Fatalf("%+v %v", out, err)
	}
	if passwordHash(t, e, uid) != "h:"+resetNewPassword {
		t.Fatal("пароль не задан")
	}
}

// Непредусмотренное 1 ревью W5-А: смена пароля гасит живые mfa_token аккаунта — старый пароль не доводит вход вторым
// фактором.
func TestChangePasswordKillsStepTokens(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, _, tok := mfaLogin(t, e, "change_tokens", "abcde-fghij")
	if err := e.a.ChangePassword(context.Background(), freshSession(t, e, uid), resetNewPassword, web("req")); err != nil {
		t.Fatal(err)
	}
	if _, err := complete(e, tok, SecondFactor{Recovery: "abcde-fghij"}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("mfa_token после смены пароля: %v", err)
	}
}

// Непредусмотренное 1 ревью W5-А: сброс пароля (tx2) гасит живые mfa_token аккаунта.
func TestConfirmResetKillsStepTokens(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, secret, tok := mfaLogin(t, e, "reset_tokens", "abcde-fghij")
	sent := startReset(t, e, "reset_tokens@example.com")
	if _, err := confirmReset(e, sent, plainCode(t, e, sent.CodeID), &SecondFactor{TOTP: totpNow(e, secret)}, resetNewPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := complete(e, tok, SecondFactor{Recovery: "abcde-fghij"}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("mfa_token после сброса пароля: %v", err)
	}
}
