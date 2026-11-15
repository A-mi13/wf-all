package public_test

import (
	"net/http"
	"strings"
	"testing"

	"wf/backend/internal/identity"
)

// pendingAccount — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 14 плана I-2.
var pendingAccount = []string{}

// HTTP каждой операции области «мой аккаунт» на настоящих сценариях и настоящем загрузчике сессий: успех по
// контракту (Cache-Control: private, no-store) и коды ошибок.
func TestAccountHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	e.seedLogin(t, "http_user", "Свисток на 46-й минуте")
	tok := e.login(t, "http_user", "Свисток на 46-й минуте").Tokens.AccessToken

	rec := e.do(t, http.MethodGet, "/v1/me", "", tok, false)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" ||
		!strings.Contains(rec.Body.String(), `"documents_acceptance_required":true`) {
		t.Fatalf("getMe: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if rec = e.do(t, http.MethodGet, "/v1/me", "", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("getMe без входа: %d", rec.Code)
	}
	if rec = e.do(t, http.MethodPatch, "/v1/me", `{"locale":"en"}`, tok, true); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"locale":"en"`) {
		t.Fatalf("updateMe: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do(t, http.MethodPatch, "/v1/me/nickname", `{"nickname":"http_new"}`, tok, true); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"nickname":"http_new"`) || !strings.Contains(rec.Body.String(), `"nickname_changeable_at":"`) {
		t.Fatalf("changeNickname: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do(t, http.MethodPatch, "/v1/me/nickname", `{"nickname":"http_third"}`, tok, true); rec.Code != http.StatusConflict ||
		problemCodeOf(t, rec) != identity.CodeNicknameChangeTooSoon || !strings.Contains(rec.Body.String(), `"available_at"`) {
		t.Fatalf("changeNickname раньше срока: %d %s", rec.Code, rec.Body)
	}
	e.seedLogin(t, "http_other", "Пенальти на 90-й")
	other := e.login(t, "http_other", "Пенальти на 90-й").Tokens.AccessToken
	if rec = e.do(t, http.MethodPatch, "/v1/me/nickname", `{"nickname":"李小龍"}`, other, true); rec.Code != http.StatusUnprocessableEntity ||
		problemCodeOf(t, rec) != identity.CodeNicknameInvalid {
		t.Fatalf("nickname_invalid: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do(t, http.MethodPatch, "/v1/me/nickname", `{"nickname":"HTTP_NEW"}`, other, true); rec.Code != http.StatusConflict ||
		problemCodeOf(t, rec) != identity.CodeNicknameTaken {
		t.Fatalf("nickname_taken: %d %s", rec.Code, rec.Body)
	}
	docs := `{"accepted_documents":[{"document":"terms","version":"2026-11-01"},{"document":"privacy","version":"2026-11-01"}]}`
	if rec = e.do(t, http.MethodPost, "/v1/me/documents", docs, tok, true); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"documents_acceptance_required":false`) {
		t.Fatalf("acceptDocuments: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do(t, http.MethodPost, "/v1/me/documents", `{"accepted_documents":[{"document":"terms","version":"2026-01-01"}]}`, tok, true); rec.Code != http.StatusUnprocessableEntity ||
		problemCodeOf(t, rec) != identity.CodeDocumentsOutdated {
		t.Fatalf("documents_outdated: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do(t, http.MethodGet, "/v1/me/auth-methods", "", tok, false); rec.Code != http.StatusOK ||
		rec.Header().Get("Cache-Control") != "private, no-store" ||
		!strings.Contains(rec.Body.String(), `"identities":[]`) || !strings.Contains(rec.Body.String(), `"totp_enabled":false`) {
		t.Fatalf("listMyAuthMethods: %d %s", rec.Code, rec.Body)
	}
}
