package admin

import (
	"errors"
	"net/http"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/staff"
)

// statusOf — HTTP-статус кода модуля (спека сотрудников §7). Новый код staff без строки здесь ушёл
// бы клиенту 500 — страж TestProblemCoversStaffCodes.
var statusOf = map[string]int{
	staff.CodeInvalidCredentials: http.StatusBadRequest,
	staff.CodeMFAInvalid:         http.StatusBadRequest,
	staff.CodePasswordInvalid:    http.StatusBadRequest,
	staff.CodeWrongStage:         http.StatusConflict,
	staff.CodeInviteInvalid:      http.StatusBadRequest,
	staff.CodeEmailTaken:         http.StatusConflict,
	staff.CodeNotFound:           http.StatusNotFound,
	staff.CodeSessionNotFound:    http.StatusNotFound,
	staff.CodeCityNotFound:       http.StatusUnprocessableEntity,
	staff.CodeLastOwner:          http.StatusConflict,
	staff.CodeSelfAction:         http.StatusConflict,
}

var errUnauthenticated = httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthenticated)

// Problem — ошибка сценария staff как ответ клиенту (единственное место перевода; операции тега staff
// получают его в Middleware, операции подтверждений 10b зовут сами):
//   - httpx.ProblemError (поле validation.failed, 403, 429 с Retry-After — RateLimitError,
//     ResendTooSoonError) — как есть, обёрнутая тоже;
//   - *staff.MFAError — 400 staff.mfa_invalid или staff.password_invalid (неверный текущий пароль) с
//     attempts_left (0 — вход закрыт);
//   - ошибка модуля (staff.CodeOf, обёрнутая тоже) — её код со статусом §7;
//   - auth.ErrSessionInvalid и auth.ErrSessionReplaced (сессию погасили или заменили между конвейером
//     и замком сценария) — 401 auth.unauthenticated; стирать ли cookie, решает Middleware;
//   - прочее — как есть: WriteError ответит 500 internal, причина — только в лог.
func Problem(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[httpx.ProblemError](err); ok {
		return err
	}
	if errors.Is(err, auth.ErrSessionInvalid) || errors.Is(err, auth.ErrSessionReplaced) {
		return errUnauthenticated
	}
	if me, ok := errors.AsType[*staff.MFAError](err); ok {
		code := staff.CodeMFAInvalid
		if errors.Is(me.Err, staff.ErrPasswordInvalid) {
			code = staff.CodePasswordInvalid
		}
		return httpx.NewError(http.StatusBadRequest, code).WithAttemptsLeft(me.AttemptsLeft)
	}
	if code, ok := staff.CodeOf(err); ok {
		if status, ok := statusOf[code]; ok {
			return httpx.NewError(status, code)
		}
	}
	return err
}
