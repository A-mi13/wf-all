package blob

// SetFSFault — сбой между записью частей пары (TestFSPairAtomic).
func SetFSFault(s *FS, fn func() error) { s.fault = fn }

// FSMetaPath — путь файла meta ключа.
func FSMetaPath(s *FS, key string) string { return s.metaPath(key) }
