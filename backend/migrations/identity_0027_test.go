package migrations_test

import (
	"database/sql"
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

// identityCaptaincyVersion — 0027_identity_captaincy.sql (план I-4, Task 1): статистика способов входа для admin-api,
// индексы очереди и истории заявок, banned в status_before_deletion (R11), живой код смены почты по
// (purpose, user_id, email) (R40).
const identityCaptaincyVersion = 27

// liveCode — живой настоящий код назначения purpose на почту email аккаунта user ($1 purpose, $2 email, $3 user_id).
const liveCode = `INSERT INTO auth_codes (id, purpose, email, user_id, decoy, code_mac, key_id, last_sent_at, expires_at, ip,
	created_at) VALUES (gen_random_uuid(), $1::auth_code_purpose, $2, $3, false, decode(repeat('02', 32), 'hex'), '` + keyID + `',
	now(), now() + interval '15 minutes', '203.0.113.7', now())`

// banPendingDeletion — бан поверх удаления (R11): pending_deletion с прежним статусом banned.
const banPendingDeletion = `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'banned',
	deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`

// Накат 0027 на схеме 0026 и откат: откат возвращает схему 0026 в точности; при строке status_before_deletion = banned
// и при двух живых кодах email_change на одну почту — отказ с понятным текстом, версия остаётся 0027.
func TestIdentity0027UpDown(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	if err := migrateTo(t, db, identityDeletionVersion); err != nil {
		t.Fatalf("down до 0026: %v", err)
	}
	before := snapshotOf(t, db, identitySnapshotSQL)
	if err := migrateTo(t, db, identityCaptaincyVersion); err != nil {
		t.Fatalf("up 0027: %v", err)
	}
	if v := dbVersion(t, db); v != identityCaptaincyVersion {
		t.Fatalf("версия после наката = %d", v)
	}
	up := snapshotOf(t, db, identitySnapshotSQL)
	for _, want := range []string{
		"rel v identity_read_auth_method_usage",
		"idx CREATE INDEX captain_applications_status_idx ON public.captain_applications USING btree (status, created_at, id)",
		"idx CREATE INDEX captain_applications_user_idx ON public.captain_applications USING btree (user_id, created_at DESC)",
		"idx CREATE UNIQUE INDEX auth_codes_live_key ON public.auth_codes USING btree (purpose, email) WHERE ((purpose <> 'email_change'::auth_code_purpose) AND (consumed_at IS NULL) AND (revoked_at IS NULL))",
		"idx CREATE UNIQUE INDEX auth_codes_live_email_change_key ON public.auth_codes USING btree (purpose, user_id, email) WHERE ((purpose = 'email_change'::auth_code_purpose) AND (consumed_at IS NULL) AND (revoked_at IS NULL))",
	} {
		if !slices.Contains(up, want) {
			t.Errorf("после 0027 нет %q", want)
		}
	}

	banned := identityUser(t, db, "banned@example.com", "banned_one")
	execArgs(t, db, banPendingDeletion, banned)
	a := identityUser(t, db, "first@example.com", "first_one")
	b := identityUser(t, db, "second@example.com", "second_one")
	execArgs(t, db, liveCode, "email_change", "shared@example.com", a)
	execArgs(t, db, liveCode, "email_change", "shared@example.com", b)

	wantDownRefused(t, db, "users: строк со status_before_deletion = banned — 1")
	execArgs(t, db, `UPDATE users SET status_before_deletion = 'active' WHERE id = $1`, banned)
	wantDownRefused(t, db, "auth_codes: почт с несколькими живыми кодами смены почты — 1")
	execArgs(t, db, `UPDATE auth_codes SET revoked_at = now() WHERE user_id = $1`, b)

	if err := migrateTo(t, db, identityDeletionVersion); err != nil {
		t.Fatalf("down 0027: %v", err)
	}
	after := snapshotOf(t, db, identitySnapshotSQL)
	if extra, lost := missingFrom(after, before), missingFrom(before, after); len(extra) != 0 || len(lost) != 0 {
		t.Fatalf("откат 0027 не вернул схему 0026:\nлишнее: %q\nпропало: %q", extra, lost)
	}
	if err := migrateTo(t, db, identityCaptaincyVersion); err != nil {
		t.Fatalf("повторный up 0027: %v", err)
	}
	if again := snapshotOf(t, db, identitySnapshotSQL); !slices.Equal(again, up) {
		t.Fatalf("повторный накат дал другую схему:\nлишнее: %q\nпропало: %q", missingFrom(again, up), missingFrom(up, again))
	}
}

// wantDownRefused — откат 0027 отказывает с текстом want, версия остаётся 0027.
func wantDownRefused(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	err := migrateTo(t, db, identityDeletionVersion)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("ждали отказ %q, получили: %v", want, err)
	}
	if v := dbVersion(t, db); v != identityCaptaincyVersion {
		t.Fatalf("версия после отказа = %d", v)
	}
}

// R11: бан поверх удаления — status_before_deletion = banned допустим; deleted и pending_deletion — нет.
func TestStatusBeforeDeletionAllowsBanned(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	u := identityUser(t, db, "ban_keep@example.com", "ban_keep")
	for _, v := range []string{"deleted", "pending_deletion"} {
		wantArgsError(t, db, "23514", "users_status_before_deletion", `UPDATE users SET status = 'pending_deletion',
			status_before_deletion = $2::user_status, deletion_requested_at = now(), purge_at = now() + interval '30 days'
			WHERE id = $1`, u, v)
	}
	execArgs(t, db, banPendingDeletion, u)
}

// R40: живой код смены почты — один на (аккаунт, почта): два аккаунта на один адрес — можно, второй живой у того же
// аккаунта — 23505 auth_codes_live_email_change_key; прочие назначения — по-прежнему один на (purpose, email).
func TestEmailChangeLiveCodeIndex(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	a := identityUser(t, db, "idx_a@example.com", "idx_a")
	b := identityUser(t, db, "idx_b@example.com", "idx_b")
	execArgs(t, db, liveCode, "email_change", "idx_new@example.com", a)
	execArgs(t, db, liveCode, "email_change", "idx_new@example.com", b)
	wantArgsError(t, db, "23505", "auth_codes_live_email_change_key", liveCode, "email_change", "idx_new@example.com", a)
	execArgs(t, db, liveCode, "password_reset", "idx_a@example.com", a)
	wantArgsError(t, db, "23505", "auth_codes_live_key", liveCode, "password_reset", "idx_a@example.com", b)
}

// Статистика способов входа (R2): A — пароль + Apple, B — только Apple, C — только ключ доступа, D — удалён, с Apple
// (не учитывается); строк ровно три, google — нули.
func TestAuthMethodUsageView(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	const usage = `SELECT string_agg(method || ' ' || only_method || ' ' || linked, ',' ORDER BY method)
		FROM identity_read_auth_method_usage`
	if got := queryString(t, db, usage); got != "apple 0 0,google 0 0,passkeys 0 0" {
		t.Fatalf("пустая база: %q", got)
	}
	identity := `INSERT INTO user_identities (id, user_id, provider, subject, email_is_private_relay, key_id, linked_at)
		VALUES (gen_random_uuid(), $1::uuid, 'apple', $1::text, false, '` + keyID + `', now())`
	passkey := `INSERT INTO passkeys (id, user_id, credential_id, public_key, aaguid, sign_count, transports, backup_eligible,
		backup_state, attestation_format, name, created_at) VALUES (gen_random_uuid(), $1::uuid, decode(md5($1::text), 'hex'),
		decode('a501', 'hex'), gen_random_uuid(), 0, '{internal}', true, true, 'none', 'iPhone', now())`
	ua := identityUser(t, db, "usage_a@example.com", "usage_a")
	execArgs(t, db, `INSERT INTO credentials (user_id, password_hash, updated_at) VALUES ($1, 'h', now())`, ua)
	execArgs(t, db, identity, ua)
	ub := identityUser(t, db, "usage_b@example.com", "usage_b")
	execArgs(t, db, identity, ub)
	uc := identityUser(t, db, "usage_c@example.com", "usage_c")
	execArgs(t, db, passkey, uc)
	ud := identityUser(t, db, "usage_d@example.com", "usage_d")
	execArgs(t, db, identity, ud)
	execArgs(t, db, `UPDATE users SET status = 'deleted', deleted_at = now(), birth_date = NULL WHERE id = $1`, ud)
	if got := queryString(t, db, usage); got != "apple 1 2,google 0 0,passkeys 1 1" {
		t.Fatalf("статистика: %q", got)
	}
}

// Представление — только агрегаты: колонки ровно method, only_method, linked (ни одного user_id).
func TestAuthMethodUsageNoUserIDs(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	got := queryString(t, db, `SELECT string_agg(column_name || ' ' || data_type, ',' ORDER BY ordinal_position)
		FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'identity_read_auth_method_usage'`)
	if got != "method text,only_method bigint,linked bigint" {
		t.Fatalf("колонки: %q", got)
	}
}
