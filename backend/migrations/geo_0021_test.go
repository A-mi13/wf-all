package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/testkit/dbtest"
)

// geoAdminVersion — версия миграции 0021_geo_admin.sql.
const geoAdminVersion = 21

// columnsSnapshot — колонки всех таблиц public (без объектов расширений): снимок схемы 0019
// колонки feature_flags и app_versions не видит.
func columnsSnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT c.relname || '.' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
		       || CASE WHEN a.attnotnull THEN ' not null' ELSE '' END
		       || coalesce(' default ' || pg_get_expr(d.adbin, d.adrelid), '')
		  FROM pg_attribute a
		  JOIN pg_class c ON c.oid = a.attrelid AND c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p')
		  LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		 WHERE a.attnum > 0 AND NOT a.attisdropped
		   AND NOT EXISTS (SELECT 1 FROM pg_depend dp WHERE dp.objid = c.oid AND dp.deptype = 'e')
		 ORDER BY 1`)
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

// Накат и откат 0021 (спека сотрудников §3.2, R12): после up — версия флагов, строгий store_url и
// форматы правил страны, сид их проходит; down возвращает схему 0020 в точности (колонки всех
// таблиц, индексы и ограничения, прежний CHECK store_url с прежним именем); повторный up проходит.
func TestGeo0021UpDown(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()
	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, geoAdminVersion-1); err != nil {
		t.Fatalf("down до %d: %v", geoAdminVersion-1, err)
	}
	before := append(schemaSnapshot(t, db), columnsSnapshot(t, db)...)
	if !slices.Contains(before, "con app_versions app_versions_store_url_check CHECK ((store_url ~ '^https://'::text))") {
		t.Fatal("до 0021 нет прежнего CHECK store_url — снимок сравнивает не то")
	}

	if _, err := p.UpTo(ctx, geoAdminVersion); err != nil {
		t.Fatalf("up 0021 на сиде: %v", err)
	}
	up := append(schemaSnapshot(t, db), columnsSnapshot(t, db)...)
	for _, want := range []string{
		"feature_flags.version bigint not null default 1",
		"con feature_flags feature_flags_version_check CHECK ((version > 0))",
		"con app_versions app_versions_store_url_check CHECK ((store_url ~ '^https://[^[:space:][:cntrl:]]+$'::text))",
		"con countries countries_code_format CHECK ((code ~ '^[A-Z]{2}$'::text))",
		"con countries countries_currency_format CHECK ((currency ~ '^[A-Z]{3}$'::text))",
		"con countries countries_default_locale_format CHECK ((default_locale ~ '^[a-z]{2}$'::text))",
		"con countries countries_phone_prefix_format CHECK ((phone_prefix ~ '^\\+[1-9][0-9]{0,3}$'::text))",
		"con countries countries_ages_range CHECK ((((min_signup_age >= 1) AND (min_signup_age <= 120)) AND ((age_of_majority >= 1) AND (age_of_majority <= 120))))",
	} {
		if !slices.Contains(up, want) {
			t.Errorf("после up 0021 нет %q", want)
		}
	}

	if _, err := p.DownTo(ctx, geoAdminVersion-1); err != nil {
		t.Fatalf("down 0021: %v", err)
	}
	after := append(schemaSnapshot(t, db), columnsSnapshot(t, db)...)
	if extra, lost := missingFrom(after, before), missingFrom(before, after); len(extra) != 0 || len(lost) != 0 {
		t.Fatalf("down 0021 не вернул схему 0020:\nлишнее после отката: %q\nпропало после отката: %q", extra, lost)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("повторный up после отката 0021: %v", err)
	}
}

// Старые данные, не проходящие новые CHECK, — миграция падает целиком с именем ограничения, версия
// остаётся 20, колонки feature_flags.version нет (частично не применяется). Исправили — проходит.
// Перечень SELECT-проверок стенда под эти CHECK — Task 25 плана B (R24).
func TestGeo0021RefusesBadData(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()
	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, geoAdminVersion-1); err != nil {
		t.Fatalf("down до %d: %v", geoAdminVersion-1, err)
	}

	refused := func(constraint string) {
		t.Helper()
		_, err := p.UpTo(ctx, geoAdminVersion)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "23514" || pg.ConstraintName != constraint {
			t.Fatalf("ждали отказ 23514 на %q, получили: %v", constraint, err)
		}
		if v, err := p.GetDBVersion(ctx); err != nil || v != geoAdminVersion-1 {
			t.Fatalf("версия после отказа = %d (%v), ждали %d", v, err, geoAdminVersion-1)
		}
		var col sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT attname FROM pg_attribute
			WHERE attrelid = 'feature_flags'::regclass AND attname = 'version' AND NOT attisdropped`).Scan(&col); err != nil &&
			!errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		if col.Valid {
			t.Fatal("после отказа осталась feature_flags.version — миграция применилась частично")
		}
	}

	mustExec(t, db, `INSERT INTO app_versions (platform, min_version, recommended_version, store_url)
		VALUES ('ios', '1.0.0', '1.0.0', 'https://apps.apple.com/app/id1 x')`)
	refused("app_versions_store_url_check")
	mustExec(t, db, `UPDATE app_versions SET store_url = 'https://apps.apple.com/app/id1' WHERE platform = 'ios'`)

	mustExec(t, db, `UPDATE countries SET currency = 'rub' WHERE code = 'RU'`)
	refused("countries_currency_format")
	mustExec(t, db, `UPDATE countries SET currency = 'RUB' WHERE code = 'RU'`)

	if _, err := p.UpTo(ctx, geoAdminVersion); err != nil {
		t.Fatalf("up после исправления данных: %v", err)
	}
}

// CHECK store_url (спека сотрудников §6.3 п.6): https:// в нижнем регистре, дальше хотя бы один
// символ и ни одного пробельного или управляющего. Не пробельный управляющий (\x01, ESC) ловит
// только [:cntrl:], пробел — только [:space:]: каждый класс со своим случаем и подсадкой.
// U+00A0 и U+0085 регулярка PostgreSQL ловить не обязана — их ловит код (appversion.Validate).
func TestGeo0021StoreURLCheck(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	const constraint = "app_versions_store_url_check"
	insert := func(url string) string {
		return `INSERT INTO app_versions (platform, min_version, recommended_version, store_url)
			VALUES ('ios', '1.0.0', '1.0.0', ` + url + `)`
	}
	dropSpace := `ALTER TABLE app_versions DROP CONSTRAINT app_versions_store_url_check,
		ADD CONSTRAINT app_versions_store_url_check CHECK (store_url ~ '^https://[^[:cntrl:]]+$')`
	dropCntrl := `ALTER TABLE app_versions DROP CONSTRAINT app_versions_store_url_check,
		ADD CONSTRAINT app_versions_store_url_check CHECK (store_url ~ '^https://[^[:space:]]+$')`
	dropAll := `ALTER TABLE app_versions DROP CONSTRAINT app_versions_store_url_check`
	guards := []r13Guard{
		{name: "пробел в пути", check: violates(insert(`'https://apps.apple.com/app/id1 x'`), "23514", constraint, ""), plant: dropSpace},
		{name: "TAB", check: violates(insert(`'https://apps.apple.com/' || chr(9) || 'app'`), "23514", constraint, ""), plant: dropAll},
		{name: "перевод строки в конце", check: violates(insert(`'https://apps.apple.com/app/id1' || chr(10)`), "23514", constraint, ""), plant: dropAll},
		{name: "управляющий \\x01", check: violates(insert(`'https://apps.apple.com/' || chr(1) || 'app'`), "23514", constraint, ""), plant: dropCntrl},
		{name: "ESC", check: violates(insert(`'https://apps.apple.com/' || chr(27) || 'app'`), "23514", constraint, ""), plant: dropCntrl},
		{name: "схема в верхнем регистре", check: violates(insert(`'HTTPS://apps.apple.com/app/id1'`), "23514", constraint, ""), plant: dropAll},
		{name: "пусто после схемы", check: violates(insert(`'https://'`), "23514", constraint, ""), plant: dropAll},
	}
	runGuards(t, db, guards)

	if err := rolledBack(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insert(`'https://apps.apple.com/app/id1'`))
		return err
	}); err != nil {
		t.Errorf("допустимый store_url: %v", err)
	}
}

// Форматы правил страны (R12): каждый CHECK со своим именем; код проверяет строже (действующие ISO,
// поддерживаемые локали), база — защита в глубину.
func TestGeo0021CountryFormats(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	upd := func(set string) string { return `UPDATE countries SET ` + set + ` WHERE code = 'RU'` }
	drop := func(name string) string { return `ALTER TABLE countries DROP CONSTRAINT ` + name }
	guards := []r13Guard{
		{name: "code строчными", check: violates(upd(`code = 'ru'`), "23514", "countries_code_format", ""), plant: drop("countries_code_format")},
		{name: "code с цифрой", check: violates(upd(`code = 'R1'`), "23514", "countries_code_format", ""), plant: drop("countries_code_format")},
		{name: "currency строчными", check: violates(upd(`currency = 'rub'`), "23514", "countries_currency_format", ""), plant: drop("countries_currency_format")},
		{name: "default_locale заглавными", check: violates(upd(`default_locale = 'RU'`), "23514", "countries_default_locale_format", ""), plant: drop("countries_default_locale_format")},
		{name: "phone_prefix без плюса", check: violates(upd(`phone_prefix = '7'`), "23514", "countries_phone_prefix_format", ""), plant: drop("countries_phone_prefix_format")},
		{name: "phone_prefix с ведущим нулём", check: violates(upd(`phone_prefix = '+07'`), "23514", "countries_phone_prefix_format", ""), plant: drop("countries_phone_prefix_format")},
		{name: "phone_prefix из пяти цифр", check: violates(upd(`phone_prefix = '+12345'`), "23514", "countries_phone_prefix_format", ""), plant: drop("countries_phone_prefix_format")},
		{name: "min_signup_age = 0", check: violates(upd(`min_signup_age = 0`), "23514", "countries_ages_range", ""), plant: drop("countries_ages_range")},
		{name: "age_of_majority = 121", check: violates(upd(`age_of_majority = 121`), "23514", "countries_ages_range", ""), plant: drop("countries_ages_range")},
	}
	runGuards(t, db, guards)

	// границы: префикс из четырёх цифр, возрасты 1 и 120
	if err := rolledBack(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, upd(`phone_prefix = '+1234', min_signup_age = 1, age_of_majority = 120`))
		return err
	}); err != nil {
		t.Errorf("границы правил страны: %v", err)
	}
}

// Сид флагов получает version = 1 (спека сотрудников §3.2): оптимистичная версия PATCH /v1/flags/{key}.
func TestFeatureFlagsVersion(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	var total, bad int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE version <> 1) FROM feature_flags`).Scan(&total, &bad); err != nil {
		t.Fatal(err)
	}
	if total == 0 || bad != 0 {
		t.Fatalf("флагов %d, с version <> 1: %d", total, bad)
	}
	wantPgError(t, db, `UPDATE feature_flags SET version = 0 WHERE key = 'ads'`, "23514", "feature_flags_version_check")
}
