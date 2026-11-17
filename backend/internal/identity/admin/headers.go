package admin

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"wf/backend/internal/platform/emailaddr"
	"wf/backend/internal/platform/httpx"
)

// Заголовки ПД админки identity (контракт admin §7.1, план I-1 R19): причина доступа X-WF-Access-Reason и почта поиска
// X-WF-Lookup-Email — UTF-8 в percent-encoding (encodeURIComponent; символы вне алфавита отсекает pattern валидатора).
// Битая кодировка (обрыв «%», не hex, не UTF-8 после декодирования) — 400 validation.failed по полю заголовка;
// значение после декодирования вне правил — 422 validation.failed по тому же полю.

const (
	fieldAccessReason = "header.X-WF-Access-Reason"
	fieldLookupEmail  = "header.X-WF-Lookup-Email"
	minAccessReason   = 3   // символов (рун) после декодирования и обрезки пробелов
	maxAccessReason   = 200 // символов
	maxLookupEmail    = 254 // байт (как emailaddr)
)

// headerProblem — validation.failed по полю заголовка со статусом 400 или 422: у httpx.NewFieldError статус всегда 400.
type headerProblem struct {
	status      int
	field, rule string
}

func (e headerProblem) Error() string {
	return "httpx: " + httpx.CodeValidationFailed + ": " + e.field + " " + e.rule
}

func (e headerProblem) Problem() httpx.Problem {
	return httpx.Problem{Status: e.status, Code: httpx.CodeValidationFailed,
		Errors: []httpx.FieldError{{Field: e.field, Code: e.rule}}}
}

// decodeHeader — percent-decoding значения заголовка; битая кодировка — 400 по field.
func decodeHeader(raw, field string) (string, error) {
	s, err := url.PathUnescape(raw)
	if err != nil || !utf8.ValidString(s) {
		return "", headerProblem{status: http.StatusBadRequest, field: field, rule: "format"}
	}
	return s, nil
}

// visibleText — нет управляющих (C0, C1, DEL, перевод строки) и невидимых символов (emailaddr.IsInvisible: Cf, ZWSP,
// bidi, BOM, заполнители): NUL Postgres в text не примет (500), невидимые дают пустую на вид запись журнала (I1 ревью
// W1). Иначе — 422 format по field.
func visibleText(s, field string) error {
	for _, r := range s {
		if unicode.IsControl(r) || emailaddr.IsInvisible(r) {
			return headerProblem{status: http.StatusUnprocessableEntity, field: field, rule: "format"}
		}
	}
	return nil
}

// decodeAccessReason — причина доступа к ПД: после декодирования без управляющих и невидимых символов, без крайних
// пробелов 3–200 символов, иначе 422. Причина уходит только в audit_log.reason (R10), не в логи.
func decodeAccessReason(raw string) (string, error) {
	s, err := decodeHeader(raw, fieldAccessReason)
	if err != nil {
		return "", err
	}
	if err := visibleText(s, fieldAccessReason); err != nil {
		return "", err
	}
	s = strings.TrimSpace(s)
	switch n := utf8.RuneCountInString(s); {
	case n < minAccessReason:
		return "", headerProblem{status: http.StatusUnprocessableEntity, field: fieldAccessReason, rule: "minLength"}
	case n > maxAccessReason:
		return "", headerProblem{status: http.StatusUnprocessableEntity, field: fieldAccessReason, rule: "maxLength"}
	}
	return s, nil
}

// decodeLookupEmail — почта поиска в канонической форме emailaddr.Normalize: управляющие и невидимые символы —
// 422 format, длиннее 254 байт — 422 maxLength, не адрес — 422 format (такой почты нет ни у одного игрока, а не 404).
func decodeLookupEmail(raw string) (string, error) {
	s, err := decodeHeader(raw, fieldLookupEmail)
	if err != nil {
		return "", err
	}
	if err := visibleText(s, fieldLookupEmail); err != nil {
		return "", err // Normalize срезал бы крайний перевод строки и принял адрес
	}
	if len(s) > maxLookupEmail {
		return "", headerProblem{status: http.StatusUnprocessableEntity, field: fieldLookupEmail, rule: "maxLength"}
	}
	email, ok := emailaddr.Normalize(s)
	if !ok {
		return "", headerProblem{status: http.StatusUnprocessableEntity, field: fieldLookupEmail, rule: "format"}
	}
	return email, nil
}
