// Package rivertest — помощники тестов River: какие виды задач зарегистрированы в реестре исполнителей.
package rivertest

import (
	"reflect"
	"slices"

	"github.com/riverqueue/river"
)

// Kinds — виды задач реестра w по алфавиту. River не отдаёт их наружу, поэтому — чтение неэкспортированного
// workersMap через reflect (только чтение ключей; поменяет River устройство — тест упадёт здесь, а не
// пройдёт вхолостую).
func Kinds(w *river.Workers) []string {
	m := reflect.ValueOf(w).Elem().FieldByName("workersMap")
	if m.Kind() != reflect.Map {
		panic("rivertest: у river.Workers нет workersMap — River поменял устройство реестра")
	}
	out := make([]string, 0, m.Len())
	for _, k := range m.MapKeys() {
		out = append(out, k.String())
	}
	slices.Sort(out)
	return out
}
