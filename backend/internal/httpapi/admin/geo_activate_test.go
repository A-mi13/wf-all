package admin_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
)

// activateGeoCity (Task 10) за полным конвейером: 200 с карточкой по контракту; повтор с тем же Idempotency-Key —
// тот же ответ без второго города; модератор и поддержка — 403 конвейера; ошибки сценария — коды контракта.

// seedPlace — импорт RU и место 526815 «Михайловск» (admin1 71) с названиями ru/en.
func (e geoEnv) seedPlace(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	imp := uuid.New()
	for _, q := range []string{
		`INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at) VALUES ('` + imp.String() + `', 'RU', 'succeeded', now(), now())`,
		`INSERT INTO geonames_admin1 (country_code, admin1_code, geoname_id, ascii_name) VALUES ('RU', '71', 500071, 'Sverdlovsk Oblast')`,
		`INSERT INTO geonames_admin1_names (country_code, admin1_code, locale, name) VALUES ('RU', '71', 'ru', 'Свердловская область')`,
		`INSERT INTO geonames_places (geoname_id, country_code, admin1_code, name, ascii_name, feature_code, population,
		     location, timezone, import_id)
		 VALUES (526815, 'RU', '71', 'Mikhaylovsk', 'Mikhaylovsk', 'PPL', 9000,
		     ST_SetSRID(ST_MakePoint(59.4, 56.4), 4326)::geography, 'Asia/Yekaterinburg', '` + imp.String() + `')`,
		`INSERT INTO geonames_place_names (geoname_id, locale, name) VALUES (526815, 'ru', 'Михайловск'), (526815, 'en', 'Mikhaylovsk')`,
	} {
		if _, err := e.owner.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func (e geoEnv) post(t *testing.T, token, path, key, body, lang string) *httpRec {
	t.Helper()
	r := call(http.MethodPost, path, token, body)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if lang != "" {
		r.Header.Set("Accept-Language", lang)
	}
	return e.v.Do(t, e.h, r)
}

func activator() *auth.Staff {
	s := staffOf(perm.Admin, auth.StageActive, false)
	s.Label = "Ольга В."
	return s
}

func TestActivateGeoCityHTTP(t *testing.T) {
	mod := staffOf(perm.Moderator, auth.StageActive, false)
	e := newGeoEnv(t, map[string]*auth.Staff{"admin": activator(), "mod": mod})
	mod.CityIDs = []uuid.UUID{e.stav}
	e.seedPlace(t)
	// регион не из страны — до активации (на занятом месте ответ — city_already_active раньше проверки региона)
	wantProblem(t, e.post(t, "admin", "/v1/geo/cities", "", `{"geoname_id":526815,"region_id":"`+uuid.NewString()+`"}`, ""),
		http.StatusUnprocessableEntity, geo.CodeRegionNotFound)
	key := uuid.NewString()
	rec := e.post(t, "admin", "/v1/geo/cities", key, `{"geoname_id":526815,"status":"pilot"}`, "en")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Language") != "en" || rec.Header().Get("Vary") != "Accept-Language" {
		t.Fatalf("активация: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	c := decode[oapi.GeoCityDetails](t, rec)
	if c.Slug != "mikhaylovsk" || c.Status != "pilot" || c.Name != "Mikhaylovsk" || c.Region == nil || c.Version != 1 ||
		c.GeonameId == nil || *c.GeonameId != 526815 || c.Names["ru"] != "Михайловск" {
		t.Fatalf("карточка: %+v", c)
	}
	// повтор с тем же ключом — тот же ответ, второго города нет. Мимо apitest: повтор идемпотентности хранит только
	// Content-Type, Location, ETag (idempotency.savedHeaders) — Vary и Content-Language контракта в нём теряются
	// (дефект платформы, в отчёте Task 10)
	r := call(http.MethodPost, "/v1/geo/cities", "admin", `{"geoname_id":526815,"status":"pilot"}`)
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("Accept-Language", "en")
	again := serve(e.h, r)
	if c2 := decode[oapi.GeoCityDetails](t, again); again.Code != http.StatusOK || c2.Id != c.Id || c2.Slug != c.Slug ||
		again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("повтор: %d %s", again.Code, again.Body.String())
	}
	var n int
	if err := e.owner.QueryRow(context.Background(), `SELECT count(*) FROM cities WHERE geoname_id = 526815`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("городов %d %v", n, err)
	}
	// новый ключ — 409 geo.city_already_active
	wantProblem(t, e.post(t, "admin", "/v1/geo/cities", "", `{"geoname_id":526815}`, ""), http.StatusConflict, geo.CodeCityAlreadyActive)
	wantProblem(t, e.post(t, "admin", "/v1/geo/cities", "", `{"geoname_id":1}`, ""), http.StatusNotFound, geo.CodePlaceNotFound)
	wantProblem(t, e.post(t, "admin", "/v1/geo/cities", "", `{"geoname_id":526815,"country_code":"ZZ"}`, ""),
		http.StatusUnprocessableEntity, geo.CodeCountryNotEnabled)
	// права: модератор и поддержка — 403 конвейера
	for _, tok := range []string{"mod", "support"} {
		rec := serve(e.h, call(http.MethodPost, "/v1/geo/cities", tok, `{"geoname_id":526815}`))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"auth.forbidden"`) {
			t.Fatalf("%s: %d %s", tok, rec.Code, rec.Body.String())
		}
	}
}
