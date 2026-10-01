package apitest_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/testkit/apitest"
)

type fakeTeams struct{}

func (fakeTeams) GetTeam() {}

type fakeServer struct{ fakeTeams }

func (fakeServer) GetHealth() {}

// Сервер объявил метод, который есть и у встроенного хендлера: Go молча берёт метод
// сервера (он мельче), так что операцию обслуживает платформа, а не модуль.
type shadowServer struct{ fakeTeams }

func (shadowServer) GetTeam() {}

// То же с указательным получателем: в методах значения сервера GetTeam нет вовсе.
type shadowPtrServer struct{ fakeTeams }

func (*shadowPtrServer) GetTeam() {}

// Два модуля, второй встроен указателем: *sync.Mutex — тип из другого пакета,
// чтобы moduleOf получил два разных пути пакета.
type twoModulesServer struct {
	fakeTeams
	*sync.Mutex
}

// moduleOf тестов: пакет sync — «модуль» matches, пакет тестов — teams.
func fakeModuleOf(pkgPath string) string {
	if pkgPath == "sync" {
		return "matches"
	}
	return "teams"
}

func TestOwners(t *testing.T) {
	cases := []struct {
		name   string
		server reflect.Type
		want   map[string]string
	}{
		{"встроенный модуль и метод платформы", reflect.TypeFor[fakeServer](),
			map[string]string{"GetTeam": "teams", "GetHealth": "platform"}},
		{"метод сервера затеняет метод модуля", reflect.TypeFor[shadowServer](),
			map[string]string{"GetTeam": "platform"}},
		{"затенение указательным получателем", reflect.TypeFor[shadowPtrServer](),
			map[string]string{"GetTeam": "platform"}},
		{"два модуля, один встроен указателем", reflect.TypeFor[twoModulesServer](),
			map[string]string{"GetTeam": "teams", "Lock": "matches", "TryLock": "matches", "Unlock": "matches"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := apitest.Owners(c.server, fakeModuleOf); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

// Встроенный тип вне модулей (moduleOf вернул "") не становится «платформой».
func TestOwnersKeepsUnknownModule(t *testing.T) {
	owners := apitest.Owners(reflect.TypeFor[fakeServer](), func(string) string { return "" })
	if owner, ok := owners["GetTeam"]; !ok || owner != "" {
		t.Fatalf("GetTeam: owner = %q, ok = %v; ожидался пустой модуль", owner, ok)
	}
	v := apitest.TagViolations([]apitest.Operation{{Method: "GetTeam", Tag: "teams"}}, owners)
	if len(v) != 1 || !strings.Contains(v[0], "тип вне модулей") {
		t.Fatalf("ожидалось нарушение «тип вне модулей», есть %v", v)
	}
}

// Затенение доходит до нарушения: тег teams, а обслуживает платформа.
func TestShadowingIsViolation(t *testing.T) {
	owners := apitest.Owners(reflect.TypeFor[shadowServer](), fakeModuleOf)
	v := apitest.TagViolations([]apitest.Operation{{Method: "GetTeam", Tag: "teams"}}, owners)
	if len(v) != 1 || !strings.Contains(v[0], `реализует модуль "platform"`) {
		t.Fatalf("ожидалось нарушение «реализует модуль platform», есть %v", v)
	}
}

func TestTagViolations(t *testing.T) {
	owners := map[string]string{"GetTeam": "teams", "GetHealth": "platform"}
	ok := []apitest.Operation{{Method: "GetTeam", Tag: "teams"}, {Method: "GetHealth", Tag: "platform"}}
	if v := apitest.TagViolations(ok, owners); len(v) != 0 {
		t.Fatalf("лишние нарушения: %v", v)
	}
	// подсадка: тег не совпадает с реализатором; операция без реализации — у каждой свой текст
	bad := []apitest.Operation{{Method: "GetTeam", Tag: "matches"}, {Method: "GetMissing", Tag: "teams"}}
	want := []string{
		`GetTeam: тег "matches", а реализует модуль "teams"`,
		"GetMissing: сервер не реализует операцию",
	}
	if v := apitest.TagViolations(bad, owners); !reflect.DeepEqual(v, want) {
		t.Fatalf("нарушения %q, ожидались %q", v, want)
	}
}

func TestModuleOf(t *testing.T) {
	cases := map[string]string{
		"wf/backend/internal/teams/httpapi": "teams",
		"wf/backend/internal/teams/admin":   "teams",
		"github.com/x/y":                    "",
	}
	for in, want := range cases {
		if got := apitest.ModuleOf(in); got != want {
			t.Errorf("ModuleOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOperationsNeedsExactlyOneTag(t *testing.T) {
	load := func(tags string) *openapi3.T {
		t.Helper()
		doc, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /x:
    get:
      operationId: getX
      tags: ` + tags + `
      responses: {"200": {description: ok}}
`))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	ops, err := apitest.Operations(load("[teams]"))
	if err != nil || !reflect.DeepEqual(ops, []apitest.Operation{{Method: "GetX", Tag: "teams"}}) {
		t.Fatalf("ops = %v, err = %v", ops, err)
	}
	if _, err := apitest.Operations(load("[teams, matches]")); err == nil {
		t.Fatal("два тега не отклонены")
	}
	if _, err := apitest.Operations(load("[]")); err == nil {
		t.Fatal("операция без тега не отклонена")
	}
}
