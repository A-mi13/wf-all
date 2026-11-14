package app

import "context"

// Чистка и ротация кольца (спека §5.10, R28) — владелец Task 19. Сигнатуры — Task 8 (I9), тело —
// errNotImplemented; владелец может вынести Rekey в rekey.go.

// CleanupReport — сколько строк вычищено по видам.
type CleanupReport struct {
	Rows map[string]int
}

// Cleanup — чистка identity пачками (воркер).
func (a *App) Cleanup(_ context.Context) (CleanupReport, error) {
	return CleanupReport{}, errNotImplemented
}

// RekeyReport — строки по key_id до и после перешифровки и резервные коды под старыми ключами.
type RekeyReport struct {
	Before, After   map[string]int
	RecoveryOldKeys map[string]int
}

// Rekey — перешифровка долгоживущих секретов текущим ключом (api identity rekey).
func (a *App) Rekey(_ context.Context, _ bool) (RekeyReport, error) {
	return RekeyReport{}, errNotImplemented
}
