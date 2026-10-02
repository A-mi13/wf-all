package public_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/token"
)

// testOptions — рабочий набор зависимостей: лимиты пропускают всё, сессий нет, ключ токенов —
// новый на каждый тест, база — чистая (идемпотентность).
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
		DB:        dbtest.NewPool(t),
		Tokens:    token.NewIssuer(k, clock.System),
		Sessions:  auth.NoSessions,
		Limiter:   ratelimit.Unlimited,
		RateRules: ratelimit.DefaultRules(),
	}
}

func TestHealthMatchesContract(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := v.Do(t, h, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
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
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/health", body))
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

// Конвейер собран: слой аутентификации стоит и на анонимной операции (битый токен — 401),
// rate limit — до хендлера.
func TestPipelineWired(t *testing.T) {
	o := testOptions(t)
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/health", nil)
	r.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"auth.unauthenticated"`) {
		t.Fatalf("битый токен: %d %s", rec.Code, rec.Body.String())
	}

	o.Limiter = denyAll{}
	h, _ = public.NewHandler(slog.New(slog.DiscardHandler), o)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/health", nil))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("лимит: %d %v", rec.Code, rec.Header())
	}
}

type denyAll struct{}

func (denyAll) Allow(context.Context, string, ratelimit.Policy) (ratelimit.Result, error) {
	return ratelimit.Result{RetryAfter: 5 * time.Second}, nil
}
func (denyAll) Add(context.Context, string, ratelimit.Policy) error          { return nil }
func (denyAll) Over(context.Context, string, ratelimit.Policy) (bool, error) { return true, nil }

// Без зависимостей хендлер не собирается: забытый в main лимитер или токены — отказ старта.
func TestNewHandlerRequiresDependencies(t *testing.T) {
	full := testOptions(t)
	for name, mutate := range map[string]func(*public.Options){
		"без базы":     func(o *public.Options) { o.DB = nil },
		"без токенов":  func(o *public.Options) { o.Tokens = nil },
		"без сессий":   func(o *public.Options) { o.Sessions = nil },
		"без лимитера": func(o *public.Options) { o.Limiter = nil },
		"без правил":   func(o *public.Options) { o.RateRules = nil },
		"класс x-rate-limit без правила": func(o *public.Options) {
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

// Страж: каждый класс x-rate-limit контракта описан в правилах по умолчанию.
func TestRateLimitClassesKnown(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range ratelimit.UnknownClasses(spec, ratelimit.DefaultRules()) {
		t.Error(v)
	}
}
