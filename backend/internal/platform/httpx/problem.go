// Package httpx — общая HTTP-платформа для api и admin-api: одна реализация на оба бинарника.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

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
	// Challenge — задача антибота у humancheck.required (протокол ALTCHA): клиент решает её и
	// повторяет запрос с решением в заголовке X-WF-Humancheck.
	Challenge any `json:"challenge,omitempty"`
	// RetryAfter — через сколько секунд повторить (ratelimit.exceeded); уходит заголовком
	// Retry-After, не в теле.
	RetryAfter int `json:"-"`
}

func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	writeProblem(w, r, Problem{Status: status, Code: code, Detail: detail})
}

// WriteValidationProblem — 400 validation.failed со списком нарушенных полей.
func WriteValidationProblem(w http.ResponseWriter, r *http.Request, errs []FieldError) {
	writeProblem(w, r, Problem{Status: http.StatusBadRequest, Code: CodeValidationFailed, Errors: errs})
}

// WriteProblemValue — готовая Problem: с задачей антибота, Retry-After.
func WriteProblemValue(w http.ResponseWriter, r *http.Request, p Problem) {
	writeProblem(w, r, p)
}

// ProblemError — ошибка, которая сама знает ответ клиенту: rate limit, антибот,
// идемпотентность, выключенная функция. Хендлер или механизм платформы возвращает её как
// обычную ошибку (можно обёрнутой через %w) — WriteError отвечает её Problem, а не 500.
type ProblemError interface {
	error
	Problem() Problem
}

// Error — ProblemError по статусу и коду.
type Error struct{ p Problem }

// NewError — ProblemError с кодом из codes.go (литерал ловит страж TestProblemCodesAreConstants).
func NewError(status int, code string) *Error {
	return &Error{p: Problem{Status: status, Code: code}}
}

// NewFieldError — 400 validation.failed с одним полем: проверка хендлера, которую схема контракта
// не выражает (q из одних знаков после нормализации) или которую хендлер дублирует явно (NaN в
// координате). Ответ тот же, что у валидатора: field — query.<имя>, path.<имя>, body.<путь>;
// code — правило (type, minLength, maximum…).
func NewFieldError(field, code string) *Error {
	return &Error{p: Problem{Status: http.StatusBadRequest, Code: CodeValidationFailed,
		Errors: []FieldError{{Field: field, Code: code}}}}
}

func (e *Error) Error() string { return "httpx: " + e.p.Code }

func (e *Error) Problem() Problem { return e.p }

// WriteError — ответ на ошибку хендлера или middleware: ProblemError — её Problem (в лог не
// пишется: это ожидаемый ответ), иное — 500 internal, причина — только в лог.
func WriteError(log *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	if pe, ok := errors.AsType[ProblemError](err); ok {
		writeProblem(w, r, pe.Problem())
		return
	}
	log.ErrorContext(r.Context(), "handler error", "err", err, "request_id", middleware.GetReqID(r.Context()))
	WriteProblem(w, r, http.StatusInternalServerError, CodeInternal, "")
}

func writeProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	p.Type = "about:blank"
	p.Title = http.StatusText(p.Status)
	p.RequestID = middleware.GetReqID(r.Context())
	if p.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(p.RetryAfter))
	}
	w.Header().Set("Content-Type", ContentTypeProblem)
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// RequestErrorHandler — запрос не разобрал сгенерированный код oapi-codegen (биндинг
// параметров в chi-обёртке, декодирование тела в strict-сервере). Обычно до него не доходит:
// ValidateRequests отвечает раньше. Текст ошибки раскрывает устройство сервера — он в лог,
// клиенту — только код.
func RequestErrorHandler(log *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		log.InfoContext(r.Context(), "request error", "err", err, "request_id", middleware.GetReqID(r.Context()))
		WriteProblem(w, r, http.StatusBadRequest, CodeRequestInvalid, "")
	}
}

// ResponseErrorHandler — хендлер вернул ошибку: WriteError.
func ResponseErrorHandler(log *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) { WriteError(log, w, r, err) }
}
