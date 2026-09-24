package public_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/platform/testkit/apitest"
)

func TestHealthMatchesContract(t *testing.T) {
	v := apitest.New(t, public.GetSpec)
	rec := v.Do(t, public.NewHandler(slog.New(slog.DiscardHandler)),
		httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
