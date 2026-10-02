package ratelimit_test

import (
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/ratelimit"
)

func TestDefaultRulesValid(t *testing.T) {
	r := ratelimit.DefaultRules()
	for _, class := range []string{"default", "auth"} {
		cp, ok := r[class]
		if !ok {
			t.Fatalf("нет класса %s", class)
		}
		for _, p := range []ratelimit.Policy{cp.IP, cp.User, cp.Device} {
			if err := p.Validate(); err != nil {
				t.Fatalf("%s: %v", class, err)
			}
		}
	}
	if r["default"].IP.Limit == 0 {
		t.Fatal("default без лимита по IP")
	}
}

func TestParseRules(t *testing.T) {
	got, err := ratelimit.ParseRules("auth.ip=10/1m:5, default.device=off, upload.user=30/1h", ratelimit.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	if got["auth"].IP != (ratelimit.Policy{Limit: 10, Period: time.Minute, Burst: 5}) {
		t.Fatalf("auth.ip = %+v", got["auth"].IP)
	}
	if got["default"].Device != (ratelimit.Policy{}) {
		t.Fatalf("off не выключил: %+v", got["default"].Device)
	}
	if got["upload"].User != (ratelimit.Policy{Limit: 30, Period: time.Hour}) {
		t.Fatalf("новый класс: %+v", got["upload"])
	}
	if got["default"].IP != ratelimit.DefaultRules()["default"].IP {
		t.Fatal("незатронутое правило изменилось")
	}
	// база не меняется
	if ratelimit.DefaultRules()["auth"].IP.Limit == 10 {
		t.Fatal("ParseRules изменил базу")
	}
	if got, _ := ratelimit.ParseRules("", ratelimit.DefaultRules()); len(got) != len(ratelimit.DefaultRules()) {
		t.Fatal("пустая строка — должны остаться умолчания")
	}
	for _, bad := range []string{"auth.ip", "auth.cookie=1/1m", "auth.ip=x/1m", "auth.ip=1/xx", "auth.ip=0/1m", "Auth.ip=1/1m", "auth.ip=1/1m:-2"} {
		if _, err := ratelimit.ParseRules(bad, ratelimit.DefaultRules()); err == nil {
			t.Errorf("%q принято", bad)
		}
	}
}

// Rules.Validate — проверка на старте api: все политики всех классов и обязательный default.
func TestRulesValidate(t *testing.T) {
	if err := ratelimit.DefaultRules().Validate(); err != nil {
		t.Fatalf("умолчания отвергнуты: %v", err)
	}
	noDefault := ratelimit.DefaultRules()
	delete(noDefault, ratelimit.DefaultClass)
	if noDefault.Validate() == nil {
		t.Fatal("правила без default приняты")
	}
	for _, cp := range []ratelimit.ClassPolicy{
		{IP: ratelimit.Policy{Limit: -1, Period: time.Second}},
		{User: ratelimit.Policy{Limit: 1}},
		{Device: ratelimit.Policy{Limit: 1, Period: time.Second, Burst: -1}},
	} {
		r := ratelimit.DefaultRules()
		r["upload"] = cp
		if r.Validate() == nil {
			t.Fatalf("принята невалидная политика %+v", cp)
		}
	}
}

// База — переданная карта, а не свежий DefaultRules: ParseRules её не трогает.
func TestParseRulesKeepsBase(t *testing.T) {
	base := ratelimit.DefaultRules()
	if _, err := ratelimit.ParseRules("auth.ip=10/1m", base); err != nil {
		t.Fatal(err)
	}
	if base["auth"].IP != ratelimit.DefaultRules()["auth"].IP {
		t.Fatalf("база изменилась: %+v", base["auth"].IP)
	}
}

// Страж: класс x-rate-limit в контракте без политики — ошибка конфигурации (проверяет тест
// хендлеров на настоящих контрактах, Task 19).
func TestUnknownClasses(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /a:
    get: {operationId: a, x-rate-limit: auth, responses: {"200": {description: ok}}}
  /b:
    get: {operationId: b, x-rate-limit: nope, responses: {"200": {description: ok}}}
  /c:
    get: {operationId: c, responses: {"200": {description: ok}}}
`))
	if err != nil {
		t.Fatal(err)
	}
	got := ratelimit.UnknownClasses(spec, ratelimit.DefaultRules())
	if len(got) != 1 || got[0] != "GET /b: x-rate-limit nope" {
		t.Fatalf("got %v", got)
	}
}
