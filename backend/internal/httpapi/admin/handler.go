package admin

import (
	"log/slog"
	"net/http"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
)

// NewHandler собирает API админки на общей HTTP-платформе.
func NewHandler(log *slog.Logger) http.Handler {
	strict := oapi.NewStrictHandlerWithOptions(Server{}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httpx.RequestErrorHandler,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	return oapi.HandlerFromMux(strict, httpx.NewRouter(log))
}
