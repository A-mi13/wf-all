package blob

// SetFSFault — сбой перед фиксацией Put, после записи временного файла (TestFSFailedPutKeepsOld, TestFSSameKeyRace).
func SetFSFault(s *FS, fn func() error) { s.fault = fn }

// FSObjectPath — путь файла объекта ключа.
func FSObjectPath(s *FS, key string) string { return s.path(key) }
