// Package clocktest — управляемые часы для тестов (clock.Clock).
package clocktest

import (
	"sync"
	"time"
)

// Fake — часы, которые идут только по команде теста.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

func New(t time.Time) *Fake { return &Fake{now: t.UTC()} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.UTC()
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
