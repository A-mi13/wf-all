package public_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
)

// pendingMfaReset — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 18 плана I-2.
var pendingMfaReset = []string{}

// cancelMyMfaReset через конвейер: сессия без второго фактора (вход паролем) при живом сбросе — 403
// identity.reauth_required, сброс жив; сессия, прошедшая второй фактор, — 204 (отменён владельцем), повтор — 204; без
// живого сброса и без второго фактора — 404 identity.mfa_reset_not_found.
func TestCancelMyMfaResetHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	ctx := context.Background()
	uid := e.seedLogin(t, "reset_http", "Штрафной с линии штрафной")
	tok := e.login(t, "reset_http", "Штрафной с линии штрафной").Tokens.AccessToken
	rid := uuid.New()
	// 403 — только при подтверждённой 2FA (без неё отменяет любая сессия): строка user_mfa после входа паролем
	if _, err := e.pool.Exec(ctx, `INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at)
		VALUES ($1, decode('00', 'hex'), 'testkey0000', now(), now())`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id,
		requested_by_label, reason, execute_at, created_at)
		VALUES ($1, $2, gen_random_uuid(), gen_random_uuid(), 'Ольга', 'потерян телефон', now() + interval '7 days', now())`,
		rid, uid); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodDelete, "/v1/me/mfa-reset", "", tok, true); rec.Code != http.StatusForbidden ||
		problemCodeOf(t, rec) != identity.CodeReauthRequired {
		t.Fatalf("без второго фактора: %d %s", rec.Code, rec.Body)
	}
	var live int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_at IS NULL`, rid).Scan(&live); err != nil ||
		live != 1 {
		t.Fatalf("сброс отменён без второго фактора: %d %v", live, err)
	}
	// второй фактор этой сессии (вход TOTP или свежий вход вторым фактором) — mfa_at
	if _, err := e.pool.Exec(ctx, `UPDATE sessions SET mfa_at = now() WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodDelete, "/v1/me/mfa-reset", "", tok, true); rec.Code != http.StatusNoContent {
		t.Fatalf("отмена: %d %s", rec.Code, rec.Body)
	}
	var by string
	if err := e.pool.QueryRow(ctx, `SELECT cancelled_by FROM mfa_resets WHERE id = $1`, rid).Scan(&by); err != nil ||
		by != "owner" {
		t.Fatalf("cancelled_by %q %v", by, err)
	}
	if rec := e.do(t, http.MethodDelete, "/v1/me/mfa-reset", "", tok, true); rec.Code != http.StatusNoContent {
		t.Fatalf("повтор: %d %s", rec.Code, rec.Body)
	}
	// новая сессия без второго фактора, живого сброса нет — 404 (2FA снята, чтобы вход паролем выдал токены)
	if _, err := e.pool.Exec(ctx, `DELETE FROM user_mfa WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	tok2 := e.login(t, "reset_http", "Штрафной с линии штрафной").Tokens.AccessToken
	if rec := e.do(t, http.MethodDelete, "/v1/me/mfa-reset", "", tok2, true); rec.Code != http.StatusNotFound ||
		problemCodeOf(t, rec) != identity.CodeMfaResetNotFound {
		t.Fatalf("без сброса: %d %s", rec.Code, rec.Body)
	}
}
