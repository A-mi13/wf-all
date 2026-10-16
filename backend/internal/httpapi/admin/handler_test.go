package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/apitest"
	staffadmin "wf/backend/internal/staff/admin"
)

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// loginRequest — анонимный вход с телом по схеме: проходит CSRF (Origin админки и X-WF-Admin),
// валидатор и доходит до хендлера.
func loginRequest() *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/staff/auth/login",
		strings.NewReader(`{"email":"olga.v@wf.example","password":"Жёлтая карточка на 90-й"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("X-WF-Admin", "1")
	return req
}

// Task 9a: контракт заморожен раньше сценариев — операция без хендлера проходит весь конвейер и
// отвечает 501 http.not_implemented в формате Problem, как его описывает контракт.
func TestStubAnswersNotImplementedThroughPipeline(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	h, err := admin.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := v.Do(t, h, loginRequest())
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), `"code":"http.not_implemented"`) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// Все 33 операции плана A собраны в Server (операции platform — свои, staff и geo — встроенные
// хендлеры модулей). Как в cmd/admin-api до сборки 10c (нулевые хендлеры), каждая — через
// strict-middleware модуля staff, который подключает NewHandler, — отвечает 501 http.not_implemented:
// операции staff — из Middleware нулевого хендлера (Task 10a), остальные — заглушки 9a.
func TestServerStubsNotImplemented(t *testing.T) {
	mw := (&staffadmin.Handler{}).Middleware()
	v := reflect.ValueOf(admin.Server{})
	ctxType, errType := reflect.TypeFor[context.Context](), reflect.TypeFor[error]()
	n := 0
	for i := range v.NumMethod() {
		m := v.Type().Method(i)
		ft := m.Type // с получателем: (s, ctx, запрос)
		if ft.NumIn() != 3 || ft.In(1) != ctxType || ft.NumOut() != 2 || ft.Out(1) != errType {
			continue
		}
		n++
		f := func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
			out := v.Method(i).Call([]reflect.Value{reflect.ValueOf(ctx), reflect.Zero(ft.In(2))})
			err, _ := out[1].Interface().(error)
			return out[0].Interface(), err
		}
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		resp, err := mw(f, m.Name)(r.Context(), httptest.NewRecorder(), r, nil)
		pe, ok := errors.AsType[httpx.ProblemError](err)
		if resp != nil || !ok || pe.Problem().Status != http.StatusNotImplemented || pe.Problem().Code != httpx.CodeNotImplemented {
			t.Errorf("%s: ответ %v, ошибка %v — ждали 501 http.not_implemented", m.Name, resp, err)
		}
	}
	if n != 33 {
		t.Fatalf("операций сервера %d, ждали 33", n)
	}
}

// Операция с сессией без входа — 401 auth.unauthenticated от валидатора, до хендлера.
func TestSessionOperationRequiresLogin(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	h, err := admin.NewHandler(slog.New(slog.DiscardHandler), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := v.Do(t, h, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/staff/me", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"auth.unauthenticated"`) {
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
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/staff/me", body))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"code":"request.too_large"`) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// Страж спеки §12.4 «коды ошибок задокументированы»: коды платформы (httpx.PlatformCodes) и
// конвейера admin-api (httpx.AdminCommonCodes, спека сотрудников §5.1) и x-error-codes-common
// контракта admin совпадают в обе стороны.
func TestPlatformCodesDocumented(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	v, err := apitest.CommonCodeViolations(spec, slices.Concat(httpx.PlatformCodes, httpx.AdminCommonCodes))
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range v {
		t.Error(msg)
	}
}

// Спека сотрудников §7: коды подтверждений (httpx.AdminApprovalCodes) и коды platform.* в
// x-error-codes операций контракта admin совпадают в обе стороны — ни кода без документа, ни
// документа без кода.
func TestAdminApprovalCodesDocumented(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	byOp, err := apitest.OperationCodes(spec)
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for _, codes := range byOp {
		for _, c := range codes {
			if strings.HasPrefix(c, "platform.") {
				documented = append(documented, c)
			}
		}
	}
	slices.Sort(documented)
	documented = slices.Compact(documented)
	if want := slices.Sorted(slices.Values(httpx.AdminApprovalCodes)); !slices.Equal(documented, want) {
		t.Fatalf("коды platform.* операций %v, httpx.AdminApprovalCodes %v", documented, want)
	}
}

// Решение подтверждения (approveApproval, rejectApproval) доступно по любому праву «вдвоём»:
// x-permission-any — ровно нечерновые права, у которых хоть одна роль в матрице — dual (R8).
// Новое право «вдвоём» без правки контракта сюда не попадёт — страж это ловит.
func TestApprovalDecisionPermissionsAreDual(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, p := range perm.Catalog {
		if p.Draft {
			continue
		}
		for _, g := range perm.Matrix[p.Key] {
			if g == auth.GrantDual {
				want = append(want, p.Key)
				break
			}
		}
	}
	slices.Sort(want)
	if len(want) == 0 {
		t.Fatal("в матрице нет прав «вдвоём» — страж проверяет вхолостую")
	}
	for _, id := range []string{"approveApproval", "rejectApproval"} {
		op := operation(t, spec, id)
		b, err := json.Marshal(op.Extensions["x-permission-any"])
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("%s: x-permission-any — не список строк: %v", id, err)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s: x-permission-any %v, права «вдвоём» %v", id, got, want)
		}
	}
}

// operation — операция контракта по operationId.
func operation(t *testing.T, spec *openapi3.T, id string) *openapi3.Operation {
	t.Helper()
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			// oapi-codegen вшивает operationId с заглавной буквы (имя метода)
			if strings.EqualFold(op.OperationID, id) {
				return op
			}
		}
	}
	t.Fatalf("нет операции %s", id)
	return nil
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
		"без сессий":   func(o *admin.Options) { o.Sessions = nil },
		"без staff":    func(o *admin.Options) { o.Staff = nil },
		"без geo":      func(o *admin.Options) { o.Geo = nil },
		"без часов":    func(o *admin.Options) { o.Clock = nil },
		"без класса staff_auth": func(o *admin.Options) {
			r := ratelimit.DefaultRules()
			delete(r, "staff_auth")
			o.RateRules = r
		},
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

// Страж: каждый класс x-rate-limit контракта описан в правилах по умолчанию (staff_auth — вход
// в админку, спека сотрудников §4.6). Контракт без x-rate-limit — ошибка: страж проверял бы
// вхолостую.
func TestRateLimitClassesKnown(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	// при пустых правилах UnknownClasses перечисляет все операции с x-rate-limit
	if len(ratelimit.UnknownClasses(spec, ratelimit.Rules{})) == 0 {
		t.Fatal("в контракте нет x-rate-limit — страж проверяет вхолостую")
	}
	for _, v := range ratelimit.UnknownClasses(spec, ratelimit.DefaultRules()) {
		t.Error(v)
	}
}

// Конвейер собран: rate limit подключён и стоит до хендлера (вход — класс staff_auth).
func TestPipelineWired(t *testing.T) {
	o := testOptions(t)
	o.Limiter = denyAll{}
	h, err := admin.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginRequest())
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
