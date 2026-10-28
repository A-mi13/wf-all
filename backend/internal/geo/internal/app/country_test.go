package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/geo/internal/domain"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// Страны (Task 13): база на тест под ролью admin (actEnv: XA — выключенная ru, XB — включённая en, RU сида),
// реестр подтверждений с действиями geo и подменённой проверкой инициатора и замком акторов (их регистрирует staff;
// настоящие — сквозные тесты internal/httpapi/admin). Сервис подтверждений — на том же пуле.

type countryEnv struct {
	actEnv
	a   *app.Admin
	svc *approvals.Service
}

func newCountryEnv(t *testing.T) countryEnv {
	t.Helper()
	e := countryEnv{actEnv: newActEnv(t)}
	reg := approvals.NewRegistry()
	e.svc = approvals.New(e.as, e.clk, reg)
	e.a = e.admin()
	e.a.RegisterApprovals(reg, e.svc)
	reg.SetInitiatorCheck(func(context.Context, pgx.Tx, approvals.Approval) error { return nil })
	reg.SetActorLock(func(context.Context, pgx.Tx, approvals.Approval, *auth.Staff) error { return nil })
	if err := reg.Validate(); err != nil {
		t.Fatal(err)
	}
	return e
}

func kz() app.CountryInput {
	return app.CountryInput{
		CountryRules: domain.CountryRules{Code: "KZ", Currency: "KZT", DefaultLocale: "ru", PhonePrefix: "+7",
			WeekStartsOn: 1, MinSignupAge: 14, AgeOfMajority: 18},
		IsEnabled: true, Names: map[string]string{"ru": "Казахстан", "en": "Kazakhstan"},
	}
}

func intp(v int) *int { return &v }

func (e countryEnv) approve(t *testing.T, by *auth.Staff, apID uuid.UUID) (approvals.Approval, error) {
	t.Helper()
	return e.svc.Approve(context.Background(), by, by.Label, approvals.Meta{}, apID, "")
}

func (e countryEnv) payload(t *testing.T, apID uuid.UUID) map[string]any {
	t.Helper()
	var raw []byte
	if err := e.owner.QueryRow(context.Background(), `SELECT payload FROM approvals WHERE id = $1`, apID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e countryEnv) countryVersion(t *testing.T, code string) int64 {
	t.Helper()
	var v int64
	if err := e.owner.QueryRow(context.Background(), `SELECT version FROM countries WHERE code = $1`, code).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Создание — всегда запрос geo.country_create (202): страны нет, запрос pending с правом geo.countries.rules и полями
// CountryInput наверху payload; код уже есть — 409 до запроса; без названия на default_locale, RUR — 400; модератор и
// поддержка — 403.
func TestCreateCountryNeedsApproval(t *testing.T) {
	e := newCountryEnv(t)
	s := staffAs(perm.Admin)
	ap, err := e.a.CreateCountry(context.Background(), s, kz(), approvals.Meta{})
	if err != nil || ap.Status != approvals.StatusPending || ap.Action != "geo.country_create" ||
		ap.Permission != "geo.countries.rules" || ap.RequestedBy != s.StaffID {
		t.Fatalf("%+v %v", ap, err)
	}
	if n := e.count(t, `SELECT count(*) FROM countries WHERE code = 'KZ'`); n != 0 {
		t.Fatal("страна создана без подтверждения")
	}
	p := e.payload(t, ap.ID)
	want := map[string]any{"v": 1.0, "init_id": s.StaffID.String(), "init_ver": 1.0, "code": "KZ", "currency": "KZT",
		"default_locale": "ru", "phone_prefix": "+7", "week_starts_on": 1.0, "min_signup_age": 14.0,
		"age_of_majority": 18.0, "is_enabled": true, "names": map[string]any{"ru": "Казахстан", "en": "Kazakhstan"}}
	if !jsonEqual(p, want) {
		t.Fatalf("payload %v", p)
	}
	ru := kz()
	ru.Code = "RU"
	if _, err := e.a.CreateCountry(context.Background(), s, ru, approvals.Meta{}); !errors.Is(err, geo.ErrCountryExists) {
		t.Fatalf("код есть: %v", err)
	}
	noName := kz()
	noName.Names = map[string]string{"en": "Kazakhstan"}
	_, err = e.a.CreateCountry(context.Background(), s, noName, approvals.Meta{})
	wantField(t, err, "body.names.ru", "required")
	rur := kz()
	rur.Currency = "RUR"
	_, err = e.a.CreateCountry(context.Background(), s, rur, approvals.Meta{})
	wantField(t, err, "body.currency", "format")
	for _, role := range []perm.Role{perm.Moderator, perm.Support} {
		_, err := e.a.CreateCountry(context.Background(), staffAs(role), kz(), approvals.Meta{})
		wantForbidden(t, err)
	}
	if n := e.count(t, `SELECT count(*) FROM approvals`); n != 1 {
		t.Fatalf("запросов %d", n)
	}
}

// m5 ревью G5 (R12): XK — пользовательский код, не ISO 3166-1 — 400 body.code format.
func TestCreateCountryXK(t *testing.T) {
	e := newCountryEnv(t)
	xk := kz()
	xk.Code, xk.Currency = "XK", "EUR"
	_, err := e.a.CreateCountry(context.Background(), staffAs(perm.Owner), xk, approvals.Meta{})
	wantField(t, err, "body.code", "format")
}

// Второй владелец/админ подтверждает — страна создана (is_enabled как в запросе), названия, событие и аудит от
// подтверждающего (requested_by, approval_id); свой запрос инициатор подтвердить не может.
func TestCountryCreateApproveExecutes(t *testing.T) {
	e := newCountryEnv(t)
	a, b := staffAs(perm.Admin), staffAs(perm.Owner)
	ap, err := e.a.CreateCountry(context.Background(), a, kz(), approvals.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.approve(t, a, ap.ID); !errors.Is(err, approvals.ErrSelf) {
		t.Fatalf("свой запрос: %v", err)
	}
	res, err := e.approve(t, b, ap.ID)
	if err != nil || res.Status != approvals.StatusExecuted {
		t.Fatalf("%+v %v", res, err)
	}
	if n := e.count(t, `SELECT count(*) FROM countries WHERE code = 'KZ' AND name = 'Казахстан' AND currency = 'KZT'
		AND default_locale = 'ru' AND phone_prefix = '+7' AND is_enabled AND version = 1 AND min_signup_age = 14
		AND age_of_majority = 18`); n != 1 {
		t.Fatal("страна")
	}
	if n := e.count(t, `SELECT count(*) FROM country_names n JOIN countries c ON c.id = n.country_id WHERE c.code = 'KZ'`); n != 2 {
		t.Fatalf("названий %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox o JOIN countries c ON c.id = o.aggregate_id WHERE c.code = 'KZ'
		AND o.event_type = 'geo.country_created' AND o.aggregate_type = 'country' AND o.aggregate_version = 1
		AND o.payload->>'country_code' = 'KZ'`); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log l JOIN countries c ON c.id = l.object_id WHERE c.code = 'KZ'
		AND l.action = 'geo.country_created' AND l.object_type = 'country' AND l.actor_staff_id = $1
		AND l.actor_label = $2 AND l.scope_city_id IS NULL AND l.after->>'requested_by' = $3 AND l.after->>'approval_id' = $4`,
		b.StaffID, b.Label, a.StaffID.String(), ap.ID.String()); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
}

// Два ждущих запроса на один код: первый исполнен, второй — failed (код причины geo.country_exists), страна одна.
func TestCountryCreateTwoRequests(t *testing.T) {
	e := newCountryEnv(t)
	a, b := staffAs(perm.Admin), staffAs(perm.Owner)
	ap1, err := e.a.CreateCountry(context.Background(), a, kz(), approvals.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	ap2, err := e.a.CreateCountry(context.Background(), a, kz(), approvals.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.approve(t, b, ap1.ID); err != nil {
		t.Fatal(err)
	}
	res, err := e.approve(t, b, ap2.ID)
	if status, code, _ := problem(err); res.Status != approvals.StatusFailed || res.Error != geo.CodeCountryExists ||
		code != geo.CodeCountryExists || status != 409 {
		t.Fatalf("второй: %+v %v", res, err)
	}
	if n := e.count(t, `SELECT count(*) FROM countries WHERE code = 'KZ'`); n != 1 {
		t.Fatalf("стран %d", n)
	}
}

// Правила (валюта, пороги) — всегда 202 независимо от статусов городов: XB без городов, RU — с городами pilot
// (сид) переведёнными в waitlist; страна не изменилась, запрос pending с правом geo.countries.rules.
func TestUpdateCountryRulesAlwaysApproval(t *testing.T) {
	e := newCountryEnv(t)
	e.exec(t, `UPDATE cities SET status = 'waitlist'`)
	s := staffAs(perm.Admin)
	for _, c := range []struct {
		code string
		p    app.CountryPatch
	}{
		{"XB", app.CountryPatch{Currency: strp("USD")}},
		{"RU", app.CountryPatch{MinSignupAge: intp(16)}},
		{"RU", app.CountryPatch{AgeOfMajority: intp(21)}},
	} {
		v := e.countryVersion(t, c.code)
		_, ap, err := e.a.UpdateCountry(context.Background(), s, c.code, c.p, "ru", approvals.Meta{})
		if err != nil || ap == nil || ap.Action != "geo.country_rules" || ap.Permission != "geo.countries.rules" ||
			ap.Status != approvals.StatusPending {
			t.Fatalf("%s %+v: %+v %v", c.code, c.p, ap, err)
		}
		if e.countryVersion(t, c.code) != v {
			t.Fatalf("%s изменена без подтверждения", c.code)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM countries WHERE code = 'XB' AND currency = 'XXX'`); n != 1 {
		t.Fatal("валюта изменена")
	}
}

// Выключение включённой — 202; включение выключенной — 200 сразу: version + 1, событие и аудит сотрудника.
func TestUpdateCountryDisableApproval(t *testing.T) {
	e := newCountryEnv(t)
	s := staffAs(perm.Admin)
	_, ap, err := e.a.UpdateCountry(context.Background(), s, "XB", app.CountryPatch{IsEnabled: boolp(false)}, "", approvals.Meta{})
	if err != nil || ap == nil {
		t.Fatalf("выключение: %+v %v", ap, err)
	}
	v := e.countryVersion(t, "XA")
	c, ap, err := e.a.UpdateCountry(context.Background(), s, "XA", app.CountryPatch{IsEnabled: boolp(true)}, "", approvals.Meta{})
	if err != nil || ap != nil || !c.IsEnabled || c.Version != v+1 || c.Code != "XA" {
		t.Fatalf("включение: %+v %+v %v", c, ap, err)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox o JOIN countries c ON c.id = o.aggregate_id WHERE c.code = 'XA'
		AND o.event_type = 'geo.country_updated' AND o.aggregate_version = $1 AND o.payload->'changed' = '["is_enabled"]'::jsonb
		AND o.payload->>'country_code' = 'XA'`, v+1); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.country_updated' AND actor_staff_id = $1
		AND object_type = 'country' AND before = '{"is_enabled":false}'::jsonb AND after = '{"is_enabled":true}'::jsonb`, s.StaffID); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
}

// Неправила (префикс, неделя, названия) — 200 сразу; правило с текущим значением — не правило: 200 без запроса.
func TestUpdateCountryImmediateFields(t *testing.T) {
	e := newCountryEnv(t)
	s := staffAs(perm.Admin)
	v := e.countryVersion(t, "RU")
	c, ap, err := e.a.UpdateCountry(context.Background(), s, "RU", app.CountryPatch{
		PhonePrefix: strp("+8"), WeekStartsOn: intp(7), Names: map[string]*string{"ru": strp("Россия!"), "en": nil},
		Currency: strp("RUB"), IsEnabled: boolp(true)}, "en", approvals.Meta{})
	if err != nil || ap != nil || c.PhonePrefix != "+8" || c.WeekStartsOn != 7 || c.Names["ru"] != "Россия!" ||
		len(c.Names) != 1 || c.Version != v+1 || c.Name != "Россия!" {
		t.Fatalf("%+v %+v %v", c, ap, err)
	}
	if n := e.count(t, `SELECT count(*) FROM countries WHERE code = 'RU' AND name = 'Россия!'`); n != 1 {
		t.Fatal("countries.name не переписан строкой языка страны")
	}
	if n := e.count(t, `SELECT count(*) FROM outbox o JOIN countries c ON c.id = o.aggregate_id WHERE c.code = 'RU'
		AND o.event_type = 'geo.country_updated' AND o.payload->'changed' = '["names","phone_prefix","week_starts_on"]'::jsonb`); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM approvals`); n != 0 {
		t.Fatal("запрос на неизменившееся правило")
	}
	_, _, err = e.a.UpdateCountry(context.Background(), s, "RU", app.CountryPatch{Names: map[string]*string{"ru": nil}}, "", approvals.Meta{})
	wantField(t, err, "body.names.ru", "required")
	if _, _, err := e.a.UpdateCountry(context.Background(), s, "ZZ", app.CountryPatch{PhonePrefix: strp("+1")}, "", approvals.Meta{}); !errors.Is(err, geo.ErrCountryNotFound) {
		t.Fatalf("нет страны: %v", err)
	}
	// модератор и поддержка — 403 и на поля «сразу»
	for _, role := range []perm.Role{perm.Moderator, perm.Support} {
		_, _, err := e.a.UpdateCountry(context.Background(), staffAs(role), "RU", app.CountryPatch{PhonePrefix: strp("+9")}, "", approvals.Meta{})
		wantForbidden(t, err)
	}
	// пустой patch — 400
	_, _, err = e.a.UpdateCountry(context.Background(), s, "RU", app.CountryPatch{}, "", approvals.Meta{})
	wantField(t, err, "body", "minProperties")
}

// Префикс и валюта одним запросом — весь patch в запросе, ни одно поле не применено.
func TestUpdateCountryMixedFields(t *testing.T) {
	e := newCountryEnv(t)
	_, ap, err := e.a.UpdateCountry(context.Background(), staffAs(perm.Owner), "RU",
		app.CountryPatch{PhonePrefix: strp("+8"), Currency: strp("USD")}, "", approvals.Meta{})
	if err != nil || ap == nil {
		t.Fatalf("%+v %v", ap, err)
	}
	if n := e.count(t, `SELECT count(*) FROM countries WHERE code = 'RU' AND phone_prefix = '+7' AND currency = 'RUB'`); n != 1 {
		t.Fatal("поле «сразу» применено")
	}
}

// Правила проверяются против итоговых значений: новый min_signup_age выше текущего age_of_majority — 400
// body.age_of_majority minimum; недействующая валюта — 400 body.currency.
func TestUpdateCountryFinalRules(t *testing.T) {
	e := newCountryEnv(t)
	s := staffAs(perm.Owner)
	_, _, err := e.a.UpdateCountry(context.Background(), s, "RU", app.CountryPatch{MinSignupAge: intp(19)}, "", approvals.Meta{})
	wantField(t, err, "body.age_of_majority", "minimum")
	_, _, err = e.a.UpdateCountry(context.Background(), s, "RU", app.CountryPatch{Currency: strp("RUR")}, "", approvals.Meta{})
	wantField(t, err, "body.currency", "format")
	if n := e.count(t, `SELECT count(*) FROM approvals`); n != 0 {
		t.Fatal("запрос при нарушении правил")
	}
}

// ⚠️ ревью G8 (n4): code — строкой наверху; from/to — snake_case, только меняющиеся поля, и ВСЕ меняющиеся поля patch:
// ключи from = ключи to = ключи patch без неизменившихся; названия — по изменённым языкам; patch — весь присланный.
func TestCountryRulesPayloadShape(t *testing.T) {
	e := newCountryEnv(t)
	s := staffAs(perm.Admin)
	p := app.CountryPatch{Currency: strp("USD"), PhonePrefix: strp("+7") /* как было */, WeekStartsOn: intp(7),
		MinSignupAge: intp(15), AgeOfMajority: intp(18) /* как было */, IsEnabled: boolp(false),
		Names: map[string]*string{"en": strp("Russian Federation"), "ru": strp("Россия")}}
	_, ap, err := e.a.UpdateCountry(context.Background(), s, "RU", p, "", approvals.Meta{})
	if err != nil || ap == nil {
		t.Fatalf("%+v %v", ap, err)
	}
	pl := e.payload(t, ap.ID)
	var countryID uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM countries WHERE code = 'RU'`).Scan(&countryID); err != nil {
		t.Fatal(err)
	}
	if pl["v"] != 1.0 || pl["code"] != "RU" || pl["init_id"] != s.StaffID.String() || pl["init_ver"] != 1.0 ||
		pl["country_id"] != countryID.String() || pl["version"] != float64(e.countryVersion(t, "RU")) {
		t.Fatalf("payload %v", pl)
	}
	from, to, patch := pl["from"].(map[string]any), pl["to"].(map[string]any), pl["patch"].(map[string]any)
	wantKeys := []string{"currency", "is_enabled", "min_signup_age", "names", "week_starts_on"}
	if got := slices.Sorted(maps.Keys(from)); !slices.Equal(got, wantKeys) {
		t.Fatalf("from %v", from)
	}
	if got := slices.Sorted(maps.Keys(to)); !slices.Equal(got, wantKeys) {
		t.Fatalf("to %v", to)
	}
	// все меняющиеся ключи patch — в from/to (неизменившиеся phone_prefix и age_of_majority — нет)
	changed := []string{}
	for k := range patch {
		if !slices.Contains([]string{"phone_prefix", "age_of_majority"}, k) {
			changed = append(changed, k)
		}
	}
	if slices.Sort(changed); !slices.Equal(changed, wantKeys) || len(patch) != 7 {
		t.Fatalf("patch %v", patch)
	}
	if !jsonEqual(from, map[string]any{"currency": "RUB", "is_enabled": true, "min_signup_age": 14.0,
		"week_starts_on": 1.0, "names": map[string]any{"en": "Russia"}}) ||
		!jsonEqual(to, map[string]any{"currency": "USD", "is_enabled": false, "min_signup_age": 15.0,
			"week_starts_on": 7.0, "names": map[string]any{"en": "Russian Federation"}}) {
		t.Fatalf("from %v to %v", from, to)
	}
}

// Подтверждение исполняет весь patch: поля «сразу» и правила, version + 1, событие и аудит от подтверждающего с
// requested_by и approval_id.
func TestCountryRulesApproveExecutes(t *testing.T) {
	e := newCountryEnv(t)
	a, b := staffAs(perm.Admin), staffAs(perm.Owner)
	v := e.countryVersion(t, "RU")
	_, ap, err := e.a.UpdateCountry(context.Background(), a, "RU", app.CountryPatch{PhonePrefix: strp("+8"),
		AgeOfMajority: intp(21), IsEnabled: boolp(false), Names: map[string]*string{"en": nil}}, "", approvals.Meta{})
	if err != nil || ap == nil {
		t.Fatal(err)
	}
	res, err := e.approve(t, b, ap.ID)
	if err != nil || res.Status != approvals.StatusExecuted {
		t.Fatalf("%+v %v", res, err)
	}
	if n := e.count(t, `SELECT count(*) FROM countries WHERE code = 'RU' AND phone_prefix = '+8' AND age_of_majority = 21
		AND NOT is_enabled AND version = $1`, v+1); n != 1 {
		t.Fatal("patch не исполнен")
	}
	if n := e.count(t, `SELECT count(*) FROM country_names n JOIN countries c ON c.id = n.country_id WHERE c.code = 'RU' AND n.locale = 'en'`); n != 0 {
		t.Fatal("перевод не удалён")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.country_updated' AND actor_staff_id = $1
		AND actor_label = $2 AND after->>'requested_by' = $3 AND after->>'approval_id' = $4 AND after->>'age_of_majority' = '21'`,
		b.StaffID, b.Label, a.StaffID.String(), ap.ID.String()); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox o JOIN countries c ON c.id = o.aggregate_id WHERE c.code = 'RU'
		AND o.event_type = 'geo.country_updated' AND o.aggregate_version = $1`, v+1); n != 1 {
		t.Fatalf("событие: %d", n)
	}
}

// Страну правили напрямую после запроса (версия выросла) — failed (platform.approval_stale), 409, правила не
// применены.
func TestCountryRulesStale(t *testing.T) {
	e := newCountryEnv(t)
	a, b := staffAs(perm.Admin), staffAs(perm.Owner)
	_, ap, err := e.a.UpdateCountry(context.Background(), a, "RU", app.CountryPatch{Currency: strp("USD")}, "", approvals.Meta{})
	if err != nil || ap == nil {
		t.Fatal(err)
	}
	if _, nap, err := e.a.UpdateCountry(context.Background(), a, "RU", app.CountryPatch{PhonePrefix: strp("+8")}, "", approvals.Meta{}); err != nil || nap != nil {
		t.Fatal(err)
	}
	res, err := e.approve(t, b, ap.ID)
	if !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed || res.Error != httpx.CodeApprovalStale {
		t.Fatalf("%+v %v", res, err)
	}
	if n := e.count(t, `SELECT count(*) FROM countries WHERE code = 'RU' AND currency = 'RUB'`); n != 1 {
		t.Fatal("валюта применена")
	}
}

// jsonEqual — равенство значений после JSON (числа — float64).
func jsonEqual(got, want any) bool {
	g, err1 := json.Marshal(got)
	w, err2 := json.Marshal(want)
	if err1 != nil || err2 != nil {
		return false
	}
	var a, b any
	_ = json.Unmarshal(g, &a)
	_ = json.Unmarshal(w, &b)
	ga, _ := json.Marshal(a)
	wb, _ := json.Marshal(b)
	return string(ga) == string(wb)
}
