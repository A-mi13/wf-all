package public

import (
	"log/slog"
	"net/http"

	"wf/backend/internal/platform/httpx"
)

// NewHandler собирает публичный API на общей HTTP-платформе.
func NewHandler(log *slog.Logger) http.Handler {
	strict := NewStrictHandlerWithOptions(Server{}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httpx.RequestErrorHandler,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	return HandlerFromMux(strict, httpx.NewRouter(log))
}
