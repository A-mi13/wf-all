package logx_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/logx"
)

func TestMask(t *testing.T) {
	cases := []struct{ in, want string }{
		{"письмо на ivan.petrov@mail.ru не ушло", "письмо на ***@mail.ru не ушло"},
		{"query email=Иван@почта.рф", "query email=***@почта.рф"},
		{"phone=+7 (916) 123-45-67", "phone=***67"},
		{`value "79161234567" invalid`, `value "***67" invalid`},
		{"/v1/users/89161234567/x", "/v1/users/***67/x"},
		// телефон рядом со знаками препинания — частая форма текстов ошибок
		{"user 79161234567: blocked", "user ***67: blocked"},
		{"звонок на 79161234567.", "звонок на ***67."},
		{"phone:+79161234567", "phone:***67"},
		{"tel:+7-916-123-45-67", "tel:***67"},
		{"{Phone:79161234567 Name:x}", "{Phone:***67 Name:x}"},
		{"phone_79161234567", "phone_***67"},
		{"phone-79161234567", "phone-***67"},
		{"code-79161234567", "code-***67"},
		{"«ivan@mail.ru»", "«***@mail.ru»"},
		{"“ivan@mail.ru”", "“***@mail.ru”"},
		{"`ivan@mail.ru`|x", "`***@mail.ru`|x"},
		{"ok—ivan@mail.ru", "ok—***@mail.ru"},
		{"ok–ivan@mail.ru", "ok–***@mail.ru"},
		{"79161234567-", "***67-"},
		// почта: локальная часть с RFC-символами скрывается целиком, граница — «=», кавычки, скобки
		{"o'brien@mail.ru", "***@mail.ru"},
		{"first#last@mail.ru", "***@mail.ru"},
		{"email='a@b.co'", "email='***@b.co'"},
		{"{Email:a@b.co}", "{Email:***@b.co}"},
		// не телефоны: UUID, дата, адрес с портом, короткое число, число внутри слова
		{"id 0192f6d4-8f8e-7c3a-9d2b-426614174000", "id 0192f6d4-8f8e-7c3a-9d2b-426614174000"},
		{"at 2026-10-01T17:00:00Z", "at 2026-10-01T17:00:00Z"},
		{"at 2026-10-01 17:00:00+00", "at 2026-10-01 17:00:00+00"}, // время Postgres из DETAIL EXCLUDE
		{"ratio 0.123456789012", "ratio 0.123456789012"},           // длинная десятичная дробь
		{"dial 10.0.0.1:5432", "dial 10.0.0.1:5432"},
		{"order 12345", "order 12345"},
		{"token abc79161234567def", "token abc79161234567def"},
		{"", ""},
	}
	for _, c := range cases {
		if got := logx.Mask(c.in); got != c.want {
			t.Errorf("Mask(%q) = %q, нужно %q", c.in, got, c.want)
		}
	}
}

// Маскируются и сообщение, и строковые атрибуты, и ошибки — в обоих форматах.
func TestLoggerMasksPersonalData(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			log := logx.New(&buf, config.Log{Format: format})
			log.Info("вход ivan@mail.ru",
				"err", errors.New(`parameter "phone" value "79161234567": bad`),
				"path", "/v1/x?email=a.b@c.co",
				"request_id", "0192f6d4-8f8e-7c3a-9d2b-426614174000")
			out := buf.String()
			for _, leak := range []string{"ivan@", "a.b@", "1234567"} {
				if strings.Contains(out, leak) {
					t.Errorf("в логе осталось %q: %s", leak, out)
				}
			}
			if !strings.Contains(out, "0192f6d4-8f8e-7c3a-9d2b-426614174000") {
				t.Errorf("UUID испорчен маскированием: %s", out)
			}
		})
	}
}
