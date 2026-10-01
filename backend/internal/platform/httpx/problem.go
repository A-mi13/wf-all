// Package httpx — общая HTTP-платформа для api и admin-api: одна реализация на оба бинарника.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

const ContentTypeProblem = "application/problem+json"

// FieldError — нарушение схемы в одном поле запроса (у validation.failed).
// Field — body.<путь через точку>, query.<имя>, path.<имя>, header.<имя>;
// Code — правило схемы: required, minLength, maximum, enum, format, type, pattern…
type FieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// Problem — ответ об ошибке по RFC 9457. Клиенты переводят текст по Code;
// Title — техническая сводка (http.StatusText), не для показа пользователю.
type Problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Code      string       `json:"code"`
	Detail    string       `json:"detail,omitempty"`
	RequestID string       `json:"request_id,omitempty"`
	Errors    []FieldError `json:"errors,omitempty"`
}

func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	writeProblem(w, r, Problem{Status: status, Code: code, Detail: detail})
}

// WriteValidationProblem — 400 validation.failed со списком нарушенных полей.
func WriteValidationProblem(w http.ResponseWriter, r *http.Request, errs []FieldError) {
	writeProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "validation.failed", Errors: errs})
}

func writeProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	p.Type = "about:blank"
	p.Title = http.StatusText(p.Status)
	p.RequestID = middleware.GetReqID(r.Context())
	w.Header().Set("Content-Type", ContentTypeProblem)
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
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
