package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/httpx"
)

const routesSpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /things/{id}:
    get:
      operationId: getThing
      x-rate-limit: auth
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses: {"200": {description: ok}}
`

func TestRoutesPutsOperationInContext(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData([]byte(routesSpec))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(spec)
	if err != nil {
		t.Fatal(err)
	}
	var got *httpx.Route
	var found bool
	h := routes(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, found = httpx.RouteFrom(r.Context())
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/things/42", nil))
	if !found || got.Operation().OperationID != "getThing" {
		t.Fatalf("маршрут: found=%v %+v", found, got)
	}
	if got.Extension("x-rate-limit") != "auth" || got.Extension("x-nope") != "" {
		t.Fatalf("расширения: %q %q", got.Extension("x-rate-limit"), got.Extension("x-nope"))
	}

	// маршрут не из контракта и чужой метод — дальше без Route: ответит роутер (404/405)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/docs", nil),
		httptest.NewRequest(http.MethodDelete, "/things/42", nil),
	} {
		found = true
		h.ServeHTTP(httptest.NewRecorder(), req)
		if found {
			t.Fatalf("%s %s: Route в ctx", req.Method, req.URL.Path)
		}
	}
}

func TestTimeoutSetsDeadline(t *testing.T) {
	var left time.Duration
	var ok bool
	h := httpx.Timeout(3 * time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var dl time.Time
		dl, ok = r.Context().Deadline()
		left = time.Until(dl)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	if !ok || left <= 0 || left > 3*time.Second {
		t.Fatalf("крайний срок: ok=%v осталось %v", ok, left)
	}
}
