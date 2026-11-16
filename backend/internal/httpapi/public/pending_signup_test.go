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
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity"
	identityhttp "wf/backend/internal/identity/httpapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/ipgeo"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/token"
)

// pendingSignup — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 10 плана I-2: все пять реализованы.
var pendingSignup = []string{}

// codeEnv — собранный public API на настоящих сценариях identity с известным кольцом секретов (код из письма
// читается из code_enc, как его читает Composer воркера).
type codeEnv struct {
	h       http.Handler
	pool    *pgxpool.Pool
	secrets *keys.Ring
	tokens  *token.Issuer
	v       *apitest.Validator
	sess    *fakeSession // nil — без сессии (auth.NoSessions)
}

func codeRing(t *testing.T) *keys.Ring {
	t.Helper()
	s, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	r, err := keys.NewRing([]string{s})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// newCodeEnv — public.Handler на клоне базы; sessions — загрузчик сессий операций с замком (nil — auth.NoSessions).
func newCodeEnv(t *testing.T, withSession func(pool *pgxpool.Pool, issuer *token.Issuer) *fakeSession) *codeEnv {
	t.Helper()
	pool := dbtest.NewPool(t)
	seed, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	k, err := token.ParseKeys([]string{seed})
	if err != nil {
		t.Fatal(err)
	}
	tokens := token.NewIssuer(k, clock.System)
	secrets := codeRing(t)
	powKey, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	pow, err := humancheck.NewPoW(pool, clock.System, humancheck.PoWConfig{Keys: []string{powKey}, TTL: time.Minute, MaxNumber: 1000})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := password.NewHasher(password.DefaultParams(), 1)
	if err != nil {
		t.Fatal(err)
	}
	limiter := ratelimit.NewPG(pool, clock.System, ratelimit.Players)
	assessor, err := risk.NewAssessor(limiter, risk.DefaultPolicies())
	if err != nil {
		t.Fatal(err)
	}
	legal, err := identity.ParseLegalDocuments("terms=2026-11-01|https://example.com/terms,privacy=2026-11-01|https://example.com/privacy")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	ih, err := identityhttp.New(identity.Deps{Pool: pool, Clock: clock.System, Secrets: secrets, Devices: codeRing(t),
		Hasher: hasher, Limiter: limiter, LimiterTx: pgLimiterTx, Risk: assessor, Human: pow, Geo: geo.New(pool), Flags: flags.NewStore(pool),
		IPGeo: ipgeo.None, Legal: legal, Tokens: tokens, River: ins, Attestor: identity.AttestOff{},
		Providers: identity.NoProviders{}, TOTPIssuer: "WF test", Log: log})
	if err != nil {
		t.Fatal(err)
	}
	e := &codeEnv{pool: pool, secrets: secrets, tokens: tokens, v: apitest.New(t, oapi.GetSpec)}
	o := public.Options{DB: pool, Tokens: tokens, Identity: ih, Sessions: auth.NoSessions, Limiter: limiter,
		RateRules: ratelimit.DefaultRules(), Geo: &fakeGeo{}, AppVersions: &fakeVersions{}}
	if withSession != nil {
		e.sess = withSession(pool, tokens)
		o.Sessions = e.sess
	}
	if e.h, err = public.NewHandler(log, o); err != nil {
		t.Fatal(err)
	}
	return e
}

// do — запрос через конвейер со сверкой с контрактом; bearer — у операций с замком, ключ — у мутаций с ним.
func (e *codeEnv) do(t *testing.T, method, path, body string, bearer, idem bool) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequestWithContext(context.Background(), method, path, nil)
	} else {
		r = httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if bearer && e.sess != nil {
		r.Header.Set("Authorization", "Bearer "+e.sess.token)
	}
	if idem {
		r.Header.Set("Idempotency-Key", uuid.NewString())
	}
	return e.v.Do(t, e.h, r)
}

// code — код из письма строки auth_codes.
func (e *codeEnv) code(t *testing.T, codeID uuid.UUID) string {
	t.Helper()
	var enc []byte
	var keyID string
	if err := e.pool.QueryRow(context.Background(), `SELECT code_enc, key_id FROM auth_codes WHERE id = $1`, codeID).
		Scan(&enc, &keyID); err != nil {
		t.Fatal(err)
	}
	c, err := e.secrets.Open(enc, keyID, codeID[:])
	if err != nil {
		t.Fatal(err)
	}
	return string(c)
}

// signupCity — включённая страна с порогом 14 и город в ней.
func signupCity(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `WITH c AS (
		INSERT INTO countries (code, name, currency, default_locale, phone_prefix, week_starts_on, min_signup_age,
		                       age_of_majority, is_enabled)
		VALUES ('XS', 'Сигнапия', 'XXX', 'ru', '+999', 1, 14, 18, true) RETURNING id)
		INSERT INTO cities (country_id, name, slug, timezone, status, centroid)
		SELECT id, 'Сигнап', 'signup-city', 'Europe/Moscow', 'live', ST_SetSRID(ST_MakePoint(41.9, 45.0), 4326)::geography
		FROM c RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func signupBody(email, nick string, city uuid.UUID) string {
	return `{"email":"` + email + `","password":"Жёлтая карточка на 90-й","nickname":"` + nick + `","birth_date":"1998-04-17",` +
		`"city_id":"` + city.String() + `","accepted_documents":[{"document":"terms","version":"2026-11-01"},` +
		`{"document":"privacy","version":"2026-11-01"}],"locale":"ru","device":` + deviceJSON + `}`
}

// problemOfBody — Problem из тела ответа.
func problemOfBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("тело %q: %v", rec.Body.String(), err)
	}
	return p
}

// Task 10 через весь конвейер (анонимно, без bearer): документы (public, max-age=300), ник (private), старт регистрации
// — одинаковый 202 у свободной и занятой почты (статус, ключи тела, заголовки), повтор раньше паузы — 429 с
// Retry-After, подтверждение — 400 code_invalid с attempts_left и 201 AuthResult (new_user, no-store); ответы сверены
// с контрактом.
func TestSignupHTTP(t *testing.T) {
	t.Parallel()
	e := newCodeEnv(t, nil)
	city := signupCity(t, e.pool)
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ('http_taken@example.com', now(), 'http_taken', '2000-01-01', 'ru')`); err != nil {
		t.Fatal(err)
	}
	docs := e.do(t, http.MethodGet, "/v1/legal/documents", "", false, false)
	if docs.Code != http.StatusOK || docs.Header().Get("Cache-Control") != "public, max-age=300" ||
		!strings.Contains(docs.Body.String(), `"version":"2026-11-01"`) {
		t.Fatalf("документы: %d %v %s", docs.Code, docs.Header(), docs.Body.String())
	}
	nick := e.do(t, http.MethodGet, "/v1/nicknames/availability?nickname=http_taken", "", false, false)
	if nick.Code != http.StatusOK || nick.Header().Get("Cache-Control") != "private, no-store" ||
		!strings.Contains(nick.Body.String(), `"reason":"taken"`) {
		t.Fatalf("ник: %d %v %s", nick.Code, nick.Header(), nick.Body.String())
	}
	free := e.do(t, http.MethodPost, "/v1/auth/signup", signupBody("http_free@example.com", "http_free", city), false, false)
	taken := e.do(t, http.MethodPost, "/v1/auth/signup", signupBody("http_taken@example.com", "http_new", city), false, false)
	var sentFree, sentTaken oapi.CodeSent
	for _, r := range []struct {
		rec  *httptest.ResponseRecorder
		into *oapi.CodeSent
	}{{free, &sentFree}, {taken, &sentTaken}} {
		if r.rec.Code != http.StatusAccepted || r.rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("старт: %d %v %s", r.rec.Code, r.rec.Header(), r.rec.Body.String())
		}
		if err := json.Unmarshal(r.rec.Body.Bytes(), r.into); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range []string{"Content-Type", "Cache-Control", "Vary"} {
		if free.Header().Get(h) != taken.Header().Get(h) {
			t.Errorf("заголовок %s различает почту: %q / %q", h, free.Header().Get(h), taken.Header().Get(h))
		}
	}
	if len(free.Header()) != len(taken.Header()) {
		t.Errorf("набор заголовков различает почту: %v / %v", free.Header(), taken.Header())
	}
	if sentFree.ExpiresAt.Sub(sentFree.ResendAvailableAt) != sentTaken.ExpiresAt.Sub(sentTaken.ResendAvailableAt) {
		t.Errorf("сроки различают почту: %+v / %+v", sentFree, sentTaken)
	}
	weak := e.do(t, http.MethodPost, "/v1/auth/signup", strings.Replace(signupBody("http_weak@example.com", "http_weak", city),
		"Жёлтая карточка на 90-й", "short", 1), false, false)
	if p := problemOfBody(t, weak); weak.Code != http.StatusUnprocessableEntity || p["code"] != identity.CodePasswordWeak ||
		!strings.Contains(weak.Body.String(), `"reasons":["too_short"]`) {
		t.Fatalf("слабый пароль: %d %s", weak.Code, weak.Body.String())
	}
	soon := e.do(t, http.MethodPost, "/v1/auth/codes/"+sentTaken.CodeId.String()+"/resend", "", false, false)
	if p := problemOfBody(t, soon); soon.Code != http.StatusTooManyRequests || p["code"] != identity.CodeResendTooSoon ||
		soon.Header().Get("Retry-After") == "" {
		t.Fatalf("повтор раньше паузы: %d %v %s", soon.Code, soon.Header(), soon.Body.String())
	}
	confirm := func(code string) *httptest.ResponseRecorder {
		return e.do(t, http.MethodPost, "/v1/auth/signup/confirm", `{"code_id":"`+sentFree.CodeId.String()+`","code":"`+code+
			`","device":`+deviceJSON+`}`, false, false)
	}
	right := e.code(t, sentFree.CodeId)
	wrong := "000000"
	if right == wrong {
		wrong = "111111"
	}
	bad := confirm(wrong)
	if p := problemOfBody(t, bad); bad.Code != http.StatusBadRequest || p["code"] != identity.CodeCodeInvalid || p["attempts_left"] != 4.0 {
		t.Fatalf("неверный код: %d %s", bad.Code, bad.Body.String())
	}
	ok := confirm(right)
	if ok.Code != http.StatusCreated || ok.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(ok.Body.String(), `"new_user":true`) || !strings.Contains(ok.Body.String(), `"access_token"`) {
		t.Fatalf("подтверждение: %d %v %s", ok.Code, ok.Header(), ok.Body.String())
	}
	if again := confirm(right); again.Code != http.StatusGone {
		t.Fatalf("повтор подтверждения: %d %s", again.Code, again.Body.String())
	}
}
