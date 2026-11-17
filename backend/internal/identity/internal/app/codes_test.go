package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/httpx"
)

// problemOfErr — Problem ошибки сценария (нет — нулевой).
func problemOfErr(err error) httpx.Problem {
	var pe httpx.ProblemError
	if errors.As(err, &pe) {
		return pe.Problem()
	}
	return httpx.Problem{}
}

// plainCode — код из письма строки auth_codes (code_enc, Open кольцом секретов; так его читает Composer).
func plainCode(t *testing.T, e *env, codeID uuid.UUID) string {
	t.Helper()
	var enc []byte
	var keyID *string
	if err := e.owner.QueryRow(context.Background(), `SELECT code_enc, key_id FROM auth_codes WHERE id = $1`, codeID).
		Scan(&enc, &keyID); err != nil {
		t.Fatal(err)
	}
	if enc == nil || keyID == nil {
		t.Fatalf("код %s без code_enc", codeID)
	}
	code, err := e.a.secrets.Open(enc, *keyID, codeID[:])
	if err != nil {
		t.Fatal(err)
	}
	return string(code)
}

// wrongCode — шесть цифр, заведомо не равные коду.
func wrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

// mailJobs — задач письма вида kind (ref — любой).
func mailJobs(t *testing.T, e *env, kind string) int {
	t.Helper()
	return count(t, e.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send' AND args->>'mail' = $1`, kind)
}

// resetCodeOf — код сброса пароля аккаунта (startCode в tx: настоящий, с user_id).
func resetCodeOf(t *testing.T, e *env, uid uuid.UUID, email string, decoy bool) CodeSent {
	t.Helper()
	var sent CodeSent
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		sent, err = e.a.startCode(ctx, tx, StartCode{Purpose: identitydb.AuthCodePurposePasswordReset, Email: email,
			UserID: uid, Decoy: decoy, Client: web("x")})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return sent
}

// checkInTx — checkCode в своей tx (неудача коммитится, как у сценариев).
func checkInTx(t *testing.T, e *env, purpose identitydb.AuthCodePurpose, codeID uuid.UUID, code string, mode CodeMode) (codeRow, error) {
	t.Helper()
	var row codeRow
	err := e.a.inTxCommitFailure(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		row, err = e.a.checkCode(ctx, tx, purpose, codeID, code, mode)
		return err
	})
	return row, err
}

// §3.14: верный код в CodeVerifyOnly ничего не пишет, CodeConsume гасит (code_enc = NULL); неверный — attempts + 1
// с attempts_left, пятый — 410 и код погашен; чужое назначение и неизвестный id — 410.
func TestCheckCodeModes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "check_modes", "ru")
	sent := resetCodeOf(t, e, uid, "check_modes@example.com", false)
	code := plainCode(t, e, sent.CodeID)
	if _, err := checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, sent.CodeID, code, CodeVerifyOnly); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND consumed_at IS NULL AND code_enc IS NOT NULL
		AND attempts = 0`, sent.CodeID); n != 1 {
		t.Fatal("CodeVerifyOnly изменил строку")
	}
	if _, err := checkInTx(t, e, identitydb.AuthCodePurposeSignup, sent.CodeID, code, CodeConsume); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("чужое назначение: %v", err)
	}
	if _, err := checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, uuid.New(), code, CodeConsume); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("неизвестный id: %v", err)
	}
	for i := range 4 {
		_, err := checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, sent.CodeID, wrongCode(code), CodeConsume)
		p := problemOfErr(err)
		if p.Code != identity.CodeCodeInvalid || p.AttemptsLeft == nil || *p.AttemptsLeft != 4-i {
			t.Fatalf("неудача %d: %+v", i+1, p)
		}
	}
	if _, err := checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, sent.CodeID, code, CodeConsume); err != nil {
		t.Fatalf("верный после 4 неудач: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND consumed_at IS NOT NULL AND code_enc IS NULL`,
		sent.CodeID); n != 1 {
		t.Fatal("CodeConsume не погасил")
	}
	if _, err := checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, sent.CodeID, code, CodeConsume); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("повтор погашенного: %v", err)
	}
	// пятая неудача гасит; срок — ровно expires_at уже истёк
	worn := resetCodeOf(t, e, uid, "check_modes@example.com", false)
	wcode := plainCode(t, e, worn.CodeID)
	for range 4 {
		_, _ = checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, worn.CodeID, wrongCode(wcode), CodeConsume)
	}
	if _, err := checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, worn.CodeID, wrongCode(wcode), CodeConsume); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("пятая неудача: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND attempts = 5 AND revoked_at IS NOT NULL
		AND code_enc IS NULL`, worn.CodeID); n != 1 {
		t.Fatal("пятая неудача не погасила код")
	}
	fresh := resetCodeOf(t, e, uid, "check_modes@example.com", false)
	fcode := plainCode(t, e, fresh.CodeID)
	e.clock.Set(fresh.ExpiresAt)
	if _, err := checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, fresh.CodeID, fcode, CodeConsume); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("ровно в срок: %v", err)
	}
}

// R3-4, приём lockByRow для кода: checkCode встаёт на afterRowFound("code"); участник — lockUser → отзыв того же
// кода → коммит; checkCode продолжает и видит перепроверенную строку — 410 code_expired без 40P01. При FOR UPDATE
// строки кода до lockUser участник встал бы на строку, а checkCode — на users: взаимоблокировка.
func TestCheckCodeLockOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "lock_order_code", "ru")
	sent := resetCodeOf(t, e, uid, "lock_order_code@example.com", false)
	code := plainCode(t, e, sent.CodeID)
	found := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	e.a.hooks = &hooks{afterRowFound: func(kind string) {
		if kind != "code" {
			return
		}
		once.Do(func() {
			close(found)
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
			}
		})
	}}
	var wg sync.WaitGroup
	var partErr error
	wg.Go(func() {
		<-found
		defer close(done)
		partErr = e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := e.a.lockUser(ctx, tx, uid); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE auth_codes SET revoked_at = now(), code_enc = NULL WHERE id = $1`, sent.CodeID)
			return err
		})
	})
	_, err := checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, sent.CodeID, code, CodeConsume)
	wg.Wait()
	e.a.hooks = nil
	if partErr != nil || isDeadlock(err) || problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("checkCode: %v (40P01: %v); участник: %v", err, isDeadlock(err), partErr)
	}
	// пока tx checkCode открыта, users аккаунта под замком (lockUser приёма R30)
	live := resetCodeOf(t, e, uid, "lock_order_code@example.com", false)
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := e.a.checkCode(ctx, tx, identitydb.AuthCodePurposePasswordReset, live.CodeID, plainCode(t, e, live.CodeID),
			CodeVerifyOnly); err != nil {
			return err
		}
		_, err := e.owner.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, uid)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			t.Errorf("users аккаунта не под замком checkCode: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// §3.14: один живой код на (назначение, почта) — новый старт гасит прежний; две параллельные вставки не дают 23505.
func TestStartCodeOneLive(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "one_live", "ru")
	first := resetCodeOf(t, e, uid, "one_live@example.com", false)
	second := resetCodeOf(t, e, uid, "one_live@example.com", false)
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND revoked_at IS NOT NULL AND code_enc IS NULL`,
		first.CodeID); n != 1 {
		t.Fatal("прежний код не погашен")
	}
	if _, err := checkInTx(t, e, identitydb.AuthCodePurposePasswordReset, first.CodeID, "000000", CodeConsume); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("прежний код: %v", err)
	}
	gate := pairBarrier(2 * time.Second)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			errs[i] = e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
				gate()
				_, err := e.a.startCode(ctx, tx, StartCode{Purpose: identitydb.AuthCodePurposePasswordReset,
					Email: "one_live@example.com", UserID: uid, Client: web("x")})
				return err
			})
		})
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("параллельные старты: %v / %v", errs[0], errs[1])
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE email = 'one_live@example.com' AND consumed_at IS NULL
		AND revoked_at IS NULL`); n != 1 {
		t.Fatalf("живых кодов %d", n)
	}
	_ = second
}

// R40 (решение пользователя Q5): живой код смены почты — один на (аккаунт, почта). Старт B на адрес X не гасит код A
// на тот же X; повторный старт A гасит только прежний код A; подтверждение выбирает одного уникальностью users.email
// (второй — 409 email_taken). Сброс пароля — по-прежнему один живой код на (назначение, почта), чей бы ни был.
func TestEmailChangeCodePerUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	ua := seedUser(t, e.owner, "per_user_a", "ru")
	ub := seedUser(t, e.owner, "per_user_b", "ru")
	sa := issue(t, e, ua, web("a")).SessionID
	sb := issue(t, e, ub, web("b")).SessionID
	const target = "per_user_x@example.com"
	a1, err := e.a.StartEmailChange(ctx, sa, target, web("req"))
	if err != nil {
		t.Fatal(err)
	}
	b1, err := e.a.StartEmailChange(ctx, sb, target, web("req"))
	if err != nil {
		t.Fatal(err)
	}
	if !codeAlive(t, e, a1.CodeID) || !codeAlive(t, e, b1.CodeID) {
		t.Fatal("старт B погасил код A на тот же адрес")
	}
	a2, err := e.a.StartEmailChange(ctx, sa, target, web("req"))
	if err != nil {
		t.Fatal(err)
	}
	if codeAlive(t, e, a1.CodeID) || !codeAlive(t, e, a2.CodeID) || !codeAlive(t, e, b1.CodeID) {
		t.Fatal("повторный старт A: гаснет только прежний код A")
	}
	if me, err := e.a.ConfirmEmailChange(ctx, ua, a2.CodeID, plainCode(t, e, a2.CodeID)); err != nil || me.Email != target {
		t.Fatalf("подтверждение A: %+v %v", me, err)
	}
	if _, err := e.a.ConfirmEmailChange(ctx, ub, b1.CodeID, plainCode(t, e, b1.CodeID)); problemCode(err) != identity.CodeEmailTaken {
		t.Fatalf("подтверждение B после A: %v", err)
	}

	r1 := resetCodeOf(t, e, ua, "per_user_reset@example.com", false)
	r2 := resetCodeOf(t, e, ub, "per_user_reset@example.com", false)
	if codeAlive(t, e, r1.CodeID) || !codeAlive(t, e, r2.CodeID) {
		t.Fatal("сброс пароля: второй старт на ту же почту гасит первый")
	}
}

// I1 итогового ревью Г: вставка кода не повторяет предикат уникального индекса (ON CONFLICT DO NOTHING без цели) —
// следующая правка индексов не даёт 42P10 на любом старте кода, как старый бинарник на схеме 0027. Индексы живых кодов
// пересозданы с более узким предикатом (лишнее условие по created_at, смысл тот же): старты сброса пароля и смены почты проходят, живой код по-прежнему один.
func TestStartCodeArbiterIndependentOfIndex(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	acctExec(t, e, `DROP INDEX auth_codes_live_key`)
	acctExec(t, e, `DROP INDEX auth_codes_live_email_change_key`)
	acctExec(t, e, `CREATE UNIQUE INDEX auth_codes_live_key ON auth_codes (purpose, email)
		WHERE purpose <> 'email_change' AND consumed_at IS NULL AND revoked_at IS NULL AND created_at > '2000-01-01'`)
	acctExec(t, e, `CREATE UNIQUE INDEX auth_codes_live_email_change_key ON auth_codes (purpose, user_id, email)
		WHERE purpose = 'email_change' AND consumed_at IS NULL AND revoked_at IS NULL AND created_at > '2000-01-01'`)
	uid := seedUser(t, e.owner, "arbiter", "ru")
	first := resetCodeOf(t, e, uid, "arbiter@example.com", false)
	second := resetCodeOf(t, e, uid, "arbiter@example.com", false)
	if codeAlive(t, e, first.CodeID) || !codeAlive(t, e, second.CodeID) {
		t.Fatal("сброс пароля: живой код не один")
	}
	sid := issue(t, e, uid, web("a")).SessionID
	for range 2 {
		if _, err := e.a.StartEmailChange(context.Background(), sid, "arbiter_new@example.com", web("req")); err != nil {
			t.Fatalf("смена почты: %v", err)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE purpose = 'email_change' AND consumed_at IS NULL
		AND revoked_at IS NULL`); n != 1 {
		t.Fatalf("живых кодов смены почты %d", n)
	}
}

// §3.14: паузы 60/120/300 с, раньше — 429 resend_too_soon с Retry-After до паузы; четвёртая отправка последняя —
// дальше 410; resend_available_at после последней — expires_at. Приманка — те же паузы и ответы, писем не шлёт.
func TestResendSchedule(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	city := seedSignupCity(t, e, "XS", true, 14)
	seedUser(t, e.owner, "resend_taken", "ru")
	for _, tc := range []struct {
		name, email, nick string
		decoy             bool
	}{{"настоящий", "resend_free@example.com", "resend_free", false}, {"приманка", "resend_taken@example.com", "resend_x", true}} {
		start := e.clock.Now()
		sent, err := e.a.StartSignup(context.Background(), signupInput(tc.email, tc.nick, city), mobile("ios", "resend-"+tc.nick))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !sent.ResendAvailable.Equal(start.Add(60 * time.Second)) {
			t.Fatalf("%s: первая пауза до %v", tc.name, sent.ResendAvailable)
		}
		e.clock.Advance(59 * time.Second)
		_, err = e.a.ResendCode(context.Background(), sent.CodeID)
		if p := problemOfErr(err); p.Code != identity.CodeResendTooSoon || p.RetryAfter != 1 {
			t.Fatalf("%s: раньше паузы: %+v", tc.name, p)
		}
		pauses := []time.Duration{120 * time.Second, 300 * time.Second}
		e.clock.Advance(time.Second)
		for i := range 3 {
			got, err := e.a.ResendCode(context.Background(), sent.CodeID)
			if err != nil {
				t.Fatalf("%s: отправка %d: %v", tc.name, i+2, err)
			}
			if got.CodeID != sent.CodeID || !got.ExpiresAt.Equal(sent.ExpiresAt) {
				t.Fatalf("%s: другой код %+v", tc.name, got)
			}
			if i < 2 {
				if !got.ResendAvailable.Equal(e.clock.Now().Add(pauses[i])) {
					t.Fatalf("%s: пауза после %d-й: %v", tc.name, i+2, got.ResendAvailable)
				}
				e.clock.Advance(pauses[i])
			} else if !got.ResendAvailable.Equal(sent.ExpiresAt) {
				t.Fatalf("%s: после последней resend_available_at = %v, ждали expires_at", tc.name, got.ResendAvailable)
			}
		}
		if _, err := e.a.ResendCode(context.Background(), sent.CodeID); problemCode(err) != identity.CodeCodeExpired {
			t.Fatalf("%s: пятая отправка: %v", tc.name, err)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND sends = 4`, sent.CodeID); n != 1 {
			t.Fatalf("%s: sends не 4", tc.name)
		}
	}
	if n := mailJobs(t, e, MailSignupCode); n != 4 {
		t.Fatalf("писем кода %d, ждали 4", n)
	}
	if n := mailJobs(t, e, MailSignupAccountExists); n != 1 {
		t.Fatalf("писем «уже есть аккаунт» %d, ждали 1 (R26)", n)
	}
	if _, err := e.a.ResendCode(context.Background(), uuid.New()); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("неизвестный код: %v", err)
	}
}

// assertDetachedDeadline — ctx работы после обрыва запроса: срок задан и не дальше detachedTimeout от момента проверки
// (m11 ревью W4-A: ctx без отмены и без срока держал бы соединение и горутину вечно).
func assertDetachedDeadline(ctx context.Context, t *testing.T) {
	t.Helper()
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("у ctx нет срока: detached без WithTimeout")
	}
	if left := time.Until(dl); left > detachedTimeout {
		t.Fatalf("до срока %v — дальше detachedTimeout %v", left, detachedTimeout)
	}
}

// m11: inTxCommitFailure идёт на ctx со сроком ≤ detachedTimeout — и когда у запроса срок дальше (час).
func TestInTxCommitFailureDeadline(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	parent, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	called := false
	if err := e.a.inTxCommitFailure(parent, func(ctx context.Context, _ pgx.Tx) error {
		called = true
		assertDetachedDeadline(ctx, t)
		return nil
	}); err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}
