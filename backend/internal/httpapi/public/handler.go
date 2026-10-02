package public

import (
	"fmt"
	"log/slog"
	"net/http"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/apidocs"
	"wf/backend/internal/platform/httpx"
)

// NewHandler собирает публичный API на общей HTTP-платформе: лимит тела и проверка
// запроса по контракту стоят до strict-хендлера.
// Options — то, что включается конфигом бинарника.
type Options struct {
	// Docs — Swagger UI и контракт на /docs (config.HTTP.DocsEnabled).
	Docs bool
}

func NewHandler(log *slog.Logger, opts Options) (http.Handler, error) {
	spec, err := oapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("public: контракт: %w", err)
	}
	title := spec.Info.Title
	routes, err := httpx.Routes(spec)
	if err != nil {
		return nil, err
	}
	// аутентификация (Task 9) встанет между routes и validate; пока Principal нет ни у кого —
	// операции с security отвечают 401
	validate := httpx.ValidateRequests(httpx.ValidateOptions{WWWAuthenticate: "Bearer"})
	requestErr := httpx.RequestErrorHandler(log)
	strict := oapi.NewStrictHandlerWithOptions(Server{}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  requestErr,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	// ErrorHandlerFunc — ошибки биндинга параметров в chi-обёртке; по умолчанию oapi-codegen
	// отвечает text/plain с текстом ошибки
	// порядок — спека §6.1; полный конвейер собирается в Task 19
	router := httpx.NewRouter(log, httpx.LimitBody(httpx.MaxBodyBytes), routes, validate)
	if opts.Docs {
		// /docs не в контракте: валидатор такие маршруты пропускает дальше, к роутеру
		specJSON, err := oapi.GetSpecJSON()
		if err != nil {
			return nil, fmt.Errorf("public: контракт: %w", err)
		}
		apidocs.Mount(router, title, specJSON)
	}
	return oapi.HandlerWithOptions(strict, oapi.ChiServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: requestErr,
	}), nil
}
