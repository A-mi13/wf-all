package admin_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
)

// updateGeoCity и linkGeoCity (Task 11) за полным конвейером: ответы по контракту, повтор с тем же Idempotency-Key,
// права по полям для handler-операции (модератор чужого города — 403 сценария, Review Focus 1), коды ошибок.

func (e geoEnv) send(t *testing.T, method, token, path, key, body, lang string) *httpRec {
	t.Helper()
	r := call(method, path, token, body)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if lang != "" {
		r.Header.Set("Accept-Language", lang)
	}
	return e.v.Do(t, e.h, r)
}

func TestUpdateGeoCityHTTP(t *testing.T) {
	stavMod := staffOf(perm.Moderator, auth.StageActive, false)
	mihMod := staffOf(perm.Moderator, auth.StageActive, false)
	e := newGeoEnv(t, map[string]*auth.Staff{"admin": activator(), "stav-mod": stavMod, "mih-mod": mihMod})
	stavMod.CityIDs, mihMod.CityIDs = []uuid.UUID{e.stav}, []uuid.UUID{e.mih}
	stavMod.Label, mihMod.Label = "Модератор С.", "Модератор М." // подпись — снимок для журнала (загрузчик сессии ставит всегда)
	path := "/v1/geo/cities/" + e.stav.String()

	key := uuid.NewString()
	rec := e.send(t, http.MethodPatch, "admin", path, key, `{"status":"live","slug":"stav","names":{"en":"Stavropol City"}}`, "en")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Language") != "en" || rec.Header().Get("Vary") != "Accept-Language" {
		t.Fatalf("правка: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	c := decode[oapi.GeoCityDetails](t, rec)
	if c.Status != "live" || c.Slug != "stav" || c.Name != "Stavropol City" || len(c.SlugHistory) != 1 || c.Version < 4 {
		t.Fatalf("карточка: %+v", c)
	}
	again := e.send(t, http.MethodPatch, "admin", path, key, `{"status":"live","slug":"stav","names":{"en":"Stavropol City"}}`, "en")
	if again.Code != http.StatusOK || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("повтор: %d %s", again.Code, again.Body.String())
	}
	var events int
	if err := e.owner.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE aggregate_id = $1`, e.stav).Scan(&events); err != nil || events != 3 {
		t.Fatalf("событий %d %v", events, err)
	}

	// коды сценария
	wantProblem(t, e.send(t, http.MethodPatch, "admin", path, "", `{"slug":"mihaylovsk"}`, ""), http.StatusConflict, geo.CodeSlugTaken)
	wantProblem(t, e.send(t, http.MethodPatch, "admin", "/v1/geo/cities/"+uuid.NewString(), "", `{"status":"live"}`, ""),
		http.StatusNotFound, geo.CodeCityNotFound)
	wantField(t, e.send(t, http.MethodPatch, "admin", path, "", `{"names":{"ru":null}}`, ""), "body.names.ru", "required")
	wantField(t, e.send(t, http.MethodPatch, "admin", path, "", `{"names":{"de":"Stawropol"}}`, ""), "body.names.de", "enum")

	// модератор своего города — названия да, статус нет; чужого — 403 сценария (конвейер handler пропустил)
	if rec := e.send(t, http.MethodPatch, "stav-mod", path, "", `{"names":{"en":"Stavropol"}}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("модератор своего: %d %s", rec.Code, rec.Body.String())
	}
	wantProblem(t, e.send(t, http.MethodPatch, "stav-mod", path, "", `{"status":"pilot"}`, ""), http.StatusForbidden, "auth.forbidden")
	wantProblem(t, e.send(t, http.MethodPatch, "mih-mod", path, "", `{"names":{"en":"Hacked"}}`, ""), http.StatusForbidden, "auth.forbidden")
	wantProblem(t, serve(e.h, call(http.MethodPatch, path, "support", `{"names":{"en":"Hacked"}}`)), http.StatusForbidden, "auth.forbidden")
	var hacked int
	if err := e.owner.QueryRow(context.Background(), `SELECT count(*) FROM city_names WHERE name = 'Hacked'`).Scan(&hacked); err != nil || hacked != 0 {
		t.Fatalf("записано при отказе: %d %v", hacked, err)
	}
}

func TestLinkGeoCityHTTP(t *testing.T) {
	mod := staffOf(perm.Moderator, auth.StageActive, false)
	e := newGeoEnv(t, map[string]*auth.Staff{"admin": activator(), "mod": mod})
	e.seedPlace(t)
	var cid uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO cities (country_id, name, slug, timezone, centroid)
		SELECT id, 'Михайловск', 'mikh-unlinked', 'Europe/Moscow', ST_SetSRID(ST_MakePoint(40, 50), 4326)::geography
		FROM countries WHERE code = 'RU' RETURNING id`).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	mod.CityIDs = []uuid.UUID{cid}
	path := "/v1/geo/cities/" + cid.String() + "/link"
	key := uuid.NewString()
	rec := e.send(t, http.MethodPost, "admin", path, key, `{"geoname_id":526815}`, "ru")
	if rec.Code != http.StatusOK {
		t.Fatalf("привязка: %d %s", rec.Code, rec.Body.String())
	}
	if c := decode[oapi.GeoCityDetails](t, rec); c.GeonameId == nil || *c.GeonameId != 526815 || c.Version != 2 {
		t.Fatalf("карточка: %+v", c)
	}
	if again := e.send(t, http.MethodPost, "admin", path, key, `{"geoname_id":526815}`, "ru"); again.Code != http.StatusOK ||
		again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("повтор: %d %s", again.Code, again.Body.String())
	}
	wantProblem(t, e.send(t, http.MethodPost, "admin", path, "", `{"geoname_id":526815}`, ""), http.StatusConflict, geo.CodeCityAlreadyLinked)
	wantProblem(t, e.send(t, http.MethodPost, "admin", "/v1/geo/cities/"+e.stav.String()+"/link", "", `{"geoname_id":526815}`, ""),
		http.StatusConflict, geo.CodeCityAlreadyLinked)
	wantProblem(t, e.send(t, http.MethodPost, "admin", "/v1/geo/cities/"+uuid.NewString()+"/link", "", `{"geoname_id":526815}`, ""),
		http.StatusNotFound, geo.CodeCityNotFound)
	// модератор (даже своего города) — 403 конвейера: geo.cities.manage у него deny
	wantProblem(t, serve(e.h, call(http.MethodPost, path, "mod", `{"geoname_id":526815}`)), http.StatusForbidden, "auth.forbidden")
}
