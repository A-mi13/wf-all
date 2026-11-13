package public_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/idempotency"
)

// Маршрут public несёт x-idempotency операции (план I-1, R6, §18 спеки identity): idempotency.Middleware узнаёт
// «не сохранять ответ с секретом» по тому же признаку маршрута. Проверка маршрута, не конвейера: реализованной
// мутации с секретом в public пока нет (сквозной повтор → 409 replay_unavailable — этап 5 спеки).
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
