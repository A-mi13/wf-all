package app_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/page"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
)

// Список «Роли и доступ»: право staff.accounts.read; новые сверху, курсор; фильтры статуса и роли
// (неизвестное — query.status / query.role enum); q — начало почты или имени без учёта регистра, % и _
// — буквально; города модератора; у приглашённых — живое приглашение (назначение, срок, просрочено).
func TestAccountsList(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c := e.city("kazan")
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner", Name: "Анна В."}))
	mod := e.addMember("mod_1@wf.example", memberOpts{Role: "moderator", Name: "Иван Петров", Cities: []uuid.UUID{c}})
	e.exec(`UPDATE staff_accounts SET created_at = $2 WHERE id = $1`, mod.ID, t0.Add(-time.Hour))
	inv, _ := e.invitee("new@wf.example", memberOpts{Role: "support", Name: "Новый"}, purposeInvite)
	e.exec(`UPDATE staff_accounts SET created_at = $2 WHERE id = $1`, inv.ID, t0)
	off := e.addMember("off@wf.example", memberOpts{Role: "admin", Name: "Выкл", Status: "disabled"})
	e.exec(`UPDATE staff_accounts SET created_at = $2 WHERE id = $1`, off.ID, t0.Add(time.Hour))

	all, next, err := e.app.Accounts(ctx, owner, staff.AccountFilter{})
	if err != nil || next != "" || len(all) != 4 {
		t.Fatalf("все: %d %q %v", len(all), next, err)
	}
	if all[0].ID != off.ID || all[1].ID != inv.ID || all[2].ID != mod.ID {
		t.Fatalf("порядок: %v", ids(all))
	}
	m := all[2]
	if m.Role != "moderator" || !slices.Equal(m.CityIDs, []uuid.UUID{c}) || m.Invite != nil || m.Email != "mod_1@wf.example" {
		t.Fatalf("модератор %+v", m)
	}
	if all[3].CityIDs == nil || len(all[3].CityIDs) != 0 {
		t.Fatalf("города владельца — пустой список: %#v", all[3].CityIDs)
	}
	iv := all[1].Invite
	if iv == nil || iv.Purpose != purposeInvite || iv.Expired || !iv.ExpiresAt.Equal(t0.Add(24*time.Hour)) {
		t.Fatalf("приглашение %+v", iv)
	}
	// ровно в срок — просрочено
	e.clock.Set(t0.Add(24 * time.Hour))
	if got, _, _ := e.app.Accounts(ctx, owner, staff.AccountFilter{Status: "invited"}); len(got) != 1 || !got[0].Invite.Expired {
		t.Fatalf("просрочено: %+v", got)
	}
	for _, f := range []struct {
		f    staff.AccountFilter
		want []uuid.UUID
	}{
		{staff.AccountFilter{Status: "disabled"}, []uuid.UUID{off.ID}},
		{staff.AccountFilter{Role: "moderator"}, []uuid.UUID{mod.ID}},
		{staff.AccountFilter{Q: "ИВА"}, []uuid.UUID{mod.ID}},
		{staff.AccountFilter{Q: "MOD_"}, []uuid.UUID{mod.ID}},
		{staff.AccountFilter{Q: "mod%"}, nil},
		{staff.AccountFilter{Q: "m_d"}, nil},
		{staff.AccountFilter{Q: "петров"}, nil}, // начало имени, а не слово в середине
		{staff.AccountFilter{Q: "o", Status: "active"}, []uuid.UUID{e.accountID("owner@wf.example")}},
	} {
		got, _, err := e.app.Accounts(ctx, owner, f.f)
		if err != nil || !slices.Equal(ids(got), f.want) {
			t.Errorf("%+v: %v %v", f.f, ids(got), err)
		}
	}
	// страницы по 2: без дублей и пропусков
	p1, cur, err := e.app.Accounts(ctx, owner, staff.AccountFilter{Limit: 2})
	if err != nil || len(p1) != 2 || cur == "" {
		t.Fatalf("стр. 1: %d %q %v", len(p1), cur, err)
	}
	p2, cur2, err := e.app.Accounts(ctx, owner, staff.AccountFilter{Limit: 2, Cursor: cur})
	if err != nil || len(p2) != 2 || cur2 != "" || !slices.Equal(append(ids(p1), ids(p2)...), ids(all)) {
		t.Fatalf("стр. 2: %v %q %v", ids(p2), cur2, err)
	}
	_, _, err = e.app.Accounts(ctx, owner, staff.AccountFilter{Status: "gone"})
	wantFieldError(t, err, "query.status", "enum")
	_, _, err = e.app.Accounts(ctx, owner, staff.AccountFilter{Role: "boss"})
	wantFieldError(t, err, "query.role", "enum")
	if _, _, err = e.app.Accounts(ctx, owner, staff.AccountFilter{Cursor: "мусор"}); !errors.Is(err, page.ErrBadCursor) {
		t.Fatalf("курсор: %v", err)
	}
	_, _, err = e.app.Accounts(ctx, e.as(mod), staff.AccountFilter{})
	wantForbidden(t, err)
	// карточка
	card, err := e.app.Account(ctx, owner, mod.ID)
	if err != nil || card.ID != mod.ID || card.Role != "moderator" || !slices.Equal(card.CityIDs, []uuid.UUID{c}) {
		t.Fatalf("карточка %+v %v", card, err)
	}
	if _, err := e.app.Account(ctx, owner, uuid.New()); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет учётки: %v", err)
	}
}

func ids(vs []staff.AccountView) []uuid.UUID {
	var out []uuid.UUID
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

// Чужие сессии: список — staff.accounts.read, завершение — staff.sessions.revoke; админ — только
// модераторов и поддержки (403 для владельца и админа); над собой завершать нельзя (self_action); нет
// учётки — not_found. Завершение — аудит staff.sessions_revoked от сотрудника.
func TestAccountSessions(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	om := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	owner := e.as(om)
	am := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	admin := e.as(am)
	sup := e.addMember("sup@wf.example", memberOpts{Role: "support"})
	e.rawSession(sup.ID, "active", netip4("198.51.100.7"), "Firefox", t0)
	e.rawSession(sup.ID, "active", ipHome, "", t0.Add(time.Minute))

	got, err := e.app.AccountSessions(ctx, admin, sup.ID)
	if err != nil || len(got) != 2 || got[0].Current || got[1].IP != "198.51.100.0/24" || got[0].UserAgent != nil {
		t.Fatalf("сессии поддержки: %+v %v", got, err)
	}
	if got, err := e.app.AccountSessions(ctx, owner, am.ID); err != nil || len(got) != 1 {
		t.Fatalf("владелец видит сессии админа: %+v %v", got, err)
	}
	_, err = e.app.AccountSessions(ctx, admin, om.ID)
	wantForbidden(t, err)
	if _, err := e.app.AccountSessions(ctx, owner, uuid.New()); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет учётки: %v", err)
	}
	_, err = e.app.AccountSessions(ctx, e.as(sup), am.ID)
	wantForbidden(t, err)

	_, err = e.app.RevokeAccountSessions(ctx, admin, om.ID, metaWeb)
	wantForbidden(t, err)
	if _, err := e.app.RevokeAccountSessions(ctx, admin, am.ID, metaWeb); !errors.Is(err, staff.ErrSelfAction) {
		t.Fatalf("над собой: %v", err)
	}
	if _, err := e.app.RevokeAccountSessions(ctx, admin, uuid.New(), metaWeb); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет учётки: %v", err)
	}
	// две сессии из базы и третья — e.as(sup) выше
	n, err := e.app.RevokeAccountSessions(ctx, admin, sup.ID, metaWeb)
	if err != nil || n != 3 {
		t.Fatalf("завершено %d %v", n, err)
	}
	if left, _ := e.app.AccountSessions(ctx, owner, sup.ID); len(left) != 0 {
		t.Fatalf("осталось %d", len(left))
	}
	au := e.audits("staff.sessions_revoked")
	if len(au) != 1 || au[0].StaffID == nil || *au[0].StaffID != am.ID || *au[0].ObjectID != sup.ID {
		t.Fatalf("аудит %+v", au)
	}
	e.wantAuditMeta("staff.sessions_revoked")
	if n, err := e.app.RevokeAccountSessions(ctx, owner, am.ID, metaWeb); err != nil || n != 1 {
		t.Fatalf("владелец → админ: %d %v", n, err)
	}
}

// Матрица ролей — та же, что perm (без черновых строк), значения — Grant.String().
func TestRolesMatrix(t *testing.T) {
	e := newManageEnv(t)
	v := e.app.RolesMatrix()
	if !slices.Equal(v.Roles, []string{"owner", "admin", "moderator", "support"}) {
		t.Fatalf("роли %v", v.Roles)
	}
	var want []perm.Permission
	for _, p := range perm.Catalog {
		if !p.Draft {
			want = append(want, p)
		}
	}
	if len(v.Permissions) != len(want) {
		t.Fatalf("прав %d, ждали %d", len(v.Permissions), len(want))
	}
	for i, p := range want {
		got := v.Permissions[i]
		if got.Key != p.Key || got.Mutating != p.Mutating || len(got.Grants) != len(perm.Roles) {
			t.Fatalf("%d: %+v, ждали %+v", i, got, p)
		}
		for _, r := range perm.Roles {
			if got.Grants[string(r)] != perm.Matrix[p.Key][r].String() {
				t.Fatalf("%s/%s: %q", p.Key, r, got.Grants[string(r)])
			}
		}
	}
}

// Автор управляющего действия перепроверяется в момент действия (M-4): выключен между загрузкой
// сессии и действием — auth.ErrSessionInvalid, ничего не меняется; его сессию погасили — то же.
func TestManageActorRechecked(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c := e.city("kazan")
	e.addMember("owner.a@wf.example", memberOpts{Role: "owner"})
	am := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	admin := e.as(am)
	sup := e.addMember("sup@wf.example", memberOpts{Role: "support"})
	mod := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c, e.city("perm")}})
	inv, _ := e.invitee("inv@wf.example", memberOpts{Role: "support"}, purposeInvite)
	e.clock.Advance(2 * time.Minute) // пауза повторной отправки прошла
	if err := e.app.OperatorDisable(ctx, am.Email); err != nil {
		t.Fatal(err)
	}
	checks := map[string]error{}
	_, checks["Disable"] = e.app.Disable(ctx, admin, sup.ID, "other", "", sup.Email, metaWeb)
	_, checks["ChangeRole"] = e.app.ChangeRole(ctx, admin, mod.ID, perm.Moderator, []uuid.UUID{c}, metaWeb)
	_, checks["ResetAccess"] = e.app.ResetAccess(ctx, admin, sup.ID, "other", "", sup.Email, metaWeb)
	_, checks["Invite"] = e.app.Invite(ctx, admin, staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "support"}, metaWeb)
	_, checks["RevokeAccountSessions"] = e.app.RevokeAccountSessions(ctx, admin, sup.ID, metaWeb)
	checks["ResendInvite"] = e.app.ResendInvite(ctx, admin, inv.ID, metaWeb)
	for name, err := range checks {
		if !errors.Is(err, auth.ErrSessionInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if e.acct(sup.ID).Status != "active" || len(e.citiesOf(mod.ID)) != 2 || e.count(`SELECT count(*) FROM approvals`) != 0 ||
		len(e.invites(inv.ID)) != 1 {
		t.Fatal("действие выключенного автора что-то изменило")
	}
}

// Секретов нет в аудите, аргументах River и payload запросов: код приглашения (исполнение приглашения и
// сброса), комментарий причины — только в audit_log.reason, не в before/after.
func TestNoSecretsInManage(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	ap, err := e.app.Invite(ctx, a, staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "support"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	e.mustApprove(b, ap.ID)
	n := e.accountID("n@wf.example")
	secrets := []string{e.currentCode(n)}
	x := e.addMember("x@wf.example", memberOpts{Role: "support"})
	r, err := e.app.ResetAccess(ctx, a, x.ID, "other", "секретный комментарий", x.Email, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	e.mustApprove(b, r.ID)
	secrets = append(secrets, e.currentCode(x.ID))
	code, err := e.app.OperatorResetAccess(ctx, "n@wf.example")
	if err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, code)
	for _, s := range secrets {
		norm := strings.ReplaceAll(s, "-", "")
		for _, q := range []string{
			`SELECT count(*) FROM audit_log WHERE concat_ws(' ', before::text, after::text, reason) ILIKE '%' || $1 || '%'`,
			`SELECT count(*) FROM river_job WHERE args::text ILIKE '%' || $1 || '%'`,
			`SELECT count(*) FROM approvals WHERE payload::text ILIKE '%' || $1 || '%'`,
		} {
			if n := e.count(q, s); n != 0 {
				t.Fatalf("секрет %q: %s", s, q)
			}
			if n := e.count(q, norm); n != 0 {
				t.Fatalf("секрет %q: %s", norm, q)
			}
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE concat_ws(' ', before::text, after::text) LIKE '%секретный%'`); n != 0 {
		t.Fatal("комментарий в before/after аудита")
	}
	if st, _ := e.apState(r.ID); st != approvals.StatusExecuted {
		t.Fatal(st)
	}
}
