package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// citySpec — операции с {cityId} в пути: в контракте плана A таких нет, право scoped по городу
// проверяется на подменённом контракте. Ключи — из каталога (не черновые).
const citySpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /v1/cities/{cityId}/names:
    put:
      operationId: renameCity
      x-permission: geo.cities.rename
      parameters:
        - {name: cityId, in: path, required: true, schema: {type: string}}
      responses: {"204": {description: ok}}
  /v1/cities/{cityId}/status:
    put:
      operationId: manageCity
      x-permission: geo.cities.manage
      parameters:
        - {name: cityId, in: path, required: true, schema: {type: string}}
      responses: {"204": {description: ok}}
  /v1/countries:
    get:
      operationId: listCountries
      x-permission: geo.countries.read
      responses: {"200": {description: ok}}
  /v1/stray:
    get:
      operationId: stray
      x-permission: self
      responses: {"200": {description: ok}}
`

// permServe — слой прав над подменённым контрактом: сотрудник s (nil — анонимно) в ctx, как его
// кладёт слой сессии; пропуск — 200 от хендлера-пробы.
func permServe(t *testing.T, rules *openapi3.T, routesSpec *openapi3.T, s *auth.Staff, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	mw, err := admin.Permissions(rules, quiet())
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(routesSpec)
	if err != nil {
		t.Fatal(err)
	}
	h := routes(mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	ctx := context.Background()
	if s != nil {
		ctx = auth.WithStaff(ctx, s)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, method, path, nil))
	return rec
}

func loadSpec(t *testing.T, src string) *openapi3.T {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromData([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// {cityId} в пути (P10): scoped модератора — свой город пропускается, чужой — 403; право allow
// — любой город; dual — пропуск (сценарий создаст запрос на подтверждение); без города —
// пропуск по Grant != Deny, не Decide(perm, uuid.Nil) (он дал бы Deny, ⚠️ 3a).
func TestPermissionCityFromPath(t *testing.T) {
	spec := loadSpec(t, citySpec)
	mod := staffOf(perm.Moderator, auth.StageActive, false, cityA)
	admn := staffOf(perm.Admin, auth.StageActive, false)
	dual := staffOf(perm.Support, auth.StageActive, false)
	dual.Grants = map[string]auth.Grant{"geo.cities.manage": auth.GrantDual}
	for _, c := range []struct {
		name   string
		s      *auth.Staff
		method string
		path   string
		status int
	}{
		{"модератор, свой город", mod, http.MethodPut, "/v1/cities/" + cityA.String() + "/names", http.StatusOK},
		{"модератор, чужой город", mod, http.MethodPut, "/v1/cities/" + cityB.String() + "/names", http.StatusForbidden},
		{"модератор, город не uuid", mod, http.MethodPut, "/v1/cities/stavropol/names", http.StatusForbidden},
		{"модератор, право deny", mod, http.MethodPut, "/v1/cities/" + cityA.String() + "/status", http.StatusForbidden},
		{"админ, любой город", admn, http.MethodPut, "/v1/cities/" + cityB.String() + "/names", http.StatusOK},
		{"dual — запрос подтверждения", dual, http.MethodPut, "/v1/cities/" + cityB.String() + "/status", http.StatusOK},
		{"без города, allow", mod, http.MethodGet, "/v1/countries", http.StatusOK},
	} {
		rec := permServe(t, spec, spec, c.s, c.method, c.path)
		if rec.Code != c.status {
			t.Errorf("%s: %d %s, ждали %d", c.name, rec.Code, rec.Body.String(), c.status)
		}
		if c.status == http.StatusForbidden && !strings.Contains(rec.Body.String(), `"code":"auth.forbidden"`) {
			t.Errorf("%s: %s", c.name, rec.Body.String())
		}
	}
}

// Fail-closed (P9): маршрут контракта без правила (правила строились по другому контракту) —
// 500 internal, хендлер не вызывается; запрос без сессии на операции не anonymous — 401.
func TestPermissionFailClosed(t *testing.T) {
	rules := loadSpec(t, citySpec)
	routesSpec := loadSpec(t, strings.Replace(citySpec, "/v1/stray:", "/v1/other:", 1))
	owner := staffOf(perm.Owner, auth.StageActive, false)
	rec := permServe(t, rules, routesSpec, owner, http.MethodGet, "/v1/other")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"code":"internal"`) {
		t.Fatalf("маршрут без правила: %d %s", rec.Code, rec.Body.String())
	}
	rec = permServe(t, rules, rules, nil, http.MethodGet, "/v1/countries")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"auth.unauthenticated"`) {
		t.Fatalf("без сессии: %d %s", rec.Code, rec.Body.String())
	}
}

// Правила строятся из контракта на старте: операция без x-permission, нестроковое значение, ключ
// вне каталога или черновой, handler без x-permission-any или с чужим ключом в нём,
// x-permission-any не у handler — ошибка старта, а не 403 в работе.
func TestPermissionRulesRejectBadContract(t *testing.T) {
	for name, perm := range map[string]string{
		"без x-permission":      ``,
		"не строка":             `x-permission: 5`,
		"пустая строка":         `x-permission: ""`,
		"ключ вне каталога":     `x-permission: geo.cities.fly`,
		"черновой ключ":         `x-permission: moderation.reports.read`,
		"handler без списка":    `x-permission: handler`,
		"handler, пустой":       "x-permission: handler\n      x-permission-any: []",
		"handler, не список":    "x-permission: handler\n      x-permission-any: staff.roles.grant",
		"handler, черновой":     "x-permission: handler\n      x-permission-any: [staff.roles.grant, moderation.bans.issue]",
		"handler, вне каталога": "x-permission: handler\n      x-permission-any: [staff.roles.fly]",
		"список не у handler":   "x-permission: self\n      x-permission-any: [staff.roles.grant]",
	} {
		spec := loadSpec(t, `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /v1/x:
    get:
      operationId: x
      `+perm+`
      responses: {"200": {description: ok}}
`)
		if _, err := admin.Permissions(spec, quiet()); err == nil {
			t.Errorf("%s: правила собраны", name)
		}
	}
}

// Отказ старта на настоящем контракте с порчей: операция без x-permission, с черновым ключом,
// с нестроковым x-rate-limit (P22) — NewHandler не собирается.
func TestNewHandlerRejectsBadBundle(t *testing.T) {
	o := testOptions(t)
	for name, spoil := range map[string]func(op *openapi3.Operation){
		"без x-permission":     func(op *openapi3.Operation) { delete(op.Extensions, "x-permission") },
		"черновой ключ":        func(op *openapi3.Operation) { op.Extensions["x-permission"] = "moderation.reports.read" },
		"x-rate-limit число":   func(op *openapi3.Operation) { op.Extensions["x-rate-limit"] = float64(5) },
		"x-rate-limit пустой":  func(op *openapi3.Operation) { op.Extensions["x-rate-limit"] = "" },
		"x-permission-any нет": func(op *openapi3.Operation) { delete(op.Extensions, "x-permission-any") },
	} {
		spec, err := oapi.GetSpec()
		if err != nil {
			t.Fatal(err)
		}
		id := "getStaffRoles"
		if name == "x-permission-any нет" {
			id = "createStaffInvite"
		}
		spoil(operation(t, spec, id))
		if _, err := admin.NewHandlerWithSpec(quiet(), o, spec); err == nil {
			t.Errorf("%s: собрано", name)
		}
	}
	// без порчи — собирается: отказ выше — от порчи, а не от сборки на подменённом контракте
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.NewHandlerWithSpec(quiet(), o, spec); err != nil {
		t.Fatal(err)
	}
}

// Все операции настоящего контракта получают правило: страж на старте пропускает текущий бандл.
func TestPermissionRulesForContract(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Permissions(spec, quiet()); err != nil {
		t.Fatal(err)
	}
}
