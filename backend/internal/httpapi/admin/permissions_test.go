package admin_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Тест прав по всем операциям контракта admin (спека сотрудников §11, Task 10b): каждая операция × субъекты
// «владелец, админ, модератор своего города, модератор чужого, поддержка, без сессии, mfa_pending, setup,
// Reauth». Ожидания для ключей каталога выводятся из perm.Matrix, для handler — ручная таблица, сверенная с
// матрицей. Хендлер staff нулевой (прошедший права запрос отвечает 501 заглушки); журнал, версии, флаги и все
// операции geo — настоящие (liveOps, liveGeo; план B Task 15–16): проход — не 401/403 и не 5xx, а модератор чужого
// города на handler-операции (updateGeoCity) получает 403 от сценария. Отказ конвейера — 401/403 со своим кодом.

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
		// план B (Task 7): тела — по схемам контракта, город пути — cityInPath
		"listGeoPlaces":      {http.MethodGet, "/v1/geo/places?q=%D1%81%D1%82%D0%B0%D0%B2&country=RU", ""},
		"getGeoCity":         {http.MethodGet, "/v1/geo/cities/" + cityInPath.String(), ""},
		"activateGeoCity":    {http.MethodPost, "/v1/geo/cities", `{"geoname_id":526815}`},
		"updateGeoCity":      {http.MethodPatch, "/v1/geo/cities/" + cityInPath.String(), `{"names":{"en":"Stavropol"}}`},
		"linkGeoCity":        {http.MethodPost, "/v1/geo/cities/" + cityInPath.String() + "/link", `{"geoname_id":487846}`},
		"listGeoDistricts":   {http.MethodGet, "/v1/geo/cities/" + cityInPath.String() + "/districts", ""},
		"createGeoDistrict":  {http.MethodPost, "/v1/geo/cities/" + cityInPath.String() + "/districts", `{"name":"Промышленный"}`},
		"updateGeoDistrict":  {http.MethodPatch, "/v1/geo/cities/" + cityInPath.String() + "/districts/" + anyID.String(), `{"archived":true}`},
		"listGeoCountries":   {http.MethodGet, "/v1/geo/countries", ""},
		"createGeoCountry":   {http.MethodPost, "/v1/geo/countries", `{"code":"KZ","currency":"KZT","default_locale":"ru","phone_prefix":"+7","week_starts_on":1,"min_signup_age":14,"age_of_majority":18,"names":{"ru":"Казахстан","en":"Kazakhstan"}}`},
		"updateGeoCountry":   {http.MethodPatch, "/v1/geo/countries/RU", `{"phone_prefix":"+7"}`},
		"listGeoRegions":     {http.MethodGet, "/v1/geo/countries/RU/regions", ""},
		"startGeoImport":     {http.MethodPost, "/v1/geo/imports", `{"country":"RU"}`},
		"listGeoImports":     {http.MethodGet, "/v1/geo/imports?country=RU", ""},
		"getGeoImport":       {http.MethodGet, "/v1/geo/imports/" + anyID.String(), ""},
		"getGeoImportJob":    {http.MethodGet, "/v1/geo/imports/jobs/42", ""},
		"cancelGeoImportJob": {http.MethodPost, "/v1/geo/imports/jobs/42/cancel", ""},
		"listAppVersions":    {http.MethodGet, "/v1/app/versions", ""},
		"putAppVersions":     {http.MethodPut, "/v1/app/versions/ios", `{"min_version":"1.4.0","recommended_version":"1.6.2","store_url":"https://apps.apple.com/app/id6740000000"}`},
		"listFeatureFlags":   {http.MethodGet, "/v1/flags", ""},
		"updateFeatureFlag":  {http.MethodPatch, "/v1/flags/economy", `{"version":1,"enabled_globally":true}`},
		// план I-1 (Task 4): identity — город заявки не в пути (скоуп модератора — сценарий), заголовки ПД — opsHeaders
		"listCaptainApplications":      {http.MethodGet, "/v1/identity/captain-applications?status=pending&limit=20", ""},
		"getCaptainApplication":        {http.MethodGet, "/v1/identity/captain-applications/" + anyID.String(), ""},
		"getCaptainApplicationContact": {http.MethodGet, "/v1/identity/captain-applications/" + anyID.String() + "/contact", ""},
		"approveCaptainApplication":    {http.MethodPost, "/v1/identity/captain-applications/" + anyID.String() + "/approve", `{"comment":"Знаю по турниру"}`},
		"rejectCaptainApplication":     {http.MethodPost, "/v1/identity/captain-applications/" + anyID.String() + "/reject", `{"rejection_code":"insufficient_info"}`},
		"getAuthMethodsUsage":          {http.MethodGet, "/v1/identity/auth-methods/usage", ""},
		"findPlayerByEmail":            {http.MethodGet, "/v1/identity/users/lookup", ""},
		"requestMfaReset":              {http.MethodPost, "/v1/identity/users/" + anyID.String() + "/mfa-reset", `{"reason":"Потерял телефон, личность проверена","ticket":"SUP-4812"}`},
		"cancelMfaReset":               {http.MethodPost, "/v1/identity/mfa-resets/" + anyID.String() + "/cancel", ""},
		"putStaffPlayerLink":           {http.MethodPut, "/v1/staff/accounts/" + anyID.String() + "/player-link", `{"user_id":"` + anyID.String() + `"}`},
		"deleteStaffPlayerLink":        {http.MethodDelete, "/v1/staff/accounts/" + anyID.String() + "/player-link", ""},
	}
	// opsHeaders — заголовки запроса сверх общих (пары имя, значение): ПД и причина чтения — в заголовках, не в query
	// (план I-1, R19); без них валидатор отвечает 400 до прав и хендлера.
	opsHeaders = map[string][]string{
		"getCaptainApplicationContact": {"X-WF-Access-Reason", accessReason},
		"findPlayerByEmail":            {"X-WF-Lookup-Email", "ivan.petrov%40mail.example", "X-WF-Access-Reason", accessReason},
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
		// план B: названия — geo.cities.rename (модератор — scoped, свой город сверяет сценарий, R10)
		"updateGeoCity":    {perm.Owner, perm.Admin, perm.Moderator},
		"updateGeoCountry": {perm.Owner, perm.Admin},
	}
)

// cityRow — ответ конвейера модератору своего города (город пути — cityInPath) и чужого.
type cityRow struct{ own, foreign outcome }

// cityRows — ручная таблица операций с {cityId} в пути (ревью 10b m6): ожидания модератора своего и чужого
// города задаются здесь явно, а не выводятся из expected. Операция с {cityId} без строки здесь уронит
// TestPermissionsAllOperations (cityRowViolations), а путь её строки opsBase обязан вести в cityInPath.
var cityRows = map[string]cityRow{
	"getGeoCity":       {own: pass, foreign: forbidden}, // geo.cities.read — scoped
	"listGeoDistricts": {own: pass, foreign: forbidden}, // geo.cities.read — scoped
	// handler: конвейер город пути не сверяет — чужой город отсекает сценарий (Decide по полю, R10, Review Focus 1):
	// тест идёт на настоящем хендлере geo (Task 16, m6 плана A — раньше свой и чужой были неразличимы)
	"updateGeoCity":     {own: pass, foreign: forbidden},
	"linkGeoCity":       {own: forbidden, foreign: forbidden}, // geo.cities.manage — у модератора deny
	"createGeoDistrict": {own: pass, foreign: forbidden},      // geo.districts.manage — scoped
	"updateGeoDistrict": {own: pass, foreign: forbidden},      // geo.districts.manage — scoped
}

// cityRowViolations — операции контракта с {cityId} без строки rows (или со строкой opsBase не в город
// cityInPath) и строки rows без такой операции.
func cityRowViolations(spec *openapi3.T, rows map[string]cityRow) []string {
	var bad []string
	seen := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		if !strings.Contains(path, "{cityId}") {
			continue
		}
		for method, op := range item.Operations() {
			id := opID(op)
			seen[id] = true
			if _, ok := rows[id]; !ok {
				bad = append(bad, method+" "+path+" ("+id+"): операция с {cityId} без строки cityRows — модератор своего и чужого города неразличимы")
			}
			if req, ok := opsBase[id]; ok && !strings.Contains(req.path, cityInPath.String()) {
				bad = append(bad, id+": путь строки opsBase не ведёт в cityInPath")
			}
		}
	}
	for id := range rows {
		if !seen[id] {
			bad = append(bad, id+": строка cityRows без операции с {cityId} в контракте")
		}
	}
	slices.Sort(bad)
	return bad
}

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

// liveGeo — настоящий хендлер geo на пуле теста: клиент River (импорт) и действия подтверждения geo в своём реестре
// с подменёнными хуками staff (учёток тестовых субъектов в базе нет).
func liveGeo(t *testing.T, pool *pgxpool.Pool, clk clock.Clock) *geoadmin.Handler {
	t.Helper()
	rc, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	gh := geoadmin.New(geo.New(pool), geoadmin.Deps{Pool: pool, Clock: clk, River: rc})
	reg := approvals.NewRegistry()
	gh.RegisterApprovals(reg, approvals.New(pool, clk, reg))
	reg.SetInitiatorCheck(func(context.Context, pgx.Tx, approvals.Approval) error { return nil })
	reg.SetActorLock(func(context.Context, pgx.Tx, approvals.Approval, *auth.Staff) error { return nil })
	if err := reg.Validate(); err != nil {
		t.Fatal(err)
	}
	return gh
}

// liveOps — операции платформы с настоящими хендлерами в сборке теста (Task 15): журнал проверяется выше по 200,
// версии и флаги пишут базу — проход до хендлера по отсутствию отказа конвейера. Операции тега geo — тоже
// настоящие (liveGeo, Task 16): «проход» — не 401 и не 403 конвейера или сценария, исход — забота сценария.
var liveOps = map[string]struct{}{"listAppVersions": {}, "putAppVersions": {}, "listFeatureFlags": {}, "updateFeatureFlag": {}}

func TestPermissionsAllOperations(t *testing.T) {
	t.Parallel()
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
	o.Geo = liveGeo(t, pools.As, o.Clock)
	h := newHandler(t, o)

	for _, v := range cityRowViolations(spec, cityRows) {
		t.Error(v)
	}
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
			_, live := liveOps[opID(op)]
			live = live || slices.Contains(op.Tags, "geo")
			row, cityOp := cityRows[opID(op)]
			for _, s := range subjects {
				want := expected(t, op, path, s)
				// операция с {cityId}: модератор своего и чужого города — по ручной строке cityRows
				if cityOp && s.name == "moderator-own" {
					want = row.own
				} else if cityOp && s.name == "moderator-foreign" {
					want = row.foreign
				}
				rec := serve(h, withHeaders(call(req.method, req.path, s.token, req.body), opsHeaders[opID(op)]))
				checked++
				if want.pass && live {
					// настоящий хендлер платформы: дошёл — не отказ конвейера (401, 403); исход — забота хендлера
					// (повтор правки флага той же version — 409 platform.flag_stale)
					if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden || rec.Code >= 500 {
						t.Errorf("%s × %s: %d %s — ждали проход до хендлера", opID(op), s.name, rec.Code, rec.Body.String())
					}
					continue
				}
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

// Страж cityRows не вакуумен: контракт с операцией {cityId} без строки — нарушение; та же операция со
// строкой — нет; строка без операции — нарушение. На текущем контракте нарушений нет.
func TestCityRowsGuard(t *testing.T) {
	t.Parallel()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	if bad := cityRowViolations(spec, cityRows); len(bad) != 0 {
		t.Fatalf("текущий контракт: %v", bad)
	}
	spec.Paths.Set("/v1/geo/cities/{cityId}/probe", &openapi3.PathItem{Get: &openapi3.Operation{
		OperationID: "probeGeoCity", Extensions: map[string]any{"x-permission": "geo.cities.read"},
	}})
	if bad := cityRowViolations(spec, cityRows); len(bad) != 1 || !strings.Contains(bad[0], "probeGeoCity") {
		t.Fatalf("операция с {cityId} без строки: %v", bad)
	}
	rows := maps.Clone(cityRows)
	rows["probeGeoCity"] = cityRow{own: pass, foreign: forbidden}
	if bad := cityRowViolations(spec, rows); len(bad) != 0 {
		t.Fatalf("со строкой: %v", bad)
	}
	rows["listGeoCities"] = cityRow{own: pass, foreign: pass}
	if bad := cityRowViolations(spec, rows); len(bad) != 1 || !strings.Contains(bad[0], "listGeoCities") {
		t.Fatalf("строка без операции: %v", bad)
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

// accessReason — причина чтения ПД «обращение 4812» в percent-encoding UTF-8 (R19, ревью плана I4).
const accessReason = "%D0%BE%D0%B1%D1%80%D0%B0%D1%89%D0%B5%D0%BD%D0%B8%D0%B5%204812"

// withHeaders — запрос с заголовками из пар имя, значение.
func withHeaders(r *http.Request, hdr []string) *http.Request {
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	return r
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
	t.Parallel()
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
