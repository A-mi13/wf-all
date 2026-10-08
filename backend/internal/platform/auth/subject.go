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
// При false Subject всегда нулевой.
func SubjectFrom(ctx context.Context) (Subject, bool) {
	var sub Subject
	if s, ok := StaffFrom(ctx); ok {
		sub = Subject{Kind: SubjectStaff, ID: s.StaffID}
	} else if p, ok := From(ctx); ok {
		sub = Subject{Kind: SubjectUser, ID: p.UserID, DeviceID: p.DeviceID}
	}
	if sub.ID == uuid.Nil {
		return Subject{}, false
	}
	return sub, true
}
