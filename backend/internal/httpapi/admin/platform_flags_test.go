package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/appversion"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Версии приложения и флаги функций (Task 15; спека §6.1, §3.2) за полным конвейером на настоящих staff и
// подтверждениях; ответы сверяются с контрактом (e.v).

const iosBody = `{"min_version":"1.0.0","recommended_version":"1.2.0","store_url":"https://apps.apple.com/app/id1"}`

func versionsOf(t *testing.T, rec *httptest.ResponseRecorder) map[oapi.AppPlatform]oapi.AppVersions {
	t.Helper()
	wantStatus(t, rec, http.StatusOK)
	var l oapi.AppVersionsList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	out := map[oapi.AppPlatform]oapi.AppVersions{}
	for _, v := range l.Items {
		out[v.Platform] = v
	}
	return out
}

func flagOf(t *testing.T, rec *httptest.ResponseRecorder) oapi.FeatureFlag {
	t.Helper()
	wantStatus(t, rec, http.StatusOK)
	var f oapi.FeatureFlag
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// fieldErrors — поля 400 validation.failed.
func fieldErrors(t *testing.T, rec *httptest.ResponseRecorder) []httpx.FieldError {
	t.Helper()
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || rec.Code != http.StatusBadRequest || p.Code != httpx.CodeValidationFailed {
		t.Fatalf("ждали 400 validation.failed: %d %s", rec.Code, rec.Body.String())
	}
	return p.Errors
}

// listAppVersions — обе платформы всегда (незаведённая — null-поля); putAppVersions — создание (version 1), правка
// (version 2), событие platform.app_versions_changed (агрегат id.Named, версия строки, payload {platform}) и аудит
// сотрудника с before/after, без города; те же значения — 200 без события и аудита; все нарушения Validate сразу —
// 400 с полями body.*; store_url с пробелом — 400.
func TestPutAppVersions(t *testing.T) {
	e := newGeoStaffEnv(t)
	e.v = apitest.New(t, oapi.GetSpec)
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	list := versionsOf(t, e.get("/v1/app/versions", adm.token))
	if len(list) != 2 || list[oapi.Ios].Version != nil || list[oapi.Android].MinVersion != nil {
		t.Fatalf("до заведения: %+v", list)
	}
	rec := e.send(http.MethodPut, "/v1/app/versions/ios", adm.token, uuid.NewString(), iosBody)
	wantStatus(t, rec, http.StatusOK)
	var v oapi.AppVersions
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.Version == nil || *v.Version != 1 || *v.MinVersion != "1.0.0" {
		t.Fatalf("создание: %s", rec.Body.String())
	}
	rec = e.send(http.MethodPut, "/v1/app/versions/ios", adm.token, uuid.NewString(), strings.Replace(iosBody, `"1.0.0"`, `"1.1.0"`, 1))
	if wantStatus(t, rec, http.StatusOK); !strings.Contains(rec.Body.String(), `"version":2`) {
		t.Fatalf("правка: %s", rec.Body.String())
	}
	agg := id.Named("app_versions", "ios")
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = 'platform.app_versions_changed' AND aggregate_type = 'app_versions'
		AND aggregate_id = $1 AND payload = '{"platform":"ios"}'::jsonb AND aggregate_version IN (1, 2)`, agg); n != 2 {
		t.Fatalf("событий %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.app_versions_changed' AND actor_staff_id = $1
		AND object_type = 'app_versions' AND object_id = $2 AND scope_city_id IS NULL
		AND before->>'min_version' = '1.0.0' AND after->>'min_version' = '1.1.0'`, adm.id, agg); n != 1 {
		t.Fatalf("аудит правки: %d", n)
	}
	// те же значения — без события и аудита
	wantStatus(t, e.send(http.MethodPut, "/v1/app/versions/ios", adm.token, uuid.NewString(), strings.Replace(iosBody, `"1.0.0"`, `"1.1.0"`, 1)), http.StatusOK)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.app_versions_changed'`); n != 2 {
		t.Fatalf("аудитов %d — запись без изменений", n)
	}
	// все нарушения сразу: min > recommended и чужой хост стора
	bad := e.send(http.MethodPut, "/v1/app/versions/android", adm.token, uuid.NewString(),
		`{"min_version":"2.0.0","recommended_version":"1.0.0","store_url":"https://apps.apple.com/app/id1"}`)
	got := fieldErrors(t, bad)
	want := []httpx.FieldError{{Field: "body.recommended_version", Code: appversion.CodeMinimum}, {Field: "body.store_url", Code: appversion.CodeHost}}
	if !slices.Equal(got, want) {
		t.Fatalf("поля %v, ждали %v", got, want)
	}
	e.v = nil // пробел в store_url схема пропускает (pattern ^https://), отвергает Validate
	sp := e.send(http.MethodPut, "/v1/app/versions/ios", adm.token, uuid.NewString(),
		`{"min_version":"1.0.0","recommended_version":"1.2.0","store_url":"https://apps.apple.com/app id1"}`)
	if got := fieldErrors(t, sp); !slices.Equal(got, []httpx.FieldError{{Field: "body.store_url", Code: appversion.CodeFormat}}) {
		t.Fatalf("пробел: %v", got)
	}
	if n := e.count(`SELECT count(*) FROM app_versions`); n != 1 {
		t.Fatalf("строк %d", n)
	}
}

// listFeatureFlags — сид 0010: version = 1, городов нет.
func TestListFeatureFlags(t *testing.T) {
	e := newGeoStaffEnv(t)
	e.v = apitest.New(t, oapi.GetSpec)
	sup := e.firstLogin("support@wf.example", string(perm.Support))
	rec := e.get("/v1/flags", sup.token)
	wantStatus(t, rec, http.StatusOK)
	var l oapi.FeatureFlagList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Items) == 0 {
		t.Fatalf("%v %s", err, rec.Body.String())
	}
	for _, f := range l.Items {
		if f.Version != 1 || len(f.EnabledCityIds) != 0 || f.Description == "" {
			t.Fatalf("сид: %+v", f)
		}
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
}

func flagBody(version int64, globally *bool, cities ...uuid.UUID) string {
	b := map[string]any{"version": version}
	if globally != nil {
		b["enabled_globally"] = *globally
	}
	if cities != nil {
		b["enabled_city_ids"] = cities
	}
	raw, _ := json.Marshal(b)
	return string(raw)
}

// Два PATCH с одной version последовательно — второй 409 platform.flag_stale, запись одна; событие
// platform.flag_changed — агрегат id.Named("feature_flag", key), версия = новая version, payload — ключ, флаг, id
// городов; аудит сотрудника с before/after; ничего не меняется — 200 без события, version не растёт.
func TestUpdateFlagStale(t *testing.T) {
	e := newGeoStaffEnv(t)
	e.v = apitest.New(t, oapi.GetSpec)
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	on := true
	f := flagOf(t, e.send(http.MethodPatch, "/v1/flags/ads", adm.token, uuid.NewString(), flagBody(1, &on)))
	if f.Version != 2 || !f.EnabledGlobally || f.Key != "ads" {
		t.Fatalf("%+v", f)
	}
	wantProblem(t, e.send(http.MethodPatch, "/v1/flags/ads", adm.token, uuid.NewString(), flagBody(1, &on)), http.StatusConflict, httpx.CodeFlagStale)
	agg := id.Named("feature_flag", "ads")
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = 'platform.flag_changed' AND aggregate_type = 'feature_flag'
		AND aggregate_id = $1 AND aggregate_version = 2
		AND payload = '{"key":"ads","enabled_globally":true,"enabled_city_ids":[]}'::jsonb`, agg); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.flag_changed' AND actor_staff_id = $1
		AND object_type = 'feature_flag' AND object_id = $2 AND scope_city_id IS NULL
		AND before->>'enabled_globally' = 'false' AND after->>'enabled_globally' = 'true'`, adm.id, agg); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
	// без изменений — 200, версия 2, нового события нет
	if f := flagOf(t, e.send(http.MethodPatch, "/v1/flags/ads", adm.token, uuid.NewString(), flagBody(2, &on))); f.Version != 2 {
		t.Fatalf("без изменений: %+v", f)
	}
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = 'platform.flag_changed'`); n != 1 {
		t.Fatalf("событий %d", n)
	}
}

// Города флага: город выключенной страны допустим; несуществующий — 422 platform.flag_city_not_found без записи;
// повтор id — один в ответе; больше 1000 — 400 схемой.
func TestUpdateFlagCities(t *testing.T) {
	e := newGeoStaffEnv(t)
	e.v = apitest.New(t, oapi.GetSpec)
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	e.exec(`INSERT INTO countries (code, name, currency, default_locale, phone_prefix, week_starts_on, min_signup_age,
	                               age_of_majority, is_enabled) VALUES ('XA', 'Тестовия', 'XXX', 'ru', '+999', 1, 14, 18, false)`)
	var hidden, stav uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO cities (country_id, name, slug, timezone, status, centroid)
		SELECT id, 'Скрытск', 'skrytsk', 'Europe/Moscow', 'waitlist', ST_SetSRID(ST_MakePoint(40, 50), 4326)::geography
		FROM countries WHERE code = 'XA' RETURNING id`).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM cities WHERE slug = 'stavropol'`).Scan(&stav); err != nil {
		t.Fatal(err)
	}
	f := flagOf(t, e.send(http.MethodPatch, "/v1/flags/ads", adm.token, uuid.NewString(), flagBody(1, nil, hidden, stav, hidden)))
	if want := flags.CitySet([]uuid.UUID{hidden, stav}); !slices.Equal(f.EnabledCityIds, want) || f.EnabledGlobally || f.Version != 2 {
		t.Fatalf("%+v, ждали города %v", f, want)
	}
	wantProblem(t, e.send(http.MethodPatch, "/v1/flags/ads", adm.token, uuid.NewString(), flagBody(2, nil, stav, uuid.New())),
		http.StatusUnprocessableEntity, httpx.CodeFlagCityNotFound)
	if n := e.count(`SELECT count(*) FROM feature_flags WHERE key = 'ads' AND version = 2`); n != 1 {
		t.Fatal("записано при несуществующем городе")
	}
	many := make([]uuid.UUID, 1001)
	for i := range many {
		many[i] = stav
	}
	e.v = nil // вне контракта — мимо e.v
	fieldErrors(t, e.send(http.MethodPatch, "/v1/flags/ads", adm.token, uuid.NewString(), flagBody(2, nil, many...)))
}

// PATCH несуществующего ключа — 404 platform.flag_not_found (путь — строка, не enum; R6); ключ не по шаблону — 400
// path.key; без version — 400.
func TestUpdateFlagUnknown(t *testing.T) {
	e := newGeoStaffEnv(t)
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	on := true
	wantProblem(t, e.send(http.MethodPatch, "/v1/flags/no_such_flag", adm.token, uuid.NewString(), flagBody(1, &on)),
		http.StatusNotFound, httpx.CodeFlagNotFound)
	if got := fieldErrors(t, e.send(http.MethodPatch, "/v1/flags/Bad-Key", adm.token, uuid.NewString(), flagBody(1, &on))); len(got) != 1 || got[0].Field != "path.key" {
		t.Fatalf("Bad-Key: %v", got)
	}
	fieldErrors(t, e.send(http.MethodPatch, "/v1/flags/ads", adm.token, uuid.NewString(), `{"enabled_globally":true}`))
}

// R6: ключи feature_flags после миграций = enum FeatureFlagKey контракта (подписи флагов на экране сторожит enum).
func TestFlagKeysMatchContract(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := spec.Components.Schemas["FeatureFlagKey"]
	if !ok || ref.Value == nil {
		t.Fatal("нет схемы FeatureFlagKey")
	}
	var enum []string
	for _, v := range ref.Value.Enum {
		enum = append(enum, fmt.Sprint(v))
	}
	rows, err := dbtest.NewPool(t).Query(context.Background(), `SELECT key FROM feature_flags ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	slices.Sort(enum)
	if !slices.Equal(keys, enum) {
		t.Fatalf("ключи базы %v, enum контракта %v", keys, enum)
	}
}

// Права (Приложение 1): поддержка читает версии и флаги (allow), писать — 403.
func TestPlatformVersionsFlagsPermissions(t *testing.T) {
	e := newGeoStaffEnv(t)
	sup := e.firstLogin("support@wf.example", string(perm.Support))
	wantStatus(t, e.get("/v1/app/versions", sup.token), http.StatusOK)
	wantStatus(t, e.get("/v1/flags", sup.token), http.StatusOK)
	on := true
	wantProblem(t, e.send(http.MethodPut, "/v1/app/versions/ios", sup.token, uuid.NewString(), iosBody), http.StatusForbidden, httpx.CodeForbidden)
	wantProblem(t, e.send(http.MethodPatch, "/v1/flags/ads", sup.token, uuid.NewString(), flagBody(1, &on)), http.StatusForbidden, httpx.CodeForbidden)
}
