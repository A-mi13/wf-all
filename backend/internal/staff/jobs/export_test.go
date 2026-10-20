package jobs

import (
	"context"
	"time"
)

// SetCleanerTestHooks — проход и тики Run под управлением теста (без часов и реального часа ожидания).
func SetCleanerTestHooks(c *Cleaner, pass func(context.Context) error, tick <-chan time.Time) {
	c.pass, c.tick = pass, tick
}
