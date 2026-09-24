// Package migrations вшивает SQL-миграции в бинарники и тесты.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
