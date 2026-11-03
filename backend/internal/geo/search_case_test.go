package geo_test

import (
	"context"
	"slices"
	"testing"

	"wf/backend/internal/geo"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Публичный listCities (контракт: поиск «без учёта регистра»): префикс в любом регистре находит город по названию
// любой локали — в том числе со строчными латинскими b h m t (0023_normalize_text_case.sql; до неё 'mikh' не
// находил Mikhaylovsk, 'tv' — Tver, 'bar' — Barnaul). Свой клон: посев Твери и Барнаула не трогает общий seedReads.
func TestListCitiesSearchIgnoresCase(t *testing.T) {
	t.Parallel()
	pools := dbtest.NewPoolsAs(t, "api")
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
	} {
		if _, err := pools.Owner.Exec(ctx, q); err != nil {
			t.Fatalf("посев: %v", err)
		}
	}
	svc := geo.New(pools.As)
	for _, c := range []struct {
		q    string
		want []string
	}{
		{"mikh", []string{"mihaylovsk"}},
		{"MIKH", []string{"mihaylovsk"}},
		{"Mikh", []string{"mihaylovsk"}},
		{"mikhaylovsk", []string{"mihaylovsk"}},
		{"михайловск", []string{"mihaylovsk"}},
		{"МИХАЙЛОВСК", []string{"mihaylovsk"}},
		{"tv", []string{"tver"}},
		{"TV", []string{"tver"}},
		{"tver", []string{"tver"}},
		{"bar", []string{"barnaul"}},
		{"BAR", []string{"barnaul"}},
		{"барн", []string{"barnaul"}},
		{"stavropol", []string{"stavropol"}},
	} {
		pg, err := svc.ListCities(ctx, geo.ListParams{Q: c.q})
		if err != nil || !slices.Equal(slugs(pg.Items), c.want) {
			t.Errorf("q=%q: %q %v, ждали %q", c.q, slugs(pg.Items), err, c.want)
		}
	}
}
