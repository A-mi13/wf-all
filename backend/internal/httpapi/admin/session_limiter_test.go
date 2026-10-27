package admin_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/ratelimit"
)

var errLimiterDown = errors.New("хранилище лимитов недоступно")

// downLimiter — хранилище лимитов недоступно: Over и/или Add отвечают ошибкой; Allow (ratelimit.Middleware
// дальше по конвейеру) пропускает. Запоминает вызовы Add.
type downLimiter struct {
	overErr, addErr error

	mu   sync.Mutex
	adds int
}

func (l *downLimiter) Allow(context.Context, string, ratelimit.Policy) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

func (l *downLimiter) Add(context.Context, string, ratelimit.Policy) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.adds++
	return l.addErr
}

func (l *downLimiter) Over(context.Context, string, ratelimit.Policy) (bool, error) {
	return false, l.overErr
}

// Хранилище лимитов недоступно в слое сессии (ревью ветки, HTTP Minor 5; session.go — проверка Over и учёт
// отказа Add): как в ratelimit.Middleware, проверка пропускается, а не 500 и не 429 — сотрудник с живой
// сессией работает, невалидная cookie по-прежнему стирается и запрос идёт анонимно (401); сбой — в логе.
func TestSessionLimiterUnavailable(t *testing.T) {
	type want struct {
		status    int
		code      string
		cleared   bool
		adds      int
		logged    []string
		notLogged []string
	}
	const overMsg, addMsg = "лимит отказов сессии недоступен", "отказ сессии не учтён в лимите"
	for _, c := range []struct {
		name   string
		lim    *downLimiter
		token  string
		passes bool
		want   want
	}{
		{"Over недоступен, сессия живая — проходит", &downLimiter{overErr: errLimiterDown}, "tok", true,
			want{adds: 0, logged: []string{overMsg}, notLogged: []string{addMsg}}},
		{"Over недоступен, cookie невалидна — 401 и стирание", &downLimiter{overErr: errLimiterDown}, "bad", false,
			want{status: http.StatusUnauthorized, code: httpx.CodeUnauthenticated, cleared: true, adds: 1, logged: []string{overMsg}}},
		{"Add недоступен, cookie невалидна — 401 и стирание", &downLimiter{addErr: errLimiterDown}, "bad", false,
			want{status: http.StatusUnauthorized, code: httpx.CodeUnauthenticated, cleared: true, adds: 1, logged: []string{addMsg},
				notLogged: []string{overMsg}}},
		{"оба недоступны, cookie невалидна — 401 и стирание", &downLimiter{overErr: errLimiterDown, addErr: errLimiterDown}, "bad", false,
			want{status: http.StatusUnauthorized, code: httpx.CodeUnauthenticated, cleared: true, adds: 1, logged: []string{overMsg, addMsg}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := testOptions(t)
			o.Limiter = c.lim
			o.Sessions = &fakeSessions{staff: map[string]*auth.Staff{"tok": staffOf(perm.Owner, auth.StageActive, false)}}
			logs := &bytes.Buffer{}
			h, err := admin.NewHandler(slog.New(slog.NewTextHandler(logs, nil)), o)
			if err != nil {
				t.Fatal(err)
			}
			rec := serve(h, call(http.MethodGet, "/v1/staff/me", c.token, ""))
			if c.passes {
				wantPassed(t, rec)
				if len(rec.Result().Cookies()) != 0 {
					t.Fatalf("живая сессия, а cookie тронута: %v", rec.Header().Values("Set-Cookie"))
				}
			} else {
				wantProblem(t, rec, c.want.status, c.want.code)
				if clearsCookie(rec) != c.want.cleared {
					t.Fatalf("стирание cookie: %v, ждали %v", rec.Header().Values("Set-Cookie"), c.want.cleared)
				}
			}
			if c.lim.adds != c.want.adds {
				t.Fatalf("Add вызван %d раз, ждали %d", c.lim.adds, c.want.adds)
			}
			for _, m := range c.want.logged {
				if !strings.Contains(logs.String(), m) || !strings.Contains(logs.String(), errLimiterDown.Error()) {
					t.Fatalf("в логе нет %q с причиной: %s", m, logs.String())
				}
			}
			for _, m := range c.want.notLogged {
				if strings.Contains(logs.String(), m) {
					t.Fatalf("в логе лишнее %q: %s", m, logs.String())
				}
			}
		})
	}
}
