package id_test

import (
	"bytes"
	"testing"

	"wf/backend/internal/platform/id"
)

func TestNewIsVersion7(t *testing.T) {
	if v := id.New().Version(); v != 7 {
		t.Fatalf("версия = %d, нужна 7", v)
	}
}

// Ключи UUIDv7 растут во времени: вставки в индекс идут в конец, keyset-пагинация по id
// совпадает с порядком создания внутри одного процесса.
func TestNewIsMonotonic(t *testing.T) {
	prev := id.New()
	for range 10_000 {
		next := id.New()
		if bytes.Compare(prev[:], next[:]) >= 0 {
			t.Fatalf("%s не меньше %s", prev, next)
		}
		prev = next
	}
}
