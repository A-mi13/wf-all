package app_test

import (
	"context"
	"slices"
	"testing"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Админские listGeoCities и listGeoPlaces: префикс в любом регистре находит город и место GeoNames — в том числе со
// строчными латинскими b h m t (0023_normalize_text_case.sql; до неё 'mikh', 'tv', 'bar' не находили Mikhaylovsk,
// Tver, Barnaul). Свой клон: общий seedAdminReads не трогается. Места 99000x — условные geoname_id.
func TestAdminSearchIgnoresCase(t *testing.T) {
	t.Parallel()
	pools := dbtest.NewPoolsAs(t, "admin")
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO cities (country_id, name, slug, timezone, centroid, status, population)
		 SELECT id, v.name, v.slug, 'Europe/Moscow', ST_SetSRID(ST_MakePoint(v.lon, v.lat), 4326)::geography, 'waitlist', 1000
		   FROM countries, (VALUES ('Тверь', 'tver', 35.9, 56.86), ('Барнаул', 'barnaul', 83.76, 53.35)) AS v(name, slug, lon, lat)
		  WHERE code = 'RU'`,
		`INSERT INTO city_names (city_id, locale, name)
		 SELECT c.id, v.locale, v.name FROM cities c
		   JOIN (VALUES ('tver', 'ru', 'Тверь'), ('tver', 'en', 'Tver'), ('barnaul', 'ru', 'Барнаул'),
		                ('barnaul', 'en', 'Barnaul')) AS v(slug, locale, name) ON v.slug = c.slug`,
		`INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at)
		 VALUES ('01900000-0000-7000-8000-0000000003a1', 'RU', 'succeeded', now(), now())`,
		`INSERT INTO geonames_places (geoname_id, country_code, name, ascii_name, feature_code, population, location,
		                             timezone, import_id)
		 SELECT v.id, 'RU', v.name, v.name, 'PPL', v.pop, ST_SetSRID(ST_MakePoint(40, 50), 4326)::geography,
		        'Europe/Moscow', '01900000-0000-7000-8000-0000000003a1'
		   FROM (VALUES (990001, 'Mikhaylovsk', 3), (990002, 'Tver', 2), (990003, 'Barnaul', 1)) AS v(id, name, pop)`,
		`INSERT INTO geonames_place_names (geoname_id, locale, name)
		 VALUES (990001, 'ru', 'Михайловск'), (990002, 'ru', 'Тверь'), (990003, 'ru', 'Барнаул')`,
	} {
		if _, err := pools.Owner.Exec(ctx, q); err != nil {
			t.Fatalf("посев: %v", err)
		}
	}
	r := app.NewAdminReader(pools.As)

	for _, c := range []struct {
		q    string
		want []string
	}{
		{"mikh", []string{"mihaylovsk"}},
		{"MIKH", []string{"mihaylovsk"}},
		{"михайловск", []string{"mihaylovsk"}},
		{"tv", []string{"tver"}},
		{"TV", []string{"tver"}},
		{"bar", []string{"barnaul"}},
		{"BAR", []string{"barnaul"}},
	} {
		pg, err := r.Cities(ctx, geo.AdminCityQuery{Q: c.q})
		if err != nil || !slices.Equal(citySlugs(pg.Items), c.want) {
			t.Errorf("города q=%q: %q %v, ждали %q", c.q, citySlugs(pg.Items), err, c.want)
		}
	}

	for _, c := range []struct {
		q    string
		want []int64
	}{
		{"mikh", []int64{990001}},
		{"MIKH", []int64{990001}},
		{"михайл", []int64{990001}},
		{"tv", []int64{990002}},
		{"TVER", []int64{990002}},
		{"bar", []int64{990003}},
		{"Bar", []int64{990003}},
	} {
		pg, err := r.Places(ctx, geo.PlaceQuery{Q: c.q})
		got := []int64{}
		for _, p := range pg.Items {
			got = append(got, p.GeonameID)
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("места q=%q: %v %v, ждали %v", c.q, got, err, c.want)
		}
	}
}
