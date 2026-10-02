package flags_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestEnabledByCity(t *testing.T) {
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	moscow, kazan := uuid.New(), uuid.New()
	// 'ads' уже заведён сидом 0010 выключенным — берём ключи теста с префиксом t_.
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO feature_flags (key, enabled_globally, enabled_city_ids) VALUES
		('t_chat', false, ARRAY[$1::uuid]), ('t_ads', true, '{}'), ('t_off', false, '{}')`, moscow); err != nil {
		t.Fatal(err)
	}
	s := flags.NewStore(pools.As)
	cases := []struct {
		key  string
		city uuid.UUID
		want bool
	}{
		{"t_chat", moscow, true}, {"t_chat", kazan, false}, {"t_ads", kazan, true}, {"t_off", moscow, false},
	}
	for _, c := range cases {
		got, err := s.Enabled(ctx, c.key, c.city)
		if err != nil || got != c.want {
			t.Errorf("%s в %v: %v %v", c.key, c.city, got, err)
		}
	}
	if _, err := s.Enabled(ctx, "nope", moscow); !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("неизвестный флаг: %v", err)
	}

	if err := s.Require(ctx, "t_chat", moscow); err != nil {
		t.Fatal(err)
	}
	err := s.Require(ctx, "t_chat", kazan)
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !errors.Is(err, flags.ErrDisabled) || !ok || pe.Problem().Status != http.StatusForbidden || pe.Problem().Code != httpx.CodeFeatureDisabled {
		t.Fatalf("выключенный флаг: %v", err)
	}
	if err := s.Require(ctx, "nope", moscow); !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("Require неизвестного флага: %v", err)
	}
}

// Сид 0010 заводит флаги выключенными: «чего сейчас НЕ делаем» из CLAUDE.md.
func TestSeededFlagIsOff(t *testing.T) {
	on, err := flags.NewStore(dbtest.NewPool(t)).EnabledGlobally(context.Background(), "subscriptions")
	if err != nil {
		t.Fatal(err)
	}
	if on {
		t.Fatal("subscriptions должен быть выключен")
	}
}

func TestUnknownFlag(t *testing.T) {
	_, err := flags.NewStore(dbtest.NewPool(t)).EnabledGlobally(context.Background(), "no_such_flag")
	if !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("err = %v, ждали ErrUnknownFlag", err)
	}
}
