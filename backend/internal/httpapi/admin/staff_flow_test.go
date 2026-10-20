package admin_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
)

// Нулевой хендлер staff (тесты конвейера; cmd/admin-api собирает настоящий): операции тега staff за полным конвейером
// отвечают 501 http.not_implemented — без паники (recover дал бы 500) и до сценариев.
func TestStaffZeroHandler(t *testing.T) {
	o := testOptions(t)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{
		"pending": staffOf(perm.Owner, auth.StageMFAPending, false),
		"active":  staffOf(perm.Owner, auth.StageActive, false),
	}}
	h := newHandler(t, o)
	cases := []struct{ method, path, token, body string }{
		{http.MethodPost, "/v1/staff/auth/login", "", `{"email":"a@wf.example","password":"x"}`},
		{http.MethodPost, "/v1/staff/auth/invite/accept", "", `{"email":"a@wf.example","code":"k7m2p-x4q6d","password":"Жёлтая карточка на 90-й"}`},
		{http.MethodPost, "/v1/staff/auth/mfa", "pending", `{"code":"123456"}`},
		{http.MethodPost, "/v1/staff/auth/logout", "active", ""},
		{http.MethodGet, "/v1/staff/me", "active", ""},
		{http.MethodPost, "/v1/staff/me/sessions/revoke-others", "active", ""},
	}
	for _, c := range cases {
		wantPassed(t, serve(h, call(c.method, c.path, c.token, c.body)))
	}
}

// Первый вход (§4.4–4.5): приглашение оператора → принятие (сессия setup, новая cookie) → секрет →
// подтверждение кодом (10 кодов) → ПЕРЕЗАГРУЗКА: me — setup с сессией, «Мой доступ» закрыт стадией
// (403 setup_required — getStaffMfa это self), повторный выпуск секрета — wrong_stage (так админка узнаёт,
// что TOTP уже подтверждён), ack с той же cookie проходит → новый токен, стадия active; старый токен
// после ack — 401 без стирания новой cookie; getStaffMfa — confirmed_at и 10 кодов.
func TestFirstLogin(t *testing.T) {
	e := newStaffEnv(t)
	id := e.invite("olga.v@wf.example", string(perm.Admin))
	rec := e.anon("/v1/staff/auth/invite/accept", "", jsonOf(t, map[string]string{
		"email": " Olga.V@wf.example ", "code": "K7M2P X4Q6D", "password": staffPassword,
	}))
	wantStatus(t, rec, http.StatusOK)
	if got := bodyOf(t, rec)["stage"]; got != auth.StageSetup {
		t.Fatalf("stage %v", got)
	}
	setup := newToken(t, rec)

	me := bodyOf(t, wantOK(t, e.get("/v1/staff/me", setup)))
	if me["stage"] != auth.StageSetup || me["id"] != id.String() || me["email"] != "olga.v@wf.example" ||
		me["role"] != string(perm.Admin) || me["locale"] != "ru" {
		t.Fatalf("me в setup: %v", me)
	}
	wantEmptyAccess(t, me)
	sess, _ := me["session"].(map[string]any)
	if sess == nil || sess["id"] == nil || sess["expires_at"] != t0.Add(8*time.Hour).Format(time.RFC3339) {
		t.Fatalf("сессия в setup: %v", me["session"])
	}

	rec = e.post("/v1/staff/me/mfa/setup", setup, "")
	wantStatus(t, rec, http.StatusOK)
	wantNoStore(t, rec)
	secret, _ := bodyOf(t, rec)["secret"].(string)
	if uri, _ := bodyOf(t, rec)["otpauth_uri"].(string); secret == "" || uri == "" {
		t.Fatalf("секрет: %s", rec.Body.String())
	}
	// неверный код настройки — mfa_invalid без attempts_left (в блокировку не считается)
	rec = e.post("/v1/staff/me/mfa/confirm", setup, jsonOf(t, map[string]string{"code": "000000"}))
	if totp(t, secret, e.clock.Now()) != "000000" {
		wantProblem(t, rec, http.StatusBadRequest, staff.CodeMFAInvalid)
		if _, ok := bodyOf(t, rec)["attempts_left"]; ok {
			t.Fatalf("attempts_left в настройке: %s", rec.Body.String())
		}
	}
	rec = e.post("/v1/staff/me/mfa/confirm", setup, jsonOf(t, map[string]string{"code": totp(t, secret, e.clock.Now())}))
	wantStatus(t, rec, http.StatusOK)
	wantNoStore(t, rec)
	var codes struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &codes); err != nil || len(codes.RecoveryCodes) != 10 {
		t.Fatalf("коды: %s", rec.Body.String())
	}

	// перезагрузка страницы между confirm и ack
	me = bodyOf(t, wantOK(t, e.get("/v1/staff/me", setup)))
	if me["stage"] != auth.StageSetup || me["session"] == nil {
		t.Fatalf("me после перезагрузки: %v", me)
	}
	wantProblem(t, e.get("/v1/staff/me/mfa", setup), http.StatusForbidden, httpx.CodeSetupRequired)
	wantProblem(t, e.post("/v1/staff/me/mfa/setup", setup, ""), http.StatusConflict, staff.CodeWrongStage)

	rec = e.post("/v1/staff/me/mfa/recovery-codes/ack", setup, "")
	wantStatus(t, rec, http.StatusNoContent)
	active := newToken(t, rec)
	if active == setup || len(setCookies(rec)) != 1 {
		t.Fatalf("ack: токен не сменился или лишний Set-Cookie %q", setCookies(rec))
	}
	// параллельный запрос со старым токеном — 401 без стирания новой cookie
	rec = e.get("/v1/staff/me", setup)
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	noCookie(t, rec)

	me = bodyOf(t, wantOK(t, e.get("/v1/staff/me", active)))
	if me["stage"] != auth.StageActive || me["reauth_required"] != false {
		t.Fatalf("me после ack: %v", me)
	}
	if p, _ := me["permissions"].(map[string]any); p["staff.roles.grant"] != "dual" || p["staff.roles.read"] != "allow" {
		t.Fatalf("права админа: %v", me["permissions"])
	}
	mfa := bodyOf(t, wantOK(t, e.get("/v1/staff/me/mfa", active)))
	if mfa["confirmed_at"] != t0.Format(time.RFC3339) || mfa["recovery_codes_left"] != float64(10) {
		t.Fatalf("mfa: %v", mfa)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND status = 'active'`, id); n != 1 {
		t.Fatal("учётка не active")
	}
}

// wantOK — 200, ответ.
func wantOK(t *testing.T, rec *httpRec) *httpRec {
	t.Helper()
	wantStatus(t, rec, http.StatusOK)
	return rec
}

// wantNoStore — ответ с секретом не кэшируется (D14).
func wantNoStore(t *testing.T, rec *httpRec) {
	t.Helper()
	if got := rec.Header().Values("Cache-Control"); !slices.Equal(got, []string{"no-store"}) {
		t.Fatalf("Cache-Control %q", got)
	}
}

// wantEmptyAccess — city_ids — пустой массив, permissions — пустой объект (не null, ⚠️ 9a).
func wantEmptyAccess(t *testing.T, me map[string]any) {
	t.Helper()
	cities, ok := me["city_ids"].([]any)
	perms, ok2 := me["permissions"].(map[string]any)
	if !ok || !ok2 || len(cities) != 0 || len(perms) != 0 {
		t.Fatalf("city_ids %v, permissions %v", me["city_ids"], me["permissions"])
	}
}
