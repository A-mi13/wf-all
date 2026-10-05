package public_test

// Публичные операции geo и min-version на полном конвейере (спека geo §4.2, §10 «Контракт»):
// заголовки, язык, маршрутизация, края координат, коды ошибок, класс лимита, приватность.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/appversion"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/page"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/apitest"
)

var cityPath = "/v1/cities/" + stavropolID.String()

func newPublic(t *testing.T, o public.Options) http.Handler {
	t.Helper()
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func request(target string) *http.Request {
	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("тело не Problem: %s", rec.Body.String())
	}
	return p
}

func bodyOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("тело не JSON-объект: %s", rec.Body.String())
	}
	return m
}

// Каждая операция отвечает по контракту (запрос и ответ сверяет apitest) со своим Cache-Control;
// справочники — с Vary: Accept-Language, min-version — без него.
func TestPublicOperationsMatchContract(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	h := newPublic(t, testOptions(t))
	for _, c := range []struct{ target, cache, vary string }{
		{"/v1/cities", "public, max-age=300", "Accept-Language"},
		{"/v1/cities?status=pilot&status=live&country=RU&q=%D1%81%D1%82%D0%B0%D0%B2&limit=20", "public, max-age=300", "Accept-Language"},
		{cityPath, "public, max-age=300", "Accept-Language"},
		{"/v1/cities/by-slug/stavropol", "public, max-age=300", "Accept-Language"},
		{"/v1/cities/nearest?lat=45.04&lon=41.97", "private, no-store", "Accept-Language"},
		{"/v1/app/min-version?platform=ios", "public, max-age=60", ""},
	} {
		rec := v.Do(t, h, request(c.target))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", c.target, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != c.cache {
			t.Errorf("%s: Cache-Control = %q, want %q", c.target, got, c.cache)
		}
		if got := rec.Header().Get("Vary"); got != c.vary {
			t.Errorf("%s: Vary = %q, want %q", c.target, got, c.vary)
		}
	}
}

// /v1/cities/nearest и /v1/cities/by-slug/{slug} не перехватываются шаблоном /v1/cities/{cityId}
// ни роутером контракта (валидатор, gorillamux), ни chi (strict-хендлер) — спека geo §4.2.
func TestCityRoutesDoNotShadowEachOther(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	o := testOptions(t)
	for _, c := range []struct {
		target, call string
		check        func(*fakeGeo) bool
	}{
		{"/v1/cities", "ListCities", func(*fakeGeo) bool { return true }},
		{"/v1/cities/nearest?lat=45.04&lon=41.97", "Nearest", func(g *fakeGeo) bool { return g.point == geo.Point{Lat: 45.04, Lon: 41.97} }},
		{"/v1/cities/by-slug/stavropol", "CityBySlug", func(g *fakeGeo) bool { return g.slug == "stavropol" }},
		{cityPath, "City", func(g *fakeGeo) bool { return g.id == stavropolID }},
	} {
		g := &fakeGeo{}
		o.Geo = g
		if rec := v.Do(t, newPublic(t, o), request(c.target)); rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", c.target, rec.Code, rec.Body.String())
		}
		if !slices.Equal(g.calls, []string{c.call}) || !c.check(g) {
			t.Fatalf("%s: вызовы %v (%+v), want %s", c.target, g.calls, g, c.call)
		}
	}
}

// Параметры списка доходят до сценария.
func TestListCitiesPassesParams(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	g := &fakeGeo{}
	o := testOptions(t)
	o.Geo = g
	v.Do(t, newPublic(t, o), request("/v1/cities?status=pilot&status=live&country=RU&q=%D1%81%D1%82%D0%B0%D0%B2&cursor=eyJ2IjoyfQ&limit=5"))
	limit := 5
	want := geo.ListParams{Status: []geo.Status{"pilot", "live"}, Country: "RU", Q: "став", Cursor: "eyJ2IjoyfQ", Limit: &limit}
	if !reflect.DeepEqual(g.list, want) {
		t.Fatalf("сценарий получил %+v, want %+v", g.list, want)
	}
}

// Тело списка: город по схеме City; есть курсор — next_cursor строкой, конец — null (поле есть).
func TestListCitiesBody(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	o := testOptions(t)
	for _, c := range []struct {
		next string
		want any
	}{{"eyJ2IjoyfQ", "eyJ2IjoyfQ"}, {"", nil}} {
		o.Geo = &fakeGeo{next: c.next}
		got := bodyOf(t, v.Do(t, newPublic(t, o), request("/v1/cities?limit=1")))
		if next, ok := got["next_cursor"]; !ok || next != c.want {
			t.Fatalf("next_cursor = %v (есть: %v), want %v", next, ok, c.want)
		}
		items, _ := got["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("items = %v", got["items"])
		}
		item := items[0].(map[string]any)
		region := item["region"].(map[string]any)
		location := item["location"].(map[string]any)
		if item["slug"] != "stavropol" || region["name"] != "Ставропольский край" || location["lat"] != 45.03442 {
			t.Fatalf("город: %v", item)
		}
	}
}

// nearest_open и here — объекты или null (поля обязательны); контракт сверяет apitest.
func TestNearestBody(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	o := testOptions(t)
	o.Geo = &fakeGeo{nearest: &geo.Nearest{}}
	got := bodyOf(t, v.Do(t, newPublic(t, o), request("/v1/cities/nearest?lat=0&lon=0")))
	if !reflect.DeepEqual(got, map[string]any{"nearest_open": nil, "here": nil}) {
		t.Fatalf("пустой ответ: %v", got)
	}
	o.Geo = &fakeGeo{}
	got = bodyOf(t, v.Do(t, newPublic(t, o), request("/v1/cities/nearest?lat=45.04&lon=41.97")))
	open, _ := got["nearest_open"].(map[string]any)
	here, _ := got["here"].(map[string]any)
	if open == nil || open["distance_m"] != 1840.5 || here == nil || here["geoname_id"] != float64(487846) || here["city"] == nil {
		t.Fatalf("ответ: %v", got)
	}
}

// Язык (Review Focus 5): q-веса, регион, неподдерживаемый, мусор, очень длинный заголовок,
// несколько строк — всегда 200 и Vary; Content-Language только при выбранном языке; сценарий
// получает тот же язык ("" — названия на языке страны каждой записи).
func TestAcceptLanguage(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	o := testOptions(t)
	targets := []string{"/v1/cities?limit=1", cityPath, "/v1/cities/by-slug/stavropol", "/v1/cities/nearest?lat=45.04&lon=41.97"}
	for _, c := range []struct {
		name   string
		header []string // строки Accept-Language; nil — заголовка нет
		locale string
	}{
		{"нет заголовка", nil, ""},
		{"пустой", []string{""}, ""},
		{"en", []string{"en"}, "en"},
		{"регион отбрасывается", []string{"en-US,en;q=0.9"}, "en"},
		{"q-веса", []string{"fr;q=1, en;q=0.4, ru;q=0.8"}, "ru"},
		{"неподдерживаемый", []string{"fr-FR, de;q=0.8"}, ""},
		{"звёздочка", []string{"*"}, ""},
		{"мусор в q", []string{"en-US;q=abc"}, ""},
		{"длиннее 1 КБ", []string{strings.Repeat("en-US,", 400)}, ""},
		{"несколько строк заголовка", []string{"fr", "en;q=0.5"}, "en"},
	} {
		for i, target := range targets {
			t.Run(fmt.Sprintf("%s/%d", c.name, i), func(t *testing.T) {
				g := &fakeGeo{}
				o.Geo = g
				r := request(target)
				for _, h := range c.header {
					r.Header.Add("Accept-Language", h)
				}
				rec := v.Do(t, newPublic(t, o), r)
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
				}
				if got := rec.Header().Get("Vary"); got != "Accept-Language" {
					t.Fatalf("Vary = %q", got)
				}
				got := rec.Header().Values("Content-Language")
				if (c.locale == "" && len(got) != 0) || (c.locale != "" && !slices.Equal(got, []string{c.locale})) {
					t.Fatalf("Content-Language = %q, want %q", got, c.locale)
				}
				if g.locale != c.locale {
					t.Fatalf("сценарий получил язык %q, want %q", g.locale, c.locale)
				}
			})
		}
	}
}

// Края координат (Review Focus 2): полюса, антимеридиан, Чукотка (lon < 0) — 200, точка доходит до
// сценария как есть; NaN, ±Inf, переполнение, не число, пусто, нет параметра, вне диапазона — 400
// validation.failed с полем, сценарий не вызывается; 500 не бывает.
func TestNearestCoordinateEdges(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	o := testOptions(t)
	for _, c := range []struct {
		query string
		want  geo.Point
	}{
		{"lat=90&lon=180", geo.Point{Lat: 90, Lon: 180}},
		{"lat=-90&lon=-180", geo.Point{Lat: -90, Lon: -180}},
		{"lat=64.73&lon=-179.9", geo.Point{Lat: 64.73, Lon: -179.9}},
		{"lat=0&lon=0", geo.Point{}},
	} {
		g := &fakeGeo{}
		o.Geo = g
		rec := v.Do(t, newPublic(t, o), request("/v1/cities/nearest?"+c.query))
		if rec.Code != http.StatusOK || g.point != c.want {
			t.Fatalf("%s: status = %d, точка %+v, want %+v", c.query, rec.Code, g.point, c.want)
		}
	}
	for _, c := range []struct {
		query string
		field httpx.FieldError
	}{
		{"lat=NaN&lon=0", httpx.FieldError{Field: "query.lat", Code: "type"}},
		{"lat=0&lon=NaN", httpx.FieldError{Field: "query.lon", Code: "type"}},
		{"lat=Inf&lon=0", httpx.FieldError{Field: "query.lat", Code: "type"}},
		{"lat=0&lon=-Inf", httpx.FieldError{Field: "query.lon", Code: "type"}},
		{"lat=1e400&lon=0", httpx.FieldError{Field: "query.lat", Code: "type"}},
		{"lat=abc&lon=0", httpx.FieldError{Field: "query.lat", Code: "type"}},
		{"lat=&lon=0", httpx.FieldError{Field: "query.lat", Code: "empty"}},
		{"lon=0", httpx.FieldError{Field: "query.lat", Code: "required"}},
		{"lat=90.000001&lon=0", httpx.FieldError{Field: "query.lat", Code: "maximum"}},
		{"lat=0&lon=-180.5", httpx.FieldError{Field: "query.lon", Code: "minimum"}},
	} {
		g := &fakeGeo{}
		o.Geo = g
		// запрос нарушает контракт — мимо apitest
		rec := serve(newPublic(t, o), request("/v1/cities/nearest?"+c.query))
		p := problemOf(t, rec)
		if rec.Code != http.StatusBadRequest || p.Code != httpx.CodeValidationFailed ||
			!reflect.DeepEqual(p.Errors, []httpx.FieldError{c.field}) {
			t.Fatalf("%s: status = %d, problem = %+v, want поле %+v", c.query, rec.Code, p, c.field)
		}
		if len(g.calls) != 0 {
			t.Fatalf("%s: сценарий вызван", c.query)
		}
	}
}

// Ошибки сценария — коды контракта: нет города — 404 geo.city_not_found (и по slug), q из одних
// знаков — 400 по query.q, негодный курсор — Problem платформы как есть, сбой — 500 internal.
func TestGeoErrors(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	o := testOptions(t)
	for _, c := range []struct {
		name   string
		err    error
		target string
		status int
		code   string
		fields []httpx.FieldError
	}{
		{"нет города", fmt.Errorf("geo: город: %w", geo.ErrCityNotFound), cityPath, http.StatusNotFound, geo.CodeCityNotFound, nil},
		{"нет города по slug", geo.ErrCityNotFound, "/v1/cities/by-slug/nowhere", http.StatusNotFound, geo.CodeCityNotFound, nil},
		{"q из одних знаков", geo.ErrBadQuery, "/v1/cities?q=%21%21%21", http.StatusBadRequest, httpx.CodeValidationFailed,
			[]httpx.FieldError{{Field: "query.q", Code: "minLength"}}},
		{"негодный курсор", page.ErrBadCursor, "/v1/cities?cursor=xyz", http.StatusBadRequest, httpx.CodeValidationFailed,
			[]httpx.FieldError{{Field: "query.cursor", Code: "format"}}},
		{"сбой хранилища", errors.New("pgx: сломалось"), "/v1/cities/nearest?lat=45.04&lon=41.97", http.StatusInternalServerError, httpx.CodeInternal, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			o.Geo = &fakeGeo{err: c.err}
			rec := v.Do(t, newPublic(t, o), request(c.target))
			p := problemOf(t, rec)
			if rec.Code != c.status || p.Code != c.code || !reflect.DeepEqual(p.Errors, c.fields) {
				t.Fatalf("status = %d, problem = %+v", rec.Code, p)
			}
		})
	}
}

// min-version: незаведённая платформа — 200 с null; заведённая — значения; незнакомая и пропущенная
// платформа — 400 от валидатора без обращения к хранилищу; сбой хранилища — 500.
func TestAppMinVersion(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	o := testOptions(t)

	f := &fakeVersions{}
	o.AppVersions = f
	got := bodyOf(t, v.Do(t, newPublic(t, o), request("/v1/app/min-version?platform=android")))
	want := map[string]any{"platform": "android", "min_version": nil, "recommended_version": nil, "store_url": nil}
	if !reflect.DeepEqual(got, want) || f.asked != appversion.Platform("android") {
		t.Fatalf("незаведённая платформа: %v, спросили %q", got, f.asked)
	}

	o.AppVersions = &fakeVersions{ok: true, v: appversion.Versions{
		Platform: appversion.Platform("ios"), Min: "1.4.0", Recommended: "1.6.2",
		StoreURL: "https://apps.apple.com/app/id6740000000", Version: 3,
		UpdatedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
	}}
	got = bodyOf(t, v.Do(t, newPublic(t, o), request("/v1/app/min-version?platform=ios")))
	want = map[string]any{"platform": "ios", "min_version": "1.4.0", "recommended_version": "1.6.2",
		"store_url": "https://apps.apple.com/app/id6740000000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("заведённая платформа: %v", got)
	}

	for query, field := range map[string]httpx.FieldError{
		"platform=web": {Field: "query.platform", Code: "enum"},
		"":             {Field: "query.platform", Code: "required"},
	} {
		f := &fakeVersions{}
		o.AppVersions = f
		rec := serve(newPublic(t, o), request("/v1/app/min-version?"+query))
		p := problemOf(t, rec)
		if rec.Code != http.StatusBadRequest || p.Code != httpx.CodeValidationFailed ||
			!reflect.DeepEqual(p.Errors, []httpx.FieldError{field}) || f.asked != "" {
			t.Fatalf("%q: status = %d, problem = %+v, спросили %q", query, rec.Code, p, f.asked)
		}
	}

	o.AppVersions = &fakeVersions{err: errors.New("pgx: сломалось")}
	rec := v.Do(t, newPublic(t, o), request("/v1/app/min-version?platform=ios"))
	if rec.Code != http.StatusInternalServerError || problemOf(t, rec).Code != httpx.CodeInternal {
		t.Fatalf("сбой: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// Автоопределение — свой класс лимита geo_nearest (x-rate-limit контракта): исчерпанный
// geo_nearest не трогает справочник (класс default).
func TestNearestHasOwnRateClass(t *testing.T) {
	o := testOptions(t)
	o.Limiter = ratelimit.NewPG(o.DB, clock.System)
	o.RateRules = ratelimit.DefaultRules()
	o.RateRules["geo_nearest"] = ratelimit.ClassPolicy{IP: ratelimit.Policy{Limit: 1, Period: time.Hour}}
	h := newPublic(t, o)
	nearest := func() int { return serve(h, request("/v1/cities/nearest?lat=45.04&lon=41.97")).Code }
	if c := nearest(); c != http.StatusOK {
		t.Fatalf("первый nearest: %d", c)
	}
	if c := nearest(); c != http.StatusTooManyRequests {
		t.Fatalf("второй nearest: %d — ждали 429 по классу geo_nearest", c)
	}
	if c := serve(h, request("/v1/cities?limit=1")).Code; c != http.StatusOK {
		t.Fatalf("список после исчерпанного geo_nearest: %d — классы смешались", c)
	}
}

// Координаты не попадают в лог приложения (спека geo §4.2): лог доступа пишет путь без query,
// ошибки валидации — без значений.
func TestNearestCoordinatesNotLogged(t *testing.T) {
	var logs bytes.Buffer
	h, err := public.NewHandler(slog.New(slog.NewTextHandler(&logs, nil)), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	serve(h, request("/v1/cities/nearest?lat=45.0417&lon=41.9734"))
	serve(h, request("/v1/cities/nearest?lat=NaN&lon=41.9734"))
	if !strings.Contains(logs.String(), "/v1/cities/nearest") {
		t.Fatal("лога доступа нет — страж проверяет вхолостую")
	}
	if strings.Contains(logs.String(), "45.0417") || strings.Contains(logs.String(), "41.9734") {
		t.Fatalf("координаты в логе: %s", logs.String())
	}
}
