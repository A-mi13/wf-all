package httpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"wf/backend/internal/platform/httpx"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != httpx.ContentTypeProblem {
		t.Fatalf("Content-Type = %q", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Ошибка механизма платформы, даже обёрнутая, отвечает своей Problem и не пишется в лог как сбой.
func TestWriteErrorProblemError(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	err := fmt.Errorf("teams: создать: %w", httpx.NewError(http.StatusConflict, httpx.CodeIdempotencyInProgress))
	rec := httptest.NewRecorder()
	httpx.WriteError(log, rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil), err)
	if rec.Code != http.StatusConflict {
		t.Fatalf("статус = %d", rec.Code)
	}
	if m := decode(t, rec); m["code"] != httpx.CodeIdempotencyInProgress || m["status"] != float64(409) {
		t.Fatalf("тело: %v", m)
	}
	if logs.Len() != 0 {
		t.Fatalf("ожидаемый ответ попал в лог: %s", logs.String())
	}
}

func TestWriteErrorUnknownIsInternal(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	rec := httptest.NewRecorder()
	httpx.WriteError(log, rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil), errors.New("pgx: сломалось"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("статус = %d", rec.Code)
	}
	m := decode(t, rec)
	if m["code"] != httpx.CodeInternal || strings.Contains(rec.Body.String(), "pgx") {
		t.Fatalf("тело раскрывает причину или код не тот: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "pgx: сломалось") {
		t.Fatalf("причины нет в логе: %s", logs.String())
	}
}

// Главный путь прода: strict-хендлер вернул ошибку, oapi-codegen зовёт ResponseErrorHandler.
// ProblemError (обёрнутая) отвечает своей Problem без записи в лог, иное — 500 internal с
// причиной только в логе.
func TestResponseErrorHandler(t *testing.T) {
	var logs bytes.Buffer
	handle := httpx.ResponseErrorHandler(slog.New(slog.NewTextHandler(&logs, nil)))

	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil),
		fmt.Errorf("x: %w", httpx.NewError(http.StatusForbidden, httpx.CodeFeatureDisabled)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("статус = %d", rec.Code)
	}
	if m := decode(t, rec); m["code"] != httpx.CodeFeatureDisabled || m["status"] != float64(403) {
		t.Fatalf("тело: %v", m)
	}
	if logs.Len() != 0 {
		t.Fatalf("ожидаемый ответ попал в лог: %s", logs.String())
	}

	rec = httptest.NewRecorder()
	handle(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil), errors.New("pgx: сломалось"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("статус = %d", rec.Code)
	}
	if m := decode(t, rec); m["code"] != httpx.CodeInternal || strings.Contains(rec.Body.String(), "pgx") {
		t.Fatalf("тело раскрывает причину или код не тот: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "pgx: сломалось") {
		t.Fatalf("причины нет в логе: %s", logs.String())
	}
}

// Retry-After — заголовком, не полем тела; задача антибота — в теле.
func TestWriteProblemValueRetryAfterAndChallenge(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteProblemValue(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil), httpx.Problem{
		Status: http.StatusTooManyRequests, Code: httpx.CodeRateLimited, RetryAfter: 7,
	})
	if got := rec.Header().Get("Retry-After"); got != "7" {
		t.Fatalf("Retry-After = %q", got)
	}
	m := decode(t, rec)
	for _, k := range []string{"retry_after", "RetryAfter", "challenge"} {
		if _, ok := m[k]; ok {
			t.Fatalf("лишнее поле %s: %v", k, m)
		}
	}

	rec = httptest.NewRecorder()
	httpx.WriteProblemValue(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil), httpx.Problem{
		Status: http.StatusForbidden, Code: httpx.CodeHumancheckRequired, Challenge: map[string]any{"salt": "s"},
	})
	m = decode(t, rec)
	if c, ok := m["challenge"].(map[string]any); !ok || c["salt"] != "s" {
		t.Fatalf("challenge: %v", m)
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatal("Retry-After без RetryAfter")
	}
}

// Проверка хендлера сверх схемы (q из одних знаков, явная проверка NaN) — тот же ответ, что у
// валидатора: 400 validation.failed с полем; обёрнутая ошибка не становится 500 и не пишется в лог.
func TestNewFieldError(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	err := fmt.Errorf("geo: список: %w", httpx.NewFieldError("query.q", "minLength"))
	rec := httptest.NewRecorder()
	httpx.WriteError(log, rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil), err)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("статус = %d", rec.Code)
	}
	m := decode(t, rec)
	want := []any{map[string]any{"field": "query.q", "code": "minLength"}}
	if m["code"] != httpx.CodeValidationFailed || !reflect.DeepEqual(m["errors"], want) {
		t.Fatalf("тело: %v", m)
	}
	if logs.Len() != 0 {
		t.Fatalf("ожидаемый ответ попал в лог: %s", logs.String())
	}
}

// attempts_left (неверный код или пароль, спека сотрудников §7) — полем тела; 0 после последней
// неудачи тоже уходит (иначе клиент не узнал бы о закрытии входа). WithAttemptsLeft не трогает
// исходную ошибку: общие переменные Err… не должны получать чужой счётчик. Без него поля нет.
func TestErrorWithAttemptsLeft(t *testing.T) {
	base := httpx.NewError(http.StatusBadRequest, httpx.CodeValidationFailed)
	for _, n := range []int{3, 0} {
		rec := httptest.NewRecorder()
		httpx.WriteError(slog.New(slog.DiscardHandler), rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil),
			fmt.Errorf("обёртка: %w", base.WithAttemptsLeft(n)))
		if got, ok := decode(t, rec)["attempts_left"]; !ok || got != float64(n) {
			t.Fatalf("attempts_left = %v (%v), ждали %d", got, ok, n)
		}
	}
	rec := httptest.NewRecorder()
	httpx.WriteError(slog.New(slog.DiscardHandler), rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil), base)
	if _, ok := decode(t, rec)["attempts_left"]; ok {
		t.Fatal("attempts_left у ошибки без счётчика — WithAttemptsLeft изменил исходную")
	}
}

// R26 (ревью G4): 409 geo.import_in_progress несёт job_id существующей задачи — поле Problem job_id (int64, 0 тоже
// уходит); WithJobID исходную ошибку не трогает, без него поля нет (прочие ответы не меняются, omitempty).
func TestErrorWithJobID(t *testing.T) {
	base := httpx.NewError(http.StatusConflict, httpx.CodeIdempotencyInProgress)
	for _, id := range []int64{1842, 0, 1 << 62} {
		rec := httptest.NewRecorder()
		httpx.WriteError(slog.New(slog.DiscardHandler), rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil),
			fmt.Errorf("обёртка: %w", base.WithJobID(id)))
		var m map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusConflict || string(m["job_id"]) != fmt.Sprint(id) {
			t.Fatalf("job_id = %s, ждали %d (%d)", m["job_id"], id, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	httpx.WriteError(slog.New(slog.DiscardHandler), rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil), base)
	if _, ok := decode(t, rec)["job_id"]; ok {
		t.Fatal("job_id у ошибки без задачи — WithJobID изменил исходную")
	}
}

// Все нарушения сразу — 400 validation.failed со списком полей в исходном порядке.
func TestNewFieldErrors(t *testing.T) {
	errs := []httpx.FieldError{{Field: "body.recommended_version", Code: "minimum"}, {Field: "body.store_url", Code: "host"}}
	rec := httptest.NewRecorder()
	httpx.WriteError(slog.New(slog.DiscardHandler), rec, httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/x", nil),
		httpx.NewFieldErrors(errs))
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || rec.Code != http.StatusBadRequest ||
		p.Code != httpx.CodeValidationFailed || !reflect.DeepEqual(p.Errors, errs) {
		t.Fatalf("%d %s %v", rec.Code, rec.Body.String(), err)
	}
}
