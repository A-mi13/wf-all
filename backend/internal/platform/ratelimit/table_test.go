package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Имена таблиц — для журнала чистки и сообщений об ошибках.
func TestTableString(t *testing.T) {
	t.Parallel()
	if ratelimit.Players.String() != "rate_limits" || ratelimit.Staff.String() != "staff_rate_limits" {
		t.Fatalf("Players=%q Staff=%q", ratelimit.Players, ratelimit.Staff)
	}
	if got := ratelimit.Table(7).String(); got != "ratelimit.Table(7)" {
		t.Fatalf("неизвестная: %q", got)
	}
}

// Неизвестная таблица — паника при сборке, а не тихий уход в таблицу игроков.
func TestNewPGUnknownTable(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]func(){
		"NewPG": func() { ratelimit.NewPG(noRows{}, clocktest.New(start), ratelimit.Table(7)) },
		"DeleteExpired": func() {
			_, _ = ratelimit.DeleteExpired(context.Background(), noRows{}, clocktest.New(start), ratelimit.Table(7))
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("нет паники на неизвестной таблице")
				}
			}()
			f()
		})
	}
}

// Лимитер admin-api (Staff) под ролью admin считает в staff_rate_limits — Allow, Add и Over; в rate_limits
// ничего (у admin на неё прав нет, R2). Лимитер игроков под ролью api — наоборот.
func TestLimiterTables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 2, Period: time.Minute}
	for _, c := range []struct {
		role         string
		table        ratelimit.Table
		mine, others string
	}{
		{"admin", ratelimit.Staff, "staff_rate_limits", "rate_limits"},
		{"api", ratelimit.Players, "rate_limits", "staff_rate_limits"},
	} {
		t.Run(c.role, func(t *testing.T) {
			t.Parallel()
			pools := dbtest.NewPoolsAs(t, c.role)
			l := ratelimit.NewPG(pools.As, clocktest.New(start), c.table)
			if r := allow(t, l, "ip:staff_auth:203.0.113.7", p); !r.Allowed {
				t.Fatal("первый запрос не пропущен")
			}
			if err := l.Add(ctx, "email:staff_login:x", p); err != nil {
				t.Fatal(err)
			}
			allow(t, l, "ip:staff_auth:203.0.113.7", p)
			if r := allow(t, l, "ip:staff_auth:203.0.113.7", p); r.Allowed {
				t.Fatal("третий запрос сверх лимита пропущен — счётчик не в той таблице")
			}
			if !over(t, l, "ip:staff_auth:203.0.113.7", p) {
				t.Fatal("Over не видит исчерпанный ключ")
			}
			count := func(table string) int {
				var n int
				if err := pools.Owner.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			if n := count(c.mine); n != 2 {
				t.Errorf("строк в %s: %d, ждали 2", c.mine, n)
			}
			if n := count(c.others); n != 0 {
				t.Errorf("строк в %s: %d, ждали 0", c.others, n)
			}
		})
	}
}

// Чистка staff_rate_limits — DeleteExpired(…, Staff) пулом admin (тикер admin-api): прошлое удалено порциями,
// будущее и чужая таблица не тронуты.
func TestDeleteExpiredStaff(t *testing.T) {
	t.Parallel()
	pools := dbtest.NewPoolsAs(t, "admin")
	ctx := context.Background()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO staff_rate_limits (key, tat)
		SELECT 'k' || g, $1 FROM generate_series(1, 2500) g`, start.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO staff_rate_limits (key, tat) VALUES ('live', $1)`, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO rate_limits (key, tat) VALUES ('player', $1)`, start.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := ratelimit.DeleteExpired(ctx, pools.As, clocktest.New(start), ratelimit.Staff)
	if err != nil || n != 2500 {
		t.Fatalf("удалено %d, %v; ждали 2500", n, err)
	}
	var left, players int
	if err := pools.Owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM staff_rate_limits), (SELECT count(*) FROM rate_limits)`).
		Scan(&left, &players); err != nil {
		t.Fatal(err)
	}
	if left != 1 || players != 1 {
		t.Fatalf("осталось staff_rate_limits=%d (ждали live), rate_limits=%d (ждали 1)", left, players)
	}
}
