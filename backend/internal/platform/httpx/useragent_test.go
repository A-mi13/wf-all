package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"wf/backend/internal/platform/httpx"
)

// User-Agent в аудит и сессии (неизменяемый журнал, staff_sessions) — не длиннее 512 байт (ревью ветки, HTTP
// Minor 4: анонимный вход с UA ~1 МиБ раздувал бы журнал). Обрезка — по границе руны; невалидный UTF-8 и NUL
// Postgres в text не примет (22021) — запись упала бы 500, поэтому они заменяются и убираются.
func TestUserAgent(t *testing.T) {
	ru := strings.Repeat("Ж", 300) // 600 байт
	for _, c := range []struct {
		name, ua, want string
	}{
		{"пусто", "", ""},
		{"короткий как есть", "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0", "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0"},
		{"ровно 512", strings.Repeat("a", 512), strings.Repeat("a", 512)},
		{"513 — до 512", strings.Repeat("a", 513), strings.Repeat("a", 512)},
		{"100 КБ — до 512", strings.Repeat("b", 100<<10), strings.Repeat("b", 512)},
		{"кириллица — по границе руны", ru, strings.Repeat("Ж", 256)},
		{"руна поперёк границы — целиком отброшена", "a" + ru, "a" + strings.Repeat("Ж", 255)},
		{"невалидный UTF-8 — заменён", "Agent/\xff\xfe1", "Agent/�1"},
		{"NUL убран", "Agent\x00/1", "Agent/1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if c.ua != "" {
				r.Header.Set("User-Agent", c.ua)
			} else {
				r.Header.Del("User-Agent")
			}
			got := httpx.UserAgent(r)
			if got != c.want {
				t.Fatalf("UserAgent: %d байт %q…, ждали %d байт", len(got), got[:min(len(got), 40)], len(c.want))
			}
			if len(got) > httpx.MaxUserAgentBytes || !utf8.ValidString(got) || strings.ContainsRune(got, 0) {
				t.Fatalf("UserAgent: %d байт, валидный UTF-8 %v", len(got), utf8.ValidString(got))
			}
		})
	}
	if httpx.MaxUserAgentBytes != 512 {
		t.Fatalf("MaxUserAgentBytes = %d", httpx.MaxUserAgentBytes)
	}
}
