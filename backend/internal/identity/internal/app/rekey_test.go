package app

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/identity"
	"wf/backend/internal/platform/keys"
)

// rekeyFixture — аккаунты с секретами под разными ключами кольца: old — прежний текущий (остался в кольце вторым),
// cur — новый текущий, gone — убран из кольца раньше rekey (строка нечитаема).
type rekeyFixture struct {
	e                  *env
	ra                 *App // NewMaintenance под ролью api с кольцом [cur, old]
	rotated            *keys.Ring
	oldID, curID       string
	goneID             string
	curKey             string    // закодированный текущий ключ (кольцо без старого)
	confirmed, pending uuid.UUID // 2FA под old: подтверждённая и неподтверждённая
	onCur, unreadable  uuid.UUID // 2FA под cur (не трогается) и под gone
	apple, google      uuid.UUID // привязки: Apple с refresh под old, Google без шифротекста
}

func newRekeyFixture(t *testing.T) *rekeyFixture {
	t.Helper()
	oldKey, curKey, goneKey := genKey(t), genKey(t), genKey(t)
	e := newEnv(t, func(d *identity.Deps) { d.Secrets = newRing(t, oldKey) })
	f := &rekeyFixture{e: e, rotated: newRing(t, curKey, oldKey), curKey: curKey}
	f.oldID, f.curID, f.goneID = e.a.secrets.CurrentID(), f.rotated.CurrentID(), newRing(t, goneKey).CurrentID()
	ra, err := NewMaintenance(e.a.pool, e.clock, f.rotated, e.a.river, e.a.log)
	if err != nil {
		t.Fatal(err)
	}
	f.ra = ra
	ctx := context.Background()
	f.confirmed = seedUser(t, e.owner, "rk_confirmed", "ru")
	f.pending = seedUser(t, e.owner, "rk_pending", "ru")
	f.onCur = seedUser(t, e.owner, "rk_oncur", "ru")
	f.unreadable = seedUser(t, e.owner, "rk_gone", "ru")
	seedRekeyMfa(t, e.owner, e.a.secrets, f.confirmed, "secret-confirmed", true)
	seedRekeyMfa(t, e.owner, e.a.secrets, f.pending, "secret-pending", false)
	seedRekeyMfa(t, e.owner, f.rotated, f.onCur, "secret-oncur", true)
	seedRekeyMfa(t, e.owner, newRing(t, goneKey), f.unreadable, "secret-gone", true)
	// привязки: Apple с refresh-токеном (aad — id привязки) и Google без шифротекста (не трогается и не считается)
	f.apple, f.google = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	ct, kid, err := e.a.secrets.Seal([]byte("apple-refresh"), f.apple[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.owner.Exec(ctx, `INSERT INTO user_identities (id, user_id, provider, subject, email_is_private_relay,
		apple_refresh_enc, key_id, linked_at) VALUES ($1, $2, 'apple', 'apple-sub', false, $3, $4, now()),
		($5, $2, 'google', 'google-sub', false, NULL, NULL, now())`, f.apple, f.confirmed, ct, kid, f.google); err != nil {
		t.Fatal(err)
	}
	// резервные коды: два живых и один использованный под old, один живой под cur — MAC не пересчитываются
	if _, err := e.owner.Exec(ctx, `INSERT INTO user_recovery_codes (user_id, code_mac, key_id, used_at, created_at)
		VALUES ($1, decode(repeat('01', 32), 'hex'), $2, NULL, now()), ($1, decode(repeat('02', 32), 'hex'), $2, NULL, now()),
		       ($1, decode(repeat('03', 32), 'hex'), $2, now(), now()), ($3, decode(repeat('04', 32), 'hex'), $4, NULL, now())`,
		f.confirmed, f.oldID, f.onCur, f.curID); err != nil {
		t.Fatal(err)
	}
	return f
}

// seedRekeyMfa — строка user_mfa с секретом, запечатанным кольцом ring (aad — id аккаунта).
func seedRekeyMfa(t *testing.T, owner *pgxpool.Pool, ring *keys.Ring, userID uuid.UUID, secret string, confirmed bool) {
	t.Helper()
	ct, kid, err := ring.Seal([]byte(secret), userID[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(context.Background(), `INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at)
		VALUES ($1, $2, $3, CASE WHEN $4 THEN now() END, now())`, userID, ct, kid, confirmed); err != nil {
		t.Fatal(err)
	}
}

// secretRow — шифротекст и ключ строки, как в базе.
type secretRow struct {
	enc string // шифротекст hex
	key string
}

// snapshot — все шифротексты 2FA, привязок и резервных кодов (сверка «ничего не записано»).
func (f *rekeyFixture) snapshot(t *testing.T) map[string]secretRow {
	t.Helper()
	rows, err := f.e.owner.Query(context.Background(), `
		SELECT 'mfa:' || user_id, encode(secret_enc, 'hex'), key_id FROM user_mfa
		UNION ALL SELECT 'idt:' || id, coalesce(encode(apple_refresh_enc, 'hex'), ''), coalesce(key_id, '') FROM user_identities
		UNION ALL SELECT 'rc:' || user_id || encode(code_mac, 'hex'), encode(code_mac, 'hex'), key_id FROM user_recovery_codes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]secretRow{}
	for rows.Next() {
		var k string
		var r secretRow
		if err := rows.Scan(&k, &r.enc, &r.key); err != nil {
			t.Fatal(err)
		}
		out[k] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// openMfa — секрет TOTP аккаунта кольцом ring и его key_id.
func (f *rekeyFixture) openMfa(t *testing.T, ring *keys.Ring, userID uuid.UUID) (string, string) {
	t.Helper()
	var enc []byte
	var kid string
	if err := f.e.owner.QueryRow(context.Background(), `SELECT secret_enc, key_id FROM user_mfa WHERE user_id = $1`,
		userID).Scan(&enc, &kid); err != nil {
		t.Fatal(err)
	}
	pt, err := ring.Open(enc, kid, userID[:])
	if err != nil {
		t.Fatalf("секрет %s не открылся: %v", userID, err)
	}
	return string(pt), kid
}

func TestRekeyRotatesLongLivedSecrets(t *testing.T) {
	t.Parallel()
	f := newRekeyFixture(t)
	ctx := context.Background()
	before := f.snapshot(t)
	// пачка 1 — курсор проходит все строки, а не только первую пачку
	rep, err := f.ra.rekey(ctx, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rep.CurrentKey != f.curID || rep.MFA != 2 || rep.Apple != 1 {
		t.Fatalf("перешифровано: ключ %s, 2FA %d, Apple %d; want %s, 2, 1", rep.CurrentKey, rep.MFA, rep.Apple, f.curID)
	}
	wantMfaBefore := map[string]int{f.oldID: 2, f.curID: 1, f.goneID: 1}
	wantMfaAfter := map[string]int{f.curID: 3, f.goneID: 1}
	if !maps.Equal(rep.MFABefore, wantMfaBefore) || !maps.Equal(rep.MFAAfter, wantMfaAfter) {
		t.Fatalf("2FA по ключам: до %v, после %v; want %v, %v", rep.MFABefore, rep.MFAAfter, wantMfaBefore, wantMfaAfter)
	}
	if !maps.Equal(rep.AppleBefore, map[string]int{f.oldID: 1}) || !maps.Equal(rep.AppleAfter, map[string]int{f.curID: 1}) {
		t.Fatalf("Apple по ключам: до %v, после %v", rep.AppleBefore, rep.AppleAfter)
	}
	// живые резервные коды под старым ключом — в отчёте; использованный и под текущим — нет
	if !maps.Equal(rep.RecoveryOldKeys, map[string]int{f.oldID: 2}) {
		t.Fatalf("резервные коды под старыми ключами: %v", rep.RecoveryOldKeys)
	}
	if !slices.Equal(rep.UnreadableMFA, []uuid.UUID{f.unreadable}) || len(rep.UnreadableApple) != 0 {
		t.Fatalf("нечитаемые: 2FA %v, Apple %v", rep.UnreadableMFA, rep.UnreadableApple)
	}
	if rep.Clean() {
		t.Fatal("Clean при нечитаемой строке и кодах под старым ключом")
	}
	// перешифрованные открываются новым кольцом и только текущим ключом (старый ключ кольцу больше не нужен)
	curOnly := newRing(t, f.curKey)
	for id, want := range map[uuid.UUID]string{f.confirmed: "secret-confirmed", f.pending: "secret-pending", f.onCur: "secret-oncur"} {
		if got, kid := f.openMfa(t, curOnly, id); got != want || kid != f.curID {
			t.Fatalf("2FA %s: %q под %s; want %q под %s", id, got, kid, want, f.curID)
		}
	}
	var appleEnc []byte
	var appleKey string
	if err := f.e.owner.QueryRow(ctx, `SELECT apple_refresh_enc, key_id FROM user_identities WHERE id = $1`, f.apple).
		Scan(&appleEnc, &appleKey); err != nil {
		t.Fatal(err)
	}
	if pt, err := curOnly.Open(appleEnc, appleKey, f.apple[:]); err != nil || string(pt) != "apple-refresh" {
		t.Fatalf("Apple refresh: %q, %v", pt, err)
	}
	after := f.snapshot(t)
	// не тронуты: строка под текущим ключом, нечитаемая, Google, все резервные коды
	for k, b := range before {
		a := after[k]
		changed := a.enc != b.enc || a.key != b.key
		shouldChange := k == "mfa:"+f.confirmed.String() || k == "mfa:"+f.pending.String() || k == "idt:"+f.apple.String()
		if changed != shouldChange {
			t.Fatalf("%s: изменена %v, ожидалось %v", k, changed, shouldChange)
		}
	}
	// повтор — нечего перешифровывать, запись не меняется
	rep2, err := f.ra.rekey(ctx, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.MFA != 0 || rep2.Apple != 0 || !maps.Equal(f.snapshot(t), after) {
		t.Fatalf("повтор: 2FA %d, Apple %d, база изменилась", rep2.MFA, rep2.Apple)
	}
}

func TestRekeyDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	f := newRekeyFixture(t)
	before := f.snapshot(t)
	rep, err := f.ra.Rekey(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(f.snapshot(t), before) {
		t.Fatal("--dry-run изменил базу")
	}
	// что было бы сделано: те же счётчики и нечитаемые, после — нет
	if !rep.DryRun || rep.MFA != 2 || rep.Apple != 1 || !slices.Equal(rep.UnreadableMFA, []uuid.UUID{f.unreadable}) ||
		rep.MFAAfter != nil || rep.AppleAfter != nil {
		t.Fatalf("отчёт --dry-run: %+v", rep)
	}
	if !maps.Equal(rep.RecoveryOldKeys, map[string]int{f.oldID: 2}) {
		t.Fatalf("резервные коды: %v", rep.RecoveryOldKeys)
	}
}

// TestRekeyKeepsConcurrentWrite — строку сменили между чтением пачки и записью (экземпляр прежнего релиза при
// скользящем деплое запечатал новый секрет старым ключом): rekey её не перетирает старым секретом.
func TestRekeyKeepsConcurrentWrite(t *testing.T) {
	t.Parallel()
	f := newRekeyFixture(t)
	fresh, freshKey, err := f.e.a.secrets.Seal([]byte("secret-fresh"), f.confirmed[:])
	if err != nil {
		t.Fatal(err)
	}
	f.ra.hooks = &hooks{afterRowFound: func(kind string) {
		if kind != rowRekeyMfa {
			return
		}
		f.ra.hooks = nil // один раз
		if _, err := f.e.owner.Exec(context.Background(), `UPDATE user_mfa SET secret_enc = $2, key_id = $3 WHERE user_id = $1`,
			f.confirmed, fresh, freshKey); err != nil {
			t.Error(err)
		}
	}}
	rep, err := f.ra.Rekey(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got, kid := f.openMfa(t, f.rotated, f.confirmed); got != "secret-fresh" || kid != f.oldID {
		t.Fatalf("строку перетёр rekey: %q под %s", got, kid)
	}
	// не перешифрована — значит, в «после» она под старым ключом
	if rep.MFA != 1 || rep.MFAAfter[f.oldID] != 1 {
		t.Fatalf("отчёт: 2FA %d, после %v", rep.MFA, rep.MFAAfter)
	}
}

func TestRekeyRequiresSecrets(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ra, err := NewMaintenance(e.a.pool, e.clock, nil, e.a.river, e.a.log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ra.Rekey(context.Background(), false); !errors.Is(err, errNoSecrets) {
		t.Fatalf("Rekey без кольца: %v; want errNoSecrets", err)
	}
}
