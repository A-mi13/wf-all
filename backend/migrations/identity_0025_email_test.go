package migrations_test

import (
	"os"
	"strings"
	"testing"
)

// emailFormatHead — начало выражения формы почты в 0025: и в предпроверке (NOT (…)), и в CHECK users_email_format.
const emailFormatHead = "octet_length(email::text) <= 254 AND ("

// balancedExpr — выражение от start до закрывающей скобки, парной первой открывающей после start; скобки внутри
// строковых литералов SQL (в кавычках; удвоенная кавычка — экранированная) не считаются.
func balancedExpr(t *testing.T, s string, start int) string {
	t.Helper()
	depth, quoted := 0, false
	for i := start; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			quoted = !quoted // '' внутри литерала — две смены подряд, итог тот же
		case quoted:
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	t.Fatalf("незакрытое выражение с позиции %d", start)
	return ""
}

// Предпроверка 0025 повторяет CHECK users_email_format дословно (с точностью до пробелов): разойдись они — накат
// пропустит строку, на которой упадёт ADD CONSTRAINT голым 23514, или откажет строке, которую CHECK принял бы.
func TestIdentityEmailPrecheckMatchesCheck(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("0025_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(b)
	if i := strings.Index(up, "-- +goose Down"); i > 0 {
		up = up[:i]
	}
	var exprs []string
	for off := 0; ; {
		i := strings.Index(up[off:], emailFormatHead)
		if i < 0 {
			break
		}
		e := balancedExpr(t, up, off+i+len(emailFormatHead)-1)
		exprs = append(exprs, strings.Join(strings.Fields(e), " "))
		off += i + len(emailFormatHead)
	}
	if len(exprs) != 2 {
		t.Fatalf("выражений формы почты в Up — %d, ждали 2 (предпроверка и CHECK)", len(exprs))
	}
	if exprs[0] != exprs[1] {
		t.Fatalf("предпроверка и CHECK users_email_format разошлись:\n%s\n%s", exprs[0], exprs[1])
	}
	if !strings.Contains(up, "ADD CONSTRAINT users_email_format CHECK (\n        "+emailFormatHead) {
		t.Fatal("второе выражение — не CHECK users_email_format")
	}
}
