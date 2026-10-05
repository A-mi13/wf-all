package geo_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/geo"
	"wf/backend/internal/platform/page"
	"wf/backend/internal/platform/testkit/dbtest"
)

// fixture — база после 0019: сид RU (включена), «Ставропольский край» (admin1 70), Ставрополь
// (487846, pilot, 433931) и Михайловск (493702, pilot, 59198) с точками и строками ru/en
// («Russia», «Stavropol Krai», «Stavropol», «Mikhaylovsk»). Данные теста пишет владелец,
// сервис читает под ролью api — с правами из grants.sql, как в проде.
type fixture struct {
	t     *testing.T
	owner *pgxpool.Pool
	svc   geo.Service
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api")
	return fixture{t: t, owner: pools.Owner, svc: geo.New(pools.As)}
}

func (f fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.owner.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f fixture) cityID(slug string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.owner.QueryRow(context.Background(), `SELECT id FROM cities WHERE slug = $1`, slug).Scan(&id); err != nil {
		f.t.Fatalf("город %s: %v", slug, err)
	}
	return id
}

func (f fixture) regionID(name string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.owner.QueryRow(context.Background(), `SELECT id FROM regions WHERE name = $1`, name).Scan(&id); err != nil {
		f.t.Fatalf("регион %s: %v", name, err)
	}
	return id
}

// testCity — город теста; Region nil — без региона; RuName — завести строку city_names ru.
type testCity struct {
	Country, Slug, Name, Status string
	Region                      *uuid.UUID
	Lat, Lon                    float64
	Population                  int64
	RuName                      bool
	GeonameID                   *int64
}

func (f fixture) addCity(c testCity) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	err := f.owner.QueryRow(context.Background(), `
		INSERT INTO cities (country_id, region_id, name, slug, timezone, status, centroid, population, geoname_id)
		SELECT id, $2, $3, $4, 'Europe/Moscow', $5::city_status,
		       ST_SetSRID(ST_MakePoint($7, $6), 4326)::geography, $8, $9
		FROM countries WHERE code = $1
		RETURNING id`,
		c.Country, c.Region, c.Name, c.Slug, c.Status, c.Lat, c.Lon, c.Population, c.GeonameID).Scan(&id)
	if err != nil {
		f.t.Fatalf("город %s: %v", c.Slug, err)
	}
	if c.RuName {
		f.exec(`INSERT INTO city_names (city_id, locale, name) VALUES ($1, 'ru', $2)`, id, c.Name)
	}
	return id
}

func (f fixture) addRegion(country, name, admin1 string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	err := f.owner.QueryRow(context.Background(), `
		INSERT INTO regions (country_id, name, geoname_admin1_code)
		SELECT id, $2, $3 FROM countries WHERE code = $1 RETURNING id`, country, name, admin1).Scan(&id)
	if err != nil {
		f.t.Fatalf("регион %s: %v", name, err)
	}
	f.exec(`INSERT INTO region_names (region_id, locale, name) VALUES ($1, 'ru', $2)`, id, name)
	return id
}

// addHiddenCountry — выключенная страна XA (код из пользовательского диапазона ISO: тестовые
// данные, не настоящая страна и не настоящие пороги).
func (f fixture) addHiddenCountry() {
	f.exec(`INSERT INTO countries (code, name, currency, default_locale, phone_prefix,
	                               week_starts_on, min_signup_age, age_of_majority, is_enabled)
	        VALUES ('XA', 'Тестовия', 'XXX', 'ru', '+999', 1, 14, 18, false)`)
}

// addImport — строка журнала: места источника ссылаются на неё (import_id NOT NULL).
func (f fixture) addImport(country string) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at)
	        VALUES ($1, $2, 'succeeded', now(), now())`, id, country)
	return id
}

type testPlace struct {
	GeonameID       int64
	Country, Admin1 string
	Name            string
	Population      int64
	Lat, Lon        float64
	Names           map[string]string // locale → name
}

func (f fixture) addPlace(importID uuid.UUID, p testPlace) {
	f.t.Helper()
	f.exec(`INSERT INTO geonames_places (geoname_id, country_code, admin1_code, name, ascii_name,
	                                     feature_code, population, location, timezone, import_id)
	        VALUES ($1, $2, NULLIF($3::text, ''), $4, $4, 'PPL', $5, ST_SetSRID(ST_MakePoint($7, $6), 4326)::geography,
	                'Europe/Moscow', $8)`,
		p.GeonameID, p.Country, p.Admin1, p.Name, p.Population, p.Lat, p.Lon, importID)
	for locale, name := range p.Names {
		f.exec(`INSERT INTO geonames_place_names (geoname_id, locale, name) VALUES ($1, $2, $3)`,
			p.GeonameID, locale, name)
	}
}

func slugs(items []geo.City) []string {
	out := make([]string, 0, len(items))
	for _, c := range items {
		out = append(out, c.Slug)
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func near(a, b, tolerance float64) bool { return math.Abs(a-b) <= tolerance }

// Порядок: ранг статуса (live, pilot, waitlist), population по убыванию, id; курсор v2 проходит
// весь список без пропусков и повторов, в том числе через ничью по population.
func TestListCitiesOrderAndCursor(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.addCity(testCity{Country: "RU", Slug: "livsk", Name: "Лайвск", Status: "live", Lat: 50, Lon: 40, Population: 1000, RuName: true})
	f.addCity(testCity{Country: "RU", Slug: "zhdunovo", Name: "Ждуново", Status: "waitlist", Lat: 51, Lon: 40, Population: 1_000_000, RuName: true})
	a := f.addCity(testCity{Country: "RU", Slug: "tihovo", Name: "Тихово", Status: "waitlist", Lat: 52, Lon: 40, Population: 500, RuName: true})
	b := f.addCity(testCity{Country: "RU", Slug: "tihonovo", Name: "Тихоново", Status: "waitlist", Lat: 53, Lon: 40, Population: 500, RuName: true})
	tie := []string{"tihovo", "tihonovo"}
	if bytes.Compare(b[:], a[:]) < 0 { // ничья по population — по id (uuid в PG сравнивается побайтно)
		tie = []string{"tihonovo", "tihovo"}
	}
	want := append([]string{"livsk", "stavropol", "mihaylovsk", "zhdunovo"}, tie...)

	all, err := f.svc.ListCities(ctx, geo.ListParams{Limit: ptr(100)})
	if err != nil {
		t.Fatal(err)
	}
	if got := slugs(all.Items); !slices.Equal(got, want) || all.Next != "" {
		t.Fatalf("одна страница: %q, next %q; ждали %q без next", got, all.Next, want)
	}

	var walked []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatalf("курсор зациклился: %q", walked)
		}
		pg, err := f.svc.ListCities(ctx, geo.ListParams{Limit: ptr(1), Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, slugs(pg.Items)...)
		if pg.Next == "" {
			break
		}
		cursor = pg.Next
	}
	if !slices.Equal(walked, want) {
		t.Fatalf("по страницам: %q, ждали %q", walked, want)
	}

	// лимит по умолчанию — 20 (страница целиком), курсор чужого набора и мусор — ErrBadCursor
	if pg, err := f.svc.ListCities(ctx, geo.ListParams{}); err != nil || len(pg.Items) != len(want) {
		t.Fatalf("без limit: %d %v", len(pg.Items), err)
	}
	foreign := page.EncodeKeyset(page.Keyset{Set: "geo.other", Values: []any{int64(0), int64(0), uuid.New()}},
		page.KindInt64, page.KindInt64, page.KindUUID)
	for _, bad := range []string{foreign, "мусор", page.Encode(page.Cursor{ID: uuid.New()})} {
		if _, err := f.svc.ListCities(ctx, geo.ListParams{Cursor: bad}); !errors.Is(err, page.ErrBadCursor) {
			t.Errorf("курсор %q: %v, ждали page.ErrBadCursor", bad, err)
		}
	}
}

func TestListCitiesFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.addCity(testCity{Country: "RU", Slug: "livsk", Name: "Лайвск", Status: "live", Lat: 50, Lon: 40, Population: 1000, RuName: true})
	f.addCity(testCity{Country: "RU", Slug: "zhdunovo", Name: "Ждуново", Status: "waitlist", Lat: 51, Lon: 40, Population: 10, RuName: true})
	cases := []struct {
		p    geo.ListParams
		want []string
	}{
		{geo.ListParams{Status: []geo.Status{geo.StatusLive}}, []string{"livsk"}},
		{geo.ListParams{Status: []geo.Status{geo.StatusWaitlist, geo.StatusPilot}}, []string{"stavropol", "mihaylovsk", "zhdunovo"}},
		{geo.ListParams{Country: "ru"}, []string{"livsk", "stavropol", "mihaylovsk", "zhdunovo"}},
		{geo.ListParams{Country: "DE"}, []string{}},
		{geo.ListParams{Status: []geo.Status{"unknown"}}, []string{}},
	}
	for _, c := range cases {
		pg, err := f.svc.ListCities(ctx, c.p)
		if err != nil || !slices.Equal(slugs(pg.Items), c.want) {
			t.Errorf("%+v: %q %v, ждали %q", c.p, slugs(pg.Items), err, c.want)
		}
	}
}

// q — префикс на любом языке после normalize_text: регистр, ё, латиница-двойники, пробелы.
func TestListCitiesSearch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.addCity(testCity{Country: "RU", Slug: "orel", Name: "Орёл", Status: "waitlist", Lat: 52.97, Lon: 36.06, Population: 300_000, RuName: true})
	cases := []struct {
		q    string
		want []string
	}{
		{"ставр", []string{"stavropol"}},
		{"СТАВ", []string{"stavropol"}},
		{"CTAB", []string{"stavropol"}},  // латинские C, T, A, B — двойники кириллицы
		{"stavr", []string{"stavropol"}}, // по en-названию
		{"  Ставрополь  ", []string{"stavropol"}},
		{"орел", []string{"orel"}},
		{"Орёл", []string{"orel"}},
		{"рополь", []string{}}, // середина «Ставрополь» — поиск по началу, не по вхождению
	}
	for _, c := range cases {
		pg, err := f.svc.ListCities(ctx, geo.ListParams{Q: c.q})
		if err != nil || !slices.Equal(slugs(pg.Items), c.want) {
			t.Errorf("q=%q: %q %v, ждали %q", c.q, slugs(pg.Items), err, c.want)
		}
	}
	for _, q := range []string{"!!!", " - ", "★"} {
		if _, err := f.svc.ListCities(ctx, geo.ListParams{Q: q}); !errors.Is(err, geo.ErrBadQuery) {
			t.Errorf("q=%q: %v, ждали ErrBadQuery", q, err)
		}
	}
}

// Review Focus 1: тёзки в разных регионах находятся оба; город без строки city_names нужной
// локали (и вовсе без строк) не роняет ответ — название каноническое.
func TestListCitiesNamesakesAndMissingTranslation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sverdlovsk := f.addRegion("RU", "Свердловская область", "71") // только ru-строка
	// координаты и население тёзок — условные тестовые данные
	twin := f.addCity(testCity{Country: "RU", Slug: "mihaylovsk-sverdlovsk", Name: "Михайловск", Status: "waitlist",
		Region: &sverdlovsk, Lat: 56.44, Lon: 59.12, Population: 9000, RuName: true})
	f.addCity(testCity{Country: "RU", Slug: "mihaylovskoe", Name: "Михайловское", Status: "waitlist",
		Lat: 54.0, Lon: 38.0, Population: 100}) // ни одной строки city_names

	pg, err := f.svc.ListCities(ctx, geo.ListParams{Q: "михайловск", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mihaylovsk", "mihaylovsk-sverdlovsk", "mihaylovskoe"}
	if !slices.Equal(slugs(pg.Items), want) {
		t.Fatalf("тёзки: %q, ждали %q", slugs(pg.Items), want)
	}
	type view struct{ name, region, country string }
	got := make([]view, 0, len(pg.Items))
	for _, c := range pg.Items {
		v := view{name: c.Name, country: c.Country.Name}
		if c.Region != nil {
			v.region = c.Region.Name
		}
		got = append(got, v)
	}
	wantViews := []view{
		{"Mikhaylovsk", "Stavropol Krai", "Russia"},
		{"Михайловск", "Свердловская область", "Russia"}, // нет en у города и региона — канонические
		{"Михайловское", "", "Russia"},
	}
	if !slices.Equal(got, wantViews) {
		t.Fatalf("названия en: %+v, ждали %+v", got, wantViews)
	}

	// латиница находит только тот, у кого есть en-строка; без локали — канонические названия
	pg, err = f.svc.ListCities(ctx, geo.ListParams{Q: "Mikhaylovsk"})
	if err != nil || !slices.Equal(slugs(pg.Items), []string{"mihaylovsk"}) || pg.Items[0].Name != "Михайловск" ||
		pg.Items[0].Region == nil || pg.Items[0].Region.Name != "Ставропольский край" {
		t.Fatalf("q=Mikhaylovsk без локали: %+v %v", pg.Items, err)
	}

	d, err := f.svc.City(ctx, twin, "en")
	if err != nil || d.Name != "Михайловск" || d.Region == nil || d.Region.Name != "Свердловская область" || d.Country.Name != "Russia" {
		t.Fatalf("город без en-строки: %+v %v", d, err)
	}
}

func TestCityDetails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	stav := f.cityID("stavropol")
	f.exec(`INSERT INTO districts (city_id, name) VALUES ($1, 'Юго-Запад'), ($1, 'Центр')`, stav)
	f.exec(`INSERT INTO districts (city_id, name, archived_at) VALUES ($1, 'Старый', now())`, stav)

	d, err := f.svc.City(ctx, stav, "en")
	if err != nil {
		t.Fatal(err)
	}
	region := f.regionID("Ставропольский край")
	if d.ID != stav || d.Slug != "stavropol" || d.Name != "Stavropol" || d.Status != geo.StatusPilot ||
		d.Timezone != "Europe/Moscow" || !near(d.Location.Lat, 45.03442, 1e-9) || !near(d.Location.Lon, 41.9642, 1e-9) ||
		d.Region == nil || *d.Region != (geo.Ref{ID: region, Name: "Stavropol Krai"}) ||
		d.Country != (geo.Country{Code: "RU", Name: "Russia"}) {
		t.Fatalf("город en: %+v", d.City)
	}
	if d.Currency != "RUB" || d.DefaultLocale != "ru" || d.PhonePrefix != "+7" || d.WeekStartsOn != 1 ||
		d.MinSignupAge != 14 || d.AgeOfMajority != 18 {
		t.Fatalf("правила страны: %+v", d)
	}
	var names []string
	for _, x := range d.Districts {
		names = append(names, x.Name)
	}
	if !slices.Equal(names, []string{"Центр", "Юго-Запад"}) {
		t.Fatalf("районы: %q, ждали без архивного", names)
	}

	// нет локали и неподдерживаемая локаль — канонические названия (язык страны)
	for _, locale := range []string{"", "de"} {
		d, err := f.svc.City(ctx, stav, locale)
		if err != nil || d.Name != "Ставрополь" || d.Region == nil || d.Region.Name != "Ставропольский край" || d.Country.Name != "Россия" {
			t.Fatalf("локаль %q: %+v %v", locale, d.City, err)
		}
	}
	if _, err := f.svc.City(ctx, uuid.New(), "ru"); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("нет города: %v", err)
	}
}

func TestCityBySlug(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	stav := f.cityID("stavropol")
	f.exec(`INSERT INTO city_slug_history (slug, city_id, replaced_at) VALUES ('stavropol-old', $1, now())`, stav)
	for _, slug := range []string{"stavropol", "stavropol-old"} {
		d, err := f.svc.CityBySlug(ctx, slug, "ru")
		if err != nil || d.ID != stav || d.Slug != "stavropol" {
			t.Fatalf("slug %s: %+v %v — ответ с текущим slug", slug, d.City, err)
		}
	}
	// slug из истории Ставрополя теперь текущий у Михайловска: текущий slug важнее истории
	f.exec(`INSERT INTO city_slug_history (slug, city_id, replaced_at) VALUES ('mihaylovsk', $1, now())`, stav)
	if d, err := f.svc.CityBySlug(ctx, "mihaylovsk", "ru"); err != nil || d.ID != f.cityID("mihaylovsk") {
		t.Fatalf("slug mihaylovsk: %+v %v — ждали Михайловск (текущий slug), а не Ставрополь из истории", d.City, err)
	}
	if _, err := f.svc.CityBySlug(ctx, "nope", "ru"); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("нет slug: %v", err)
	}
}

// Город выключенной страны не виден нигде публично; CitySettings отдаёт его с country_enabled=false.
func TestDisabledCountryIsInvisible(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.addHiddenCountry()
	// ближе всех к Михайловску и pilot — но страна выключена
	hidden := f.addCity(testCity{Country: "XA", Slug: "skrytsk", Name: "Скрытск", Status: "pilot",
		Lat: 45.131, Lon: 42.027, Population: 5_000_000, RuName: true})

	pg, err := f.svc.ListCities(ctx, geo.ListParams{})
	if err != nil || slices.Contains(slugs(pg.Items), "skrytsk") {
		t.Fatalf("список: %q %v", slugs(pg.Items), err)
	}
	if pg, err := f.svc.ListCities(ctx, geo.ListParams{Country: "XA"}); err != nil || len(pg.Items) != 0 {
		t.Fatalf("фильтр по выключенной стране: %q %v", slugs(pg.Items), err)
	}
	if _, err := f.svc.City(ctx, hidden, "ru"); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("City: %v", err)
	}
	if _, err := f.svc.CityBySlug(ctx, "skrytsk", "ru"); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("CityBySlug: %v", err)
	}
	n, err := f.svc.Nearest(ctx, geo.Point{Lat: 45.131, Lon: 42.027}, "ru")
	if err != nil || n.Open == nil || n.Open.City.Slug != "mihaylovsk" {
		t.Fatalf("nearest_open: %+v %v", n.Open, err)
	}

	s, err := f.svc.CitySettings(ctx, hidden)
	if err != nil || s.CityID != hidden || s.CountryCode != "XA" || s.CountryEnabled || s.Status != geo.StatusPilot ||
		s.Currency != "XXX" || s.Timezone != "Europe/Moscow" {
		t.Fatalf("CitySettings выключенной страны: %+v %v", s, err)
	}
}

func TestCitySettings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	stav := f.cityID("stavropol")
	s, err := f.svc.CitySettings(ctx, stav)
	want := geo.CitySettings{CityID: stav, Timezone: "Europe/Moscow", Status: geo.StatusPilot, CountryCode: "RU",
		CountryEnabled: true, Currency: "RUB", DefaultLocale: "ru", MinSignupAge: 14, AgeOfMajority: 18}
	if err != nil || s != want {
		t.Fatalf("CitySettings: %+v %v, ждали %+v", s, err, want)
	}
	if _, err := f.svc.CitySettings(ctx, uuid.New()); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("нет города: %v", err)
	}
}

// nearest_open — ближайший видимый pilot/live без ограничения расстояния; waitlist не берётся.
func TestNearestOpen(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.addCity(testCity{Country: "RU", Slug: "ryadom", Name: "Рядом", Status: "waitlist",
		Lat: 45.03, Lon: 41.96, Population: 100, RuName: true})
	cases := []struct {
		p        geo.Point
		slug     string
		distance float64
	}{
		{geo.Point{Lat: 45.03442, Lon: 41.9642}, "stavropol", 0},
		{geo.Point{Lat: 45.13097, Lon: 42.02703}, "mihaylovsk", 0},
		// в центре waitlist-«Рядом»: он ближе всех (0 м), но не открыт — Ставрополь в 592 м
		{geo.Point{Lat: 45.03, Lon: 41.96}, "stavropol", 592.3},
	}
	for _, c := range cases {
		n, err := f.svc.Nearest(ctx, c.p, "en")
		if err != nil || n.Open == nil || n.Open.City.Slug != c.slug || !near(n.Open.DistanceM, c.distance, 1) {
			t.Fatalf("%+v: %+v %v, ждали %s", c.p, n.Open, err, c.slug)
		}
	}
	n, err := f.svc.Nearest(ctx, geo.Point{Lat: 45.13097, Lon: 42.02703}, "en")
	if err != nil || n.Open == nil || n.Open.City.Name != "Mikhaylovsk" || n.Open.City.Country.Name != "Russia" {
		t.Fatalf("локаль en: %+v %v", n.Open, err)
	}
	// края диапазона и антимеридиан — ответ без ошибки, ближайший открытый есть всегда
	for _, p := range []geo.Point{{Lat: 90, Lon: 180}, {Lat: -90, Lon: -180}, {Lat: 65, Lon: -179.9}} {
		n, err := f.svc.Nearest(ctx, p, "")
		if err != nil || n.Open == nil || n.Open.DistanceM < 1_000_000 {
			t.Fatalf("%+v: %+v %v", p, n.Open, err)
		}
	}
}

// here — место, накрывающее точку: r(pop) = clamp(2 км + 20 м·√pop, 2 км, 30 км), из накрывающих —
// ближайшее; страна места — по правилу §4.1, невидимая страна — here = nil.
func TestNearestHere(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.addHiddenCountry()
	imp := f.addImport("RU")
	f.exec(`INSERT INTO geonames_admin1 (country_code, admin1_code, geoname_id, ascii_name)
	        VALUES ('RU', '70', 1, 'Stavropol Kray')`)
	f.exec(`INSERT INTO geonames_admin1_names (country_code, admin1_code, locale, name)
	        VALUES ('RU', '70', 'ru', 'Ставропольский Край')`)
	// Ставрополь и Михайловск — места сида (geoname_id городов из 0019): заведены городами
	f.addPlace(imp, testPlace{GeonameID: 487846, Country: "RU", Admin1: "70", Name: "Stavropol", Population: 433_931,
		Lat: 45.03442, Lon: 41.9642, Names: map[string]string{"ru": "Ставрополь", "en": "Stavropol"}})
	f.addPlace(imp, testPlace{GeonameID: 493702, Country: "RU", Admin1: "70", Name: "Mikhaylovsk", Population: 59_198,
		Lat: 45.13097, Lon: 42.02703, Names: map[string]string{"ru": "Михайловск", "en": "Mikhaylovsk"}})
	// остальное — условные тестовые места (id из диапазона 9xxxxx)
	f.addPlace(imp, testPlace{GeonameID: 900001, Country: "RU", Admin1: "70", Name: "Selo", Population: 600,
		Lat: 45.3, Lon: 41.5, Names: map[string]string{"ru": "Село"}}) // r ≈ 2490 м
	f.addPlace(imp, testPlace{GeonameID: 900002, Country: "RU", Name: "Moskva", Population: 12_000_000,
		Lat: 55.7558, Lon: 37.6173}) // r = 30 км
	f.addPlace(imp, testPlace{GeonameID: 900003, Country: "RU", Name: "Chukotka", Population: 10_000,
		Lat: 65, Lon: 179.95}) // r = 4 км, по ту сторону антимеридиана от точки
	f.addPlace(imp, testPlace{GeonameID: 900004, Country: "UA", Name: "Sevastopol", Population: 400_000,
		Lat: 44.6, Lon: 33.52, Names: map[string]string{"ru": "Севастополь"}})
	f.addCity(testCity{Country: "RU", Slug: "sevastopol", Name: "Севастополь", Status: "waitlist",
		Lat: 44.6, Lon: 33.52, Population: 400_000, RuName: true, GeonameID: ptr(int64(900004))})
	f.addPlace(imp, testPlace{GeonameID: 900005, Country: "XA", Name: "Hidden", Population: 100_000, Lat: 10, Lon: 10})
	// место выключенной страны XA заведено городом RU: страна города важнее страны источника (§4.1)
	f.addPlace(imp, testPlace{GeonameID: 900007, Country: "XA", Name: "Pogranichny", Population: 100_000,
		Lat: 30, Lon: 30, Names: map[string]string{"ru": "Пограничный"}})
	f.addCity(testCity{Country: "RU", Slug: "pogranichny", Name: "Пограничный", Status: "waitlist",
		Lat: 30, Lon: 30, Population: 100_000, RuName: true, GeonameID: ptr(int64(900007))})
	f.addPlace(imp, testPlace{GeonameID: 900006, Country: "XB", Name: "Unknown", Population: 100_000, Lat: 20, Lon: 20})

	type want struct {
		geonameID     int64
		name, region  string // region "" — RegionName nil
		country       geo.Country
		citySlug      string // "" — City nil
		distance, tol float64
	}
	cases := []struct {
		label  string
		p      geo.Point
		locale string
		want   *want
	}{
		{"в центре Михайловска — он ближайший из накрывающих (Ставрополь в 11,8 км тоже накрывает)",
			geo.Point{Lat: 45.13097, Lon: 42.02703}, "",
			&want{493702, "Михайловск", "Ставропольский край", geo.Country{Code: "RU", Name: "Россия"}, "mihaylovsk", 0, 1}},
		{"то же на en: названия места, региона города и страны",
			geo.Point{Lat: 45.13097, Lon: 42.02703}, "en",
			&want{493702, "Mikhaylovsk", "Stavropol Krai", geo.Country{Code: "RU", Name: "Russia"}, "mihaylovsk", 0, 1}},
		{"14 км к югу от Ставрополя — внутри r ≈ 15,2 км",
			geo.Point{Lat: 44.90844, Lon: 41.9642}, "ru",
			&want{487846, "Ставрополь", "Ставропольский край", geo.Country{Code: "RU", Name: "Россия"}, "stavropol", 14000, 150}},
		{"17 км к югу от Ставрополя — кандидаты есть, накрывающих нет", geo.Point{Lat: 44.88145, Lon: 41.9642}, "ru", nil},
		{"2 км от села — внутри r ≈ 2,49 км; регион — admin1 источника",
			geo.Point{Lat: 45.318, Lon: 41.5}, "",
			&want{900001, "Село", "Ставропольский Край", geo.Country{Code: "RU", Name: "Россия"}, "", 2000, 50}},
		{"3 км от села — снаружи", geo.Point{Lat: 45.327, Lon: 41.5}, "", nil},
		{"Москва, 29 км — r упирается в 30 км",
			geo.Point{Lat: 56.0166, Lon: 37.6173}, "en",
			&want{900002, "Moskva", "", geo.Country{Code: "RU", Name: "Russia"}, "", 29037, 50}},
		{"Москва, 31 км — за радиусом поиска", geo.Point{Lat: 56.0346, Lon: 37.6173}, "en", nil},
		{"Чукотка: точка по другую сторону антимеридиана, 3,3 км",
			geo.Point{Lat: 65, Lon: -179.98}, "",
			&want{900003, "Chukotka", "", geo.Country{Code: "RU", Name: "Россия"}, "", 3302, 10}},
		{"место UA заведено городом RU — страна города (§4.1)",
			geo.Point{Lat: 44.6, Lon: 33.52}, "",
			&want{900004, "Севастополь", "", geo.Country{Code: "RU", Name: "Россия"}, "sevastopol", 0, 1}},
		{"место XA (выключена) заведено городом RU — страна города, а не источника (§4.1)",
			geo.Point{Lat: 30, Lon: 30}, "",
			&want{900007, "Пограничный", "", geo.Country{Code: "RU", Name: "Россия"}, "pogranichny", 0, 1}},
		{"страна источника выключена", geo.Point{Lat: 10, Lon: 10}, "", nil},
		{"страны источника нет в справочнике", geo.Point{Lat: 20, Lon: 20}, "", nil},
	}
	for _, c := range cases {
		n, err := f.svc.Nearest(ctx, c.p, c.locale)
		if err != nil {
			t.Fatalf("%s: %v", c.label, err)
		}
		h := n.Here
		if c.want == nil {
			if h != nil {
				t.Errorf("%s: here = %+v, ждали nil", c.label, *h)
			}
			continue
		}
		if h == nil {
			t.Errorf("%s: here = nil", c.label)
			continue
		}
		region := ""
		if h.RegionName != nil {
			region = *h.RegionName
		}
		citySlug := ""
		if h.City != nil {
			citySlug = h.City.Slug
		}
		w := c.want
		if h.GeonameID != w.geonameID || h.Name != w.name || region != w.region || h.Country != w.country ||
			citySlug != w.citySlug || !near(h.DistanceM, w.distance, w.tol) {
			t.Errorf("%s: here = {%d %q %q %+v city=%q %.0f м}, ждали %+v",
				c.label, h.GeonameID, h.Name, region, h.Country, citySlug, h.DistanceM, *w)
		}
	}
}

// Спека §10 «смешанные страны»: без согласованной локали (locale "") каждая запись — на языке
// своей страны: место и регион источника XB (default_locale en) — по-английски, RU — по-русски;
// с локалью ru место XB — по-русски. Название источника (p.name) нарочно отличается от en-строки.
func TestMixedCountriesLocale(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// XB — включённая тестовая страна с языком en (код из пользовательского диапазона ISO)
	f.exec(`INSERT INTO countries (code, name, currency, default_locale, phone_prefix,
	                               week_starts_on, min_signup_age, age_of_majority, is_enabled)
	        VALUES ('XB', 'Testland', 'XXX', 'en', '+998', 1, 14, 18, true)`)
	f.exec(`INSERT INTO geonames_admin1 (country_code, admin1_code, geoname_id, ascii_name)
	        VALUES ('XB', '01', 2, 'North Province'), ('RU', '70', 1, 'Stavropol Kray')`)
	f.exec(`INSERT INTO geonames_admin1_names (country_code, admin1_code, locale, name)
	        VALUES ('XB', '01', 'en', 'Northern Province'), ('XB', '01', 'ru', 'Северная провинция'),
	               ('RU', '70', 'ru', 'Ставропольский Край'), ('RU', '70', 'en', 'Stavropol Kray')`)
	f.addPlace(f.addImport("XB"), testPlace{GeonameID: 910001, Country: "XB", Admin1: "01", Name: "Nordstad",
		Population: 50_000, Lat: 10, Lon: 20, Names: map[string]string{"en": "Northtown", "ru": "Нортаун"}})
	f.addPlace(f.addImport("RU"), testPlace{GeonameID: 910002, Country: "RU", Admin1: "70", Name: "Selo",
		Population: 600, Lat: 45.3, Lon: 41.5, Names: map[string]string{"ru": "Село", "en": "Village"}})

	type view struct{ name, region, country string }
	here := func(p geo.Point, locale string) view {
		t.Helper()
		n, err := f.svc.Nearest(ctx, p, locale)
		if err != nil || n.Here == nil || n.Here.RegionName == nil {
			t.Fatalf("%+v %q: here = %+v, %v", p, locale, n.Here, err)
		}
		return view{n.Here.Name, *n.Here.RegionName, n.Here.Country.Name}
	}
	for _, c := range []struct {
		label  string
		p      geo.Point
		locale string
		want   view
	}{
		{"XB без локали — язык страны en", geo.Point{Lat: 10, Lon: 20}, "", view{"Northtown", "Northern Province", "Testland"}},
		{"RU без локали — язык страны ru", geo.Point{Lat: 45.3, Lon: 41.5}, "", view{"Село", "Ставропольский Край", "Россия"}},
		{"XB с локалью ru — ru-строки", geo.Point{Lat: 10, Lon: 20}, "ru", view{"Нортаун", "Северная провинция", "Testland"}},
	} {
		if got := here(c.p, c.locale); got != c.want {
			t.Errorf("%s: %+v, ждали %+v", c.label, got, c.want)
		}
	}
}

// Точка вне диапазона — ошибка, а не паника и не ошибка PostGIS; HTTP-слой отсекает это раньше.
func TestNearestRejectsBadPoint(t *testing.T) {
	f := newFixture(t)
	for _, p := range []geo.Point{
		{Lat: math.NaN(), Lon: 0}, {Lat: 0, Lon: math.Inf(1)}, {Lat: 90.0001, Lon: 0}, {Lat: 0, Lon: -180.0001},
	} {
		if _, err := f.svc.Nearest(context.Background(), p, ""); err == nil {
			t.Errorf("%+v: ждали ошибку", p)
		}
	}
}
