package staff

import (
	"errors"
	"math"
	"net/http"
	"time"

	"wf/backend/internal/platform/httpx"
)

// Error — бизнес-ошибка модуля с кодом клиента (§7). Сравнивать — errors.Is с переменными Err*.
type Error struct{ code string }

func (e *Error) Error() string { return e.code }

// Code — код ошибки для клиента (staff.<ошибка>).
func (e *Error) Code() string { return e.code }

// Ошибки сценариев staff. Все отказы пароля при входе (нет почты, неверный пароль, учётка не
// active, без 2FA, закрыта) — одна ErrInvalidCredentials: существование учётки не раскрывается.
var (
	ErrInvalidCredentials = &Error{CodeInvalidCredentials}
	ErrMFAInvalid         = &Error{CodeMFAInvalid}
	ErrPasswordInvalid    = &Error{CodePasswordInvalid}
	ErrWrongStage         = &Error{CodeWrongStage}
	ErrInviteInvalid      = &Error{CodeInviteInvalid}
	ErrEmailTaken         = &Error{CodeEmailTaken}
	ErrNotFound           = &Error{CodeNotFound}
	ErrSessionNotFound    = &Error{CodeSessionNotFound}
	ErrCityNotFound       = &Error{CodeCityNotFound}
	ErrLastOwner          = &Error{CodeLastOwner}
	ErrSelfAction         = &Error{CodeSelfAction}
)

// MFAError — неудача второго фактора (шаг mfa, unlock, TOTP в «Моём доступе») или неверный текущий
// пароль в /me/password: Err — ErrMFAInvalid или ErrPasswordInvalid, AttemptsLeft — сколько неудач
// подряд осталось до блокировки (0 — учётка закрыта на час).
type MFAError struct {
	AttemptsLeft int
	Err          error
}

func (e *MFAError) Error() string { return e.Err.Error() }

func (e *MFAError) Unwrap() error { return e.Err }

// CodeOf — код клиента для ошибки модуля (в том числе обёрнутой и MFAError); false — не ошибка
// модуля.
func CodeOf(err error) (string, bool) {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.code, true
	}
	return "", false
}

// ErrRateLimited — сценарий упёрся в свой лимит (вход по почте, D7). Ответ — RateLimitError.
var ErrRateLimited = errors.New("staff: лимит исчерпан")

// RateLimitError — 429 ratelimit.exceeded с Retry-After: лимит, который сценарий ведёт сам через
// ratelimit.Limiter (почта входа). Сама знает ответ клиенту (httpx.ProblemError).
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return ErrRateLimited.Error() }

func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// Problem — 429 ratelimit.exceeded; Retry-After — в целых секундах вверх, не меньше 1.
func (e *RateLimitError) Problem() httpx.Problem { return tooManyRequests(e.RetryAfter) }

// ErrResendTooSoon — повторная отправка приглашения админом раньше паузы 60 с (§4.3 п.4). Ответ —
// ResendTooSoonError.
var ErrResendTooSoon = errors.New("staff: повторная отправка раньше паузы")

// ResendTooSoonError — 429 ratelimit.exceeded с Retry-After: остаток паузы повторной отправки
// приглашения (контракт resendStaffInvite). Сама знает ответ клиенту (httpx.ProblemError).
type ResendTooSoonError struct{ RetryAfter time.Duration }

func (e *ResendTooSoonError) Error() string { return ErrResendTooSoon.Error() }

func (e *ResendTooSoonError) Unwrap() error { return ErrResendTooSoon }

// Problem — 429 ratelimit.exceeded; Retry-After — в целых секундах вверх, не меньше 1.
func (e *ResendTooSoonError) Problem() httpx.Problem { return tooManyRequests(e.RetryAfter) }

// tooManyRequests — 429 ratelimit.exceeded с Retry-After в целых секундах вверх, не меньше 1.
func tooManyRequests(retry time.Duration) httpx.Problem {
	return httpx.Problem{
		Status: http.StatusTooManyRequests, Code: httpx.CodeRateLimited,
		RetryAfter: max(1, int(math.Ceil(retry.Seconds()))),
	}
}
