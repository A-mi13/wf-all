package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/testkit/dbtest"
)

// normalizeCaseVersion — версия миграции 0023_normalize_text_case.sql.
const normalizeCaseVersion = 23

// homoglyphsFrom, homoglyphsTo — строчные латинские двойники кириллицы (таблица новой normalize_text). Через них
// выражается связь старой и новой функции: новое(x) = translate(старое(x), homoglyphsFrom, homoglyphsTo).
const (
	homoglyphsFrom = "abcehkmoptxy"
	homoglyphsTo   = "авсенкмортху"
)

// alphabets — все буквы латиницы и кириллицы (в нижнем регистре; верхний получает upper в базе).
const alphabets = "abcdefghijklmnopqrstuvwxyzабвгдеёжзийклмнопрстуфхцчшщъыьэюя"

func queryStrings(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
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

func normalize(t *testing.T, db *sql.DB, s string) string {
	t.Helper()
	var out string
	if err := db.QueryRowContext(context.Background(), `SELECT normalize_text($1)`, s).Scan(&out); err != nil {
		t.Fatalf("normalize_text(%q): %v", s, err)
	}
	return out
}

// Регистр не влияет ни на одну букву латиницы и кириллицы; префикс в любом регистре — префикс названия;
// клоны латиницей и с диакритикой совпадают; разные названия — разные; NULL, пустое и чужая письменность.
func TestNormalizeTextCase(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	// каждая буква: n(x) = n(upper(x)) = n(lower(x)), и не пусто
	letterSQL := `SELECT %s FROM (SELECT ch, normalize_text(ch) AS n, normalize_text(upper(ch)) AS up, normalize_text(lower(ch)) AS low
		FROM unnest(string_to_array($1::text, NULL)) AS ch) AS l`
	for _, bad := range queryStrings(t, db, fmt.Sprintf(letterSQL, `format('буква %s: как есть %s, upper %s, lower %s', ch, n, up, low)`)+
		` WHERE n = '' OR n <> up OR n <> low`, alphabets) {
		t.Errorf("%s — ждали одно непустое", bad)
	}
	var letters int
	if err := db.QueryRowContext(ctx, fmt.Sprintf(letterSQL, "count(*)"), alphabets).Scan(&letters); err != nil {
		t.Fatal(err)
	}
	if letters != 26+33 {
		t.Fatalf("проверено букв %d, ждали 59", letters)
	}

	// префикс в любом регистре находит название (поиск по началу в geo)
	for _, c := range []struct{ prefix, name string }{
		{"mikh", "Mikhaylovsk"}, {"MIKH", "Mikhaylovsk"}, {"Mikh", "mikhaylovsk"}, {"mIkH", "MIKHAYLOVSK"},
		{"tv", "Tver"}, {"TV", "tver"}, {"bar", "Barnaul"}, {"BAR", "barnaul"}, {"moscow", "MOSCOW"},
		{"мих", "Михайловск"}, {"МИХ", "михайловск"}, {"михайловск", "МИХАЙЛОВСК"}, {"орел", "Орёл"},
	} {
		p, n := normalize(t, db, c.prefix), normalize(t, db, c.name)
		if !strings.HasPrefix(n, p) {
			t.Errorf("%q → %q не префикс %q → %q", c.prefix, p, c.name, n)
		}
	}

	// одно значение у каждой группы: регистр, пробелы и знаки, клоны латиницей, диакритика
	for _, group := range [][]string{
		{"Mikhaylovsk", "mikhaylovsk", "MIKHAYLOVSK", "mIkHaYlOvSk"},
		{"Михайловск", "михайловск", "МИХАЙЛОВСК"},
		{"Быки", "БЫКИ", " быки ", "Б.ы.к.и", "б_ы-к и"},
		{"MOCKBA", "mockba", "Москва", "москва", "MOCKВА"}, // латиница, кириллица и смесь
		{"BMW", "ВМW", "bmw"}, // В и М кириллические
		{"Bulkan", "Вulkan", "вulkаn"},
		{"Cafe", "Café", "CAFÉ", "cafe", "Сafe"}, // С кириллическая
		{"Tom", "tom", "TOM", "Том"},
	} {
		want := normalize(t, db, group[0])
		for _, s := range group[1:] {
			if got := normalize(t, db, s); got != want {
				t.Errorf("%q → %q, а %q → %q: ждали одно значение", s, got, group[0], want)
			}
		}
	}

	// разные названия остаются разными
	for _, pair := range [][2]string{{"Быки", "Бычки"}, {"Tver", "Tula"}, {"Barnaul", "Varnaul"}, {"mikh", "nikh"}} {
		if a, b := normalize(t, db, pair[0]), normalize(t, db, pair[1]); a == b {
			t.Errorf("%q и %q дают одно %q", pair[0], pair[1], a)
		}
	}

	// точные значения: латиница-двойник — кириллица, остальная латиница — как есть
	for in, want := range map[string]string{
		"Mikhaylovsk": "мiкнауlоvsк",
		"mikh":        "мiкн",
		"Tver":        "тvеr",
		"Barnaul":     "ваrnаul",
		"  Орёл ":     "орел",
		"":            "",
		"   ":         "",
		"東京":          "",
		"★!":          "",
	} {
		if got := normalize(t, db, in); got != want {
			t.Errorf("normalize_text(%q) = %q, ждали %q", in, got, want)
		}
	}
	var null sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT normalize_text(NULL)`).Scan(&null); err != nil || null.Valid {
		t.Errorf("normalize_text(NULL) = %+v %v, ждали NULL", null, err)
	}

	// свойства функции: та же сигнатура и IMMUTABLE STRICT PARALLEL SAFE (генерируемые колонки, план запроса)
	var volatile, parallel string
	var strict bool
	if err := db.QueryRowContext(ctx, `SELECT provolatile::text, proisstrict, proparallel::text FROM pg_proc
		WHERE oid = 'normalize_text(text)'::regprocedure`).Scan(&volatile, &strict, &parallel); err != nil {
		t.Fatal(err)
	}
	if volatile != "i" || !strict || parallel != "s" {
		t.Errorf("normalize_text: provolatile %q, strict %v, parallel %q — ждали i, true, s", volatile, strict, parallel)
	}
}

// generatedNormalized — генерируемые колонки на normalize_text: «таблица.колонка ← источник».
func generatedNormalized(t *testing.T, db *sql.DB) [][3]string {
	t.Helper()
	var out [][3]string
	for _, s := range queryStrings(t, db, `
		SELECT c.relname || ' ' || a.attname || ' ' || substring(pg_get_expr(d.adbin, d.adrelid) FROM '^normalize_text\((\w+)\)$')
		  FROM pg_attribute a
		  JOIN pg_class c ON c.oid = a.attrelid AND c.relnamespace = 'public'::regnamespace AND c.relkind = 'r'
		  JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		 WHERE a.attgenerated = 's' AND NOT a.attisdropped AND pg_get_expr(d.adbin, d.adrelid) LIKE '%normalize_text(%'
		 ORDER BY 1`) {
		f := strings.Fields(s)
		if len(f) != 3 {
			t.Fatalf("генерируемая колонка не вида normalize_text(колонка): %q", s)
		}
		out = append(out, [3]string{f[0], f[1], f[2]})
	}
	return out
}

// normalizedValues — значения всех генерируемых колонок: «таблица.колонка источник → значение», по порядку.
func normalizedValues(t *testing.T, db *sql.DB, cols [][3]string) []string {
	t.Helper()
	var out []string
	for _, c := range cols {
		table, col, src := pgx.Identifier{c[0]}.Sanitize(), pgx.Identifier{c[1]}.Sanitize(), pgx.Identifier{c[2]}.Sanitize()
		vals := queryStrings(t, db, fmt.Sprintf(`SELECT %s || ' → ' || %s FROM %s ORDER BY 1`, src, col, table))
		if len(vals) == 0 {
			t.Fatalf("в %s нет строк — пересчёт %s не проверен", c[0], c[1])
		}
		for _, v := range vals {
			out = append(out, c[0]+"."+c[1]+" "+v)
		}
	}
	return out
}

// stale — строки, где генерируемая колонка не равна normalize_text(источника) текущей функции.
func stale(t *testing.T, db *sql.DB, cols [][3]string) []string {
	t.Helper()
	var out []string
	for _, c := range cols {
		table, col, src := pgx.Identifier{c[0]}.Sanitize(), pgx.Identifier{c[1]}.Sanitize(), pgx.Identifier{c[2]}.Sanitize()
		for _, v := range queryStrings(t, db, fmt.Sprintf(
			`SELECT %s || ' → ' || %s FROM %s WHERE %s IS DISTINCT FROM normalize_text(%s)`, src, col, table, col, src)) {
			out = append(out, c[0]+"."+c[1]+" "+v)
		}
	}
	return out
}

// seed0023 — по строке с латинскими b h m t в каждую таблицу с генерируемой колонкой и в хранимые ники (версия 22).
func seed0023(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO cities (country_id, name, slug, timezone, centroid, status)
		 SELECT id, 'Tambov', 'tambov', 'Europe/Moscow', ST_SetSRID(ST_MakePoint(41.45, 52.72), 4326)::geography, 'waitlist'
		   FROM countries WHERE code = 'RU'`,
		`INSERT INTO city_names (city_id, locale, name)
		 SELECT id, 'en', 'Tambov' FROM cities WHERE slug = 'tambov'
		 UNION ALL SELECT id, 'ru', 'Тамбов' FROM cities WHERE slug = 'tambov'`,
		`INSERT INTO districts (city_id, name) SELECT id, 'Bolshoy' FROM cities WHERE slug = 'stavropol'`,
		`INSERT INTO users (email, nickname, birth_date) VALUES ('tom@example.com', 'tom_hanks', '2000-01-01')`,
		`INSERT INTO teams (city_id, name, display_name, slug, captain_id, primary_format, roster_limit)
		 SELECT c.id, 'barsy', 'barsy Stavropol', 'barsy', u.id, '5x5', 10
		   FROM cities c, users u WHERE c.slug = 'stavropol' AND u.nickname = 'tom_hanks'`,
		`INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at)
		 VALUES ('01900000-0000-7000-8000-0000000002a1', 'RU', 'succeeded', now(), now())`,
		`INSERT INTO geonames_places (geoname_id, country_code, name, ascii_name, feature_code, location, timezone, import_id)
		 VALUES (980001, 'RU', 'Михайловка', 'Mikhaylovka', 'PPL', ST_SetSRID(ST_MakePoint(44, 50), 4326)::geography,
		         'Europe/Moscow', '01900000-0000-7000-8000-0000000002a1')`,
		`INSERT INTO geonames_place_names (geoname_id, locale, name) VALUES (980001, 'en', 'mikhaylovka')`,
		// хранимые ники: старое 'ADMIN' рядом со старым 'admin' из сида 0010 — после пересчёта одно (клон собран на последней
		// версии, и 'ADMIN' уже мог оставить откат 0023)
		`INSERT INTO reserved_nicknames (nickname_normalized, reason) VALUES (normalize_text('ADMIN'), 'test') ON CONFLICT DO NOTHING`,
		// карантин: 'Tim' и 'tiM' — разные старые значения, одно новое; остаётся поздний срок
		`INSERT INTO nickname_reservations (nickname_normalized, released_at, expires_at)
		 VALUES (normalize_text('Tim'), '2026-10-01T12:00:00Z', '2026-12-30T12:00:00Z'), (normalize_text('tiM'), '2026-10-02T12:00:00Z', '2026-12-31T12:00:00Z'),
		        (normalize_text('Mia'), '2026-10-03T12:00:00Z', '2027-01-01T12:00:00Z')`,
	} {
		mustExec(t, db, q)
	}
}

// Накат и откат 0023 на клоне: все генерируемые колонки на normalize_text пересчитаны новой функцией (значения
// до/после), хранимые ники — тем же translate без потерь; откат возвращает прежнюю функцию и прежние значения
// колонок в точности; повторный накат проходит.
func Test0023UpDown(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, normalizeCaseVersion-1); err != nil {
		t.Fatalf("down до %d: %v", normalizeCaseVersion-1, err)
	}
	if got := normalize(t, db, "mikh"); got != "miкh" {
		t.Fatalf("до 0023 normalize_text('mikh') = %q, ждали прежнее 'miкh' — тест проверяет не то", got)
	}
	seed0023(t, db)

	cols := generatedNormalized(t, db)
	wantCols := [][3]string{
		{"cities", "name_normalized", "name"},
		{"city_names", "name_normalized", "name"},
		{"districts", "name_normalized", "name"},
		{"geonames_place_names", "name_normalized", "name"},
		{"geonames_places", "ascii_name_normalized", "ascii_name"},
		{"teams", "name_normalized", "name"},
		{"users", "nickname_normalized", "nickname"},
	}
	if !slices.Equal(cols, wantCols) {
		t.Fatalf("генерируемые колонки на normalize_text: %q, ждали %q", cols, wantCols)
	}
	before := normalizedValues(t, db, cols)
	corpus := []string{alphabets, strings.ToUpper(alphabets), "Mikhaylovsk", "Café CAFÉ", "Ḿḿ Ħħ Ṫṫ Ḃḃ Æ ß ℃",
		"Tver Барнаул Орёл ЁЖ", "BMW ВМW", " Б.ы_к-и ", "東京 ★", ""}
	oldCorpus := make([]string, len(corpus))
	for i, s := range corpus {
		oldCorpus[i] = normalize(t, db, s)
	}
	reservedBefore := queryStrings(t, db, `SELECT nickname_normalized FROM reserved_nicknames ORDER BY 1`)
	if !slices.Contains(reservedBefore, "аdmin") || !slices.Contains(reservedBefore, "аdмin") {
		t.Fatalf("до 0023 нет старых 'аdmin' и 'аdмin': %q", reservedBefore)
	}

	if _, err := p.UpTo(ctx, normalizeCaseVersion); err != nil {
		t.Fatalf("up 0023: %v", err)
	}
	if bad := stale(t, db, cols); len(bad) != 0 {
		t.Fatalf("после up 0023 не пересчитаны: %q", bad)
	}
	up := normalizedValues(t, db, cols)
	// в каждой колонке посев меняет значение — иначе «не пересчитано» (в том числе откатом) не видно
	for _, c := range cols {
		prefix := c[0] + "." + c[1] + " "
		if !slices.ContainsFunc(up, func(v string) bool { return strings.HasPrefix(v, prefix) && !slices.Contains(before, v) }) {
			t.Errorf("посев не меняет ни одного значения %s", prefix)
		}
	}
	for _, want := range []string{
		"cities.name_normalized Tambov → тамвоv",
		"city_names.name_normalized Tambov → тамвоv",
		"districts.name_normalized Bolshoy → воlsноу",
		"geonames_place_names.name_normalized mikhaylovka → мiкнауlоvка",
		"geonames_places.ascii_name_normalized Mikhaylovka → мiкнауlоvка",
		"teams.name_normalized barsy → ваrsу",
		"users.nickname_normalized tom_hanks → томнаnкs",
	} {
		if !slices.Contains(up, want) {
			t.Errorf("после up 0023 нет %q", want)
		}
	}
	// новое(x) = translate(старое(x)): на этом держатся пересчёт хранимых ников и проверка стенда до наката
	for i, s := range corpus {
		var got, want string
		if err := db.QueryRowContext(ctx, `SELECT normalize_text($1), translate($2, $3, $4)`,
			s, oldCorpus[i], homoglyphsFrom, homoglyphsTo).Scan(&got, &want); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%q: новое %q, translate(старого %q) = %q", s, got, oldCorpus[i], want)
		}
	}

	// хранимые ники: каждый старый — translate, совпавшие схлопнуты в один; карантин — с поздним сроком
	var wantReserved []string
	for _, v := range reservedBefore {
		var n string
		if err := db.QueryRowContext(ctx, `SELECT translate($1, $2, $3)`, v, homoglyphsFrom, homoglyphsTo).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(wantReserved, n) {
			wantReserved = append(wantReserved, n)
		}
	}
	slices.Sort(wantReserved)
	if got := queryStrings(t, db, `SELECT nickname_normalized FROM reserved_nicknames ORDER BY nickname_normalized COLLATE "C"`); !slices.Equal(got, wantReserved) {
		t.Errorf("reserved_nicknames после up: %q, ждали %q", got, wantReserved)
	}
	for _, nick := range []string{"admin", "ADMIN", "Admin", "moderator", "MODERATOR", "Админ"} {
		var reserved bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM reserved_nicknames WHERE nickname_normalized = normalize_text($1))`,
			nick).Scan(&reserved); err != nil || !reserved {
			t.Errorf("ник %q после up не зарезервирован (%v)", nick, err)
		}
	}
	if got := queryStrings(t, db, `SELECT nickname_normalized || ' ' || to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD')
		FROM nickname_reservations ORDER BY 1`); !slices.Equal(got, []string{"мiа 2027-01-01", "тiм 2026-12-31"}) {
		t.Errorf("nickname_reservations после up: %q", got)
	}

	if _, err := p.DownTo(ctx, normalizeCaseVersion-1); err != nil {
		t.Fatalf("down 0023: %v", err)
	}
	if got := normalize(t, db, "mikh"); got != "miкh" {
		t.Errorf("после down normalize_text('mikh') = %q, ждали прежнее 'miкh'", got)
	}
	if bad := stale(t, db, cols); len(bad) != 0 {
		t.Fatalf("после down 0023 не пересчитаны: %q", bad)
	}
	if after := normalizedValues(t, db, cols); !slices.Equal(after, before) {
		t.Fatalf("down 0023 не вернул значения колонок:\nдо:    %q\nпосле: %q", before, after)
	}
	// хранимые ники обратно не переводятся (исходного текста нет): остаются новые и возвращаются старые сида 0010
	reservedDown := queryStrings(t, db, `SELECT nickname_normalized FROM reserved_nicknames ORDER BY 1`)
	for _, v := range append(slices.Clone(reservedBefore), wantReserved...) {
		if !slices.Contains(reservedDown, v) {
			t.Errorf("после down в reserved_nicknames нет %q", v)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("повторный up после отката 0023: %v", err)
	}
}

// Две строки, различавшиеся до 0023 только регистром (или диакритикой), после пересчёта совпадают: накат падает
// 23505 с именем уникального индекса, версия остаётся 22, функция прежняя (транзакция откатилась целиком).
// Строка вне предиката частичного индекса (архивный район, удалённый игрок) накату не мешает.
func Test0023UniqueConflict(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, normalizeCaseVersion-1); err != nil {
		t.Fatalf("down до %d: %v", normalizeCaseVersion-1, err)
	}
	mustExec(t, db, `INSERT INTO users (email, nickname, birth_date)
		VALUES ('c1@example.com', 'captain_one', '2000-01-01'), ('c2@example.com', 'captain_two', '2000-01-01')`)

	refused := func(index string) {
		t.Helper()
		_, err := p.UpTo(ctx, normalizeCaseVersion)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "23505" || pg.ConstraintName != index || !strings.Contains(pg.Message, index) {
			t.Fatalf("ждали отказ 23505 с именем %q, получили: %v", index, err)
		}
		if v, err := p.GetDBVersion(ctx); err != nil || v != normalizeCaseVersion-1 {
			t.Fatalf("версия после отказа = %d (%v), ждали %d", v, err, normalizeCaseVersion-1)
		}
		if got := normalize(t, db, "mikh"); got != "miкh" {
			t.Fatalf("после отказа normalize_text('mikh') = %q — функция заменена частично применённой миграцией", got)
		}
	}

	cases := []struct {
		index, conflict, fix string
	}{
		{"districts_city_name_active_key",
			`INSERT INTO districts (city_id, name) SELECT id, unnest(ARRAY['Bolshoy', 'bolshoy']) FROM cities WHERE slug = 'stavropol'`,
			`UPDATE districts SET archived_at = now() WHERE name = 'bolshoy'`},
		{"users_nickname_key",
			`INSERT INTO users (email, nickname, birth_date) VALUES ('t1@example.com', 'Tom', '2000-01-01'), ('t2@example.com', 'tom', '2000-01-01')`,
			`UPDATE users SET deleted_at = now() WHERE nickname = 'tom'`},
		{"teams_city_name_key",
			`INSERT INTO teams (city_id, name, display_name, slug, captain_id, primary_format, roster_limit)
			 SELECT c.id, v.name, v.name, v.slug, u.id, '5x5', 10
			   FROM cities c, (VALUES ('Café', 'cafe-1', 'captain_one'), ('CAFE', 'cafe-2', 'captain_two')) AS v(name, slug, captain)
			   JOIN users u ON u.nickname = v.captain
			  WHERE c.slug = 'stavropol'`,
			`UPDATE teams SET name = 'Cafe Two' WHERE slug = 'cafe-2'`},
	}
	for _, c := range cases {
		mustExec(t, db, c.conflict)
		refused(c.index)
		mustExec(t, db, c.fix)
	}
	if _, err := p.UpTo(ctx, normalizeCaseVersion); err != nil {
		t.Fatalf("up после исправления данных (вне предикатов частичных индексов): %v", err)
	}
}

// conflictCheckSQL — проверка стенда до наката 0023, дословно из docs/deploy-dev.md («До мержа миграции в main»):
// по каждому уникальному индексу на генерируемой *_normalized — число групп строк, которые после пересчёта совпадут
// (новое значение = translate старого, поэтому запрос идёт на версии 22). Только числа, без данных строк.
// Хранимые ники (PK reserved_nicknames, nickname_reservations) миграция схлопывает сама — их здесь нет.
const conflictCheckSQL = `SELECT 'users_nickname_key' AS idx, count(*) AS groups FROM (
  SELECT 1 FROM users WHERE deleted_at IS NULL
   GROUP BY translate(nickname_normalized, 'abcehkmoptxy', 'авсенкмортху') HAVING count(*) > 1) AS g
UNION ALL
SELECT 'teams_city_name_key', count(*) FROM (
  SELECT 1 FROM teams WHERE deleted_at IS NULL
   GROUP BY city_id, translate(name_normalized, 'abcehkmoptxy', 'авсенкмортху') HAVING count(*) > 1) AS g
UNION ALL
SELECT 'districts_city_name_active_key', count(*) FROM (
  SELECT 1 FROM districts WHERE archived_at IS NULL
   GROUP BY city_id, translate(name_normalized, 'abcehkmoptxy', 'авсенкмортху') HAVING count(*) > 1) AS g`

// Проверка стенда до наката (docs/deploy-dev.md, Task 25 плана B): тот же SQL на версии 22 находит каждую пару,
// которая уронит 0023, и ничего не находит без них; перечень индексов в нём — все уникальные на *_normalized.
func Test0023ConflictCheckSQL(t *testing.T) {
	doc, err := os.ReadFile("../../docs/deploy-dev.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ReplaceAll(string(doc), "\r\n", "\n"), conflictCheckSQL) {
		t.Fatal("в docs/deploy-dev.md нет проверки конфликтов 0023 дословно (conflictCheckSQL)")
	}
	db := dbtest.New(t)
	ctx := context.Background()
	unique := queryStrings(t, db, `
		SELECT DISTINCT i.indexrelid::regclass::text
		  FROM pg_index i
		  JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		  JOIN pg_class c ON c.oid = i.indrelid AND c.relnamespace = 'public'::regnamespace
		 WHERE i.indisunique AND a.attname LIKE '%\_normalized'
		 ORDER BY 1`)
	wantUnique := []string{"districts_city_name_active_key", "nickname_reservations_pkey", "reserved_nicknames_pkey",
		"teams_city_name_key", "users_nickname_key"}
	if !slices.Equal(unique, wantUnique) {
		t.Fatalf("уникальные индексы на *_normalized: %q, ждали %q — новый индекс нужен в conflictCheckSQL и 0023", unique, wantUnique)
	}
	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, normalizeCaseVersion-1); err != nil {
		t.Fatalf("down до %d: %v", normalizeCaseVersion-1, err)
	}
	check := func() []string {
		t.Helper()
		return queryStrings(t, db, `SELECT idx || ' ' || groups FROM (`+conflictCheckSQL+`) AS c ORDER BY 1`)
	}
	want := []string{"districts_city_name_active_key 0", "teams_city_name_key 0", "users_nickname_key 0"}
	if got := check(); !slices.Equal(got, want) {
		t.Fatalf("без конфликтов: %q, ждали %q", got, want)
	}
	mustExec(t, db, `INSERT INTO users (email, nickname, birth_date)
		VALUES ('c1@example.com', 'Tom', '2000-01-01'), ('c2@example.com', 'tom', '2000-01-01'),
		       ('c3@example.com', 'Mia', '2000-01-01'), ('c4@example.com', 'mia', '2000-01-01'),
		       ('c5@example.com', 'bob', '2000-01-01')`)
	mustExec(t, db, `UPDATE users SET deleted_at = now() WHERE nickname = 'bob'`)
	mustExec(t, db, `INSERT INTO users (email, nickname, birth_date) VALUES ('c6@example.com', 'Bob', '2000-01-01')`)
	mustExec(t, db, `INSERT INTO districts (city_id, name) SELECT id, unnest(ARRAY['Bolshoy', 'bolshoy']) FROM cities WHERE slug = 'stavropol'`)
	mustExec(t, db, `INSERT INTO teams (city_id, name, display_name, slug, captain_id, primary_format, roster_limit)
		SELECT c.id, v.name, v.name, v.slug, u.id, '5x5', 10
		  FROM cities c, (VALUES ('Café', 'cafe-1', 'Tom'), ('CAFE', 'cafe-2', 'Mia')) AS v(name, slug, captain)
		  JOIN users u ON u.nickname = v.captain
		 WHERE c.slug = 'stavropol'`)
	want = []string{"districts_city_name_active_key 1", "teams_city_name_key 1", "users_nickname_key 2"}
	if got := check(); !slices.Equal(got, want) {
		t.Fatalf("с конфликтами: %q, ждали %q", got, want)
	}
}
