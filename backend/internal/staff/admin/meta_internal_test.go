package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/staff"
)

// HTTP Minor 4 финального ревью: User-Agent, который Middleware кладёт в Meta сценариев staff (сессии,
// неизменяемый журнал), — только через httpx.UserAgent: заголовок 100 КБ → ровно MaxUserAgentBytes, начало
// сохранено. Тот же помощник у requestMeta операций подтверждений (TestDecisionHugeUserAgent в httpapi/admin).
func TestMetaUserAgentCapped(t *testing.T) {
	h := New(staff.Deps{}, Cookie{Name: "wf_admin_session"})
	huge := "Mozilla/5.0 " + strings.Repeat("x", 100<<10)
	r := httptest.NewRequest(http.MethodPost, "/v1/staff/auth/login", nil)
	r.Header.Set("User-Agent", huge)
	var got string
	f := h.Middleware()(func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
		got = meta(ctx).UserAgent
		return nil, nil
	}, "StaffLogin")
	if _, err := f(context.Background(), httptest.NewRecorder(), r, nil); err != nil {
		t.Fatal(err)
	}
	if len(got) != httpx.MaxUserAgentBytes || !strings.HasPrefix(got, "Mozilla/5.0 xxx") {
		t.Fatalf("User-Agent в Meta: %d байт (ждали %d)", len(got), httpx.MaxUserAgentBytes)
	}
}
