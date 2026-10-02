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
      x-limit: 5
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

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/things/42", nil))
	if !found || got.Operation().OperationID != "getThing" {
		t.Fatalf("маршрут: found=%v %+v", found, got)
	}
	// нет расширения и нестроковое (x-limit: 5) — ""
	if got.Extension("x-rate-limit") != "auth" || got.Extension("x-nope") != "" || got.Extension("x-limit") != "" {
		t.Fatalf("расширения: %q %q %q", got.Extension("x-rate-limit"), got.Extension("x-nope"), got.Extension("x-limit"))
	}

	// маршрут не из контракта и чужой метод — дальше без Route: ответит роутер (404/405)
	for _, req := range []*http.Request{
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/docs", nil),
		httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/things/42", nil),
	} {
		found = true
		h.ServeHTTP(httptest.NewRecorder(), req)
		if found {
			t.Fatalf("%s %s: Route в ctx", req.Method, req.URL.Path)
		}
	}
}

// securedSpec — спека, где часть операций требует bearer. Имя без «bearer»: иначе gosec G101 принимает константу за секрет.
const securedSpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
security:
  - bearer: []
components:
  securitySchemes:
    bearer: {type: http, scheme: Bearer}
    key: {type: apiKey, in: header, name: X-Key}
paths:
  /inherited:
    get: {responses: {"200": {description: ok}}}
  /anonymous:
    get: {security: [], responses: {"200": {description: ok}}}
  /empty-requirement:
    get: {security: [{}], responses: {"200": {description: ok}}}
  /optional:
    get: {security: [{}, {bearer: []}], responses: {"200": {description: ok}}}
  /other-scheme:
    get: {security: [{key: []}], responses: {"200": {description: ok}}}
`

const noSecuritySpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /plain:
    get: {responses: {"200": {description: ok}}}
`

// Принимает ли операция bearer: security операции, а без неё — общий документа. Анонимная
// (security: [] или только пустое требование) и операция с другой схемой — нет.
func TestRouteAcceptsBearer(t *testing.T) {
	cases := []struct {
		spec, path string
		want       bool
	}{
		{securedSpec, "/inherited", true},
		{securedSpec, "/anonymous", false},
		{securedSpec, "/empty-requirement", false},
		{securedSpec, "/optional", true},
		{securedSpec, "/other-scheme", false},
		{noSecuritySpec, "/plain", false},
	}
	for _, c := range cases {
		spec, err := openapi3.NewLoader().LoadFromData([]byte(c.spec))
		if err != nil {
			t.Fatal(err)
		}
		routes, err := httpx.Routes(spec)
		if err != nil {
			t.Fatal(err)
		}
		var got *httpx.Route
		routes(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got, _ = httpx.RouteFrom(r.Context())
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, c.path, nil))
		if got == nil {
			t.Fatalf("%s: нет маршрута", c.path)
		}
		if got.AcceptsBearer() != c.want {
			t.Errorf("%s: AcceptsBearer = %v, ждали %v", c.path, !c.want, c.want)
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
