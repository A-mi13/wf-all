package public_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
)

// opRequest — запрос операции, годный по схеме контракта (валидатор стоит до хендлера).
type opRequest struct{ method, path, body string }

// opsBase — строка запроса на каждую операцию публичного контракта: операция без строки — падение
// TestNoStubOperations (новая операция не проходит мимо стража).
var opsBase = map[string]opRequest{
	"listCities":       {http.MethodGet, "/v1/cities?limit=1", ""},
	"findNearestCity":  {http.MethodGet, "/v1/cities/nearest?lat=45.0344&lon=41.9642", ""},
	"getCityBySlug":    {http.MethodGet, "/v1/cities/by-slug/stavropol", ""},
	"getCity":          {http.MethodGet, "/v1/cities/3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47", ""},
	"getAppMinVersion": {http.MethodGet, "/v1/app/min-version?platform=ios", ""},
	// identity — вход и регистрация, аккаунт, сессии (план I-1, Task 2)
	"checkNickname": {http.MethodGet, "/v1/nicknames/availability?nickname=kosta_10", ""},
	"startSignup": {http.MethodPost, "/v1/auth/signup", `{"email":"kosta.f@example.com","password":"Жёлтая карточка на 90-й",` +
		`"nickname":"kosta_10","birth_date":"1998-04-17","city_id":"3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47",` +
		`"accepted_documents":[{"document":"terms","version":"2026-11-01"},{"document":"privacy","version":"2026-11-01"}],` +
		`"locale":"ru","device":` + deviceJSON + `}`},
	"confirmSignup":      {http.MethodPost, "/v1/auth/signup/confirm", `{"code_id":"0193a1c3-1f2e-7d31-a4b5-6c7d8e9f0a12","code":"492817","device":` + deviceJSON + `}`},
	"resendAuthCode":     {http.MethodPost, "/v1/auth/codes/0193a1c3-1f2e-7d31-a4b5-6c7d8e9f0a12/resend", ""},
	"login":              {http.MethodPost, "/v1/auth/login", `{"email":"kosta.f@example.com","password":"Жёлтая карточка на 90-й","device":` + deviceJSON + `}`},
	"completeMfa":        {http.MethodPost, "/v1/auth/mfa", `{"mfa_token":"Qm9vdHN0cmFwLW1mYS10b2tlbi0yNTYtYml0cy1iYXNlNjR1","code":"492817"}`},
	"startPasswordReset": {http.MethodPost, "/v1/auth/password-reset", `{"email":"kosta.f@example.com"}`},
	"confirmPasswordReset": {http.MethodPost, "/v1/auth/password-reset/confirm", `{"code_id":"0193a1c3-1f2e-7d31-a4b5-6c7d8e9f0a12",` +
		`"code":"492817","new_password":"Свисток на 46-й минуте","mfa":{"recovery_code":"k7m2q-x9p4t"}}`},
	"refreshSession":        {http.MethodPost, "/v1/auth/refresh", `{"refresh_token":"Yx3kQp1bZt7Lm2Vb4Np8Rt1Zw6Yc0Hs5Ae9Df3Gh7Jk","install_id":"6F9619FF-8B86-4D01-B42D-00CF4FC964FF"}`},
	"cancelDeletion":        {http.MethodPost, "/v1/auth/deletion/cancel", `{"restore_token":"UmVzdG9yZS10b2tlbi0yNTYtYml0cy1iYXNlNjR1cmwtYWJj"}`},
	"getLegalDocuments":     {http.MethodGet, "/v1/legal/documents", ""},
	"createDeviceChallenge": {http.MethodPost, "/v1/devices/challenges", ""},
	"logout":                {http.MethodPost, "/v1/auth/logout", ""},
	"getMe":                 {http.MethodGet, "/v1/me", ""},
	"updateMe":              {http.MethodPatch, "/v1/me", `{"locale":"en"}`},
	"changeNickname":        {http.MethodPatch, "/v1/me/nickname", `{"nickname":"kosta_11"}`},
	"listMySessions":        {http.MethodGet, "/v1/me/sessions", ""},
	"revokeMySession":       {http.MethodDelete, "/v1/me/sessions/0193a1c2-7b3e-7c21-9d5e-3e8f1a2b4c70", ""},
	"revokeOtherSessions":   {http.MethodPost, "/v1/me/sessions/revoke-others", ""},
	"changePassword":        {http.MethodPost, "/v1/me/password", `{"new_password":"Свисток на 46-й минуте"}`},
	"startEmailChange":      {http.MethodPost, "/v1/me/email-change", `{"new_email":"kosta.new@example.com"}`},
	"confirmEmailChange":    {http.MethodPost, "/v1/me/email-change/confirm", `{"code_id":"0193a1c3-1f2e-7d31-a4b5-6c7d8e9f0a12","code":"492817"}`},
	"acceptDocuments":       {http.MethodPost, "/v1/me/documents", `{"accepted_documents":[{"document":"terms","version":"2026-11-01"}]}`},
	"deleteAccount":         {http.MethodPost, "/v1/me/delete", `{"confirm_nickname":"kosta_10"}`},
	// identity — 2FA, ключи доступа, соцвходы, способы входа, свежий вход (план I-1, Task 3)
	"getAuthMethods":           {http.MethodGet, "/v1/auth/methods", ""},
	"startMfaPasskey":          {http.MethodPost, "/v1/auth/mfa/passkey-options", `{"mfa_token":"Qm9vdHN0cmFwLW1mYS10b2tlbi0yNTYtYml0cy1iYXNlNjR1"}`},
	"createSocialNonce":        {http.MethodPost, "/v1/auth/social/nonces", `{"provider":"apple"}`},
	"socialLogin":              {http.MethodPost, "/v1/auth/social", `{"provider":"apple","nonce_id":"0193a1c6-4c5d-7a61-97e8-9f0a1b2c3d45",` + `"id_token":"` + providerJWT + `","apple":{"authorization_code":"c1a2b3d4e5f6.0.ab12.example-authorization-code"},"device":` + deviceJSON + `}`},
	"completeSocialSignup":     {http.MethodPost, "/v1/auth/social/signup", `{"signup_token":"signup-token-from-social-login",` + `"city_id":"3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47","birth_date":"1998-04-17","nickname":"kosta_10",` + `"accepted_documents":[{"document":"terms","version":"2026-11-01"}],"device":` + deviceJSON + `}`},
	"startPasskeyLogin":        {http.MethodPost, "/v1/auth/passkey/options", ""},
	"passkeyLogin":             {http.MethodPost, "/v1/auth/passkey", `{"challenge_id":"0193a1c5-3b4c-7f51-86d7-8e9f0a1b2c34","credential":` + credentialJSON + `,"device":` + deviceJSON + `}`},
	"reauthenticate":           {http.MethodPost, "/v1/me/reauth", `{"totp_code":"492817"}`},
	"startReauthPasskey":       {http.MethodPost, "/v1/me/reauth/passkey-options", ""},
	"listMyAuthMethods":        {http.MethodGet, "/v1/me/auth-methods", ""},
	"linkIdentity":             {http.MethodPost, "/v1/me/identities", `{"provider":"google","nonce_id":"0193a1c6-4c5d-7a61-97e8-9f0a1b2c3d45","id_token":"` + providerJWT + `"}`},
	"unlinkIdentity":           {http.MethodDelete, "/v1/me/identities/0193a1c7-5d6e-7b71-a8f9-0a1b2c3d4e56", ""},
	"getMyMfa":                 {http.MethodGet, "/v1/me/mfa", ""},
	"setupTotp":                {http.MethodPost, "/v1/me/mfa/totp/setup", ""},
	"confirmTotp":              {http.MethodPost, "/v1/me/mfa/totp/confirm", `{"code":"492817"}`},
	"disableTotp":              {http.MethodDelete, "/v1/me/mfa/totp", ""},
	"regenerateRecoveryCodes":  {http.MethodPost, "/v1/me/mfa/recovery-codes", ""},
	"startPasskeyRegistration": {http.MethodPost, "/v1/me/passkeys/registration-options", ""},
	"registerPasskey":          {http.MethodPost, "/v1/me/passkeys", `{"challenge_id":"0193a1c5-3b4c-7f51-86d7-8e9f0a1b2c34","credential":` + credentialJSON + `,"name":"iPhone"}`},
	"renamePasskey":            {http.MethodPatch, "/v1/me/passkeys/0193a1c8-6e7f-7c81-b90a-1b2c3d4e5f67", `{"name":"Рабочий ноутбук"}`},
	"deletePasskey":            {http.MethodDelete, "/v1/me/passkeys/0193a1c8-6e7f-7c81-b90a-1b2c3d4e5f67", ""},
	"cancelMyMfaReset":         {http.MethodDelete, "/v1/me/mfa-reset", ""},
	// identity — капитанство (план I-1, Task 4)
	"getMyCaptaincy":             {http.MethodGet, "/v1/me/captaincy", ""},
	"listMyCaptainApplications":  {http.MethodGet, "/v1/me/captain-applications?limit=20", ""},
	"submitCaptainApplication":   {http.MethodPost, "/v1/captain-applications", `{"proposed_format":"6×6 по средам","contact_link":"https://t.me/kosta_10","phone":"+79181234567"}`},
	"withdrawCaptainApplication": {http.MethodPost, "/v1/captain-applications/0193a1c9-7f80-7d91-8a1b-2c3d4e5f6a78/withdraw", ""},
}

// providerJWT — выдуманный id_token провайдера (форма JWT, подпись — слово signature).
const providerJWT = "eyJhbGciOiJFZERTQSIsImtpZCI6Ikh4M2s5UXBMIn0.eyJzdWIiOiIwMTkzMmYxYy03YTNlIn0.c2lnbmF0dXJl"

// credentialJSON — ответ платформенного API WebAuthn (сервер разбирает его сам; схема — свободный объект).
const credentialJSON = `{"id":"KbDl1lrv0Ue6hXXH3Z2E1g","rawId":"KbDl1lrv0Ue6hXXH3Z2E1g","type":"public-key",` +
	`"response":{"clientDataJSON":"eyJ0eXBlIjoid2ViYXV0aG4uZ2V0In0","signature":"MEUCIQDgYk0"}}`

// deviceJSON — годное по схеме DeviceInfo устройство мобильного клиента.
const deviceJSON = `{"platform":"ios","install_id":"6F9619FF-8B86-4D01-B42D-00CF4FC964FF","model":"iPhone15,2",` +
	`"os_version":"18.1","app_version":"1.4.0"}`

// pendingOps — операции контракта, которые собранный сервер ещё не исполняет: отвечают 501 http.not_implemented
// (контракт identity заморожен до кода, этапы спеки выкатываются по очереди — план I-1, R1, R2). Реализовали
// операцию — убрать её отсюда (TestNoStubOperations падает «убрать из pendingOps»). Список и TestPendingOpsExpire
// удаляет последняя задача последнего плана identity.
var pendingOps = []string{
	// Task 2: вход и регистрация, аккаунт, сессии — этап 4 спеки identity
	"checkNickname", "startSignup", "confirmSignup", "resendAuthCode", "login", "completeMfa", "startPasswordReset",
	"confirmPasswordReset", "refreshSession", "cancelDeletion", "getLegalDocuments", "createDeviceChallenge", "logout",
	"getMe", "updateMe", "changeNickname", "listMySessions", "revokeMySession", "revokeOtherSessions", "changePassword",
	"startEmailChange", "confirmEmailChange", "acceptDocuments", "deleteAccount",
	// Task 3: 2FA, ключи доступа, соцвходы, способы входа, свежий вход — этапы 5, 7, 8 спеки identity
	"getAuthMethods", "startMfaPasskey", "createSocialNonce", "socialLogin", "completeSocialSignup", "startPasskeyLogin",
	"passkeyLogin", "reauthenticate", "startReauthPasskey", "listMyAuthMethods", "linkIdentity", "unlinkIdentity",
	"getMyMfa", "setupTotp", "confirmTotp", "disableTotp", "regenerateRecoveryCodes", "startPasskeyRegistration",
	"registerPasskey", "renamePasskey", "deletePasskey", "cancelMyMfaReset",
	// Task 4: капитанство — этап 9 спеки identity
	"getMyCaptaincy", "listMyCaptainApplications", "submitCaptainApplication", "withdrawCaptainApplication",
}

// pendingOpsAtFreeze — замороженное множество (R2, R17): после заморозки контракта identity (Task 5 плана I-1) только
// уменьшается; pendingOps — его подмножество, поэтому операция другого модуля или новая операция без хендлера в
// список не прячется.
var pendingOpsAtFreeze = []string{
	// Task 2
	"checkNickname", "startSignup", "confirmSignup", "resendAuthCode", "login", "completeMfa", "startPasswordReset",
	"confirmPasswordReset", "refreshSession", "cancelDeletion", "getLegalDocuments", "createDeviceChallenge", "logout",
	"getMe", "updateMe", "changeNickname", "listMySessions", "revokeMySession", "revokeOtherSessions", "changePassword",
	"startEmailChange", "confirmEmailChange", "acceptDocuments", "deleteAccount",
	// Task 3
	"getAuthMethods", "startMfaPasskey", "createSocialNonce", "socialLogin", "completeSocialSignup", "startPasskeyLogin",
	"passkeyLogin", "reauthenticate", "startReauthPasskey", "listMyAuthMethods", "linkIdentity", "unlinkIdentity",
	"getMyMfa", "setupTotp", "confirmTotp", "disableTotp", "regenerateRecoveryCodes", "startPasskeyRegistration",
	"registerPasskey", "renamePasskey", "deletePasskey", "cancelMyMfaReset",
	// Task 4
	"getMyCaptaincy", "listMyCaptainApplications", "submitCaptainApplication", "withdrawCaptainApplication",
}

// Страж «без заглушек» (план I-1, R2): собранный public.Server (подделки geo, версий и сессий) на каждой операции
// контракта: операция из pendingOps — ровно 501 http.not_implemented в формате Problem через весь конвейер; вне
// списка — не 501 и не 401 (запрос дошёл до хендлера). Операции с замком идут с access-токеном живой подделанной
// сессии, мутации — с новым Idempotency-Key.
func TestNoStubOperations(t *testing.T) {
	t.Parallel()
	o := testOptions(t)
	sessions := withFakeSession(t, &o)
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			id := opID(op)
			req, ok := opsBase[id]
			if !ok {
				t.Errorf("%s: нет строки запроса (opsBase)", id)
				continue
			}
			n++
			r := httptest.NewRequestWithContext(context.Background(), req.method, req.path, strings.NewReader(req.body))
			if req.body != "" {
				r.Header.Set("Content-Type", "application/json")
			}
			if needsBearer(op) {
				r.Header.Set("Authorization", "Bearer "+sessions.token)
			}
			if req.method != http.MethodGet {
				r.Header.Set("Idempotency-Key", uuid.NewString())
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			body := rec.Body.String()
			stub := rec.Code == http.StatusNotImplemented || strings.Contains(body, `"code":"`+httpx.CodeNotImplemented+`"`)
			switch {
			case slices.Contains(pendingOps, id):
				if rec.Code != http.StatusNotImplemented || !strings.Contains(body, `"code":"`+httpx.CodeNotImplemented+`"`) ||
					!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
					t.Errorf("%s: в pendingOps, а ответ %d %s — реализована? убрать из pendingOps", id, rec.Code, body)
				}
			case stub:
				t.Errorf("%s: заглушка вне pendingOps — %d %s", id, rec.Code, body)
			case rec.Code == http.StatusUnauthorized:
				t.Errorf("%s: не дошёл до хендлера — %d %s", id, rec.Code, body)
			}
		}
	}
	if n != len(opsBase) {
		t.Errorf("проверено %d операций, строк opsBase %d — лишние строки без операции", n, len(opsBase))
	}
}

// Срок временного списка (R2): (а) каждое имя pendingOps есть в контракте; (б) у операции тег identity;
// (в) список ⊆ pendingOpsAtFreeze. Пока списки пусты, проверка не вакуумна за счёт пробы на подставных списках.
func TestPendingOpsExpire(t *testing.T) {
	t.Parallel()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range pendingViolations(spec, pendingOps, pendingOpsAtFreeze) {
		t.Error(v)
	}
	for _, id := range pendingOpsAtFreeze {
		if !slices.Contains(opsIDs(spec), id) {
			t.Errorf("pendingOpsAtFreeze: %s — нет в контракте", id)
		}
	}
	// проба: операции нет в контракте, чужой тег, вне замороженного множества — каждое ловится
	got := pendingViolations(spec, []string{"getNothing", "listCities"}, []string{"listCities"})
	want := []string{
		"pendingOps: getNothing — нет в pendingOpsAtFreeze",
		"pendingOps: getNothing — нет в контракте",
		"pendingOps: listCities — тег [geo], а заглушки бывают только у identity",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("проба стража:\n%v\nждали:\n%v", got, want)
	}
}

// pendingViolations — нарушения срока списка заглушек по алфавиту.
func pendingViolations(spec *openapi3.T, pending, frozen []string) []string {
	tags := map[string][]string{}
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			tags[opID(op)] = op.Tags
		}
	}
	var out []string
	for _, id := range pending {
		tag, ok := tags[id]
		switch {
		case !ok:
			out = append(out, "pendingOps: "+id+" — нет в контракте")
		case !slices.Equal(tag, []string{"identity"}):
			out = append(out, "pendingOps: "+id+" — тег ["+strings.Join(tag, ",")+"], а заглушки бывают только у identity")
		}
		if !slices.Contains(frozen, id) {
			out = append(out, "pendingOps: "+id+" — нет в pendingOpsAtFreeze")
		}
	}
	slices.Sort(out)
	return out
}

// opsIDs — operationId всех операций контракта.
func opsIDs(spec *openapi3.T) []string {
	var out []string
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			out = append(out, opID(op))
		}
	}
	return out
}

// needsBearer — операция требует или принимает access-токен (в security есть bearer).
func needsBearer(op *openapi3.Operation) bool {
	if op.Security == nil {
		return true // корневая security — bearer
	}
	for _, req := range *op.Security {
		if _, ok := req["bearer"]; ok {
			return true
		}
	}
	return false
}

// opID — operationId контракта (oapi-codegen вшивает его с заглавной буквы — имя метода; таблицы теста — как в
// контракте, lowerCamel).
func opID(op *openapi3.Operation) string {
	id := op.OperationID
	if id == "" {
		return id
	}
	return strings.ToLower(id[:1]) + id[1:]
}
