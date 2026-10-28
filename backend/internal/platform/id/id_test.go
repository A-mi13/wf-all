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

// R18: детерминированный uuid агрегатов и объектов без своей строки (флаг, версии приложения, задача импорта)
// — UUIDv5 от фиксированного пространства; значения посчитаны независимо (Python uuid.uuid5) и не меняются
// никогда: на них ссылаются события outbox и журнал.
func TestNamedUUIDGolden(t *testing.T) {
	for _, c := range []struct{ space, name, want string }{
		{"feature_flag", "challenges", "795a4e8e-7d2a-5ed8-bf5e-dc99943bdb51"},
		{"app_versions", "ios", "07cfb7f4-4397-55f3-97c9-fe17331cbed4"},
		{"geonames_import_job", "42", "ed612299-3ee5-5c6f-b72c-9436f358de30"},
	} {
		got := id.Named(c.space, c.name)
		if got.String() != c.want || got.Version() != 5 {
			t.Errorf("Named(%q, %q) = %s (v%d), ждали %s", c.space, c.name, got, got.Version(), c.want)
		}
	}
	// разделитель: ("ab", "c") и ("a", "bc") — разные
	if id.Named("ab", "c") == id.Named("a", "bc") {
		t.Fatal("пространство и имя склеены без разделителя")
	}
}
