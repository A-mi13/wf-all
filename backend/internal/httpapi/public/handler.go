package public

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/apidocs"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
)

// Options — зависимости публичного API; cmd/api собирает их из конфига.
type Options struct {
	Docs           bool          // Swagger UI и контракт на /docs (config.HTTP.DocsEnabled)
	RequestTimeout time.Duration // крайний срок запроса; 0 — 15 с
	Peer           peer.Config
	DB             *pgxpool.Pool // ключи идемпотентности
	Tokens         auth.Verifier
	Sessions       auth.SessionLoader
	Limiter        ratelimit.Limiter
	RateRules      ratelimit.Rules
}

const defaultRequestTimeout = 15 * time.Second

// NewHandler собирает публичный API на общей HTTP-платформе: конвейер спеки §6.1 стоит до
// strict-хендлера. Забытая зависимость или класс x-rate-limit без правила — отказ старта.
func NewHandler(log *slog.Logger, o Options) (http.Handler, error) {
	spec, err := oapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("public: контракт: %w", err)
	}
	return newHandler(log, o, spec)
}

// newHandler — сборка на заданном контракте; в работе это всегда oapi.GetSpec (NewHandler).
func newHandler(log *slog.Logger, o Options, spec *openapi3.T) (http.Handler, error) {
	if o.DB == nil || o.Tokens == nil || o.Sessions == nil || o.Limiter == nil || o.RateRules == nil {
		return nil, errors.New("public: не заданы зависимости (DB, Tokens, Sessions, Limiter, RateRules)")
	}
	// класс default (операции без x-rate-limit) есть, политики валидны: невалидная дала бы
	// ошибку Allow, а ratelimit.Middleware при ошибке пропускает — лимит молча не работал бы
	if err := o.RateRules.Validate(); err != nil {
		return nil, fmt.Errorf("public: %w", err)
	}
	if err := o.Peer.Validate(); err != nil {
		return nil, err
	}
	if unknown := ratelimit.UnknownClasses(spec, o.RateRules); len(unknown) > 0 {
		return nil, fmt.Errorf("public: классы rate limit без правил: %v", unknown)
	}
	// класс из API_RATE_LIMITS, которого нет ни в умолчаниях, ни в контракте, — опечатка
	if stray := ratelimit.StrayClasses(spec, o.RateRules); len(stray) > 0 {
		return nil, fmt.Errorf("public: классы rate limit не из правил по умолчанию и не из x-rate-limit контракта (опечатка в API_RATE_LIMITS?): %v", stray)
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
		auth.Middleware(o.Tokens, o.Sessions, log),
		httpx.ValidateRequests(httpx.ValidateOptions{Authenticated: auth.Authenticated, WWWAuthenticate: "Bearer"}),
		ratelimit.Middleware(o.Limiter, o.RateRules, log),
		humancheck.Middleware(),
		idempotency.Middleware(o.DB, idempotency.Config{}, log),
	)
	if o.Docs {
		// /docs не в контракте: валидатор такие маршруты пропускает дальше, к роутеру
		specJSON, err := oapi.GetSpecJSON()
		if err != nil {
			return nil, fmt.Errorf("public: контракт: %w", err)
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
