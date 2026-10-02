package auth

// CacheLen — число записей кэша (только тесты).
func CacheLen(l SessionLoader) int {
	c := l.(*cachedLoader)
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
