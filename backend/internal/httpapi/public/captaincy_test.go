package public_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Капитанство игрока через весь конвейер public (план I-4, Task 3): каждая операция — успех по контракту и код
// ошибки, Cache-Control по контракту (CachePrivate), повтор подачи с тем же Idempotency-Key — тот же ответ, чужая
// заявка неотличима от несуществующей.

// captaincyCity — живой город включённой страны (порог 14) для заявок; id.
func captaincyCity(t *testing.T, e *coreEnv) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `WITH c AS (INSERT INTO countries (code, name, currency, default_locale,
		phone_prefix, week_starts_on, min_signup_age, age_of_majority, is_enabled)
		VALUES ('QC', 'Страна QC', 'XXX', 'ru', '+998', 1, 14, 18, true) RETURNING id)
		INSERT INTO cities (country_id, name, slug, timezone, status, centroid)
		SELECT id, 'Город QC', 'qc-city', 'Europe/Moscow', 'live', ST_SetSRID(ST_MakePoint(41.9, 45.0), 4326)::geography FROM c
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// captaincyPlayer — игрок в городе city с живой сессией; id и access-токен.
func captaincyPlayer(t *testing.T, e *coreEnv, nick string, city uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	uid := e.seedUser(t, nick, corePW1)
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET city_id = $2 WHERE id = $1`, uid, city); err != nil {
		t.Fatal(err)
	}
	return uid, e.session(t, uid)
}

// wantCode — статус и код Problem ответа.
func wantCode(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || problemCodeOf(t, rec) != code {
		t.Fatalf("%s: ждали %d %s, получили %d %s", what, status, code, rec.Code, rec.Body.String())
	}
}

func wantPrivate(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("%s: Cache-Control %q", what, got)
	}
}

func TestCaptaincyHTTP(t *testing.T) {
	t.Parallel()
	e := newCoreEnv(t)
	city := captaincyCity(t, e)
	_, a := captaincyPlayer(t, e, "cap_http_a", city)
	_, b := captaincyPlayer(t, e, "cap_http_b", city)

	// getMyCaptaincy: можно подать
	rec := e.do(t, http.MethodGet, "/v1/me/captaincy", "", a)
	m := e.ok(t, "getMyCaptaincy", rec)
	wantPrivate(t, "getMyCaptaincy", rec)
	if el := m["eligibility"].(map[string]any); el["eligible"] != true || el["city_id"] != city.String() {
		t.Fatalf("eligibility %v", el)
	}

	// управляющие и невидимые символы в форме — 400 по полю, а не 500 (I1 ревью Task 3)
	for field, bad := range map[string]string{"body.note": `{"note":"a\u0000b"}`,
		"body.contact_link": `{"contact_link":"https://t.me/\u202emoc.live"}`} {
		rec := e.do(t, http.MethodPost, "/v1/captain-applications", bad, a)
		wantCode(t, field, rec, http.StatusBadRequest, "validation.failed")
		if !strings.Contains(rec.Body.String(), `"`+field+`"`) {
			t.Errorf("%s: поле в ответе: %s", field, rec.Body.String())
		}
	}

	// submitCaptainApplication: 201, повтор с тем же ключом — тот же ответ
	body := `{"proposed_format":"6×6 по средам","contact_link":"https://t.me/kosta_10","phone":"+79181234567"}`
	key := uuid.NewString()
	submit := func() *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/captain-applications", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+a)
		r.Header.Set("Idempotency-Key", key)
		return e.v.Do(t, e.h, r)
	}
	first := submit()
	if first.Code != http.StatusCreated {
		t.Fatalf("submit: %d %s", first.Code, first.Body.String())
	}
	wantPrivate(t, "submit", first)
	var app map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	if app["status"] != "pending" || app["has_phone"] != true || app["city_id"] != city.String() {
		t.Fatalf("заявка %v", app)
	}
	if _, ok := app["phone"]; ok || strings.Contains(first.Body.String(), "9181234567") {
		t.Fatalf("телефон в ответе: %s", first.Body.String())
	}
	// повтор — тоже по контракту: Cache-Control сохраняется вместе с ответом (idempotency.savedHeaders)
	replay := submit()
	var again map[string]any
	if err := json.Unmarshal(replay.Body.Bytes(), &again); err != nil || replay.Code != http.StatusCreated ||
		!reflect.DeepEqual(again, app) || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("повтор с тем же ключом: %d %s", replay.Code, replay.Body.String())
	}
	if n := count(t, e.pool, `SELECT count(*) FROM captain_applications`); n != 1 {
		t.Fatalf("заявок %d после повтора", n)
	}
	appID := app["id"].(string)

	// вторая подача — application_exists
	wantCode(t, "submit again", e.do(t, http.MethodPost, "/v1/captain-applications", `{}`, a),
		http.StatusConflict, "identity.application_exists")

	// getMyCaptaincy: открытая заявка
	m = e.ok(t, "getMyCaptaincy open", e.do(t, http.MethodGet, "/v1/me/captaincy", "", a))
	if open, _ := m["open_application"].(map[string]any); open["id"] != appID ||
		m["eligibility"].(map[string]any)["eligible"] != false {
		t.Fatalf("экран с открытой заявкой: %v", m)
	}

	// withdraw: чужая — как несуществующая
	foreign := e.do(t, http.MethodPost, "/v1/captain-applications/"+appID+"/withdraw", "", b)
	missing := e.do(t, http.MethodPost, "/v1/captain-applications/"+uuid.Must(uuid.NewV7()).String()+"/withdraw", "", b)
	wantCode(t, "withdraw чужой", foreign, http.StatusNotFound, "identity.application_not_found")
	wantCode(t, "withdraw несуществующей", missing, http.StatusNotFound, "identity.application_not_found")
	if foreign.Body.Len() == 0 || foreign.Header().Get("Content-Type") != missing.Header().Get("Content-Type") {
		t.Error("чужая и несуществующая отвечают по-разному")
	}
	if n := count(t, e.pool, `SELECT count(*) FROM captain_applications WHERE id = $1 AND status = 'pending'`,
		uuid.MustParse(appID)); n != 1 {
		t.Fatal("чужой отзыв тронул заявку")
	}
	rec = e.do(t, http.MethodPost, "/v1/captain-applications/"+appID+"/withdraw", "", a)
	m = e.ok(t, "withdraw", rec)
	wantPrivate(t, "withdraw", rec)
	if m["status"] != "withdrawn" || m["decided_at"] == nil {
		t.Fatalf("отзыв %v", m)
	}
	wantCode(t, "withdraw again", e.do(t, http.MethodPost, "/v1/captain-applications/"+appID+"/withdraw", "", a),
		http.StatusConflict, "identity.application_closed")

	// listMyCaptainApplications
	rec = e.do(t, http.MethodGet, "/v1/me/captain-applications?limit=1", "", a)
	m = e.ok(t, "list", rec)
	wantPrivate(t, "list", rec)
	if items := m["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != appID || m["next_cursor"] != nil {
		t.Fatalf("история %v", m)
	}
	if m = e.ok(t, "list b", e.do(t, http.MethodGet, "/v1/me/captain-applications", "", b)); len(m["items"].([]any)) != 0 {
		t.Fatalf("чужая история видна: %v", m)
	}
	rec = e.do(t, http.MethodGet, "/v1/me/captain-applications?cursor=bad", "", a)
	wantCode(t, "испорченный курсор", rec, http.StatusBadRequest, "validation.failed")
	if !strings.Contains(rec.Body.String(), `"query.cursor"`) {
		t.Errorf("поле курсора: %s", rec.Body.String())
	}
}
