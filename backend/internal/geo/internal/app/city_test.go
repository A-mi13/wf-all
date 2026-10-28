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
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	a := e.admin()
	for _, c := range []struct{ locale, name, rule string }{
		{"en", "   ", "minLength"},
		{"en", "Stav​ropol", "pattern"},
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
	e := newActEnv(t)
	if _, err := update(e.admin(), staffAs(perm.Admin), uuid.New(), app.CityPatch{Status: statusp(geo.StatusLive)}); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("%v", err)
	}
}

// Модератор своего города: названия — можно; статус (и вместе с названиями) — 403, ничего не записано.
func TestUpdateCityModeratorOwnCity(t *testing.T) {
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
}

// NeedsApproval у права правки города (матрица изменилась без сценария) — ошибка без кода (500), ничего не записано.
func TestUpdateCityNeedsApprovalIs500(t *testing.T) {
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

// Два города одновременно на один свободный slug (барьер: оба прошли проверку занятости): один успех, другой —
// 409 geo.slug_taken (23505 cities_slug_key → код, не 500).
func TestUpdateCityConcurrentSlug(t *testing.T) {
	e := newActEnv(t)
	stav, mih := e.cityID(t, "stavropol"), e.cityID(t, "mihaylovsk")
	at, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	second := e.admin()
	second.SetStageHook(func(_ context.Context, s string) {
		if s == "slug" {
			once.Do(func() { close(at); <-done })
		}
	})
	var errB error
	var wg sync.WaitGroup
	wg.Go(func() { _, errB = update(second, staffAs(perm.Admin), mih, app.CityPatch{Slug: strp("kavkaz")}) })
	<-at
	_, errA := update(e.admin(), staffAs(perm.Admin), stav, app.CityPatch{Slug: strp("kavkaz")})
	close(done)
	wg.Wait()
	if errA != nil || !errors.Is(errB, geo.ErrSlugTaken) {
		t.Fatalf("a: %v, b: %v", errA, errB)
	}
	if n := e.count(t, `SELECT count(*) FROM city_slug_history WHERE city_id = $1`, mih); n != 0 {
		t.Fatal("история проигравшего записана")
	}
}
