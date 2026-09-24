package admin_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/testkit/apitest"
)

func TestHealthMatchesContract(t *testing.T) {
	v := apitest.New(t, admin.GetSpec)
	rec := v.Do(t, admin.NewHandler(slog.New(slog.DiscardHandler)),
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
