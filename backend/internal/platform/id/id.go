// Package id — идентификаторы сущностей: UUIDv7, генерируются в Go (спека бэкенда §6.9).
// Строки, вставленные SQL (сиды, триггеры), получают v4 по DEFAULT, поэтому порядок по
// времени для них — только по created_at.
package id

import "github.com/google/uuid"

// New — новый UUIDv7. Ошибка генерации возможна только при отказе crypto/rand — тогда
// продолжать нельзя, поэтому паника.
func New() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}
