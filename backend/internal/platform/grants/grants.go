// Package grants применяет права ролей БД (спека бэкенда §10.1) — идемпотентный grants.sql.
package grants

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed grants.sql
var script string

// Execer — *sql.DB или *sql.Conn.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Apply выдаёт права ролям api, admin, worker на объекты, которыми владеет текущая роль.
// Один оператор DO — атомарно: окна «права сняты, новые не выданы» снаружи не видно.
func Apply(ctx context.Context, db Execer) error {
	if _, err := db.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("grants: %w", err)
	}
	return nil
}
