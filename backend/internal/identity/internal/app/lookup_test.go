package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
)

// Поиск игрока по почте и статистика способов входа (спека §3.15 п. 2, §7.1, §4.3; план I-4, Task 5). Сценарии — под
// ролью admin (newResetEnv), как в admin-api.

const lookupReason = "Обращение SUP-4812: не приходит код"

// lookupPlayer — игрок с почтой <nick>@example.com в городе city (uuid.Nil — без города).
func lookupPlayer(t *testing.T, r *resetEnv, nick string, city uuid.UUID) uuid.UUID {
	t.Helper()
	uid := seedUser(t, r.owner, nick, "ru")
	if city != uuid.Nil {
		acctExec(t, r.env, `UPDATE users SET city_id = $2 WHERE id = $1`, uid, city)
	}
	return uid
}

// wantNotFound — 404 identity.user_not_found.
func wantNotFound(t *testing.T, what string, err error) {
	t.Helper()
	if st, code := problemStatus(err); st != http.StatusNotFound || code != identity.CodeUserNotFound {
		t.Fatalf("%s: %v — ждали 404 %s", what, err, identity.CodeUserNotFound)
	}
}

func TestFindPlayerByEmail(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	c := seedCaptainCities(t, r.env)
	owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
	ctx := context.Background()

	plain := lookupPlayer(t, r, "look_plain", c.main)
	got, err := r.adm.FindPlayerByEmail(ctx, owner, "look_plain@example.com", lookupReason)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != plain || got.Nickname != "look_plain" || got.CityID != c.main ||
		got.Status != "active" || got.CreatedAt.IsZero() || got.MfaEnabled || got.MfaReset != nil {
		t.Fatalf("без 2FA: %+v", got)
	}

	unconf := lookupPlayer(t, r, "look_unconf", c.main)
	r.withMfa(t, unconf, false)
	if got, err := r.adm.FindPlayerByEmail(ctx, owner, "look_unconf@example.com", lookupReason); err != nil || got.MfaEnabled {
		t.Fatalf("неподтверждённая 2FA: %+v %v", got, err)
	}

	withReset := lookupPlayer(t, r, "look_reset", c.main)
	r.withMfa(t, withReset, true)
	rid := r.liveReset(t, withReset)
	got, err = r.adm.FindPlayerByEmail(ctx, owner, "look_reset@example.com", lookupReason)
	if err != nil || !got.MfaEnabled || got.MfaReset == nil || got.MfaReset.ID != rid ||
		!got.MfaReset.ExecuteAt.Equal(testNow.Add(7*24*time.Hour)) {
		t.Fatalf("живой сброс: %+v %v", got, err)
	}
	acctExec(t, r.env, `UPDATE mfa_resets SET cancelled_at = now(), cancelled_by = 'owner' WHERE id = $1`, rid)
	if got, err := r.adm.FindPlayerByEmail(ctx, owner, "look_reset@example.com", lookupReason); err != nil ||
		got.MfaReset != nil || !got.MfaEnabled {
		t.Fatalf("отменённый сброс: %+v %v", got, err)
	}

	pending := lookupPlayer(t, r, "look_pending", c.main)
	acctExec(t, r.env, `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'active',
		deletion_requested_at = $2, purge_at = $3 WHERE id = $1`, pending, testNow.Add(-time.Hour), testNow.Add(30*24*time.Hour))
	if got, err := r.adm.FindPlayerByEmail(ctx, owner, "look_pending@example.com", lookupReason); err != nil ||
		got.ID != pending || got.Status != "pending_deletion" {
		t.Fatalf("в удалении: %+v %v", got, err)
	}

	gone := lookupPlayer(t, r, "look_gone", c.main)
	acctExec(t, r.env, `UPDATE users SET status = 'deleted', deleted_at = $2 WHERE id = $1`, gone, testNow)
	_, err = r.adm.FindPlayerByEmail(ctx, owner, "look_gone@example.com", lookupReason)
	wantNotFound(t, "удалённый", err)
	_, err = r.adm.FindPlayerByEmail(ctx, owner, "nobody@example.com", lookupReason)
	wantNotFound(t, "нет почты", err)

	// без права (стадия не active или роль без identity.pii.read) — 403 до чтения
	half := staffMember(perm.Owner, "Ольга", uuid.Nil)
	half.Stage = auth.StageMFAPending
	if st, _ := problemStatus(func() error {
		_, err := r.adm.FindPlayerByEmail(ctx, half, "look_plain@example.com", lookupReason)
		return err
	}()); st != http.StatusForbidden {
		t.Fatalf("сессия без второго фактора: %d", st)
	}
}

// Модератор видит только игроков своих городов (R10, R37): чужой город и модератор без городов — 404, как «нет»;
// владелец и поддержка находят любого игрока с городом; игрок без города — 404 всем.
func TestFindPlayerScope(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	c := seedCaptainCities(t, r.env)
	ctx := context.Background()
	lookupPlayer(t, r, "scope_main", c.main)
	lookupPlayer(t, r, "scope_other", c.other)
	lookupPlayer(t, r, "scope_nocity", uuid.Nil)

	mod := staffMember(perm.Moderator, "Модератор", uuid.Nil)
	mod.CityIDs = []uuid.UUID{c.main}
	if got, err := r.adm.FindPlayerByEmail(ctx, mod, "scope_main@example.com", lookupReason); err != nil ||
		got.Nickname != "scope_main" {
		t.Fatalf("модератор, свой город: %+v %v", got, err)
	}
	_, err := r.adm.FindPlayerByEmail(ctx, mod, "scope_other@example.com", lookupReason)
	wantNotFound(t, "модератор, чужой город", err)
	_, err = r.adm.FindPlayerByEmail(ctx, mod, "scope_nocity@example.com", lookupReason)
	wantNotFound(t, "модератор, игрок без города", err)

	bare := staffMember(perm.Moderator, "Без городов", uuid.Nil)
	bare.CityIDs = []uuid.UUID{}
	for _, email := range []string{"scope_main@example.com", "scope_other@example.com"} {
		_, err := r.adm.FindPlayerByEmail(ctx, bare, email, lookupReason)
		wantNotFound(t, "модератор без городов: "+email, err)
	}

	for _, st := range []*auth.Staff{staffMember(perm.Owner, "Ольга", uuid.Nil),
		staffMember(perm.Support, "Поддержка", uuid.Nil)} {
		for _, nick := range []string{"scope_main", "scope_other"} {
			if got, err := r.adm.FindPlayerByEmail(ctx, st, nick+"@example.com", lookupReason); err != nil ||
				got.Nickname != nick {
				t.Fatalf("%s ищет %s: %+v %v", st.Role, nick, got, err)
			}
		}
		// игрок без города (город удалён) — для всех как «нет»: город в ответе обязателен (решение контроллера 17.11)
		_, err := r.adm.FindPlayerByEmail(ctx, st, "scope_nocity@example.com", lookupReason)
		wantNotFound(t, st.Role+", игрок без города", err)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM audit_log WHERE action = $1`, ActionPlayerLookup); n != 6 {
		t.Fatalf("записей player_lookup %d, ждали 6 (2 модератора + 2 без городов + 2 без города)", n)
	}
}

// auditRow — запись журнала сотрудника: действие, объект, город, before/after и строка записи целиком.
type auditRow struct {
	action, objectType, reason string
	objectID, scope            *uuid.UUID
	after, whole               string
}

func staffAudit(t *testing.T, r *resetEnv, staffID uuid.UUID) []auditRow {
	t.Helper()
	rows, err := r.owner.Query(context.Background(), `SELECT action, object_type, object_id, scope_city_id,
		coalesce(reason, ''), coalesce(after::text, ''), row_to_json(a)::text
		FROM audit_log a WHERE actor_staff_id = $1 ORDER BY created_at, id`, staffID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var a auditRow
		if err := rows.Scan(&a.action, &a.objectType, &a.objectID, &a.scope, &a.reason, &a.after, &a.whole); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Каждый поиск — в журнал (R10): найден — identity.pii_read по игроку с городом и причиной; не найден (нет почты, чужой
// город модератора) — identity.player_lookup без object_id, after {found: false}; почты в записи нет нигде.
func TestFindPlayerAudit(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	c := seedCaptainCities(t, r.env)
	ctx := context.Background()
	uid := lookupPlayer(t, r, "audit_found", c.main)
	lookupPlayer(t, r, "audit_foreign", c.other)

	sup := staffMember(perm.Support, "Поддержка", uuid.Nil)
	if _, err := r.adm.FindPlayerByEmail(ctx, sup, "audit_found@example.com", lookupReason); err != nil {
		t.Fatal(err)
	}
	_, err := r.adm.FindPlayerByEmail(ctx, sup, "audit_missing@example.com", lookupReason)
	wantNotFound(t, "нет почты", err)
	got := staffAudit(t, r, sup.StaffID)
	if len(got) != 2 {
		t.Fatalf("записей %d, ждали 2: %+v", len(got), got)
	}
	found, miss := got[0], got[1]
	if found.action != ActionPiiRead || found.objectType != objectUser || found.objectID == nil || *found.objectID != uid ||
		found.scope == nil || *found.scope != c.main || found.reason != lookupReason {
		t.Fatalf("найден: %+v", found)
	}
	if miss.action != ActionPlayerLookup || miss.objectType != objectUser || miss.objectID != nil ||
		miss.reason != lookupReason || strings.ReplaceAll(miss.after, " ", "") != `{"found":false}` {
		t.Fatalf("не найден: %+v", miss)
	}

	mod := staffMember(perm.Moderator, "Модератор", uuid.Nil)
	mod.CityIDs = []uuid.UUID{c.main}
	_, err = r.adm.FindPlayerByEmail(ctx, mod, "audit_foreign@example.com", lookupReason)
	wantNotFound(t, "чужой город", err)
	modRows := staffAudit(t, r, mod.StaffID)
	if len(modRows) != 1 || modRows[0].action != ActionPlayerLookup || modRows[0].objectID != nil {
		t.Fatalf("чужой город модератора — как «не найден»: %+v", modRows)
	}

	for _, a := range append(got, modRows...) {
		for _, needle := range []string{"@example.com", "audit_found@", "audit_missing", "audit_foreign"} {
			if strings.Contains(a.whole, needle) {
				t.Fatalf("почта в журнале (%s): %s", needle, a.whole)
			}
		}
	}
}

// Статистика способов входа: всегда три строки apple, google, passkeys; до I-3 (привязок и ключей нет) — нули.
func TestAuthMethodsUsage(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	seedUser(t, r.owner, "usage_pw", "ru")
	got, err := r.adm.AuthMethodsUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []AuthMethodUsage{{Method: "apple"}, {Method: "google"}, {Method: "passkeys"}}
	if len(got) != len(want) {
		t.Fatalf("строк %d: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("строка %d: %+v, ждали %+v", i, got[i], want[i])
		}
	}
}
