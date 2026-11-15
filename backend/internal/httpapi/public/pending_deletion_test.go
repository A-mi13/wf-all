package public_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity"
)

// pendingDeletion — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 15 плана I-2.
var pendingDeletion = []string{}

// deleteAccount на настоящем загрузчике сессий: без свежего входа — 403, ник не тот — 422, успех — 200 {purge_at}
// (private, no-store); отозванная сессия доходит до запросов по истечении кэша загрузчика (5 с) — 401.
func TestDeleteAccountHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	e.seedLogin(t, "del_http", "Угловой на последней минуте")
	res := e.login(t, "del_http", "Угловой на последней минуте")
	tok := res.Tokens.AccessToken
	if _, err := e.pool.Exec(context.Background(), `UPDATE sessions SET reauth_at = now() - interval '11 minutes' WHERE id = $1`,
		res.Tokens.SessionId); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodPost, "/v1/me/delete", `{"confirm_nickname":"del_http"}`, tok, true); rec.Code != http.StatusForbidden ||
		problemCodeOf(t, rec) != identity.CodeReauthRequired {
		t.Fatalf("без свежего входа: %d %s", rec.Code, rec.Body)
	}
	tok = e.login(t, "del_http", "Угловой на последней минуте").Tokens.AccessToken
	if rec := e.do(t, http.MethodPost, "/v1/me/delete", `{"confirm_nickname":"другой"}`, tok, true); rec.Code != http.StatusUnprocessableEntity ||
		problemCodeOf(t, rec) != identity.CodeConfirmMismatch {
		t.Fatalf("confirm_mismatch: %d %s", rec.Code, rec.Body)
	}
	rec := e.do(t, http.MethodPost, "/v1/me/delete", `{"confirm_nickname":"DEL_HTTP"}`, tok, true)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" ||
		!strings.Contains(rec.Body.String(), `"purge_at":"`) {
		t.Fatalf("deleteAccount: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	e.cache.Advance(5 * time.Second)
	if rec := e.do(t, http.MethodGet, "/v1/me", "", tok, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("после удаления: %d", rec.Code)
	}
}

// cancelDeletion — анонимно: вход в период отвечает deletion_pending с restore_token; отмена — 200 authenticated
// (no-store, documents_acceptance_required из транзакции отмены); повтор и чужой токен — 410.
func TestCancelDeletionHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	e.seedLogin(t, "cancel_http", "Жёлтая карточка на 90-й")
	tok := e.login(t, "cancel_http", "Жёлтая карточка на 90-й").Tokens.AccessToken
	if rec := e.do(t, http.MethodPost, "/v1/me/delete", `{"confirm_nickname":"cancel_http"}`, tok, true); rec.Code != http.StatusOK {
		t.Fatalf("deleteAccount: %d %s", rec.Code, rec.Body)
	}
	pending := e.login(t, "cancel_http", "Жёлтая карточка на 90-й")
	if pending.Status != oapi.AuthStatusDeletionPending || pending.Deletion == nil || pending.Tokens != nil {
		t.Fatalf("вход в период: %+v", pending)
	}
	body := `{"restore_token":"` + pending.Deletion.RestoreToken + `","device":` + deviceJSON + `}`
	rec := e.do(t, http.MethodPost, "/v1/auth/deletion/cancel", body, "", false)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cancelDeletion: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var res oapi.AuthResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != oapi.AuthStatusAuthenticated || res.Tokens == nil || res.User == nil || !res.User.DocumentsAcceptanceRequired {
		t.Fatalf("cancelDeletion: %s", rec.Body)
	}
	if rec := e.do(t, http.MethodGet, "/v1/me", "", res.Tokens.AccessToken, false); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"status":"active"`) {
		t.Fatalf("после отмены: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodPost, "/v1/auth/deletion/cancel", body, "", false); rec.Code != http.StatusGone ||
		problemCodeOf(t, rec) != identity.CodeRestoreTokenInvalid {
		t.Fatalf("повтор: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodPost, "/v1/auth/deletion/cancel", `{"restore_token":"UmVzdG9yZS10b2tlbi0yNTYtYml0cy1iYXNlNjR1cmwtYWJj"}`, "", false); rec.Code != http.StatusGone {
		t.Fatalf("чужой токен: %d %s", rec.Code, rec.Body)
	}
}
