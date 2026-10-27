package staff_test

import (
	"errors"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/staff"
)

// Задача письма staff.mail (§4.12): в аргументах только вид, id сущности и id получателя — без
// адресов и кодов; версия аргументов ставится конструктором; очередь — staff (исполняет admin-api).
func TestMailArgs(t *testing.T) {
	ref, to := uuid.New(), uuid.New()
	a := staff.NewMailArgs(staff.MailLocked, ref, to)
	if a.V != 1 || a.Mail != staff.MailLocked || a.Ref != ref || a.Recipient != to {
		t.Fatalf("аргументы %+v", a)
	}
	if a.Kind() != "staff.mail" {
		t.Fatalf("вид задачи %q", a.Kind())
	}
	opts := a.InsertOpts()
	if opts.Queue != queue.Staff || queue.Staff != "staff" || opts.MaxAttempts < 1 {
		t.Fatalf("очередь %+v", opts)
	}
	kinds := map[string]string{
		staff.MailInvite: "staff.invite", staff.MailAccessReset: "staff.access_reset", staff.MailLocked: "staff.locked",
		staff.MailBootstrapped: "staff.bootstrapped", staff.MailAdminDisabled: "staff.admin_disabled",
	}
	for got, want := range kinds {
		if got != want {
			t.Errorf("вид письма %q, ждали %q", got, want)
		}
	}
}

// Ошибки модуля знают свой код (§7); MFAError несёт attempts_left и разворачивается в причину —
// staff.mfa_invalid или staff.password_invalid.
func TestErrorsAndCodes(t *testing.T) {
	cases := map[error]string{
		staff.ErrInvalidCredentials: staff.CodeInvalidCredentials,
		staff.ErrMFAInvalid:         staff.CodeMFAInvalid,
		staff.ErrPasswordInvalid:    staff.CodePasswordInvalid,
		staff.ErrWrongStage:         staff.CodeWrongStage,
		staff.ErrInviteInvalid:      staff.CodeInviteInvalid,
		staff.ErrEmailTaken:         staff.CodeEmailTaken,
		staff.ErrNotFound:           staff.CodeNotFound,
		staff.ErrSessionNotFound:    staff.CodeSessionNotFound,
		staff.ErrCityNotFound:       staff.CodeCityNotFound,
		staff.ErrLastOwner:          staff.CodeLastOwner,
		staff.ErrSelfAction:         staff.CodeSelfAction,
	}
	for err, code := range cases {
		got, ok := staff.CodeOf(err)
		if !ok || got != code || !slices.Contains(staff.Codes, code) || !strings.HasPrefix(code, "staff.") {
			t.Errorf("%v: код %q (%v), ждали %q из Codes", err, got, ok, code)
		}
	}
	if len(staff.Codes) != len(cases) {
		t.Fatalf("кодов %d, ошибок %d", len(staff.Codes), len(cases))
	}
	e := error(&staff.MFAError{AttemptsLeft: 3, Err: staff.ErrPasswordInvalid})
	if !errors.Is(e, staff.ErrPasswordInvalid) || errors.Is(e, staff.ErrMFAInvalid) {
		t.Fatal("MFAError не разворачивается в причину")
	}
	if me, ok := errors.AsType[*staff.MFAError](e); !ok || me.AttemptsLeft != 3 {
		t.Fatal("attempts_left потерян")
	}
	if code, ok := staff.CodeOf(e); !ok || code != staff.CodePasswordInvalid {
		t.Fatalf("код MFAError %q", code)
	}
	if _, ok := staff.CodeOf(errors.New("сбой")); ok {
		t.Fatal("чужая ошибка получила код")
	}
}

// Лимит входа по почте (D7) — 429 ratelimit.exceeded с Retry-After: ошибка сама знает ответ.
func TestRateLimitError(t *testing.T) {
	err := error(&staff.RateLimitError{RetryAfter: 1500 * time.Millisecond})
	if !errors.Is(err, staff.ErrRateLimited) {
		t.Fatal("не ErrRateLimited")
	}
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !ok {
		t.Fatal("не httpx.ProblemError")
	}
	p := pe.Problem()
	if p.Status != http.StatusTooManyRequests || p.Code != httpx.CodeRateLimited || p.RetryAfter != 2 {
		t.Fatalf("problem %+v", p)
	}
}

// Повторная отправка приглашения админом раньше 60 с (§4.3 п.4) — 429 ratelimit.exceeded с
// Retry-After остатка паузы: ошибка сама знает ответ.
func TestResendTooSoonError(t *testing.T) {
	err := error(&staff.ResendTooSoonError{RetryAfter: 41200 * time.Millisecond})
	if !errors.Is(err, staff.ErrResendTooSoon) || errors.Is(err, staff.ErrRateLimited) {
		t.Fatal("не ErrResendTooSoon")
	}
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !ok {
		t.Fatal("не httpx.ProblemError")
	}
	if p := pe.Problem(); p.Status != http.StatusTooManyRequests || p.Code != httpx.CodeRateLimited || p.RetryAfter != 42 {
		t.Fatalf("problem %+v", p)
	}
	if p := (&staff.ResendTooSoonError{}).Problem(); p.RetryAfter != 1 {
		t.Fatalf("Retry-After меньше секунды: %d", p.RetryAfter)
	}
}

// P1: корень staff не импортирует internal/app, admin и jobs — иначе цикл (их пакеты импортируют
// корень).
func TestRootDoesNotImportInnerPackages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			rest, ok := strings.CutPrefix(path, "wf/backend/internal/staff/")
			if !ok {
				continue
			}
			for _, banned := range []string{"internal/app", "admin", "jobs"} {
				if rest == banned || strings.HasPrefix(rest, banned+"/") {
					t.Errorf("%s импортирует staff/%s — корень их не видит (P1)", f, rest)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("файлов корня не найдено — страж проверяет вхолостую")
	}
}
