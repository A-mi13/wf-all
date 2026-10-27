package apitest_test

import (
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/testkit/apitest"
)

func specWithCommon(t *testing.T, common string) *openapi3.T {
	t.Helper()
	src := `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{}` + common + `}`
	spec, err := openapi3.NewLoader().LoadFromData([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// Подсадка багов: код платформы без записи в контракте и устаревший код контракта.
func TestCommonCodeViolations(t *testing.T) {
	spec := specWithCommon(t, `,"x-error-codes-common":["internal","request.invalid"]`)
	cases := []struct {
		name     string
		platform []string
		want     int
	}{
		{"совпадают в любом порядке", []string{"request.invalid", "internal"}, 0},
		{"код платформы не задокументирован", []string{"internal", "request.invalid", "http.not_found"}, 1},
		{"в контракте код, который платформа не выдаёт", []string{"internal"}, 1},
		{"оба направления", []string{"internal", "http.not_found"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := apitest.CommonCodeViolations(spec, c.platform)
			if err != nil {
				t.Fatal(err)
			}
			if len(v) != c.want {
				t.Fatalf("нарушения %v, ожидалось %d", v, c.want)
			}
		})
	}
}

func TestCommonCodeViolationsWithoutExtension(t *testing.T) {
	for _, common := range []string{``, `,"x-error-codes-common":"internal"`} {
		if _, err := apitest.CommonCodeViolations(specWithCommon(t, common), []string{"internal"}); err == nil {
			t.Errorf("%q: ошибки нет", common)
		}
	}
}

func specWithPaths(t *testing.T, paths string) *openapi3.T {
	t.Helper()
	src := `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":` + paths + `}`
	spec, err := openapi3.NewLoader().LoadFromData([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// x-error-codes операций по operationId: пустой список — пустой, не nil; у всех методов пути.
func TestOperationCodes(t *testing.T) {
	spec := specWithPaths(t, `{
		"/v1/a": {
			"get": {"operationId": "getA", "x-error-codes": ["a.not_found"], "responses": {"200": {"description": "ok"}}},
			"post": {"operationId": "createA", "x-error-codes": [], "responses": {"200": {"description": "ok"}}}
		},
		"/v1/b": {"delete": {"operationId": "deleteB", "x-error-codes": ["b.gone", "b.locked"], "responses": {"200": {"description": "ok"}}}}
	}`)
	got, err := apitest.OperationCodes(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"getA": {"a.not_found"}, "createA": {}, "deleteB": {"b.gone", "b.locked"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OperationCodes = %#v, want %#v", got, want)
	}
}

// Операция без x-error-codes или со списком не из строк — ошибка, а не пустой список: иначе
// сверка кодов прошла бы вхолостую.
func TestOperationCodesMalformed(t *testing.T) {
	for _, op := range []string{
		`{"operationId": "getA", "responses": {"200": {"description": "ok"}}}`,
		`{"operationId": "getA", "x-error-codes": "a.not_found", "responses": {"200": {"description": "ok"}}}`,
	} {
		if _, err := apitest.OperationCodes(specWithPaths(t, `{"/v1/a": {"get": `+op+`}}`)); err == nil {
			t.Errorf("%s: ошибки нет", op)
		}
	}
}
