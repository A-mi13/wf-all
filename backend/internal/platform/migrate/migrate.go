// Package migrate накатывает вшитые миграции goose — без CLI, из любого бинарника и теста.
package migrate

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"wf/backend/migrations"
)

func NewProvider(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSessionLocker(locker),       // два инстанса не накатят одновременно
		goose.WithDisableGlobalRegistry(true), // только SQL-миграции
	)
}

func Up(ctx context.Context, db *sql.DB) error {
	p, err := NewProvider(db)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}
