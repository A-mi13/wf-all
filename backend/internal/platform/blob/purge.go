package blob

import "context"

// Purger — сброс кэша CDN по публичным адресам снятых объектов (спека §3.8).
type Purger interface {
	Purge(ctx context.Context, publicURLs []string) error
}

// NoopPurger — без CDN (R10: во всех бинарниках) и в dev: снятое исчезает сразу.
type NoopPurger struct{}

func (NoopPurger) Purge(context.Context, []string) error { return nil }
