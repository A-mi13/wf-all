package migrations_test

import (
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/testkit/dbtest"
)

// identityDeletionVersion — 0026_identity_deletion.sql (решения пользователя 15.11: прежний статус при отмене удаления,
// анонимизация затирает дату рождения).
const identityDeletionVersion = 26

// Ограничения 0026: прежний статус задан ⇔ pending_deletion и только active|suspended (с 0027 — и banned); дата
// рождения обязательна, кроме удалённого.
func TestIdentityDeletionConstraints(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	u := identityUser(t, db, "keeper@example.com", "keeper")
	for _, c := range []struct {
		name, constraint, query string
	}{
		{"pending_deletion без прежнего статуса", "users_status_before_deletion",
			`UPDATE users SET status = 'pending_deletion', deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`},
		// banned допустим с 0027 (R11) — TestStatusBeforeDeletionAllowsBanned
		{"прежний статус deleted", "users_status_before_deletion",
			`UPDATE users SET status = 'pending_deletion', status_before_deletion = 'deleted', deletion_requested_at = now(),
			 purge_at = now() + interval '30 days' WHERE id = $1`},
		{"прежний статус у активного", "users_status_before_deletion",
			`UPDATE users SET status_before_deletion = 'active' WHERE id = $1`},
		{"нет даты рождения у живого", "users_birth_date_required", `UPDATE users SET birth_date = NULL WHERE id = $1`},
		{"нет даты рождения в периоде удаления", "users_birth_date_required",
			`UPDATE users SET birth_date = NULL, status = 'pending_deletion', status_before_deletion = 'active',
			 deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantArgsError(t, db, "23514", c.constraint, c.query, u)
		})
	}
	execArgs(t, db, `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'suspended',
		deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`, u)
	execArgs(t, db, `UPDATE users SET status = 'deleted', status_before_deletion = NULL, deletion_requested_at = NULL,
		purge_at = NULL, birth_date = NULL WHERE id = $1`, u)
}

// Накат 0026 на данных 0025: строка периода удаления получает прежний статус active (до 0026 отмена всегда давала
// active), удалённый до 0026 аккаунт затирается как анонимизацией. Откат при удалённом без даты рождения — отказ с
// понятным текстом; без таких строк — схема 0025 в точности.
func TestIdentityDeletionUpDown(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	if err := migrateTo(t, db, identityVersion); err != nil {
		t.Fatalf("down до 0025: %v", err)
	}
	before := snapshotOf(t, db, identitySnapshotSQL)
	leaving := identityUser(t, db, "leaving@example.com", "leaving")
	execArgs(t, db, `UPDATE users SET status = 'pending_deletion', deletion_requested_at = now(),
		purge_at = now() + interval '30 days' WHERE id = $1`, leaving)
	gone := identityUser(t, db, "gone@example.com", "gone_one")
	execArgs(t, db, `UPDATE users SET status = 'deleted', deleted_at = now(), display_name = 'Иван', height_cm = 180,
		motto = 'вперёд', favourite_club = 'Динамо', timezone = 'Europe/Moscow', last_login_at = now() WHERE id = $1`, gone)
	execArgs(t, db, `INSERT INTO captain_applications (user_id, city_id, phone, note, contact_link, proposed_pitch)
		VALUES ($1, `+stavropol+`, '+79990000000', 'звоните вечером', 'https://t.me/gone', 'поле у школы')`, gone)
	// m9: секреты, привязки, следы и допуски удалённого — как у app.anonymize; у живого leaving те же строки остаются
	for _, u := range []string{gone, leaving} {
		seedAccountTraces(t, db, u)
	}
	execArgs(t, db, `INSERT INTO auth_codes (id, purpose, email, decoy, last_sent_at, expires_at, ip, created_at)
		VALUES (gen_random_uuid(), 'signup', 'gone@example.com', true, now(), now() + interval '1 hour', '203.0.113.7', now())`)

	if err := migrateTo(t, db, identityDeletionVersion); err != nil {
		t.Fatalf("up 0026: %v", err)
	}
	if got := queryString(t, db, `SELECT status_before_deletion::text FROM users WHERE id = $1`, leaving); got != "active" {
		t.Fatalf("прежний статус строки периода удаления: %s", got)
	}
	if got := queryString(t, db, `SELECT concat_ws(',', birth_date, display_name, height_cm, motto, favourite_club, timezone,
		last_login_at)::text FROM users WHERE id = $1`, gone); got != "" {
		t.Fatalf("удалённый до 0026 не затёрт: %q", got)
	}
	if got := queryString(t, db, `SELECT concat_ws(',', phone, note, contact_link, proposed_pitch) FROM captain_applications
		WHERE user_id = $1`, gone); got != "" {
		t.Fatalf("заявка удалённого не затёрта: %q", got)
	}
	goneID := uuid.MustParse(gone)
	wantNick := "deleted-" + hex.EncodeToString(goneID[10:])
	if got := queryString(t, db, `SELECT email::text || ' ' || nickname FROM users WHERE id = $1`, gone); got != "deleted+"+gone+"@invalid "+wantNick {
		t.Fatalf("заглушки почты и ника: %q", got)
	}
	if got := accountTraces(t, db, gone); got != "0 0 0 0 0 0 0 0 0 0 0 1 1" {
		t.Fatalf("следы удалённого после 0026 (credentials user_mfa recovery passkeys identities consents codes "+
			"nickname_changes sessions security_events живые_допуски отозванные_допуски затёртые_сбросы): %s", got)
	}
	if got := accountTraces(t, db, leaving); got != "1 1 1 1 1 1 1 1 1 1 1 0 0" {
		t.Fatalf("следы живого аккаунта тронуты: %s", got)
	}
	if n := queryString(t, db, `SELECT count(*)::text FROM auth_codes WHERE email = 'gone@example.com'`); n != "0" {
		t.Fatalf("код на почту удалённого: %s", n)
	}

	err := migrateTo(t, db, identityVersion)
	const wantMsg = "users: строк без даты рождения — 1"
	if err == nil || !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("ждали отказ %q, получили: %v", wantMsg, err)
	}
	if v := dbVersion(t, db); v != identityDeletionVersion {
		t.Fatalf("версия после отказа = %d", v)
	}
	execArgs(t, db, `DELETE FROM users WHERE id = $1`, gone)
	execArgs(t, db, `UPDATE users SET status = 'active', status_before_deletion = NULL, deletion_requested_at = NULL,
		purge_at = NULL WHERE id = $1`, leaving)
	if err := migrateTo(t, db, identityVersion); err != nil {
		t.Fatalf("down 0026: %v", err)
	}
	after := snapshotOf(t, db, identitySnapshotSQL)
	if extra, lost := missingFrom(after, before), missingFrom(before, after); len(extra) != 0 || len(lost) != 0 {
		t.Fatalf("откат 0026 не вернул схему 0025:\nлишнее: %q\nпропало: %q", extra, lost)
	}
}

// seedAccountTraces — по строке в каждой таблице, которую чистит анонимизация (app.anonymize): пароль, 2FA, резервный
// код, ключ доступа, привязка, согласие, код с user_id, история ника, сессия, журнал безопасности, живой допуск
// капитана, живой сброс 2FA с причиной и тикетом.
func seedAccountTraces(t *testing.T, db *sql.DB, u string) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO credentials (user_id, password_hash, updated_at) VALUES ($1, 'h', now())`,
		`INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at) VALUES ($1, decode('00', 'hex'), '` + keyID + `', now(), now())`,
		`INSERT INTO user_recovery_codes (user_id, code_mac, key_id, created_at)
			VALUES ($1, decode(repeat('01', 32), 'hex'), '` + keyID + `', now())`,
		`INSERT INTO passkeys (id, user_id, credential_id, public_key, aaguid, sign_count, transports, backup_eligible,
			backup_state, attestation_format, name, created_at) VALUES (gen_random_uuid(), $1::uuid, decode(md5($1::text), 'hex'),
			decode('a501', 'hex'), gen_random_uuid(), 0, '{internal}', true, true, 'none', 'iPhone', now())`,
		`INSERT INTO user_identities (id, user_id, provider, subject, email_is_private_relay, key_id, linked_at)
			VALUES (gen_random_uuid(), $1::uuid, 'google', $1::text, false, '` + keyID + `', now())`,
		`INSERT INTO consents (user_id, document, version, accepted_at, client_kind) VALUES ($1, 'terms', '2026-11-01', now(), 'web')`,
		`INSERT INTO auth_codes (id, purpose, email, user_id, decoy, code_mac, key_id, last_sent_at, expires_at, ip, created_at)
			SELECT gen_random_uuid(), 'password_reset', email, id, false, decode(repeat('02', 32), 'hex'), '` + keyID + `', now(),
			now() + interval '1 hour', '203.0.113.7', now() FROM users WHERE id = $1`,
		`INSERT INTO nickname_changes (user_id, old_value, new_value) VALUES ($1, 'old_nick', 'new_nick')`,
		`INSERT INTO sessions (id, user_id, device_id, client_kind, auth_method, ip, created_at, last_seen_at, idle_expires_at,
			absolute_expires_at) VALUES (gen_random_uuid(), $1, NULL, 'web', 'password', '203.0.113.7', now(), now(),
			now() + interval '30 days', now() + interval '180 days')`,
		`INSERT INTO security_events (id, user_id, kind, meta, created_at) VALUES (gen_random_uuid(), $1, 'login', '{}', now())`,
		`INSERT INTO role_assignments (user_id, role, scope_type, scope_id, granted_by_staff_id)
			VALUES ($1, 'captain', 'city', ` + stavropol + `, gen_random_uuid())`,
		`INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason, ticket,
			execute_at, created_at) VALUES (gen_random_uuid(), $1, gen_random_uuid(), gen_random_uuid(), 'Мария К.',
			'звонил Иван, +79990000000', 'SUP-7', now() + interval '7 days', now())`,
	} {
		execArgs(t, db, q, u)
	}
}

// accountTraces — счётчики строк seedAccountTraces через пробел; последние три — живые допуски, допуски, отозванные
// с account_deleted, и сбросы 2FA, отменённые системой с затёртыми причиной и тикетом.
func accountTraces(t *testing.T, db *sql.DB, u string) string {
	t.Helper()
	return queryString(t, db, `SELECT concat_ws(' ',
		(SELECT count(*) FROM credentials WHERE user_id = $1),
		(SELECT count(*) FROM user_mfa WHERE user_id = $1),
		(SELECT count(*) FROM user_recovery_codes WHERE user_id = $1),
		(SELECT count(*) FROM passkeys WHERE user_id = $1),
		(SELECT count(*) FROM user_identities WHERE user_id = $1),
		(SELECT count(*) FROM consents WHERE user_id = $1),
		(SELECT count(*) FROM auth_codes WHERE user_id = $1),
		(SELECT count(*) FROM nickname_changes WHERE user_id = $1),
		(SELECT count(*) FROM sessions WHERE user_id = $1),
		(SELECT count(*) FROM security_events WHERE user_id = $1),
		(SELECT count(*) FROM role_assignments WHERE user_id = $1 AND revoked_at IS NULL),
		(SELECT count(*) FROM role_assignments WHERE user_id = $1 AND revoke_reason = 'account_deleted'),
		(SELECT count(*) FROM mfa_resets WHERE user_id = $1 AND cancelled_by = 'system' AND reason = '---' AND ticket IS NULL))`, u)
}
