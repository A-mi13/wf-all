package admin_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: TOTP по RFC 6238 — HMAC-SHA-1, как у приложений-аутентификаторов
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/token"
	"wf/backend/internal/staff"
	staffadmin "wf/backend/internal/staff/admin"
)

// Сквозные тесты операций тега staff (Task 10a): настоящие сценарии staff на чистой базе под ролью
// admin за полным конвейером admin-api — сессия по cookie, CSRF, валидатор, права, лимиты,
// идемпотентность. Приглашение «оператора» заводится строками SQL (staff/internal/app отсюда не
// импортировать — archtest); код приглашения шифруется кольцом так же, как newInviteCode.

const (
	staffPassword = "Жёлтая карточка на 90-й" //nolint:gosec // G101: пароль тестовой учётки
	newPassword   = "Свисток на 46-й минуте"  //nolint:gosec // G101: новый пароль тестовой учётки
	inviteCode    = "k7m2p-x4q6d"
)

// argon2 считает долго — один хэшер на пакет
var staffHasher = sync.OnceValues(func() (*password.Hasher, error) {
	return password.NewHasher(password.DefaultParams(), 4)
})

// staffEnv — admin-api с настоящим модулем staff: owner — суперпользователь (подготовка и проверки).
type staffEnv struct {
	t     *testing.T
	owner *pgxpool.Pool
	clock *clocktest.Fake
	ring  *keys.Ring
	h     http.Handler
	// onLoad — вызывается после каждой загрузки сессии конвейером (между загрузкой и сценарием):
	// гонки «сессию погасили или заменили посреди запроса», барьер двух вкладок. nil — ничего.
	onLoad atomic.Pointer[func(*auth.Staff)]
}

// newStaffEnv — сборка как в cmd/admin-api (10c): Sessions — загрузчик хендлера staff, cookie —
// admin.CookieName(true). opts меняют Options до сборки (лимиты, обёртка загрузчика).
func newStaffEnv(t *testing.T, opts ...func(*admin.Options)) *staffEnv {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "admin")
	clk := clocktest.New(t0)
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatal(err)
	}
	ring, err := keys.NewRing([]string{base64.StdEncoding.EncodeToString(key)})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := staffHasher()
	if err != nil {
		t.Fatal(err)
	}
	rc, err := river.NewClient(riverpgxv5.New(pools.As), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sh := staffadmin.New(staff.Deps{
		Pool: pools.As, Clock: clk, Keys: ring, Hasher: hasher, Limiter: ratelimit.Unlimited, River: rc,
		Rand: rand.Reader, Origin: testOrigin, Log: quiet(),
	}, staffadmin.Cookie{Name: admin.CookieName(true), Secure: true})
	e := &staffEnv{t: t, owner: pools.Owner, clock: clk, ring: ring}
	loader := sh.Sessions()
	o := admin.Options{
		DB: pools.As, Limiter: ratelimit.Unlimited, RateRules: ratelimit.DefaultRules(), Origin: testOrigin,
		CookieSecure: true, Staff: sh, Geo: &geoadmin.Handler{}, Clock: clk,
		Sessions: auth.StaffSessionsFunc(func(ctx context.Context, tok string, ip netip.Addr, bg bool) (*auth.Staff, error) {
			s, err := loader.LoadStaff(ctx, tok, ip, bg)
			if f := e.onLoad.Load(); f != nil && err == nil {
				(*f)(s)
			}
			return s, err
		}),
	}
	for _, f := range opts {
		f(&o)
	}
	e.h = newHandler(t, o)
	return e
}

func (e *staffEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.owner.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *staffEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.owner.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// invite — учётка invited с ролью role и живым приглашением (код inviteCode, отправлен сейчас) — как
// после OperatorBootstrap или исполненного приглашения.
func (e *staffEnv) invite(email, role string) uuid.UUID {
	e.t.Helper()
	staffID, inviteID := uuid.New(), uuid.New()
	now := e.clock.Now()
	e.exec(`INSERT INTO staff_accounts (id, email, display_name, locale, status, created_at)
		VALUES ($1, $2, $3, 'ru', 'invited', $4)`, staffID, email, "Сотрудник "+strings.Split(email, "@")[0], now)
	e.exec(`INSERT INTO staff_roles (staff_id, role, granted_at) VALUES ($1, $2::staff_role, $3)`, staffID, role, now)
	ct, keyID, err := e.ring.Seal([]byte(inviteCode), inviteID[:])
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO staff_invites (id, staff_id, purpose, code_enc, code_key_id, expires_at, sends, last_sent_at, created_at)
		VALUES ($1, $2, 'invite', $3, $4, $5, 1, $6, $6)`, inviteID, staffID, ct, keyID, now.Add(24*time.Hour), now)
	return staffID
}

// send — запрос к admin-api: token — cookie сессии ("" — без cookie), key — Idempotency-Key ("" — без
// него), body — JSON ("" — без тела). Изменяющие методы — с Origin админки и X-WF-Admin: 1.
func (e *staffEnv) send(method, path, token, key, body string, hdr ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequestWithContext(context.Background(), method, path, rd)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.AddCookie(sessionCookie(hostCookie, token))
	}
	if method != http.MethodGet {
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("X-WF-Admin", "1")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	return serve(e.h, r)
}

// anon — анонимная мутация (вход, принятие, самоотправка): без Idempotency-Key, как админка.
func (e *staffEnv) anon(path, token, body string) *httptest.ResponseRecorder {
	return e.send(http.MethodPost, path, token, "", body)
}

// post — мутация сессии с новым Idempotency-Key.
func (e *staffEnv) post(path, token, body string) *httptest.ResponseRecorder {
	return e.send(http.MethodPost, path, token, uuid.NewString(), body)
}

func (e *staffEnv) get(path, token string) *httptest.ResponseRecorder {
	return e.send(http.MethodGet, path, token, "", "")
}

// jsonOf — тело запроса из полей.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bodyOf — тело ответа как объект.
func bodyOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("тело %q: %v", rec.Body.String(), err)
	}
	return m
}

// wantStatus — статус ответа.
func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("ждали %d, получили %d %s", status, rec.Code, rec.Body.String())
	}
}

// wantAttempts — 400 с кодом code и attempts_left = left.
func wantAttempts(t *testing.T, rec *httptest.ResponseRecorder, code string, left int) {
	t.Helper()
	wantProblem(t, rec, http.StatusBadRequest, code)
	if got, ok := bodyOf(t, rec)["attempts_left"]; !ok || got != float64(left) {
		t.Fatalf("attempts_left = %v (%v), ждали %d: %s", got, ok, left, rec.Body.String())
	}
}

// setCookies — заголовки Set-Cookie ответа по порядку.
func setCookies(rec *httptest.ResponseRecorder) []string { return rec.Header().Values("Set-Cookie") }

// newToken — ответ ставит ровно одну новую cookie сессии последним заголовком Set-Cookie: точная
// строка P8 (Path=/, HttpOnly, Secure, SameSite=Strict, без Max-Age, Expires и Domain). Токен.
func newToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	sc := setCookies(rec)
	if len(sc) == 0 {
		t.Fatalf("нет Set-Cookie: %d %s", rec.Code, rec.Body.String())
	}
	last := sc[len(sc)-1]
	tok, ok := strings.CutPrefix(last, hostCookie+"=")
	if !ok {
		t.Fatalf("последний Set-Cookie %q", last)
	}
	tok, attrs, _ := strings.Cut(tok, ";")
	if tok == "" || attrs != " Path=/; HttpOnly; Secure; SameSite=Strict" {
		t.Fatalf("cookie сессии %q", last)
	}
	return tok
}

// clearedCookie — ответ стирает cookie сессии (Max-Age=0, те же атрибуты) — единственным Set-Cookie.
func clearedCookie(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	want := hostCookie + "=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Strict"
	if sc := setCookies(rec); len(sc) != 1 || sc[0] != want {
		t.Fatalf("ждали стирание cookie, Set-Cookie %q", sc)
	}
}

// noCookie — ответ не трогает cookie.
func noCookie(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if sc := setCookies(rec); len(sc) != 0 {
		t.Fatalf("лишний Set-Cookie %q", sc)
	}
}

// totp — код RFC 6238 (SHA-1, 6 цифр, шаг 30 с) секрета base32 без = на момент at.
func totp(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("секрет %q: %v", secret, err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30)) //nolint:gosec // G115: время после 1970
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff%1_000_000)
}

// member — сотрудник, прошедший первый вход: почта, секрет TOTP, резервные коды, токен active.
type member struct {
	id     uuid.UUID
	email  string
	secret string
	codes  []string
	token  string
}

// code — код следующего шага TOTP: часы сдвигаются на шаг (шаг принимается один раз).
func (e *staffEnv) code(m member) string {
	e.clock.Advance(30 * time.Second)
	return totp(e.t, m.secret, e.clock.Now())
}

// firstLogin — приглашение → принятие → секрет → подтверждение → ack: сотрудник с активной сессией.
func (e *staffEnv) firstLogin(email, role string) member {
	e.t.Helper()
	m := member{id: e.invite(email, role), email: email}
	rec := e.anon("/v1/staff/auth/invite/accept", "", jsonOf(e.t, map[string]string{"email": email, "code": inviteCode, "password": staffPassword}))
	wantStatus(e.t, rec, http.StatusOK)
	setup := newToken(e.t, rec)
	rec = e.post("/v1/staff/me/mfa/setup", setup, "")
	wantStatus(e.t, rec, http.StatusOK)
	m.secret, _ = bodyOf(e.t, rec)["secret"].(string)
	rec = e.post("/v1/staff/me/mfa/confirm", setup, jsonOf(e.t, map[string]string{"code": totp(e.t, m.secret, e.clock.Now())}))
	wantStatus(e.t, rec, http.StatusOK)
	var codes struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &codes); err != nil || len(codes.RecoveryCodes) != 10 {
		e.t.Fatalf("коды: %v %s", err, rec.Body.String())
	}
	m.codes = codes.RecoveryCodes
	rec = e.post("/v1/staff/me/mfa/recovery-codes/ack", setup, "")
	wantStatus(e.t, rec, http.StatusNoContent)
	m.token = newToken(e.t, rec)
	return m
}

// login — шаг пароля без cookie: токен mfa_pending.
func (e *staffEnv) login(m member) string {
	e.t.Helper()
	rec := e.anon("/v1/staff/auth/login", "", e.creds(m.email, staffPassword))
	wantStatus(e.t, rec, http.StatusOK)
	return newToken(e.t, rec)
}

// signIn — вход целиком (пароль и TOTP) без cookie: токен active.
func (e *staffEnv) signIn(m member) string {
	e.t.Helper()
	rec := e.post("/v1/staff/auth/mfa", e.login(m), e.factor(e.code(m), ""))
	wantStatus(e.t, rec, http.StatusNoContent)
	return newToken(e.t, rec)
}

func (e *staffEnv) creds(email, pw string) string {
	return jsonOf(e.t, map[string]string{"email": email, "password": pw})
}

// factor — тело StaffFactorRequest: код или резервный код.
func (e *staffEnv) factor(code, recovery string) string {
	if recovery != "" {
		return jsonOf(e.t, map[string]string{"recovery_code": recovery})
	}
	return jsonOf(e.t, map[string]string{"code": code})
}

// wrongCode — 6 цифр, заведомо не код текущего шага.
func (e *staffEnv) wrongCode(m member) string {
	c := totp(e.t, m.secret, e.clock.Now())
	if c == "000000" {
		return "111111"
	}
	return "000000"
}

// sessionReason — причина гашения сессии с токеном tok ("" — живая).
func (e *staffEnv) sessionReason(tok string) string {
	e.t.Helper()
	var reason *string
	h := token.HashRefresh(tok)
	if err := e.owner.QueryRow(context.Background(), `SELECT revoked_reason FROM staff_sessions WHERE token_hash = $1`, h).Scan(&reason); err != nil {
		e.t.Fatalf("сессия: %v", err)
	}
	if reason == nil {
		return ""
	}
	return *reason
}

// containsAny — b содержит хоть одну из строк.
func containsAny(b []byte, ss ...string) bool {
	for _, s := range ss {
		if s != "" && bytes.Contains(b, []byte(s)) {
			return true
		}
	}
	return false
}

// httpRec — ответ httptest.
type httpRec = httptest.ResponseRecorder
