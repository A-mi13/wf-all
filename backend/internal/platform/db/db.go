// backend/internal/platform/db/db.go
// Package db открывает пул pgx по конфигу. Пинг на старте: недоступная база — ошибка сразу.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
)

func Open(ctx context.Context, c config.DB) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.URL)
	if err != nil {
		return nil, fmt.Errorf("db: разбор URL: %w", err)
	}
	pc.MaxConns = c.MaxConns
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("db: пул: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}
