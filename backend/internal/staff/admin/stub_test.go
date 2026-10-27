package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"wf/backend/internal/platform/httpx"
	staffadmin "wf/backend/internal/staff/admin"
)

// operations — методы-операции strict-интерфейса у *Handler: (ctx, oapi.…RequestObject) (ответ, error);
// служебные методы той же формы (Label) — не операции.
func operations() []reflect.Method {
	t := reflect.TypeFor[*staffadmin.Handler]()
	var out []reflect.Method
	for i := range t.NumMethod() {
		if m := t.Method(i); staffadmin.IsOperation(m.Type) {
			out = append(out, m)
		}
	}
	return out
}

// Нулевой Handler (&staffadmin.Handler{} — тесты конвейера; cmd/admin-api собирает New): все 27 операций тега
// staff отвечают 501 http.not_implemented через Middleware — одно место, без вызова метода и без
// паники на несобранных сценариях. Операции других тегов Middleware не трогает.
func TestStubsNotImplemented(t *testing.T) {
	mw := (&staffadmin.Handler{}).Middleware()
	ops := operations()
	if len(ops) != 27 {
		t.Fatalf("операций staff %d, ждали 27", len(ops))
	}
	for _, m := range ops {
		called := false
		f := func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
			called = true
			return nil, nil
		}
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		resp, err := mw(f, m.Name)(r.Context(), httptest.NewRecorder(), r, nil)
		pe, ok := errors.AsType[httpx.ProblemError](err)
		if called || resp != nil || !ok || pe.Problem().Status != http.StatusNotImplemented || pe.Problem().Code != httpx.CodeNotImplemented {
			t.Errorf("%s: вызван %v, ответ %v, ошибка %v — ждали 501 http.not_implemented", m.Name, called, resp, err)
		}
	}
	called := false
	other := func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		called = true
		return "geo", nil
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	if resp, err := mw(other, "ListGeoCities")(r.Context(), httptest.NewRecorder(), r, nil); !called || resp != "geo" || err != nil {
		t.Fatalf("чужая операция: вызвана %v, %v, %v", called, resp, err)
	}
}
