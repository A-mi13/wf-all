package app_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/page"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Общий клон чтений админки (seedAdminReads) удаляется после всех тестов пакета.
func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// Чтения админки (Task 9) — общий засеянный клон под ролью admin (dbtest.SharedAs), только чтение. Посев поверх
// сида 0019 (RU включена: Ставропольский край admin1 70, Ставрополь 487846 и Михайловск 493702 — pilot):
//   - RU: livsk (live), zhdunovo (waitlist), регион «Свердловская область» admin1 71 с городом
//     mihaylovsk-sverdlovsk (waitlist); у Ставрополя районы (два действующих, один архивный) и два прежних адреса;
//   - XA — выключенная страна с городом skrytsk (pilot, en-название), заведённым на месте 900001 с источником RU;
//   - места источника RU: 487846, 493702, 526815 (Михайловск Свердловский, admin1 71), 900001;
//   - журнал импортов: строки с отчётом сверки (RU), подписи автора (XL — 25 строк, XR — повтор job_id, XM —
//     сотрудник, оператор, без записи). Коды XA, XL, XM, XR — пользовательский диапазон ISO, тестовые данные.
var adminSeeded struct {
	cities  map[string]uuid.UUID
	regions map[string]uuid.UUID
	report  uuid.UUID // импорт RU с отчётом сверки
	running uuid.UUID // импорт RU без отчёта (идёт)
	xm      map[string]uuid.UUID
	xr      map[string]uuid.UUID
}

var tImp = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func seedAdminReads(ctx context.Context, owner *pgxpool.Pool) error {
	s := aseeder{ctx: ctx, owner: owner}
	adminSeeded.cities, adminSeeded.regions = map[string]uuid.UUID{}, map[string]uuid.UUID{}
	adminSeeded.xm, adminSeeded.xr = map[string]uuid.UUID{}, map[string]uuid.UUID{}
	s.exec(`INSERT INTO countries (code, name, currency, default_locale, phone_prefix,
	                               week_starts_on, min_signup_age, age_of_majority, is_enabled)
	        VALUES ('XA', 'Тестовия', 'XXX', 'ru', '+999', 1, 14, 18, false)`)
	s.exec(`INSERT INTO country_names (country_id, locale, name) SELECT id, 'ru', 'Тестовия' FROM countries WHERE code = 'XA'`)
	s.exec(`INSERT INTO country_names (country_id, locale, name) SELECT id, 'en', 'Testland' FROM countries WHERE code = 'XA'`)
	s.lookup(`SELECT slug, id FROM cities`, adminSeeded.cities)
	s.lookup(`SELECT name, id FROM regions`, adminSeeded.regions)
	var sverd uuid.UUID
	s.scan(`INSERT INTO regions (country_id, name, geoname_admin1_code)
	        SELECT id, 'Свердловская область', '71' FROM countries WHERE code = 'RU' RETURNING id`, nil, &sverd)
	s.exec(`INSERT INTO region_names (region_id, locale, name) VALUES ($1, 'ru', 'Свердловская область')`, sverd)
	adminSeeded.regions["Свердловская область"] = sverd

	s.city("RU", nil, "livsk", "Лайвск", "live", 1000, nil, map[string]string{"ru": "Лайвск"})
	s.city("RU", nil, "zhdunovo", "Ждуново", "waitlist", 1_000_000, nil, nil)
	s.city("RU", &sverd, "mihaylovsk-sverdlovsk", "Михайловск", "waitlist", 9000, nil, map[string]string{"ru": "Михайловск"})
	g := int64(900001)
	s.city("XA", nil, "skrytsk", "Скрытск", "pilot", 5_000_000, &g, map[string]string{"ru": "Скрытск", "en": "Skrytsk"})

	stav := adminSeeded.cities["stavropol"]
	s.exec(`INSERT INTO districts (city_id, name) VALUES ($1, 'Промышленный'), ($1, 'Октябрьский')`, stav)
	s.exec(`INSERT INTO districts (city_id, name, archived_at) VALUES ($1, 'Архивный', $2)`, stav, tImp)
	s.exec(`INSERT INTO city_slug_history (slug, city_id, replaced_at) VALUES ('stavropol-old', $1, $2), ('stavropol-older', $1, $3)`,
		stav, tImp, tImp.Add(-24*time.Hour))

	// источник
	base := uuid.New()
	s.exec(`INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at) VALUES ($1, 'RU', 'succeeded', $2, $2)`,
		base, tImp.Add(-72*time.Hour))
	s.exec(`INSERT INTO geonames_admin1 (country_code, admin1_code, geoname_id, ascii_name)
	        VALUES ('RU', '70', 500070, 'Stavropol Kray'), ('RU', '71', 500071, 'Sverdlovsk Oblast')`)
	s.exec(`INSERT INTO geonames_admin1_names (country_code, admin1_code, locale, name)
	        VALUES ('RU', '70', 'ru', 'Ставропольский край'), ('RU', '71', 'ru', 'Свердловская область'),
	               ('RU', '70', 'en', 'Stavropol Krai'), ('RU', '71', 'en', 'Sverdlovsk Oblast')`)
	s.place(base, 487846, "70", "Stavropol", 433931, map[string]string{"ru": "Ставрополь", "en": "Stavropol"})
	s.place(base, 493702, "70", "Mikhaylovsk", 59198, map[string]string{"ru": "Михайловск", "en": "Mikhaylovsk"})
	s.place(base, 526815, "71", "Mikhaylovsk", 9000, map[string]string{"ru": "Михайловск"})
	s.place(base, 900001, "70", "Skrytsk", 5_000_000, map[string]string{"ru": "Скрытск"})

	// импорт RU с отчётом сверки: кандидат 999999 уже удалён из мест
	adminSeeded.report = uuid.New()
	s.exec(`INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at, places_upserted,
	            places_removed, places_missing, names_upserted, reconciled)
	        VALUES ($1, 'RU', 'succeeded', $2, $3, 4, 1, 1, 9, $4)`, adminSeeded.report, tImp.Add(-48*time.Hour),
		tImp.Add(-47*time.Hour), fmt.Sprintf(`{"linked":[{"city_id":%q,"slug":"stavropol","geoname_id":487846}],
		  "ambiguous":[{"city_id":%q,"slug":"livsk","candidates":[526815,493702,999999]}],
		  "not_found":[{"city_id":%q,"slug":"zhdunovo","candidates":null}],
		  "conflict":[{"city_id":%q,"slug":"mihaylovsk-sverdlovsk","candidates":[526815]}],
		  "skipped_timezone":3,"missing":[111]}`, stav, adminSeeded.cities["livsk"], adminSeeded.cities["zhdunovo"],
			adminSeeded.cities["mihaylovsk-sverdlovsk"]))
	adminSeeded.running = uuid.New()
	s.exec(`INSERT INTO geonames_imports (id, country_code, status, started_at) VALUES ($1, 'RU', 'running', $2)`,
		adminSeeded.running, tImp)

	// XL — 25 строк задач 1001…1025, у каждой запись постановки за минуту до начала
	for i := range 25 {
		job := int64(1001 + i)
		at := tImp.Add(time.Duration(i) * time.Minute)
		s.importRow("XL", job, at)
		s.requested(job, at.Add(-time.Minute), fmt.Sprintf("Сотрудник %d", i))
	}
	// XR — повтор job_id 2001 после сброса последовательности: старая запись → старый импорт, новая → новый
	adminSeeded.xr["old"] = s.importRow("XR", 2001, tImp.Add(-10*time.Hour))
	adminSeeded.xr["new"] = s.importRow("XR", 2001, tImp.Add(-time.Hour))
	s.requested(2001, tImp.Add(-11*time.Hour), "Старая Анна")
	s.requested(2001, tImp.Add(-2*time.Hour), "Новый Пётр")
	// XM — сотрудник, оператор, строка без записи постановки; запись другой задачи с другим действием не мешает
	adminSeeded.xm["staff"] = s.importRow("XM", 3001, tImp.Add(-3*time.Hour))
	s.requested(3001, tImp.Add(-4*time.Hour), "Мария К.")
	adminSeeded.xm["operator"] = uuid.New()
	s.exec(`INSERT INTO geonames_imports (id, country_code, status, started_at) VALUES ($1, 'XM', 'succeeded', $2)`,
		adminSeeded.xm["operator"], tImp.Add(-5*time.Hour))
	adminSeeded.xm["norecord"] = s.importRow("XM", 3002, tImp.Add(-6*time.Hour))
	s.exec(`INSERT INTO audit_log (actor_staff_id, actor_role, actor_label, action, object_type, object_id, created_at)
	        VALUES ($1, 'admin', 'Чужое действие', 'geo.import_cancelled', 'geonames_import_job', $2, $3)`,
		uuid.New(), id.Named("geonames_import_job", "3002"), tImp.Add(-7*time.Hour))
	return s.err
}

type aseeder struct {
	ctx   context.Context
	owner *pgxpool.Pool
	err   error
}

func (s *aseeder) exec(sql string, args ...any) {
	if s.err != nil {
		return
	}
	if _, err := s.owner.Exec(s.ctx, sql, args...); err != nil {
		s.err = fmt.Errorf("%s: %w", sql, err)
	}
}

func (s *aseeder) scan(sql string, args []any, dst ...any) {
	if s.err != nil {
		return
	}
	if err := s.owner.QueryRow(s.ctx, sql, args...).Scan(dst...); err != nil {
		s.err = fmt.Errorf("%s: %w", sql, err)
	}
}

func (s *aseeder) lookup(sql string, m map[string]uuid.UUID) {
	if s.err != nil {
		return
	}
	rows, err := s.owner.Query(s.ctx, sql)
	if err != nil {
		s.err = err
		return
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v uuid.UUID
		if s.err = rows.Scan(&k, &v); s.err != nil {
			return
		}
		m[k] = v
	}
	s.err = rows.Err()
}

func (s *aseeder) city(country string, region *uuid.UUID, slug, name, status string, pop int64, geoname *int64, names map[string]string) {
	var cid uuid.UUID
	s.scan(`INSERT INTO cities (country_id, region_id, name, slug, timezone, status, centroid, population, geoname_id)
	        SELECT id, $2, $3, $4, 'Europe/Moscow', $5::city_status, ST_SetSRID(ST_MakePoint(40, 50), 4326)::geography, $6, $7
	        FROM countries WHERE code = $1 RETURNING id`, []any{country, region, name, slug, status, pop, geoname}, &cid)
	for l, n := range names {
		s.exec(`INSERT INTO city_names (city_id, locale, name) VALUES ($1, $2, $3)`, cid, l, n)
	}
	adminSeeded.cities[slug] = cid
}

func (s *aseeder) place(imp uuid.UUID, gid int64, admin1, ascii string, pop int64, names map[string]string) {
	s.exec(`INSERT INTO geonames_places (geoname_id, country_code, admin1_code, name, ascii_name, feature_code,
	                                     population, location, timezone, import_id)
	        VALUES ($1, 'RU', $2, $3, $3, 'PPL', $4, ST_SetSRID(ST_MakePoint(42, 45), 4326)::geography, 'Europe/Moscow', $5)`,
		gid, admin1, ascii, pop, imp)
	for l, n := range names {
		s.exec(`INSERT INTO geonames_place_names (geoname_id, locale, name) VALUES ($1, $2, $3)`, gid, l, n)
	}
}

func (s *aseeder) importRow(country string, job int64, at time.Time) uuid.UUID {
	rid := uuid.New()
	s.exec(`INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at, started_by, river_job_id)
	        VALUES ($1, $2, 'succeeded', $3, $3, $4, $5)`, rid, country, at, uuid.New(), job)
	return rid
}

// requested — запись аудита постановки (как напишет Task 14) с заданным временем.
func (s *aseeder) requested(job int64, at time.Time, label string) {
	s.exec(`INSERT INTO audit_log (actor_staff_id, actor_role, actor_label, action, object_type, object_id, created_at)
	        VALUES ($1, 'admin', $2, 'geo.import_requested', 'geonames_import_job', $3, $4)`,
		uuid.New(), label, id.Named("geonames_import_job", fmt.Sprint(job)), at)
}

func adminReader(t *testing.T) *app.AdminReader {
	t.Helper()
	return app.NewAdminReader(dbtest.SharedAs(t, "admin", seedAdminReads).As)
}

func cityOf(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	cid, ok := adminSeeded.cities[slug]
	if !ok {
		t.Fatalf("города %s нет в посеве", slug)
	}
	return cid
}

func citySlugs(items []geo.AdminCityItem) []string {
	out := make([]string, 0, len(items))
	for _, c := range items {
		out = append(out, c.Slug)
	}
	return out
}

// Выключенная страна: список, ids и карточка админки её город отдают (country_enabled = false); публичный
// список — нет (регресс ⚠️ плана A: WHERE co.is_enabled прятал город даже по ids).
func TestAdminCitiesIncludeDisabledCountry(t *testing.T) {
	t.Parallel()
	r := adminReader(t)
	ctx := context.Background()
	sk := cityOf(t, "skrytsk")
	pg, err := r.Cities(ctx, geo.AdminCityQuery{Country: "XA"})
	if err != nil || len(pg.Items) != 1 || pg.Items[0].ID != sk || pg.Items[0].CountryEnabled {
		t.Fatalf("список XA: %+v %v", pg, err)
	}
	pg, err = r.Cities(ctx, geo.AdminCityQuery{IDs: []uuid.UUID{sk}})
	if err != nil || len(pg.Items) != 1 {
		t.Fatalf("ids: %+v %v", pg, err)
	}
	card, err := r.City(ctx, sk, "en")
	if err != nil || card.Country.IsEnabled || card.Country.Code != "XA" || card.Country.Name != "Testland" || card.Name != "Skrytsk" {
		t.Fatalf("карточка: %+v %v", card, err)
	}
	pub := geo.New(dbtest.SharedAs(t, "admin", seedAdminReads).As)
	if pp, err := pub.ListCities(ctx, geo.ListParams{IDs: []uuid.UUID{sk}}); err != nil || len(pp.Items) != 0 {
		t.Fatalf("публичный список отдал город выключенной страны: %+v %v", pp, err)
	}
	if _, err := pub.City(ctx, sk, ""); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("публичная карточка: %v", err)
	}
}

// Фильтры списка складываются: статусы, страна, регион, q (ru/en, ё), ids; порядок — как публичный.
func TestAdminCitiesFilters(t *testing.T) {
	t.Parallel()
	r := adminReader(t)
	ctx := context.Background()
	sverd := adminSeeded.regions["Свердловская область"]
	cases := []struct {
		name string
		q    geo.AdminCityQuery
		want []string
	}{
		{"все RU по порядку", geo.AdminCityQuery{Country: "RU"},
			[]string{"livsk", "stavropol", "mihaylovsk", "zhdunovo", "mihaylovsk-sverdlovsk"}},
		{"страна в нижнем регистре", geo.AdminCityQuery{Country: "ru", Status: []geo.Status{geo.StatusLive}}, []string{"livsk"}},
		{"pilot и live всех стран", geo.AdminCityQuery{Status: []geo.Status{geo.StatusPilot, geo.StatusLive}},
			[]string{"livsk", "skrytsk", "stavropol", "mihaylovsk"}},
		{"waitlist", geo.AdminCityQuery{Status: []geo.Status{geo.StatusWaitlist}}, []string{"zhdunovo", "mihaylovsk-sverdlovsk"}},
		{"регион", geo.AdminCityQuery{RegionID: &sverd}, []string{"mihaylovsk-sverdlovsk"}},
		{"регион и статус мимо", geo.AdminCityQuery{RegionID: &sverd, Status: []geo.Status{geo.StatusPilot}}, []string{}},
		{"q ru с ё/е", geo.AdminCityQuery{Q: "МИХАЙЛОВСК"}, []string{"mihaylovsk", "mihaylovsk-sverdlovsk"}},
		{"q en", geo.AdminCityQuery{Q: "skry"}, []string{"skrytsk"}},
		{"q ё", geo.AdminCityQuery{Q: "ждуно"}, []string{"zhdunovo"}},
		{"ids пустой — ни одного", geo.AdminCityQuery{IDs: []uuid.UUID{}}, []string{}},
	}
	for _, c := range cases {
		pg, err := r.Cities(ctx, c.q)
		if err != nil || !slices.Equal(citySlugs(pg.Items), c.want) {
			t.Errorf("%s: %v %v, ждали %v", c.name, citySlugs(pg.Items), err, c.want)
		}
	}
	// поля строки
	pg, _ := r.Cities(ctx, geo.AdminCityQuery{IDs: []uuid.UUID{cityOf(t, "stavropol"), cityOf(t, "mihaylovsk-sverdlovsk")}, Locale: "en"})
	st, ms := pg.Items[0], pg.Items[1]
	if st.DistrictsCount != 2 || st.GeonameID == nil || *st.GeonameID != 487846 || st.Region == nil ||
		st.Region.Name != "Stavropol Krai" || !st.CountryEnabled || st.Population != 433931 {
		t.Errorf("Ставрополь: %+v", st)
	}
	if ms.GeonameID != nil || ms.Region == nil || ms.Region.ID != sverd || ms.Name != "Михайловск" {
		t.Errorf("Михайловск Свердловский (без en — каноническое): %+v", ms)
	}
	// q пустой после нормализации — ErrBadQuery; письменность вне латиницы и кириллицы — тоже
	for _, q := range []string{"!!!", "東京"} {
		if _, err := r.Cities(ctx, geo.AdminCityQuery{Q: q}); !errors.Is(err, geo.ErrBadQuery) {
			t.Errorf("q=%q: %v", q, err)
		}
	}
}

// Курсор: порядок страниц как у полного списка; курсор публичного списка (другой Set) — page.ErrBadCursor.
func TestAdminCitiesCursor(t *testing.T) {
	t.Parallel()
	r := adminReader(t)
	ctx := context.Background()
	all, err := r.Cities(ctx, geo.AdminCityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	cursor := ""
	for range 20 {
		pg, err := r.Cities(ctx, geo.AdminCityQuery{Cursor: cursor, Limit: new(2)})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, citySlugs(pg.Items)...)
		if cursor = pg.Next; cursor == "" {
			break
		}
	}
	if !slices.Equal(got, citySlugs(all.Items)) {
		t.Fatalf("по страницам %v, целиком %v", got, citySlugs(all.Items))
	}
	pubPage, err := geo.New(dbtest.SharedAs(t, "admin", seedAdminReads).As).ListCities(ctx, geo.ListParams{Limit: new(1)})
	if err != nil || pubPage.Next == "" {
		t.Fatalf("публичная страница: %+v %v", pubPage, err)
	}
	if _, err := r.Cities(ctx, geo.AdminCityQuery{Cursor: pubPage.Next}); !errors.Is(err, page.ErrBadCursor) {
		t.Fatalf("курсор публичного списка в админке: %v", err)
	}
	adm, _ := r.Cities(ctx, geo.AdminCityQuery{Limit: new(1)})
	if _, err := geo.New(dbtest.SharedAs(t, "admin", seedAdminReads).As).ListCities(ctx,
		geo.ListParams{Cursor: adm.Next}); !errors.Is(err, page.ErrBadCursor) {
		t.Fatalf("курсор админки в публичном списке: %v", err)
	}
}

// Карточка: названия ru/en, история адресов по убыванию, районы без архивных, версия; нет города — 404.
func TestGetGeoCity(t *testing.T) {
	t.Parallel()
	r := adminReader(t)
	ctx := context.Background()
	c, err := r.City(ctx, cityOf(t, "stavropol"), "en")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Stavropol" || c.Names["ru"] != "Ставрополь" || c.Names["en"] != "Stavropol" || len(c.Names) != 2 ||
		c.CanonicalLocale != "ru" || c.DistrictsCount != 2 || c.Version != 1 || c.Status != geo.StatusPilot ||
		c.Region == nil || c.Region.Name != "Stavropol Krai" || c.Country != (geo.CityCountry{Code: "RU", Name: "Russia", IsEnabled: true}) ||
		c.Timezone == "" || c.GeonameID == nil || *c.GeonameID != 487846 {
		t.Fatalf("карточка: %+v", c)
	}
	if len(c.SlugHistory) != 2 || c.SlugHistory[0].Slug != "stavropol-old" || c.SlugHistory[1].Slug != "stavropol-older" ||
		!c.SlugHistory[0].ReplacedAt.Equal(tImp) {
		t.Fatalf("история: %+v", c.SlugHistory)
	}
	// без языка — канонические названия; регион без перевода — канонический
	z, err := r.City(ctx, cityOf(t, "zhdunovo"), "")
	if err != nil || z.Name != "Ждуново" || z.Region != nil || len(z.Names) != 0 || z.SlugHistory == nil || len(z.SlugHistory) != 0 {
		t.Fatalf("Ждуново: %+v %v", z, err)
	}
	if _, err := r.City(ctx, uuid.New(), "ru"); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("нет города: %v", err)
	}
}

// Места: префикс кириллицей и по ascii, порядок по населению, city_id у заведённого, страна заведённого —
// страна города (900001 из источника RU — город XA), q из одних знаков — ErrBadQuery, курсор.
func TestListGeoPlaces(t *testing.T) {
	t.Parallel()
	r := adminReader(t)
	ctx := context.Background()
	gids := func(pg geo.PlacePage) []int64 {
		out := []int64{}
		for _, p := range pg.Items {
			out = append(out, p.GeonameID)
		}
		return out
	}
	pg, err := r.Places(ctx, geo.PlaceQuery{Q: "михайл"})
	if err != nil || !slices.Equal(gids(pg), []int64{493702, 526815}) {
		t.Fatalf("кириллица: %v %v", gids(pg), err)
	}
	if pg.Items[0].CityID == nil || *pg.Items[0].CityID != cityOf(t, "mihaylovsk") || pg.Items[1].CityID != nil {
		t.Fatalf("city_id: %+v", pg.Items)
	}
	if rn := pg.Items[1].RegionName; rn == nil || *rn != "Свердловская область" || pg.Items[1].Name != "Михайловск" ||
		pg.Items[1].ASCIIName != "Mikhaylovsk" || pg.Items[1].FeatureCode != "PPL" || pg.Items[1].Timezone != "Europe/Moscow" {
		t.Fatalf("место 526815: %+v", pg.Items[1])
	}
	pg, err = r.Places(ctx, geo.PlaceQuery{Q: "Mikh", Locale: "en"})
	if err != nil || !slices.Equal(gids(pg), []int64{493702, 526815}) || pg.Items[1].Name != "Михайловск" ||
		pg.Items[0].Name != "Mikhaylovsk" {
		t.Fatalf("ascii, en (у 526815 нет en — язык страны): %+v %v", pg.Items, err)
	}
	pg, err = r.Places(ctx, geo.PlaceQuery{Country: "XA", Locale: "en"})
	if err != nil || !slices.Equal(gids(pg), []int64{900001}) || pg.Items[0].Country != (geo.Country{Code: "XA", Name: "Testland"}) ||
		pg.Items[0].CityID == nil || *pg.Items[0].CityID != cityOf(t, "skrytsk") {
		t.Fatalf("страна заведённого — страна города: %+v %v", pg.Items, err)
	}
	pg, err = r.Places(ctx, geo.PlaceQuery{Country: "RU"})
	if err != nil || !slices.Equal(gids(pg), []int64{487846, 493702, 526815}) {
		t.Fatalf("RU без 900001 (город в XA): %v %v", gids(pg), err)
	}
	all, _ := r.Places(ctx, geo.PlaceQuery{})
	var paged []int64
	cursor := ""
	for range 10 {
		p, err := r.Places(ctx, geo.PlaceQuery{Cursor: cursor, Limit: new(1)})
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, gids(p)...)
		if cursor = p.Next; cursor == "" {
			break
		}
	}
	if !slices.Equal(paged, gids(all)) || !slices.Equal(gids(all), []int64{900001, 487846, 493702, 526815}) {
		t.Fatalf("курсор: %v, целиком %v", paged, gids(all))
	}
	if _, err := r.Places(ctx, geo.PlaceQuery{Q: "!!!"}); !errors.Is(err, geo.ErrBadQuery) {
		t.Fatalf("q=!!!: %v", err)
	}
}

// Районы: действующие по названию, затем архивные; нет города — ErrCityNotFound; город без районов — пусто.
func TestListGeoDistrictsWithArchived(t *testing.T) {
	t.Parallel()
	r := adminReader(t)
	ctx := context.Background()
	ds, err := r.Districts(ctx, cityOf(t, "stavropol"))
	if err != nil || len(ds) != 3 {
		t.Fatalf("%+v %v", ds, err)
	}
	if ds[0].Name != "Октябрьский" || ds[1].Name != "Промышленный" || ds[2].Name != "Архивный" ||
		ds[0].ArchivedAt != nil || ds[2].ArchivedAt == nil || !ds[2].ArchivedAt.Equal(tImp) || ds[0].Version != 1 {
		t.Fatalf("порядок и архив: %+v", ds)
	}
	if ds, err := r.Districts(ctx, cityOf(t, "skrytsk")); err != nil || ds == nil || len(ds) != 0 {
		t.Fatalf("без районов: %+v %v", ds, err)
	}
	if _, err := r.Districts(ctx, uuid.New()); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("нет города: %v", err)
	}
}

// Страны: все, включая выключенные, по коду; open_cities — pilot и live (waitlist не считается).
func TestListGeoCountriesOpenCities(t *testing.T) {
	t.Parallel()
	cs, err := adminReader(t).Countries(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	byCode := map[string]geo.AdminCountry{}
	codes := []string{}
	for _, c := range cs {
		byCode[c.Code] = c
		codes = append(codes, c.Code)
	}
	if !slices.IsSorted(codes) || !slices.Contains(codes, "RU") || !slices.Contains(codes, "XA") {
		t.Fatalf("страны: %v", codes)
	}
	ru, xa := byCode["RU"], byCode["XA"]
	if ru.OpenCities != 3 || ru.Name != "Russia" || ru.Names["ru"] != "Россия" || ru.Names["en"] != "Russia" ||
		ru.Currency != "RUB" || ru.DefaultLocale != "ru" || ru.PhonePrefix != "+7" || ru.WeekStartsOn != 1 ||
		ru.MinSignupAge != 14 || ru.AgeOfMajority != 18 || !ru.IsEnabled || ru.Version != 1 {
		t.Fatalf("RU: %+v", ru)
	}
	if xa.OpenCities != 1 || xa.IsEnabled || xa.Name != "Testland" {
		t.Fatalf("XA: %+v", xa)
	}
}

// Регионы: по названию на языке ответа, коды admin1 и названия; страны нет — ErrCountryNotFound; страна без
// регионов — пусто.
func TestListGeoRegionsUnknownCountry(t *testing.T) {
	t.Parallel()
	r := adminReader(t)
	ctx := context.Background()
	rs, err := r.Regions(ctx, "RU", "ru")
	if err != nil || len(rs) != 2 || rs[0].Name != "Свердловская область" || rs[1].Name != "Ставропольский край" ||
		rs[1].Admin1Code == nil || *rs[1].Admin1Code != "70" || rs[1].Names["en"] != "Stavropol Krai" || len(rs[0].Names) != 1 {
		t.Fatalf("RU: %+v %v", rs, err)
	}
	if rs, err := r.Regions(ctx, "XA", "ru"); err != nil || rs == nil || len(rs) != 0 {
		t.Fatalf("XA: %+v %v", rs, err)
	}
	if _, err := r.Regions(ctx, "ZZ", "ru"); !errors.Is(err, geo.ErrCountryNotFound) {
		t.Fatalf("ZZ: %v", err)
	}
}

// Отчёт сверки: кандидаты ambiguous и conflict с названием, регионом и населением (удалённое место — без
// строки), linked, not_found, missing, skipped_timezone; идущий импорт — без отчёта; нет строки — 404.
func TestGetGeoImportReport(t *testing.T) {
	t.Parallel()
	r := adminReader(t)
	ctx := context.Background()
	rep, err := r.Import(ctx, adminSeeded.report, "ru")
	if err != nil || rep.Reconciled == nil {
		t.Fatalf("%+v %v", rep, err)
	}
	rc := rep.Reconciled
	if len(rc.Linked) != 1 || rc.Linked[0].GeonameID != 487846 || rc.Linked[0].Slug != "stavropol" ||
		len(rc.NotFound) != 1 || rc.NotFound[0].Slug != "zhdunovo" || !slices.Equal(rc.Missing, []int64{111}) ||
		rc.SkippedTimezone != 3 {
		t.Fatalf("отчёт: %+v", rc)
	}
	amb := rc.Ambiguous[0]
	if amb.Slug != "livsk" || len(amb.Candidates) != 2 || amb.Candidates[0].GeonameID != 526815 ||
		amb.Candidates[0].Name != "Михайловск" || amb.Candidates[0].RegionName == nil ||
		*amb.Candidates[0].RegionName != "Свердловская область" || amb.Candidates[0].Population != 9000 ||
		amb.Candidates[1].GeonameID != 493702 {
		t.Fatalf("ambiguous: %+v", amb)
	}
	if len(rc.Conflict) != 1 || len(rc.Conflict[0].Candidates) != 1 || rc.Conflict[0].Candidates[0].GeonameID != 526815 {
		t.Fatalf("conflict: %+v", rc.Conflict)
	}
	if *rep.PlacesUpserted != 4 || *rep.NamesUpserted != 9 || rep.Status != "succeeded" || rep.FinishedAt == nil {
		t.Fatalf("строка: %+v", rep.Import)
	}
	run, err := r.Import(ctx, adminSeeded.running, "")
	if err != nil || run.Reconciled != nil || run.PlacesUpserted != nil || run.FinishedAt != nil {
		t.Fatalf("идёт: %+v %v", run, err)
	}
	if _, err := r.Import(ctx, uuid.New(), ""); !errors.Is(err, geo.ErrImportNotFound) {
		t.Fatalf("нет импорта: %v", err)
	}
}

// auditCounter — счётчик запросов к audit_log на пуле (подписи журнала — одним запросом на страницу).
type auditCounter struct{ n atomic.Int32 }

func (c *auditCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "audit_log") {
		c.n.Add(1)
	}
	return ctx
}

func (c *auditCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// countingReader — чтения на отдельном пуле к тому же общему клону, со счётчиком запросов журнала.
func countingReader(t *testing.T) (*app.AdminReader, *auditCounter) {
	t.Helper()
	cfg := dbtest.SharedAs(t, "admin", seedAdminReads).As.Config().Copy()
	c := &auditCounter{}
	cfg.ConnConfig.Tracer = c
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return app.NewAdminReader(pool), c
}

func labels(items []geo.Import) map[uuid.UUID]*string {
	out := map[uuid.UUID]*string{}
	for _, it := range items {
		out[it.ID] = it.StartedByLabel
	}
	return out
}

// Подпись из аудита постановки: сотрудник — его имя; оператор — null; строка без записи (чужое действие по той
// же задаче не в счёт) — null; страница из N строк — один запрос к audit_log.
func TestListImportsStartedByFromAudit(t *testing.T) {
	t.Parallel()
	r, cnt := countingReader(t)
	pg, err := r.Imports(context.Background(), geo.ImportQuery{Country: "XM"})
	if err != nil || len(pg.Items) != 3 {
		t.Fatalf("%+v %v", pg, err)
	}
	l := labels(pg.Items)
	if v := l[adminSeeded.xm["staff"]]; v == nil || *v != "Мария К." {
		t.Errorf("сотрудник: %v", v)
	}
	if l[adminSeeded.xm["operator"]] != nil || l[adminSeeded.xm["norecord"]] != nil {
		t.Errorf("оператор и без записи — null: %v, %v", l[adminSeeded.xm["operator"]], l[adminSeeded.xm["norecord"]])
	}
	for _, it := range pg.Items {
		if it.ID == adminSeeded.xm["operator"] && (it.StartedBy != nil || it.RiverJobID != nil) {
			t.Errorf("оператор: %+v", it)
		}
	}
	if n := cnt.n.Load(); n != 1 {
		t.Fatalf("запросов к audit_log %d, ждали 1", n)
	}
	// карточка импорта — та же подпись
	one, err := r.Import(context.Background(), adminSeeded.xm["staff"], "")
	if err != nil || one.StartedByLabel == nil || *one.StartedByLabel != "Мария К." {
		t.Fatalf("getGeoImport: %+v %v", one, err)
	}
}

// Страница из 25 строк — подпись у всех 25 (audit.List по умолчанию отдаёт 20 — лимит задан явно).
func TestListImportsStartedByPage25(t *testing.T) {
	t.Parallel()
	r, cnt := countingReader(t)
	pg, err := r.Imports(context.Background(), geo.ImportQuery{Country: "XL", Limit: new(25)})
	if err != nil || len(pg.Items) != 25 || pg.Next != "" {
		t.Fatalf("%d %q %v", len(pg.Items), pg.Next, err)
	}
	for _, it := range pg.Items {
		want := fmt.Sprintf("Сотрудник %d", *it.RiverJobID-1001)
		if it.StartedByLabel == nil || *it.StartedByLabel != want {
			t.Errorf("задача %d: %v, ждали %q", *it.RiverJobID, it.StartedByLabel, want)
		}
	}
	if n := cnt.n.Load(); n != 1 {
		t.Fatalf("запросов к audit_log %d, ждали 1", n)
	}
	// новые сверху, курсор по страницам
	var all []uuid.UUID
	cursor := ""
	for range 30 {
		p, err := r.Imports(context.Background(), geo.ImportQuery{Country: "XL", Limit: new(10), Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range p.Items {
			all = append(all, it.ID)
		}
		if cursor = p.Next; cursor == "" {
			break
		}
	}
	if len(all) != 25 || all[0] != pg.Items[0].ID || all[24] != pg.Items[24].ID || *pg.Items[0].RiverJobID != 1025 {
		t.Fatalf("страницы: %d", len(all))
	}
}

// Повтор job_id после сброса последовательности: строке старого импорта — подпись старой записи, новой — новой.
func TestListImportsStartedByReusedJobID(t *testing.T) {
	t.Parallel()
	r := adminReader(t)
	pg, err := r.Imports(context.Background(), geo.ImportQuery{Country: "XR"})
	if err != nil || len(pg.Items) != 2 {
		t.Fatalf("%+v %v", pg, err)
	}
	l := labels(pg.Items)
	if v := l[adminSeeded.xr["old"]]; v == nil || *v != "Старая Анна" {
		t.Errorf("старый импорт: %v", v)
	}
	if v := l[adminSeeded.xr["new"]]; v == nil || *v != "Новый Пётр" {
		t.Errorf("новый импорт: %v", v)
	}
	// фильтр job_id отдаёт обе строки задачи, новые сверху
	job := int64(2001)
	pg, err = r.Imports(context.Background(), geo.ImportQuery{JobID: &job})
	if err != nil || len(pg.Items) != 2 || pg.Items[0].ID != adminSeeded.xr["new"] {
		t.Fatalf("job_id: %+v %v", pg, err)
	}
}
