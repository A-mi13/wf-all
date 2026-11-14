package grants_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/grants"
	"wf/backend/internal/platform/testkit/dbtest"
)

// identityTables — таблицы и представления identity и связь сотрудника с игроком (спека identity §5.5, §7.6).
var identityTables = []string{
	"users", "credentials", "auth_codes", "auth_challenges", "user_mfa", "user_recovery_codes", "passkeys",
	"user_identities", "consents", "mfa_resets", "sessions", "refresh_tokens", "devices", "device_accounts",
	"security_events", "user_restrictions", "role_assignments", "captain_applications", "nickname_reservations",
	"nickname_changes", "reserved_nicknames", "staff_player_links",
	"identity_read_users", "identity_read_linked_accounts", "identity_read_captain_permits", "identity_read_restrictions",
}

// identityMatrix — права ролей на уровне таблицы по §5.5 построчно: S — SELECT, I — INSERT, U — UPDATE, D — DELETE;
// пусто — ничего. Колоночные права — identityColumnGrants.
var identityMatrix = map[string][3]string{ // api, worker, admin
	"users":                 {"SIUD", "SU", "S"},
	"credentials":           {"SIUD", "D", ""},
	"auth_codes":            {"SIUD", "SD", ""},
	"auth_challenges":       {"SIUD", "D", ""},
	"user_mfa":              {"SIUD", "D", ""},
	"user_recovery_codes":   {"SIUD", "D", ""},
	"passkeys":              {"SIUD", "D", ""},
	"user_identities":       {"SIUD", "SUD", ""},
	"consents":              {"SIUD", "D", ""},
	"mfa_resets":            {"SU", "SU", "SIU"},
	"sessions":              {"SIUD", "SUD", "S"},
	"refresh_tokens":        {"SIUD", "SUD", ""},
	"devices":               {"SIUD", "SU", "S"},
	"device_accounts":       {"SIUD", "SU", "S"},
	"security_events":       {"SI", "SID", "I"},
	"user_restrictions":     {"SIUD", "SU", "S"},
	"role_assignments":      {"SIUD", "SU", "SIUD"},
	"captain_applications":  {"SIUD", "SU", "SIUD"},
	"nickname_reservations": {"SIUD", "SID", "S"},
	"nickname_changes":      {"SIUD", "SD", "S"},
	"reserved_nicknames":    {"SIUD", "SD", "S"},
	// таблица модуля staff — ветка staff_* (только admin)
	"staff_player_links":            {"", "", "SIUD"},
	"identity_read_users":           {"S", "S", "S"},
	"identity_read_linked_accounts": {"S", "S", "S"},
	"identity_read_captain_permits": {"S", "S", "S"},
	"identity_read_restrictions":    {"S", "S", "S"},
}

// identityColumnGrants — колоночные права §5.5: «роль таблица право» → колонки с правом; у остальных колонок таблицы
// этого права нет.
var identityColumnGrants = map[string][]string{
	"worker credentials SELECT":         {"user_id"},
	"worker auth_challenges SELECT":     {"expires_at", "consumed_at"},
	"worker user_mfa SELECT":            {"user_id"},
	"worker user_recovery_codes SELECT": {"user_id"},
	"worker passkeys SELECT":            {"user_id"},
	"worker consents SELECT":            {"user_id"},
	"worker security_events UPDATE":     {"meta_enc"},
	"admin user_mfa SELECT":             {"user_id", "confirmed_at"},
}

var matrixRoles = [3]string{"api", "worker", "admin"}

// grantAllThenApply — всем ролям всё на объекты identity, затем grants.Apply дважды: отказ доказывает, что Apply
// права отзывает, а не просто не выдаёт.
func grantAllThenApply(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "GRANT ALL ON "+strings.Join(identityTables, ", ")+" TO api, admin, worker"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := grants.Apply(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
}

// Каждая клетка §5.5: право роли на таблицу — ровно по матрице (ни одна таблица identity не получает DML через ELSE),
// колоночные права — ровно на перечисленные колонки; плюс исполнение запросов под ролью для обратных клеток (К2, К6).
func TestIdentityGrantsMatrix(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()
	grantAllThenApply(t, db)
	if len(identityMatrix) != len(identityTables) {
		t.Fatalf("матрица: %d строк, объектов %d", len(identityMatrix), len(identityTables))
	}
	privs := map[byte]string{'S': "SELECT", 'I': "INSERT", 'U': "UPDATE", 'D': "DELETE"}
	for _, table := range identityTables {
		row, ok := identityMatrix[table]
		if !ok {
			t.Fatalf("%s нет в матрице", table)
		}
		for i, role := range matrixRoles {
			for _, p := range []byte("SIUD") {
				want := strings.IndexByte(row[i], p) >= 0
				var got bool
				if err := db.QueryRowContext(ctx, `SELECT has_table_privilege($1, $2, $3)`, role, "public."+table,
					privs[p]).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("%s %s %s: есть %v, по §5.5 %v", role, privs[p], table, got, want)
				}
			}
		}
	}

	rows, err := db.QueryContext(ctx, `SELECT g.grantee, g.table_name, g.privilege_type, g.column_name
		FROM information_schema.column_privileges g
		WHERE g.table_schema = 'public' AND g.grantee IN ('api', 'worker', 'admin') AND g.table_name = ANY($1)
		  AND NOT has_table_privilege(g.grantee, 'public.' || g.table_name, g.privilege_type)
		ORDER BY 1, 2, 3, 4`, identityTables)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string][]string{}
	for rows.Next() {
		var role, table, priv, col string
		if err := rows.Scan(&role, &table, &priv, &col); err != nil {
			t.Fatal(err)
		}
		k := role + " " + table + " " + priv
		got[k] = append(got[k], col)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for k, want := range identityColumnGrants {
		w := slices.Clone(want)
		slices.Sort(w)
		if !slices.Equal(got[k], w) {
			t.Errorf("колоночные права %s: %v, ждали %v", k, got[k], w)
		}
	}
	for k, cols := range got {
		if _, ok := identityColumnGrants[k]; !ok {
			t.Errorf("лишние колоночные права %s: %v", k, cols)
		}
	}

	for _, c := range []struct {
		role, sql string
		allowed   bool
	}{
		{"admin", "UPDATE users SET locale = locale WHERE false", false}, // К2
		{"admin", "UPDATE sessions SET last_seen_at = last_seen_at WHERE false", false},
		{"admin", "SELECT count(*) FROM sessions WHERE revoked_at IS NULL", true},
		{"admin", "INSERT INTO security_events (id, kind, meta, created_at) VALUES (gen_random_uuid(), 'mfa_reset_requested', '{}', now())", true},
		{"admin", "INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason, execute_at, created_at) " +
			"SELECT gen_random_uuid(), id, gen_random_uuid(), gen_random_uuid(), 'Мария К.', 'потерял телефон', now(), now() FROM users LIMIT 0", true},
		{"admin", "UPDATE mfa_resets SET cancelled_at = now(), cancelled_by = 'staff:Мария К.' WHERE false", true},
		{"api", "INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason, execute_at, created_at) " +
			"SELECT gen_random_uuid(), id, gen_random_uuid(), gen_random_uuid(), 'Мария К.', 'потерял телефон', now(), now() FROM users LIMIT 0", false},
		{"api", "UPDATE mfa_resets SET cancelled_at = now(), cancelled_by = 'owner' WHERE false", true},
		{"worker", "UPDATE security_events SET meta_enc = NULL, key_id = key_id WHERE false", false},
		{"worker", "UPDATE security_events SET meta_enc = NULL WHERE created_at < now()", true}, // К6
		{"worker", "UPDATE security_events SET kind = kind WHERE false", false},
		{"worker", "DELETE FROM auth_challenges WHERE expires_at < now() OR consumed_at IS NOT NULL", true},
		{"worker", "DELETE FROM user_mfa WHERE user_id = gen_random_uuid()", true},
		{"worker", "DELETE FROM consents WHERE user_id = gen_random_uuid()", true},
		{"worker", "UPDATE users SET status = 'deleted' WHERE false", true},
		{"worker", "INSERT INTO users (email, email_verified_at, nickname, birth_date) VALUES ('w@example.com', now(), 'worker', '2000-01-01')", false},
		{"worker", "INSERT INTO nickname_reservations (nickname_normalized, released_at, expires_at) VALUES ('probe', now(), now())", true},
		{"api", "DELETE FROM security_events WHERE false", false},
		{"api", "SELECT 1 FROM staff_player_links", false},
		{"worker", "SELECT 1 FROM staff_player_links", false},
		{"admin", "SELECT user_id FROM staff_player_links", true},
		{"api", "SELECT id, nickname, hidden FROM identity_read_users", true},
	} {
		t.Run(c.role+": "+c.sql, func(t *testing.T) {
			err := asRole(ctx, t, db, c.role, c.sql)
			var pg *pgconn.PgError
			denied := errors.As(err, &pg) && pg.Code == "42501"
			switch {
			case c.allowed && err != nil:
				t.Fatalf("ждали доступ, получили: %v", err)
			case !c.allowed && !denied:
				t.Fatalf("ждали отказ в правах (42501), получили: %v", err)
			}
		})
	}
}

// Страж §5.5: под admin не читаются секреты (credentials, user_mfa.secret_enc, auth_codes, user_identities, passkeys),
// а есть ли 2FA — читается; под worker не читаются хэш пароля и секрет TOTP.
func TestIdentitySecretsHidden(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()
	grantAllThenApply(t, db)
	for _, c := range []struct {
		role, sql string
		allowed   bool
	}{
		{"admin", "SELECT * FROM credentials", false},
		{"admin", "SELECT user_id FROM credentials", false},
		{"admin", "SELECT secret_enc FROM user_mfa", false},
		{"admin", "SELECT * FROM auth_codes", false},
		{"admin", "SELECT * FROM user_identities", false},
		{"admin", "SELECT * FROM passkeys", false},
		{"admin", "SELECT * FROM auth_challenges", false},
		{"admin", "SELECT * FROM refresh_tokens", false},
		{"admin", "SELECT * FROM user_recovery_codes", false},
		{"admin", "SELECT user_id, confirmed_at FROM user_mfa", true},
		{"worker", "SELECT password_hash FROM credentials", false},
		{"worker", "SELECT secret_enc FROM user_mfa", false},
		{"worker", "SELECT code_mac FROM user_recovery_codes", false},
		{"worker", "SELECT data_enc FROM auth_challenges", false},
		{"worker", "SELECT public_key FROM passkeys", false},
		{"worker", "SELECT user_id FROM credentials", true},
	} {
		t.Run(c.role+": "+c.sql, func(t *testing.T) {
			err := asRole(ctx, t, db, c.role, c.sql)
			var pg *pgconn.PgError
			denied := errors.As(err, &pg) && pg.Code == "42501"
			switch {
			case c.allowed && err != nil:
				t.Fatalf("ждали доступ, получили: %v", err)
			case !c.allowed && !denied:
				t.Fatalf("ждали отказ в правах (42501), получили: %v", err)
			}
		})
	}
}
