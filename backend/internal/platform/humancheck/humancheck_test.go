package humancheck_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestRequire(t *testing.T) {
	ctx := context.Background()
	pow := newPoW(t, dbtest.NewPoolsAs(t, "api"), clocktest.New(start), newKey(t))

	// решения нет — 403 humancheck.required с задачей
	err := humancheck.Require(ctx, pow)
	req, ok := errors.AsType[*humancheck.RequiredError](err)
	if !ok {
		t.Fatalf("ждали RequiredError: %v", err)
	}
	p := req.Problem()
	if p.Status != http.StatusForbidden || p.Code != httpx.CodeHumancheckRequired || p.Challenge == nil {
		t.Fatalf("Problem: %+v", p)
	}
	if _, ok := errors.AsType[httpx.ProblemError](err); !ok {
		t.Fatal("RequiredError не ProblemError — хендлер ответит 500")
	}

	// решение верное — проходит; то же второй раз — снова задача
	s := solve(t, req.Challenge)
	if err := humancheck.Require(humancheck.WithSolution(ctx, s), pow); err != nil {
		t.Fatalf("верное решение: %v", err)
	}
	if _, ok := errors.AsType[*humancheck.RequiredError](humancheck.Require(humancheck.WithSolution(ctx, s), pow)); !ok {
		t.Fatal("повтор решения пропущен")
	}
}

func TestMiddlewareCarriesSolution(t *testing.T) {
	pow := newPoW(t, dbtest.NewPoolsAs(t, "api"), clocktest.New(start), newKey(t))
	ch, _ := pow.NewChallenge(context.Background())
	s := solve(t, ch)
	var got error
	h := humancheck.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = humancheck.Require(r.Context(), pow)
	}))
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(humancheck.Header, s)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != nil {
		t.Fatalf("решение из заголовка не дошло: %v", got)
	}
}
