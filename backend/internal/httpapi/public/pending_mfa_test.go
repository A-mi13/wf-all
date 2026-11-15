package public_test

import (
	"encoding/base32"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/otp"
)

// pendingMfa — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 16 плана I-2: все шесть реализованы.
var pendingMfa = []string{}

// totpSecret — сырой секрет из base32 ответа setupTotp.
func totpSecret(t *testing.T, b32 string) []byte {
	t.Helper()
	s, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Task 16 через весь конвейер по контракту: getMyMfa (200 private); setupTotp и confirmTotp (200 no-store, неверный
// код — 400 mfa_invalid); regenerateRecoveryCodes (200 no-store); вход паролем — mfa_required, completeMfa: неверный
// код — 400 attempts_left, верный — 200 AuthResult no-store; disableTotp сессией без второго фактора — 403
// reauth_required, сессией после completeMfa — 204; повтор — 409 mfa_not_enabled.
func TestMfaHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	const pw = "Пенальти на последней минуте" //nolint:gosec // G101: пароль теста, не учётные данные
	e.seedLogin(t, "http_mfa", pw)
	access := e.login(t, "http_mfa", pw).Tokens.AccessToken

	st := e.do(t, http.MethodGet, "/v1/me/mfa", "", access, false)
	if st.Code != http.StatusOK || st.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("getMyMfa: %d %v %s", st.Code, st.Header(), st.Body.String())
	}
	setup := e.do(t, http.MethodPost, "/v1/me/mfa/totp/setup", "", access, true)
	if setup.Code != http.StatusOK || setup.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("setupTotp: %d %v %s", setup.Code, setup.Header(), setup.Body.String())
	}
	var ts oapi.TotpSetup
	if err := json.Unmarshal(setup.Body.Bytes(), &ts); err != nil {
		t.Fatal(err)
	}
	secret := totpSecret(t, ts.Secret)
	step := otp.Step(time.Now())
	code := otp.Code(secret, step)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if r := e.do(t, http.MethodPost, "/v1/me/mfa/totp/confirm", `{"code":"`+wrong+`"}`, access, true); r.Code != http.StatusBadRequest ||
		problemCodeOf(t, r) != identity.CodeMfaInvalid {
		t.Fatalf("неверный код: %d %s", r.Code, r.Body.String())
	}
	confirm := e.do(t, http.MethodPost, "/v1/me/mfa/totp/confirm", `{"code":"`+code+`"}`, access, true)
	if confirm.Code != http.StatusOK || confirm.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("confirmTotp: %d %v %s", confirm.Code, confirm.Header(), confirm.Body.String())
	}
	regen := e.do(t, http.MethodPost, "/v1/me/mfa/recovery-codes", "", access, true)
	if regen.Code != http.StatusOK || regen.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("regenerate: %d %v %s", regen.Code, regen.Header(), regen.Body.String())
	}

	login := e.login(t, "http_mfa", pw)
	if login.Status != oapi.AuthStatusMfaRequired || login.Mfa == nil {
		t.Fatalf("вход при 2FA: %+v", login)
	}
	complete := func(c string) *httptest.ResponseRecorder {
		return e.do(t, http.MethodPost, "/v1/auth/mfa", `{"mfa_token":"`+login.Mfa.MfaToken+`","code":"`+c+`","device":`+deviceJSON+`}`, "", false)
	}
	if r := complete(wrong); r.Code != http.StatusBadRequest || problemOfBody(t, r)["attempts_left"] != 4.0 {
		t.Fatalf("completeMfa неверный: %d %s", r.Code, r.Body.String())
	}
	done := complete(otp.Code(secret, step+1)) // шаг confirm уже принят (last_used_step)
	if done.Code != http.StatusOK || done.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("completeMfa: %d %v %s", done.Code, done.Header(), done.Body.String())
	}
	var res oapi.AuthResult
	if err := json.Unmarshal(done.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != oapi.AuthStatusAuthenticated || res.Tokens == nil || res.User == nil {
		t.Fatalf("%+v", res)
	}

	if r := e.do(t, http.MethodDelete, "/v1/me/mfa/totp", "", access, true); r.Code != http.StatusForbidden ||
		problemCodeOf(t, r) != identity.CodeReauthRequired {
		t.Fatalf("выключение без второго фактора: %d %s", r.Code, r.Body.String())
	}
	if r := e.do(t, http.MethodDelete, "/v1/me/mfa/totp", "", res.Tokens.AccessToken, true); r.Code != http.StatusNoContent {
		t.Fatalf("выключение: %d %s", r.Code, r.Body.String())
	}
	if r := e.do(t, http.MethodDelete, "/v1/me/mfa/totp", "", res.Tokens.AccessToken, true); r.Code != http.StatusConflict ||
		problemCodeOf(t, r) != identity.CodeMfaNotEnabled {
		t.Fatalf("повтор: %d %s", r.Code, r.Body.String())
	}
}

// m5 ревью W5-А: коды контракта Task 16 через весь конвейер (apitest сверяет Problem с объявленными ответами):
// confirmTotp без начатой настройки — 409 mfa_setup_missing; regenerateRecoveryCodes без 2FA — 409 mfa_not_enabled;
// несвежая сессия — 403 reauth_required у setupTotp и regenerateRecoveryCodes; setupTotp при включённой — 409
// mfa_already_enabled; completeMfa: неизвестный токен — 410 mfa_expired, мобильный без device — 422 device_required,
// 10 неудач фактора за час — 429 ratelimit.exceeded с Retry-After.
func TestMfaHTTPCodes(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	const pw = "Угловой на последней секунде" //nolint:gosec // G101: пароль теста, не учётные данные
	uid := e.seedLogin(t, "http_mfa_codes", pw)
	access := e.login(t, "http_mfa_codes", pw).Tokens.AccessToken
	want := func(name string, r *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if r.Code != status || problemCodeOf(t, r) != code {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	want("confirm без setup", e.do(t, http.MethodPost, "/v1/me/mfa/totp/confirm", `{"code":"123456"}`, access, true),
		http.StatusConflict, identity.CodeMfaSetupMissing)
	want("regenerate без 2FA", e.do(t, http.MethodPost, "/v1/me/mfa/recovery-codes", "", access, true),
		http.StatusConflict, identity.CodeMfaNotEnabled)

	freshness := func(at string) {
		t.Helper()
		if _, err := e.pool.Exec(t.Context(), `UPDATE sessions SET reauth_at = `+at+` WHERE user_id = $1`, uid); err != nil {
			t.Fatal(err)
		}
	}
	freshness("now() - interval '1 hour'")
	want("setup несвежей", e.do(t, http.MethodPost, "/v1/me/mfa/totp/setup", "", access, true),
		http.StatusForbidden, identity.CodeReauthRequired)
	want("regenerate несвежей", e.do(t, http.MethodPost, "/v1/me/mfa/recovery-codes", "", access, true),
		http.StatusForbidden, identity.CodeReauthRequired)
	freshness("now()")

	setup := e.do(t, http.MethodPost, "/v1/me/mfa/totp/setup", "", access, true)
	if setup.Code != http.StatusOK {
		t.Fatalf("setupTotp: %d %s", setup.Code, setup.Body.String())
	}
	var ts oapi.TotpSetup
	if err := json.Unmarshal(setup.Body.Bytes(), &ts); err != nil {
		t.Fatal(err)
	}
	secret := totpSecret(t, ts.Secret)
	code := otp.Code(secret, otp.Step(time.Now()))
	if r := e.do(t, http.MethodPost, "/v1/me/mfa/totp/confirm", `{"code":"`+code+`"}`, access, true); r.Code != http.StatusOK {
		t.Fatalf("confirmTotp: %d %s", r.Code, r.Body.String())
	}
	want("setup при включённой", e.do(t, http.MethodPost, "/v1/me/mfa/totp/setup", "", access, true),
		http.StatusConflict, identity.CodeMfaAlreadyEnabled)

	completeMfa := func(tok, body string) *httptest.ResponseRecorder {
		return e.do(t, http.MethodPost, "/v1/auth/mfa", `{"mfa_token":"`+tok+`",`+body+`}`, "", false)
	}
	withDevice := `"code":"` + code + `","device":` + deviceJSON
	want("неизвестный токен", completeMfa("unknown-token", withDevice), http.StatusGone, identity.CodeMfaExpired)
	tok := e.login(t, "http_mfa_codes", pw).Mfa.MfaToken
	want("без device", completeMfa(tok, `"code":"`+code+`"`), http.StatusUnprocessableEntity, identity.CodeDeviceRequired)

	// 10 неудач user:mfa: два токена по 5 (пятая гасит токен — 410), третий — 429 до проверки фактора
	wrong := `"recovery_code":"aaaaa-aaaaa","device":` + deviceJSON
	for range 2 {
		tok := e.login(t, "http_mfa_codes", pw).Mfa.MfaToken
		for i := range 5 {
			r := completeMfa(tok, wrong)
			if (i < 4 && r.Code != http.StatusBadRequest) || (i == 4 && r.Code != http.StatusGone) {
				t.Fatalf("неудача %d: %d %s", i, r.Code, r.Body.String())
			}
		}
	}
	r := completeMfa(e.login(t, "http_mfa_codes", pw).Mfa.MfaToken, wrong)
	want("лимит user:mfa", r, http.StatusTooManyRequests, httpx.CodeRateLimited)
	if r.Header().Get("Retry-After") == "" {
		t.Fatal("429 без Retry-After")
	}
}
