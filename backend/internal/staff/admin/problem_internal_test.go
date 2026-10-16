package admin

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/staff"
)

// wantProblemOf — Problem(err) — ProblemError со статусом, кодом и attempts_left (nil — поля нет).
func wantProblemOf(t *testing.T, err error, status int, code string, left *int) httpx.Problem {
	t.Helper()
	pe, ok := errors.AsType[httpx.ProblemError](Problem(err))
	if !ok {
		t.Fatalf("%v: не ProblemError", err)
	}
	p := pe.Problem()
	if p.Status != status || p.Code != code {
		t.Fatalf("%v: %d %s, ждали %d %s", err, p.Status, p.Code, status, code)
	}
	switch {
	case left == nil && p.AttemptsLeft != nil:
		t.Fatalf("%v: лишний attempts_left %d", err, *p.AttemptsLeft)
	case left != nil && (p.AttemptsLeft == nil || *p.AttemptsLeft != *left):
		t.Fatalf("%v: attempts_left %v, ждали %d", err, p.AttemptsLeft, *left)
	}
	return p
}

func ptr(n int) *int { return &n }

// Перевод ошибок сценариев в ответы (спека §7, таблица вводной Task 10a): коды модуля — со своими
// статусами, обёрнутые — так же; неудача второго фактора — с attempts_left (0 тоже); неверный текущий
// пароль — staff.password_invalid со счётчиком; сессия погашена или заменена — 401.
func TestProblemTable(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
		left   *int
	}{
		{staff.ErrInvalidCredentials, http.StatusBadRequest, staff.CodeInvalidCredentials, nil},
		{staff.ErrMFAInvalid, http.StatusBadRequest, staff.CodeMFAInvalid, nil},
		{staff.ErrPasswordInvalid, http.StatusBadRequest, staff.CodePasswordInvalid, nil},
		{staff.ErrWrongStage, http.StatusConflict, staff.CodeWrongStage, nil},
		{staff.ErrInviteInvalid, http.StatusBadRequest, staff.CodeInviteInvalid, nil},
		{staff.ErrEmailTaken, http.StatusConflict, staff.CodeEmailTaken, nil},
		{staff.ErrNotFound, http.StatusNotFound, staff.CodeNotFound, nil},
		{staff.ErrSessionNotFound, http.StatusNotFound, staff.CodeSessionNotFound, nil},
		{staff.ErrCityNotFound, http.StatusUnprocessableEntity, staff.CodeCityNotFound, nil},
		{staff.ErrLastOwner, http.StatusConflict, staff.CodeLastOwner, nil},
		{staff.ErrSelfAction, http.StatusConflict, staff.CodeSelfAction, nil},
		{fmt.Errorf("сценарий: %w", staff.ErrWrongStage), http.StatusConflict, staff.CodeWrongStage, nil},
		{&staff.MFAError{AttemptsLeft: 3, Err: staff.ErrMFAInvalid}, http.StatusBadRequest, staff.CodeMFAInvalid, ptr(3)},
		{&staff.MFAError{AttemptsLeft: 0, Err: staff.ErrMFAInvalid}, http.StatusBadRequest, staff.CodeMFAInvalid, ptr(0)},
		{&staff.MFAError{AttemptsLeft: 2, Err: staff.ErrPasswordInvalid}, http.StatusBadRequest, staff.CodePasswordInvalid, ptr(2)},
		{fmt.Errorf("обёртка: %w", &staff.MFAError{AttemptsLeft: 1, Err: staff.ErrPasswordInvalid}), http.StatusBadRequest, staff.CodePasswordInvalid, ptr(1)},
		{auth.ErrSessionInvalid, http.StatusUnauthorized, httpx.CodeUnauthenticated, nil},
		{fmt.Errorf("сценарий: %w", auth.ErrSessionInvalid), http.StatusUnauthorized, httpx.CodeUnauthenticated, nil},
		{auth.ErrSessionReplaced, http.StatusUnauthorized, httpx.CodeUnauthenticated, nil},
	}
	for _, c := range cases {
		wantProblemOf(t, c.err, c.status, c.code, c.left)
	}
}

// У каждого кода модуля есть статус: новый код без строки в таблице Problem ушёл бы клиенту 500.
func TestProblemCoversStaffCodes(t *testing.T) {
	if len(statusOf) != len(staff.Codes) {
		t.Fatalf("статусов %d, кодов %d", len(statusOf), len(staff.Codes))
	}
	for _, c := range staff.Codes {
		if _, ok := statusOf[c]; !ok {
			t.Errorf("код %s без статуса", c)
		}
	}
}

// ProblemError (поле, 403, лимит, пауза повторной отправки) — как есть, обёрнутая тоже: у неё свой
// ответ (Retry-After, поле). Прочее — как есть (WriteError ответит 500 и запишет причину в лог), nil — nil.
func TestProblemPassThrough(t *testing.T) {
	field := httpx.NewFieldError("body.new_password", "too_short")
	if got := Problem(field); !errors.Is(got, field) {
		t.Fatalf("поле: %v", got)
	}
	p := wantProblemOf(t, fmt.Errorf("x: %w", field), http.StatusBadRequest, httpx.CodeValidationFailed, nil)
	if !slices.Equal(p.Errors, []httpx.FieldError{{Field: "body.new_password", Code: "too_short"}}) {
		t.Fatalf("поля: %v", p.Errors)
	}
	p = wantProblemOf(t, &staff.ResendTooSoonError{RetryAfter: 1500 * time.Millisecond}, http.StatusTooManyRequests, httpx.CodeRateLimited, nil)
	if p.RetryAfter != 2 {
		t.Fatalf("Retry-After %d", p.RetryAfter)
	}
	wantProblemOf(t, &staff.RateLimitError{RetryAfter: time.Minute}, http.StatusTooManyRequests, httpx.CodeRateLimited, nil)
	wantProblemOf(t, httpx.NewError(http.StatusForbidden, httpx.CodeForbidden), http.StatusForbidden, httpx.CodeForbidden, nil)

	other := errors.New("pgx: сломалось")
	if got := Problem(other); !errors.Is(got, other) {
		t.Fatalf("прочее: %v", got)
	}
	if Problem(nil) != nil {
		t.Fatal("nil")
	}
}
