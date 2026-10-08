package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/testkit/dbtest"
)

// staffVersion — версия миграции 0020_staff.sql.
const staffVersion = 20

// Участники подтверждений и записей журнала: у approvals и audit_log внешних ключей нет.
const (
	initiatorID = "01900000-0000-7000-8000-000000000001"
	targetID    = "01900000-0000-7000-8000-000000000002"
	approverID  = "01900000-0000-7000-8000-000000000003"
	cityID      = "01900000-0000-7000-8000-0000000000c1"
)

// keyID — форма keys.ID: 11 символов base64url.
const keyID = "AbCdEfGh_-1"

const stavropol = `(SELECT id FROM cities WHERE slug = 'stavropol')`

func wantPg(t *testing.T, err error, code, constraint, what string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != code || (constraint != "" && pg.ConstraintName != constraint) {
		t.Fatalf("%s: ждали %s на %q, получили: %v", what, code, constraint, err)
	}
}

func execArgs(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s %v: %v", query, args, err)
	}
}

func wantArgsError(t *testing.T, db *sql.DB, code, constraint, query string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), query, args...)
	wantPg(t, err, code, constraint, query)
}

// inTx — операторы одной транзакцией: отложенные триггеры срабатывают на COMMIT.
func inTx(t *testing.T, db *sql.DB, stmts ...func(tx *sql.Tx) error) error {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stmts {
		if err := s(tx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ex — один оператор для inTx.
func ex(query string, args ...any) func(tx *sql.Tx) error {
	return func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), query, args...)
		return err
	}
}

// newStaff — учётка сотрудника (invited) с уже нормализованной почтой; id — строкой.
func newStaff(t *testing.T, db *sql.DB, email string) string {
	t.Helper()
	var id string
	if err := db.QueryRowContext(context.Background(), `INSERT INTO staff_accounts (id, email, display_name, created_at)
		VALUES (gen_random_uuid(), $1, 'Мария К.', now()) RETURNING id::text`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// newUser — игрок для проверок role_assignments.
func newUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	var id string
	if err := db.QueryRowContext(context.Background(), `INSERT INTO users (email, nickname, birth_date)
		VALUES ('player@example.com', 'probe_player', '2000-01-01') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Учётка сотрудника (спека §3.1): почта уникальна и хранится нормализованной (Review Focus 1), имя
// 1–80, язык ru|en, активной учётке нужен пароль, счётчик неудач и версия — в границах.
func TestStaff0020Accounts(t *testing.T) {
	db := dbtest.New(t)
	const ins = `INSERT INTO staff_accounts (id, email, display_name, locale, status, password_hash, mfa_failures,
		created_at, version) VALUES (gen_random_uuid(), $1, $2, $3, $4::staff_status, $5, $6, now(), $7)`
	row := func(email string) []any { return []any{email, "Мария К.", "ru", "invited", nil, 0, 1} }

	execArgs(t, db, ins, row("maria.k@example.com")...)
	wantArgsError(t, db, "23505", "staff_accounts_email_key", ins, row("maria.k@example.com")...)
	for _, bad := range []string{"Igor.L@Example.com", " igor.l@example.com", "igor.l@example.com ", "igor.l@@example.com",
		"igor.l", "igor l@example.com", strings.Repeat("a", 243) + "@example.com"} {
		wantArgsError(t, db, "23514", "staff_accounts_email_format", ins, row(bad)...)
	}

	cases := []struct {
		constraint string
		edit       func(a []any)
	}{
		{"staff_accounts_display_name_check", func(a []any) { a[1] = "   " }},
		{"staff_accounts_display_name_check", func(a []any) { a[1] = strings.Repeat("я", 81) }},
		{"staff_accounts_locale_check", func(a []any) { a[2] = "de" }},
		{"staff_accounts_active_password", func(a []any) { a[3] = "active" }},
		{"staff_accounts_mfa_failures_check", func(a []any) { a[5] = -1 }},
		{"staff_accounts_version_check", func(a []any) { a[6] = 0 }},
	}
	for i, c := range cases {
		a := row("case" + strconv.Itoa(i) + "@example.com")
		c.edit(a)
		wantArgsError(t, db, "23514", c.constraint, ins, a...)
	}
	a := row("igor.l@example.com")
	a[3], a[4] = "active", "argon2id-hash-placeholder"
	execArgs(t, db, ins, a...)
}

// Города бывают только у модератора, и у него хотя бы один (спека §2.3, §3.1): отложенная проверка на
// COMMIT — роль и города меняются в одной транзакции в любом порядке.
func TestStaff0020RoleCities(t *testing.T) {
	db := dbtest.New(t)
	const (
		role    = `INSERT INTO staff_roles (staff_id, role, granted_at) VALUES ($1, $2::staff_role, now())`
		city    = `INSERT INTO staff_role_cities (staff_id, city_id) VALUES ($1, ` + stavropol + `)`
		drop    = `DELETE FROM staff_role_cities WHERE staff_id = $1`
		toAdmin = `UPDATE staff_roles SET role = 'admin' WHERE staff_id = $1`
	)
	const check = "staff_role_cities_moderator"

	mod := newStaff(t, db, "mod@example.com")
	if err := inTx(t, db, ex(role, mod, "moderator"), ex(city, mod)); err != nil {
		t.Fatalf("модератор с городом: %v", err)
	}
	wantPg(t, inTx(t, db, ex(drop, mod)), "23514", check, "модератор без последнего города")
	wantPg(t, inTx(t, db, ex(toAdmin, mod)), "23514", check, "админ с городами")
	if err := inTx(t, db, ex(toAdmin, mod), ex(drop, mod)); err != nil {
		t.Fatalf("модератор → админ вместе со снятием городов: %v", err)
	}

	lone := newStaff(t, db, "lone@example.com")
	wantPg(t, inTx(t, db, ex(role, lone, "moderator")), "23514", check, "модератор без городов")
	sup := newStaff(t, db, "support@example.com")
	wantPg(t, inTx(t, db, ex(role, sup, "support"), ex(city, sup)), "23514", check, "город у поддержки")
	wantPg(t, inTx(t, db, ex(role, sup, "city_moderator")), "22P02", "", "роль не из staff_role")
	wantPg(t, inTx(t, db, ex(role, sup, "moderator"),
		ex(`INSERT INTO staff_role_cities (staff_id, city_id) VALUES ($1, gen_random_uuid())`, sup)),
		"23503", "staff_role_cities_city_id_fkey", "несуществующий город")

	// учётка удаляется вместе с ролью и городами — отложенная проверка этому не мешает
	gone := newStaff(t, db, "gone@example.com")
	if err := inTx(t, db, ex(role, gone, "moderator"), ex(city, gone)); err != nil {
		t.Fatal(err)
	}
	if err := inTx(t, db, ex(`DELETE FROM staff_accounts WHERE id = $1`, gone)); err != nil {
		t.Fatalf("удаление модератора: %v", err)
	}
}

// Секреты (D4, D5): шифротекст или HMAC с key_id в форме keys.ID; хэши по 32 байта; одно живое
// приглашение на учётку; код стирается при погашении; активная сессия — только после второго фактора.
func TestStaff0020Secrets(t *testing.T) {
	db := dbtest.New(t)
	s := newStaff(t, db, "secrets@example.com")
	const ts = "2026-10-06T10:00:00Z"

	const mfa = `INSERT INTO staff_mfa (staff_id, secret_enc, key_id, last_used_step)
		VALUES ($1, decode('00ff', 'hex'), $2, $3)`
	wantArgsError(t, db, "23514", "staff_mfa_key_id_format", mfa, s, "kid", 0)
	wantArgsError(t, db, "23514", "staff_mfa_last_used_step_check", mfa, s, keyID, -1)
	execArgs(t, db, mfa, s, keyID, 0)

	const rc = `INSERT INTO staff_recovery_codes (staff_id, code_mac, key_id)
		VALUES ($1, decode($2, 'hex'), $3)`
	execArgs(t, db, rc, s, strings.Repeat("ab", 32), keyID)
	wantArgsError(t, db, "23505", "staff_recovery_codes_pkey", rc, s, strings.Repeat("ab", 32), keyID)
	wantArgsError(t, db, "23514", "staff_recovery_codes_code_mac_check", rc, s, strings.Repeat("ab", 31), keyID)
	wantArgsError(t, db, "23514", "staff_recovery_codes_key_id_format", rc, s, strings.Repeat("cd", 32), "short")

	const inv = `INSERT INTO staff_invites (id, staff_id, purpose, code_enc, code_key_id, expires_at, code_attempts,
		last_sent_at, created_at, consumed_at) VALUES (gen_random_uuid(), $1, 'invite', decode($2, 'hex'), $3,
		now() + interval '24 hours', $4, now(), now(), $5::timestamptz)`
	execArgs(t, db, inv, s, "0a0b", keyID, 0, nil)
	wantArgsError(t, db, "23505", "staff_invites_live_key", inv, s, "0c0d", keyID, 0, nil)
	execArgs(t, db, inv, s, nil, nil, 0, ts) // погашенное не мешает живому
	s2 := newStaff(t, db, "invite2@example.com")
	wantArgsError(t, db, "23514", "staff_invites_code_pair", inv, s2, "0a", nil, 0, nil)
	wantArgsError(t, db, "23514", "staff_invites_code_pair", inv, s2, nil, keyID, 0, nil)
	wantArgsError(t, db, "23514", "staff_invites_code_key_id_format", inv, s2, "0a", "bad", 0, nil)
	wantArgsError(t, db, "23514", "staff_invites_code_attempts_check", inv, s2, "0a", keyID, 6, nil)
	wantArgsError(t, db, "23514", "staff_invites_closed_no_code", inv, s2, "0a", keyID, 0, ts)
	// 5 неверных вводов стирают код (D12): код с пятью попытками — нарушение, четыре — ещё можно
	wantArgsError(t, db, "23514", "staff_invites_code_wiped", inv, s2, "0a", keyID, 5, nil)
	execArgs(t, db, inv, s2, "0a", keyID, 4, nil)
	execArgs(t, db, `UPDATE staff_invites SET revoked_at = now(), code_enc = NULL, code_key_id = NULL WHERE staff_id = $1`, s2)
	execArgs(t, db, inv, s2, nil, nil, 5, nil) // 5 неверных кодов: код стёрт, приглашение живо (D12)

	const ses = `INSERT INTO staff_sessions (id, staff_id, token_hash, stage, created_at, last_seen_at, mfa_at,
		expires_at, ip, user_agent, revoked_at, revoked_reason) VALUES (gen_random_uuid(), $1, decode($2, 'hex'),
		$3::staff_session_stage, now(), now(), $4::timestamptz, now() + interval '8 hours', '203.0.113.7', '', $5::timestamptz, $6)`
	tok := func(b string) string { return strings.Repeat(b, 32) }
	execArgs(t, db, ses, s, tok("11"), "mfa_pending", nil, nil, nil)
	wantArgsError(t, db, "23505", "staff_sessions_token_hash_key", ses, s, tok("11"), "mfa_pending", nil, nil, nil)
	wantArgsError(t, db, "23514", "staff_sessions_token_hash_check", ses, s, strings.Repeat("22", 31), "mfa_pending", nil, nil, nil)
	wantArgsError(t, db, "23514", "staff_sessions_active_mfa", ses, s, tok("33"), "active", nil, nil, nil)
	execArgs(t, db, ses, s, tok("44"), "active", ts, nil, nil)
	wantArgsError(t, db, "23514", "staff_sessions_revoked_pair", ses, s, tok("55"), "active", ts, ts, nil)
	wantArgsError(t, db, "23514", "staff_sessions_revoked_pair", ses, s, tok("56"), "active", ts, nil, "logout")
	wantArgsError(t, db, "23514", "staff_sessions_revoked_reason_check", ses, s, tok("66"), "active", ts, ts, "forgot")
	execArgs(t, db, ses, s, tok("77"), "active", ts, ts, "logout")
}

const insertApproval = `INSERT INTO approvals (id, action, permission, payload, target_id, requested_by,
	requested_by_label, requested_at, expires_at, status, decided_by, decided_by_label, decided_at, decision_comment, error)
	VALUES (gen_random_uuid(), $1, $2, $3::jsonb, $4::uuid, $5::uuid, 'Алексей М.', now(), now() + $6::interval,
	        $7::approval_status, $8::uuid, $9, CASE WHEN $10::boolean THEN now() END, $11, $12)`

// approvalRow — строка approvals; nil — NULL.
type approvalRow struct {
	action, permission, payload, expiresIn, status string
	decidedBy, decidedByLabel, comment, errText    any
	decidedAt                                      bool
}

// pendingApproval — живой запрос инициатора на смену роли цели.
func pendingApproval() approvalRow {
	return approvalRow{action: "staff.role_change", permission: "staff.roles.grant", payload: `{"v": 1, "role": "support"}`,
		expiresIn: "24 hours", status: "pending"}
}

func (a approvalRow) decide(status string, by any) approvalRow {
	a.status, a.decidedBy, a.decidedByLabel, a.decidedAt = status, by, "Игорь Л.", true
	return a
}

func (a approvalRow) with(edit func(a *approvalRow)) approvalRow {
	edit(&a)
	return a
}

func (a approvalRow) args() []any {
	return []any{a.action, a.permission, a.payload, targetID, initiatorID, a.expiresIn, a.status,
		a.decidedBy, a.decidedByLabel, a.decidedAt, a.comment, a.errText}
}

// Подтверждение вдвоём (D2, D17): формат действия и права, версия payload, срок, согласованность
// статуса и решения, решивший — не инициатор и не цель, отказ — с комментарием, ошибка — только у failed.
func TestStaff0020Approvals(t *testing.T) {
	db := dbtest.New(t)
	p := pendingApproval()
	for _, a := range []approvalRow{
		p,
		p.decide("executed", approverID),
		p.decide("rejected", approverID).with(func(a *approvalRow) { a.comment = "роль не нужна" }),
		p.decide("cancelled", initiatorID), // отменяет инициатор
		// решено системой (Task 8a: ApprovalExpireDue, ApprovalCancelByInitiator): без решившего, метка system
		p.with(func(a *approvalRow) { a.status, a.decidedAt, a.decidedByLabel = "expired", true, "system" }),
		p.with(func(a *approvalRow) {
			a.status, a.decidedAt, a.decidedByLabel, a.errText = "cancelled", true, "system", "initiator_changed"
		}),
		p.decide("failed", approverID).with(func(a *approvalRow) { a.errText = "platform.approval_stale" }),
	} {
		execArgs(t, db, insertApproval, a.args()...)
	}
	for _, c := range []struct {
		constraint string
		a          approvalRow
	}{
		{"approvals_action_format", p.with(func(a *approvalRow) { a.action = "StaffInvite" })},
		{"approvals_permission_format", p.with(func(a *approvalRow) { a.permission = "staff.roles" })},
		{"approvals_payload_versioned", p.with(func(a *approvalRow) { a.payload = `{}` })},
		{"approvals_payload_versioned", p.with(func(a *approvalRow) { a.payload = `{"v": "1"}` })},
		{"approvals_payload_versioned", p.with(func(a *approvalRow) { a.payload = `[1]` })},
		{"approvals_expiry", p.with(func(a *approvalRow) { a.expiresIn = "0 seconds" })},
		{"approvals_pending_undecided", p.with(func(a *approvalRow) { a.decidedAt = true })},
		{"approvals_pending_undecided", p.with(func(a *approvalRow) { a.decidedBy, a.decidedByLabel = approverID, "Игорь Л." })},
		{"approvals_decided_at", p.with(func(a *approvalRow) { a.status = "expired" })},
		{"approvals_decider_required", p.with(func(a *approvalRow) { a.status, a.decidedAt = "executed", true })},
		{"approvals_decider_label", p.decide("executed", approverID).with(func(a *approvalRow) { a.decidedByLabel = nil })},
		{"approvals_decider_label", p.decide("cancelled", initiatorID).with(func(a *approvalRow) { a.decidedByLabel = nil })},
		{"approvals_system_decider", p.with(func(a *approvalRow) { a.status, a.decidedAt, a.decidedByLabel = "expired", true, "Игорь Л." })},
		// метка system у живого запроса; у rejected/executed без решившего раньше сработал бы
		// approvals_decider_required (CHECK проверяются по алфавиту имён)
		{"approvals_system_decider", p.with(func(a *approvalRow) { a.decidedByLabel = "system" })},
		{"approvals_two_hands", p.decide("executed", initiatorID)},
		{"approvals_two_hands", p.decide("executed", targetID)},
		{"approvals_reject_comment", p.decide("rejected", approverID)},
		{"approvals_reject_comment", p.decide("rejected", approverID).with(func(a *approvalRow) { a.comment = "   " })},
		{"approvals_failed_error", p.decide("failed", approverID)},
		{"approvals_failed_error", p.decide("executed", approverID).with(func(a *approvalRow) { a.errText = "boom" })},
		{"approvals_failed_error", p.with(func(a *approvalRow) { a.status, a.decidedAt, a.errText = "expired", true, "boom" })},
		// решённое без решившего — только системой и с меткой system
		{"approvals_system_decider", p.with(func(a *approvalRow) { a.status, a.decidedAt = "expired", true })},
		{"approvals_system_decider", p.with(func(a *approvalRow) { a.status, a.decidedAt = "cancelled", true })},
		// отменяет человек — только инициатор и без error (error — у отменённого системой, §4.8)
		{"approvals_cancel_by_initiator", p.decide("cancelled", approverID)},
		{"approvals_cancel_by_initiator", p.decide("cancelled", initiatorID).with(func(a *approvalRow) { a.errText = "initiator_changed" })},
	} {
		wantArgsError(t, db, "23514", c.constraint, insertApproval, c.a.args()...)
	}
}

const insertAudit = `INSERT INTO audit_log (action, object_type, actor_user_id, actor_staff_id, actor_role, actor_label,
	scope_city_id) VALUES ('staff.probe', 'staff_account', $1::uuid, $2::uuid, $3, $4, $5::uuid)`

// Автор записи журнала (D11): не больше одного автора; роль — известная; у сотрудника — роль
// сотрудника и снимок имени; служебная роль (system, operator, anonymous) — только без автора, а
// без автора — только служебная (спека §3.2: роль обязательна); снимок имени непустой.
func TestStaff0020AuditActors(t *testing.T) {
	db := dbtest.New(t)
	for _, a := range [][]any{
		{nil, nil, "system", "system", nil},
		{nil, nil, "operator", "operator", nil},
		{nil, nil, "anonymous", nil, nil},                  // неудачный вход
		{nil, approverID, "moderator", "Мария К.", cityID}, // сотрудник в своём городе
		{targetID, nil, "player", nil, nil},
		{targetID, nil, "admin", nil, nil}, // импорт geo со started_by — до перевода на actor_staff_id (план B)
	} {
		execArgs(t, db, insertAudit, a...)
	}
	for _, c := range []struct {
		constraint string
		args       []any
	}{
		{"audit_log_one_actor", []any{targetID, approverID, "owner", "Алексей М.", nil}},
		{"audit_log_actor_role", []any{targetID, nil, "city_moderator", nil, nil}},
		{"audit_log_staff_actor", []any{nil, approverID, "player", "Мария К.", nil}},
		{"audit_log_staff_actor", []any{nil, approverID, "owner", nil, nil}},
		{"audit_log_service_actor", []any{nil, nil, "owner", "Алексей М.", nil}},
		{"audit_log_service_actor", []any{targetID, nil, "system", nil, nil}},
		{"audit_log_service_actor", []any{targetID, nil, nil, nil, nil}},
		{"audit_log_service_actor", []any{nil, nil, nil, nil, nil}}, // запись без автора и без роли
		{"audit_log_actor_label", []any{nil, nil, "operator", "   ", nil}},
	} {
		wantArgsError(t, db, "23514", c.constraint, insertAudit, c.args...)
	}
}

// Роли игроков после 0020 (спека §3.2, M1): только допуск капитана в городе.
func TestStaff0020RoleAssignments(t *testing.T) {
	db := dbtest.New(t)
	user := newUser(t, db)
	var city string
	if err := db.QueryRowContext(context.Background(), `SELECT id::text FROM cities WHERE slug = 'stavropol'`).Scan(&city); err != nil {
		t.Fatal(err)
	}
	const ins = `INSERT INTO role_assignments (user_id, role, scope_type, scope_id) VALUES ($1, $2::role_name, $3::role_scope, $4::uuid)`
	execArgs(t, db, ins, user, "captain", "city", city)
	wantArgsError(t, db, "22P02", "", ins, user, "admin", "city", city)
	wantArgsError(t, db, "22P02", "", ins, user, "captain", "global", nil)
	wantArgsError(t, db, "23514", "role_scope_id_present", ins, user, "captain", "city", nil)
}

// Роль сотрудника в role_assignments на старой базе: 0020 падает с понятным текстом, транзакция
// миграции откатывается целиком; допуск капитана в городе переживает миграцию.
func TestStaff0020RefusesStaffRoleAssignments(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, staffVersion-1); err != nil {
		t.Fatalf("down 0020: %v", err)
	}
	user := newUser(t, db)
	execArgs(t, db, `INSERT INTO role_assignments (user_id, role, scope_type) VALUES ($1, 'admin', 'global')`, user)

	_, err := p.UpTo(ctx, staffVersion)
	const wantMsg = "role_assignments: строк с ролями сотрудников или не городским скоупом — 1"
	if err == nil || !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("ждали ошибку %q, получили: %v", wantMsg, err)
	}
	if v, err := p.GetDBVersion(ctx); err != nil || v != staffVersion-1 {
		t.Fatalf("версия после отказа = %d (%v), ждали %d", v, err, staffVersion-1)
	}
	var accounts sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.staff_accounts')::text`).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if accounts.Valid {
		t.Fatal("после отказа осталась staff_accounts — миграция применилась частично")
	}

	execArgs(t, db, `UPDATE role_assignments SET role = 'captain', scope_type = 'city', scope_id = `+stavropol+`
		WHERE user_id = $1`, user)
	if _, err := p.UpTo(ctx, staffVersion); err != nil {
		t.Fatalf("up после переноса роли: %v", err)
	}
	var role, scope string
	if err := db.QueryRowContext(ctx, `SELECT role::text, scope_type::text FROM role_assignments WHERE user_id = $1`,
		user).Scan(&role, &scope); err != nil {
		t.Fatal(err)
	}
	if role != "captain" || scope != "city" {
		t.Fatalf("допуск после 0020: %s/%s", role, scope)
	}
}

// staffSnapshotSQL — всё, что задевает 0020: колонки и индексы audit_log, idempotency_keys,
// role_assignments, все ограничения и триггеры, значения enum-типов, функции и отношения public
// (без объектов расширений), комментарий idempotency_keys.user_id.
const staffSnapshotSQL = `
WITH t(rel) AS (VALUES ('audit_log'), ('idempotency_keys'), ('role_assignments'))
SELECT 'col ' || c.relname || '.' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
       || CASE WHEN a.attnotnull THEN ' not null' ELSE '' END
       || coalesce(' default ' || pg_get_expr(d.adbin, d.adrelid), '')
  FROM pg_attribute a
  JOIN pg_class c ON c.oid = a.attrelid AND c.relnamespace = 'public'::regnamespace
  LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
 WHERE c.relname IN (SELECT rel FROM t) AND a.attnum > 0 AND NOT a.attisdropped
UNION ALL
SELECT 'idx ' || indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename IN (SELECT rel FROM t)
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
SELECT 'fn ' || pr.proname
  FROM pg_proc pr
 WHERE pr.pronamespace = 'public'::regnamespace
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = pr.oid AND d.deptype = 'e')
UNION ALL
SELECT 'cmt idempotency_keys.user_id ' || coalesce(col_description('public.idempotency_keys'::regclass,
       (SELECT attnum FROM pg_attribute WHERE attrelid = 'public.idempotency_keys'::regclass AND attname = 'user_id')), '-')
UNION ALL
SELECT 'rel ' || c.relkind::text || ' ' || c.relname
  FROM pg_class c
 WHERE c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p', 'v', 'm')
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')
ORDER BY 1`

func snapshotOf(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// down 0020 возвращает схему 0019 в точности: типы role_name/role_scope со всеми значениями, CHECK
// role_scope_id_present с 'global', audit_log без новых колонок, без комментария user_id, без
// таблиц staff_* и approvals. Затем повторный up проходит.
func TestStaff0020DownRestoresSchema(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down до 0: %v", err)
	}
	if _, err := p.UpTo(ctx, staffVersion-1); err != nil {
		t.Fatalf("up до %d: %v", staffVersion-1, err)
	}
	before := snapshotOf(t, db, staffSnapshotSQL)

	if _, err := p.UpTo(ctx, staffVersion); err != nil {
		t.Fatalf("up 0020: %v", err)
	}
	if v, err := p.GetDBVersion(ctx); err != nil || v != staffVersion {
		t.Fatalf("версия после up = %d (%v), ждали %d", v, err, staffVersion)
	}
	up := snapshotOf(t, db, staffSnapshotSQL)
	for _, want := range []string{
		"rel r approvals", "rel r staff_accounts", "rel r staff_sessions", "fn staff_role_cities_check",
		"type role_name captain", "type role_scope city", "type staff_role owner,admin,moderator,support",
		"cmt idempotency_keys.user_id id субъекта: игрок (users) или сотрудник (staff_accounts); UUIDv7 не пересекаются",
		// keyset журнала по (created_at, id) — у частичных индексов автора и города тоже
		"idx CREATE INDEX audit_log_staff_actor_idx ON public.audit_log USING btree (actor_staff_id, created_at DESC, id DESC) WHERE (actor_staff_id IS NOT NULL)",
		"idx CREATE INDEX audit_log_scope_city_idx ON public.audit_log USING btree (scope_city_id, created_at DESC, id DESC) WHERE (scope_city_id IS NOT NULL)",
	} {
		if !slices.Contains(up, want) {
			t.Errorf("после up нет %q — снимок сравнивает не то", want)
		}
	}

	if _, err := p.DownTo(ctx, staffVersion-1); err != nil {
		t.Fatalf("down 0020: %v", err)
	}
	after := snapshotOf(t, db, staffSnapshotSQL)
	if extra, lost := missingFrom(after, before), missingFrom(before, after); len(extra) != 0 || len(lost) != 0 {
		t.Fatalf("down 0020 не вернул схему 0019:\nлишнее после отката: %q\nпропало после отката: %q", extra, lost)
	}
	for _, want := range []string{
		"type role_name player,captain,vice_captain,city_moderator,admin",
		"type role_scope global,country,city,team",
		"cmt idempotency_keys.user_id -",
	} {
		if !slices.Contains(after, want) {
			t.Errorf("после отката нет %q", want)
		}
	}
	if !slices.ContainsFunc(after, func(s string) bool {
		return strings.HasPrefix(s, "con role_assignments role_scope_id_present ") && strings.Contains(s, "'global'")
	}) {
		t.Error("после отката нет прежнего CHECK role_scope_id_present")
	}
	if _, err := p.UpTo(ctx, staffVersion); err != nil {
		t.Fatalf("повторный up 0020 после отката: %v", err)
	}
}
