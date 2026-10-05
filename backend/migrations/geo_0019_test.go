package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"

	"wf/backend/internal/platform/migrate"
	"wf/backend/internal/platform/testkit/dbtest"
)

// geoVersion — версия миграции 0019_geo.sql.
const geoVersion = 19

func newProvider(t *testing.T, db *sql.DB) *goose.Provider {
	t.Helper()
	p, err := migrate.NewProvider(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// wantPgError — оператор упал с кодом code на ограничении constraint ("" — имя не проверяем).
func wantPgError(t *testing.T, db *sql.DB, query, code, constraint string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), query)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != code || (constraint != "" && pg.ConstraintName != constraint) {
		t.Fatalf("%s: ждали %s на %q, получили: %v", query, code, constraint, err)
	}
}

// Сид после 0019 (спека geo §3.6): точки, geoname_id и население городов, названия ru/en
// городов, региона и страны, код admin1 региона; centroid обязателен; city_settings заменено
// geo_read_city_settings с колонками §3.7 в этом порядке.
func TestGeo0019SeedFilled(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	type city struct {
		slug, ru, en   string
		geonameID, pop int64
		lat, lon       float64
	}
	want := []city{
		{"mihaylovsk", "Михайловск", "Mikhaylovsk", 493702, 59198, 45.13097, 42.02703},
		{"stavropol", "Ставрополь", "Stavropol", 487846, 433931, 45.03442, 41.9642},
	}
	rows, err := db.QueryContext(ctx, `
		SELECT c.slug, ru.name, en.name, c.geoname_id, c.population,
		       ST_Y(c.centroid::geometry), ST_X(c.centroid::geometry)
		  FROM cities c
		  JOIN city_names ru ON ru.city_id = c.id AND ru.locale = 'ru'
		  JOIN city_names en ON en.city_id = c.id AND en.locale = 'en'
		 ORDER BY c.slug`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []city
	for rows.Next() {
		var c city
		if err := rows.Scan(&c.slug, &c.ru, &c.en, &c.geonameID, &c.pop, &c.lat, &c.lon); err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("города сида: %+v, ждали %+v", got, want)
	}
	for i, w := range want {
		g := got[i]
		if g.slug != w.slug || g.ru != w.ru || g.en != w.en || g.geonameID != w.geonameID || g.pop != w.pop ||
			math.Abs(g.lat-w.lat) > 1e-9 || math.Abs(g.lon-w.lon) > 1e-9 {
			t.Errorf("город %d: %+v, ждали %+v (lat — широта, lon — долгота)", i, g, w)
		}
	}

	var admin1, regionRU, regionEN, countryRU, countryEN string
	if err := db.QueryRowContext(ctx, `
		SELECT r.geoname_admin1_code, rr.name, re.name, cr.name, ce.name
		  FROM regions r
		  JOIN countries co ON co.id = r.country_id AND co.code = 'RU'
		  JOIN region_names rr ON rr.region_id = r.id AND rr.locale = 'ru'
		  JOIN region_names re ON re.region_id = r.id AND re.locale = 'en'
		  JOIN country_names cr ON cr.country_id = co.id AND cr.locale = 'ru'
		  JOIN country_names ce ON ce.country_id = co.id AND ce.locale = 'en'`).
		Scan(&admin1, &regionRU, &regionEN, &countryRU, &countryEN); err != nil {
		t.Fatal(err)
	}
	if admin1 != "70" || regionRU != "Ставропольский край" || regionEN != "Stavropol Krai" ||
		countryRU != "Россия" || countryEN != "Russia" {
		t.Errorf("регион и страна: admin1=%q ru=%q en=%q, страна ru=%q en=%q", admin1, regionRU, regionEN, countryRU, countryEN)
	}

	var notNull bool
	if err := db.QueryRowContext(ctx, `SELECT attnotnull FROM pg_attribute
		WHERE attrelid = 'cities'::regclass AND attname = 'centroid'`).Scan(&notNull); err != nil {
		t.Fatal(err)
	}
	if !notNull {
		t.Error("cities.centroid допускает NULL")
	}

	var legacy sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.city_settings')::text`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Valid {
		t.Error("city_settings не удалено")
	}
	vr, err := db.QueryContext(ctx, `SELECT * FROM geo_read_city_settings LIMIT 0`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := vr.Close(); err != nil {
			t.Error(err)
		}
	}()
	cols, err := vr.Columns()
	if err != nil {
		t.Fatal(err)
	}
	wantCols := []string{"city_id", "city_name", "slug", "timezone", "status", "centroid", "country_id",
		"country_code", "country_enabled", "currency", "default_locale", "phone_prefix", "week_starts_on",
		"min_signup_age", "age_of_majority"}
	if !slices.Equal(cols, wantCols) {
		t.Errorf("колонки geo_read_city_settings: %v, ждали %v", cols, wantCols)
	}
	var enabled bool
	var currency string
	if err := db.QueryRowContext(ctx, `SELECT country_enabled, currency FROM geo_read_city_settings
		WHERE slug = 'stavropol'`).Scan(&enabled, &currency); err != nil {
		t.Fatal(err)
	}
	if !enabled || currency != "RUB" {
		t.Errorf("Ставрополь: country_enabled=%v currency=%q", enabled, currency)
	}

	// префиксный поиск по нормализованному названию (listCities q) видит строку перевода
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM city_names
		WHERE name_normalized LIKE normalize_text('МИХАЙЛ') || '%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("поиск по префиксу «МИХАЙЛ»: %d строк, ждали 1", n)
	}
}

// Ограничения 0019 (спека geo §3.1–3.5): тёзки разрешены, идентичность — geoname_id, имя
// архивного района свободно, один импорт страны за раз, коды admin1 уникальны в стране,
// форматы локали, slug и версий приложения.
func TestGeo0019Constraints(t *testing.T) {
	db := dbtest.New(t)

	// тёзка в той же стране и регионе (Михайловск 70 и 71, Заречный ×5) — не ошибка
	mustExec(t, db, `INSERT INTO cities (country_id, region_id, name, slug, timezone, centroid)
		SELECT country_id, region_id, name, 'mihaylovsk-2', timezone, centroid FROM cities WHERE slug = 'mihaylovsk'`)
	wantPgError(t, db, `UPDATE cities SET geoname_id = 493702 WHERE slug = 'mihaylovsk-2'`, "23505", "cities_geoname_id_key")
	wantPgError(t, db, `UPDATE cities SET slug = 'Bad_Slug' WHERE slug = 'mihaylovsk-2'`, "23514", "cities_slug_format")
	wantPgError(t, db, `UPDATE cities SET slug = 'trailing-' WHERE slug = 'mihaylovsk-2'`, "23514", "cities_slug_format")
	// длиннее maxLength 100 контракта — по API недостижим, база не примет (R13)
	wantPgError(t, db, `UPDATE cities SET slug = repeat('a', 101) WHERE slug = 'mihaylovsk-2'`, "23514", "cities_slug_format")
	wantPgError(t, db, `INSERT INTO cities (country_id, name, slug, timezone)
		SELECT id, 'Без точки', 'no-point', 'Europe/Moscow' FROM countries WHERE code = 'RU'`, "23502", "")

	// районы: архивный не держит имя, два активных с одним нормализованным именем — нельзя
	mustExec(t, db, `INSERT INTO districts (city_id, name, archived_at) SELECT id, 'Центр', now() FROM cities WHERE slug = 'stavropol'`)
	mustExec(t, db, `INSERT INTO districts (city_id, name) SELECT id, 'Центр' FROM cities WHERE slug = 'stavropol'`)
	wantPgError(t, db, `INSERT INTO districts (city_id, name) SELECT id, 'ЦЕНТР' FROM cities WHERE slug = 'stavropol'`,
		"23505", "districts_city_name_active_key")

	// один импорт страны за раз; завершённые не мешают
	mustExec(t, db, `INSERT INTO geonames_imports (country_code, status, started_at) VALUES ('RU', 'running', now())`)
	wantPgError(t, db, `INSERT INTO geonames_imports (country_code, status, started_at) VALUES ('RU', 'running', now())`,
		"23505", "geonames_imports_running")
	mustExec(t, db, `INSERT INTO geonames_imports (country_code, status, started_at, finished_at)
		VALUES ('RU', 'succeeded', now(), now())`)
	wantPgError(t, db, `INSERT INTO geonames_imports (country_code, status, started_at) VALUES ('ru', 'failed', now())`,
		"23514", "geonames_imports_country_code_check")

	// код admin1 уникален в стране
	wantPgError(t, db, `INSERT INTO regions (country_id, name, geoname_admin1_code)
		SELECT id, 'Дубль', '70' FROM countries WHERE code = 'RU'`, "23505", "regions_country_admin1_key")

	// переводы: локаль — две строчные латинские, имя не пустое
	wantPgError(t, db, `INSERT INTO city_names (city_id, locale, name) SELECT id, 'RU', 'x' FROM cities WHERE slug = 'stavropol'`,
		"23514", "city_names_locale_check")
	wantPgError(t, db, `INSERT INTO city_names (city_id, locale, name) SELECT id, 'de', '  ' FROM cities WHERE slug = 'stavropol'`,
		"23514", "city_names_name_check")

	// версии приложения: MAJOR.MINOR.PATCH без ведущих нулей, только https
	mustExec(t, db, `INSERT INTO app_versions (platform, min_version, recommended_version, store_url)
		VALUES ('ios', '1.0.0', '1.10.0', 'https://apps.apple.com/app/id1')`)
	wantPgError(t, db, `INSERT INTO app_versions (platform, min_version, recommended_version, store_url)
		VALUES ('android', '1.2', '1.2.0', 'https://play.google.com/store/apps/details?id=x')`, "23514", "app_versions_min_version_check")
	wantPgError(t, db, `INSERT INTO app_versions (platform, min_version, recommended_version, store_url)
		VALUES ('android', '1.2.0', '01.2.0', 'https://play.google.com/store/apps/details?id=x')`, "23514", "app_versions_recommended_version_check")
	wantPgError(t, db, `INSERT INTO app_versions (platform, min_version, recommended_version, store_url)
		VALUES ('android', '1.2.0', '1.2.0', 'http://play.google.com/store/apps/details?id=x')`, "23514", "app_versions_store_url_check")
	wantPgError(t, db, `INSERT INTO app_versions (platform, min_version, recommended_version, store_url)
		VALUES ('web', '1.2.0', '1.2.0', 'https://example.org')`, "23514", "app_versions_platform_check")
}

// Поиск места по префиксу ascii-названия в админке (спека geo §3.1): индекс по генерируемой
// колонке ascii_name_normalized — индекс по выражению с normalize_text с PG17 не строится
// (CREATE INDEX идёт с search_path = pg_catalog, pg_temp).
func TestGeo0019PlaceAsciiSearch(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	var def string
	if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public' AND indexname = 'geonames_places_ascii_idx'`).Scan(&def); err != nil {
		t.Fatalf("индекс geonames_places_ascii_idx: %v", err)
	}
	const wantDef = "CREATE INDEX geonames_places_ascii_idx ON public.geonames_places USING btree (country_code, ascii_name_normalized text_pattern_ops)"
	if def != wantDef {
		t.Errorf("индекс: %q, ждали %q", def, wantDef)
	}

	mustExec(t, db, `INSERT INTO geonames_imports (id, country_code, status, started_at)
		VALUES ('00000000-0000-0000-0000-000000000019', 'RU', 'succeeded', now())`)
	mustExec(t, db, `INSERT INTO geonames_places (geoname_id, country_code, admin1_code, name, ascii_name,
		feature_code, population, location, timezone, import_id)
		VALUES (487846, 'RU', '70', 'Ставрополь', 'Stavropol', 'PPLA', 433931,
		        'SRID=4326;POINT(41.9642 45.03442)'::geography, 'Europe/Moscow', '00000000-0000-0000-0000-000000000019')`)
	var got, want string
	if err := db.QueryRowContext(ctx, `SELECT ascii_name_normalized, normalize_text('Stavropol')
		FROM geonames_places WHERE geoname_id = 487846`).Scan(&got, &want); err != nil {
		t.Fatal(err)
	}
	if got != want || got == "" {
		t.Errorf("ascii_name_normalized = %q, ждали normalize_text = %q", got, want)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM geonames_places
		WHERE country_code = 'RU' AND ascii_name_normalized LIKE normalize_text('stavr') || '%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("поиск по префиксу «stavr»: %d строк, ждали 1", n)
	}
}

// schemaSnapshotSQL — всё, что задевает 0019: колонки, индексы, ограничения и триггеры таблиц
// geo, определения всех представлений и список отношений public (без объектов расширений).
const schemaSnapshotSQL = `
WITH geo(rel) AS (VALUES ('countries'), ('regions'), ('cities'), ('districts'))
SELECT 'col ' || c.relname || '.' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
       || CASE WHEN a.attnotnull THEN ' not null' ELSE '' END
       || CASE a.attgenerated WHEN 's' THEN ' generated' ELSE '' END
       || coalesce(' default ' || pg_get_expr(d.adbin, d.adrelid), '')
  FROM pg_attribute a
  JOIN pg_class c ON c.oid = a.attrelid AND c.relnamespace = 'public'::regnamespace
  LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
 WHERE c.relname IN (SELECT rel FROM geo) AND a.attnum > 0 AND NOT a.attisdropped
UNION ALL
SELECT 'idx ' || indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename IN (SELECT rel FROM geo)
UNION ALL
SELECT 'con ' || conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid)
  FROM pg_constraint WHERE connamespace = 'public'::regnamespace AND conrelid <> 0
UNION ALL
SELECT 'trg ' || t.tgrelid::regclass::text || ' ' || t.tgname
  FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
 WHERE NOT t.tgisinternal AND c.relnamespace = 'public'::regnamespace
UNION ALL
SELECT 'view ' || viewname || ' ' || definition FROM pg_views WHERE schemaname = 'public'
UNION ALL
SELECT 'rel ' || c.relkind::text || ' ' || c.relname
  FROM pg_class c
 WHERE c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p', 'v', 'm')
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')
ORDER BY 1`

func schemaSnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), schemaSnapshotSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func missingFrom(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// down 0019 возвращает схему 0002 в точности (спека geo §3): снимок базы, на которой 0019 не было
// никогда, совпадает со снимком после up+down — city_settings, cities_country_name_key,
// districts_city_name_key на месте, новых таблиц и колонок нет. Затем повторный up проходит.
func TestGeo0019DownRestoresSchema(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down до 0: %v", err)
	}
	if _, err := p.UpTo(ctx, geoVersion-1); err != nil {
		t.Fatalf("up до %d: %v", geoVersion-1, err)
	}
	before := schemaSnapshot(t, db)

	if _, err := p.UpTo(ctx, geoVersion); err != nil {
		t.Fatalf("up 0019: %v", err)
	}
	if v, err := p.GetDBVersion(ctx); err != nil || v != geoVersion {
		t.Fatalf("версия после up = %d (%v), ждали %d", v, err, geoVersion)
	}
	if _, err := p.DownTo(ctx, geoVersion-1); err != nil {
		t.Fatalf("down 0019: %v", err)
	}
	after := schemaSnapshot(t, db)

	if extra, lost := missingFrom(after, before), missingFrom(before, after); len(extra) != 0 || len(lost) != 0 {
		t.Fatalf("down 0019 не вернул схему 0002:\nлишнее после отката: %q\nпропало после отката: %q", extra, lost)
	}
	for _, want := range []string{
		"idx CREATE UNIQUE INDEX cities_country_name_key ON public.cities USING btree (country_id, name_normalized)",
		"idx CREATE UNIQUE INDEX districts_city_name_key ON public.districts USING btree (city_id, name_normalized)",
		"rel v city_settings",
	} {
		if !slices.Contains(after, want) {
			t.Errorf("после отката нет %q — снимок сравнивает не то", want)
		}
	}
	if _, err := p.UpTo(ctx, geoVersion); err != nil {
		t.Fatalf("повторный up 0019 после отката: %v", err)
	}
}

// Город без точки на базе со старыми данными: 0019 падает с понятным текстом (спека geo §3.6),
// а не общим «column contains null values»; в списке только он — сид получил точки раньше
// проверки. Транзакция миграции откатывается целиком: версия 18, новых таблиц нет.
func TestGeo0019RefusesCityWithoutCentroid(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, geoVersion-1); err != nil {
		t.Fatalf("down 0019: %v", err)
	}
	mustExec(t, db, `INSERT INTO cities (country_id, name, slug, timezone)
		SELECT id, 'Безточечный', 'no-point', 'Europe/Moscow' FROM countries WHERE code = 'RU'`)

	_, err := p.UpTo(ctx, geoVersion)
	const wantMsg = "города без centroid: no-point — заполните точки (см. спеку geo §3.6)"
	if err == nil || !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("ждали ошибку %q, получили: %v", wantMsg, err)
	}
	if v, err := p.GetDBVersion(ctx); err != nil || v != geoVersion-1 {
		t.Fatalf("версия после отказа = %d (%v), ждали %d", v, err, geoVersion-1)
	}
	var names sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.city_names')::text`).Scan(&names); err != nil {
		t.Fatal(err)
	}
	if names.Valid {
		t.Fatal("после отказа осталась city_names — миграция применилась частично")
	}

	// оператор заполнил точку — миграция проходит
	mustExec(t, db, `UPDATE cities SET centroid = ST_SetSRID(ST_MakePoint(42.0, 45.0), 4326)::geography
		WHERE slug = 'no-point'`)
	if _, err := p.UpTo(ctx, geoVersion); err != nil {
		t.Fatalf("up после заполнения точки: %v", err)
	}
}
