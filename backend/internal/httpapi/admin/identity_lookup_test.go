package admin_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
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

// lookupHeaders — заголовки поиска: почта и причина в percent-encoding.
func lookupHeaders(email string) []string {
	return []string{"X-WF-Lookup-Email", email, "X-WF-Access-Reason", accessReason}
}

// TestFindPlayerByEmailHTTP — поиск игрока через весь конвейер на настоящем identity (план I-4, Task 5): закодированные
// почта (в любом регистре) и причина — 200 по контракту с Cache-Control private, no-store; нет почты, модератор
// чужого города — 404 user_not_found; почта не адрес после декодирования — 422 по полю заголовка, битая кодировка —
// 400; статистика способов входа — 200, три строки.
func TestFindPlayerByEmailHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	own, other := e.city("lookup-own"), e.city("lookup-other")
	owner := e.owners(1)[0]
	mod := e.firstLoginModerator("mod@wf.example", own)
	uid := e.player("lookup_http")
	e.exec(`UPDATE users SET city_id = $2 WHERE id = $1`, uid, other)
	const path = "/v1/identity/users/lookup"

	rec := e.send(http.MethodGet, path, owner.token, "", "", lookupHeaders("Lookup_HTTP%40Example.com")...)
	wantStatus(t, rec, http.StatusOK)
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("Cache-Control: %q", cc)
	}
	var got struct {
		ID         uuid.UUID `json:"id"`
		Nickname   string    `json:"nickname"`
		CityID     uuid.UUID `json:"city_id"`
		Status     string    `json:"status"`
		MfaEnabled bool      `json:"mfa_enabled"`
		MfaReset   *struct{} `json:"mfa_reset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.ID != uid || got.Nickname != "lookup_http" ||
		got.CityID != other || got.Status != "active" || !got.MfaEnabled || got.MfaReset != nil ||
		!strings.Contains(rec.Body.String(), `"mfa_reset":null`) {
		t.Fatalf("ответ: %v %s", err, rec.Body.String())
	}

	wantProblem(t, e.send(http.MethodGet, path, owner.token, "", "", lookupHeaders("nobody%40example.com")...),
		http.StatusNotFound, identity.CodeUserNotFound)
	wantProblem(t, e.send(http.MethodGet, path, mod.token, "", "", lookupHeaders("lookup_http%40example.com")...),
		http.StatusNotFound, identity.CodeUserNotFound)

	for _, c := range []struct {
		email  string
		status int
	}{{"not-an-email", http.StatusUnprocessableEntity}, {"%D0%", http.StatusBadRequest}} {
		rec := e.send(http.MethodGet, path, owner.token, "", "", lookupHeaders(c.email)...)
		wantProblem(t, rec, c.status, httpx.CodeValidationFailed)
		if !strings.Contains(rec.Body.String(), `"field":"header.X-WF-Lookup-Email"`) {
			t.Fatalf("%s: нет поля заголовка — %s", c.email, rec.Body.String())
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action IN ('identity.pii_read', 'identity.player_lookup')`); n != 3 {
		t.Fatalf("записей поиска %d, ждали 3 (найден, нет, чужой город)", n)
	}

	rec = e.send(http.MethodGet, "/v1/identity/auth-methods/usage", mod.token, "", "")
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `{"linked":0,"method":"apple","only_method":0}`) ||
		rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("статистика: %s", rec.Body.String())
	}
}
