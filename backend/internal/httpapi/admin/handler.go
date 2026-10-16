package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/apidocs"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
	staffadmin "wf/backend/internal/staff/admin"
)

// Options — зависимости API админки; cmd/admin-api собирает их из конфига (config.Admin).
type Options struct {
	Docs           bool          // Swagger UI и контракт на /docs (config.HTTP.DocsEnabled)
	RequestTimeout time.Duration // крайний срок запроса; 0 — 15 с
	Peer           peer.Config
	DB             *pgxpool.Pool // ключи идемпотентности
	Limiter        ratelimit.Limiter
	RateRules      ratelimit.Rules
	// Origin — ADMIN_ORIGIN: единственный origin админки (схема://хост[:порт]); с ним CSRF
	// сверяет Origin изменяющих запросов (ValidateOrigin).
	Origin string
	// IPAllowlist — ADMIN_IP_ALLOWLIST: сети, из которых пускают; пусто — выключен, /0 — отказ
	// старта (ValidateAllowlist).
	IPAllowlist []netip.Prefix
	// CookieSecure — cookie сессии с Secure и именем __Host-wf_admin_session (CookieName);
	// false — только dev по http.
	CookieSecure bool
	// StaticDir — ADMIN_STATIC_DIR: собранная apps/admin с того же origin; пусто — не отдаётся.
	StaticDir string
	Sessions  auth.StaffSessions  // загрузчик сессии сотрудника по токену cookie
	Staff     *staffadmin.Handler // операции тега staff
	Geo       *geoadmin.Handler   // операции тега geo
	Clock     clock.Clock         // негативный кэш сессий
	// Approvals — подтверждения вдвоём (*approvals.Service после RegisterApprovals модулей, сборка 10c):
	// операции тега platform listApprovals, approve, reject, cancel. Обязателен вместе с собранным Staff
	// (подпись решившего — Staff.Label) и только с ним: nil при нулевом Staff — эти операции отвечают 501.
	Approvals ApprovalService
}

// ApprovalService — решения и списки подтверждений вдвоём (реализует *approvals.Service; интерфейс —
// чтобы тесты сервера могли подменить сервис без сценариев staff).
type ApprovalService interface {
	List(ctx context.Context, st *auth.Staff, box, status, cursor string, limit int) ([]approvals.Approval, string, error)
	Approve(ctx context.Context, st *auth.Staff, label string, m approvals.Meta, id uuid.UUID, comment string) (approvals.Approval, error)
	Reject(ctx context.Context, st *auth.Staff, label string, m approvals.Meta, id uuid.UUID, comment string) (approvals.Approval, error)
	Cancel(ctx context.Context, st *auth.Staff, label string, m approvals.Meta, id uuid.UUID) (approvals.Approval, error)
}

var _ ApprovalService = (*approvals.Service)(nil)

const defaultRequestTimeout = 15 * time.Second

// NewHandler собирает API админки на общей HTTP-платформе: конвейер спеки сотрудников §5.1 стоит
// до strict-хендлера. Забытая зависимость, класс x-rate-limit без правила, операция без
// x-permission, неверный ADMIN_ORIGIN или аллоулист — отказ старта.
func NewHandler(log *slog.Logger, o Options) (http.Handler, error) {
	spec, err := oapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("admin: контракт: %w", err)
	}
	return newHandler(log, o, spec)
}

// newHandler — сборка на заданном контракте; в работе это всегда oapi.GetSpec (NewHandler).
func newHandler(log *slog.Logger, o Options, spec *openapi3.T) (http.Handler, error) {
	if o.DB == nil || o.Limiter == nil || o.RateRules == nil || o.Sessions == nil || o.Staff == nil ||
		o.Geo == nil || o.Clock == nil {
		return nil, errors.New("admin: не заданы зависимости (DB, Limiter, RateRules, Sessions, Staff, Geo, Clock)")
	}
	// класс default (операции без x-rate-limit) есть, политики валидны: невалидная дала бы
	// ошибку Allow, а ratelimit.Middleware при ошибке пропускает — лимит молча не работал бы
	if err := o.RateRules.Validate(); err != nil {
		return nil, fmt.Errorf("admin: %w", err)
	}
	// отказы сессии считаются в IP-лимите staff_auth (R9): без класса перебор cookie не ограничен
	authRule, ok := o.RateRules[classStaffAuth]
	if !ok {
		return nil, fmt.Errorf("admin: нет правила rate limit класса %s (отказы сессии сотрудника)", classStaffAuth)
	}
	// cookie ставит хендлер staff, читает и стирает конвейер: имя и Secure — одни и те же (ревью 10a m2).
	// Нулевой хендлер (cmd/admin-api до сборки 10c) cookie не ставит — сверять нечего.
	if c, ok := o.Staff.CookieConfig(); ok && (c.Name != CookieName(o.CookieSecure) || c.Secure != o.CookieSecure) {
		return nil, fmt.Errorf("admin: cookie хендлера staff (%s, Secure=%v) не совпадает с cookie конвейера (%s, Secure=%v)",
			c.Name, c.Secure, CookieName(o.CookieSecure), o.CookieSecure)
	}
	// подтверждения — ровно с собранным staff: без них операции platform молча отвечали бы 501, а без
	// staff некому подписать решившего (Staff.Label)
	if _, built := o.Staff.CookieConfig(); built != (o.Approvals != nil) {
		return nil, fmt.Errorf("admin: подтверждения (Approvals) задаются ровно вместе с собранным хендлером staff (собран: %v, Approvals: %v)", built, o.Approvals != nil)
	}
	if err := o.Peer.Validate(); err != nil {
		return nil, err
	}
	if err := ValidateOrigin(o.Origin); err != nil {
		return nil, err
	}
	if err := ValidateAllowlist(o.IPAllowlist); err != nil {
		return nil, err
	}
	if unknown := ratelimit.UnknownClasses(spec, o.RateRules); len(unknown) > 0 {
		return nil, fmt.Errorf("admin: классы rate limit без правил: %v", unknown)
	}
	// класс из ADMIN_RATE_LIMITS, которого нет ни в умолчаниях, ни в контракте, — опечатка
	if stray := ratelimit.StrayClasses(spec, o.RateRules); len(stray) > 0 {
		return nil, fmt.Errorf("admin: классы rate limit не из правил по умолчанию и не из x-rate-limit контракта (опечатка в ADMIN_RATE_LIMITS?): %v", stray)
	}
	perms, err := buildPermissions(spec)
	if err != nil {
		return nil, err
	}
	var static http.Handler
	if o.StaticDir != "" {
		if static, err = newStatic(o.StaticDir, log); err != nil {
			return nil, err
		}
	}
	title := spec.Info.Title
	routes, err := httpx.Routes(spec)
	if err != nil {
		return nil, err
	}
	timeout := o.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	sessions := &sessionLayer{
		sessions: o.Sessions, cookie: CookieName(o.CookieSecure), secure: o.CookieSecure,
		limiter: o.Limiter, policy: authRule.IP, neg: newNegCache(o.Clock, negCacheTTL, negCacheMax), log: log,
	}
	requestErr := httpx.RequestErrorHandler(log)
	// strict-middleware модуля staff: User-Agent и токен cookie в ctx операций тега staff, перевод
	// ошибок staff.Problem, стирание cookie при погашенной сессии, 501 у несобранного хендлера
	srv := Server{staffHandler: o.Staff, geoHandler: o.Geo, approvals: o.Approvals, audit: o.DB}
	if o.Approvals != nil {
		srv.label = o.Staff.Label
	}
	strict := oapi.NewStrictHandlerWithOptions(srv, []oapi.StrictMiddlewareFunc{o.Staff.Middleware(), requestMeta}, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  requestErr,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	// конвейер — спека сотрудников §5.1 (.claude/rules/backend-http.md)
	router := httpx.NewRouter(log,
		peer.Middleware(o.Peer),
		// аллоулист — на всё: API, /docs и статику
		allowlist(o.IPAllowlist),
		// несколько строк Accept-Language — одна (RFC 9110 §5.3): сгенерированный код принимает у
		// заголовка-параметра ровно одну строку, на вторую ответил бы 400
		httpx.CombineHeaders("Accept-Language"),
		httpx.LimitBody(httpx.MaxBodyBytes),
		httpx.Timeout(timeout),
		routes,
		noStore,
		// сессия — до CSRF и валидатора: валидатор узнаёт о входе из ctx (security операции), а
		// сотрудник нужен лимиту (staff:<id>) и идемпотентности (⚠️ 3a)
		sessions.middleware,
		// до валидатора: тело изменяющего запроса не разбирается, пока не доказано, что он со
		// страницы админки
		csrf(o.Origin),
		// 401 без сессии на операциях с security; WWW-Authenticate у cookie-сессии нет
		httpx.ValidateRequests(httpx.ValidateOptions{Authenticated: auth.AuthenticatedStaff}),
		permissions(perms, log),
		ratelimit.Middleware(o.Limiter, o.RateRules, log),
		idempotency.Middleware(o.DB, idempotency.Config{}, log),
	)
	if o.Docs {
		// /docs не в контракте: валидатор такие маршруты пропускает дальше, к роутеру
		specJSON, err := oapi.GetSpecJSON()
		if err != nil {
			return nil, fmt.Errorf("admin: контракт: %w", err)
		}
		apidocs.Mount(router, title, specJSON)
	}
	if static != nil {
		// всё, что не нашёл роутер: вне /v1 — сборка админки, /v1/* — 404 Problem
		router.NotFound(static.ServeHTTP)
	}
	return oapi.HandlerWithOptions(strict, oapi.ChiServerOptions{
		BaseRouter: router,
		// без маршрута контракта (пустой сегмент {param}) хендлер недостижим: RequireRoute — на хендлере,
		// NotFoundWithoutRoute — на ошибке биндинга параметра. RequireRoute — защита в глубину:
		// на текущем контракте интеграционно не воспроизводится (пустой обязательный параметр падает
		// на биндинге раньше), его стережёт юнит-тест httpx.TestRequireRoute.
		Middlewares: []oapi.MiddlewareFunc{httpx.RequireRoute},
		// ErrorHandlerFunc — ошибки биндинга параметров в chi-обёртке; по умолчанию oapi-codegen
		// отвечает text/plain с текстом ошибки
		ErrorHandlerFunc: httpx.NotFoundWithoutRoute(requestErr),
	}), nil
}

// noStore — ответы маршрутов контракта не кэшируются (данные сотрудника, отказы конвейера):
// Cache-Control: no-store до хендлера; хендлер вправе поставить свой (private, no-store).
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := httpx.RouteFrom(r.Context()); ok {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
