package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/otp"
)

// seedMfa — подтверждённая 2FA аккаунта: секрет TOTP (Seal, aad = id аккаунта) и резервные коды (HMAC).
func seedMfa(t *testing.T, e *env, uid uuid.UUID, recovery ...string) []byte {
	t.Helper()
	secret, err := otp.NewSecret(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ct, keyID, err := e.a.secrets.Seal(secret, uid[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.owner.Exec(context.Background(), `INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at)
		VALUES ($1, $2, $3, $4, $4)`, uid, ct, keyID, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	for _, c := range recovery {
		norm, ok := otp.NormalizeRecoveryCode(c)
		if !ok {
			t.Fatalf("резервный код %q", c)
		}
		mac, err := e.a.recoveryMAC(e.a.secrets.CurrentID(), uid, norm)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.owner.Exec(context.Background(), `INSERT INTO user_recovery_codes (user_id, code_mac, key_id, created_at)
			VALUES ($1, $2, $3, $4)`, uid, mac, e.a.secrets.CurrentID(), e.clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return secret
}

// verify — verifySecondFactor в своей tx; неудача коммитится (образец вызывающего: ошибка — после коммита).
func verify(t *testing.T, e *env, uid uuid.UUID, f SecondFactor) (string, error) {
	t.Helper()
	var method string
	var verr error
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := e.a.lockUser(ctx, tx, uid); err != nil {
			return err
		}
		method, verr = e.a.verifySecondFactor(ctx, tx, uid, f)
		if errors.Is(verr, errFactorInvalid) {
			return nil // неудача сохраняется
		}
		return verr
	}); err != nil {
		t.Fatal(err)
	}
	return method, verr
}

// Повтор TOTP того же шага и код шага старше принятого — отказ (last_used_step).
func TestVerifyTOTPReplay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "totp_replay", "ru")
	secret := seedMfa(t, e, uid)
	now := e.clock.Now()
	code := otp.Code(secret, otp.Step(now))
	if m, err := verify(t, e, uid, SecondFactor{TOTP: code}); err != nil || m != "totp" {
		t.Fatalf("первый: %q %v", m, err)
	}
	if _, err := verify(t, e, uid, SecondFactor{TOTP: code}); !errors.Is(err, errFactorInvalid) {
		t.Fatalf("повтор: %v", err)
	}
	if _, err := verify(t, e, uid, SecondFactor{TOTP: otp.Code(secret, otp.Step(now)-1)}); !errors.Is(err, errFactorInvalid) {
		t.Fatalf("шаг старше принятого: %v", err)
	}
	if n := count(t, e.owner, `SELECT last_used_step FROM user_mfa WHERE user_id = $1`, uid); int64(n) != otp.Step(now) {
		t.Fatalf("last_used_step = %d", n)
	}
}

// Резервный код сгорает при использовании; код с ключом вне кольца — отказ без 500; письмо recovery_code_used.
func TestVerifyRecoveryBurns(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "recovery_burn", "ru")
	seedMfa(t, e, uid, "abcde-fghij", "klmno-pqrst")
	if m, err := verify(t, e, uid, SecondFactor{Recovery: "ABCDE FGHIJ"}); err != nil || m != "recovery_code" {
		t.Fatalf("первый: %q %v", m, err)
	}
	if _, err := verify(t, e, uid, SecondFactor{Recovery: "abcde-fghij"}); !errors.Is(err, errFactorInvalid) {
		t.Fatalf("повтор: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send' AND args->>'mail' = $1`, MailRecoveryCodeUsed); n != 1 {
		t.Fatalf("писем recovery_code_used %d", n)
	}
	var left string
	if err := e.owner.QueryRow(context.Background(), `SELECT meta->>'left' FROM security_events WHERE user_id = $1 AND kind = 'recovery_code_used'`,
		uid).Scan(&left); err != nil || left != "1" {
		t.Fatalf("осталось %q %v", left, err)
	}
	// ключ строки убран из кольца — код не проверяется, но и не 500
	e.a.secrets = newRing(t, genKey(t))
	if _, err := verify(t, e, uid, SecondFactor{Recovery: "klmno-pqrst"}); !errors.Is(err, errFactorInvalid) {
		t.Fatalf("ключ вне кольца: %v", err)
	}
}

// R11: оба поля или ни одного — 400 validation.failed по полю body.
func TestVerifyBothFactorsRejected(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "both_factors", "ru")
	seedMfa(t, e, uid, "abcde-fghij")
	for _, f := range []SecondFactor{{TOTP: "123456", Recovery: "abcde-fghij"}, {}} {
		err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
			_, err := e.a.verifySecondFactor(ctx, tx, uid, f)
			return err
		})
		var pe httpx.ProblemError
		if !errors.As(err, &pe) || pe.Problem().Code != httpx.CodeValidationFailed || len(pe.Problem().Errors) != 1 ||
			pe.Problem().Errors[0].Field != "body" {
			t.Fatalf("%+v: %v", f, err)
		}
	}
	if n := count(t, e.owner, `SELECT failures FROM user_mfa WHERE user_id = $1`, uid); n != 0 {
		t.Fatalf("негодный запрос посчитан неудачей: %d", n)
	}
}

// §3.3 п. 3: пятая неудача подряд — одно письмо mfa_failures; ещё пять в тот же день — без письма; успех обнуляет.
func TestMfaFailuresMailOncePerDay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "mfa_failures", "ru")
	secret := seedMfa(t, e, uid)
	mails := func() int {
		return count(t, e.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send' AND args->>'mail' = $1`, MailMfaFailures)
	}
	fail := func(n int) {
		for range n {
			if _, err := verify(t, e, uid, SecondFactor{TOTP: "000000"}); !errors.Is(err, errFactorInvalid) {
				t.Fatal(err)
			}
		}
	}
	fail(4)
	if mails() != 0 {
		t.Fatal("письмо до пятой неудачи")
	}
	fail(1)
	if mails() != 1 || count(t, e.owner, `SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'mfa_failures'`, uid) != 1 {
		t.Fatalf("после пятой: писем %d", mails())
	}
	e.clock.Advance(time.Minute)
	if _, err := verify(t, e, uid, SecondFactor{TOTP: otp.Code(secret, otp.Step(e.clock.Now()))}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT failures FROM user_mfa WHERE user_id = $1`, uid); n != 0 {
		t.Fatalf("успех не обнулил: %d", n)
	}
	fail(5)
	if mails() != 1 {
		t.Fatalf("второе письмо в тот же день: %d", mails())
	}
	e.clock.Advance(24 * time.Hour)
	fail(5)
	if mails() != 2 {
		t.Fatalf("через сутки: писем %d", mails())
	}
}

// m6 ревью W3: ключ секрета TOTP убран из кольца раньше rekey — фактор не проверить по вине сервера: отказ
// errFactorInvalid и Warn в журнал, но неудача не засчитывается и письмо mfa_failures не уходит (даже на «пятой»).
func TestVerifyTOTPKeyOutOfRing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var buf bytes.Buffer
	e.a.log = slog.New(slog.NewTextHandler(&buf, nil))
	uid := seedUser(t, e.owner, "key_out_of_ring", "ru")
	seedMfa(t, e, uid)
	if _, err := e.owner.Exec(context.Background(), `UPDATE user_mfa SET key_id = 'gonekey0000', failures = 4 WHERE user_id = $1`,
		uid); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(t, e, uid, SecondFactor{TOTP: "000000"}); !errors.Is(err, errFactorInvalid) {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT failures FROM user_mfa WHERE user_id = $1`, uid); n != 4 {
		t.Fatalf("неудача по вине сервера засчитана: failures = %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send' AND args->>'mail' = $1`, MailMfaFailures); n != 0 {
		t.Fatalf("письмо mfa_failures: %d", n)
	}
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "gonekey0000") {
		t.Fatalf("нет Warn с ключом: %q", buf.String())
	}
}

// Ловушка Task 9: неудача второго фактора сохраняется, только если вызывающий коммитит tx и отдаёт ошибку после
// коммита; вернуть ошибку из db.InTx — откат счётчика.
func TestFactorFailurePersists(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "failure_persists", "ru")
	seedMfa(t, e, uid)
	if _, err := verify(t, e, uid, SecondFactor{TOTP: "000000"}); !errors.Is(err, errFactorInvalid) {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT failures FROM user_mfa WHERE user_id = $1`, uid); n != 1 {
		t.Fatalf("закоммиченная неудача: failures = %d", n)
	}
	err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := e.a.verifySecondFactor(ctx, tx, uid, SecondFactor{TOTP: "000000"})
		return err
	})
	if !errors.Is(err, errFactorInvalid) {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT failures FROM user_mfa WHERE user_id = $1`, uid); n != 1 {
		t.Fatalf("ошибка из InTx откатила бы неудачу, а failures = %d", n)
	}
}
