package flags_test

import (
	"context"
	"errors"
	"testing"

	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/testkit/dbtest"
)

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
