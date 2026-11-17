package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"wf/backend/internal/platform/httpx"
)

// problemOf — статус, код и поля ProblemError (nil — не ProblemError).
func problemOf(t *testing.T, err error) httpx.Problem {
	t.Helper()
	var pe httpx.ProblemError
	if !errors.As(err, &pe) {
		t.Fatalf("ошибка %v — не ProblemError", err)
	}
	return pe.Problem()
}

func wantField(t *testing.T, name string, err error, status int, field string) {
	t.Helper()
	p := problemOf(t, err)
	if p.Status != status || p.Code != httpx.CodeValidationFailed || len(p.Errors) != 1 || p.Errors[0].Field != field {
		t.Errorf("%s: %+v, ждали %d validation.failed по %s", name, p, status, field)
	}
}

// Причина доступа (контракт §7.1, план I-1 R19): percent-encoding UTF-8; битая кодировка — 400, после декодирования
// 3–200 символов (руны, не байты) — иначе 422; поле — header.X-WF-Access-Reason.
func TestDecodeAccessReason(t *testing.T) {
	t.Parallel()
	const field = "header.X-WF-Access-Reason"
	ok := map[string]string{
		"%D0%BE%D0%B1%D1%80%D0%B0%D1%89%D0%B5%D0%BD%D0%B8%D0%B5%204812": "обращение 4812",
		"abc":                         "abc",
		"%D1%8F%D1%8F%D1%8F":          "яяя", // 3 руны, 6 байт
		strings.Repeat("%D1%8F", 200): strings.Repeat("я", 200),
		"ticket-42%3A%20%D0%BF%D0%BE%D1%87%D1%82%D0%B0": "ticket-42: почта",
		"cafe%CC%81": "café", // комбинирующий знак при букве — видимый текст
	}
	for raw, want := range ok {
		if got, err := decodeAccessReason(raw); err != nil || got != want {
			t.Errorf("%q → %q %v, ждали %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"%D0%", "%ZZabc", "abc%", "%FF%FE%FD"} {
		_, err := decodeAccessReason(raw)
		wantField(t, raw, err, http.StatusBadRequest, field)
	}
	for _, raw := range []string{"", "ab", "%D1%8F%D1%8F", strings.Repeat("%D1%8F", 201), "%20%20%20"} {
		_, err := decodeAccessReason(raw)
		wantField(t, raw, err, http.StatusUnprocessableEntity, field)
	}
	// управляющие и невидимые символы (I1 ревью W1): NUL в audit_log.reason — 500 Postgres, ZWSP — пустая на вид причина
	for _, raw := range []string{"%00%00%00", "abc%0Adef", "abc%1B", "%E2%80%8B%E2%80%8B%E2%80%8B", "ab%E2%80%8B",
		"abc%C2%85", "%E2%80%AEabc", "abc%EF%BB%BF",
		// m2 итогового ревью Б: разделители строки и абзаца (Zl, Zp), строка из одних комбинирующих знаков (Mn, Me)
		"abc%E2%80%A8def", "abc%E2%80%A9def", "%CC%81%CC%81%CC%81", "%20%CC%81%CC%81%CC%81%20", "%E2%83%9D%E2%83%9D%E2%83%9D"} {
		_, err := decodeAccessReason(raw)
		p := problemOf(t, err)
		if p.Status != http.StatusUnprocessableEntity || len(p.Errors) != 1 || p.Errors[0].Field != field ||
			p.Errors[0].Code != "format" {
			t.Errorf("%s: %+v, ждали 422 format по %s", raw, p, field)
		}
	}
}

// Почта поиска: percent-encoding UTF-8; битая кодировка — 400; после декодирования длиннее 254 байт или не адрес
// (emailaddr.Normalize) — 422 по header.X-WF-Lookup-Email; годная — каноническая форма.
func TestDecodeLookupEmail(t *testing.T) {
	t.Parallel()
	const field = "header.X-WF-Lookup-Email"
	ok := map[string]string{
		"ivan.petrov%40mail.example":                         "ivan.petrov@mail.example",
		"Ivan.Petrov%40Mail.Example":                         "ivan.petrov@mail.example",
		"ivan%40%D0%BF%D0%BE%D1%87%D1%82%D0%B0.%D1%80%D1%84": "ivan@xn--80a1acny.xn--p1ai",
	}
	for raw, want := range ok {
		if got, err := decodeLookupEmail(raw); err != nil || got != want {
			t.Errorf("%q → %q %v, ждали %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"ivan%40mail%2", "%E0%A4%A"} {
		_, err := decodeLookupEmail(raw)
		wantField(t, raw, err, http.StatusBadRequest, field)
	}
	long := strings.Repeat("a", 64) + "%40" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." +
		strings.Repeat("d", 63) + ".example" // 64 + 1 + 200 байт — длиннее 254
	for _, raw := range []string{"ivan.petrov", "ivan%40", "%40mail.example", "a%20b%40mail.example", long, "",
		"ivan%00%40mail.example", "iv%E2%80%8Ban%40mail.example", "ivan%40ma%E2%80%8Bil.example", "ivan%40mail.example%0A"} {
		_, err := decodeLookupEmail(raw)
		wantField(t, raw, err, http.StatusUnprocessableEntity, field)
	}
}
