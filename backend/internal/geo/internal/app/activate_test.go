package app_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Активация города (Task 10): база на тест под ролью admin (запись), часы теста. Источник: импорт RU с admin1
// 70 «Ставропольский край» (есть в сиде) и 71 «Свердловская область» (региона нет), места:
//   - 526815 Михайловск (71, ru/en);
//   - 700001, 700002 Zarechnyy (admin1 «00», только ascii) — тёзки для гонки slug;
//   - 700003 — ascii из одних выброшенных Slugify символов; 700004 — пропал из источника (missing_since);
//   - 700005 — место выключенной страны XA; 700006 — негодная таймзона (нарушенный инвариант импорта).

var tAct = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

type actEnv struct {
	owner, as *pgxpool.Pool
	clk       *clocktest.Fake
}

func newActEnv(t *testing.T) actEnv {
	t.Helper()
	p := dbtest.NewPoolsAs(t, "admin")
	e := actEnv{owner: p.Owner, as: p.As, clk: clocktest.New(tAct)}
	imp := uuid.New()
	e.exec(t, `INSERT INTO countries (code, name, currency, default_locale, phone_prefix, week_starts_on, min_signup_age,
	                                  age_of_majority, is_enabled)
	           VALUES ('XA', 'Тестовия', 'XXX', 'ru', '+999', 1, 14, 18, false),
	                  ('XB', 'Testland', 'XXX', 'en', '+998', 1, 14, 18, true)`)
	e.exec(t, `INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at) VALUES ($1, 'RU', 'succeeded', $2, $2)`, imp, tAct)
	e.exec(t, `INSERT INTO geonames_admin1 (country_code, admin1_code, geoname_id, ascii_name)
	           VALUES ('RU', '70', 500070, 'Stavropol Kray'), ('RU', '71', 500071, 'Sverdlovsk Oblast')`)
	e.exec(t, `INSERT INTO geonames_admin1_names (country_code, admin1_code, locale, name)
	           VALUES ('RU', '71', 'ru', 'Свердловская область'), ('RU', '71', 'en', 'Sverdlovsk Oblast')`)
	e.place(t, imp, 526815, "RU", "71", "Mikhaylovsk", "Europe/Moscow", false, map[string]string{"ru": "Михайловск", "en": "Mikhaylovsk"})
	e.place(t, imp, 700001, "RU", "00", "Zarechnyy", "Europe/Moscow", false, nil)
	e.place(t, imp, 700002, "RU", "00", "Zarechnyy", "Europe/Moscow", false, nil)
	e.place(t, imp, 700003, "RU", "", "ʻ''", "Europe/Moscow", false, map[string]string{"ru": "Аин"})
	e.place(t, imp, 700004, "RU", "70", "Propal", "Europe/Moscow", true, nil)
	e.place(t, imp, 700005, "XA", "", "Skrytsk", "Europe/Moscow", false, nil)
	e.place(t, imp, 700006, "RU", "70", "Badzone", "Mars/Phobos", false, nil)
	return e
}

func (e actEnv) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.owner.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (e actEnv) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.owner.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (e actEnv) place(t *testing.T, imp uuid.UUID, gid int64, country, admin1, ascii, tz string, missing bool, names map[string]string) {
	t.Helper()
	var miss *time.Time
	if missing {
		miss = &tAct
	}
	e.exec(t, `INSERT INTO geonames_places (geoname_id, country_code, admin1_code, name, ascii_name, feature_code,
	                                        population, location, timezone, missing_since, import_id)
	           VALUES ($1, $2, NULLIF($3::text, ''), $4, $4, 'PPL', 9000, ST_SetSRID(ST_MakePoint(60.7, 56.9), 4326)::geography,
	                   $5, $6, $7)`, gid, country, admin1, ascii, tz, miss, imp)
	for l, n := range names {
		e.exec(t, `INSERT INTO geonames_place_names (geoname_id, locale, name) VALUES ($1, $2, $3)`, gid, l, n)
	}
}

// occupy — город RU со slug (занимает адрес), без места.
func (e actEnv) occupy(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	var cid uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `
		INSERT INTO cities (country_id, name, slug, timezone, status, centroid)
		SELECT id, $1, $1, 'Europe/Moscow', 'waitlist', ST_SetSRID(ST_MakePoint(40, 50), 4326)::geography
		FROM countries WHERE code = 'RU' RETURNING id`, slug).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	return cid
}

func (e actEnv) admin() *app.Admin {
	return app.NewAdmin(app.AdminDeps{Pool: e.as, Clock: e.clk, Log: slog.New(slog.DiscardHandler)})
}

var actor = &auth.Staff{StaffID: uuid.MustParse("0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c71"), Role: "admin", Label: "Ольга В."}

func activate(_ actEnv, a *app.Admin, in app.Activation) (geo.AdminCity, error) {
	return a.Activate(context.Background(), actor, in, approvals.Meta{})
}

// Место 526815 (регион 71 нет в справочнике): город waitlist, регион заведён с кодом 71 и названиями ru/en, slug
// с суффиксом региона (свой mikhaylovsk занят), названия ru/en, время — часов сценария; аудит сотрудника с
// городом записи и событие — в той же транзакции.
func TestActivateFromPlace(t *testing.T) {
	e := newActEnv(t)
	e.occupy(t, "mikhaylovsk")
	c, err := activate(e, e.admin(), app.Activation{GeonameID: 526815, Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Slug != "mikhaylovsk-sverdlovsk-oblast" || c.Status != geo.StatusWaitlist || c.Name != "Mikhaylovsk" ||
		c.Names["ru"] != "Михайловск" || c.Names["en"] != "Mikhaylovsk" || c.Version != 1 || c.Population != 9000 ||
		c.GeonameID == nil || *c.GeonameID != 526815 || c.Timezone != "Europe/Moscow" || c.Region == nil ||
		c.Region.Name != "Sverdlovsk Oblast" || c.Country.Code != "RU" || c.CanonicalLocale != "ru" ||
		c.Location.Lat < 56.8 || c.Location.Lon < 60.6 {
		t.Fatalf("карточка: %+v", c)
	}
	var name, admin1 string
	var created time.Time
	if err := e.owner.QueryRow(context.Background(), `SELECT r.name, r.geoname_admin1_code, c.created_at
		FROM cities c JOIN regions r ON r.id = c.region_id WHERE c.id = $1`, c.ID).Scan(&name, &admin1, &created); err != nil {
		t.Fatal(err)
	}
	if name != "Свердловская область" || admin1 != "71" || !created.Equal(tAct) {
		t.Fatalf("регион %q %q, created_at %v", name, admin1, created)
	}
	if n := e.count(t, `SELECT count(*) FROM region_names WHERE region_id = $1`, c.Region.ID); n != 2 {
		t.Fatalf("названий региона %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE id = $1 AND name = 'Михайловск'`, c.ID); n != 1 {
		t.Fatal("каноническое имя — не на языке страны")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.city_activated' AND actor_staff_id = $1
		AND actor_role = 'admin' AND actor_label = 'Ольга В.' AND object_type = 'city' AND object_id = $2
		AND scope_city_id = $2 AND after->>'slug' = 'mikhaylovsk-sverdlovsk-oblast' AND after->>'status' = 'waitlist'
		AND (after->>'geoname_id')::bigint = 526815 AND after->>'country_code' = 'RU' AND after->>'region_id' = $3`,
		actor.StaffID, c.ID, c.Region.ID.String()); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.city_activated' AND aggregate_id = $1
		AND aggregate_version = 1 AND payload->>'status' = 'waitlist'`, c.ID); n != 1 {
		t.Fatalf("событие: %d", n)
	}
}

// Переопределённая страна без региона — регион не ставится, названия — язык страны (en); region_id чужой страны
// или несуществующий — geo.ErrRegionNotFound; region_id своей страны — он; статус из запроса.
func TestActivateOverrideCountry(t *testing.T) {
	e := newActEnv(t)
	// у XB тоже есть admin1 «71»: регион по коду места при переопределённой стране был бы чужим
	e.exec(t, `INSERT INTO geonames_admin1 (country_code, admin1_code, geoname_id, ascii_name) VALUES ('XB', '71', 600071, 'Xb Province')`)
	a := e.admin()
	c, err := activate(e, a, app.Activation{GeonameID: 526815, CountryCode: "xb", Status: geo.StatusPilot})
	if err != nil || c.Region != nil || c.Country.Code != "XB" || c.Name != "Mikhaylovsk" || len(c.Names) != 1 ||
		c.CanonicalLocale != "en" || c.Status != geo.StatusPilot {
		t.Fatalf("XB без региона: %+v %v", c, err)
	}
	var stavRegion uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM regions WHERE geoname_admin1_code = '70'`).Scan(&stavRegion); err != nil {
		t.Fatal(err)
	}
	for _, rid := range []uuid.UUID{stavRegion, uuid.New()} {
		if _, err := activate(e, a, app.Activation{GeonameID: 700001, CountryCode: "XB", RegionID: &rid}); !errors.Is(err, geo.ErrRegionNotFound) {
			t.Fatalf("регион %s у XB: %v", rid, err)
		}
	}
	c, err = activate(e, a, app.Activation{GeonameID: 700001, RegionID: &stavRegion})
	if err != nil || c.Region == nil || c.Region.ID != stavRegion || c.Slug != "zarechnyy" {
		t.Fatalf("регион своей страны: %+v %v", c, err)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE geoname_id = 700001`); n != 1 {
		t.Fatalf("откат после отказа: %d", n)
	}
}

// admin1 «00» и пустой — без региона; ascii из одних выброшенных символов — city-<geoname_id>.
func TestActivateEmptySlugify(t *testing.T) {
	e := newActEnv(t)
	c, err := activate(e, e.admin(), app.Activation{GeonameID: 700003})
	if err != nil || c.Slug != "city-700003" || c.Region != nil || c.Names["ru"] != "Аин" || c.Names["en"] != "ʻ''" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = activate(e, e.admin(), app.Activation{GeonameID: 700002})
	if err != nil || c.Region != nil || c.Names["ru"] != "Zarechnyy" {
		t.Fatalf("admin1 00: %+v %v", c, err)
	}
}

// Страна выключена или её нет — geo.ErrCountryNotEnabled; место пропало или его нет — geo.ErrPlaceNotFound; ничего
// не записано.
func TestActivateCountryDisabled(t *testing.T) {
	e := newActEnv(t)
	a := e.admin()
	for _, in := range []app.Activation{{GeonameID: 700005}, {GeonameID: 526815, CountryCode: "XA"}, {GeonameID: 526815, CountryCode: "XZ"}} {
		if _, err := activate(e, a, in); !errors.Is(err, geo.ErrCountryNotEnabled) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM regions WHERE geoname_admin1_code = '71'`); n != 0 {
		t.Fatalf("регион записан при отказе: %d", n)
	}
}

func TestActivateMissingPlace(t *testing.T) {
	e := newActEnv(t)
	for _, gid := range []int64{700004, 999999} {
		if _, err := activate(e, e.admin(), app.Activation{GeonameID: gid}); !errors.Is(err, geo.ErrPlaceNotFound) {
			t.Errorf("%d: %v", gid, err)
		}
	}
}

// Повтор по тому же geoname_id — 409 до записи (проверка «на месте уже город»).
func TestActivateAlreadyActive(t *testing.T) {
	e := newActEnv(t)
	a := e.admin()
	if _, err := activate(e, a, app.Activation{GeonameID: 526815}); err != nil {
		t.Fatal(err)
	}
	if _, err := activate(e, a, app.Activation{GeonameID: 526815}); !errors.Is(err, geo.ErrCityAlreadyActive) {
		t.Fatalf("повтор: %v", err)
	}
}

// Негодная таймзона места — нарушенный инвариант импорта: ошибка (500), ничего не записано.
func TestActivateBadTimezone(t *testing.T) {
	e := newActEnv(t)
	_, err := activate(e, e.admin(), app.Activation{GeonameID: 700006})
	if err == nil || errors.Is(err, geo.ErrPlaceNotFound) {
		t.Fatalf("ждали внутреннюю ошибку: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE geoname_id = 700006`); n != 0 {
		t.Fatal("город записан")
	}
}

// Адрес занят в истории другого города — следующий кандидат; все 20 кандидатов заняты — ошибка без записи.
func TestActivateSlugFromHistory(t *testing.T) {
	e := newActEnv(t)
	other := e.occupy(t, "zarechnyy-old")
	e.exec(t, `INSERT INTO city_slug_history (slug, city_id, replaced_at) VALUES ('zarechnyy', $1, $2)`, other, tAct)
	c, err := activate(e, e.admin(), app.Activation{GeonameID: 700001})
	if err != nil || c.Slug != "zarechnyy-2" {
		t.Fatalf("%+v %v", c, err)
	}
	// 700002: заняты zarechnyy (история), zarechnyy-2 (город), -3 … -20 — исчерпано
	for i := 3; i <= 20; i++ {
		e.occupy(t, fmt.Sprintf("zarechnyy-%d", i))
	}
	if _, err := activate(e, e.admin(), app.Activation{GeonameID: 700002}); err == nil || errors.Is(err, geo.ErrCityAlreadyActive) {
		t.Fatalf("исчерпано: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE geoname_id = 700002`); n != 0 {
		t.Fatal("город записан")
	}
}

// race — вторая активация (b) проходит проверки, на стадии stage ждёт, пока первая (a) закоммитит, и продолжает:
// барьер каналами, без sleep. Возвращает ошибки a и b.
func race(t *testing.T, e actEnv, stage string, a, b app.Activation) (geo.AdminCity, error, geo.AdminCity, error) {
	t.Helper()
	at, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	second := e.admin()
	second.SetStageHook(func(_ context.Context, s string) {
		if s != stage {
			return
		}
		once.Do(func() {
			close(at)
			<-done
		})
	})
	var (
		cb   geo.AdminCity
		errB error
		wg   sync.WaitGroup
	)
	wg.Go(func() { cb, errB = activate(e, second, b) })
	<-at
	ca, errA := activate(e, e.admin(), a)
	close(done)
	wg.Wait()
	return ca, errA, cb, errB
}

// Две активации одного места: вторая прошла проверку «места нет», ждёт, пока первая закоммитит, и получает
// 23505 cities_geoname_id_key → geo.ErrCityAlreadyActive; в базе один город, одно событие, один аудит.
func TestActivateConcurrent(t *testing.T) {
	e := newActEnv(t)
	in := app.Activation{GeonameID: 700001}
	_, errA, _, errB := race(t, e, "insert", in, in)
	if errA != nil || !errors.Is(errB, geo.ErrCityAlreadyActive) {
		t.Fatalf("a: %v, b: %v", errA, errB)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE geoname_id = 700001`); n != 1 {
		t.Fatalf("городов %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.city_activated'`); n != 1 {
		t.Fatalf("событий %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.city_activated'`); n != 1 {
		t.Fatalf("аудитов %d", n)
	}
}

// Две активации тёзок (одинаковый ascii): вторая видела адрес свободным, первая заняла его и закоммитила —
// 23505 cities_slug_key откатывает только savepoint вставки, вторая берёт следующий кандидат; обе успешны.
func TestActivateSlugRace(t *testing.T) {
	e := newActEnv(t)
	ca, errA, cb, errB := race(t, e, "insert", app.Activation{GeonameID: 700001}, app.Activation{GeonameID: 700002})
	if errA != nil || errB != nil || ca.Slug != "zarechnyy" || cb.Slug != "zarechnyy-2" {
		t.Fatalf("a: %s %v, b: %s %v", ca.Slug, errA, cb.Slug, errB)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.city_activated'`); n != 2 {
		t.Fatalf("событий %d", n)
	}
}

// Две активации мест одного нового региона: вторая проверила, что региона нет, первая завела его и закоммитила —
// вторая не падает на regions_country_admin1_key, а берёт тот же регион; оба города в одном регионе.
func TestActivateRegionRace(t *testing.T) {
	e := newActEnv(t)
	imp := uuid.New()
	e.exec(t, `INSERT INTO geonames_imports (id, country_code, status, started_at) VALUES ($1, 'RU', 'succeeded', $2)`, imp, tAct)
	e.place(t, imp, 700007, "RU", "71", "Asbest", "Asia/Yekaterinburg", false, nil)
	ca, errA, cb, errB := race(t, e, "region", app.Activation{GeonameID: 526815}, app.Activation{GeonameID: 700007})
	if errA != nil || errB != nil || ca.Region == nil || cb.Region == nil || ca.Region.ID != cb.Region.ID {
		t.Fatalf("a: %+v %v, b: %+v %v", ca.Region, errA, cb.Region, errB)
	}
	if n := e.count(t, `SELECT count(*) FROM regions WHERE geoname_admin1_code = '71'`); n != 1 {
		t.Fatalf("регионов 71: %d", n)
	}
}

// Импорт держит транзакцию сверки (хук перед привязкой города к месту 526815), а админ в это время активирует то
// же место: активация не ждёт импорт и успешна (место видно — оно из прошлого импорта), привязка импорта ловит
// 23505 и уходит в conflict своим savepoint (R35), импорт успешен — взаимной блокировки нет. Обратный порядок
// (импорт уже привязал, активация ждёт его коммита и получает 23505 → 409) детерминированно без хука «после
// привязки» не воспроизвести; его исход — тот же перевод 23505 cities_geoname_id_key, что в TestActivateConcurrent.
func TestActivateDuringImportLink(t *testing.T) {
	e := newEnv(t) // импорт — под ролью worker
	ctx := context.Background()
	raw := fixture(t)
	if _, err := e.importer(&raw).Import(ctx, "RU", nil, nil); err != nil {
		t.Fatal(err)
	}
	ural := addRegion(t, e, "Свердловская область")
	mikh := addCity(t, e, ural, "test-mikhaylovsk-ural", map[string]string{"ru": "Михайловск"})
	cfg := e.owner.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE admin")
		return err
	}
	adminPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	adm := app.NewAdmin(app.AdminDeps{Pool: adminPool, Clock: e.clk, Log: slog.New(slog.DiscardHandler)})
	var activated geo.AdminCity
	var actErr error
	im := e.importer(&raw)
	im.SetBeforeLink(func(_ context.Context, cityID uuid.UUID, gid int64) {
		if cityID == mikh && gid == 526815 {
			// не t.Fatal: колбэк внутри db.InTx держит соединение
			activated, actErr = adm.Activate(context.Background(), actor, app.Activation{GeonameID: gid}, approvals.Meta{})
		}
	})
	res, err := im.Import(ctx, "RU", nil, nil)
	if err != nil || actErr != nil {
		t.Fatalf("импорт: %v, активация: %v", err, actErr)
	}
	if len(res.Reconciled.Conflict) != 1 || res.Reconciled.Conflict[0].CityID != mikh {
		t.Fatalf("конфликт: %+v", res.Reconciled)
	}
	var owner uuid.UUID
	if err := e.owner.QueryRow(ctx, `SELECT id FROM cities WHERE geoname_id = 526815`).Scan(&owner); err != nil || owner != activated.ID {
		t.Fatalf("место 526815 у %s, активирован %s: %v", owner, activated.ID, err)
	}
}
