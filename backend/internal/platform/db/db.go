// Package db открывает пул pgx по конфигу. Пинг на старте: недоступная база — ошибка сразу.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
)

// ErrUnparsableURL — ошибка разбора строки подключения. Исходную ошибку pgx
// не заворачиваем: она цитирует строку, а пароль маскирует лишь «best effort»
// (хвост после пробела в пароле, '@' в пароле, незакрытая кавычка — утекают).
var ErrUnparsableURL = errors.New("db: строка подключения не разбирается (значение не выводится)")

func Open(ctx context.Context, c config.DB) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.URL)
	if err != nil {
		return nil, ErrUnparsableURL
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
