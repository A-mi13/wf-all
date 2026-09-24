// Package apitest прогоняет запрос через хендлер и сверяет запрос и ответ с вшитой спекой:
// сервер не может тихо разойтись с контрактом.
package apitest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

type Validator struct{ router routers.Router }

func New(t testing.TB, load func() (*openapi3.T, error)) *Validator {
	t.Helper()
	spec, err := load()
	if err != nil {
		t.Fatalf("спека: %v", err)
	}
	spec.Servers = nil // иначе FindRoute ищет хост и префикс из servers
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("спека невалидна: %v", err)
	}
	r, err := gorillamux.NewRouter(spec)
	if err != nil {
		t.Fatalf("роутер: %v", err)
	}
	return &Validator{router: r}
}

func (v *Validator) Do(t testing.TB, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	ctx := context.Background()
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	route, params, err := v.router.FindRoute(req)
	if err != nil {
		t.Fatalf("маршрута нет в контракте: %s %s: %v", req.Method, req.URL.Path, err)
	}
	in := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	if err := openapi3filter.ValidateRequest(ctx, in); err != nil {
		t.Fatalf("запрос нарушает контракт: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	}
	out.SetBodyBytes(rec.Body.Bytes())
	if err := openapi3filter.ValidateResponse(ctx, out); err != nil {
		t.Fatalf("ответ нарушает контракт: %v\nтело: %s", err, rec.Body.String())
	}
	return rec
}
