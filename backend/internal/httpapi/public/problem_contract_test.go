package public_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
)

// adminBundle — бандл контракта admin-api. Из файла, а не из httpapi/admin/oapi: публичный пакет не
// зависит от админки даже в тестах (depguard public-api-no-admin); бандл и встроенная спека сверяет gen:check.
const adminBundle = "../../../../contracts/openapi/admin.yaml"

// Хвост m5 плана I-1: JSON-ключи httpx.Problem равны свойствам components.schemas.Problem обоих корней
// контракта — поле, которого нет в схеме, валидатор ответов и клиенты не увидят; свойство без поля
// сервер никогда не пришлёт.
func TestProblemMatchesContract(t *testing.T) {
	t.Parallel()
	want := problemJSONKeys()

	pub, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	adm, err := openapi3.NewLoader().LoadFromFile(adminBundle)
	if err != nil {
		t.Fatal(err)
	}
	for name, spec := range map[string]*openapi3.T{"public": pub, "admin": adm} {
		ref := spec.Components.Schemas["Problem"]
		if ref == nil || ref.Value == nil {
			t.Fatalf("%s: нет components.schemas.Problem", name)
		}
		got := make([]string, 0, len(ref.Value.Properties))
		for k := range ref.Value.Properties {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s: свойства Problem %v, JSON-ключи httpx.Problem %v", name, got, want)
		}
	}
}

// problemJSONKeys — имена JSON-полей httpx.Problem по тегам (json:"-" — не поле тела).
func problemJSONKeys() []string {
	var keys []string
	rt := reflect.TypeFor[httpx.Problem]()
	for f := range rt.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		keys = append(keys, name)
	}
	slices.Sort(keys)
	return keys
}
