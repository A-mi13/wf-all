package admin

import (
	"fmt"
	"log/slog"
	"net/http"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
)

// NewHandler собирает API админки на общей HTTP-платформе: лимит тела и проверка
// запроса по контракту стоят до strict-хендлера.
func NewHandler(log *slog.Logger) (http.Handler, error) {
	spec, err := oapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("admin: контракт: %w", err)
	}
	validate, err := httpx.ValidateRequests(spec)
	if err != nil {
		return nil, err
	}
	requestErr := httpx.RequestErrorHandler(log)
	strict := oapi.NewStrictHandlerWithOptions(Server{}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  requestErr,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	// ErrorHandlerFunc — ошибки биндинга параметров в chi-обёртке; по умолчанию oapi-codegen
	// отвечает text/plain с текстом ошибки
	return oapi.HandlerWithOptions(strict, oapi.ChiServerOptions{
		BaseRouter:       httpx.NewRouter(log, httpx.LimitBody(httpx.MaxBodyBytes), validate),
		ErrorHandlerFunc: requestErr,
	}), nil
}
