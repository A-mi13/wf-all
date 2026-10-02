package risk_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestAssessor(t *testing.T) {
	ctx := context.Background()
	c := clocktest.New(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	l := ratelimit.NewPG(dbtest.NewPoolsAs(t, "api").As, c)
	a, err := risk.NewAssessor(l, map[risk.Signal]ratelimit.Policy{
		risk.LoginFailed: {Limit: 3, Period: 15 * time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := a.Record(ctx, risk.LoginFailed, "user:a"); err != nil {
			t.Fatal(err)
		}
	}
	if bad, err := a.Suspicious(ctx, risk.LoginFailed, "user:a"); err != nil || !bad {
		t.Fatalf("3 неудачи из 3 — не подозрительно: %v", err)
	}
	// любой из ключей: аккаунт чист, а IP — нет
	if bad, _ := a.Suspicious(ctx, risk.LoginFailed, "ip:x", "user:a"); !bad {
		t.Fatal("подозрительный ключ среди нескольких не замечен")
	}
	if bad, _ := a.Suspicious(ctx, risk.LoginFailed, "user:b"); bad {
		t.Fatal("чистый аккаунт подозрителен")
	}
	c.Advance(15 * time.Minute)
	if bad, _ := a.Suspicious(ctx, risk.LoginFailed, "user:a"); bad {
		t.Fatal("подозрение не проходит со временем")
	}
	if _, err := a.Suspicious(ctx, risk.Signup, "x"); err == nil {
		t.Fatal("сигнал без политики принят")
	}
	if err := a.Record(ctx, risk.Signup, "x"); err == nil {
		t.Fatal("сигнал без политики записан")
	}
}

func TestDefaultPolicies(t *testing.T) {
	p := risk.DefaultPolicies()
	for _, s := range []risk.Signal{risk.LoginFailed, risk.Signup, risk.NewDevice, risk.Activity} {
		if p[s].Limit == 0 {
			t.Errorf("нет политики %s", s)
		}
	}
	if _, err := risk.NewAssessor(ratelimit.Unlimited, p); err != nil {
		t.Fatal(err)
	}
	if _, err := risk.NewAssessor(ratelimit.Unlimited, map[risk.Signal]ratelimit.Policy{risk.Signup: {Limit: -1}}); err == nil {
		t.Fatal("битая политика принята")
	}
}

func TestSubnet(t *testing.T) {
	cases := map[string]string{
		"203.0.113.77":         "203.0.113.0/24",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"::ffff:203.0.113.77":  "203.0.113.0/24",
	}
	for in, want := range cases {
		if got := risk.Subnet(netip.MustParseAddr(in)); got != want {
			t.Errorf("Subnet(%s) = %q, нужно %q", in, got, want)
		}
	}
	if risk.Subnet(netip.Addr{}) != "" {
		t.Error("пустой адрес")
	}
}
