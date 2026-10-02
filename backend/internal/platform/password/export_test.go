package password

// CommonList — строки вшитого списка (только тесты).
func CommonList() []string {
	out := make([]string, 0, len(commonSet()))
	for k := range commonSet() {
		out = append(out, k)
	}
	return out
}

// Occupy — занять все места семафора; возвращает освобождение (только тесты).
func Occupy(h *Hasher) func() {
	for range cap(h.sem) {
		h.sem <- struct{}{}
	}
	return func() {
		for range cap(h.sem) {
			<-h.sem
		}
	}
}
