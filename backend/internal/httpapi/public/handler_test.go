package public_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/token"
)

// minVersionPath — анонимная операция класса default без базы (версии — подделка): на ней стоят
// стражи конвейера; раньше — /v1/health, удалён из контракта (спека geo §4.4).
const minVersionPath = "/v1/app/min-version?platform=ios"

// testOptions — рабочий набор зависимостей: лимиты пропускают всё, сессий нет, ключ токенов —
// новый на каждый тест, база — чистая (идемпотентность), geo и версии — подделки.
func testOptions(t *testing.T) public.Options {
	t.Helper()
	seed, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	k, err := token.ParseKeys([]string{seed})
	if err != nil {
		t.Fatal(err)
	}
	return public.Options{
		DB:          dbtest.NewPool(t),
		Tokens:      token.NewIssuer(k, clock.System),
		Sessions:    auth.NoSessions,
		Limiter:     ratelimit.Unlimited,
		RateRules:   ratelimit.DefaultRules(),
		Geo:         &fakeGeo{},
		AppVersions: &fakeVersions{},
	}
}

// Страж спеки §12.4: операцию с тегом <модуль> реализует хендлер этого модуля.
func TestOperationsImplementedByTaggedModule(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	ops, err := apitest.Operations(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("в контракте нет операций — страж проверяет вхолостую")
	}
	owners := apitest.Owners(reflect.TypeOf(public.Server{}), apitest.ModuleOf)
	for _, v := range apitest.TagViolations(ops, owners) {
		t.Error(v)
	}
}

// Валидатор пропускает маршрут не из контракта — 404 отвечает роутер в формате Problem.
func TestUnknownRouteIsProblemThroughValidator(t *testing.T) {
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/nope", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"http.not_found"`) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// Страж проводки: лимит тела и валидатор стоят в цепочке хендлера — тело больше
// httpx.MaxBodyBytes отвергается до strict-хендлера, даже у операции без тела.
func TestOversizedBodyIsRejected(t *testing.T) {
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.NewReader(strings.Repeat("a", httpx.MaxBodyBytes+1))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, minVersionPath, body))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"code":"request.too_large"`) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// Страж спеки §12.4 «коды ошибок задокументированы»: коды платформы (httpx.PlatformCodes)
// и x-error-codes-common контракта совпадают в обе стороны.
func TestPlatformCodesDocumented(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	v, err := apitest.CommonCodeViolations(spec, httpx.PlatformCodes)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range v {
		t.Error(msg)
	}
}

// Страж «коды модуля задокументированы» (.claude/rules/backend-http.md): geo.Codes и x-error-codes
// операций с тегом geo совпадают в обе стороны. Админские операции geo (этап 2) добавят свой
// контракт в сверку.
func TestGeoCodesDocumented(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			if !slices.Equal(op.Tags, []string{"geo"}) {
				continue
			}
			raw, ok := op.Extensions["x-error-codes"]
			if !ok {
				t.Fatalf("%s: нет x-error-codes", op.OperationID)
			}
			// значение расширения — json.RawMessage или []any, в зависимости от загрузчика
			b, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			var codes []string
			if err := json.Unmarshal(b, &codes); err != nil {
				t.Fatalf("%s: x-error-codes — не список строк: %v", op.OperationID, err)
			}
			documented = append(documented, codes...)
		}
	}
	slices.Sort(documented)
	documented = slices.Compact(documented)
	want := slices.Sorted(slices.Values(geo.Codes))
	if !slices.Equal(documented, want) {
		t.Fatalf("x-error-codes операций geo %v, geo.Codes %v", documented, want)
	}
}

// Swagger (/docs) — только по флагу: без него маршрута нет, ответ — обычный 404 Problem.
// С флагом контракт отдаётся ровно тот, что вшит в бинарник.
func TestDocsOnlyWhenEnabled(t *testing.T) {
	get := func(h http.Handler, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
		return rec
	}
	off, err := public.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(off, "/docs"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"http.not_found"`) {
		t.Fatalf("без флага: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	o := testOptions(t)
	o.Docs = true
	on, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(on, "/docs"); rec.Code != http.StatusOK {
		t.Fatalf("/docs: status = %d", rec.Code)
	}
	want, err := oapi.GetSpecJSON()
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(on, "/docs/openapi.json"); rec.Code != http.StatusOK || rec.Body.String() != string(want) {
		t.Fatalf("/docs/openapi.json: status = %d", rec.Code)
	}
}

// Конвейер собран: на анонимной операции Authorization не читается (битый токен — запрос идёт
// анонимно), rate limit — до хендлера; на операции с bearer аутентификация стоит до rate limit.
func TestPipelineWired(t *testing.T) {
	get := func(h http.Handler, authz string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, minVersionPath, nil)
		if authz != "" {
			r.Header.Set("Authorization", authz)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	o := testOptions(t)
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	// min-version — security: []: клиент с истёкшим токеном в заголовке не получает 401
	if rec := get(h, "Bearer garbage"); rec.Code != http.StatusOK {
		t.Fatalf("анонимная операция, битый токен: %d %s — ждали 200", rec.Code, rec.Body.String())
	}

	o.Limiter = denyAll{}
	h, err = public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(h, ""); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("лимит: %d %v", rec.Code, rec.Header())
	}

	// Порядок на операции с bearer — обязательным и опциональным ([{}, {bearer: []}]): в контракте
	// такой операции пока нет, поэтому security у min-version подменяется. Аутентификация стоит до
	// rate limit — это принятый порядок спеки §6.1, а не защита: битый токен получает 401, не
	// расходуя лимит, и перебор токенов лимитом не сдерживается (остаток M-3 — спека identity).
	for name, sec := range map[string]openapi3.SecurityRequirements{
		"обязательный bearer": {{"bearer": {}}},
		"опциональный bearer": {{}, {"bearer": {}}},
	} {
		spec, err := oapi.GetSpec()
		if err != nil {
			t.Fatal(err)
		}
		spec.Paths.Find("/v1/app/min-version").Get.Security = &sec
		h, err := public.NewHandlerWithSpec(slog.New(slog.DiscardHandler), o, spec)
		if err != nil {
			t.Fatal(err)
		}
		if rec := get(h, "Bearer garbage"); rec.Code != http.StatusUnauthorized ||
			!strings.Contains(rec.Body.String(), `"code":"auth.unauthenticated"`) {
			t.Fatalf("%s, битый токен при исчерпанном лимите: %d %s — ждали 401 (auth до ratelimit)",
				name, rec.Code, rec.Body.String())
		}
	}
}

// Render за Cloudflare (пир 10.x / без локального прокси): все клиенты приходят от одного пира
// балансировщика с одним узлом CDN правее в X-Forwarded-For. С заголовком CDN клиенты не делят
// лимит по IP; второй запрос того же клиента — 429 (лимит действительно считается по адресу из
// заголовка).
func TestRateLimitByClientIPHeader(t *testing.T) {
	o := testOptions(t)
	o.Peer = peer.Config{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		ClientIPHeader: "CF-Connecting-IP",
	}
	o.Limiter = ratelimit.NewPG(o.DB, clock.System)
	o.RateRules = ratelimit.DefaultRules()
	d := o.RateRules[ratelimit.DefaultClass]
	d.IP = ratelimit.Policy{Limit: 1, Period: time.Hour}
	o.RateRules[ratelimit.DefaultClass] = d
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	get := func(client string) int {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, minVersionPath, nil)
		r.RemoteAddr = "10.0.0.5:4000"
		r.Header.Set("X-Forwarded-For", client+", 104.16.0.1")
		r.Header.Set("CF-Connecting-IP", client)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if code := get("203.0.113.7"); code != http.StatusOK {
		t.Fatalf("первый клиент: %d", code)
	}
	if code := get("198.51.100.8"); code != http.StatusOK {
		t.Fatalf("второй клиент за тем же узлом CDN: %d — лимит общий", code)
	}
	if code := get("203.0.113.7"); code != http.StatusTooManyRequests {
		t.Fatalf("первый клиент повторно: %d — ждали 429", code)
	}
}

type denyAll struct{}

func (denyAll) Allow(context.Context, string, ratelimit.Policy) (ratelimit.Result, error) {
	return ratelimit.Result{RetryAfter: 5 * time.Second}, nil
}
func (denyAll) Add(context.Context, string, ratelimit.Policy) error          { return nil }
func (denyAll) Over(context.Context, string, ratelimit.Policy) (bool, error) { return true, nil }

// Без зависимостей хендлер не собирается: забытый в main лимитер, токены, geo или версии — отказ старта.
func TestNewHandlerRequiresDependencies(t *testing.T) {
	full := testOptions(t)
	for name, mutate := range map[string]func(*public.Options){
		"без базы":     func(o *public.Options) { o.DB = nil },
		"без токенов":  func(o *public.Options) { o.Tokens = nil },
		"без сессий":   func(o *public.Options) { o.Sessions = nil },
		"без лимитера": func(o *public.Options) { o.Limiter = nil },
		"без правил":   func(o *public.Options) { o.RateRules = nil },
		"без geo":      func(o *public.Options) { o.Geo = nil },
		"без версий":   func(o *public.Options) { o.AppVersions = nil },
		"нет класса default": func(o *public.Options) {
			o.RateRules = ratelimit.Rules{"auth": ratelimit.DefaultRules()["auth"]} // нет default
		},
		"невалидная политика": func(o *public.Options) {
			r := ratelimit.DefaultRules()
			d := r[ratelimit.DefaultClass]
			d.IP = ratelimit.Policy{Limit: 5} // без периода: Allow ошибался бы, а лимит молча не работал
			r[ratelimit.DefaultClass] = d
			o.RateRules = r
		},
	} {
		o := full
		mutate(&o)
		if _, err := public.NewHandler(slog.New(slog.DiscardHandler), o); err == nil {
			t.Errorf("%s: собрано", name)
		}
	}
}

// Опечатка класса в API_RATE_LIMITS (atuh вместо auth) — отказ старта с именем класса, а не
// переопределение, которое молча ни на что не действует.
func TestNewHandlerRejectsStrayRateClass(t *testing.T) {
	o := testOptions(t)
	r, err := ratelimit.ParseRules("atuh.ip=30/1m:10", ratelimit.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	o.RateRules = r
	if _, err := public.NewHandler(slog.New(slog.DiscardHandler), o); err == nil || !strings.Contains(err.Error(), "atuh") {
		t.Fatalf("err = %v", err)
	}
}

// Страж: каждый класс x-rate-limit контракта описан в правилах по умолчанию (иначе API не
// стартует). С findNearestCity (geo_nearest) x-rate-limit в контракте есть — пустой список значит,
// что страж проверяет вхолостую.
func TestRateLimitClassesKnown(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	// при пустых правилах UnknownClasses перечисляет все операции с x-rate-limit
	if len(ratelimit.UnknownClasses(spec, ratelimit.Rules{})) == 0 {
		t.Fatal("в контракте нет ни одной операции с x-rate-limit — страж проверяет вхолостую")
	}
	for _, v := range ratelimit.UnknownClasses(spec, ratelimit.DefaultRules()) {
		t.Error(v)
	}
}
