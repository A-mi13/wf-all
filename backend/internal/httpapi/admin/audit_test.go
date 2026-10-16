package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Журнал действий (GET /v1/audit, спека §5.3) за полным конвейером: скоуп модератора (свои города и свои
// записи; без городов — только свои), фильтры (action_prefix по сегментам, from/to с миллисекундами,
// from ≥ to — 400), маска IP, поля записи по контракту.

type auditSeed struct {
	id, staff, city         uuid.UUID
	action, label, ip, role string
	at                      time.Time
}

func seedAudit(t *testing.T, owner *pgxpool.Pool, rs ...auditSeed) {
	t.Helper()
	for _, r := range rs {
		role, label := r.role, r.label
		var staffID, city *uuid.UUID
		if r.staff != uuid.Nil {
			staffID = &r.staff
		}
		if r.city != uuid.Nil {
			city = &r.city
		}
		if role == "" {
			role = "system"
		}
		if _, err := owner.Exec(context.Background(), `INSERT INTO audit_log (id, actor_staff_id, actor_role, actor_label,
			action, object_type, scope_city_id, before, after, reason, ip, user_agent, created_at)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, 'staff_account', $6, '{"status":"active"}', '{"status":"disabled"}',
			'left_team', NULLIF($7, '')::inet, 'Mozilla/5.0', $8)`,
			r.id, staffID, role, label, r.action, city, r.ip, r.at); err != nil {
			t.Fatal(err)
		}
	}
}

type auditEnv struct {
	h http.Handler
	v *apitest.Validator
}

func (e auditEnv) list(t *testing.T, token string, q url.Values) (*httpRec, []uuid.UUID) {
	t.Helper()
	rec := e.v.Do(t, e.h, call(http.MethodGet, "/v1/audit?"+q.Encode(), token, ""))
	if rec.Code != http.StatusOK {
		return rec, nil
	}
	var p oapi.AuditPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	ids := make([]uuid.UUID, 0, len(p.Items))
	for _, it := range p.Items {
		ids = append(ids, it.Id)
	}
	return rec, ids
}

func TestAuditList(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	t1 := time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC)
	stav, mih := uuid.New(), uuid.New()
	ownerS := staffOf(perm.Owner, auth.StageActive, false)
	mod := staffOf(perm.Moderator, auth.StageActive, false, stav)
	modNoCities := staffOf(perm.Moderator, auth.StageActive, false) // CityIDs nil (⚠️ 3b)
	other := uuid.New()
	r1, r2, r3, r4, r5, r6 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedAudit(t, pools.Owner,
		auditSeed{id: r1, staff: mod.StaffID, role: "moderator", label: "Модератор", action: "staff.login", ip: "203.0.113.7", at: t1},
		auditSeed{id: r2, staff: other, role: "admin", label: "Админ", action: "geo.city_updated", city: stav, at: t1.Add(time.Minute)},
		auditSeed{id: r3, staff: other, role: "admin", label: "Админ", action: "geo.city_updated", city: mih, at: t1.Add(2 * time.Minute)},
		auditSeed{id: r4, role: "anonymous", action: "staff.login_failed", at: t1.Add(3 * time.Minute)},
		auditSeed{id: r5, staff: ownerS.StaffID, role: "owner", label: "Ольга В.", action: "platform.approval_executed", ip: "2001:db8:abcd:12::1", at: t1.Add(4 * time.Minute)},
		auditSeed{id: r6, staff: modNoCities.StaffID, role: "moderator", label: "Без городов", action: "staff.logout", at: t1.Add(5 * time.Minute)},
	)
	o := testOptions(t)
	o.DB = pools.As
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{
		"owner": ownerS, "mod": mod, "no-cities": modNoCities,
		"support": staffOf(perm.Support, auth.StageActive, false),
	}}
	e := auditEnv{h: newHandler(t, o), v: apitest.New(t, oapi.GetSpec)}

	cases := []struct {
		name, token string
		q           url.Values
		want        []uuid.UUID
	}{
		{"владелец — всё", "owner", url.Values{}, []uuid.UUID{r6, r5, r4, r3, r2, r1}},
		{"поддержка — всё", "support", url.Values{}, []uuid.UUID{r6, r5, r4, r3, r2, r1}},
		{"модератор — свой город и свои", "mod", url.Values{}, []uuid.UUID{r2, r1}},
		{"модератор без городов — только свои", "no-cities", url.Values{}, []uuid.UUID{r6}},
		{"модератор, автор чужой", "mod", url.Values{"actor_staff_id": {other.String()}}, []uuid.UUID{r2}},
		{"действие и подвиды", "owner", url.Values{"action_prefix": {"staff.login"}}, []uuid.UUID{r1}},
		{"вкладка staff.", "owner", url.Values{"action_prefix": {"staff."}}, []uuid.UUID{r6, r4, r1}},
		{"вкладка platform.approval_", "owner", url.Values{"action_prefix": {"platform.approval_"}}, []uuid.UUID{r5}},
		{"from/to с миллисекундами", "owner", url.Values{"from": {"2026-10-13T21:01:00.000Z"}, "to": {"2026-10-13T21:03:00.000Z"}}, []uuid.UUID{r3, r2}},
		{"объект и тип", "owner", url.Values{"object_type": {"approval"}}, []uuid.UUID{}},
	}
	for _, c := range cases {
		rec, got := e.list(t, c.token, c.q)
		if rec.Code != http.StatusOK || !slices.Equal(got, c.want) {
			t.Errorf("%s: %d %v, ждали %v (%s)", c.name, rec.Code, got, c.want, rec.Body.String())
		}
	}

	// from не раньше to — 400 query.from range (и равные)
	for _, q := range []url.Values{
		{"from": {"2026-10-13T21:03:00.000Z"}, "to": {"2026-10-13T21:01:00Z"}},
		{"from": {"2026-10-13T21:01:00Z"}, "to": {"2026-10-13T21:01:00.000Z"}},
	} {
		rec, _ := e.list(t, "owner", q)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"query.from"`) || !strings.Contains(rec.Body.String(), `"range"`) {
			t.Errorf("%v: %d %s", q, rec.Code, rec.Body.String())
		}
	}
	rec, _ := e.list(t, "owner", url.Values{"cursor": {"x"}})
	wantProblem(t, rec, http.StatusBadRequest, httpx.CodeValidationFailed)

	// курсор: страницы без пропусков
	rec, first := e.list(t, "owner", url.Values{"limit": {"4"}})
	var p oapi.AuditPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.NextCursor == nil || len(first) != 4 {
		t.Fatalf("первая страница: %v %s", err, rec.Body.String())
	}
	if _, rest := e.list(t, "owner", url.Values{"limit": {"4"}, "cursor": {*p.NextCursor}}); !slices.Equal(rest, []uuid.UUID{r2, r1}) {
		t.Fatalf("вторая страница: %v", rest)
	}

	// поля записи: снимок имени, роль, маска IP (/24 и /48), было/стало, причина, UA
	rec, _ = e.list(t, "owner", url.Values{"limit": {"100"}})
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	byID := map[uuid.UUID]oapi.AuditRecord{}
	for _, it := range p.Items {
		byID[it.Id] = it
	}
	if g := byID[r1]; g.Ip == nil || *g.Ip != "203.0.113.0/24" || g.ActorLabel == nil || *g.ActorLabel != "Модератор" ||
		g.ActorStaffId == nil || *g.ActorStaffId != mod.StaffID || g.Reason == nil || *g.Reason != "left_team" ||
		g.Before == nil || (*g.Before)["status"] != "active" || g.UserAgent == nil || g.ScopeCityId != nil {
		t.Fatalf("запись r1: %+v", g)
	}
	if g := byID[r5]; g.Ip == nil || *g.Ip != "2001:db8:abcd::/48" {
		t.Fatalf("запись r5: %+v", g)
	}
	if g := byID[r4]; g.ActorStaffId != nil || g.ActorLabel != nil || g.Ip != nil || g.ActorRole == nil || *g.ActorRole != "anonymous" {
		t.Fatalf("запись r4: %+v", g)
	}
}
