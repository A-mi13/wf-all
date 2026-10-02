package clocktest_test

import (
	"sync"
	"testing"
	"time"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/testkit/clocktest"
)

var _ clock.Clock = (*clocktest.Fake)(nil)

func TestFakeSetAndAdvance(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	f := clocktest.New(start)
	if got := f.Now(); !got.Equal(start) || got.Location() != time.UTC {
		t.Fatalf("Now = %v, нужен %v в UTC", got, start)
	}
	f.Advance(90 * time.Second)
	if got := f.Now(); !got.Equal(start.Add(90 * time.Second)) {
		t.Fatalf("после Advance: %v", got)
	}
	later := start.Add(time.Hour)
	f.Set(later)
	if got := f.Now(); !got.Equal(later) || got.Location() != time.UTC {
		t.Fatalf("после Set: %v", got)
	}
}

func TestFakeConcurrentAdvance(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f := clocktest.New(start)
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { f.Advance(time.Second); _ = f.Now() })
	}
	wg.Wait()
	if got := f.Now(); !got.Equal(start.Add(100 * time.Second)) {
		t.Fatalf("после 100 сдвигов: %v", got)
	}
}
