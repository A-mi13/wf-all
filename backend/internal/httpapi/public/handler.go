package public

import (
	"fmt"
	"log/slog"
	"net/http"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
)

// NewHandler собирает публичный API на общей HTTP-платформе: лимит тела и проверка
// запроса по контракту стоят до strict-хендлера.
func NewHandler(log *slog.Logger) (http.Handler, error) {
	spec, err := oapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("public: контракт: %w", err)
	}
	validate, err := httpx.ValidateRequests(spec)
	if err != nil {
		return nil, err
	}
	strict := oapi.NewStrictHandlerWithOptions(Server{}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httpx.RequestErrorHandler,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	return oapi.HandlerFromMux(strict, httpx.NewRouter(log, httpx.LimitBody(httpx.MaxBodyBytes), validate)), nil
}
