package rekey_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	identityrekey "wf/backend/internal/identity/rekey"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

func ring(t *testing.T, encoded ...string) *keys.Ring {
	t.Helper()
	r, err := keys.NewRing(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func genKey(t *testing.T) string {
	t.Helper()
	k, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// mfaRow — шифротекст и ключ секрета TOTP аккаунта.
func mfaRow(t *testing.T, owner *pgxpool.Pool, userID uuid.UUID) (string, string) {
	t.Helper()
	var enc, kid string
	if err := owner.QueryRow(context.Background(), `SELECT encode(secret_enc, 'hex'), key_id FROM user_mfa WHERE user_id = $1`,
		userID).Scan(&enc, &kid); err != nil {
		t.Fatal(err)
	}
	return enc, kid
}

// TestRekey — сборщик команды api identity rekey под ролью api (I4): секрет под старым ключом после прогона — под
// текущим и открывается; --dry-run ничего не пишет; резервные коды под старым ключом — в отчёте и не изменены.
func TestRekey(t *testing.T) {
	t.Parallel()
	pools := dbtest.NewPoolsAs(t, "api")
	ctx := context.Background()
	oldKey, curKey := genKey(t), genKey(t)
	old, rotated := ring(t, oldKey), ring(t, curKey, oldKey)
	var userID uuid.UUID
	if err := pools.Owner.QueryRow(ctx, `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ('rekey@example.com', now(), 'rekey_user', '2000-01-01', 'ru') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	ct, kid, err := old.Seal([]byte("totp-secret"), userID[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at)
		VALUES ($1, $2, $3, now(), now())`, userID, ct, kid); err != nil {
		t.Fatal(err)
	}
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO user_recovery_codes (user_id, code_mac, key_id, created_at)
		VALUES ($1, decode(repeat('0a', 32), 'hex'), $2, now())`, userID, kid); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pools.As), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	d := identityrekey.Deps{Pool: pools.As, Clock: clocktest.New(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)),
		Secrets: rotated, River: ins, Log: log}
	recoveryBefore := count(t, pools.Owner, `SELECT count(*) FROM user_recovery_codes WHERE user_id = $1 AND key_id = $2`, userID, kid)

	encBefore, _ := mfaRow(t, pools.Owner, userID)
	dry, err := identityrekey.Run(ctx, d, true)
	if err != nil {
		t.Fatal(err)
	}
	if enc, k := mfaRow(t, pools.Owner, userID); enc != encBefore || k != kid || !dry.DryRun || dry.MFA != 1 {
		t.Fatalf("--dry-run: записал (%v) или отчёт %+v", enc != encBefore || k != kid, dry)
	}

	rep, err := identityrekey.Run(ctx, d, false)
	if err != nil {
		t.Fatal(err)
	}
	enc, k := mfaRow(t, pools.Owner, userID)
	if k != rotated.CurrentID() || rep.MFA != 1 || rep.MFAAfter[rotated.CurrentID()] != 1 {
		t.Fatalf("после rekey: ключ %s, отчёт %+v", k, rep)
	}
	var raw []byte
	if err := pools.Owner.QueryRow(ctx, `SELECT decode($1, 'hex')`, enc).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if pt, err := ring(t, curKey).Open(raw, k, userID[:]); err != nil || string(pt) != "totp-secret" {
		t.Fatalf("секрет под текущим ключом: %q, %v", pt, err)
	}
	if rep.RecoveryOldKeys[kid] != 1 || rep.Clean() {
		t.Fatalf("резервные коды под старым ключом в отчёте: %v, Clean %v", rep.RecoveryOldKeys, rep.Clean())
	}
	if n := count(t, pools.Owner, `SELECT count(*) FROM user_recovery_codes WHERE user_id = $1 AND key_id = $2`, userID, kid); n != recoveryBefore {
		t.Fatalf("резервные коды изменены: %d → %d", recoveryBefore, n)
	}
}

func count(t *testing.T, owner *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
