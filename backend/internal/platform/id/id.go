// Package id — идентификаторы сущностей: UUIDv7, генерируются в Go (спека бэкенда §6.9).
// Строки, вставленные SQL (сиды, триггеры), получают v4 по DEFAULT, поэтому порядок по
// времени для них — только по created_at.
package id

import "github.com/google/uuid"

// New — новый UUIDv7. Ошибка генерации возможна только при отказе crypto/rand — тогда
// продолжать нельзя, поэтому паника.
func New() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// namedSpace — пространство UUIDv5 платформы (R18). Литерал не меняется никогда: от него зависят id агрегатов
// событий и объектов журнала, уже записанные в базу.
var namedSpace = uuid.MustParse("6f1c2a9e-3b7d-4e58-9a41-0c5d8e2f7b13")

// Named — детерминированный uuid (UUIDv5) сущности без своей строки с id: агрегат события feature_flag/<ключ>,
// app_versions/<платформа>, объект журнала geonames_import_job/<id задачи>. Имя — space + "\x00" + name:
// разделитель не даёт склейкам ("ab", "c") и ("a", "bc") совпасть.
func Named(space, name string) uuid.UUID {
	return uuid.NewSHA1(namedSpace, []byte(space+"\x00"+name))
}
