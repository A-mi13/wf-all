package migrations_test

import (
	"strings"
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

// identityDeletionVersion — 0026_identity_deletion.sql (решения пользователя 15.11: прежний статус при отмене удаления,
// анонимизация затирает дату рождения).
const identityDeletionVersion = 26

// Ограничения 0026: прежний статус задан ⇔ pending_deletion и только active|suspended; дата рождения обязательна,
// кроме удалённого.
func TestIdentityDeletionConstraints(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	u := identityUser(t, db, "keeper@example.com", "keeper")
	for _, c := range []struct {
		name, constraint, query string
	}{
		{"pending_deletion без прежнего статуса", "users_status_before_deletion",
			`UPDATE users SET status = 'pending_deletion', deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`},
		{"прежний статус banned", "users_status_before_deletion",
			`UPDATE users SET status = 'pending_deletion', status_before_deletion = 'banned', deletion_requested_at = now(),
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
