package migrations_test

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"strings"
	"testing"

	"wf/backend/internal/platform/emailaddr"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Версии миграций identity (спека identity §5.1): 0024 — значение pending_deletion без транзакции, 0025 — всё
// остальное одной транзакцией.
const (
	identityStatusVersion = 24
	identityVersion       = 25
)

// identitySnapshotSQL — вся схема public без объектов расширений: колонки всех таблиц и представлений, индексы,
// ограничения, триггеры, значения enum-типов, функции (с телом), определения представлений и список отношений.
// 0025 задевает users, sessions, role_assignments, captain_applications, feature_flags и заводит новые объекты —
// снимок целиком ловит и забытое в down, и лишнее.
const identitySnapshotSQL = `
SELECT 'col ' || c.relname || '.' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
       || CASE WHEN a.attnotnull THEN ' not null' ELSE '' END
       || CASE a.attgenerated WHEN 's' THEN ' generated' ELSE '' END
       || coalesce(' default ' || pg_get_expr(d.adbin, d.adrelid), '')
  FROM pg_attribute a
  JOIN pg_class c ON c.oid = a.attrelid AND c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p', 'v', 'm')
  LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
 WHERE a.attnum > 0 AND NOT a.attisdropped
   AND NOT EXISTS (SELECT 1 FROM pg_depend dp WHERE dp.objid = c.oid AND dp.deptype = 'e')
UNION ALL
SELECT 'idx ' || indexdef FROM pg_indexes WHERE schemaname = 'public'
UNION ALL
SELECT 'con ' || conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid)
  FROM pg_constraint WHERE connamespace = 'public'::regnamespace AND conrelid <> 0
UNION ALL
SELECT 'trg ' || tg.tgrelid::regclass::text || ' ' || tg.tgname
  FROM pg_trigger tg JOIN pg_class c ON c.oid = tg.tgrelid
 WHERE NOT tg.tgisinternal AND c.relnamespace = 'public'::regnamespace
UNION ALL
SELECT 'type ' || ty.typname || ' ' || string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder)
  FROM pg_type ty JOIN pg_enum e ON e.enumtypid = ty.oid
 WHERE ty.typnamespace = 'public'::regnamespace
 GROUP BY ty.typname
UNION ALL
SELECT 'fn ' || pr.proname || '(' || pg_get_function_identity_arguments(pr.oid) || ') ' || md5(pr.prosrc)
  FROM pg_proc pr
 WHERE pr.pronamespace = 'public'::regnamespace
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = pr.oid AND d.deptype = 'e')
UNION ALL
SELECT 'view ' || viewname || ' ' || definition FROM pg_views WHERE schemaname = 'public'
UNION ALL
SELECT 'rel ' || c.relkind::text || ' ' || c.relname
  FROM pg_class c
 WHERE c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p', 'v', 'm')
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')
UNION ALL
SELECT 'flag ' || key FROM feature_flags
ORDER BY 1`

// migrateTo — накат или откат до версии v.
func migrateTo(t *testing.T, db *sql.DB, v int64) error {
	t.Helper()
	ctx := context.Background()
	p := newProvider(t, db)
	cur, err := p.GetDBVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v < cur {
		_, err = p.DownTo(ctx, v)
	} else {
		_, err = p.UpTo(ctx, v)
	}
	return err
}

func dbVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	v, err := newProvider(t, db).GetDBVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// identityUser — игрок на схеме 0025 (почта подтверждена и в канонической форме); id — строкой.
func identityUser(t *testing.T, db *sql.DB, email, nick string) string {
	t.Helper()
	var id string
	if err := db.QueryRowContext(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date)
		VALUES ($1, now(), $2, '2000-01-01') RETURNING id::text`, email, nick).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func queryString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s
}

// Накат и откат 0024–0025 (спека identity §5.1): откат до 0023 возвращает схему в точности (users_contact_present,
// старые колонки и индексы sessions, granted_by, reviewed_by, rejection_reason, тип user_status без
// pending_deletion, без строк флагов auth_*); повторный накат даёт ту же схему, что и первый.
func TestIdentityMigrationRoundTrip(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	if err := migrateTo(t, db, 0); err != nil {
		t.Fatalf("down до 0: %v", err)
	}
	if err := migrateTo(t, db, identityStatusVersion-1); err != nil {
		t.Fatalf("up до %d: %v", identityStatusVersion-1, err)
	}
	before := snapshotOf(t, db, identitySnapshotSQL)
	for _, want := range []string{
		"con users users_contact_present CHECK (((email IS NOT NULL) OR (phone IS NOT NULL)))",
		"col sessions.refresh_hash text not null",
		"type user_status active,suspended,banned,deleted",
	} {
		if !slices.Contains(before, want) {
			t.Fatalf("в снимке 0023 нет %q — снимок сравнивает не то", want)
		}
	}

	if err := migrateTo(t, db, identityVersion); err != nil {
		t.Fatalf("up 0025: %v", err)
	}
	up := snapshotOf(t, db, identitySnapshotSQL)
	for _, want := range []string{
		"type user_status active,suspended,banned,pending_deletion,deleted",
		"rel r credentials", "rel r auth_codes", "rel r auth_challenges", "rel r devices", "rel r device_accounts",
		"rel r refresh_tokens", "rel r user_mfa", "rel r user_recovery_codes", "rel r user_identities", "rel r passkeys",
		"rel r security_events", "rel r user_restrictions", "rel r consents", "rel r mfa_resets",
		"rel r staff_player_links",
		"rel v identity_read_users", "rel v identity_read_linked_accounts", "rel v identity_read_captain_permits",
		"rel v identity_read_restrictions",
		"col users.email citext not null", "col users.version bigint not null default 1",
		"col sessions.reauth_failed_at timestamp with time zone",
		"col role_assignments.granted_by_staff_id uuid", "col captain_applications.rejection_code text",
		"flag auth_apple", "flag auth_passkeys_login",
	} {
		if !slices.Contains(up, want) {
			t.Errorf("после up нет %q", want)
		}
	}
	for _, gone := range []string{"col sessions.refresh_hash text not null", "col role_assignments.granted_by uuid",
		"col captain_applications.reviewed_by uuid", "col captain_applications.rejection_reason text"} {
		if slices.Contains(up, gone) {
			t.Errorf("после up осталось %q", gone)
		}
	}
	// в новых таблицах нет умолчаний now() и gen_random_uuid() (спека §5.3: id и время — из Go)
	for _, s := range up {
		if strings.HasPrefix(s, "col ") && (strings.Contains(s, "default now()") || strings.Contains(s, "gen_random_uuid()")) &&
			!slices.Contains(before, s) {
			t.Errorf("новая колонка с умолчанием времени или id: %q", s)
		}
	}

	// откат 0025 оставляет значение pending_deletion — его снимает только откат 0024
	if err := migrateTo(t, db, identityStatusVersion); err != nil {
		t.Fatalf("down 0025: %v", err)
	}
	if !slices.Contains(snapshotOf(t, db, identitySnapshotSQL), "type user_status active,suspended,banned,pending_deletion,deleted") {
		t.Error("после отката 0025 тип user_status без pending_deletion")
	}
	if err := migrateTo(t, db, identityStatusVersion-1); err != nil {
		t.Fatalf("down 0024: %v", err)
	}
	after := snapshotOf(t, db, identitySnapshotSQL)
	if extra, lost := missingFrom(after, before), missingFrom(before, after); len(extra) != 0 || len(lost) != 0 {
		t.Fatalf("откат 0024–0025 не вернул схему 0023:\nлишнее после отката: %q\nпропало после отката: %q", extra, lost)
	}

	if err := migrateTo(t, db, identityVersion); err != nil {
		t.Fatalf("повторный up 0025: %v", err)
	}
	again := snapshotOf(t, db, identitySnapshotSQL)
	if extra, lost := missingFrom(again, up), missingFrom(up, again); len(extra) != 0 || len(lost) != 0 {
		t.Fatalf("повторный накат дал другую схему:\nлишнее: %q\nпропало: %q", extra, lost)
	}
}

// Перед изменением 0025 проверяет данные (спека §5.1): игрок без почты (или без подтверждения) и любая строка
// sessions — отказ с понятным текстом, транзакция откатывается целиком (версия остаётся 24: 0024 — своя миграция).
func TestIdentityMigrationRefusesData(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, seed, wantMsg string
	}{
		{"игрок без почты",
			`INSERT INTO users (phone, nickname, birth_date) VALUES ('+79990000000', 'phone_only', '2000-01-01')`,
			"users: строк без подтверждённой почты — 1"},
		{"почта не подтверждена",
			`INSERT INTO users (email, nickname, birth_date) VALUES ('a@example.com', 'unverified', '2000-01-01')`,
			"users: строк без подтверждённой почты — 1"},
		{"сессия",
			`WITH u AS (INSERT INTO users (email, email_verified_at, nickname, birth_date)
			            VALUES ('s@example.com', now(), 'has_session', '2000-01-01') RETURNING id)
			 INSERT INTO sessions (user_id, refresh_hash, family_id, expires_at)
			 SELECT id, 'hash', gen_random_uuid(), now() + interval '1 day' FROM u`,
			"sessions: строк — 1"},
		{"почта не в канонической форме",
			`INSERT INTO users (email, email_verified_at, nickname, birth_date) VALUES ('Ivan@Mail.ru', now(), 'ivan_up', '2000-01-01')`,
			"users: строк с почтой не в канонической форме — 1"},
		{"отклонённая заявка без кода причины",
			`WITH u AS (INSERT INTO users (email, email_verified_at, nickname, birth_date)
			            VALUES ('c@example.com', now(), 'old_captain', '2000-01-01') RETURNING id)
			 INSERT INTO captain_applications (user_id, city_id, status, reviewed_at, rejection_reason)
			 SELECT id, ` + stavropol + `, 'rejected', now(), 'мало сведений' FROM u`,
			"captain_applications: строк, не проходящих новые проверки — 1"},
		{"ссылка заявки не https",
			`WITH u AS (INSERT INTO users (email, email_verified_at, nickname, birth_date)
			            VALUES ('l@example.com', now(), 'old_link', '2000-01-01') RETURNING id)
			 INSERT INTO captain_applications (user_id, city_id, contact_link)
			 SELECT id, ` + stavropol + `, 'http://t.me/old' FROM u`,
			"captain_applications: строк, не проходящих новые проверки — 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			db := dbtest.New(t)
			if err := migrateTo(t, db, identityStatusVersion-1); err != nil {
				t.Fatalf("down до 0023: %v", err)
			}
			mustExec(t, db, c.seed)
			err := migrateTo(t, db, identityVersion)
			if err == nil || !strings.Contains(err.Error(), c.wantMsg) {
				t.Fatalf("ждали отказ %q, получили: %v", c.wantMsg, err)
			}
			if v := dbVersion(t, db); v != identityStatusVersion {
				t.Fatalf("версия после отказа = %d, ждали %d", v, identityStatusVersion)
			}
			if got := queryString(t, db, `SELECT coalesce(to_regclass('public.credentials')::text, '-')`); got != "-" {
				t.Fatal("после отказа осталась credentials — 0025 применилась частично")
			}
		})
	}
}

// Откат 0024 при строках pending_deletion — отказ с понятным текстом (значение из enum иначе не убрать); тип не
// меняется; после перевода строки в другой статус откат проходит.
func TestPendingDeletionDownRefuses(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	u := identityUser(t, db, "leaving@example.com", "leaving")
	execArgs(t, db, `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'active', deletion_requested_at = now(),
		purge_at = now() + interval '30 days' WHERE id = $1`, u)

	err := migrateTo(t, db, identityStatusVersion-1)
	const wantMsg = "users: строк в статусе pending_deletion — 1"
	if err == nil || !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("ждали отказ %q, получили: %v", wantMsg, err)
	}
	if v := dbVersion(t, db); v != identityStatusVersion {
		t.Fatalf("версия после отказа = %d, ждали %d", v, identityStatusVersion)
	}
	if got := queryString(t, db, `SELECT string_agg(enumlabel, ',' ORDER BY enumsortorder) FROM pg_enum
		WHERE enumtypid = 'user_status'::regtype`); got != "active,suspended,banned,pending_deletion,deleted" {
		t.Fatalf("тип после отказа: %s", got)
	}
	execArgs(t, db, `UPDATE users SET status = 'deleted' WHERE id = $1`, u)
	if err := migrateTo(t, db, identityStatusVersion-1); err != nil {
		t.Fatalf("откат после перевода строки: %v", err)
	}
	if got := queryString(t, db, `SELECT status::text FROM users WHERE id = $1`, u); got != "deleted" {
		t.Fatalf("статус после отката: %s", got)
	}
}

// Ограничения §5.2–5.3: каждый случай проверяет имя ограничения.
func TestIdentityConstraints(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	u := identityUser(t, db, "ivan@example.com", "ivan")
	other := identityUser(t, db, "petr@example.com", "petr")
	const hash32 = "0101010101010101010101010101010101010101010101010101010101010101"
	device := queryString(t, db, `INSERT INTO devices (id, platform, install_mac, key_id, first_seen_at, last_seen_at)
		VALUES (gen_random_uuid(), 'ios', decode($1, 'hex'), $2, now(), now()) RETURNING id::text`, hash32, keyID)
	session := queryString(t, db, `INSERT INTO sessions (id, user_id, device_id, client_kind, auth_method, ip, created_at,
		last_seen_at, idle_expires_at, absolute_expires_at) VALUES (gen_random_uuid(), $1, $2, 'mobile', 'password',
		'203.0.113.7', now(), now(), now() + interval '90 days', now() + interval '365 days') RETURNING id::text`, u, device)

	// auth_codes: purpose, email, decoy, code_mac, code_enc, key_id, attempts, consumed_at
	const code = `INSERT INTO auth_codes (id, purpose, email, user_id, decoy, code_mac, code_enc, payload_enc, key_id,
		attempts, sends, last_sent_at, expires_at, consumed_at, ip, device_id, created_at)
		VALUES (gen_random_uuid(), $1::auth_code_purpose, $2, NULL, $3, decode($4, 'hex'), decode($5, 'hex'), NULL, $6,
		$7, 1, now(), now() + interval '15 minutes', $8::timestamptz, '203.0.113.7', NULL, now())`
	execArgs(t, db, code, "signup", "new@example.com", false, hash32, "0a0b", keyID, 0, nil)
	execArgs(t, db, code, "signup", "new@example.com", false, hash32, nil, keyID, 0, "2026-11-13T10:00:00Z") // погашенный не мешает
	execArgs(t, db, code, "password_reset", "new@example.com", false, hash32, "0a0b", keyID, 0, nil)         // другое назначение
	execArgs(t, db, code, "signup", "decoy@example.com", true, nil, nil, nil, 0, nil)                        // приманка

	const refresh = `INSERT INTO refresh_tokens (id, session_id, token_hash, created_at, successor_enc, successor_until)
		VALUES (gen_random_uuid(), $1, decode($2, 'hex'), now(), decode($3, 'hex'), $4::timestamptz)`
	execArgs(t, db, refresh, session, hash32, "0c0d", "2026-11-13T10:00:15Z")

	const identity = `INSERT INTO user_identities (id, user_id, provider, subject, email_is_private_relay, apple_refresh_enc,
		key_id, linked_at) VALUES (gen_random_uuid(), $1, $2::identity_provider, $3, false, decode($4, 'hex'), $5, now())`
	execArgs(t, db, identity, u, "apple", "apple-sub", "0e0f", keyID)

	const passkey = `INSERT INTO passkeys (id, user_id, credential_id, public_key, aaguid, sign_count, transports,
		backup_eligible, backup_state, attestation_format, name, created_at) VALUES (gen_random_uuid(), $1,
		decode($2, 'hex'), decode('a501', 'hex'), gen_random_uuid(), 0, '{internal}', true, true, 'none', $3, now())`
	execArgs(t, db, passkey, u, strings.Repeat("ab", 16), "iPhone")

	const event = `INSERT INTO security_events (id, user_id, kind, meta, created_at) VALUES (gen_random_uuid(), $1, $2, '{}', now())`
	execArgs(t, db, event, u, "refresh_reuse")

	const reset = `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason,
		execute_at, executed_at, cancelled_at, cancelled_by, created_at) VALUES (gen_random_uuid(), $1, gen_random_uuid(),
		gen_random_uuid(), 'Мария К.', 'потерял телефон', now() + interval '7 days', $2::timestamptz, $3::timestamptz, $4, now())`
	execArgs(t, db, reset, u, nil, nil, nil)
	execArgs(t, db, reset, u, nil, "2026-11-13T10:00:00Z", "owner") // отменённый не мешает живому

	const app = `INSERT INTO captain_applications (user_id, city_id, status, reviewed_at, rejection_code, comment)
		VALUES ($1, ` + stavropol + `, $2::application_status, $3::timestamptz, $4, $5)`
	execArgs(t, db, app, u, "rejected", "2026-11-13T10:00:00Z", "insufficient_info", nil)

	staffA, staffB := newStaff(t, db, "a.staff@example.com"), newStaff(t, db, "b.staff@example.com")
	const link = `INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at) VALUES ($1, $2, $1, now())`
	execArgs(t, db, link, staffA, u)

	// ссылка заявки: схема без учёта регистра
	const linkApp = `INSERT INTO captain_applications (user_id, city_id, contact_link) SELECT $1, id, $2 FROM cities WHERE slug = $3`
	execArgs(t, db, linkApp, other, "HTTPS://t.me/petr", "mihaylovsk")
	// сброс по подтверждению: approval_id — параметром
	const resetBy = `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason,
		execute_at, cancelled_at, cancelled_by, created_at) VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), 'Мария К.',
		'потерял телефон', now(), now(), 'system', now())`
	const approval = "01900000-0000-7000-8000-0000000000a7"
	execArgs(t, db, resetBy, other, approval)
	const codeSends = `INSERT INTO auth_codes (id, purpose, email, decoy, attempts, sends, last_sent_at, expires_at, ip, created_at)
		VALUES (gen_random_uuid(), 'signup', $1, true, 0, $2, now(), now(), '203.0.113.7', now())`
	execArgs(t, db, codeSends, "s4@example.com", 4)

	// ник удалённого — deleted-<12 hex> (20 символов), почта — заглушка анонимизации (R24)
	execArgs(t, db, `INSERT INTO users (email, email_verified_at, nickname, birth_date, status)
		VALUES ('deleted+01900000-0000-7000-8000-0a1b2c3d4e5f@invalid', now(), 'deleted-0a1b2c3d4e5f', '2000-01-01', 'deleted')`)

	const ses = `INSERT INTO sessions (id, user_id, device_id, client_kind, auth_method, ip, user_agent, created_at,
		last_seen_at, idle_expires_at, absolute_expires_at, revoked_at, revoked_reason) VALUES (gen_random_uuid(), $1, NULL,
		'web', 'password', '203.0.113.7', $2, now(), now(), now() + interval '30 days', now() + interval '180 days',
		$3::timestamptz, $4)`
	// причины отзыва — ровно список §5.3 (domain.RevokedReason)
	for _, r := range []string{"logout", "revoked", "revoked_others", "password_changed", "password_reset", "refresh_reuse",
		"account_deleted", "banned", "expired"} {
		execArgs(t, db, ses, u, nil, "2026-11-13T10:00:00Z", r)
	}

	for _, c := range []struct {
		name, code, constraint, query string
		args                          []any
	}{
		{"приманка с code_mac", "23514", "auth_codes_decoy_no_secret", code,
			[]any{"signup", "d2@example.com", true, hash32, nil, keyID, 0, nil}},
		{"настоящий код без code_mac", "23514", "auth_codes_real_has_mac", code,
			[]any{"signup", "r2@example.com", false, nil, nil, nil, 0, nil}},
		{"второй живой код (purpose, email)", "23505", "auth_codes_live_key", code,
			[]any{"signup", "NEW@example.com", false, hash32, "0a0b", keyID, 0, nil}},
		{"attempts = 6", "23514", "auth_codes_attempts_check", code,
			[]any{"email_change", "a6@example.com", false, hash32, "0a0b", keyID, 6, nil}},
		{"шифротекст без key_id", "23514", "auth_codes_secret_key", code,
			[]any{"email_change", "k@example.com", false, hash32, "0a0b", nil, 0, nil}},
		{"код у погашенного", "23514", "auth_codes_closed_no_code", code,
			[]any{"email_change", "c@example.com", false, hash32, "0a0b", keyID, 0, "2026-11-13T10:00:00Z"}},
		{"successor_enc без successor_until", "23514", "refresh_tokens_successor_pair", refresh,
			[]any{session, strings.Repeat("02", 32), "0c0d", nil}},
		{"apple_refresh_enc у google", "23514", "user_identities_apple_refresh", identity,
			[]any{other, "google", "google-sub", "0e0f", keyID}},
		{"credential_id 15 байт", "23514", "passkeys_credential_id_len", passkey,
			[]any{u, strings.Repeat("cd", 15), "Mac"}},
		{"kind не в форме", "23514", "security_events_kind_format", event, []any{u, "Bad"}},
		{"второй живой сброс 2FA", "23505", "mfa_resets_live_key", reset, []any{u, nil, nil, nil}},
		{"исполнен и отменён", "23514", "mfa_resets_one_outcome", reset,
			[]any{other, "2026-11-13T10:00:00Z", "2026-11-13T10:00:00Z", "system"}},
		{"purge_at у активного", "23514", "users_deletion_pending",
			`UPDATE users SET purge_at = now() + interval '30 days', deletion_requested_at = now() WHERE id = $1`, []any{other}},
		{"pending_deletion без сроков", "23514", "users_deletion_pending",
			`UPDATE users SET status = 'pending_deletion' WHERE id = $1`, []any{other}},
		{"почта не в канонической форме", "23514", "users_email_format",
			`INSERT INTO users (email, email_verified_at, nickname, birth_date) VALUES ($1, now(), 'ivan_m', '2000-01-01')`,
			[]any{"Ivan@Mail.ru"}},
		{"отклонена без кода причины", "23514", "captain_applications_rejected_code", app,
			[]any{other, "rejected", "2026-11-13T10:00:00Z", nil, nil}},
		{"other без комментария", "23514", "captain_applications_other_comment", app,
			[]any{other, "rejected", "2026-11-13T10:00:00Z", "other", nil}},
		{"вторая связь на того же игрока", "23505", "staff_player_links_user_id_key", link, []any{staffB, u}},
		{"причина отзыва не из списка", "23514", "sessions_revoked_reason_check", ses,
			[]any{u, nil, "2026-11-13T10:00:00Z", "forgot"}},
		{"User-Agent длиннее 512", "23514", "sessions_user_agent_check", ses,
			[]any{u, strings.Repeat("a", 513), nil, nil}},
		{"отозвана без причины", "23514", "sessions_revoked_pair", ses,
			[]any{u, nil, "2026-11-13T10:00:00Z", nil}},
		{"причина без отзыва", "23514", "sessions_revoked_pair", ses, []any{u, nil, nil, "logout"}},
		{"второй сброс по тому же подтверждению", "23505", "mfa_resets_approval_key", resetBy, []any{u, approval}},
		{"одобрена без времени решения", "23514", "captain_applications_reviewed", app,
			[]any{other, "approved", nil, nil, nil}},
		{"на рассмотрении с временем решения", "23514", "captain_applications_reviewed", app,
			[]any{other, "pending", "2026-11-13T10:00:00Z", nil, nil}},
		{"пятая отправка кода", "23514", "auth_codes_sends_check", codeSends, []any{"s5@example.com", 5}},
		{"refresh-токен с тем же хэшем", "23505", "refresh_tokens_token_hash_key", refresh,
			[]any{session, hash32, nil, nil}},
		{"ссылка заявки не https", "23514", "captain_applications_contact_link_check", linkApp,
			[]any{other, "http://t.me/petr", "stavropol"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantArgsError(t, db, c.code, c.constraint, c.query, c.args...)
		})
	}
}

// users_email_format принимает всё, что выдаёт emailaddr.Normalize (§5.7, R24): обычный адрес, плюс-адрес и спецсимволы
// dot-atom, IDN (A-label), домен из одной метки, а также заглушку анонимизации. Иначе нормализованная почта игрока —
// 23514 и 500 вместо регистрации.
func TestIdentityEmailFormatAcceptsNormalized(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	for i, in := range []string{
		"Ivan.Petrov+football@Mail.RU",
		"o'brien_{x}@example.com",
		"ivan@пример.рф",
		"ivan@рф",
		"admin@localhost",
		"a@b-c.d-e.example",
		"deleted+01900000-0000-7000-8000-0a1b2c3d4e5f@invalid",
	} {
		addr, ok := emailaddr.Normalize(in)
		if !ok {
			t.Fatalf("emailaddr.Normalize(%q) отверг адрес — набор теста устарел", in)
		}
		if _, err := db.ExecContext(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date)
			VALUES ($1, now(), $2, '2000-01-01')`, addr, "nick_"+strconv.Itoa(i)); err != nil {
			t.Errorf("%q → %q: %v", in, addr, err)
		}
	}
}

// Строки шести флагов §4.1 (спека identity, решение пользователя 13.11 В2): основные выключены, аварийные включены,
// только «везде» (городов нет — правило GlobalOnly), версия 1; auth_google_login не зависит от auth_apple_login.
func TestIdentityFlagsSeeded(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	rows, err := db.QueryContext(context.Background(), `SELECT key, enabled_globally, cardinality(enabled_city_ids),
		version, description <> '' FROM feature_flags WHERE key LIKE 'auth\_%' ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var key string
		var on, described bool
		var cities, version int64
		if err := rows.Scan(&key, &on, &cities, &version, &described); err != nil {
			t.Fatal(err)
		}
		if cities != 0 || version != 1 || !described {
			t.Errorf("%s: городов %d, версия %d, описание %v", key, cities, version, described)
		}
		state := "off"
		if on {
			state = "on"
		}
		got = append(got, key+"="+state)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"auth_apple=off", "auth_apple_login=on", "auth_google=off", "auth_google_login=on",
		"auth_passkeys=off", "auth_passkeys_login=on"}
	if !slices.Equal(got, want) {
		t.Fatalf("флаги auth_*: %v, ждали %v", got, want)
	}
}

// Представления §5.6: скрытые (pending_deletion, deleted) и ник-заглушка удалённого; пары связанных аккаунтов в обе
// стороны; живые допуски капитана; только действующие ограничения.
func TestIdentityReadViews(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	active := identityUser(t, db, "active@example.com", "active_one")
	leaving := identityUser(t, db, "leaving@example.com", "leaving_one")
	gone := identityUser(t, db, "gone@example.com", "gone_one")
	execArgs(t, db, `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'active', deletion_requested_at = now(),
		purge_at = now() + interval '30 days' WHERE id = $1`, leaving)
	execArgs(t, db, `UPDATE users SET status = 'deleted' WHERE id = $1`, gone)

	const usersView = `SELECT nickname || ' ' || deleted::text || ' ' || hidden::text FROM identity_read_users WHERE id = $1`
	if got := queryString(t, db, usersView, active); got != "active_one false false" {
		t.Errorf("активный: %s", got)
	}
	if got := queryString(t, db, usersView, leaving); got != "leaving_one false true" {
		t.Errorf("в периоде удаления: %s", got)
	}
	wantStub := "deleted-" + strings.ReplaceAll(gone, "-", "")[20:]
	if got := queryString(t, db, usersView, gone); got != wantStub+" true true" {
		t.Errorf("удалённый: %s, ждали ник %s", got, wantStub)
	}

	device := queryString(t, db, `INSERT INTO devices (id, platform, install_mac, key_id, first_seen_at, last_seen_at)
		VALUES (gen_random_uuid(), 'android', decode(repeat('03', 32), 'hex'), $1, now(), now()) RETURNING id::text`, keyID)
	for _, u := range []string{active, leaving} {
		execArgs(t, db, `INSERT INTO device_accounts (device_id, user_id, signed_up, first_seen_at, last_seen_at)
			VALUES ($1, $2, true, now(), now())`, device, u)
	}
	if got := queryString(t, db, `SELECT string_agg(user_id::text || '>' || linked_user_id::text, ',' ORDER BY user_id)
		FROM identity_read_linked_accounts WHERE device_id = $1`, device); got != strings.Join(sortedPair(active, leaving), ",") {
		t.Errorf("связанные аккаунты: %s", got)
	}

	app := queryString(t, db, `INSERT INTO captain_applications (user_id, city_id, status, reviewed_at)
		VALUES ($1, `+stavropol+`, 'approved', now()) RETURNING id::text`, active)
	execArgs(t, db, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id, application_id, granted_by_staff_id)
		VALUES ($1, 'captain', 'city', `+stavropol+`, $2, gen_random_uuid())`, active, app)
	execArgs(t, db, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id, revoked_at, revoked_by_staff_id,
		revoke_reason) SELECT $1, 'captain', 'city', id, now(), gen_random_uuid(), 'проба' FROM cities WHERE slug = 'mihaylovsk'`, active)
	if got := queryString(t, db, `SELECT count(*)::text || ' ' || bool_and(is_probationary)::text || ' ' ||
		bool_and(application_id = $2)::text FROM identity_read_captain_permits WHERE user_id = $1`, active, app); got != "1 true true" {
		t.Errorf("допуски капитана: %s", got)
	}

	const restriction = `INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at, lifted_at)
		VALUES (gen_random_uuid(), $1, $2, now() + $3::interval, gen_random_uuid(), now(), CASE WHEN $4 THEN now() END)`
	execArgs(t, db, restriction, active, "captain", nil, false)      // бессрочно
	execArgs(t, db, restriction, active, "comments", "1 day", false) // до завтра
	execArgs(t, db, restriction, active, "votes", "-1 day", false)   // истекло
	execArgs(t, db, restriction, active, "matches", nil, true)       // снято
	if got := queryString(t, db, `SELECT string_agg(capability, ',' ORDER BY capability) FROM identity_read_restrictions
		WHERE user_id = $1`, active); got != "captain,comments" {
		t.Errorf("действующие ограничения: %s", got)
	}
}

// sortedPair — пары «a>b» и «b>a» по возрастанию первого id (как в string_agg … ORDER BY user_id).
func sortedPair(a, b string) []string {
	if b < a {
		a, b = b, a
	}
	return []string{a + ">" + b, b + ">" + a}
}
