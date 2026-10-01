package events

import "testing"

// SetCleanupBatch уменьшает пачку чистки на время теста — чтобы проверить цикл по пачкам на
// нескольких строках. Глобальное значение: не вызывать из теста с t.Parallel.
func SetCleanupBatch(t testing.TB, n int32) {
	t.Helper()
	prev := cleanupBatch
	cleanupBatch = n
	t.Cleanup(func() { cleanupBatch = prev })
}
