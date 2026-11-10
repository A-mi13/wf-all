package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/geo/internal/store/geodb"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/perm"
)

// Ручная привязка города к месту GeoNames (Task 11, R14): та же привязка, что у сверки импорта.

// unlinked — город RU без geoname_id в регионе region (nil — без региона) с названиями names.
func (e actEnv) unlinked(t *testing.T, slug string, region *uuid.UUID, names map[string]string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO cities (country_id, region_id, name, slug, timezone, centroid, population)
		SELECT id, $1, $2, $3, 'Europe/Moscow', ST_SetSRID(ST_MakePoint(40, 50), 4326)::geography, 123
		FROM countries WHERE code = 'RU' RETURNING id`, region, names["ru"], slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for l, n := range names {
		e.exec(t, `INSERT INTO city_names (city_id, locale, name) VALUES ($1, $2, $3)`, id, l, n)
	}
	return id
}

// region — регион RU без кода admin1 с ru-названием.
func (e actEnv) region(t *testing.T, ru string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO regions (country_id, name) SELECT id, $1 FROM countries
		WHERE code = 'RU' RETURNING id`, ru).Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `INSERT INTO region_names (region_id, locale, name) VALUES ($1, 'ru', $2)`, id, ru)
	return id
}

func link(a *app.Admin, id uuid.UUID, gid int64) (geo.AdminCity, error) {
	return a.LinkCity(context.Background(), staffAs(perm.Admin), id, gid, "ru", approvals.Meta{})
}

// Успех: geoname_id, version + 1, недостающие переводы (en админа не затёрт), региону — код admin1 и переводы;
// точка и население города не меняются; событие и аудит в той же транзакции.
func TestLinkCity(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	ural := e.region(t, "Свердловская обл.")
	cid := e.unlinked(t, "mikh", &ural, map[string]string{"ru": "Михайловск", "en": "Mikhailovsk (admin)"})
	s := staffAs(perm.Admin)
	c, err := e.admin().LinkCity(context.Background(), s, cid, 526815, "en", approvals.Meta{})
	if err != nil || c.GeonameID == nil || *c.GeonameID != 526815 || c.Version != 2 || c.Population != 123 ||
		c.Names["en"] != "Mikhailovsk (admin)" || c.Names["ru"] != "Михайловск" || c.Location.Lon != 40 {
		t.Fatalf("%+v %v", c, err)
	}
	if n := e.count(t, `SELECT count(*) FROM regions WHERE id = $1 AND geoname_admin1_code = '71'`, ural); n != 1 {
		t.Fatal("код региона")
	}
	if n := e.count(t, `SELECT count(*) FROM region_names WHERE region_id = $1 AND locale = 'en' AND name = 'Sverdlovsk Oblast'`, ural); n != 1 {
		t.Fatal("перевод региона")
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.city_linked' AND aggregate_id = $1
		AND aggregate_version = 2 AND (payload->>'geoname_id')::bigint = 526815`, cid); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.city_linked' AND actor_staff_id = $1
		AND object_type = 'city' AND object_id = $2 AND scope_city_id = $2 AND (after->>'geoname_id')::bigint = 526815`,
		s.StaffID, cid); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
}

func TestLinkCityAlreadyLinked(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	if _, err := link(e.admin(), e.cityID(t, "stavropol"), 526815); !errors.Is(err, geo.ErrCityAlreadyLinked) {
		t.Fatalf("%v", err)
	}
	if _, err := link(e.admin(), uuid.New(), 526815); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("нет города: %v", err)
	}
}

// Места нет, оно пропало из источника или оно другой страны, чем город, — 404 geo.place_not_found.
func TestLinkCityPlaceNotFound(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	cid := e.unlinked(t, "zar", nil, map[string]string{"ru": "Заречный"})
	for _, gid := range []int64{999999, 700004, 700005} {
		if _, err := link(e.admin(), cid, gid); !errors.Is(err, geo.ErrPlaceNotFound) {
			t.Errorf("%d: %v", gid, err)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE id = $1 AND geoname_id IS NULL AND version = 1`, cid); n != 1 {
		t.Fatal("город изменён")
	}
}

// Место у другого города — 409 geo.city_already_active: и проверкой до записи, и 23505 cities_geoname_id_key, когда
// город на месте завели параллельно после проверки (хук «link» — активация того же места коммитится между ними).
func TestLinkCityPlaceTaken(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	cid := e.unlinked(t, "zar", nil, map[string]string{"ru": "Заречный"})
	if _, err := activate(e, e.admin(), app.Activation{GeonameID: 700001}); err != nil {
		t.Fatal(err)
	}
	if _, err := link(e.admin(), cid, 700001); !errors.Is(err, geo.ErrCityAlreadyActive) {
		t.Fatalf("проверка: %v", err)
	}
	a := e.admin()
	a.SetStageHook(func(ctx context.Context, stage string) {
		if stage == "link" {
			if _, err := e.admin().Activate(context.WithoutCancel(ctx), actor, app.Activation{GeonameID: 700002}, approvals.Meta{}); err != nil {
				t.Errorf("активация в хуке: %v", err)
			}
		}
	})
	if _, err := link(a, cid, 700002); !errors.Is(err, geo.ErrCityAlreadyActive) {
		t.Fatalf("гонка: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE id = $1 AND geoname_id IS NULL`, cid); n != 1 {
		t.Fatal("город привязан")
	}
}

// ⚠️ ревью G5 (I1-r1), по образцу TestActivateCrossCityImportLock: сверка привязала test-a-lesnoy (регион ural под
// её замком) и подходит к test-b-mikh; в этот момент mikh привязывают вручную к тому же месту. Ручная привязка ждёт
// разделяемый замок страны, сверка привязывает mikh и коммитит, ручная — 409 geo.city_already_linked; одна привязка,
// без 40P01. Без замка страны ручная взяла бы mikh и ждала регион сверки, а сверка — mikh (40P01).
func TestLinkCityRaceWithImport(t *testing.T) {
	t.Parallel()
	r := newImportRaceEnv(t, "test-b-mikh", func(e env, ural uuid.UUID) {
		addCity(t, e, ural, "test-a-lesnoy", map[string]string{"ru": "Лесной"})
	})
	var linkErr error
	done := make(chan struct{})
	im := r.importer(&r.raw)
	im.SetBeforeLink(func(ctx context.Context, cityID uuid.UUID, _ int64) {
		if cityID != r.mikh {
			return
		}
		go func() {
			defer close(done)
			_, linkErr = r.adm().LinkCity(context.WithoutCancel(ctx), staffAs(perm.Admin), r.mikh, 526815, "ru", approvals.Meta{})
		}()
		// не t.Fatal: колбэк внутри db.InTx держит соединение
		if !waitLockOrDone(t, r.owner, done) {
			t.Errorf("ручная привязка не ждала сверку: %v", linkErr)
		}
	})
	res, err := im.Import(context.Background(), "RU", nil, nil)
	<-done
	if isDeadlock(err) || isDeadlock(linkErr) {
		t.Fatalf("взаимная блокировка: импорт %v, привязка %v", err, linkErr)
	}
	if err != nil || !errors.Is(linkErr, geo.ErrCityAlreadyLinked) {
		t.Fatalf("импорт: %v, привязка: %v", err, linkErr)
	}
	linked := false
	for _, l := range res.Reconciled.Linked {
		linked = linked || l.CityID == r.mikh && l.GeonameID == 526815
	}
	if !linked {
		t.Fatalf("сверка: %+v", res.Reconciled)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM outbox WHERE event_type = 'geo.city_linked' AND aggregate_id = $1`, r.mikh); n != 1 {
		t.Fatalf("событий привязки %d", n)
	}
}

// Активация завела регион (RU, 71) и не закоммитила (хук «insert»); ручная привязка города другого региона к месту с
// admin1 71 ставит своему региону тот же код — ждёт активацию, после её коммита 23505 regions_country_admin1_key
// откатывает только код региона (savepoint): привязка успешна, регион без кода.
func TestLinkCityRegionRaceWithActivation(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	imp := uuid.New()
	e.exec(t, `INSERT INTO geonames_imports (id, country_code, status, started_at) VALUES ($1, 'RU', 'succeeded', $2)`, imp, tAct)
	e.place(t, imp, 700007, "RU", "71", "Asbest", "Asia/Yekaterinburg", false, nil)
	ural := e.region(t, "Урал")
	cid := e.unlinked(t, "mikh", &ural, map[string]string{"ru": "Михайловск"})

	at, release, actDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	adm := e.admin()
	adm.SetStageHook(func(_ context.Context, stage string) {
		if stage == "insert" {
			once.Do(func() { close(at); <-release })
		}
	})
	var actErr error
	go func() {
		defer close(actDone)
		_, actErr = activate(e, adm, app.Activation{GeonameID: 700007})
	}()
	<-at
	linkDone := make(chan struct{})
	var linkErr error
	var c geo.AdminCity
	go func() {
		defer close(linkDone)
		c, linkErr = link(e.admin(), cid, 526815)
	}()
	waited := waitLockOrDone(t, e.owner, linkDone)
	close(release)
	<-actDone
	<-linkDone
	if !waited || actErr != nil || linkErr != nil || c.GeonameID == nil {
		t.Fatalf("ждала %v, активация: %v, привязка: %v", waited, actErr, linkErr)
	}
	if n := e.count(t, `SELECT count(*) FROM regions WHERE id = $1 AND geoname_admin1_code IS NULL`, ural); n != 1 {
		t.Fatal("код региона")
	}
	// ревью G6 I1: код 71 отдан новому региону активации — переводы admin1 71 «Уралу» не пишутся
	if n := e.count(t, `SELECT count(*) FROM region_names WHERE region_id = $1 AND locale <> 'ru'`, ural); n != 0 {
		t.Fatalf("переводы чужого admin1 у региона: %d", n)
	}
}

// Ревью G6 I1: код admin1 и переводы региона — только своего admin1.
//   - место с admin1 «00» («неизвестен» в GeoNames) — региону код не ставится (как planRegion активации, R13), и
//     сверка не находит кандидатами тёзок с неизвестным admin1 по всей стране;
//   - у региона уже другой код — переводы admin1 места ему не пишутся.
func TestLinkCityRegionCodeOnlyOwnAdmin1(t *testing.T) {
	t.Parallel()
	t.Run("admin1 00", func(t *testing.T) {
		t.Parallel()
		e := newActEnv(t)
		ural := e.region(t, "Урал")
		cid := e.unlinked(t, "zar", &ural, map[string]string{"ru": "Заречный"})
		if c, err := link(e.admin(), cid, 700001); err != nil || c.GeonameID == nil || *c.GeonameID != 700001 {
			t.Fatalf("%+v %v", c, err)
		}
		if n := e.count(t, `SELECT count(*) FROM regions WHERE id = $1 AND geoname_admin1_code IS NULL`, ural); n != 1 {
			t.Fatal("регион получил код «00»")
		}
		if n := e.count(t, `SELECT count(*) FROM regions WHERE geoname_admin1_code = '00'`); n != 0 {
			t.Fatalf("регионов с кодом «00»: %d", n)
		}
		// тёзка 700002 (admin1 «00») не кандидат города «Урала» с тем же именем
		other := e.unlinked(t, "zar-2", &ural, map[string]string{"ru": "Zarechnyy"})
		cands, err := geodb.New(e.owner).ImportCityCandidates(context.Background(),
			geodb.ImportCityCandidatesParams{CityID: other, CountryCode: "RU"})
		if err != nil || len(cands) != 0 {
			t.Fatalf("кандидаты сверки: %+v %v", cands, err)
		}
	})
	t.Run("регион с другим кодом", func(t *testing.T) {
		t.Parallel()
		e := newActEnv(t)
		var tyumen uuid.UUID
		if err := e.owner.QueryRow(context.Background(), `INSERT INTO regions (country_id, name, geoname_admin1_code)
			SELECT id, 'Тюменская область', '72' FROM countries WHERE code = 'RU' RETURNING id`).Scan(&tyumen); err != nil {
			t.Fatal(err)
		}
		e.exec(t, `INSERT INTO region_names (region_id, locale, name) VALUES ($1, 'ru', 'Тюменская область')`, tyumen)
		cid := e.unlinked(t, "mikh", &tyumen, map[string]string{"ru": "Михайловск"})
		if _, err := link(e.admin(), cid, 526815); err != nil {
			t.Fatal(err)
		}
		if n := e.count(t, `SELECT count(*) FROM regions WHERE id = $1 AND geoname_admin1_code = '72'`, tyumen); n != 1 {
			t.Fatal("код региона изменён")
		}
		if n := e.count(t, `SELECT count(*) FROM region_names WHERE region_id = $1`, tyumen); n != 1 {
			t.Fatalf("переводы admin1 71 у региона 72: %d строк названий", n)
		}
	})
}

// Ревью G6 I2: активация и ручная привязка ОДНОГО места 526815 (admin1 71, региона 71 нет). Активация завела регион
// (RU, 71) и стоит перед вставкой города (хук «insert»); привязка города «Урала» без кода к тому же месту ждёт её.
// Порядок индексов у обоих — регион (regions_country_admin1_key), затем место (cities_geoname_id_key): без цикла,
// активация успешна, привязка — 409 geo.city_already_active, не 40P01. При встречном порядке (место привязки раньше
// кода региона) привязка держала бы geoname_id = 526815 и ждала регион, активация — её geoname_id (40P01 → 500).
func TestLinkCitySamePlaceRaceWithActivation(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	ural := e.region(t, "Урал")
	cid := e.unlinked(t, "mikh", &ural, map[string]string{"ru": "Михайловск"})

	at, release, actDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	adm := e.admin()
	adm.SetStageHook(func(_ context.Context, stage string) {
		if stage == "insert" {
			once.Do(func() { close(at); <-release })
		}
	})
	var actErr error
	go func() {
		defer close(actDone)
		_, actErr = activate(e, adm, app.Activation{GeonameID: 526815})
	}()
	if !recvWithin(t, at, 15*time.Second) {
		close(release)
		t.Fatalf("активация не дошла до вставки города: %v", actErr)
	}
	linkDone := make(chan struct{})
	var linkErr error
	go func() {
		defer close(linkDone)
		_, linkErr = link(e.admin(), cid, 526815)
	}()
	waited := waitLockOrDoneWithin(t, e.owner, linkDone, 15*time.Second)
	close(release)
	if !recvWithin(t, actDone, 30*time.Second) || !recvWithin(t, linkDone, 30*time.Second) {
		t.Fatal("активация или привязка не завершились")
	}
	if isDeadlock(actErr) || isDeadlock(linkErr) {
		t.Fatalf("взаимная блокировка: активация %v, привязка %v", actErr, linkErr)
	}
	if !waited || actErr != nil || !errors.Is(linkErr, geo.ErrCityAlreadyActive) {
		t.Fatalf("ждала %v, активация: %v, привязка: %v", waited, actErr, linkErr)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE id = $1 AND geoname_id IS NULL AND version = 1`, cid); n != 1 {
		t.Fatal("город привязки изменён")
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE geoname_id = 526815`); n != 1 {
		t.Fatalf("городов на месте: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM regions WHERE id = $1 AND geoname_admin1_code IS NULL`, ural); n != 1 {
		t.Fatal("код региона")
	}
}

// Ревью G6 n1 — обратный порядок гонки I2: привязка «mikh» к месту 526815 записала код 71 региону «Урал» и
// geoname_id городу и стоит до коммита (точка linked); активация того же места ждёт её (ON CONFLICT региона), затем
// упирается в cities_geoname_id_key: привязка 200, активация 409 geo.city_already_active, без 40P01.
func TestLinkCityFirstThenActivationSamePlace(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	ural := e.region(t, "Урал")
	cid := e.unlinked(t, "mikh", &ural, map[string]string{"ru": "Михайловск"})

	at, release, linkDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	adm := e.admin()
	adm.SetStageHook(func(_ context.Context, stage string) {
		if stage == "linked" {
			once.Do(func() { close(at); <-release })
		}
	})
	var linkErr error
	go func() {
		defer close(linkDone)
		_, linkErr = link(adm, cid, 526815)
	}()
	if !recvWithin(t, at, 15*time.Second) {
		close(release)
		t.Fatalf("привязка не дошла до точки linked: %v", linkErr)
	}
	actDone := make(chan struct{})
	var actErr error
	go func() {
		defer close(actDone)
		_, actErr = activate(e, e.admin(), app.Activation{GeonameID: 526815})
	}()
	waited := waitLockOrDoneWithin(t, e.owner, actDone, 15*time.Second)
	close(release)
	if !recvWithin(t, linkDone, 30*time.Second) || !recvWithin(t, actDone, 30*time.Second) {
		t.Fatal("привязка или активация не завершились")
	}
	if isDeadlock(actErr) || isDeadlock(linkErr) {
		t.Fatalf("взаимная блокировка: активация %v, привязка %v", actErr, linkErr)
	}
	if !waited || linkErr != nil || !errors.Is(actErr, geo.ErrCityAlreadyActive) {
		t.Fatalf("ждала %v, привязка: %v, активация: %v", waited, linkErr, actErr)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE geoname_id = 526815 AND id = $1`, cid); n != 1 {
		t.Fatal("место не у города привязки")
	}
	if n := e.count(t, `SELECT count(*) FROM regions WHERE geoname_admin1_code = '71'`); n != 1 {
		t.Fatalf("регионов с admin1 71: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM regions WHERE id = $1 AND geoname_admin1_code = '71'`, ural); n != 1 {
		t.Fatal("код 71 не у «Урала»")
	}
}

// recvWithin — ждать закрытия ch не дольше d (ревью G6 m5: барьер не висит до -timeout пакета).
func recvWithin(t *testing.T, ch <-chan struct{}, d time.Duration) bool {
	t.Helper()
	select {
	case <-ch:
		return true
	case <-time.After(d):
		t.Errorf("канал не закрыт за %s", d)
		return false
	}
}
