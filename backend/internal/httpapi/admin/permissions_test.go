package admin_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Тест прав по всем операциям контракта admin (спека сотрудников §11, Task 10b): каждая операция × субъекты
// «владелец, админ, модератор своего города, модератор чужого, поддержка, без сессии, mfa_pending, setup,
// Reauth». Ожидания для ключей каталога выводятся из perm.Matrix, для handler — ручная таблица, сверенная с
// матрицей. Хендлеры нулевые (как cmd/admin-api до 10c): прошедший права запрос отвечает 501 заглушки
// (журнал — 200: он читает базу без сценариев), отказ конвейера — 401/403 со своим кодом.

// opRequest — запрос операции по схеме контракта (валидатор стоит до прав: тело обязано быть годным).
type opRequest struct{ method, path, body string }

var (
	anyID   = uuid.MustParse("0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c70")
	opsBase = map[string]opRequest{
		"staffLogin":                   {http.MethodPost, "/v1/staff/auth/login", `{"email":"olga.v@wf.example","password":"Жёлтая карточка на 90-й"}`},
		"staffVerifyMfa":               {http.MethodPost, "/v1/staff/auth/mfa", `{"code":"492817"}`},
		"staffUnlock":                  {http.MethodPost, "/v1/staff/auth/unlock", `{"code":"492817"}`},
		"staffLogout":                  {http.MethodPost, "/v1/staff/auth/logout", ""},
		"staffAcceptInvite":            {http.MethodPost, "/v1/staff/auth/invite/accept", `{"email":"olga.v@wf.example","code":"k7m2p-x4q6d","password":"Жёлтая карточка на 90-й"}`},
		"staffResendInviteSelf":        {http.MethodPost, "/v1/staff/auth/invite/resend", `{"email":"olga.v@wf.example"}`},
		"getStaffMe":                   {http.MethodGet, "/v1/staff/me", ""},
		"updateStaffMeLocale":          {http.MethodPatch, "/v1/staff/me/locale", `{"locale":"en"}`},
		"getStaffMfa":                  {http.MethodGet, "/v1/staff/me/mfa", ""},
		"staffMfaSetup":                {http.MethodPost, "/v1/staff/me/mfa/setup", ""},
		"staffMfaConfirm":              {http.MethodPost, "/v1/staff/me/mfa/confirm", `{"code":"492817"}`},
		"staffRecoveryAck":             {http.MethodPost, "/v1/staff/me/mfa/recovery-codes/ack", ""},
		"regenerateStaffRecoveryCodes": {http.MethodPost, "/v1/staff/me/mfa/recovery-codes", `{"code":"492817"}`},
		"changeStaffPassword":          {http.MethodPost, "/v1/staff/me/password", `{"current_password":"Жёлтая карточка на 90-й","code":"492817","new_password":"Свисток на 46-й минуте"}`},
		"listStaffMySessions":          {http.MethodGet, "/v1/staff/me/sessions", ""},
		"revokeStaffOtherSessions":     {http.MethodPost, "/v1/staff/me/sessions/revoke-others", ""},
		"revokeStaffMySession":         {http.MethodDelete, "/v1/staff/me/sessions/" + anyID.String(), ""},
		"createStaffInvite":            {http.MethodPost, "/v1/staff/invites", `{"email":"ivan.p@wf.example","display_name":"Иван П.","role":"support"}`},
		"listStaffAccounts":            {http.MethodGet, "/v1/staff/accounts", ""},
		"getStaffAccount":              {http.MethodGet, "/v1/staff/accounts/" + anyID.String(), ""},
		"changeStaffRole":              {http.MethodPut, "/v1/staff/accounts/" + anyID.String() + "/role", `{"role":"support","city_ids":[]}`},
		"disableStaffAccount":          {http.MethodPost, "/v1/staff/accounts/" + anyID.String() + "/disable", `{"reason":"left_team","confirm":"ivan.p@wf.example"}`},
		"resetStaffAccess":             {http.MethodPost, "/v1/staff/accounts/" + anyID.String() + "/access-reset", `{"reason":"security","confirm":"ivan.p@wf.example"}`},
		"resendStaffInvite":            {http.MethodPost, "/v1/staff/accounts/" + anyID.String() + "/invite/resend", ""},
		"listStaffAccountSessions":     {http.MethodGet, "/v1/staff/accounts/" + anyID.String() + "/sessions", ""},
		"revokeStaffAccountSessions":   {http.MethodPost, "/v1/staff/accounts/" + anyID.String() + "/sessions/revoke-all", ""},
		"getStaffRoles":                {http.MethodGet, "/v1/staff/roles", ""},
		"listGeoCities":                {http.MethodGet, "/v1/geo/cities", ""},
		"listAudit":                    {http.MethodGet, "/v1/audit", ""},
		"listApprovals":                {http.MethodGet, "/v1/approvals?box=incoming", ""},
		"approveApproval":              {http.MethodPost, "/v1/approvals/" + anyID.String() + "/approve", `{}`},
		"rejectApproval":               {http.MethodPost, "/v1/approvals/" + anyID.String() + "/reject", `{"comment":"Сначала обучение"}`},
		"cancelApproval":               {http.MethodPost, "/v1/approvals/" + anyID.String() + "/cancel", ""},
	}
	// handlerRoles — ручная таблица операций x-permission: handler: какие роли конвейер пропускает (точное
	// право и цель решает сценарий). Сверяется с матрицей: хоть одно право из x-permission-any не Deny.
	handlerRoles = map[string][]perm.Role{
		"createStaffInvite":   {perm.Owner, perm.Admin},
		"changeStaffRole":     {perm.Owner, perm.Admin},
		"disableStaffAccount": {perm.Owner, perm.Admin},
		"resetStaffAccess":    {perm.Owner, perm.Admin},
		"approveApproval":     {perm.Owner, perm.Admin},
		"rejectApproval":      {perm.Owner, perm.Admin},
	}
)

// subject — кто делает запрос: роль и стадия сессии (token "" — без сессии).
type subject struct {
	name  string
	token string
	staff *auth.Staff
}

func permissionSubjects() []subject {
	mk := func(name string, s *auth.Staff) subject { return subject{name: name, token: "tok-" + name, staff: s} }
	return []subject{
		mk("owner", staffOf(perm.Owner, auth.StageActive, false)),
		mk("admin", staffOf(perm.Admin, auth.StageActive, false)),
		mk("moderator-own", staffOf(perm.Moderator, auth.StageActive, false, cityA)),
		mk("moderator-foreign", staffOf(perm.Moderator, auth.StageActive, false, cityB)),
		mk("support", staffOf(perm.Support, auth.StageActive, false)),
		{name: "anonymous"},
		mk("mfa_pending", staffOf(perm.Owner, auth.StageMFAPending, false)),
		mk("setup", staffOf(perm.Owner, auth.StageSetup, false)),
		mk("reauth", staffOf(perm.Owner, auth.StageActive, true)),
	}
}

// outcome — ожидаемый ответ конвейера: pass — дошёл до хендлера.
type outcome struct {
	pass   bool
	status int
	code   string
}

var (
	pass       = outcome{pass: true}
	unauth     = outcome{status: http.StatusUnauthorized, code: httpx.CodeUnauthenticated}
	setupReq   = outcome{status: http.StatusForbidden, code: httpx.CodeSetupRequired}
	reauthReq  = outcome{status: http.StatusUnauthorized, code: httpx.CodeReauthRequired}
	forbidden  = outcome{status: http.StatusForbidden, code: httpx.CodeForbidden}
	cityInPath = cityA // город пути у операций с {cityId}: «свой» у moderator-own
)

// expected — ответ конвейера операции op субъекту s по x-permission.
func expected(t *testing.T, op *openapi3.Operation, path string, s subject) outcome {
	t.Helper()
	kind, _ := op.Extensions["x-permission"].(string)
	if kind == perm.Anonymous {
		return pass
	}
	if s.staff == nil {
		return unauth
	}
	if kind == perm.Session {
		return pass
	}
	switch {
	case s.staff.Stage == auth.StageSetup:
		return setupReq
	case s.staff.Stage != auth.StageActive:
		return unauth
	case s.staff.Reauth:
		return reauthReq
	}
	role := perm.Role(s.staff.Role)
	switch kind {
	case perm.Self:
		return pass
	case perm.Handler:
		want, ok := handlerRoles[opID(op)]
		if !ok {
			t.Fatalf("%s: handler без строки в ручной таблице handlerRoles", opID(op))
		}
		if slices.Contains(want, role) {
			return pass
		}
		return forbidden
	}
	g := perm.Matrix[kind][role]
	if strings.Contains(path, "{cityId}") {
		// право в городе пути: scoped — только свой город
		if g == auth.GrantDeny || g == auth.GrantScoped && !slices.Contains(s.staff.CityIDs, cityInPath) {
			return forbidden
		}
		return pass
	}
	if g == auth.GrantDeny {
		return forbidden
	}
	return pass
}

func TestPermissionsAllOperations(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	subjects := permissionSubjects()
	sessions := map[string]*auth.Staff{}
	for _, s := range subjects {
		if s.staff != nil {
			sessions[s.token] = s.staff
		}
	}
	pools := dbtest.NewPoolsAs(t, "admin")
	o := testOptions(t)
	o.DB = pools.As // журнал читается под ролью admin
	o.Sessions = &fakeSessions{staff: sessions}
	h := newHandler(t, o)

	seen := map[string]bool{}
	checked := 0
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			req, ok := opsBase[opID(op)]
			if !ok {
				t.Errorf("%s %s (%s): нет строки в тесте прав", method, path, opID(op))
				continue
			}
			seen[opID(op)] = true
			if req.method != method || !pathMatches(path, req.path) {
				t.Errorf("%s: строка теста %s %s, контракт %s %s", opID(op), req.method, req.path, method, path)
			}
			passStatus := http.StatusNotImplemented
			if opID(op) == "listAudit" {
				passStatus = http.StatusOK
			}
			for _, s := range subjects {
				want := expected(t, op, path, s)
				rec := serve(h, call(req.method, req.path, s.token, req.body))
				checked++
				if want.pass {
					if rec.Code != passStatus {
						t.Errorf("%s × %s: %d %s — ждали проход до хендлера (%d)", opID(op), s.name, rec.Code, rec.Body.String(), passStatus)
					}
					continue
				}
				if rec.Code != want.status || !strings.Contains(rec.Body.String(), `"code":"`+want.code+`"`) {
					t.Errorf("%s × %s: %d %s — ждали %d %s", opID(op), s.name, rec.Code, rec.Body.String(), want.status, want.code)
				}
			}
		}
	}
	for id := range opsBase {
		if !seen[id] {
			t.Errorf("строка теста %s — нет такой операции в контракте", id)
		}
	}
	if checked < len(opsBase)*len(subjects) || len(opsBase) == 0 {
		t.Fatalf("проверено %d — страж вхолостую", checked)
	}
}

// opID — operationId контракта (oapi-codegen вшивает его с заглавной буквы — имя метода; таблицы теста —
// как в контракте, lowerCamel).
func opID(op *openapi3.Operation) string {
	id := op.OperationID
	if id == "" {
		return id
	}
	return strings.ToLower(id[:1]) + id[1:]
}

// pathMatches — строка теста ведёт на путь контракта: параметры пути — любой непустой сегмент, query
// не учитывается.
func pathMatches(pattern, path string) bool {
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(pattern, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, "{") {
			parts[i] = `[^/]+`
		} else {
			parts[i] = regexp.QuoteMeta(p)
		}
	}
	return regexp.MustCompile("^" + strings.Join(parts, "/") + "$").MatchString(path)
}

// Ручная таблица handler сверена с матрицей: роли, у которых хоть одно право из x-permission-any не Deny, —
// ровно роли таблицы; лишняя строка таблицы (операция не handler) — падение.
func TestHandlerRolesMatchMatrix(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	handlers := 0
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			if op.Extensions["x-permission"] != perm.Handler {
				if _, ok := handlerRoles[opID(op)]; ok {
					t.Errorf("%s: в handlerRoles, но x-permission %v", opID(op), op.Extensions["x-permission"])
				}
				continue
			}
			handlers++
			b, err := json.Marshal(op.Extensions["x-permission-any"])
			if err != nil {
				t.Fatal(err)
			}
			var anyOf []string
			if err := json.Unmarshal(b, &anyOf); err != nil {
				t.Fatal(err)
			}
			var derived []perm.Role
			for _, r := range perm.Roles {
				if slices.ContainsFunc(anyOf, func(k string) bool { return perm.Matrix[k][r] != auth.GrantDeny }) {
					derived = append(derived, r)
				}
			}
			if !slices.Equal(derived, handlerRoles[opID(op)]) {
				t.Errorf("%s: по матрице %v, в ручной таблице %v", opID(op), derived, handlerRoles[opID(op)])
			}
		}
	}
	if handlers != len(handlerRoles) {
		t.Fatalf("операций handler %d, строк таблицы %d", handlers, len(handlerRoles))
	}
}
