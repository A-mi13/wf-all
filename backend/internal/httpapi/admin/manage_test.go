package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/staff"
)

// Управление сотрудниками (тег staff, спека §4.9) на настоящих сценариях за полным конвейером: списки и
// карточка, матрица ролей, смена роли (200 сразу / 202 вдвоём), выключение, сброс доступа, повторная
// отправка, чужие сессии. Ответы сверяются с контрактом (apitest).

// city — город для модератора (справочник geo — только внешний ключ staff_role_cities).
func (e *staffEnv) city(slug string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO cities (country_id, name, slug, timezone, centroid)
		SELECT id, $1, $1, 'Europe/Moscow', ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326)::geography
		FROM countries WHERE code = 'RU' RETURNING id`, slug).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// moderator — приглашённый модератор с городами: роль и города одним оператором (проверка «модератор ⇔
// хотя бы один город» отложена до COMMIT).
func (e *staffEnv) moderator(email string, cities ...uuid.UUID) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	now := e.clock.Now()
	e.exec(`INSERT INTO staff_accounts (id, email, display_name, locale, status, created_at)
		VALUES ($1, $2, 'Модератор', 'ru', 'invited', $3)`, id, email, now)
	e.exec(`WITH r AS (INSERT INTO staff_roles (staff_id, role, granted_at) VALUES ($1, 'moderator', $2))
		INSERT INTO staff_role_cities (staff_id, city_id) SELECT $1, unnest($3::uuid[])`, id, now, cities)
	return id
}

func (e *staffEnv) put(path, token, body string) *httpRec {
	return e.send(http.MethodPut, path, token, uuid.NewString(), body)
}

func confirmed(reason, confirm string) string {
	b, _ := json.Marshal(map[string]string{"reason": reason, "comment": "перешёл в другой проект", "confirm": confirm})
	return string(b)
}

func roleBody(role string, cities ...uuid.UUID) string {
	b, _ := json.Marshal(map[string]any{"role": role, "city_ids": append([]uuid.UUID{}, cities...)})
	return string(b)
}

// approvalAction — действие запроса из 202 {approval}.
func approvalAction(t *testing.T, rec *httpRec) string {
	t.Helper()
	wantStatus(t, rec, http.StatusAccepted)
	var b oapi.ApprovalAccepted
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return string(b.Approval.Action)
}

func account(t *testing.T, rec *httpRec) oapi.StaffAccount {
	t.Helper()
	wantStatus(t, rec, http.StatusOK)
	var a oapi.StaffAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

// Список (фильтры), карточка (города, приглашение), 404 на несуществующего, матрица ролей.
func TestManageAccountsRead(t *testing.T) {
	e := newStaffEnv(t)
	a := e.owners(1)[0]
	e.v = apitest.New(t, oapi.GetSpec)
	city := e.city("vremensk")
	sup := e.invite("sup@wf.example", "support")
	mod := e.moderator("mod@wf.example", city)

	list := func(query string) []uuid.UUID {
		t.Helper()
		rec := e.get("/v1/staff/accounts"+query, a.token)
		wantStatus(t, rec, http.StatusOK)
		var p oapi.StaffAccountPage
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		out := make([]uuid.UUID, 0, len(p.Items))
		for _, it := range p.Items {
			out = append(out, it.Id)
		}
		return out
	}
	if got := list(""); len(got) != 3 || !slices.Contains(got, a.id) || !slices.Contains(got, sup) || !slices.Contains(got, mod) {
		t.Fatalf("все: %v", got)
	}
	if got := list("?role=support"); !slices.Equal(got, []uuid.UUID{sup}) {
		t.Fatalf("role=support: %v", got)
	}
	if got := list("?status=active"); !slices.Equal(got, []uuid.UUID{a.id}) {
		t.Fatalf("status=active: %v", got)
	}
	if got := list("?q=MOD"); !slices.Equal(got, []uuid.UUID{mod}) {
		t.Fatalf("q=MOD: %v", got)
	}
	// курсор: страницы по одной без пропусков и повторов
	var walked []uuid.UUID
	cursor := ""
	for range 5 {
		rec := e.get("/v1/staff/accounts?limit=1"+cursor, a.token)
		wantStatus(t, rec, http.StatusOK)
		var p oapi.StaffAccountPage
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || len(p.Items) != 1 {
			t.Fatalf("страница: %v %s", err, rec.Body.String())
		}
		walked = append(walked, p.Items[0].Id)
		if p.NextCursor == nil {
			break
		}
		cursor = "&cursor=" + *p.NextCursor
	}
	if all := list(""); !slices.Equal(walked, all) {
		t.Fatalf("курсор: %v, без курсора %v", walked, all)
	}

	card := account(t, e.get("/v1/staff/accounts/"+mod.String(), a.token))
	if card.Role != "moderator" || card.Status != "invited" || !slices.Equal(card.CityIds, []uuid.UUID{city}) ||
		card.Invite != nil || card.Version < 1 {
		t.Fatalf("карточка модератора: %+v", card)
	}
	if card := account(t, e.get("/v1/staff/accounts/"+sup.String(), a.token)); card.Invite == nil ||
		card.Invite.Purpose != "invite" || card.Invite.Expired || card.CityIds == nil || len(card.CityIds) != 0 {
		t.Fatalf("карточка поддержки: %+v", card)
	}
	wantProblem(t, e.get("/v1/staff/accounts/"+uuid.NewString(), a.token), http.StatusNotFound, staff.CodeNotFound)

	rec := e.get("/v1/staff/roles", a.token)
	wantStatus(t, rec, http.StatusOK)
	var roles oapi.StaffRoles
	if err := json.Unmarshal(rec.Body.Bytes(), &roles); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(roles.Roles, []oapi.StaffRole{"owner", "admin", "moderator", "support"}) {
		t.Fatalf("роли %v", roles.Roles)
	}
	i := slices.IndexFunc(roles.Permissions, func(p oapi.RolePermission) bool { return p.Key == "staff.roles.grant" })
	if i < 0 || roles.Permissions[i].Grants != (oapi.RoleGrants{Owner: "dual", Admin: "dual", Moderator: "deny", Support: "deny"}) ||
		!roles.Permissions[i].Mutating {
		t.Fatalf("staff.roles.grant: %+v", roles.Permissions)
	}
}

// Сессии чужой учётки (⚠️ 9a): админ видит только модераторов и поддержки — сессии владельца и свои через
// карточку — 403; завершить сессии владельца — 403, поддержки — 200 с числом; свои — 409 self_action;
// несуществующий — 404.
func TestAccountSessionsAdminScope(t *testing.T) {
	e := newStaffEnv(t)
	a := e.owners(1)[0]
	adm := e.firstLogin("adm@wf.example", string(perm.Admin))
	sup := e.firstLogin("sup@wf.example", string(perm.Support))
	e.v = apitest.New(t, oapi.GetSpec)

	wantProblem(t, e.get("/v1/staff/accounts/"+a.id.String()+"/sessions", adm.token), http.StatusForbidden, httpx.CodeForbidden)
	wantProblem(t, e.get("/v1/staff/accounts/"+adm.id.String()+"/sessions", adm.token), http.StatusForbidden, httpx.CodeForbidden)
	rec := e.get("/v1/staff/accounts/"+sup.id.String()+"/sessions", adm.token)
	wantStatus(t, rec, http.StatusOK)
	var list oapi.StaffSessionList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 || list.Items[0].Current {
		t.Fatalf("сессии поддержки: %v %s", err, rec.Body.String())
	}
	// владелец — сессии админа видит
	wantStatus(t, e.get("/v1/staff/accounts/"+adm.id.String()+"/sessions", a.token), http.StatusOK)
	wantProblem(t, e.get("/v1/staff/accounts/"+uuid.NewString()+"/sessions", a.token), http.StatusNotFound, staff.CodeNotFound)

	revoke := func(id uuid.UUID, token string) *httpRec {
		return e.post("/v1/staff/accounts/"+id.String()+"/sessions/revoke-all", token, "")
	}
	wantProblem(t, revoke(a.id, adm.token), http.StatusForbidden, httpx.CodeForbidden)
	wantProblem(t, revoke(adm.id, adm.token), http.StatusConflict, staff.CodeSelfAction)
	wantProblem(t, revoke(uuid.New(), adm.token), http.StatusNotFound, staff.CodeNotFound)
	rec = revoke(sup.id, adm.token)
	wantStatus(t, rec, http.StatusOK)
	if got := bodyOf(t, rec)["revoked"]; got != float64(1) {
		t.Fatalf("revoked = %v", got)
	}
	if e.sessionReason(sup.token) == "" {
		t.Fatal("сессия поддержки жива")
	}
}

// Смена роли: только снятие города — сразу (200 с учёткой); смена роли — 202 staff.role_change; без
// изменений — 200; своя — 409; нет учётки — 404; модератор без городов — 400 body.city_ids; несуществующий
// город — 422. Выключение: почта не та — 400 body.confirm; поддержка — 200 disabled; повторно — 409
// wrong_stage; владелец — 202 staff.revoke_owner; себя — 409. Выключенную цель сменить или сбросить — 403
// (⚠️ журнал: wrong_stage в контракте нет). Сброс доступа — 202 staff.access_reset; свой — 409.
func TestManageChangeRoleDisableReset(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	c1, c2 := e.city("vremensk"), e.city("pokrovsk")
	mod := e.moderator("mod@wf.example", c1, c2)
	sup := e.invite("sup@wf.example", "support")
	path := func(id uuid.UUID, op string) string { return "/v1/staff/accounts/" + id.String() + "/" + op }

	if got := account(t, e.put(path(mod, "role"), a.token, roleBody("moderator", c1))); !slices.Equal(got.CityIds, []uuid.UUID{c1}) ||
		got.Role != "moderator" {
		t.Fatalf("снятие города: %+v", got)
	}
	if got := account(t, e.put(path(mod, "role"), a.token, roleBody("moderator", c1))); !slices.Equal(got.CityIds, []uuid.UUID{c1}) {
		t.Fatalf("без изменений: %+v", got)
	}
	if act := approvalAction(t, e.put(path(mod, "role"), a.token, roleBody("support"))); act != "staff.role_change" {
		t.Fatalf("смена роли: %s", act)
	}
	if e.count(`SELECT count(*) FROM staff_roles WHERE staff_id = $1 AND role = 'moderator'`, mod) != 1 {
		t.Fatal("202 сменил роль до подтверждения")
	}
	wantProblem(t, e.put(path(a.id, "role"), a.token, roleBody("admin")), http.StatusConflict, staff.CodeSelfAction)
	wantProblem(t, e.put(path(uuid.New(), "role"), a.token, roleBody("support")), http.StatusNotFound, staff.CodeNotFound)
	wantProblem(t, e.put(path(mod, "role"), a.token, roleBody("moderator", c1, uuid.New())), http.StatusUnprocessableEntity, staff.CodeCityNotFound)
	rec := e.put(path(mod, "role"), a.token, roleBody("moderator"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"body.city_ids"`) || !strings.Contains(rec.Body.String(), "min_items") {
		t.Fatalf("модератор без городов: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.post(path(sup, "disable"), a.token, confirmed("left_team", "other@wf.example"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"body.confirm"`) {
		t.Fatalf("почта не та: %d %s", rec.Code, rec.Body.String())
	}
	if got := account(t, e.post(path(sup, "disable"), a.token, confirmed("left_team", " SUP@wf.example "))); got.Status != "disabled" {
		t.Fatalf("выключение: %+v", got)
	}
	wantProblem(t, e.post(path(sup, "disable"), a.token, confirmed("left_team", "sup@wf.example")), http.StatusConflict, staff.CodeWrongStage)
	wantProblem(t, e.put(path(sup, "role"), a.token, roleBody("admin")), http.StatusForbidden, httpx.CodeForbidden)
	wantProblem(t, e.post(path(sup, "access-reset"), a.token, confirmed("security", "sup@wf.example")), http.StatusForbidden, httpx.CodeForbidden)
	if act := approvalAction(t, e.post(path(b.id, "disable"), a.token, confirmed("other", b.email))); act != "staff.revoke_owner" {
		t.Fatalf("выключение владельца: %s", act)
	}
	wantProblem(t, e.post(path(a.id, "disable"), a.token, confirmed("other", a.email)), http.StatusConflict, staff.CodeSelfAction)

	if act := approvalAction(t, e.post(path(mod, "access-reset"), a.token, confirmed("security", "mod@wf.example"))); act != "staff.access_reset" {
		t.Fatalf("сброс доступа: %s", act)
	}
	wantProblem(t, e.post(path(a.id, "access-reset"), a.token, confirmed("security", a.email)), http.StatusConflict, staff.CodeSelfAction)
}

// Приглашение: модератор без городов — 400 body.city_ids; несуществующий город — 422; почта действующей
// учётки — 409 email_taken; админ приглашает админа — 403 (право grant_elevated только у владельца).
func TestManageInviteErrors(t *testing.T) {
	e := newStaffEnv(t)
	a := e.owners(1)[0]
	adm := e.firstLogin("adm@wf.example", string(perm.Admin))
	e.v = apitest.New(t, oapi.GetSpec)
	rec := e.inviteReq(a.token, "", "new@wf.example", "moderator")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"body.city_ids"`) {
		t.Fatalf("модератор без городов: %d %s", rec.Code, rec.Body.String())
	}
	wantProblem(t, e.inviteReq(a.token, "", "new@wf.example", "moderator", uuid.New()), http.StatusUnprocessableEntity, staff.CodeCityNotFound)
	wantProblem(t, e.inviteReq(a.token, "", adm.email, "support"), http.StatusConflict, staff.CodeEmailTaken)
	wantProblem(t, e.inviteReq(adm.token, "", "new@wf.example", "admin"), http.StatusForbidden, httpx.CodeForbidden)
	if act := approvalAction(t, e.inviteReq(adm.token, "", "new@wf.example", "support")); act != "staff.invite" {
		t.Fatalf("админ приглашает поддержку: %s", act)
	}
	if e.count(`SELECT count(*) FROM approvals`) != 1 {
		t.Fatal("отказы создали запросы")
	}
}

// Повторная отправка приглашения: пауза 60 с от выпуска — 429 с Retry-After; после паузы — 204; вход уже
// настроен — 409 wrong_stage; себе — 409 self_action; нет учётки — 404.
func TestManageResendInvite(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	sup := e.invite("sup@wf.example", "support")
	resend := func(id uuid.UUID) *httpRec {
		return e.post("/v1/staff/accounts/"+id.String()+"/invite/resend", a.token, "")
	}
	rec := resend(sup)
	wantProblem(t, rec, http.StatusTooManyRequests, httpx.CodeRateLimited)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatalf("без Retry-After: %v", rec.Header())
	}
	e.clock.Advance(61 * time.Second)
	wantStatus(t, resend(sup), http.StatusNoContent)
	wantProblem(t, resend(sup), http.StatusTooManyRequests, httpx.CodeRateLimited)
	wantProblem(t, resend(b.id), http.StatusConflict, staff.CodeWrongStage)
	wantProblem(t, resend(a.id), http.StatusConflict, staff.CodeSelfAction)
	wantProblem(t, resend(uuid.New()), http.StatusNotFound, staff.CodeNotFound)
}
