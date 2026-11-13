package app_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// Правка города (Task 11): база на тест под ролью admin (actEnv), город сида «stavropol» (pilot, RU, ru/en) и
// «mihaylovsk». Права — матрица perm по роли (как загрузчик сессии).

func staffAs(role perm.Role, cities ...uuid.UUID) *auth.Staff {
	return &auth.Staff{StaffID: uuid.New(), SessionID: uuid.New(), Role: string(role), CityIDs: cities,
		Stage: auth.StageActive, Grants: perm.Grants(role), Label: "Сотрудник " + string(role), Version: 1}
}

func (e actEnv) cityID(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM cities WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("город %s: %v", slug, err)
	}
	return id
}

func (e actEnv) cityVersion(t *testing.T, id uuid.UUID) int64 {
	t.Helper()
	var v int64
	if err := e.owner.QueryRow(context.Background(), `SELECT version FROM cities WHERE id = $1`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func update(a *app.Admin, s *auth.Staff, id uuid.UUID, p app.CityPatch) (geo.AdminCity, error) {
	return a.UpdateCity(context.Background(), s, id, p, "ru", approvals.Meta{})
}

// problem — статус, код и поля ProblemError; не ProblemError — 0.
func problem(err error) (int, string, []httpx.FieldError) {
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !ok {
		return 0, "", nil
	}
	p := pe.Problem()
	return p.Status, p.Code, p.Errors
}

func wantField(t *testing.T, err error, field, rule string) {
	t.Helper()
	status, code, fields := problem(err)
	if status != http.StatusBadRequest || code != httpx.CodeValidationFailed || len(fields) != 1 ||
		fields[0].Field != field || fields[0].Code != rule {
		t.Fatalf("ждали 400 %s %s: %v (%d %s %v)", field, rule, err, status, code, fields)
	}
}

func wantForbidden(t *testing.T, err error) {
	t.Helper()
	if status, code, _ := problem(err); status != http.StatusForbidden || code != httpx.CodeForbidden {
		t.Fatalf("ждали 403 auth.forbidden: %v", err)
	}
}

func strp(s string) *string { return &s }

func statusp(s geo.Status) *geo.Status { return &s }

// Статус — любой переход: событие geo.city_status_changed (from, to) с версией v+1, аудит geo.city_updated с
// изменённым полем и городом записи; ответ — карточка после правки.
func TestUpdateCityStatus(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	v := e.cityVersion(t, stav)
	s := staffAs(perm.Admin)
	c, err := update(e.admin(), s, stav, app.CityPatch{Status: statusp(geo.StatusLive)})
	if err != nil || c.Status != geo.StatusLive || c.Version != v+1 {
		t.Fatalf("%+v %v", c, err)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.city_status_changed' AND aggregate_id = $1
		AND aggregate_type = 'city' AND aggregate_version = $2 AND payload->>'from' = 'pilot' AND payload->>'to' = 'live'
		AND payload->>'city_id' = $3`, stav, v+1, stav.String()); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.city_updated' AND actor_staff_id = $1
		AND actor_role = 'admin' AND actor_label = $2 AND object_type = 'city' AND object_id = $3 AND scope_city_id = $3
		AND before = '{"status":"pilot"}'::jsonb AND after = '{"status":"live"}'::jsonb`, s.StaffID, s.Label, stav); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
}

// Новый slug: прежний — в историю с временем часов сценария, публичный поиск по старому адресу находит город;
// событие geo.city_slug_changed (old_slug, new_slug).
func TestUpdateCitySlug(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	c, err := update(e.admin(), staffAs(perm.Owner), stav, app.CityPatch{Slug: strp("stavropol-krai")})
	if err != nil || c.Slug != "stavropol-krai" || len(c.SlugHistory) != 1 || c.SlugHistory[0].Slug != "stavropol" ||
		!c.SlugHistory[0].ReplacedAt.Equal(tAct) {
		t.Fatalf("%+v %v", c, err)
	}
	old, err := geo.New(e.as).CityBySlug(context.Background(), "stavropol", "ru")
	if err != nil || old.ID != stav {
		t.Fatalf("публичный поиск по старому адресу: %+v %v", old, err)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.city_slug_changed' AND aggregate_id = $1
		AND payload->>'old_slug' = 'stavropol' AND payload->>'new_slug' = 'stavropol-krai'`, stav); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	// неверный формат — 400 по полю (схема ловит раньше; сценарий — защита в глубину)
	_, err = update(e.admin(), staffAs(perm.Owner), stav, app.CityPatch{Slug: strp("Bad--slug")})
	wantField(t, err, "body.slug", "pattern")
}

// Адрес — текущий slug другого города: 409 geo.slug_taken, ничего не записано.
func TestUpdateCitySlugTaken(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	if _, err := update(e.admin(), staffAs(perm.Admin), stav, app.CityPatch{Slug: strp("mihaylovsk")}); !errors.Is(err, geo.ErrSlugTaken) {
		t.Fatalf("ждали geo.ErrSlugTaken: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM city_slug_history`); n != 0 {
		t.Fatalf("история записана: %d", n)
	}
}

// Адрес — в истории другого города: 409 geo.slug_taken (старые ссылки того города не перехватываются).
func TestUpdateCitySlugTakenByHistory(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav, mih := e.cityID(t, "stavropol"), e.cityID(t, "mihaylovsk")
	e.exec(t, `INSERT INTO city_slug_history (slug, city_id, replaced_at) VALUES ('mikhaylovsk-old', $1, $2)`, mih, tAct)
	if _, err := update(e.admin(), staffAs(perm.Admin), stav, app.CityPatch{Slug: strp("mikhaylovsk-old")}); !errors.Is(err, geo.ErrSlugTaken) {
		t.Fatalf("ждали geo.ErrSlugTaken: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE id = $1 AND slug = 'stavropol'`, stav); n != 1 {
		t.Fatal("slug изменён")
	}
}

// Возврат к своему старому адресу: строка истории удалена, прежний текущий ушёл в историю.
func TestUpdateCitySlugBackToOwnOld(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	a := e.admin()
	if _, err := update(a, staffAs(perm.Admin), stav, app.CityPatch{Slug: strp("stav")}); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(time.Hour)
	c, err := update(a, staffAs(perm.Admin), stav, app.CityPatch{Slug: strp("stavropol")})
	if err != nil || c.Slug != "stavropol" || len(c.SlugHistory) != 1 || c.SlugHistory[0].Slug != "stav" ||
		!c.SlugHistory[0].ReplacedAt.Equal(tAct.Add(time.Hour)) {
		t.Fatalf("%+v %v", c, err)
	}
	if n := e.count(t, `SELECT count(*) FROM city_slug_history WHERE slug = 'stavropol'`); n != 0 {
		t.Fatal("свой старый адрес остался в истории")
	}
}

// Строка языка страны переписывает cities.name; перевод en удаляется null; удалить строку языка страны — 400
// body.names.ru required, ничего не записано.
func TestUpdateCityNamesCanonical(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	a := e.admin()
	v := e.cityVersion(t, stav)
	c, err := update(a, staffAs(perm.Admin), stav, app.CityPatch{Names: map[string]*string{"ru": strp("  Ставрополь  Новый "), "en": nil}})
	if err != nil || c.Names["ru"] != "Ставрополь Новый" || len(c.Names) != 1 || c.Version != v+1 {
		t.Fatalf("%+v %v", c, err)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE id = $1 AND name = 'Ставрополь Новый'`, stav); n != 1 {
		t.Fatal("cities.name не переписан строкой языка страны")
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.city_renamed' AND aggregate_id = $1
		AND payload->'locales' = '["en","ru"]'::jsonb AND aggregate_version = $2`, stav, v+1); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.city_updated' AND object_id = $1
		AND before = '{"names":{"en":"Stavropol","ru":"Ставрополь"}}'::jsonb
		AND after = '{"names":{"en":null,"ru":"Ставрополь Новый"}}'::jsonb`, stav); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
	_, err = update(a, staffAs(perm.Admin), stav, app.CityPatch{Status: statusp(geo.StatusLive), Names: map[string]*string{"ru": nil}})
	wantField(t, err, "body.names.ru", "required")
	if n := e.count(t, `SELECT count(*) FROM cities WHERE id = $1 AND status = 'pilot'`, stav); n != 1 {
		t.Fatal("статус записан при отказе")
	}
}

// Названия, которые не проходят CleanName, и незнакомый язык — 400 по полю.
func TestUpdateCityNamesInvalid(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	a := e.admin()
	for _, c := range []struct{ locale, name, rule string }{
		{"en", "   ", "minLength"},
		{"en", "Stav" + string(rune(0x200b)) + "ropol", "pattern"},
		{"en", "Stav\nropol", "pattern"},
		{"de", "Stawropol", "enum"},
	} {
		_, err := update(a, staffAs(perm.Admin), stav, app.CityPatch{Names: map[string]*string{c.locale: strp(c.name)}})
		wantField(t, err, "body.names."+c.locale, c.rule)
	}
}

// Ничего не изменилось (тот же статус, свой slug, то же название, удаление отсутствующего перевода) — 200 с текущей
// карточкой, без событий, аудита и роста версии. Пустой patch — 400 body.
func TestUpdateCityNoChange(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	mih := e.cityID(t, "mihaylovsk")
	e.exec(t, `DELETE FROM city_names WHERE city_id = $1 AND locale = 'en'`, mih)
	v := e.cityVersion(t, mih)
	c, err := update(e.admin(), staffAs(perm.Admin), mih, app.CityPatch{Status: statusp(geo.StatusPilot), Slug: strp("mihaylovsk"),
		Names: map[string]*string{"ru": strp("Михайловск"), "en": nil}})
	if err != nil || c.Version != v || c.Slug != "mihaylovsk" {
		t.Fatalf("%+v %v", c, err)
	}
	if n := e.count(t, `SELECT (SELECT count(*) FROM outbox WHERE aggregate_id = $1) + (SELECT count(*) FROM audit_log WHERE object_id = $1)`, mih); n != 0 {
		t.Fatalf("записи без изменений: %d", n)
	}
	_, err = update(e.admin(), staffAs(perm.Admin), mih, app.CityPatch{})
	wantField(t, err, "body", "minProperties")
}

// Нет города — geo.ErrCityNotFound.
func TestUpdateCityNotFound(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	if _, err := update(e.admin(), staffAs(perm.Admin), uuid.New(), app.CityPatch{Status: statusp(geo.StatusLive)}); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("%v", err)
	}
}

// Модератор своего города: названия — можно; статус (и вместе с названиями) — 403, ничего не записано.
func TestUpdateCityModeratorOwnCity(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	mod := staffAs(perm.Moderator, stav)
	a := e.admin()
	if c, err := update(a, mod, stav, app.CityPatch{Names: map[string]*string{"en": strp("Stavropol City")}}); err != nil || c.Names["en"] != "Stavropol City" {
		t.Fatalf("названия своего города: %+v %v", c, err)
	}
	_, err := update(a, mod, stav, app.CityPatch{Status: statusp(geo.StatusLive), Names: map[string]*string{"en": strp("Stav")}})
	wantForbidden(t, err)
	_, err = update(a, mod, stav, app.CityPatch{Slug: strp("stav")})
	wantForbidden(t, err)
	if n := e.count(t, `SELECT count(*) FROM cities c JOIN city_names n ON n.city_id = c.id AND n.locale = 'en'
		WHERE c.id = $1 AND c.status = 'pilot' AND c.slug = 'stavropol' AND n.name = 'Stavropol City'`, stav); n != 1 {
		t.Fatal("записано при отказе")
	}
}

// Review Focus 1: модератор чужого города с одним names — 403 (конвейер handler город не сверяет), ничего не записано.
func TestUpdateCityModeratorForeignCity(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav, mih := e.cityID(t, "stavropol"), e.cityID(t, "mihaylovsk")
	_, err := update(e.admin(), staffAs(perm.Moderator, mih), stav, app.CityPatch{Names: map[string]*string{"en": strp("Hacked")}})
	wantForbidden(t, err)
	if n := e.count(t, `SELECT count(*) FROM city_names WHERE city_id = $1 AND name = 'Hacked'`, stav); n != 0 {
		t.Fatal("название чужого города записано")
	}
	// поддержка — без права на любое поле
	_, err = update(e.admin(), staffAs(perm.Support), stav, app.CityPatch{Names: map[string]*string{"en": strp("Hacked")}})
	wantForbidden(t, err)
	// ревью G6 m3: пустой names — тоже правка названий: на уровне сценария права решаются до формы — 403, а не
	// ошибка формы. По HTTP {"names":{}} до сценария не доходит: валидатор контракта (minProperties) отвечает 400 и
	// своему, и чужому городу
	_, err = update(e.admin(), staffAs(perm.Moderator, mih), stav, app.CityPatch{Names: map[string]*string{}})
	wantForbidden(t, err)
	// у модератора своего города тот же вызов сценария — ошибка формы body minProperties (форма после прав)
	_, err = update(e.admin(), staffAs(perm.Moderator, stav), stav, app.CityPatch{Names: map[string]*string{}})
	wantField(t, err, "body", "minProperties")
}

// NeedsApproval у права правки города (матрица изменилась без сценария) — ошибка без кода (500), ничего не записано.
func TestUpdateCityNeedsApprovalIs500(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	s := staffAs(perm.Admin)
	s.Grants = map[string]auth.Grant{"geo.cities.manage": auth.GrantDual}
	_, err := update(e.admin(), s, stav, app.CityPatch{Status: statusp(geo.StatusLive)})
	if err == nil {
		t.Fatal("ждали ошибку")
	}
	if status, _, _ := problem(err); status != 0 || errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("ждали ошибку без кода: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE id = $1 AND status = 'pilot'`, stav); n != 1 {
		t.Fatal("записано")
	}
}

// Статус и slug одним запросом — два события с последовательными версиями, версия города — v+2, один аудит.
func TestUpdateCityEventsVersions(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	v := e.cityVersion(t, stav)
	c, err := update(e.admin(), staffAs(perm.Admin), stav, app.CityPatch{Status: statusp(geo.StatusLive), Slug: strp("stav")})
	if err != nil || c.Version != v+2 {
		t.Fatalf("%+v %v", c, err)
	}
	rows, err := e.owner.Query(context.Background(), `SELECT event_type, aggregate_version FROM outbox WHERE aggregate_id = $1
		ORDER BY aggregate_version`, stav)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	var vers []int64
	for rows.Next() {
		var typ string
		var ver int64
		if err := rows.Scan(&typ, &ver); err != nil {
			t.Fatal(err)
		}
		got, vers = append(got, typ), append(vers, ver)
	}
	if rows.Err() != nil || !slices.Equal(got, []string{"geo.city_status_changed", "geo.city_slug_changed"}) ||
		!slices.Equal(vers, []int64{v + 1, v + 2}) {
		t.Fatalf("события %v версии %v (%v)", got, vers, rows.Err())
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.city_updated' AND object_id = $1
		AND after = '{"status":"live","slug":"stav"}'::jsonb`, stav); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
}

// Два города одновременно на один свободный slug: B прошёл проверку и стоит до записи (держит замок адреса), A ждёт
// замок (ревью G6 m4), видит итог B и получает 409 geo.slug_taken; у проигравшего нет строки истории. Сроки ожидания
// у каждого барьера (ревью G6 m5): регрессия не висит до -timeout пакета.
func TestUpdateCityConcurrentSlug(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav, mih := e.cityID(t, "stavropol"), e.cityID(t, "mihaylovsk")
	at, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	second := e.admin()
	second.SetStageHook(func(_ context.Context, s string) {
		if s == "slug" {
			once.Do(func() { close(at); <-release })
		}
	})
	bDone, aDone := make(chan struct{}), make(chan struct{})
	var errA, errB error
	go func() {
		defer close(bDone)
		_, errB = update(second, staffAs(perm.Admin), mih, app.CityPatch{Slug: strp("kavkaz")})
	}()
	if !recvWithin(t, at, 15*time.Second) {
		close(release)
		t.Fatalf("B не дошёл до точки slug: %v", errB)
	}
	go func() {
		defer close(aDone)
		_, errA = update(e.admin(), staffAs(perm.Admin), stav, app.CityPatch{Slug: strp("kavkaz")})
	}()
	waited := waitLockOrDoneWithin(t, e.owner, aDone, 15*time.Second)
	close(release)
	if !recvWithin(t, bDone, 30*time.Second) || !recvWithin(t, aDone, 30*time.Second) {
		t.Fatal("правки не завершились")
	}
	if !waited || errB != nil || !errors.Is(errA, geo.ErrSlugTaken) {
		t.Fatalf("A ждал замок: %v, a: %v, b: %v", waited, errA, errB)
	}
	if n := e.count(t, `SELECT count(*) FROM city_slug_history WHERE city_id = $1`, stav); n != 0 {
		t.Fatal("история проигравшего записана")
	}
}

// Ревью уборки I1: писатель адреса без замка SlugLock (сегодня — активация города) занял адрес между проверкой и
// записью правки. Правка mihaylovsk → kavkaz стоит в точке slug (проверка прошла, замок адреса у неё); владелец
// базы пишет kavkaz городу stavropol и коммитит; правка продолжает — 23505 cities_slug_key переводится в
// geo.slug_taken (slugConflict), не 500; у mihaylovsk ни нового адреса, ни строки истории.
func TestUpdateCitySlugConflictLocklessWriter(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav, mih := e.cityID(t, "stavropol"), e.cityID(t, "mihaylovsk")
	at, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	adm := e.admin()
	adm.SetStageHook(func(_ context.Context, s string) {
		if s == "slug" {
			once.Do(func() { close(at); <-release })
		}
	})
	var err error
	go func() {
		defer close(done)
		_, err = update(adm, staffAs(perm.Admin), mih, app.CityPatch{Slug: strp("kavkaz")})
	}()
	if !recvWithin(t, at, 15*time.Second) {
		close(release)
		t.Fatalf("правка не дошла до точки slug: %v", err)
	}
	e.exec(t, `UPDATE cities SET slug = 'kavkaz' WHERE id = $1`, stav)
	close(release)
	if !recvWithin(t, done, 30*time.Second) {
		t.Fatal("правка не завершилась")
	}
	if !errors.Is(err, geo.ErrSlugTaken) {
		t.Fatalf("ждали geo.ErrSlugTaken, получили: %v", err)
	}
	if n := e.count(t, `SELECT (SELECT count(*) FROM cities WHERE id = $1 AND slug = 'mihaylovsk')
		- (SELECT count(*) FROM city_slug_history WHERE city_id = $1)`, mih); n != 1 {
		t.Fatal("адрес или история mihaylovsk записаны")
	}
}

// Ревью G6 m4 — три смены адреса: T0 «stavropol → kavkaz» записал адрес и не закоммичен; T2 «mihaylovsk → kavkaz»
// идёт следом; T0 коммитит, T1 «kavkaz → kavkaz-2» уносит адрес в историю stavropol; T2 продолжает. Без замка адреса
// T2 проверял занятость до коммита T0 (свободно) и после T1 записывал kavkaz себе — старые ссылки stavropol вели бы на
// mihaylovsk. С замком T2 ждёт T0 и получает 409; ни один текущий адрес не совпадает с историей другого города.
func TestUpdateCitySlugThreeWay(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav, mih := e.cityID(t, "stavropol"), e.cityID(t, "mihaylovsk")
	hookAt := func(stage string) (*app.Admin, chan struct{}, chan struct{}) {
		at, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		a := e.admin()
		a.SetStageHook(func(_ context.Context, s string) {
			if s == stage {
				once.Do(func() { close(at); <-release })
			}
		})
		return a, at, release
	}
	adm0, at0, release0 := hookAt("updated")
	adm2, at2, release2 := hookAt("slug")
	var err0, err2 error
	done0, done2 := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done0)
		_, err0 = update(adm0, staffAs(perm.Admin), stav, app.CityPatch{Slug: strp("kavkaz")})
	}()
	if !recvWithin(t, at0, 15*time.Second) {
		close(release0)
		close(release2)
		t.Fatalf("T0 не дошёл до записи: %v", err0)
	}
	go func() {
		defer close(done2)
		_, err2 = update(adm2, staffAs(perm.Admin), mih, app.CityPatch{Slug: strp("kavkaz")})
	}()
	// T2 либо ждёт замок адреса (исправлено), либо прошёл проверку и стоит в точке slug (дефект m4)
	waited := waitLockOrDoneWithin(t, e.owner, at2, 15*time.Second)
	close(release0)
	if !recvWithin(t, done0, 30*time.Second) || err0 != nil {
		close(release2)
		t.Fatalf("T0: %v", err0)
	}
	if _, err := update(e.admin(), staffAs(perm.Admin), stav, app.CityPatch{Slug: strp("kavkaz-2")}); err != nil {
		close(release2)
		t.Fatalf("T1: %v", err)
	}
	close(release2)
	if !recvWithin(t, done2, 30*time.Second) {
		t.Fatal("T2 не завершился")
	}
	if !waited || !errors.Is(err2, geo.ErrSlugTaken) {
		t.Fatalf("T2 ждал замок: %v, итог: %v", waited, err2)
	}
	if n := e.count(t, `SELECT count(*) FROM cities c JOIN city_slug_history h ON h.slug = c.slug AND h.city_id <> c.id`); n != 0 {
		t.Fatalf("текущий адрес города в истории другого: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE id = $1 AND slug = 'mihaylovsk'`, mih); n != 1 {
		t.Fatal("адрес T2 записан")
	}
}
