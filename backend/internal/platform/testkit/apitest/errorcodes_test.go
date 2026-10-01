package apitest_test

import (
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
