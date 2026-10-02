// Package humancheck — проверка «человек ли» (спека бэкенда §6.7): за интерфейсом Verifier.
// Реализация по умолчанию — PoW по протоколу ALTCHA (PoW); сторонний провайдер (Turnstile,
// SmartCaptcha, hCaptcha) — ещё одна реализация, включается флагом по стране. Когда требовать
// проверку, решает модуль по сигналам пакета risk.
package humancheck

import (
	"context"
	"errors"
	"net/http"

	"wf/backend/internal/platform/httpx"
)

// Header — решение задачи; клиент повторяет запрос с ним после 403 humancheck.required.
const Header = "X-WF-Humancheck"

// ErrFailed — решение неверное, просрочено, подписано неизвестным ключом или уже предъявлено.
var ErrFailed = errors.New("humancheck: решение не принято")

// Challenge — задача ALTCHA, уходит клиенту как есть (поля протокола в camelCase).
type Challenge struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	MaxNumber int64  `json:"maxNumber"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
}

type Verifier interface {
	NewChallenge(ctx context.Context) (Challenge, error)
	// Verify — ErrFailed для любого непринятого решения; иная ошибка — сбой (база).
	Verify(ctx context.Context, solution string) error
}

type solutionKey struct{}

func WithSolution(ctx context.Context, s string) context.Context {
	return context.WithValue(ctx, solutionKey{}, s)
}

// Middleware — решение из заголовка в ctx: сценарий модуля проверит его через Require, если
// риск потребует; без требования решение не тратится.
func Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s := r.Header.Get(Header); s != "" {
				r = r.WithContext(WithSolution(r.Context(), s))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequiredError — 403 humancheck.required с новой задачей.
type RequiredError struct {
	Challenge Challenge
}

func (e *RequiredError) Error() string { return "humancheck: нужна проверка" }

func (e *RequiredError) Problem() httpx.Problem {
	return httpx.Problem{Status: http.StatusForbidden, Code: httpx.CodeHumancheckRequired, Challenge: e.Challenge}
}

// Require — проверка там, где её потребовал риск: решение из ctx принято — nil; решения нет
// или оно не принято — *RequiredError с новой задачей; сбой проверки — ошибка как есть.
func Require(ctx context.Context, v Verifier) error {
	if s, _ := ctx.Value(solutionKey{}).(string); s != "" {
		err := v.Verify(ctx, s)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrFailed) {
			return err
		}
	}
	ch, err := v.NewChallenge(ctx)
	if err != nil {
		return err
	}
	return &RequiredError{Challenge: ch}
}
