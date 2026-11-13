package admin_test

import (
	"net/http"
	"strings"
	"testing"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// Заголовки ПД (план I-1, R19; ревью плана I4): значения X-WF-Lookup-Email и X-WF-Access-Reason — UTF-8 в
// percent-encoding (значение заголовка в Fetch — байтовая строка, кириллица не уходит из браузера). Через весь
// конвейер до заглушки: закодированная кириллическая причина проходит валидатор (501 заглушки), сырая — 400
// validation.failed по полю заголовка (pattern). Битая кодировка (%D0%) pattern проходит — её отказ и пределы после
// декодирования проверяет сценарий этапа 9.
func TestLookupHeadersEncoding(t *testing.T) {
	t.Parallel()
	owner := staffOf(perm.Owner, auth.StageActive, false)
	o := testOptions(t)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok-owner": owner}}
	h := newHandler(t, o)
	cases := []struct {
		name, path     string
		hdr            []string
		status         int
		code, errField string
	}{
		{"закодированная причина", "/v1/identity/captain-applications/" + anyID.String() + "/contact",
			[]string{"X-WF-Access-Reason", accessReason}, http.StatusNotImplemented, httpx.CodeNotImplemented, ""},
		{"сырая кириллица в причине", "/v1/identity/captain-applications/" + anyID.String() + "/contact",
			[]string{"X-WF-Access-Reason", "обращение 4812"}, http.StatusBadRequest, httpx.CodeValidationFailed,
			"header.X-WF-Access-Reason"},
		{"нет причины", "/v1/identity/captain-applications/" + anyID.String() + "/contact",
			nil, http.StatusBadRequest, httpx.CodeValidationFailed, "header.X-WF-Access-Reason"},
		{"почта и причина закодированы", "/v1/identity/users/lookup",
			[]string{"X-WF-Lookup-Email", "ivan.petrov%40mail.example", "X-WF-Access-Reason", accessReason},
			http.StatusNotImplemented, httpx.CodeNotImplemented, ""},
		{"почта с сырой @", "/v1/identity/users/lookup",
			[]string{"X-WF-Lookup-Email", "ivan.petrov@mail.example", "X-WF-Access-Reason", accessReason},
			http.StatusBadRequest, httpx.CodeValidationFailed, "header.X-WF-Lookup-Email"},
	}
	for _, c := range cases {
		rec := serve(h, withHeaders(call(http.MethodGet, c.path, "tok-owner", ""), c.hdr))
		body := rec.Body.String()
		if rec.Code != c.status || !strings.Contains(body, `"code":"`+c.code+`"`) {
			t.Errorf("%s: %d %s — ждали %d %s", c.name, rec.Code, body, c.status, c.code)
			continue
		}
		if c.errField != "" && !strings.Contains(body, `"field":"`+c.errField+`"`) {
			t.Errorf("%s: нет поля %s в ошибке — %s", c.name, c.errField, body)
		}
	}
}
