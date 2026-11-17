package admin_test

import (
	"net/http"
	"slices"
	"testing"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
)

// Фоновые запросы сотрудника (X-WF-Background: 1 у GET — счётчики меню, плитки главной) идут своим классом
// staff_background: сколько бы их ни было, обычный лимит сотрудника (default.user) не тратится — обычный запрос
// после них проходит. Фоновые ограничены своим классом: ровно всплеск staff_background.user, дальше 429. Часы
// стоят — счётчики не восстанавливаются.
func TestBackgroundRequestsOwnRateClass(t *testing.T) {
	t.Parallel()
	o := testOptions(t)
	clk := clocktest.New(t0)
	o.Clock = clk
	o.RateRules = ratelimit.AdminRules()
	o.Limiter = ratelimit.NewPG(o.DB, clk, ratelimit.Staff)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok": staffOf(perm.Owner, auth.StageActive, false)}}
	h := newHandler(t, o)
	bgBurst := o.RateRules[ratelimit.ClassStaffBackground].User.Burst
	userBurst := o.RateRules[ratelimit.DefaultClass].User.Burst
	if bgBurst >= userBurst {
		t.Fatalf("всплеск фоновых %d не меньше обычного %d — тест не различит классы", bgBurst, userBurst)
	}
	passed := 0
	for range userBurst + 1 {
		r := call(http.MethodGet, "/v1/staff/me", "tok", "")
		r.Header.Set("X-WF-Background", "1")
		rec := serve(h, r)
		if rec.Code == http.StatusTooManyRequests {
			wantProblem(t, rec, http.StatusTooManyRequests, httpx.CodeRateLimited)
			continue
		}
		wantPassed(t, rec)
		passed++
	}
	if passed != bgBurst {
		t.Fatalf("фоновых прошло %d, ждали всплеск staff_background %d", passed, bgBurst)
	}
	// обычный лимит не тронут: весь всплеск default.user, затем 429
	for i := range userBurst {
		rec := serve(h, call(http.MethodGet, "/v1/staff/me", "tok", ""))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("обычный запрос %d после фоновых: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	wantProblem(t, serve(h, call(http.MethodGet, "/v1/staff/me", "tok", "")), http.StatusTooManyRequests, httpx.CodeRateLimited)
}

// Операция со своим x-rate-limit свой класс не меняет и под заголовком: заголовок ставит клиент, ослаблять им
// строгий класс нельзя. GET со своим классом в контракте нет — проверка напрямую.
func TestBackgroundKeepsOwnClass(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := call(method, "/v1/staff/auth/login", "", "")
		r.Header.Set("X-WF-Background", "1")
		if got := admin.RateClass(r, "staff_auth"); got != "staff_auth" {
			t.Fatalf("%s staff_auth с заголовком → %s", method, got)
		}
	}
	r := call(http.MethodGet, "/v1/staff/me", "", "")
	r.Header.Set("X-WF-Background", "1")
	if got := admin.RateClass(r, ratelimit.DefaultClass); got != ratelimit.ClassStaffBackground {
		t.Fatalf("фоновый GET → %s", got)
	}
}

// Класс выбирается по заголовку у GET, а не по операции: мутация с X-WF-Background — обычный класс; значение
// заголовка — ровно «1».
func TestBackgroundClassOnlyForGET(t *testing.T) {
	t.Parallel()
	o := testOptions(t)
	s := staffOf(perm.Owner, auth.StageActive, false)
	o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok": s}}
	lim := &recLimiter{}
	o.Limiter = lim
	h := newHandler(t, o)
	subj := auth.Subject{Kind: auth.SubjectStaff, ID: s.StaffID}
	cases := []struct {
		name   string
		r      *http.Request
		header string
		class  string
	}{
		{"GET с 1", call(http.MethodGet, "/v1/staff/me", "tok", ""), "1", ratelimit.ClassStaffBackground},
		{"GET без заголовка", call(http.MethodGet, "/v1/staff/me", "tok", ""), "", ratelimit.DefaultClass},
		{"GET с true", call(http.MethodGet, "/v1/staff/me", "tok", ""), "true", ratelimit.DefaultClass},
		{"POST с 1", call(http.MethodPost, "/v1/staff/me/sessions/revoke-others", "tok", ""), "1", ratelimit.DefaultClass},
	}
	for _, c := range cases {
		lim.mu.Lock()
		lim.allows = nil
		lim.mu.Unlock()
		if c.header != "" {
			c.r.Header.Set("X-WF-Background", c.header)
		}
		serve(h, c.r)
		want := ratelimit.SubjectKey(c.class, subj)
		lim.mu.Lock()
		got := slices.Clone(lim.allows)
		lim.mu.Unlock()
		if !slices.Contains(got, want) {
			t.Fatalf("%s: ключи лимита %v, нет %s", c.name, got, want)
		}
	}
}
