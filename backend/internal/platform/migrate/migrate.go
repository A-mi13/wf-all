// Package migrate накатывает вшитые миграции goose — без CLI, из любого бинарника и теста.
package migrate

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"wf/backend/migrations"
)

// NewProvider собирает провайдер goose. log != nil — goose пишет о каждой
// миграции в этот логгер (как остальные бинарники, через logx); nil — молча.
func NewProvider(db *sql.DB, log *slog.Logger) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, err
	}
	opts := []goose.ProviderOption{
		goose.WithSessionLocker(locker),       // два инстанса не накатят одновременно
		goose.WithDisableGlobalRegistry(true), // только SQL-миграции
	}
	if log != nil {
		opts = append(opts, goose.WithSlog(log), goose.WithVerbose(true))
	}
	return goose.NewProvider(goose.DialectPostgres, db, migrations.FS, opts...)
}

func Up(ctx context.Context, db *sql.DB) error {
	p, err := NewProvider(db, nil)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}
