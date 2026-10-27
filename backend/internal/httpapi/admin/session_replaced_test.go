package admin_test

import (
	"net/http"
	"testing"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
)

// Гонка смены токена (находка ревью 9b): второй фактор, unlock и recovery-codes/ack выдают новый
// токен, а параллельный запрос со старым возвращается позже. «Токен заменён» — не «токен
// невалиден»: ответ тот же 401 auth.unauthenticated, но cookie не стирается (иначе браузер
// потеряет уже новую), отказ не считается в IP-лимите staff_auth и не кладётся в негативный кэш.
func TestReplacedTokenKeepsCookie(t *testing.T) {
	o := testOptions(t)
	lim := &recLimiter{}
	o.Limiter = lim
	sessions := &fakeSessions{err: auth.ErrSessionReplaced}
	o.Sessions = sessions
	h := newHandler(t, o)
	for range 2 {
		rec := serve(h, call(http.MethodGet, "/v1/staff/me", "old-token", ""))
		wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
		if c := rec.Result().Cookies(); len(c) != 0 {
			t.Fatalf("заменённый токен стёр cookie: %v", c)
		}
	}
	// анонимная операция (вход) со старой cookie — дальше анонимно, cookie тоже не трогается
	r := loginRequest()
	r.AddCookie(sessionCookie(hostCookie, "old-token"))
	rec := serve(h, r)
	wantPassed(t, rec)
	if c := rec.Result().Cookies(); len(c) != 0 {
		t.Fatalf("вход со старой cookie стёр её: %v", c)
	}
	// негативного кэша нет: каждый запрос сверяется с загрузчиком (льгота короткая, потом —
	// ErrSessionInvalid со стиранием)
	if n := len(sessions.loads()); n != 3 {
		t.Fatalf("загрузок %d, ждали 3", n)
	}
	if len(lim.adds) != 0 {
		t.Fatalf("заменённый токен посчитан в лимите: %v", lim.adds)
	}
}
