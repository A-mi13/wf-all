package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
)

// Чтения админки geo плана B (Task 9) за полным конвейером на настоящих хендлерах: поля и фильтры
// listGeoCities (⚠️ m1 ревью G4 — до Task 9 country_enabled был false у всех, фильтры игнорировались), карточка,
// районы, страны, регионы, места, журнал импортов, коды ошибок, заголовки и маршруты. Ответы сверяет с
// контрактом apitest. Подробные сценарии чтений — internal/geo/internal/app (admin_read_test.go).

// get — GET с токеном и Accept-Language (пустой — без заголовка), ответ сверен с контрактом.
func (e geoEnv) get(t *testing.T, token, path, lang string) *httpRec {
	t.Helper()
	r := call(http.MethodGet, path, token, "")
	if lang != "" {
		r.Header.Set("Accept-Language", lang)
	}
	return e.v.Do(t, e.h, r)
}

func decode[T any](t *testing.T, rec *httpRec) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("тело %s: %v", rec.Body.String(), err)
	}
	return v
}

// seedDisabled — выключенная страна XA с городом skrytsk (pilot) и районом Ставрополя, архивным и действующим.
func (e geoEnv) seedDisabled(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var sk uuid.UUID
	for _, q := range []string{
		`INSERT INTO countries (code, name, currency, default_locale, phone_prefix, week_starts_on, min_signup_age,
		                        age_of_majority, is_enabled) VALUES ('XA', 'Тестовия', 'XXX', 'ru', '+999', 1, 14, 18, false)`,
		`INSERT INTO districts (city_id, name) VALUES ('` + e.stav.String() + `', 'Промышленный')`,
		`INSERT INTO districts (city_id, name, archived_at) VALUES ('` + e.stav.String() + `', 'Архивный', now())`,
	} {
		if _, err := e.owner.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := e.owner.QueryRow(ctx, `INSERT INTO cities (country_id, name, slug, timezone, status, centroid, population)
		SELECT id, 'Скрытск', 'skrytsk', 'Europe/Moscow', 'pilot', ST_SetSRID(ST_MakePoint(42, 45), 4326)::geography, 5000000
		FROM countries WHERE code = 'XA' RETURNING id`).Scan(&sk); err != nil {
		t.Fatal(err)
	}
	return sk
}

// listGeoCities: новые поля, фильтры status (повтор параметра), country, region_id; выключенная страна видна.
func TestAdminCitiesFieldsAndFilters(t *testing.T) {
	e := newGeoEnv(t, nil)
	sk := e.seedDisabled(t)
	rec, ids, _ := e.cities(t, "owner", url.Values{})
	if rec.Code != http.StatusOK || !slices.Equal(ids, []uuid.UUID{sk, e.stav, e.mih}) {
		t.Fatalf("все: %d %v %s", rec.Code, ids, rec.Body.String())
	}
	pg := decode[oapi.GeoCityPage](t, rec)
	byID := map[uuid.UUID]oapi.GeoCity{}
	for _, c := range pg.Items {
		byID[c.Id] = c
	}
	st, s := byID[e.stav], byID[sk]
	if !st.CountryEnabled || st.DistrictsCount != 1 || st.GeonameId == nil || *st.GeonameId != 487846 ||
		st.RegionId == nil || st.Population != 433931 {
		t.Fatalf("Ставрополь: %+v", st)
	}
	if s.CountryEnabled || s.CountryCode != "XA" || s.RegionId != nil || s.RegionName != nil || s.GeonameId != nil {
		t.Fatalf("Скрытск: %+v", s)
	}
	cases := []struct {
		name string
		q    url.Values
		want []uuid.UUID
	}{
		{"status повтором", url.Values{"status": {"live", "waitlist"}}, []uuid.UUID{}},
		{"status pilot", url.Values{"status": {"pilot"}}, []uuid.UUID{sk, e.stav, e.mih}},
		{"country XA", url.Values{"country": {"XA"}}, []uuid.UUID{sk}},
		{"country RU", url.Values{"country": {"RU"}}, []uuid.UUID{e.stav, e.mih}},
		{"region_id", url.Values{"region_id": {st.RegionId.String()}}, []uuid.UUID{e.stav, e.mih}},
		{"region_id чужой", url.Values{"region_id": {uuid.NewString()}}, []uuid.UUID{}},
		{"ids города выключенной страны", idsQuery(sk), []uuid.UUID{sk}},
	}
	for _, c := range cases {
		rec, got, _ := e.cities(t, "owner", c.q)
		if rec.Code != http.StatusOK || !slices.Equal(got, c.want) {
			t.Errorf("%s: %d %v, ждали %v (%s)", c.name, rec.Code, got, c.want, rec.Body.String())
		}
	}
}

// Модератор Ставрополя: список — только его город (ids чужого — пусто), карточка и районы чужого — 403
// конвейера, своего — 200; регионы и страны — можно (R9).
func TestAdminCitiesModeratorScope(t *testing.T) {
	mod := staffOf(perm.Moderator, auth.StageActive, false)
	noCities := staffOf(perm.Moderator, auth.StageActive, false)
	e := newGeoEnv(t, map[string]*auth.Staff{"mod": mod, "no-cities": noCities})
	mod.CityIDs = []uuid.UUID{e.stav}
	sk := e.seedDisabled(t)
	for _, c := range []struct {
		name, token string
		q           url.Values
		want        []uuid.UUID
	}{
		{"свой", "mod", url.Values{}, []uuid.UUID{e.stav}},
		{"ids чужого", "mod", idsQuery(e.mih), []uuid.UUID{}},
		{"ids свой и чужой", "mod", idsQuery(e.mih, e.stav, sk), []uuid.UUID{e.stav}},
		{"фильтр статуса внутри своих", "mod", url.Values{"status": {"live"}}, []uuid.UUID{}},
		{"без городов", "no-cities", url.Values{}, []uuid.UUID{}},
	} {
		rec, got, _ := e.cities(t, c.token, c.q)
		if rec.Code != http.StatusOK || !slices.Equal(got, c.want) {
			t.Errorf("%s: %d %v, ждали %v", c.name, rec.Code, got, c.want)
		}
	}
	if rec := e.get(t, "mod", "/v1/geo/cities/"+e.stav.String(), ""); rec.Code != http.StatusOK {
		t.Fatalf("своя карточка: %d %s", rec.Code, rec.Body.String())
	}
	wantProblem(t, e.get(t, "mod", "/v1/geo/cities/"+e.mih.String(), ""), http.StatusForbidden, "auth.forbidden")
	wantProblem(t, e.get(t, "mod", "/v1/geo/cities/"+e.mih.String()+"/districts", ""), http.StatusForbidden, "auth.forbidden")
	if rec := e.get(t, "mod", "/v1/geo/countries/RU/regions", ""); rec.Code != http.StatusOK {
		t.Fatalf("регионы модератору: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.get(t, "mod", "/v1/geo/countries", ""); rec.Code != http.StatusOK {
		t.Fatalf("страны модератору: %d %s", rec.Code, rec.Body.String())
	}
}

// getGeoCity: карточка выключенной страны, названия, заголовки; нет города — 404 geo.city_not_found.
func TestGetGeoCityHTTP(t *testing.T) {
	e := newGeoEnv(t, nil)
	sk := e.seedDisabled(t)
	rec := e.get(t, "owner", "/v1/geo/cities/"+e.stav.String(), "en")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" ||
		rec.Header().Get("Vary") != "Accept-Language" || rec.Header().Get("Content-Language") != "en" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	c := decode[oapi.GeoCityDetails](t, rec)
	if c.Name != "Stavropol" || c.Names["ru"] != "Ставрополь" || c.CanonicalLocale != "ru" || c.DistrictsCount != 1 ||
		!c.Country.IsEnabled || c.Region == nil || c.Version != 1 || c.SlugHistory == nil {
		t.Fatalf("карточка: %+v", c)
	}
	rec = e.get(t, "owner", "/v1/geo/cities/"+sk.String(), "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Language") != "" {
		t.Fatalf("выключенная страна: %d %s", rec.Code, rec.Body.String())
	}
	if c := decode[oapi.GeoCityDetails](t, rec); c.Country.IsEnabled || c.Country.Code != "XA" || len(c.Names) != 0 {
		t.Fatalf("Скрытск: %+v", c)
	}
	wantProblem(t, e.get(t, "owner", "/v1/geo/cities/"+uuid.NewString(), ""), http.StatusNotFound, geo.CodeCityNotFound)
}

// Районы, страны, регионы, места, журнал импортов — 200 по контракту; ошибки — коды контракта.
func TestGeoAdminReadsHTTP(t *testing.T) {
	e := newGeoEnv(t, nil)
	e.seedDisabled(t)
	rec := e.get(t, "owner", "/v1/geo/cities/"+e.stav.String()+"/districts", "")
	if ds := decode[oapi.GeoDistrictList](t, rec); rec.Code != http.StatusOK || len(ds.Items) != 2 ||
		ds.Items[0].ArchivedAt != nil || ds.Items[1].ArchivedAt == nil || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("районы: %d %s", rec.Code, rec.Body.String())
	}
	wantProblem(t, e.get(t, "owner", "/v1/geo/cities/"+uuid.NewString()+"/districts", ""), http.StatusNotFound, geo.CodeCityNotFound)

	rec = e.get(t, "owner", "/v1/geo/countries", "en")
	cs := decode[oapi.GeoCountryList](t, rec)
	if rec.Code != http.StatusOK || len(cs.Items) != 2 || cs.Items[0].Code != "RU" || cs.Items[0].OpenCities != 2 ||
		cs.Items[1].Code != "XA" || cs.Items[1].IsEnabled || cs.Items[1].OpenCities != 1 || rec.Header().Get("Content-Language") != "en" {
		t.Fatalf("страны: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.get(t, "owner", "/v1/geo/countries/RU/regions", "en")
	if rs := decode[oapi.GeoRegionList](t, rec); rec.Code != http.StatusOK || len(rs.Items) != 1 ||
		rs.Items[0].Name != "Stavropol Krai" || rs.Items[0].GeonameAdmin1Code == nil || *rs.Items[0].GeonameAdmin1Code != "70" {
		t.Fatalf("регионы: %d %s", rec.Code, rec.Body.String())
	}
	wantProblem(t, e.get(t, "owner", "/v1/geo/countries/ZZ/regions", ""), http.StatusNotFound, geo.CodeCountryNotFound)

	rec = e.get(t, "owner", "/v1/geo/places?country=RU", "")
	if pg := decode[oapi.GeoPlacePage](t, rec); rec.Code != http.StatusOK || pg.Items == nil || pg.NextCursor != nil {
		t.Fatalf("места: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.get(t, "owner", "/v1/geo/places?q=%21%21%21", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"query.q","code":"minLength"`) {
		t.Fatalf("места q=!!!: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.get(t, "owner", "/v1/geo/places?cursor=not-a-cursor", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"query.cursor"`) {
		t.Fatalf("места курсор: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.get(t, "owner", "/v1/geo/imports?country=RU&job_id=5", "")
	if pg := decode[oapi.GeoImportPage](t, rec); rec.Code != http.StatusOK || pg.Items == nil || len(pg.Items) != 0 ||
		rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("импорты: %d %s", rec.Code, rec.Body.String())
	}
	wantProblem(t, e.get(t, "owner", "/v1/geo/imports/"+uuid.NewString(), ""), http.StatusNotFound, geo.CodeImportNotFound)
}

// Курсор публичного списка городов (другой набор полей) в админке — 400 query.cursor.
func TestAdminCitiesRejectsPublicCursor(t *testing.T) {
	e := newGeoEnv(t, nil)
	pub, err := geo.New(e.owner).ListCities(context.Background(), geo.ListParams{Limit: new(1)})
	if err != nil || pub.Next == "" {
		t.Fatalf("%+v %v", pub, err)
	}
	rec, _, _ := e.cities(t, "owner", url.Values{"cursor": {pub.Next}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"query.cursor"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// Маршруты: /v1/geo/cities/nearest в админке — getGeoCity с cityId=nearest (400 формата uuid), а не чужая
// операция; пути импортов не перехватывают друг друга (jobs/{jobId} — не getGeoImport с importId=jobs).
func TestAdminRoutes(t *testing.T) {
	e := newGeoEnv(t, nil)
	rec := serve(e.h, call(http.MethodGet, "/v1/geo/cities/nearest", "owner", ""))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"path.cityId"`) {
		t.Fatalf("nearest: %d %s", rec.Code, rec.Body.String())
	}
	rec = serve(e.h, call(http.MethodGet, "/v1/geo/imports/jobs", "owner", ""))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"path.importId"`) {
		t.Fatalf("imports/jobs: %d %s", rec.Code, rec.Body.String())
	}
	// getGeoImportJob — своя операция (заглушка до Task 14 — 501, не 404 импорта)
	rec = serve(e.h, call(http.MethodGet, "/v1/geo/imports/jobs/42", "owner", ""))
	if strings.Contains(rec.Body.String(), geo.CodeImportNotFound) || strings.Contains(rec.Body.String(), `"path.importId"`) {
		t.Fatalf("jobs/42 ушёл в getGeoImport: %d %s", rec.Code, rec.Body.String())
	}
	rec = serve(e.h, call(http.MethodGet, "/v1/geo/imports/"+uuid.NewString(), "owner", ""))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), geo.CodeImportNotFound) {
		t.Fatalf("imports/{id}: %d %s", rec.Code, rec.Body.String())
	}
	rec = serve(e.h, call(http.MethodGet, "/v1/geo/countries/RU/regions", "owner", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("regions: %d %s", rec.Code, rec.Body.String())
	}
}
