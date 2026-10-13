package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/clocktest"
)

// Имя cookie — одно место (R6): с Secure — __Host-wf_admin_session, без (dev, если браузер не
// принял __Host- на http://127.0.0.1) — wf_admin_session.
func TestCookieName(t *testing.T) {
	if admin.CookieName(true) != hostCookie || admin.CookieName(false) != "wf_admin_session" {
		t.Fatalf("CookieName: %q %q", admin.CookieName(true), admin.CookieName(false))
	}
}

// Сессия по cookie: загрузчик получает токен, IP клиента и признак фонового запроса
// (X-WF-Background: 1 не продлевает сессию, D10); сотрудник доходит до хендлера.
func TestStaffSessionLoadsByCookie(t *testing.T) {
	o := testOptions(t)
	sessions := &fakeSessions{staff: map[string]*auth.Staff{"tok-owner": staffOf(perm.Owner, auth.StageActive, false)}}
	o.Sessions = sessions
	h := newHandler(t, o)
	wantPassed(t, serve(h, call(http.MethodGet, "/v1/staff/me", "tok-owner", "")))
	bg := call(http.MethodGet, "/v1/staff/me", "tok-owner", "")
	bg.Header.Set("X-WF-Background", "1")
	wantPassed(t, serve(h, bg))
	want := []loadCall{
		{"tok-owner", netip.MustParseAddr(clientIP), false},
		{"tok-owner", netip.MustParseAddr(clientIP), true},
	}
	if got := sessions.loads(); !slices.Equal(got, want) {
		t.Fatalf("загрузки %+v, ждали %+v", got, want)
	}
	// с Secure dev-имя не читается: запрос анонимный — 401 от валидатора
	plain := call(http.MethodGet, "/v1/staff/me", "", "")
	plain.AddCookie(sessionCookie("wf_admin_session", "tok-owner"))
	wantProblem(t, serve(h, plain), http.StatusUnauthorized, httpx.CodeUnauthenticated)
}

// Dev без Secure (R6 не прошла бы): имя wf_admin_session, __Host- не читается.
func TestStaffSessionDevCookie(t *testing.T) {
	o := testOptions(t)
	o.CookieSecure = false
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok": staffOf(perm.Owner, auth.StageActive, false)}}
	h := newHandler(t, o)
	r := call(http.MethodGet, "/v1/staff/me", "", "")
	r.AddCookie(sessionCookie("wf_admin_session", "tok"))
	wantPassed(t, serve(h, r))
	wantProblem(t, serve(h, call(http.MethodGet, "/v1/staff/me", "tok", "")), http.StatusUnauthorized, httpx.CodeUnauthenticated)
}

// Сотрудник кладётся в ctx до rate limit (⚠️ 3a): лимит считает его ключом staff:<класс>:<id>.
func TestStaffPrincipalBeforeRateLimit(t *testing.T) {
	o := testOptions(t)
	s := staffOf(perm.Owner, auth.StageActive, false)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok": s}}
	lim := &recLimiter{}
	o.Limiter = lim
	wantPassed(t, serve(newHandler(t, o), call(http.MethodGet, "/v1/staff/me", "tok", "")))
	want := ratelimit.SubjectKey(ratelimit.DefaultClass, auth.Subject{Kind: auth.SubjectStaff, ID: s.StaffID})
	if !slices.Contains(lim.allows, want) {
		t.Fatalf("ключи лимита %v, нет %s", lim.allows, want)
	}
}

// Review Focus 4: перебор cookie мимо лимита невозможен. Невалидные токены на любых ручках
// (GET /v1/staff/me — session, GET /v1/staff/accounts — ключ каталога) считаются в IP-лимите
// класса staff_auth и упираются в 429; каждый отказ стирает cookie. После 429 загрузчик не
// вызывается: сессия не проверяется вовсе.
func TestInvalidCookiesHitStaffAuthLimit(t *testing.T) {
	o := testOptions(t)
	clk := clocktest.New(t0)
	o.Clock = clk
	o.Limiter = ratelimit.NewPG(o.DB, clk)
	sessions := &fakeSessions{}
	o.Sessions = sessions
	h := newHandler(t, o)
	paths := []string{"/v1/staff/me", "/v1/staff/accounts"}
	rejected := 0
	for i := range 100 {
		rec := serve(h, call(http.MethodGet, paths[i%2], "bad-"+uuid.NewString(), ""))
		if rec.Code == http.StatusTooManyRequests {
			wantProblem(t, rec, http.StatusTooManyRequests, httpx.CodeRateLimited)
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("429 без Retry-After")
			}
			break
		}
		wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
		if !clearsCookie(rec) {
			t.Fatalf("отказ %d не стёр cookie: %v", i, rec.Header().Values("Set-Cookie"))
		}
		rejected++
	}
	burst := ratelimit.DefaultRules()["staff_auth"].IP.Burst
	if rejected == 0 || rejected > burst+1 {
		t.Fatalf("до 429 отказов %d, всплеск staff_auth %d", rejected, burst)
	}
	loads := len(sessions.loads())
	if loads != rejected {
		t.Fatalf("загрузок %d, отказов %d", loads, rejected)
	}
	// лимит исчерпан — следующий токен даже не загружается, и на другой ручке тоже
	wantProblem(t, serve(h, call(http.MethodGet, "/v1/staff/me", "bad-next", "")), http.StatusTooManyRequests, httpx.CodeRateLimited)
	if len(sessions.loads()) != loads {
		t.Fatal("после 429 загрузчик вызван")
	}
	// счётчик — IP-ключ класса staff_auth: тот же, что у ratelimit.Middleware
	var n int
	if err := o.DB.QueryRow(context.Background(), `SELECT count(*) FROM rate_limits WHERE key = $1`,
		ratelimit.IPKey("staff_auth", netip.MustParseAddr(clientIP))).Scan(&n); err != nil || n != 1 {
		t.Fatalf("строка лимита ip:staff_auth: %d, %v", n, err)
	}
}

// clearsCookie — ответ стирает cookie сессии: Max-Age=0 с теми же атрибутами, что у установки.
func clearsCookie(rec *httptest.ResponseRecorder) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == hostCookie && c.Value == "" && c.MaxAge < 0 && c.Path == "/" && c.HttpOnly && c.Secure &&
			c.SameSite == http.SameSiteStrictMode {
			return true
		}
	}
	return false
}

// Негативный кэш (R9): отказ по токену помнится 5 с по хэшу токена — повтор того же токена не
// ходит в загрузчик, но считается в IP-лимите и снова стирает cookie; через 5 с — загрузка заново.
func TestInvalidTokenNegativeCache(t *testing.T) {
	o := testOptions(t)
	clk := clocktest.New(t0)
	o.Clock = clk
	lim := &recLimiter{}
	o.Limiter = lim
	sessions := &fakeSessions{}
	o.Sessions = sessions
	h := newHandler(t, o)
	for range 2 {
		rec := serve(h, call(http.MethodGet, "/v1/staff/me", "bad", ""))
		wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
		if !clearsCookie(rec) {
			t.Fatal("отказ не стёр cookie")
		}
	}
	if n := len(sessions.loads()); n != 1 {
		t.Fatalf("загрузок %d, ждали 1 (второй — из кэша)", n)
	}
	key := ratelimit.IPKey("staff_auth", netip.MustParseAddr(clientIP))
	if !slices.Equal(lim.adds, []string{key, key}) {
		t.Fatalf("Add %v, ждали дважды %s", lim.adds, key)
	}
	clk.Advance(5 * time.Second)
	wantProblem(t, serve(h, call(http.MethodGet, "/v1/staff/me", "bad", "")), http.StatusUnauthorized, httpx.CodeUnauthenticated)
	if n := len(sessions.loads()); n != 2 {
		t.Fatalf("загрузок %d, ждали 2 (кэш истёк)", n)
	}
}

// Положительного кэша нет (R9): действующая сессия сверяется с загрузчиком на каждом запросе.
func TestValidSessionNotCached(t *testing.T) {
	o := testOptions(t)
	sessions := &fakeSessions{staff: map[string]*auth.Staff{"tok": staffOf(perm.Owner, auth.StageActive, false)}}
	o.Sessions = sessions
	h := newHandler(t, o)
	for range 3 {
		wantPassed(t, serve(h, call(http.MethodGet, "/v1/staff/me", "tok", "")))
	}
	if n := len(sessions.loads()); n != 3 {
		t.Fatalf("загрузок %d, ждали 3", n)
	}
}

// Невалидная cookie на анонимной операции (вход) — запрос идёт дальше анонимно, cookie стирается.
func TestInvalidCookieOnAnonymousOperation(t *testing.T) {
	r := loginRequest()
	r.AddCookie(sessionCookie(hostCookie, "stale"))
	rec := serve(newHandler(t, testOptions(t)), r)
	wantPassed(t, rec)
	if !clearsCookie(rec) {
		t.Fatal("cookie не стёрта")
	}
}

// Сбой загрузчика (база недоступна) — 500, cookie не стирается и отказ не считается: сбой — не
// повод выкидывать сотрудника.
func TestSessionLoadFailureIs500(t *testing.T) {
	o := testOptions(t)
	o.Sessions = &fakeSessions{err: errors.New("база недоступна")}
	lim := &recLimiter{}
	o.Limiter = lim
	rec := serve(newHandler(t, o), call(http.MethodGet, "/v1/staff/me", "tok", ""))
	wantProblem(t, rec, http.StatusInternalServerError, httpx.CodeInternal)
	if len(rec.Result().Cookies()) != 0 || len(lim.adds) != 0 {
		t.Fatalf("cookie %v, Add %v", rec.Result().Cookies(), lim.adds)
	}
}

// Стадии сессии по x-permission (спека §5.1): session — любая стадия; self и ключ каталога —
// только active без Reauth: mfa_pending → 401 auth.unauthenticated, setup → 403
// auth.setup_required, Reauth → 401 auth.reauth_required.
func TestPermissionStages(t *testing.T) {
	type want struct {
		status int
		code   string
	}
	pass := want{http.StatusNotImplemented, httpx.CodeNotImplemented}
	unauth := want{http.StatusUnauthorized, httpx.CodeUnauthenticated}
	setup := want{http.StatusForbidden, httpx.CodeSetupRequired}
	reauth := want{http.StatusUnauthorized, httpx.CodeReauthRequired}
	stages := []struct {
		name   string
		stage  string
		reauth bool
	}{
		{"mfa_pending", auth.StageMFAPending, false},
		{"setup", auth.StageSetup, false},
		{"reauth", auth.StageActive, true},
		{"active", auth.StageActive, false},
	}
	ops := []struct {
		name string
		path string // GET
		want []want // по stages
	}{
		{"session getStaffMe", "/v1/staff/me", []want{pass, pass, pass, pass}},
		{"self listStaffMySessions", "/v1/staff/me/sessions", []want{unauth, setup, reauth, pass}},
		{"ключ listStaffAccounts", "/v1/staff/accounts", []want{unauth, setup, reauth, pass}},
	}
	v := apitest.New(t, oapi.GetSpec)
	o := testOptions(t)
	tokens := map[string]*auth.Staff{}
	for _, s := range stages {
		tokens[s.name] = staffOf(perm.Owner, s.stage, s.reauth)
	}
	o.Sessions = &fakeSessions{staff: tokens}
	h := newHandler(t, o)
	for _, op := range ops {
		for i, s := range stages {
			rec := v.Do(t, h, call(http.MethodGet, op.path, s.name, ""))
			w := op.want[i]
			if rec.Code != w.status || !strings.Contains(rec.Body.String(), `"code":"`+w.code+`"`) {
				t.Errorf("%s, %s: %d %s, ждали %d %s", op.name, s.name, rec.Code, rec.Body.String(), w.status, w.code)
			}
		}
	}
}

// Ключ каталога: нет права у роли — 403 auth.forbidden (модератору список сотрудников закрыт);
// scoped без {cityId} в пути — пропуск, список фильтрует ручка (модератор и города).
func TestPermissionKey(t *testing.T) {
	o := testOptions(t)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{
		"mod": staffOf(perm.Moderator, auth.StageActive, false, cityA),
	}}
	h := newHandler(t, o)
	wantProblem(t, serve(h, call(http.MethodGet, "/v1/staff/accounts", "mod", "")), http.StatusForbidden, httpx.CodeForbidden)
	wantPassed(t, serve(h, call(http.MethodGet, "/v1/geo/cities", "mod", "")))
}

// handler: хоть одно право из x-permission-any не Deny — пропуск (точное право и город решает
// сценарий); все Deny — 403 auth.forbidden. Решение подтверждения — по правам «вдвоём».
func TestPermissionHandler(t *testing.T) {
	scoped := staffOf(perm.Support, auth.StageActive, false)
	scoped.Grants = map[string]auth.Grant{"staff.access.reset": auth.GrantScoped}
	o := testOptions(t)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{
		"one":  scoped,
		"none": staffOf(perm.Support, auth.StageActive, false),
	}}
	h := newHandler(t, o)
	path := "/v1/approvals/" + uuid.NewString() + "/approve"
	wantPassed(t, serve(h, call(http.MethodPost, path, "one", "")))
	wantProblem(t, serve(h, call(http.MethodPost, path, "none", "")), http.StatusForbidden, httpx.CodeForbidden)
}

// CSRF (спека §5.1): изменяющий запрос без Origin, с чужим Origin, без X-WF-Admin или с другим
// значением — 403 auth.csrf_failed до разбора тела (битый JSON не даёт 400) и до входа (анонимная
// мутация тоже). GET без заголовков — пропуск.
func TestCSRF(t *testing.T) {
	o := testOptions(t)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok": staffOf(perm.Owner, auth.StageActive, false)}}
	h := newHandler(t, o)
	for name, mutate := range map[string]func(*http.Request){
		"без Origin":          func(r *http.Request) { r.Header.Del("Origin") },
		"чужой Origin":        func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
		"Origin с путём":      func(r *http.Request) { r.Header.Set("Origin", testOrigin+"/") },
		"Origin null":         func(r *http.Request) { r.Header.Set("Origin", "null") },
		"два Origin":          func(r *http.Request) { r.Header.Add("Origin", "https://evil.example") },
		"без X-WF-Admin":      func(r *http.Request) { r.Header.Del("X-WF-Admin") },
		"X-WF-Admin: 0":       func(r *http.Request) { r.Header.Set("X-WF-Admin", "0") },
		"битое тело, чужой":   func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
		"с сессией, без всех": func(r *http.Request) { r.Header.Del("Origin"); r.Header.Del("X-WF-Admin") },
	} {
		body := `{"email":"olga.v@wf.example","password":"Жёлтая карточка на 90-й"}`
		if strings.HasPrefix(name, "битое") {
			body = `{"email":`
		}
		r := call(http.MethodPost, "/v1/staff/auth/login", "", body)
		if strings.HasPrefix(name, "с сессией") {
			r = call(http.MethodPost, "/v1/staff/me/sessions/revoke-others", "tok", "")
		}
		mutate(r)
		rec := serve(h, r)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"auth.csrf_failed"`) {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// всё на месте — вход доходит до хендлера; GET без Origin и X-WF-Admin — пропуск
	wantPassed(t, serve(h, loginRequest()))
	wantPassed(t, serve(h, call(http.MethodGet, "/v1/staff/me", "tok", "")))
}

// IP-аллоулист: пустой — выключен; адрес вне списка — 403 auth.ip_not_allowed на всё, включая
// /docs и статику; из списка — дальше по конвейеру.
func TestIPAllowlist(t *testing.T) {
	o := testOptions(t)
	o.Docs = true
	o.StaticDir = staticDir(t)
	o.IPAllowlist = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}
	h := newHandler(t, o)
	for _, path := range []string{"/v1/staff/me", "/docs", "/", "/assets/app.js", "/v1/nope"} {
		wantProblem(t, serve(h, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)),
			http.StatusForbidden, httpx.CodeIPNotAllowed)
	}
	wantProblem(t, serve(h, loginRequest()), http.StatusForbidden, httpx.CodeIPNotAllowed)
	for _, addr := range []string{"10.1.2.3:5000", "[2001:db8::7]:5000"} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/staff/me", nil)
		r.RemoteAddr = addr
		wantProblem(t, serve(h, r), http.StatusUnauthorized, httpx.CodeUnauthenticated)
		r = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		if rec := serve(h, r); rec.Code != http.StatusOK {
			t.Fatalf("%s: статика %d", addr, rec.Code)
		}
	}
	// пустой список — выключен
	wantProblem(t, serve(newHandler(t, testOptions(t)), call(http.MethodGet, "/v1/staff/me", "", "")),
		http.StatusUnauthorized, httpx.CodeUnauthenticated)
}

// Ответы маршрутов контракта — Cache-Control: no-store (данные сотрудника и отказы не кэшируются);
// хендлер может поставить свой (private, no-store) поверх.
func TestContractResponsesNoStore(t *testing.T) {
	o := testOptions(t)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok": staffOf(perm.Owner, auth.StageActive, false)}}
	h := newHandler(t, o)
	for _, r := range []*http.Request{
		call(http.MethodGet, "/v1/staff/me", "tok", ""),
		call(http.MethodGet, "/v1/staff/me", "", ""),
		loginRequest(),
	} {
		if rec := serve(h, r); rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s: Cache-Control %q", r.Method, r.URL.Path, rec.Header().Get("Cache-Control"))
		}
	}
}

// Несколько строк Accept-Language сводятся в одну, как в public (httpx.CombineHeaders):
// сгенерированный код принимает у заголовка-параметра одну строку и на вторую ответил бы 400.
func TestAcceptLanguageCombined(t *testing.T) {
	o := testOptions(t)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok": staffOf(perm.Owner, auth.StageActive, false)}}
	r := call(http.MethodGet, "/v1/geo/cities", "tok", "")
	r.Header.Add("Accept-Language", "ru-RU")
	r.Header.Add("Accept-Language", "en;q=0.5")
	wantPassed(t, serve(newHandler(t, o), r))
}

// Неверный ADMIN_ORIGIN и нулевой префикс в ADMIN_IP_ALLOWLIST — отказ старта.
func TestNewHandlerRejectsBadOriginAndAllowlist(t *testing.T) {
	base := testOptions(t)
	for _, origin := range []string{"", "127.0.0.1:5173", "ftp://admin.wf.example", "https://admin.wf.example/",
		"https://admin.wf.example/app", "https://admin.wf.example?x=1", "https://user@admin.wf.example",
		"https://Admin.wf.example", "https://admin.wf.example:443", "http://127.0.0.1:80", "https://", "https://admin.wf.example#f"} {
		o := base
		o.Origin = origin
		if _, err := admin.NewHandler(quiet(), o); err == nil {
			t.Errorf("origin %q: собрано", origin)
		}
		if err := admin.ValidateOrigin(origin); err == nil {
			t.Errorf("ValidateOrigin(%q): nil", origin)
		}
	}
	for _, origin := range []string{"https://admin.wf.example", "http://127.0.0.1:5173", "https://[::1]:8443"} {
		if err := admin.ValidateOrigin(origin); err != nil {
			t.Errorf("ValidateOrigin(%q): %v", origin, err)
		}
	}
	for _, nets := range [][]netip.Prefix{
		{netip.MustParsePrefix("0.0.0.0/0")},
		{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::/0")},
		{netip.MustParsePrefix("10.0.0.0/8"), {}},
	} {
		o := base
		o.IPAllowlist = nets
		if _, err := admin.NewHandler(quiet(), o); err == nil {
			t.Errorf("аллоулист %v: собрано", nets)
		}
		if err := admin.ValidateAllowlist(nets); err == nil {
			t.Errorf("ValidateAllowlist(%v): nil", nets)
		}
	}
}

// Защита в глубину: chi сопоставляет {staffId} с пустым сегментом, а маршрутизатор контракта —
// нет, поэтому httpx.Route не ставится и звенья конвейера пропущены. Запрос не доходит до
// хендлера: 404 http.not_found, а не 400 биндинга параметра (и не 501 заглушки) — и с сессией
// владельца, и без неё.
func TestEmptyPathParamNeverReachesHandler(t *testing.T) {
	o := testOptions(t)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok-owner": staffOf(perm.Owner, auth.StageActive, false)}}
	h := newHandler(t, o)
	for _, token := range []string{"tok-owner", ""} {
		wantProblem(t, serve(h, call(http.MethodPut, "/v1/staff/accounts//role", token,
			`{"role":"support","cityIds":[]}`)), http.StatusNotFound, httpx.CodeNotFound)
	}
}
