package auth

import (
	"context"

	"github.com/google/uuid"
)

// SubjectKind — кто делает запрос: игрок (public API) или сотрудник (admin-api).
type SubjectKind string

const (
	SubjectUser  SubjectKind = "user"
	SubjectStaff SubjectKind = "staff"
)

// Subject — общий субъект запроса для механизмов платформы, которым всё равно, игрок это или
// сотрудник: идемпотентность (ключ по ID), rate limit (<Kind>:<класс>:<ID>). DeviceID — устройство
// сессии игрока; у сотрудника — uuid.Nil.
type Subject struct {
	Kind     SubjectKind
	ID       uuid.UUID
	DeviceID uuid.UUID
}

// SubjectFrom — субъект запроса: сотрудник (WithStaff) → staff, игрок (With) → user, иначе false.
// Оба в ctx — ошибка сборки конвейера; побеждает сотрудник (admin-api строже). Нулевой id
// (загрузчик нарушил договор) — false: общий ключ «нулевого» субъекта склеил бы чужие запросы.
func SubjectFrom(ctx context.Context) (Subject, bool) {
	if s, ok := StaffFrom(ctx); ok {
		return Subject{Kind: SubjectStaff, ID: s.StaffID}, s.StaffID != uuid.Nil
	}
	if p, ok := From(ctx); ok {
		return Subject{Kind: SubjectUser, ID: p.UserID, DeviceID: p.DeviceID}, p.UserID != uuid.Nil
	}
	return Subject{}, false
}
