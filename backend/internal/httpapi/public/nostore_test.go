package public_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/idempotency"
)

// Маршрут public несёт x-idempotency операции (план I-1, R6, §18 спеки identity): idempotency.Middleware узнаёт
// «не сохранять ответ с секретом» по тому же признаку маршрута. Проверка маршрута, не конвейера: реализованной
// мутации — сквозной повтор ниже (TestPublicNoStoreReplay).
func TestPublicRouteNoStore(t *testing.T) {
	t.Parallel()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path, want string
	}{
		{http.MethodPost, "/v1/me/mfa/totp/setup", idempotency.NoStore},
		{http.MethodPost, "/v1/me/mfa/totp/confirm", idempotency.NoStore},
		{http.MethodPost, "/v1/me/mfa/recovery-codes", idempotency.NoStore},
		{http.MethodPost, "/v1/me/passkeys", ""}, // мутация без секрета — ответ сохраняется
	} {
		var got string
		var seen bool
		h := routes(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			rt, ok := httpx.RouteFrom(r.Context())
			if ok {
				seen = true
				got = rt.Extension(idempotency.Extension)
			}
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), c.method, c.path, nil))
		if !seen {
			t.Errorf("%s %s: маршрут контракта не найден", c.method, c.path)
			continue
		}
		if got != c.want {
			t.Errorf("%s %s: %s = %q, ждали %q", c.method, c.path, idempotency.Extension, got, c.want)
		}
	}
}

// m15 плана I-1, Task 16 плана I-2: setupTotp через весь конвейер с Idempotency-Key — 200 no-store с секретом; повтор с
// тем же ключом — 409 idempotency.replay_unavailable (не тот же секрет из хранилища); в idempotency_keys — только хэш и
// статус, тела ответа нет.
func TestPublicNoStoreReplay(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	const pw = "Сухой лист в девятку 2026" //nolint:gosec // G101: пароль теста, не учётные данные
	e.seedLogin(t, "http_nostore", pw)
	access := e.login(t, "http_nostore", pw).Tokens.AccessToken
	key := uuid.NewString()
	setup := func() *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/me/mfa/totp/setup", nil)
		r.Header.Set("Authorization", "Bearer "+access)
		r.Header.Set("Idempotency-Key", key)
		return e.v.Do(t, e.h, r)
	}
	first := setup()
	if first.Code != http.StatusOK || first.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(first.Body.String(), `"secret"`) {
		t.Fatalf("первый: %d %v %s", first.Code, first.Header(), first.Body.String())
	}
	again := setup()
	if again.Code != http.StatusConflict || problemCodeOf(t, again) != httpx.CodeIdempotencyReplayUnavailable {
		t.Fatalf("повтор: %d %s", again.Code, again.Body.String())
	}
	var stored int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys WHERE key = $1 AND response_body IS NULL`,
		key).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("ключ без тела: %d %v", stored, err)
	}
}
