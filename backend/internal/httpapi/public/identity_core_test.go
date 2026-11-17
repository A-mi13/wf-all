package public_test

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity"
	identityhttp "wf/backend/internal/identity/httpapi"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/ipgeo"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/otp"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
	wftoken "wf/backend/internal/platform/token"
)

// Сквозные стражи плана I-2 (Task 20): 32 операции public на собранном сервере с настоящими сценариями identity,
// настоящим загрузчиком сессий и известным кольцом секретов (код из письма читается из code_enc, как его читает
// Composer воркера). Один сценарий (coreScenario) проходит каждую операцию I-2 успехом; стражи смотрят на его ответы и
// на базу после него.

// i2PublicOps — 32 операции public плана I-2 (Приложение 1).
var i2PublicOps = []string{
	"acceptDocuments", "cancelDeletion", "cancelMyMfaReset", "changeNickname", "changePassword", "checkNickname",
	"completeMfa", "confirmEmailChange", "confirmPasswordReset", "confirmSignup", "confirmTotp", "deleteAccount",
	"disableTotp", "getAuthMethods", "getLegalDocuments", "getMe", "getMyMfa", "listMyAuthMethods", "listMySessions",
	"login", "logout", "reauthenticate", "refreshSession", "regenerateRecoveryCodes", "resendAuthCode",
	"revokeMySession", "revokeOtherSessions", "setupTotp", "startEmailChange", "startPasswordReset", "startSignup",
	"updateMe",
}

// i2AnonymousOps — 9 анонимных мутаций I-2 (Приложение 1; в x-anonymous-mutations контракта).
var i2AnonymousOps = []string{
	"cancelDeletion", "completeMfa", "confirmPasswordReset", "confirmSignup", "login", "refreshSession",
	"resendAuthCode", "startPasswordReset", "startSignup",
}

// wantPendingI3 — 14 операций плана I-3 (соцвходы, ключи доступа, аттестация): заглушки до его задач (R1 плана I-4).
var wantPendingI3 = []string{
	"completeSocialSignup", "createDeviceChallenge", "createSocialNonce", "deletePasskey", "linkIdentity",
	"passkeyLogin", "registerPasskey", "renamePasskey", "socialLogin", "startMfaPasskey", "startPasskeyLogin",
	"startPasskeyRegistration", "startReauthPasskey", "unlinkIdentity",
}

// wantPendingPublic — что может оставаться заглушкой public после I-2: 14 операций I-3 и 4 операции I-4 (R1).
var wantPendingPublic = []string{
	"completeSocialSignup", "createDeviceChallenge", "createSocialNonce", "deletePasskey", "getMyCaptaincy",
	"linkIdentity", "listMyCaptainApplications", "passkeyLogin", "registerPasskey", "renamePasskey",
	"socialLogin", "startMfaPasskey", "startPasskeyLogin", "startPasskeyRegistration", "startReauthPasskey",
	"submitCaptainApplication", "unlinkIdentity", "withdrawCaptainApplication",
}

// allowCaptainChecks — оба порта условий капитанства выполнены (тесты httpapi не импортируют identity/identitytest —
// archtest; свой литерал порта, I7б ревью плана I-4).
func allowCaptainChecks() []identity.CaptainCheck {
	out := make([]identity.CaptainCheck, 0, len(identity.RequiredCaptainChecks))
	for _, n := range identity.RequiredCaptainChecks {
		out = append(out, allowCheck(n))
	}
	return out
}

// allowCheck — порт условия капитанства, условие выполнено.
type allowCheck string

func (c allowCheck) Name() string { return string(c) }

func (allowCheck) Check(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (string, bool, error) {
	return "", true, nil
}

// coreEnv — public API на настоящих сценариях identity: часы теста у сценариев, токенов и загрузчика (пауза повторной
// отправки кода — сдвигом часов), лимитов нет (сценарий не упирается в 429), загрузчик без кэша (отзыв виден сразу).
type coreEnv struct {
	h       http.Handler
	v       *apitest.Validator
	pool    *pgxpool.Pool
	secrets *keys.Ring
	clock   *clocktest.Fake
	tokens  *wftoken.Issuer
	routes  func(http.Handler) http.Handler
	witness string // access-токен сессии стороннего аккаунта — шлётся и анонимным операциям
	calls   []coreCall
}

// coreCall — ответ операции сценария.
type coreCall struct {
	op  string
	rec *httptest.ResponseRecorder
}

func newCoreEnv(t *testing.T) *coreEnv {
	t.Helper()
	pool := dbtest.NewPool(t)
	clk := clocktest.New(time.Now().UTC().Truncate(time.Second))
	seed, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	k, err := wftoken.ParseKeys([]string{seed})
	if err != nil {
		t.Fatal(err)
	}
	tokens := wftoken.NewIssuer(k, clk)
	secrets := codeRing(t)
	powKey, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	pow, err := humancheck.NewPoW(pool, clk, humancheck.PoWConfig{Keys: []string{powKey}, TTL: time.Minute, MaxNumber: 1000})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := password.NewHasher(password.DefaultParams(), 2)
	if err != nil {
		t.Fatal(err)
	}
	assessor, err := risk.NewAssessor(ratelimit.Unlimited, risk.DefaultPolicies())
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
	ih, err := identityhttp.New(identity.Deps{Pool: pool, Clock: clk, Secrets: secrets, Devices: codeRing(t),
		Hasher: hasher, Limiter: ratelimit.Unlimited, LimiterTx: func(pgx.Tx) ratelimit.Limiter { return ratelimit.Unlimited },
		Risk: assessor, Human: pow, Geo: geo.New(pool), Flags: flags.NewStore(pool), IPGeo: ipgeo.None, Legal: legal,
		Tokens: tokens, River: ins, Attestor: identity.AttestOff{}, Providers: identity.NoProviders{}, TOTPIssuer: "WF test",
		Log: log, CaptainChecks: allowCaptainChecks()})
	if err != nil {
		t.Fatal(err)
	}
	h, err := public.NewHandler(log, public.Options{DB: pool, Tokens: tokens, Identity: ih,
		Sessions: identity.NewSessionLoader(pool, clk), Limiter: ratelimit.Unlimited, RateRules: ratelimit.DefaultRules(),
		Geo: &fakeGeo{}, AppVersions: &fakeVersions{}})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(spec)
	if err != nil {
		t.Fatal(err)
	}
	e := &coreEnv{h: h, v: apitest.New(t, oapi.GetSpec), pool: pool, secrets: secrets, clock: clk, tokens: tokens,
		routes: routes}
	e.witness = e.session(t, e.seedUser(t, "core_witness", "Свидетель у бровки 2026"))
	return e
}

// route — маршрут контракта запроса (операция и её security).
func (e *coreEnv) route(t *testing.T, method, path string) *httpx.Route {
	t.Helper()
	var rt *httpx.Route
	e.routes(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { rt, _ = httpx.RouteFrom(r.Context()) })).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), method, path, nil))
	if rt == nil {
		t.Fatalf("%s %s: маршрута нет в контракте", method, path)
	}
	return rt
}

// do — запрос через весь конвейер со сверкой с контрактом. Каждая мутация — с новым Idempotency-Key; операции без
// bearer в security получают access-токен свидетеля (анонимная мутация не должна идти под идемпотентностью и с ним).
func (e *coreEnv) do(t *testing.T, method, path, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	rt := e.route(t, method, path)
	r := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if !rt.AcceptsBearer() {
		bearer = e.witness
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if method != http.MethodGet {
		r.Header.Set("Idempotency-Key", uuid.NewString())
	}
	rec := e.v.Do(t, e.h, r)
	e.calls = append(e.calls, coreCall{op: opID(rt.Operation()), rec: rec})
	return rec
}

// ok — 2xx, иначе падение; тело JSON (пусто — nil).
func (e *coreEnv) ok(t *testing.T, step string, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code < 200 || rec.Code > 299 {
		t.Fatalf("%s: %d %s", step, rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("%s: тело %q: %v", step, rec.Body.String(), err)
	}
	return m
}

// str — строковое поле по пути ключей.
func str(t *testing.T, m map[string]any, path ...string) string {
	t.Helper()
	var cur any = m
	for _, k := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("нет %v в %v", path, m)
		}
		cur = obj[k]
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("нет строки %v в %v", path, m)
	}
	return s
}

// code — код из письма строки auth_codes.
func (e *coreEnv) code(t *testing.T, codeID string) string {
	t.Helper()
	id := uuid.MustParse(codeID)
	var enc []byte
	var keyID string
	if err := e.pool.QueryRow(context.Background(), `SELECT code_enc, key_id FROM auth_codes WHERE id = $1`, id).
		Scan(&enc, &keyID); err != nil {
		t.Fatal(err)
	}
	c, err := e.secrets.Open(enc, keyID, id[:])
	if err != nil {
		t.Fatal(err)
	}
	return string(c)
}

// seedUser — аккаунт с паролем pw (argon2), почта <ник>@example.com.
func (e *coreEnv) seedUser(t *testing.T, nick, pw string) uuid.UUID {
	t.Helper()
	h, err := password.NewHasher(password.DefaultParams(), 1)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := h.Hash(context.Background(), pw)
	if err != nil {
		t.Fatal(err)
	}
	var uid uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `WITH u AS (
		INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ($1 || '@example.com', now(), $1, '2000-01-01', 'ru') RETURNING id)
		INSERT INTO credentials (user_id, password_hash, updated_at) SELECT id, $2, now() FROM u RETURNING user_id`,
		nick, hash).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	return uid
}

// session — живая веб-сессия аккаунта в базе (свежий вход) и её access-токен.
func (e *coreEnv) session(t *testing.T, uid uuid.UUID) string {
	t.Helper()
	sid := uuid.Must(uuid.NewV7())
	now := e.clock.Now()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO sessions (id, user_id, client_kind, auth_method, reauth_at, ip,
		created_at, last_seen_at, idle_expires_at, absolute_expires_at)
		VALUES ($1, $2, 'web', 'password', $3::timestamptz, '203.0.113.7', $3, $3, $3 + interval '30 days', $3 + interval '180 days')`,
		sid, uid, now); err != nil {
		t.Fatal(err)
	}
	tok, _, err := e.tokens.Access(uid, sid)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// login — вход паролем по API, тело AuthResult.
func (e *coreEnv) login(t *testing.T, email, pw string) map[string]any {
	t.Helper()
	return e.ok(t, "login "+email, e.do(t, http.MethodPost, "/v1/auth/login",
		`{"email":"`+email+`","password":"`+pw+`","device":`+deviceJSON+`}`, ""))
}

// Пароли сценария.
const (
	corePW1 = "Жёлтая карточка на 90-й"     //nolint:gosec // G101: пароль теста
	corePW2 = "Свисток на 46-й минуте"      //nolint:gosec // G101: пароль теста
	corePW3 = "Удар со штрафного в девятку" //nolint:gosec // G101: пароль теста
)

// coreScenario — каждая из 32 операций I-2 успехом: регистрация с повторной отправкой кода, аккаунт, сессии и refresh,
// свежий вход, смена пароля и почты, 2FA (настройка, коды, отмена сброса поддержкой, вход вторым фактором, выключение),
// сброс пароля, удаление и его отмена, выход. Почта core.a → core.new, ник core_a → core_b.
func coreScenario(t *testing.T) *coreEnv {
	t.Helper()
	e := newCoreEnv(t)
	city := signupCity(t, e.pool)
	e.ok(t, "documents", e.do(t, http.MethodGet, "/v1/legal/documents", "", ""))
	e.ok(t, "nickname", e.do(t, http.MethodGet, "/v1/nicknames/availability?nickname=core_a", "", ""))
	e.ok(t, "methods", e.do(t, http.MethodGet, "/v1/auth/methods", "", ""))
	sent := e.ok(t, "signup", e.do(t, http.MethodPost, "/v1/auth/signup", signupBody("core.a@example.com", "core_a", city), ""))
	codeID := str(t, sent, "code_id")
	e.clock.Advance(61 * time.Second) // пауза повторной отправки
	e.ok(t, "resend", e.do(t, http.MethodPost, "/v1/auth/codes/"+codeID+"/resend", "", ""))
	auth := e.ok(t, "confirm signup", e.do(t, http.MethodPost, "/v1/auth/signup/confirm",
		`{"code_id":"`+codeID+`","code":"`+e.code(t, codeID)+`","device":`+deviceJSON+`}`, ""))
	refresh := str(t, auth, "tokens", "refresh_token")
	access := str(t, auth, "tokens", "access_token")
	e.ok(t, "me", e.do(t, http.MethodGet, "/v1/me", "", access))
	e.ok(t, "update me", e.do(t, http.MethodPatch, "/v1/me", `{"locale":"en"}`, access))
	e.ok(t, "nickname change", e.do(t, http.MethodPatch, "/v1/me/nickname", `{"nickname":"core_b"}`, access))
	e.ok(t, "documents accept", e.do(t, http.MethodPost, "/v1/me/documents",
		`{"accepted_documents":[{"document":"terms","version":"2026-11-01"},{"document":"privacy","version":"2026-11-01"}]}`, access))
	e.ok(t, "auth methods", e.do(t, http.MethodGet, "/v1/me/auth-methods", "", access))
	e.ok(t, "sessions", e.do(t, http.MethodGet, "/v1/me/sessions", "", access))
	pair := e.ok(t, "refresh", e.do(t, http.MethodPost, "/v1/auth/refresh",
		`{"refresh_token":"`+refresh+`","install_id":"6F9619FF-8B86-4D01-B42D-00CF4FC964FF"}`, ""))
	access = str(t, pair, "access_token")
	second := str(t, e.login(t, "core.a@example.com", corePW1), "tokens", "session_id")
	e.login(t, "core.a@example.com", corePW1)
	e.ok(t, "revoke one", e.do(t, http.MethodDelete, "/v1/me/sessions/"+second, "", access))
	e.ok(t, "revoke others", e.do(t, http.MethodPost, "/v1/me/sessions/revoke-others", "", access))
	e.ok(t, "reauth", e.do(t, http.MethodPost, "/v1/me/reauth", `{"password":"`+corePW1+`"}`, access))
	e.ok(t, "password", e.do(t, http.MethodPost, "/v1/me/password", `{"new_password":"`+corePW2+`"}`, access))
	ec := e.ok(t, "email change", e.do(t, http.MethodPost, "/v1/me/email-change", `{"new_email":"core.new@example.com"}`, access))
	ecID := str(t, ec, "code_id")
	e.ok(t, "email confirm", e.do(t, http.MethodPost, "/v1/me/email-change/confirm",
		`{"code_id":"`+ecID+`","code":"`+e.code(t, ecID)+`"}`, access))
	e.ok(t, "mfa", e.do(t, http.MethodGet, "/v1/me/mfa", "", access))
	setup := e.ok(t, "totp setup", e.do(t, http.MethodPost, "/v1/me/mfa/totp/setup", "", access))
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(str(t, setup, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	e.ok(t, "totp confirm", e.do(t, http.MethodPost, "/v1/me/mfa/totp/confirm",
		`{"code":"`+otp.Code(secret, otp.Step(e.clock.Now()))+`"}`, access))
	regen := e.ok(t, "recovery codes", e.do(t, http.MethodPost, "/v1/me/mfa/recovery-codes", "", access))
	codes, _ := regen["recovery_codes"].([]any)
	if len(codes) == 0 {
		t.Fatalf("резервных кодов нет: %v", regen)
	}
	var uid uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE nickname = 'core_b'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id,
		requested_by_label, reason, execute_at, created_at)
		VALUES (gen_random_uuid(), $1, gen_random_uuid(), gen_random_uuid(), 'Ольга', 'потерян телефон', $2, $3)`,
		uid, e.clock.Now().Add(7*24*time.Hour), e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	// явная отмена — только сессией, прошедшей второй фактор (п. 5 волны F): сессия вошла паролем до включения 2FA
	if _, err := e.pool.Exec(context.Background(), `UPDATE sessions SET mfa_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`,
		uid, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	e.ok(t, "cancel mfa reset", e.do(t, http.MethodDelete, "/v1/me/mfa-reset", "", access))
	step := e.login(t, "core.new@example.com", corePW2)
	mfa := e.ok(t, "complete mfa", e.do(t, http.MethodPost, "/v1/auth/mfa", `{"mfa_token":"`+str(t, step, "mfa", "mfa_token")+
		`","recovery_code":"`+fmt.Sprint(codes[0])+`","device":`+deviceJSON+`}`, ""))
	access = str(t, mfa, "tokens", "access_token")
	e.ok(t, "totp disable", e.do(t, http.MethodDelete, "/v1/me/mfa/totp", "", access))
	reset := e.ok(t, "reset start", e.do(t, http.MethodPost, "/v1/auth/password-reset", `{"email":"core.new@example.com"}`, ""))
	resetID := str(t, reset, "code_id")
	done := e.ok(t, "reset confirm", e.do(t, http.MethodPost, "/v1/auth/password-reset/confirm", `{"code_id":"`+resetID+
		`","code":"`+e.code(t, resetID)+`","new_password":"`+corePW3+`","device":`+deviceJSON+`}`, ""))
	access = str(t, done, "tokens", "access_token")
	e.ok(t, "delete", e.do(t, http.MethodPost, "/v1/me/delete", `{"confirm_nickname":"core_b"}`, access))
	pending := e.login(t, "core.new@example.com", corePW3)
	back := e.ok(t, "cancel deletion", e.do(t, http.MethodPost, "/v1/auth/deletion/cancel",
		`{"restore_token":"`+str(t, pending, "deletion", "restore_token")+`","device":`+deviceJSON+`}`, ""))
	e.ok(t, "logout", e.do(t, http.MethodPost, "/v1/auth/logout", "", str(t, back, "tokens", "access_token")))
	return e
}

// TestCoreOpsNotStub — каждая из 32 операций I-2 на собранном сервере отвечает успехом (не 501); заглушки public —
// подмножество I-3 ∪ I-4, из них ровно 14 операций I-3 (R1 плана I-4); операции identity контракта = I-2 ⊔ I-3 ⊔ I-4.
func TestCoreOpsNotStub(t *testing.T) {
	t.Parallel()
	e := coreScenario(t)
	seen := map[string]bool{}
	for _, c := range e.calls {
		if c.rec.Code == http.StatusNotImplemented || strings.Contains(c.rec.Body.String(), `"code":"`+httpx.CodeNotImplemented+`"`) {
			t.Errorf("%s: заглушка — %d %s", c.op, c.rec.Code, c.rec.Body.String())
		}
		seen[c.op] = true
	}
	for _, op := range i2PublicOps {
		if !seen[op] {
			t.Errorf("%s: сценарий её не прошёл", op)
		}
	}
	// пока I-4 в работе — подмножество (R1 плана I-4): операции I-4 уходят из pendingOps по задачам, все 14 I-3 — на
	// месте; точное равенство pendingI3 — Task 12. Заглушки admin сторожит свой TestNoStubOperations.
	for _, op := range pendingOps {
		if !slices.Contains(wantPendingPublic, op) {
			t.Errorf("pendingOps public: %s — не операция I-3 или I-4", op)
		}
	}
	if got, want := sortedSet(pendingI3), sortedSet(wantPendingI3); !slices.Equal(got, want) {
		t.Errorf("pendingI3:\n%v\nждали ровно 14 операций I-3:\n%v", got, want)
	}
	for _, op := range pendingI3 {
		if !slices.Contains(pendingOps, op) {
			t.Errorf("pendingOps public: нет %s — операция I-3 реализована раньше плана I-3?", op)
		}
	}
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	var identityOps []string
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			if slices.Contains(op.Tags, "identity") {
				identityOps = append(identityOps, opID(op))
			}
		}
	}
	if got, want := sortedSet(identityOps), sortedSet(slices.Concat(i2PublicOps, wantPendingPublic)); !slices.Equal(got, want) {
		t.Errorf("операции identity контракта:\n%v\nждали I-2 + заглушки:\n%v", got, want)
	}
}

// sortedSet — множество строк по алфавиту.
func sortedSet(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return slices.Compact(out)
}

// TestCoreCacheControl — у каждого 2xx с телом операций I-2 Cache-Control равен значению контракта (план I-1, R5);
// сценарий прошёл все операции, у которых в контракте есть 2xx с телом.
func TestCoreCacheControl(t *testing.T) {
	t.Parallel()
	e := coreScenario(t)
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	withBody := map[string]bool{}
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			if !slices.Contains(i2PublicOps, opID(op)) {
				continue
			}
			for code, r := range op.Responses.Map() {
				if strings.HasPrefix(code, "2") && r.Value != nil && len(r.Value.Content) > 0 {
					withBody[opID(op)] = true
				}
			}
		}
	}
	checked := map[string]bool{}
	for _, c := range e.calls {
		if c.rec.Code < 200 || c.rec.Code > 299 || c.rec.Body.Len() == 0 {
			continue
		}
		want := contractCacheControl(t, e, c.op, c.rec.Code)
		if got := c.rec.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s %d: Cache-Control %q, контракт — %q", c.op, c.rec.Code, got, want)
		}
		checked[c.op] = true
	}
	for op := range withBody {
		if !checked[op] {
			t.Errorf("%s: 2xx с телом в контракте, а сценарий его не получил", op)
		}
	}
	if len(checked) < 20 {
		t.Fatalf("проверено %d операций — сценарий не дошёл", len(checked))
	}
}

// contractCacheControl — значение Cache-Control ответа status операции op из контракта (пример заголовка).
func contractCacheControl(t *testing.T, _ *coreEnv, op string, status int) string {
	t.Helper()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range spec.Paths.Map() {
		for _, o := range item.Operations() {
			if opID(o) != op {
				continue
			}
			r := o.Responses.Status(status)
			if r == nil || r.Value == nil {
				t.Fatalf("%s: ответа %d нет в контракте", op, status)
			}
			h := r.Value.Headers["Cache-Control"]
			if h == nil || h.Value == nil {
				t.Fatalf("%s %d: Cache-Control не описан в контракте", op, status)
			}
			if s, ok := h.Value.Example.(string); ok && s != "" {
				return s
			}
			t.Fatalf("%s %d: у Cache-Control контракта нет значения", op, status)
		}
	}
	t.Fatalf("%s: операции нет в контракте", op)
	return ""
}

// TestCoreAnonymousNoIdempotency — 9 анонимных мутаций I-2 (с Idempotency-Key и живым bearer свидетеля) не пишут
// idempotency_keys; мутации с замком того же сценария — пишут (проверка не вакуумна).
func TestCoreAnonymousNoIdempotency(t *testing.T) {
	t.Parallel()
	e := coreScenario(t)
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	anon, _ := spec.Extensions["x-anonymous-mutations"].([]any)
	for _, op := range i2AnonymousOps {
		if !slices.ContainsFunc(anon, func(v any) bool { return v == op }) {
			t.Errorf("%s: нет в x-anonymous-mutations контракта", op)
		}
	}
	rows, err := e.pool.Query(context.Background(), `SELECT endpoint FROM idempotency_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	endpoints := map[string]int{}
	for rows.Next() {
		var ep string
		if err := rows.Scan(&ep); err != nil {
			t.Fatal(err)
		}
		endpoints[ep]++
	}
	for _, p := range []string{"/v1/auth/signup", "/v1/auth/signup/confirm", "/v1/auth/login", "/v1/auth/mfa",
		"/v1/auth/password-reset", "/v1/auth/password-reset/confirm", "/v1/auth/refresh", "/v1/auth/deletion/cancel"} {
		if endpoints[p] != 0 {
			t.Errorf("%s: анонимная мутация записала ключ идемпотентности (%d)", p, endpoints[p])
		}
	}
	for ep, n := range endpoints {
		if strings.HasPrefix(ep, "/v1/auth/codes/") {
			t.Errorf("%s: анонимная мутация записала ключ идемпотентности (%d)", ep, n)
		}
	}
	// мутации с замком того же сценария — под ключом: иначе проверка выше вакуумна
	for _, p := range []string{"/v1/me/password", "/v1/me/nickname", "/v1/me/delete", "/v1/me/email-change/confirm"} {
		if endpoints[p] != 1 {
			t.Errorf("%s: ключей идемпотентности %d, ждали 1 — проверка вакуумна (%v)", p, endpoints[p], endpoints)
		}
	}
}

// TestCoreNoPIIInEvents — после полного сценария outbox.payload и river_job.args не содержат почты (ни одной «@»),
// ников, IP и install_id; события и задачи писем есть (проверка не вакуумна).
func TestCoreNoPIIInEvents(t *testing.T) {
	t.Parallel()
	e := coreScenario(t)
	ctx := context.Background()
	scan := func(q string) []string {
		rows, err := e.pool.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	payloads := scan(`SELECT event_type || ' ' || payload::text FROM outbox`)
	args := scan(`SELECT kind || ' ' || args::text FROM river_job`)
	forbidden := []string{"@", "core.a", "core.new", "core_a", "core_b", "core_witness", "example.com", "192.0.2.1",
		"203.0.113.7",
		// install_id сценария (deviceJSON, refresh) — не в событиях и аргументах задач: ни с дефисами, ни без
		"6f9619ff-8b86-4d01", "6f9619ff8b864d01"}
	for _, list := range [][]string{payloads, args} {
		for _, s := range list {
			for _, f := range forbidden {
				if strings.Contains(strings.ToLower(s), f) {
					t.Errorf("ПД %q в %s", f, s)
				}
			}
		}
	}
	for _, ev := range []string{identity.EventUserRegistered, identity.EventUserEmailChanged, identity.EventNicknameChanged,
		identity.EventPasswordChanged, identity.EventMfaChanged, identity.EventUserDeletionScheduled,
		identity.EventUserDeletionCancelled, identity.EventSessionsRevoked} {
		if !slices.ContainsFunc(payloads, func(s string) bool { return strings.HasPrefix(s, ev+" ") }) {
			t.Errorf("события %s нет — сценарий не дошёл", ev)
		}
	}
	if !slices.ContainsFunc(args, func(s string) bool { return strings.Contains(s, "identity.email_change_code") }) {
		t.Errorf("задач писем нет — сценарий не дошёл: %v", args)
	}
}

// TestCoreIDOR — чужой объект отвечает так же, как несуществующий, и не меняется: сессия другого аккаунта, код смены
// почты другого аккаунта, сброс 2FA поддержкой у другого аккаунта.
func TestCoreIDOR(t *testing.T) {
	t.Parallel()
	e := newCoreEnv(t)
	ctx := context.Background()
	a := e.session(t, e.seedUser(t, "idor_a", corePW1))
	bID := e.seedUser(t, "idor_b", corePW1)
	b := e.session(t, bID)
	var bSession uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM sessions WHERE user_id = $1`, bID).Scan(&bSession); err != nil {
		t.Fatal(err)
	}
	same := func(what string, foreign, missing *httptest.ResponseRecorder) {
		t.Helper()
		if foreign.Code != missing.Code || problemCodeOf(t, foreign) != problemCodeOf(t, missing) ||
			foreign.Header().Get("Content-Type") != missing.Header().Get("Content-Type") {
			t.Errorf("%s: чужой %d %s, несуществующий %d %s", what, foreign.Code, foreign.Body.String(), missing.Code,
				missing.Body.String())
		}
	}
	// сессии
	same("revokeMySession", e.do(t, http.MethodDelete, "/v1/me/sessions/"+bSession.String(), "", a),
		e.do(t, http.MethodDelete, "/v1/me/sessions/"+uuid.Must(uuid.NewV7()).String(), "", a))
	if n := count(t, e.pool, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, bSession); n != 1 {
		t.Error("чужая сессия отозвана")
	}
	// код смены почты
	sent := e.ok(t, "email change b", e.do(t, http.MethodPost, "/v1/me/email-change", `{"new_email":"idor_b_new@example.com"}`, b))
	bCode := str(t, sent, "code_id")
	code := e.code(t, bCode)
	same("confirmEmailChange", e.do(t, http.MethodPost, "/v1/me/email-change/confirm", `{"code_id":"`+bCode+`","code":"`+code+`"}`, a),
		e.do(t, http.MethodPost, "/v1/me/email-change/confirm", `{"code_id":"`+uuid.Must(uuid.NewV7()).String()+`","code":"`+code+`"}`, a))
	if n := count(t, e.pool, `SELECT count(*) FROM auth_codes WHERE id = $1 AND attempts = 0 AND consumed_at IS NULL
		AND revoked_at IS NULL`, uuid.MustParse(bCode)); n != 1 {
		t.Error("чужой код тронут")
	}
	if n := count(t, e.pool, `SELECT count(*) FROM users WHERE email IN ('idor_a@example.com', 'idor_b@example.com')`); n != 2 {
		t.Error("почта сменилась чужим кодом")
	}
	// сброс 2FA поддержкой
	missing := e.do(t, http.MethodDelete, "/v1/me/mfa-reset", "", a)
	var rid uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label,
		reason, execute_at, created_at) VALUES (gen_random_uuid(), $1, gen_random_uuid(), gen_random_uuid(), 'Ольга',
		'потерян телефон', $2, $3) RETURNING id`, bID, e.clock.Now().Add(7*24*time.Hour), e.clock.Now()).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	same("cancelMyMfaReset", e.do(t, http.MethodDelete, "/v1/me/mfa-reset", "", a), missing)
	if n := count(t, e.pool, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_at IS NULL`, rid); n != 1 {
		t.Error("чужой сброс отменён")
	}
}

// count — одно число запросом.
func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
