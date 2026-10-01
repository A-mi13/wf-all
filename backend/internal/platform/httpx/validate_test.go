package httpx_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/httpx"
)

const thingsSpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      security: [{bearer: []}]
      parameters:
        - name: Idempotency-Key
          in: header
          required: true
          schema: {type: string, minLength: 16}
        - name: limit
          in: query
          schema: {type: integer, maximum: 100}
        - name: sort
          in: query
          schema: {type: string, default: name}
        - name: id
          in: query
          schema: {type: string, format: uuid}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name, kind]
              properties:
                name: {type: string, minLength: 2}
                kind: {type: string, enum: [a, b]}
                note: {type: string, default: x}
                contact: {type: string, format: email}
      responses:
        "204": {description: ok}
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
`

// validated — лимит тела и валидация по thingsSpec поверх next.
func validated(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromData([]byte(thingsSpec))
	if err != nil {
		t.Fatal(err)
	}
	validate, err := httpx.ValidateRequests(spec)
	if err != nil {
		t.Fatal(err)
	}
	return httpx.LimitBody(64)(validate(next))
}

// handler — валидация поверх хендлера, который запоминает тело и отвечает 204.
func handler(t *testing.T) (http.Handler, *string) {
	t.Helper()
	seen := new(string)
	return validated(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*seen = string(b)
		w.WriteHeader(http.StatusNoContent)
	})), seen
}

func TestValidateRequests(t *testing.T) {
	const okBody = `{"name":"ab","kind":"a"}`
	cases := []struct {
		name, body, query, contentType string
		noKey                          bool
		status                         int
		code                           string
		errs                           []httpx.FieldError
	}{
		{name: "валидный запрос доходит до хендлера", body: okBody, status: 204},
		{name: "короткое имя", body: `{"name":"a","kind":"a"}`, status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "body.name", Code: "minLength"}}},
		{name: "нет обязательного свойства", body: `{"name":"ab"}`, status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "body.kind", Code: "required"}}},
		{name: "два нарушения сразу — по порядку полей", body: `{"name":"a","kind":"z"}`, status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "body.kind", Code: "enum"}, {Field: "body.name", Code: "minLength"}}},
		// kin-openapi проверяет параметры раньше тела — порядок полей задаёт сортировка, не валидатор
		{name: "нарушения в query и теле — по порядку полей", body: `{"name":"a","kind":"a"}`, query: "limit=500", status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "body.name", Code: "minLength"}, {Field: "query.limit", Code: "maximum"}}},
		{name: "нет обязательного заголовка", body: okBody, noKey: true, status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "header.Idempotency-Key", Code: "required"}}},
		{name: "query больше максимума", body: okBody, query: "limit=500", status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "query.limit", Code: "maximum"}}},
		// у операции security: [bearer], а Authorization нет — аутентификация отдельный слой
		{name: "без Authorization запрос доходит до хендлера", body: okBody, status: 204},
		{name: "query не того типа", body: okBody, query: "limit=abc", status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "query.limit", Code: "type"}}},
		{name: "пустой query", body: okBody, query: "limit=", status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "query.limit", Code: "empty"}}},
		{name: "тип в query не стирает нарушения тела", body: `{"name":"a","kind":"a"}`, query: "limit=abc", status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "body.name", Code: "minLength"}, {Field: "query.limit", Code: "type"}}},
		{name: "uuid не по формату", body: okBody, query: "id=abc", status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "query.id", Code: "format"}}},
		{name: "валидный uuid доходит до хендлера", body: okBody, query: "id=00000000-0000-7000-8000-000000000000", status: 204},
		{name: "email не по формату", body: `{"name":"ab","kind":"a","contact":"nope"}`, status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "body.contact", Code: "format"}}},
		{name: "валидный email доходит до хендлера", body: `{"name":"ab","kind":"a","contact":"a@b.c"}`, status: 204},
		{name: "битый JSON", body: `{`, status: 400, code: "request.invalid"},
		{name: "неверный Content-Type", body: okBody, contentType: "text/plain", status: 400, code: "request.invalid"},
		{name: "тело больше лимита", body: `{"name":"` + strings.Repeat("a", 80) + `","kind":"a"}`, status: 413, code: "request.too_large"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, seen := handler(t)
			url := "/things"
			if c.query != "" {
				url += "?" + c.query
			}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(c.body))
			ct := c.contentType
			if ct == "" {
				ct = "application/json"
			}
			req.Header.Set("Content-Type", ct)
			if !c.noKey {
				req.Header.Set("Idempotency-Key", strings.Repeat("k", 16)) // без энтропии — gitleaks не примет за секрет
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, c.status, rec.Body.String())
			}
			if c.status == http.StatusNoContent {
				if *seen != c.body {
					t.Fatalf("хендлер получил тело %q, want %q", *seen, c.body)
				}
				return
			}
			var p httpx.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatalf("тело не JSON: %s", rec.Body.String())
			}
			if p.Code != c.code || !reflect.DeepEqual(p.Errors, c.errs) {
				t.Fatalf("code = %q errors = %+v, want %q %+v", p.Code, p.Errors, c.code, c.errs)
			}
			if p.Detail != "" {
				t.Fatalf("detail раскрывает внутренности валидатора: %q", p.Detail)
			}
		})
	}
}

func TestValidateRequestsPassesUnknownRoute(t *testing.T) {
	h, _ := handler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("маршрут не из контракта должен идти дальше, status = %d", rec.Code)
	}
}

// Валидатор только проверяет: default из контракта он в запрос не подставляет — хендлер
// получает тело, query и заголовки клиента как есть.
func TestValidateRequestsDoesNotRewriteRequest(t *testing.T) {
	const body = `{"name":"ab","kind":"a"}`
	var gotBody, gotQuery string
	var gotLen int64
	h := validated(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotQuery, gotLen = string(b), r.URL.RawQuery, r.ContentLength
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/things", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", strings.Repeat("k", 16))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotBody != body || gotQuery != "" || gotLen != int64(len(body)) {
		t.Fatalf("запрос переписан: body = %q query = %q content-length = %d", gotBody, gotQuery, gotLen)
	}
}

// Запрос без тела (Body == nil — так собирают запросы руками и в тестах): LimitBody его не
// оборачивает, валидатор отвечает про обязательное тело, а не паникой.
func TestValidateRequestsNilBody(t *testing.T) {
	h, _ := handler(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/things", nil)
	req.Body = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", strings.Repeat("k", 16))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("тело не JSON: %s", rec.Body.String())
	}
	want := []httpx.FieldError{{Field: "body", Code: "required"}}
	if rec.Code != http.StatusBadRequest || p.Code != "validation.failed" || !reflect.DeepEqual(p.Errors, want) {
		t.Fatalf("status = %d code = %q errors = %+v", rec.Code, p.Code, p.Errors)
	}
}
