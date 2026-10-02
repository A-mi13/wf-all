package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/apidocs"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
)

// Options — зависимости API админки; cmd/admin-api собирает их из конфига. Вход сотрудников
// (cookie-сессия, TOTP) добавит спека identity.
type Options struct {
	Docs           bool          // Swagger UI и контракт на /docs (config.HTTP.DocsEnabled)
	RequestTimeout time.Duration // крайний срок запроса; 0 — 15 с
	Peer           peer.Config
	DB             *pgxpool.Pool // ключи идемпотентности
	Limiter        ratelimit.Limiter
	RateRules      ratelimit.Rules
}

const defaultRequestTimeout = 15 * time.Second

// NewHandler собирает API админки на общей HTTP-платформе: конвейер спеки §6.1 стоит до
// strict-хендлера. Забытая зависимость или класс x-rate-limit без правила — отказ старта.
func NewHandler(log *slog.Logger, o Options) (http.Handler, error) {
	if o.DB == nil || o.Limiter == nil || o.RateRules == nil {
		return nil, errors.New("admin: не заданы зависимости (DB, Limiter, RateRules)")
	}
	// класс default (операции без x-rate-limit) есть, политики валидны: невалидная дала бы
	// ошибку Allow, а ratelimit.Middleware при ошибке пропускает — лимит молча не работал бы
	if err := o.RateRules.Validate(); err != nil {
		return nil, fmt.Errorf("admin: %w", err)
	}
	if err := o.Peer.Validate(); err != nil {
		return nil, err
	}
	spec, err := oapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("admin: контракт: %w", err)
	}
	if unknown := ratelimit.UnknownClasses(spec, o.RateRules); len(unknown) > 0 {
		return nil, fmt.Errorf("admin: классы rate limit без правил: %v", unknown)
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
	requestErr := httpx.RequestErrorHandler(log)
	strict := oapi.NewStrictHandlerWithOptions(Server{}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  requestErr,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	// конвейер — спека §6.1
	router := httpx.NewRouter(log,
		peer.Middleware(o.Peer),
		httpx.LimitBody(httpx.MaxBodyBytes),
		httpx.Timeout(timeout),
		routes,
		// вход сотрудников (cookie-сессия, TOTP) — спека identity; до неё операции с security —
		// 401. Заголовок WWW-Authenticate у админки не Bearer — поэтому пуст
		httpx.ValidateRequests(httpx.ValidateOptions{}),
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
	return oapi.HandlerWithOptions(strict, oapi.ChiServerOptions{
		BaseRouter: router,
		// ErrorHandlerFunc — ошибки биндинга параметров в chi-обёртке; по умолчанию oapi-codegen
		// отвечает text/plain с текстом ошибки
		ErrorHandlerFunc: requestErr,
	}), nil
}
