package apitest

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sort"

	"wf/backend/internal/platform/httpx"
)

var ctxType = reflect.TypeFor[context.Context]()

// NotImplemented — проверка заглушек strict-хендлера: каждый метод вида (ctx, запрос) (ответ,
// error) вызывается с нулевым запросом и обязан вернуть 501 http.not_implemented без ответа.
// checked — сколько методов-операций вызвано (0 — проверять было нечего), bad — имена нарушивших,
// по алфавиту. Остальные методы (не операции) пропускаются.
func NotImplemented(h any) (checked int, bad []string) {
	v := reflect.ValueOf(h)
	t := v.Type()
	for i := range t.NumMethod() {
		m := t.Method(i)
		ft := m.Type // с получателем: (h, ctx, запрос)
		if ft.NumIn() != 3 || ft.In(1) != ctxType || ft.NumOut() != 2 ||
			ft.Out(1) != reflect.TypeFor[error]() {
			continue
		}
		checked++
		out := v.Method(i).Call([]reflect.Value{
			reflect.ValueOf(context.Background()), reflect.Zero(ft.In(2)),
		})
		err, _ := out[1].Interface().(error)
		pe, ok := errors.AsType[httpx.ProblemError](err)
		noAnswer := out[0].Kind() == reflect.Interface && out[0].IsNil()
		if !noAnswer || !ok || pe.Problem().Status != http.StatusNotImplemented ||
			pe.Problem().Code != httpx.CodeNotImplemented {
			bad = append(bad, m.Name)
		}
	}
	sort.Strings(bad)
	return checked, bad
}
