package media_test

import (
	"context"
	"testing"
	"time"

	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// R19, первая проверка: у Limiter нет веса — Add и Allow расходуют ровно один интервал GCRA независимо от «размера»
// запроса, Over смотрит только «пройдёт ли ещё одна единица». Байты так не посчитать (200 МиБ = 2²⁷·1,5 вызовов),
// поэтому объём NewUpload считает запросом SumDeclaredBytesByUser под PendingLock (решение контроллера).
func TestLimiterHasNoWeight(t *testing.T) {
	t.Parallel()
	pools := dbtest.NewPoolsAs(t, "api")
	clk := clocktest.New(svcAt)
	l := ratelimit.NewPG(pools.As, clk, ratelimit.Players)
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 3, Period: 24 * time.Hour}
	for i := range 3 {
		if over, err := l.Over(ctx, "user:media_test:x", p); err != nil || over {
			t.Fatalf("до %d-й единицы: over=%v %v", i+1, over, err)
		}
		if err := l.Add(ctx, "user:media_test:x", p); err != nil {
			t.Fatal(err)
		}
	}
	if over, err := l.Over(ctx, "user:media_test:x", p); err != nil || !over {
		t.Fatalf("после трёх Add (по единице каждый): over=%v %v", over, err)
	}
	if res, err := l.Allow(ctx, "user:media_test:x", p); err != nil || res.Allowed {
		t.Fatalf("Allow после трёх единиц: %+v %v", res, err)
	}
}
