package httpx_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	httpx.WriteError(log, rec, httptest.NewRequest(http.MethodPost, "/x", nil), err)
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
	httpx.WriteError(log, rec, httptest.NewRequest(http.MethodGet, "/x", nil), errors.New("pgx: сломалось"))
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
	handle(rec, httptest.NewRequest(http.MethodPost, "/x", nil),
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
	handle(rec, httptest.NewRequest(http.MethodGet, "/x", nil), errors.New("pgx: сломалось"))
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
	httpx.WriteProblemValue(rec, httptest.NewRequest(http.MethodGet, "/x", nil), httpx.Problem{
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
	httpx.WriteProblemValue(rec, httptest.NewRequest(http.MethodGet, "/x", nil), httpx.Problem{
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
