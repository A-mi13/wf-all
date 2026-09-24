// Package httpx — общая HTTP-платформа для api и admin-api: одна реализация на оба бинарника.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

const ContentTypeProblem = "application/problem+json"

// Problem — ответ об ошибке по RFC 9457. Клиенты переводят текст по Code;
// Title — техническая сводка (http.StatusText), не для показа пользователю.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	w.Header().Set("Content-Type", ContentTypeProblem)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Code:      code,
		Detail:    detail,
		RequestID: middleware.GetReqID(r.Context()),
	})
}

// RequestErrorHandler — невалидный запрос в strict-сервере oapi-codegen.
func RequestErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	WriteProblem(w, r, http.StatusBadRequest, "request.invalid", err.Error())
}

// ResponseErrorHandler — хендлер вернул ошибку. Детали — в лог, клиенту — только код.
func ResponseErrorHandler(log *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		log.ErrorContext(r.Context(), "handler error", "err", err, "request_id", middleware.GetReqID(r.Context()))
		WriteProblem(w, r, http.StatusInternalServerError, "internal", "")
	}
}
