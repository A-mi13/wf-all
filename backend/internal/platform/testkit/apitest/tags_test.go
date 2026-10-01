package apitest_test

import (
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/testkit/apitest"
)

type fakeTeams struct{}

func (fakeTeams) GetTeam() {}

type fakeServer struct{ fakeTeams }

func (fakeServer) GetHealth() {}

func TestOwners(t *testing.T) {
	got := apitest.Owners(reflect.TypeOf(fakeServer{}), func(string) string { return "teams" })
	want := map[string]string{"GetTeam": "teams", "GetHealth": "platform"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTagViolations(t *testing.T) {
	owners := map[string]string{"GetTeam": "teams", "GetHealth": "platform"}
	ok := []apitest.Operation{{Method: "GetTeam", Tag: "teams"}, {Method: "GetHealth", Tag: "platform"}}
	if v := apitest.TagViolations(ok, owners); len(v) != 0 {
		t.Fatalf("лишние нарушения: %v", v)
	}
	// подсадка: тег не совпадает с реализатором; операция без реализации
	bad := []apitest.Operation{{Method: "GetTeam", Tag: "matches"}, {Method: "GetMissing", Tag: "teams"}}
	if v := apitest.TagViolations(bad, owners); len(v) != 2 {
		t.Fatalf("ожидалось 2 нарушения, есть %v", v)
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
