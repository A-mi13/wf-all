package public_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/clocktest"
)

// pendingLogin — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 11 плана I-2: все три реализованы.
var pendingLogin = []string{}

// identityEnv — публичный API на настоящих сценариях identity и настоящем загрузчике сессий (кэш 5 с на часах
// теста cache — сдвиг часов кэша показывает, когда отзыв доходит до запросов).
type identityEnv struct {
	h     http.Handler
	v     *apitest.Validator
	pool  *pgxpool.Pool
	cache *clocktest.Fake
}

func newIdentityEnv(t *testing.T) *identityEnv {
	t.Helper()
	o := testOptions(t)
	cache := clocktest.New(time.Now())
	o.Sessions = auth.NewCachedLoader(identity.NewSessionLoader(o.DB, cache), 5*time.Second, cache, 1000)
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	return &identityEnv{h: h, v: apitest.New(t, oapi.GetSpec), pool: o.DB, cache: cache}
}

// seedLogin — аккаунт с паролем pw (argon2 по умолчанию), почта nick@example.com.
func (e *identityEnv) seedLogin(t *testing.T, nick, pw string) uuid.UUID {
	t.Helper()
	hasher, err := password.NewHasher(password.DefaultParams(), 1)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hasher.Hash(context.Background(), pw)
	if err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ($1, now(), $2, '2000-01-01', 'ru') RETURNING id`, nick+"@example.com", nick).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO credentials (user_id, password_hash, updated_at)
		VALUES ($1, $2, now())`, id, hash); err != nil {
		t.Fatal(err)
	}
	return id
}

// do — запрос через конвейер со сверкой запроса и ответа с контрактом.
func (e *identityEnv) do(t *testing.T, method, path, body, bearer string, idem bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if idem {
		r.Header.Set("Idempotency-Key", uuid.NewString())
	}
	return e.v.Do(t, e.h, r)
}

func (e *identityEnv) login(t *testing.T, nick, pw string) oapi.AuthResult {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/v1/auth/login", `{"email":"`+nick+`@example.com","password":"`+pw+`","device":`+deviceJSON+`}`, "", false)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("вход: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var res oapi.AuthResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func problemCodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body.String(), err)
	}
	return p.Code
}

// login по контракту: 200 authenticated (tokens, user, no-store); неверный пароль и нет аккаунта — один 400
// identity.invalid_credentials; без device — 422 identity.device_required.
func TestLoginHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	uid := e.seedLogin(t, "http_login", "Жёлтая карточка на 90-й")
	res := e.login(t, "http_login", "Жёлтая карточка на 90-й")
	if res.Status != oapi.AuthStatusAuthenticated || res.Tokens == nil || res.User == nil || res.User.Id != uid ||
		!res.User.DocumentsAcceptanceRequired || res.User.NewUser {
		t.Fatalf("%+v", res)
	}
	var bodies []string
	for _, email := range []string{"http_login@example.com", "nobody@example.com"} {
		rec := e.do(t, http.MethodPost, "/v1/auth/login", `{"email":"`+email+`","password":"не тот","device":`+deviceJSON+`}`, "", false)
		if rec.Code != http.StatusBadRequest || problemCodeOf(t, rec) != identity.CodeInvalidCredentials {
			t.Fatalf("%s: %d %s", email, rec.Code, rec.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "request_id") // своё у каждого запроса
		b, _ := json.Marshal(m)
		bodies = append(bodies, string(b)+rec.Header().Get("Content-Type")+rec.Header().Get("Cache-Control"))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("тела различаются:\n%s\n%s", bodies[0], bodies[1])
	}
	rec := e.do(t, http.MethodPost, "/v1/auth/login", `{"email":"http_login@example.com","password":"x"}`, "", false)
	if rec.Code != http.StatusUnprocessableEntity || problemCodeOf(t, rec) != identity.CodeDeviceRequired {
		t.Fatalf("без device: %d %s", rec.Code, rec.Body.String())
	}
}

// refreshSession по контракту: 200 TokenPair с no-store и новым refresh; повтор старого с другого устройства — 401
// identity.refresh_invalid, после — и новый не действует (сессия отозвана).
func TestRefreshHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	e.seedLogin(t, "http_refresh", "Свисток на 46-й минуте")
	res := e.login(t, "http_refresh", "Свисток на 46-й минуте")
	install := "6F9619FF-8B86-4D01-B42D-00CF4FC964FF"
	refresh := func(tok, inst string) *httptest.ResponseRecorder {
		return e.do(t, http.MethodPost, "/v1/auth/refresh", `{"refresh_token":"`+tok+`","install_id":"`+inst+`"}`, "", false)
	}
	rec := refresh(res.Tokens.RefreshToken, install)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var pair oapi.TokenPair
	if err := json.Unmarshal(rec.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	if pair.RefreshToken == res.Tokens.RefreshToken || pair.SessionId != res.Tokens.SessionId {
		t.Fatalf("%+v", pair)
	}
	rec = refresh(res.Tokens.RefreshToken, "11111111-2222-3333-4444-555555555555")
	if rec.Code != http.StatusUnauthorized || problemCodeOf(t, rec) != identity.CodeRefreshInvalid {
		t.Fatalf("другое устройство: %d %s", rec.Code, rec.Body.String())
	}
	if rec := refresh(pair.RefreshToken, install); rec.Code != http.StatusUnauthorized {
		t.Fatalf("преемник отозванной: %d %s", rec.Code, rec.Body.String())
	}
}

// logout: с замком и Idempotency-Key — 204; без ключа — 400; без токена — 401. После выхода access действует только
// в пределах кэша загрузчика (5 с), затем — 401 auth.unauthenticated.
func TestLogoutHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	e.seedLogin(t, "http_logout", "Угловой на последней минуте")
	res := e.login(t, "http_logout", "Угловой на последней минуте")
	access := res.Tokens.AccessToken
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/logout", nil)
	r.Header.Set("Authorization", "Bearer "+access)
	if rec := httptest.NewRecorder(); func() bool { e.h.ServeHTTP(rec, r); return rec.Code != http.StatusBadRequest }() {
		t.Fatalf("без Idempotency-Key: %d", rec.Code)
	}
	if rec := e.do(t, http.MethodPost, "/v1/auth/logout", "", "", true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("без токена: %d", rec.Code)
	}
	if rec := e.do(t, http.MethodPost, "/v1/auth/logout", "", access, true); rec.Code != http.StatusNoContent {
		t.Fatalf("выход: %d %s", rec.Code, rec.Body.String())
	}
	var reason string
	if err := e.pool.QueryRow(context.Background(), `SELECT revoked_reason FROM sessions WHERE id = $1`,
		res.Tokens.SessionId).Scan(&reason); err != nil || reason != "logout" {
		t.Fatalf("причина %q %v", reason, err)
	}
}

// Доступ после выхода: в пределах кэша (меньше 5 с) access ещё проходит, через 5 с — 401 auth.unauthenticated.
func TestAccessAfterLogout(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	e.seedLogin(t, "http_after", "Пенальти на 90-й")
	res := e.login(t, "http_after", "Пенальти на 90-й")
	access := res.Tokens.AccessToken
	if rec := e.do(t, http.MethodPost, "/v1/auth/logout", "", access, true); rec.Code != http.StatusNoContent {
		t.Fatalf("выход: %d %s", rec.Code, rec.Body.String())
	}
	e.cache.Advance(4 * time.Second)
	if rec := e.do(t, http.MethodPost, "/v1/auth/logout", "", access, true); rec.Code != http.StatusNoContent {
		t.Fatalf("в пределах кэша: %d %s", rec.Code, rec.Body.String())
	}
	e.cache.Advance(time.Second)
	rec := e.do(t, http.MethodPost, "/v1/auth/logout", "", access, true)
	if rec.Code != http.StatusUnauthorized || problemCodeOf(t, rec) != httpx.CodeUnauthenticated {
		t.Fatalf("через 5 с: %d %s", rec.Code, rec.Body.String())
	}
}
