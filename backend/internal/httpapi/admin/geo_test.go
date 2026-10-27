package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Города для выбора городов модератора (GET /v1/geo/cities, D24, P28, M10) за полным конвейером: все
// статусы, q, ids, курсор; модератору — только его города. Сид базы — Ставрополь (население больше) и
// Михайловск, оба pilot.

type geoEnv struct {
	h         http.Handler
	v         *apitest.Validator
	stav, mih uuid.UUID
}

func newGeoEnv(t *testing.T, sessions map[string]*auth.Staff) geoEnv {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "admin")
	e := geoEnv{v: apitest.New(t, oapi.GetSpec)}
	for slug, dst := range map[string]*uuid.UUID{"stavropol": &e.stav, "mihaylovsk": &e.mih} {
		if err := pools.Owner.QueryRow(context.Background(), `SELECT id FROM cities WHERE slug = $1`, slug).Scan(dst); err != nil {
			t.Fatalf("город %s: %v", slug, err)
		}
	}
	all := map[string]*auth.Staff{
		"owner":   staffOf(perm.Owner, auth.StageActive, false),
		"support": staffOf(perm.Support, auth.StageActive, false),
	}
	for k, s := range sessions {
		all[k] = s
	}
	o := testOptions(t)
	o.DB = pools.As
	o.Geo = geoadmin.New(geo.New(pools.As))
	o.Sessions = &fakeSessions{staff: all}
	e.h = newHandler(t, o)
	return e
}

// cities — GET /v1/geo/cities с query; ответ сверяется с контрактом. Возвращает ответ и id городов.
func (e geoEnv) cities(t *testing.T, token string, q url.Values, hdr ...string) (*httpRec, []uuid.UUID, string) {
	t.Helper()
	r := call(http.MethodGet, "/v1/geo/cities?"+q.Encode(), token, "")
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	rec := e.v.Do(t, e.h, r)
	if rec.Code != http.StatusOK {
		return rec, nil, ""
	}
	var page oapi.GeoCityPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("тело %s: %v", rec.Body.String(), err)
	}
	ids := make([]uuid.UUID, 0, len(page.Items))
	for _, c := range page.Items {
		ids = append(ids, c.Id)
	}
	next := ""
	if page.NextCursor != nil {
		next = *page.NextCursor
	}
	return rec, ids, next
}

func idsQuery(ids ...uuid.UUID) url.Values {
	q := url.Values{}
	for _, id := range ids {
		q.Add("ids", id.String())
	}
	return q
}

// M10: модератору — только его города; ids чужого города — пусто; ids пересекаются с его городами; курсор из
// чужого списка фильтр не снимает; модератор без городов (CityIDs nil) — пусто, а не весь справочник.
// Владелец и поддержка видят все города, ids сужает.
func TestGeoCitiesModeratorScope(t *testing.T) {
	stavMod := staffOf(perm.Moderator, auth.StageActive, false)
	mihMod := staffOf(perm.Moderator, auth.StageActive, false)
	noCities := staffOf(perm.Moderator, auth.StageActive, false)
	e := newGeoEnv(t, map[string]*auth.Staff{"stav-mod": stavMod, "mih-mod": mihMod, "no-cities": noCities})
	stavMod.CityIDs, mihMod.CityIDs = []uuid.UUID{e.stav}, []uuid.UUID{e.mih}

	cases := []struct {
		name, token string
		q           url.Values
		want        []uuid.UUID
	}{
		{"владелец — все", "owner", url.Values{}, []uuid.UUID{e.stav, e.mih}},
		{"поддержка — все", "support", url.Values{}, []uuid.UUID{e.stav, e.mih}},
		{"владелец, ids", "owner", idsQuery(e.mih), []uuid.UUID{e.mih}},
		{"модератор — свои", "stav-mod", url.Values{}, []uuid.UUID{e.stav}},
		{"модератор, ids чужого", "stav-mod", idsQuery(e.mih), []uuid.UUID{}},
		{"модератор, ids свой и чужой", "stav-mod", idsQuery(e.mih, e.stav), []uuid.UUID{e.stav}},
		{"модератор, q чужого", "stav-mod", url.Values{"q": {"мих"}}, []uuid.UUID{}},
		{"модератор без городов", "no-cities", url.Values{}, []uuid.UUID{}},
	}
	for _, c := range cases {
		rec, got, _ := e.cities(t, c.token, c.q)
		if rec.Code != http.StatusOK || !slices.Equal(got, c.want) {
			t.Errorf("%s: %d %v, ждали %v (%s)", c.name, rec.Code, got, c.want, rec.Body.String())
		}
	}
	// курсор страницы владельца (после Ставрополя) модератору Ставрополя ничего не открывает, модератору
	// Михайловска — только его город
	_, first, next := e.cities(t, "owner", url.Values{"limit": {"1"}})
	if !slices.Equal(first, []uuid.UUID{e.stav}) || next == "" {
		t.Fatalf("первая страница владельца: %v, next %q", first, next)
	}
	if _, got, _ := e.cities(t, "stav-mod", url.Values{"cursor": {next}}); len(got) != 0 {
		t.Fatalf("курсор владельца у модератора Ставрополя: %v", got)
	}
	if _, got, _ := e.cities(t, "mih-mod", url.Values{"cursor": {next}}); !slices.Equal(got, []uuid.UUID{e.mih}) {
		t.Fatalf("курсор владельца у модератора Михайловска: %v", got)
	}
}

// Заголовки и язык: Cache-Control private, no-store (ответ зависит от сотрудника), Vary: Accept-Language,
// Content-Language — согласованный язык; без поддерживаемого языка — канонические названия без
// Content-Language.
func TestGeoCitiesHeadersAndLanguage(t *testing.T) {
	e := newGeoEnv(t, nil)
	rec, _, _ := e.cities(t, "owner", idsQuery(e.stav), "Accept-Language", "en-GB, ru;q=0.5")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" ||
		rec.Header().Get("Vary") != "Accept-Language" || rec.Header().Get("Content-Language") != "en" {
		t.Fatalf("en: %d %v", rec.Code, rec.Header())
	}
	if !strings.Contains(rec.Body.String(), `"name":"Stavropol"`) {
		t.Fatalf("en: %s", rec.Body.String())
	}
	rec, _, _ = e.cities(t, "owner", idsQuery(e.stav), "Accept-Language", "de")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Language") != "" ||
		!strings.Contains(rec.Body.String(), `"name":"Ставрополь"`) {
		t.Fatalf("de: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
}

// Ввод сверх схемы и границы: q из одних знаков — 400 query.q minLength; 101 id — 400 query.ids (валидатор);
// 100 id — 200; негодный курсор — 400 query.cursor.
func TestGeoCitiesValidation(t *testing.T) {
	e := newGeoEnv(t, nil)
	wantField := func(rec *httpRec, field string) {
		t.Helper()
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"`+httpx.CodeValidationFailed+`"`) ||
			!strings.Contains(rec.Body.String(), `"`+field+`"`) {
			t.Fatalf("ждали 400 по %s: %d %s", field, rec.Code, rec.Body.String())
		}
	}
	rec, _, _ := e.cities(t, "owner", url.Values{"q": {"!!!"}})
	wantField(rec, "query.q")
	many := make([]uuid.UUID, 101)
	for i := range many {
		many[i] = uuid.New()
	}
	// запрос вне контракта — мимо apitest (он отверг бы его до сервера)
	wantField(serve(e.h, call(http.MethodGet, "/v1/geo/cities?"+idsQuery(many...).Encode(), "owner", "")), "query.ids")
	many[99] = e.mih
	if rec, got, _ := e.cities(t, "owner", idsQuery(many[:100]...)); rec.Code != http.StatusOK || !slices.Equal(got, []uuid.UUID{e.mih}) {
		t.Fatalf("100 id: %d %v %s", rec.Code, got, rec.Body.String())
	}
	rec, _, _ = e.cities(t, "owner", url.Values{"cursor": {"not-a-cursor"}})
	wantField(rec, "query.cursor")
}
