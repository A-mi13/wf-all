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

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/identity"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/staff"
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
	t.Parallel()
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

// Все 54 операции (33 плана A и 21 плана B) собраны в Server (операции platform — свои, staff и geo — встроенные
// хендлеры модулей). У нулевого Server{} (нулевые хендлеры — тесты конвейера; cmd/admin-api собирает
// настоящие) каждая — через strict-middleware модуля staff, который подключает NewHandler, — отвечает 501
// http.not_implemented: операции staff — из Middleware нулевого хендлера (Task 10a), остальные — проверка
// нулевого хендлера geo и операций платформы.
func TestServerStubsNotImplemented(t *testing.T) {
	t.Parallel()
	mw := (&staffadmin.Handler{}).Middleware()
	v := reflect.ValueOf(admin.Server{})
	n := 0
	for i := range v.NumMethod() {
		m := v.Type().Method(i)
		ft := m.Type                     // с получателем: (s, ctx, запрос)
		if !staffadmin.IsOperation(ft) { // служебные методы той же формы (Label) — не операции
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
	if n != 54 {
		t.Fatalf("операций сервера %d, ждали 54", n)
	}
}

// Операция с сессией без входа — 401 auth.unauthenticated от валидатора, до хендлера.
func TestSessionOperationRequiresLogin(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// Спека сотрудников §7, план B (Task 7): коды platform.* в x-error-codes операций контракта admin —
// ровно коды подтверждений и флагов (httpx.AdminApprovalCodes ∪ httpx.AdminFlagCodes), в обе стороны:
// ни кода без документа, ни документа без кода.
func TestAdminPlatformCodesDocumented(t *testing.T) {
	t.Parallel()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	documented := documentedWithPrefix(t, spec, "platform.")
	want := slices.Sorted(slices.Values(slices.Concat(httpx.AdminApprovalCodes, httpx.AdminFlagCodes)))
	if !slices.Equal(documented, want) {
		t.Fatalf("коды platform.* операций %v, httpx.AdminApprovalCodes ∪ AdminFlagCodes %v", documented, want)
	}
}

// План B (Task 7, спека сотрудников §6.3 п.7): коды geo.* в x-error-codes операций контракта admin и
// geo.AdminCodes совпадают в обе стороны.
func TestGeoAdminCodesDocumented(t *testing.T) {
	t.Parallel()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	documented := documentedWithPrefix(t, spec, "geo.")
	if want := slices.Sorted(slices.Values(geo.AdminCodes)); !slices.Equal(documented, want) {
		t.Fatalf("коды geo.* операций %v, geo.AdminCodes %v", documented, want)
	}
}

// Спека сотрудников §6.3 п.7: у каждого кода x-error-codes операций контракта admin — префикс модуля,
// у которого есть свой список кодов и страж сверки (staff. — TestStaffCodesDocumented, geo. —
// TestGeoAdminCodesDocumented, identity. — TestIdentityAdminCodesDocumented, platform. —
// TestAdminPlatformCodesDocumented), либо это общий код
// платформы или конвейера. Код нового модуля без своего списка и стража сюда не пройдёт.
func TestAdminCodePrefixesKnown(t *testing.T) {
	t.Parallel()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	byOp, err := apitest.OperationCodes(spec)
	if err != nil {
		t.Fatal(err)
	}
	if bad := unknownCodePrefixes(byOp); len(bad) != 0 {
		t.Fatalf("коды без списка модуля и стража: %v", bad)
	}
	// страж не вакуумен: код чужого модуля и код без модуля ловятся, известные — нет
	probe := map[string][]string{"x": {"teams.not_found", "oops", "geo.slug_taken", "staff.not_found", "platform.flag_stale", "auth.forbidden"}}
	if bad := unknownCodePrefixes(probe); !slices.Equal(bad, []string{"x: oops", "x: teams.not_found"}) {
		t.Fatalf("подсадка: %v", bad)
	}
}

// adminCodePrefixes — модули, у которых есть список кодов и страж сверки с контрактом admin.
var adminCodePrefixes = []string{"staff.", "geo.", "identity.", "platform."}

// unknownCodePrefixes — «операция: код» для кодов без известного префикса и вне общих кодов.
func unknownCodePrefixes(byOp map[string][]string) []string {
	common := slices.Concat(httpx.PlatformCodes, httpx.AdminCommonCodes)
	var bad []string
	for op, codes := range byOp {
		for _, c := range codes {
			if slices.Contains(common, c) || slices.ContainsFunc(adminCodePrefixes, func(p string) bool { return strings.HasPrefix(c, p) }) {
				continue
			}
			bad = append(bad, op+": "+c)
		}
	}
	slices.Sort(bad)
	return bad
}

// documentedWithPrefix — коды x-error-codes всех операций контракта с этим префиксом, по порядку без повторов.
func documentedWithPrefix(t *testing.T, spec *openapi3.T, prefix string) []string {
	t.Helper()
	byOp, err := apitest.OperationCodes(spec)
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for _, codes := range byOp {
		for _, c := range codes {
			if strings.HasPrefix(c, prefix) {
				documented = append(documented, c)
			}
		}
	}
	slices.Sort(documented)
	return slices.Compact(documented)
}

// Решение подтверждения (approveApproval, rejectApproval) доступно по любому праву «вдвоём»:
// x-permission-any — ровно нечерновые права, у которых хоть одна роль в матрице — dual (R8).
// Новое право «вдвоём» без правки контракта сюда не попадёт — страж это ловит.
func TestApprovalDecisionPermissionsAreDual(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// Cookie хендлера staff (staffadmin.Cookie) и конвейера (CookieName(CookieSecure), CookieSecure) задаются
// сборкой по отдельности (ревью 10a m2): рассогласование — отказ старта, иначе новая cookie входа не
// читалась бы конвейером (вход по кругу), а стирание и D9 у принятия молча не работали бы. Нулевой
// хендлер (тесты конвейера) сверку пропускает; согласованный собирается.
func TestNewHandlerCookieMismatch(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]staffadmin.Cookie{
		"dev-имя при Secure": {Name: admin.CookieName(false), Secure: true},
		"без Secure":         {Name: admin.CookieName(true), Secure: false},
		"чужое имя":          {Name: "session", Secure: true},
	} {
		o := testOptions(t)
		o.Staff = staffadmin.New(staff.Deps{}, c)
		if _, err := admin.NewHandler(quiet(), o); err == nil || !strings.Contains(err.Error(), "cookie") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, secure := range []bool{true, false} {
		o := testOptions(t)
		o.CookieSecure = secure
		if !secure {
			o.Origin = "http://127.0.0.1:5173" // без Secure — только dev по http
		}
		o.Staff = staffadmin.New(staff.Deps{}, staffadmin.Cookie{Name: admin.CookieName(secure), Secure: secure})
		o.Approvals = approvals.New(nil, o.Clock, approvals.NewRegistry())
		if _, err := admin.NewHandler(quiet(), o); err != nil {
			t.Errorf("согласованная cookie (secure=%v): %v", secure, err)
		}
	}
}

// Подтверждения вдвоём (Options.Approvals) — ровно вместе с собранным хендлером staff: собранный staff без
// них молча отвечал бы 501 на решения в проде (забыли в сборке 10c), подтверждения без staff некому
// подписать (Staff.Label). Оба нулевые (тесты конвейера) и оба заданные — собирается.
func TestNewHandlerApprovalsWiring(t *testing.T) {
	t.Parallel()
	built := func() *staffadmin.Handler {
		return staffadmin.New(staff.Deps{}, staffadmin.Cookie{Name: admin.CookieName(true), Secure: true})
	}
	svc := approvals.New(nil, clocktest.New(t0), approvals.NewRegistry())
	for name, c := range map[string]struct {
		staff *staffadmin.Handler
		svc   admin.ApprovalService
		ok    bool
	}{
		"оба нулевые":             {&staffadmin.Handler{}, nil, true},
		"оба заданы":              {built(), svc, true},
		"staff без подтверждений": {built(), nil, false},
		"подтверждения без staff": {&staffadmin.Handler{}, svc, false},
	} {
		o := testOptions(t)
		o.Staff, o.Approvals = c.staff, c.svc
		_, err := admin.NewHandler(quiet(), o)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Опечатка класса в ADMIN_RATE_LIMITS (atuh вместо auth) — отказ старта с именем класса, а не
// переопределение, которое молча ни на что не действует.
func TestNewHandlerRejectsStrayRateClass(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// Спека identity §6.5, план I-1: коды identity.* в x-error-codes операций контракта admin и identity.AdminCodes
// совпадают в обе стороны.
func TestIdentityAdminCodesDocumented(t *testing.T) {
	t.Parallel()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	documented := documentedWithPrefix(t, spec, "identity.")
	if want := slices.Sorted(slices.Values(identity.AdminCodes)); !slices.Equal(documented, want) {
		t.Fatalf("коды identity.* операций %v, identity.AdminCodes %v", documented, want)
	}
}
