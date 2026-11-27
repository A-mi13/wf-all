package blob

// SetFSFault — сбой перед фиксацией Put, после записи временного файла (TestFSFailedPutKeepsOld, TestFSSameKeyRace).
func SetFSFault(s *FS, fn func() error) { s.fault = fn }

// SetFSBeforeCreate — крючок между созданием каталога и временного файла (TestFSPruneRaceConflict).
func SetFSBeforeCreate(s *FS, fn func()) { s.beforeCreate = fn }

// FSObjectPath — путь файла объекта ключа.
func FSObjectPath(s *FS, key string) string { return s.path(key) }
