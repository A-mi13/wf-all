package auth

// CacheLen — число записей кэша (только тесты).
func CacheLen(l SessionLoader) int {
	c := l.(*cachedLoader)
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Waiters — сколько вызовов сейчас ждут общую загрузку (только тесты).
func Waiters(l SessionLoader) int {
	return int(l.(*cachedLoader).waiters.Load())
}
