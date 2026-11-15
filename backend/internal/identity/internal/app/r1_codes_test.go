package app

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
)

// Стражи раунда 1 ревью W4-А (коды, сброс и смена пароля, смена почты).

// smallPool — пул сценариев на maxConns соединений (та же база и роль, что у env). lock_timeout 2 с: взаимное
// ожидание «замок users ↔ соединение пула» рвётся ошибкой ожидающего замка, а не висит до -timeout теста.
func smallPool(t *testing.T, maxConns int32) envOpt {
	return func(d *identity.Deps) {
		cfg := d.Pool.Config()
		cfg.MaxConns = maxConns
		cfg.ConnConfig.RuntimeParams["lock_timeout"] = "2s"
		p, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		d.Pool = p
	}
}

// pgLimiters — лимитеры как в cmd/api: ratelimit.PG на пуле сценариев и на соединении транзакции.
func pgLimiters(d *identity.Deps) {
	d.Limiter = ratelimit.NewPG(d.Pool, d.Clock, ratelimit.Players)
	c := d.Clock
	d.LimiterTx = func(tx pgx.Tx) ratelimit.Limiter { return ratelimit.NewPG(tx, c, ratelimit.Players) }
}

// pgRisk — оценщик риска на лимитере базы с порогом 1 у LoginFailed и Signup.
func pgRisk(d *identity.Deps) {
	a, err := risk.NewAssessor(ratelimit.NewPG(d.Pool, d.Clock, ratelimit.Players),
		map[risk.Signal]ratelimit.Policy{risk.Signup: {Limit: 1, Period: time.Hour}, risk.LoginFailed: {Limit: 1, Period: time.Hour},
			risk.NewDevice: {Limit: 3, Period: time.Hour}, risk.Activity: {Limit: 120, Period: time.Minute}})
	if err != nil {
		panic(err)
	}
	d.Risk = a
}

// C1 ревью W4-А: лимит неудач второго фактора в tx1 сброса — на соединении транзакции. Пул из двух соединений: два
// подтверждения держат оба (барьер на afterRowFound), первое под замком users спрашивает лимит; на пуле оно ждало бы
// соединения, пока второе ждёт замок (без lock_timeout — вечно: tx на ctx без отмены). Все четыре завершаются за 5 с,
// успех ровно один, без 5xx.
func TestConfirmResetSmallPool(t *testing.T) {
	t.Parallel()
	e := newEnv(t, smallPool(t, 2), pgLimiters)
	uid := seedUser(t, e.owner, "reset_pool", "ru")
	setPassword(t, e, uid, "h:old")
	secret := seedMfa(t, e, uid)
	sent := startReset(t, e, "reset_pool@example.com")
	code := plainCode(t, e, sent.CodeID)
	gate := pairBarrier(2 * time.Second)
	e.a.hooks = &hooks{afterRowFound: func(string) { gate() }}
	const n = 4
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			_, errs[i] = confirmReset(e, sent, code, &SecondFactor{TOTP: totpNow(e, secret)}, resetNewPassword)
		})
	}
	began := time.Now()
	close(start)
	wg.Wait()
	if d := time.Since(began); d > 5*time.Second {
		t.Errorf("подтверждения шли %v (> 5 с): второе соединение пула внутри транзакции", d)
	}
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if p := problemOfErr(err); p.Status == 0 || p.Status >= 500 {
			t.Errorf("сбой: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("успехов %d, ждали 1: %v", ok, errs)
	}
}

// I1 ревью W4-А: лимит отправки кода смены почты — на соединении транзакции: пул из одного соединения, срок запроса
// 2 с — 202 (на пуле лимит ждал бы соединения, занятого самой tx, до срока).
func TestStartEmailChangeOneConn(t *testing.T) {
	t.Parallel()
	e := newEnv(t, smallPool(t, 1), pgLimiters)
	uid := seedUser(t, e.owner, "mail_pool", "ru")
	sid := issue(t, e, uid, web("x")).SessionID
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := e.a.StartEmailChange(ctx, sid, "mail_pool_new@example.com", web("req")); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM rate_limits WHERE key LIKE 'email:auth_send:%'`); n != 1 {
		t.Fatalf("лимит отправки не учтён: %d", n)
	}
}

// I2 ревью W4-А: код сброса на прежнюю почту после смены почты мёртв. Слой 1 — ConfirmEmailChange гасит живые коды
// аккаунта; слой 2 — почту сменили мимо сценария: адрес кода ≠ почте аккаунта — 410 без записей (tx1) и в tx2.
func TestConfirmResetAfterEmailChange(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	t.Run("смена почты гасит код", func(t *testing.T) {
		uid := seedUser(t, e.owner, "reset_moved", "ru")
		setPassword(t, e, uid, "h:old")
		sent := startReset(t, e, "reset_moved@example.com")
		code := plainCode(t, e, sent.CodeID)
		ch, err := e.a.StartEmailChange(ctx, issue(t, e, uid, web("x")).SessionID, "reset_moved_new@example.com", web("req"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.a.ConfirmEmailChange(ctx, uid, ch.CodeID, plainCode(t, e, ch.CodeID)); err != nil {
			t.Fatal(err)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND revoked_at IS NOT NULL
			AND code_enc IS NULL AND payload_enc IS NULL`, sent.CodeID); n != 1 {
			t.Fatal("код сброса не отозван сменой почты")
		}
		if _, err := confirmReset(e, sent, code, nil, resetNewPassword); problemCode(err) != identity.CodeCodeExpired {
			t.Fatalf("старый код: %v", err)
		}
		if passwordHash(t, e, uid) != "h:old" {
			t.Fatal("пароль сменён кодом прежней почты")
		}
	})
	t.Run("почта сменилась до tx1", func(t *testing.T) { // у аккаунта 2FA: 410 раньше mfa_required — фактор не тратится
		uid := seedUser(t, e.owner, "reset_moved1", "ru")
		setPassword(t, e, uid, "h:old")
		seedMfa(t, e, uid)
		sent := startReset(t, e, "reset_moved1@example.com")
		code := plainCode(t, e, sent.CodeID)
		if _, err := e.owner.Exec(ctx, `UPDATE users SET email = 'reset_moved1_x@example.com' WHERE id = $1`, uid); err != nil {
			t.Fatal(err)
		}
		if _, err := confirmReset(e, sent, code, nil, resetNewPassword); problemCode(err) != identity.CodeCodeExpired {
			t.Fatalf("код прежней почты: %v", err)
		}
		if passwordHash(t, e, uid) != "h:old" || codeAttempts(t, e, sent.CodeID) != 0 || !codeAlive(t, e, sent.CodeID) {
			t.Fatal("410 с записями или пароль сменён")
		}
	})
	t.Run("почта сменилась между фазами", func(t *testing.T) {
		uid := seedUser(t, e.owner, "reset_moved2", "ru")
		setPassword(t, e, uid, "h:old")
		sent := startReset(t, e, "reset_moved2@example.com")
		code := plainCode(t, e, sent.CodeID)
		var hookErr error
		e.a.hasher = &hookHasher{fn: func() { // между tx1 и tx2 (users не под замком)
			_, hookErr = e.owner.Exec(ctx, `UPDATE users SET email = 'reset_moved2_x@example.com' WHERE id = $1`, uid)
		}}
		_, err := confirmReset(e, sent, code, nil, resetNewPassword)
		e.a.hasher = &fakeHasher{}
		if hookErr != nil {
			t.Fatal(hookErr)
		}
		if problemCode(err) != identity.CodeCodeExpired || passwordHash(t, e, uid) != "h:old" {
			t.Fatalf("tx2 при сменённой почте: %v", err)
		}
	})
}

// hookHasher — хэшер с действием в момент хэша (между фазами сброса).
type hookHasher struct {
	fakeHasher
	fn func()
}

func (h *hookHasher) Hash(ctx context.Context, pw string) (string, error) {
	h.fn()
	return h.fakeHasher.Hash(ctx, pw)
}

// m2: неудача второго фактора учитывается в лимите user:mfa той же tx1 — и у оборванного запроса.
func TestConfirmResetFactorFailureCountedOnCancel(t *testing.T) {
	t.Parallel()
	e := newEnv(t, pgLimiters)
	uid := seedUser(t, e.owner, "reset_cancel_fail", "ru")
	secret := seedMfa(t, e, uid)
	sent := startReset(t, e, "reset_cancel_fail@example.com")
	code := plainCode(t, e, sent.CodeID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.a.ConfirmPasswordReset(ctx, ResetInput{CodeID: sent.CodeID, Code: code, NewPassword: resetNewPassword,
		MFA: &SecondFactor{TOTP: wrongCode(totpNow(e, secret))}}, web("reset-dev"))
	if problemCode(err) != identity.CodeMfaInvalid {
		t.Fatalf("неверный TOTP: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM rate_limits WHERE key = $1`, mfaFailureKey(uid)); n != 1 {
		t.Fatal("неудача фактора не учтена в лимите")
	}
}

// cancelOnHash — хэшер, который обрывает запрос в момент хэша (клиент ушёл после tx1) и, как семафор argon2,
// уважает отмену ctx.
type cancelOnHash struct {
	fakeHasher
	cancel context.CancelFunc
}

func (h *cancelOnHash) Hash(ctx context.Context, pw string) (string, error) {
	h.cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return h.fakeHasher.Hash(ctx, pw)
}

// m3: обрыв запроса после tx1 (шаг TOTP сожжён) не мешает довести смену: Hash и tx2 — на ctx без отмены со сроком.
func TestConfirmResetCompletesAfterCancel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "reset_cancel_ok", "ru")
	setPassword(t, e, uid, "h:old")
	secret := seedMfa(t, e, uid)
	sent := startReset(t, e, "reset_cancel_ok@example.com")
	code := plainCode(t, e, sent.CodeID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.a.hasher = &cancelOnHash{cancel: cancel}
	out, err := e.a.ConfirmPasswordReset(ctx, ResetInput{CodeID: sent.CodeID, Code: code, NewPassword: resetNewPassword,
		MFA: &SecondFactor{TOTP: totpNow(e, secret)}}, web("reset-dev"))
	if err != nil || out.Status != StatusAuthenticated {
		t.Fatalf("%+v %v", out, err)
	}
	if passwordHash(t, e, uid) != "h:"+resetNewPassword || codeAlive(t, e, sent.CodeID) {
		t.Fatal("смена не доведена")
	}
}

// m8, R31: ChangePassword хэширует до транзакции — во время Hash соединений пула не занято.
func TestChangePasswordHashOutsideTx(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	probe := &hashProbe{pool: e.a.pool}
	e.a.hasher = probe
	uid := seedUser(t, e.owner, "change_probe", "ru")
	sid := issue(t, e, uid, web("x")).SessionID
	if err := e.a.ChangePassword(context.Background(), sid, resetNewPassword, web("req")); err != nil {
		t.Fatal(err)
	}
	if probe.hashed.Load() != 1 || probe.acquired != 0 {
		t.Fatalf("хэшей %d, занято соединений во время Hash %d", probe.hashed.Load(), probe.acquired)
	}
}

// m8, §3.4 п. 1: риск LoginFailed по почте или по IP запроса — startPasswordReset требует PoW, ничего не создаёт.
func TestStartResetPowByRisk(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		key  func(e *env) string
	}{
		{"почта", func(e *env) string {
			keys, err := e.a.resetRiskKeys("reset_pow@example.com")
			if err != nil {
				panic(err)
			}
			return keys[0]
		}},
		{"IP", func(*env) string { return ratelimit.IPKey("login", netip.MustParseAddr("203.0.113.7")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, pgRisk)
			seedUser(t, e.owner, "reset_pow", "ru")
			for range 2 {
				if err := e.a.risk.Record(context.Background(), risk.LoginFailed, tc.key(e)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.a.StartPasswordReset(resetCtx(), "reset_pow@example.com"); problemCode(err) != httpx.CodeHumancheckRequired {
				t.Fatalf("риск %s: %v", tc.name, err)
			}
			if n := count(t, e.owner, `SELECT count(*) FROM auth_codes`); n != 0 {
				t.Fatal("код создан без PoW")
			}
		})
	}
}

// m5: адрес и UA записей — из текущего запроса, а не из строки сессии: auth_codes.ip смены почты, журнал
// password_changed.
func TestRequestClientRecorded(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "req_client", "ru")
	sid := issue(t, e, uid, web("x")).SessionID // сессия выдана с 203.0.113.7, Mozilla/5.0
	req := Client{Kind: domain.ClientWeb, IP: netip.MustParseAddr("198.51.100.77"), UserAgent: "req-agent/1"}
	sent, err := e.a.StartEmailChange(ctx, sid, "req_client_new@example.com", req)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND ip = '198.51.100.77'`, sent.CodeID); n != 1 {
		t.Fatal("auth_codes.ip не из запроса")
	}
	if err := e.a.ChangePassword(ctx, sid, resetNewPassword, req); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'password_changed'
		AND ip = '198.51.100.77' AND user_agent = 'req-agent/1'`, uid); n != 1 {
		t.Fatal("журнал password_changed не из запроса")
	}
}

// m1 ревью W4-А: три параллельных старта кода на одну почту — все успешны, живой код один.
func TestStartCodeThreeParallel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "three_live", "ru")
	const n = 3
	var ready, wg sync.WaitGroup
	ready.Add(n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			errs[i] = e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
				// у каждой tx свой снимок до вставок соседей
				if _, err := tx.Exec(ctx, `SELECT 1`); err != nil {
					return err
				}
				ready.Done()
				ready.Wait()
				_, err := e.a.startCode(ctx, tx, StartCode{Purpose: identitydb.AuthCodePurposePasswordReset,
					Email: "three_live@example.com", UserID: uid, Client: web("x")})
				return err
			})
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("старт %d: %v", i, err)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE email = 'three_live@example.com' AND consumed_at IS NULL
		AND revoked_at IS NULL`); n != 1 {
		t.Fatalf("живых кодов %d", n)
	}
}

// m4: resend_available_at не позже expires_at — поздняя повторная отправка (пауза 300 с выходит за срок кода).
func TestResendAvailableCappedByExpiry(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "resend_cap", "ru")
	sent := resetCodeOf(t, e, uid, "resend_cap@example.com", false)
	e.clock.Advance(time.Minute)
	if _, err := e.a.ResendCode(context.Background(), sent.CodeID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(12 * time.Minute) // 13-я минута: следующая пауза 300 с кончилась бы после 15-й
	got, err := e.a.ResendCode(context.Background(), sent.CodeID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ResendAvailable.Equal(sent.ExpiresAt) {
		t.Fatalf("resend_available_at %v, ждали expires_at %v", got.ResendAvailable, sent.ExpiresAt)
	}
}

// m6, §3.16: принятые при старте версии устарели к подтверждению — documents_acceptance_required; текущие — нет.
// m9: сигнал риска Signup учитывается на подтверждении (созданный аккаунт), не на старте.
func TestConfirmSignupDocumentsAndRisk(t *testing.T) {
	t.Parallel()
	e := newEnv(t, pgLimiters, func(d *identity.Deps) {
		a, err := risk.NewAssessor(ratelimit.NewPG(d.Pool, d.Clock, ratelimit.Players), risk.DefaultPolicies())
		if err != nil {
			panic(err)
		}
		d.Risk = a
	})
	city := seedSignupCity(t, e, "XS", true, 14)
	c := mobile("ios", "docs-dev")
	riskKey := "risk:" + string(risk.Signup) + ":" + risk.Subnet(c.IP)
	sent, code := startAndCode(t, e, "docs_cur@example.com", "docs_cur", city, c)
	if n := count(t, e.owner, `SELECT count(*) FROM rate_limits WHERE key = $1`, riskKey); n != 0 {
		t.Fatal("риск Signup учтён на старте")
	}
	out, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: sent.CodeID, Code: code}, c)
	if err != nil || out.DocumentsRequired {
		t.Fatalf("текущие версии: %+v %v", out, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM rate_limits WHERE key = $1`, riskKey); n != 1 {
		t.Fatal("риск Signup не учтён на подтверждении")
	}
	c2 := mobile("ios", "docs-dev2")
	c2.IP = netip.MustParseAddr("192.0.2.10") // другая подсеть: риск первой регистрации не требует PoW
	sent, code = startAndCode(t, e, "docs_old@example.com", "docs_old", city, c2)
	newer, err := identity.ParseLegalDocuments("terms=2026-12-01|https://example.com/terms,privacy=2026-11-01|https://example.com/privacy")
	if err != nil {
		t.Fatal(err)
	}
	e.a.legal = newer
	out, err = e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: sent.CodeID, Code: code}, c2)
	if err != nil || !out.DocumentsRequired {
		t.Fatalf("устаревшие версии: %+v %v", out, err)
	}
}
