package admin_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// testOptions — рабочий набор зависимостей: лимиты пропускают всё, база — чистая
// (идемпотентность). Входа сотрудников до спеки identity нет.
func testOptions(t *testing.T) admin.Options {
	t.Helper()
	return admin.Options{
		DB:        dbtest.NewPool(t),
		Limiter:   ratelimit.Unlimited,
		RateRules: ratelimit.DefaultRules(),
	}
}

func TestHealthMatchesContract(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	h, err := admin.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
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
	owners := apitest.Owners(reflect.TypeOf(admin.Server{}), apitest.ModuleOf)
	for _, v := range apitest.TagViolations(ops, owners) {
		t.Error(v)
	}
}

// Валидатор пропускает маршрут не из контракта — 404 отвечает роутер в формате Problem.
func TestUnknownRouteIsProblemThroughValidator(t *testing.T) {
	h, err := admin.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
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
	h, err := admin.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
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
	off, err := admin.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(off, "/docs"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"http.not_found"`) {
		t.Fatalf("без флага: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	o := testOptions(t)
	o.Docs = true
	on, err := admin.NewHandler(slog.New(slog.DiscardHandler), o)
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

// Без зависимостей хендлер не собирается: забытый в main лимитер — отказ старта.
func TestNewHandlerRequiresDependencies(t *testing.T) {
	full := testOptions(t)
	for name, mutate := range map[string]func(*admin.Options){
		"без базы":     func(o *admin.Options) { o.DB = nil },
		"без лимитера": func(o *admin.Options) { o.Limiter = nil },
		"без правил":   func(o *admin.Options) { o.RateRules = nil },
		"нет класса default": func(o *admin.Options) {
			o.RateRules = ratelimit.Rules{"auth": ratelimit.DefaultRules()["auth"]} // нет default
		},
		"невалидная политика": func(o *admin.Options) {
			r := ratelimit.DefaultRules()
			d := r[ratelimit.DefaultClass]
			d.IP = ratelimit.Policy{Limit: 5} // без периода: Allow ошибался бы, а лимит молча не работал
			r[ratelimit.DefaultClass] = d
			o.RateRules = r
		},
	} {
		o := full
		mutate(&o)
		if _, err := admin.NewHandler(slog.New(slog.DiscardHandler), o); err == nil {
			t.Errorf("%s: собрано", name)
		}
	}
}

// Опечатка класса в ADMIN_RATE_LIMITS (atuh вместо auth) — отказ старта с именем класса, а не
// переопределение, которое молча ни на что не действует.
func TestNewHandlerRejectsStrayRateClass(t *testing.T) {
	o := testOptions(t)
	r, err := ratelimit.ParseRules("atuh.ip=30/1m:10", ratelimit.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	o.RateRules = r
	if _, err := admin.NewHandler(slog.New(slog.DiscardHandler), o); err == nil || !strings.Contains(err.Error(), "atuh") {
		t.Fatalf("err = %v", err)
	}
}

// Страж: каждый класс x-rate-limit контракта описан в правилах по умолчанию. Пока в контракте
// нет ни одной операции с x-rate-limit, проверять нечего — тест пропускается явно, а не
// проходит вхолостую; с первой такой операцией страж станет активным сам.
func TestRateLimitClassesKnown(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	// при пустых правилах UnknownClasses перечисляет все операции с x-rate-limit
	if len(ratelimit.UnknownClasses(spec, ratelimit.Rules{})) == 0 {
		t.Skip("в контракте нет x-rate-limit — страж станет активным с первой такой операцией")
	}
	for _, v := range ratelimit.UnknownClasses(spec, ratelimit.DefaultRules()) {
		t.Error(v)
	}
}

// Конвейер собран: rate limit подключён и стоит до хендлера.
func TestPipelineWired(t *testing.T) {
	o := testOptions(t)
	o.Limiter = denyAll{}
	h, err := admin.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
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
