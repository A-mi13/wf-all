package public_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/token"
)

// pendingPassword — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 13 плана I-2: все шесть реализованы.
var pendingPassword = []string{}

const httpOldPassword = "Старый добрый пароль 2026" //nolint:gosec // G101: пароль теста, не учётные данные

// seedHTTPUser — аккаунт с паролем (настоящий argon2) и, если нужна сессия, живой сессией с reauth_at = reauthAt.
func seedHTTPUser(t *testing.T, pool *pgxpool.Pool, nick string) uuid.UUID {
	t.Helper()
	h, err := password.NewHasher(password.DefaultParams(), 1)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := h.Hash(context.Background(), httpOldPassword)
	if err != nil {
		t.Fatal(err)
	}
	var uid uuid.UUID
	if err := pool.QueryRow(context.Background(), `WITH u AS (
		INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ($1 || '@example.com', now(), $1, '2000-01-01', 'ru') RETURNING id)
		INSERT INTO credentials (user_id, password_hash, updated_at) SELECT id, $2, now() FROM u RETURNING user_id`,
		nick, hash).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	return uid
}

// sessionOf — живая сессия аккаунта в базе и загрузчик с ней (свежесть — reauth_at).
func sessionOf(t *testing.T, nick string, reauthAt time.Time) func(pool *pgxpool.Pool, issuer *token.Issuer) *fakeSession {
	return func(pool *pgxpool.Pool, issuer *token.Issuer) *fakeSession {
		uid := seedHTTPUser(t, pool, nick)
		f := &fakeSession{userID: uid, sessionID: uuid.Must(uuid.NewV7())}
		if _, err := pool.Exec(context.Background(), `INSERT INTO sessions (id, user_id, client_kind, auth_method, reauth_at, ip,
			created_at, last_seen_at, idle_expires_at, absolute_expires_at)
			VALUES ($1, $2, 'web', 'password', $3, '203.0.113.7', now(), now(), now() + interval '30 days', now() + interval '180 days')`,
			f.sessionID, uid, reauthAt); err != nil {
			t.Fatal(err)
		}
		var err error
		if f.token, _, err = issuer.Access(uid, f.sessionID); err != nil {
			t.Fatal(err)
		}
		return f
	}
}

// Task 13 через весь конвейер: сброс пароля (анонимно, 202 private; неверный код — 400 attempts_left; слабый — 422
// reasons; верный — 200 AuthResult no-store); свежесть (несвежая сессия — 403 reauth_required, reauthenticate паролем —
// 200 reauth_until, смена пароля — 204); смена почты (202, неверный код — 400, верный — 200 Me с новой почтой). Каждая
// мутация с замком — с Idempotency-Key; ответы сверены с контрактом.
func TestPasswordHTTP(t *testing.T) {
	t.Parallel()
	e := newCodeEnv(t, sessionOf(t, "http_me", time.Now().Add(-time.Hour)))
	seedHTTPUser(t, e.pool, "http_reset")
	start := e.do(t, http.MethodPost, "/v1/auth/password-reset", `{"email":"http_reset@example.com"}`, false, false)
	if start.Code != http.StatusAccepted || start.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("старт сброса: %d %v %s", start.Code, start.Header(), start.Body.String())
	}
	var sent oapi.CodeSent
	if err := json.Unmarshal(start.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	unknown := e.do(t, http.MethodPost, "/v1/auth/password-reset", `{"email":"nobody@example.com"}`, false, false)
	if unknown.Code != http.StatusAccepted || len(unknown.Header()) != len(start.Header()) {
		t.Fatalf("неизвестная почта: %d %v", unknown.Code, unknown.Header())
	}
	code := e.code(t, sent.CodeId)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	confirm := func(c, pw string) *httptest.ResponseRecorder {
		return e.do(t, http.MethodPost, "/v1/auth/password-reset/confirm", `{"code_id":"`+sent.CodeId.String()+`","code":"`+c+
			`","new_password":"`+pw+`","device":`+deviceJSON+`}`, false, false)
	}
	if r := confirm(wrong, "Свисток на 46-й минуте"); r.Code != http.StatusBadRequest || problemOfBody(t, r)["attempts_left"] != 4.0 {
		t.Fatalf("неверный код: %d %s", r.Code, r.Body.String())
	}
	if r := confirm(code, "short"); r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), `"reasons":["too_short"]`) {
		t.Fatalf("слабый пароль: %d %s", r.Code, r.Body.String())
	}
	if r := confirm(code, "Свисток на 46-й минуте"); r.Code != http.StatusOK || r.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(r.Body.String(), `"status":"authenticated"`) {
		t.Fatalf("сброс: %d %v %s", r.Code, r.Header(), r.Body.String())
	}

	if r := e.do(t, http.MethodPost, "/v1/me/password", `{"new_password":"Свисток на 46-й минуте"}`, true, true); r.Code != http.StatusForbidden ||
		problemOfBody(t, r)["code"] != identity.CodeReauthRequired {
		t.Fatalf("несвежая: %d %s", r.Code, r.Body.String())
	}
	if r := e.do(t, http.MethodPost, "/v1/me/reauth", `{"password":"Неверный пароль 2026"}`, true, true); r.Code != http.StatusBadRequest ||
		problemOfBody(t, r)["attempts_left"] != 4.0 {
		t.Fatalf("неверный пароль: %d %s", r.Code, r.Body.String())
	}
	reauth := e.do(t, http.MethodPost, "/v1/me/reauth", `{"password":"`+httpOldPassword+`"}`, true, true)
	if reauth.Code != http.StatusOK || !strings.Contains(reauth.Body.String(), `"reauth_until"`) {
		t.Fatalf("свежий вход: %d %s", reauth.Code, reauth.Body.String())
	}
	if r := e.do(t, http.MethodPost, "/v1/me/password", `{"new_password":"Свисток на 46-й минуте"}`, true, true); r.Code != http.StatusNoContent {
		t.Fatalf("смена пароля: %d %s", r.Code, r.Body.String())
	}
	ec := e.do(t, http.MethodPost, "/v1/me/email-change", `{"new_email":"http_me_new@example.com"}`, true, true)
	if ec.Code != http.StatusAccepted || ec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("смена почты: %d %s", ec.Code, ec.Body.String())
	}
	var ecSent oapi.CodeSent
	if err := json.Unmarshal(ec.Body.Bytes(), &ecSent); err != nil {
		t.Fatal(err)
	}
	ecCode := e.code(t, ecSent.CodeId)
	ecWrong := "000000"
	if ecCode == ecWrong {
		ecWrong = "111111"
	}
	if r := e.do(t, http.MethodPost, "/v1/me/email-change/confirm", `{"code_id":"`+ecSent.CodeId.String()+`","code":"`+ecWrong+`"}`,
		true, true); r.Code != http.StatusBadRequest || problemOfBody(t, r)["code"] != identity.CodeCodeInvalid {
		t.Fatalf("неверный код почты: %d %s", r.Code, r.Body.String())
	}
	if r := e.do(t, http.MethodPost, "/v1/me/email-change/confirm", `{"code_id":"`+ecSent.CodeId.String()+`","code":"`+ecCode+`"}`,
		true, true); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"email":"http_me_new@example.com"`) {
		t.Fatalf("подтверждение почты: %d %s", r.Code, r.Body.String())
	}
}
