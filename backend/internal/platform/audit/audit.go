// Package audit — аудит-лог действий (спека бэкенда §6.9, §10): кто, роль, действие, объект,
// было/стало с маскированием ПД, причина, IP, user-agent. Изменение пишется в транзакции
// действия (Write), чтение чувствительного — отдельной вставкой (WriteRead); журнал для
// админки читают List и Get (list.go).
package audit

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"wf/backend/internal/platform/audit/auditdb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
)

// ErrInvalidEntry — запись не прошла проверку (действие не в формате <модуль>.<действие>,
// нет типа объекта, автор не по правилам CHECK audit_log, before/after не сериализуются);
// в базу ничего не попало.
var ErrInvalidEntry = errors.New("audit: невалидная запись")

var action = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Роли автора — те же списки, что в CHECK audit_log (миграция 0020): поменял там — поменяй здесь
// (расхождение ловит TestActorRolesMatchCheck).
var (
	playerRoles  = map[string]bool{"player": true, "captain": true}
	staffRoles   = map[string]bool{"owner": true, "admin": true, "moderator": true, "support": true}
	serviceRoles = map[string]bool{"system": true, "operator": true, "anonymous": true}
)

// maxLabel — потолок снимка имени в символах (CHECK audit_log_actor_label).
const maxLabel = 120

// Entry — запись аудита. Нулевые значения (uuid.Nil, пустая строка, нулевой адрес) — NULL:
// действие системы (воркер, миграция) или оператора не имеет автора, IP и user-agent.
type Entry struct {
	// ID — id записи: задан — пишется он (письмо о действии ссылается на запись журнала, Get),
	// uuid.Nil — выдаётся новый.
	ID           uuid.UUID
	ActorUserID  uuid.UUID // игрок (public API)
	ActorStaffID uuid.UUID // сотрудник (admin-api); вместе с ActorUserID — ErrInvalidEntry
	// ActorRole — роль автора на момент действия, обязательна: у игрока — роль игрока или
	// сотрудника (импорт geo до плана B), у сотрудника — роль сотрудника; без автора — system,
	// operator или anonymous (неудачный вход, анонимная повторная отправка). Правила — как в
	// CHECK audit_log (миграция 0020), нарушение — ErrInvalidEntry до SQL.
	ActorRole string
	// ActorLabel — снимок отображаемого имени на момент действия (имя сотрудника, operator,
	// system): журнал показывает «кто» по нему, даже когда учётку переименовали (решение D11).
	// У сотрудника обязателен; 1–120 символов без крайних пробелов.
	ActorLabel string
	Action     string // <модуль>.<действие>
	ObjectType string
	ObjectID   uuid.UUID
	// ScopeCityID — город, к которому относится запись: по нему журнал модератора видит записи
	// своих городов (спека §5.3). Ставит модуль (geo — город операции); у staff — uuid.Nil.
	ScopeCityID uuid.UUID
	Before      any
	After       any
	Reason      string
	IP          netip.Addr
	UserAgent   string
}

// Write — аудит изменения: в транзакции из ctx, чтобы запись и действие жили и умирали вместе.
func Write(ctx context.Context, e Entry) error {
	tx, ok := db.TxFrom(ctx)
	if !ok {
		return db.ErrNoTx
	}
	return insert(ctx, auditdb.New(tx), e)
}

// WriteRead — аудит чтения чувствительного: отдельной короткой вставкой, без транзакции действия.
func WriteRead(ctx context.Context, q auditdb.DBTX, e Entry) error {
	return insert(ctx, auditdb.New(q), e)
}

func insert(ctx context.Context, q *auditdb.Queries, e Entry) error {
	if !action.MatchString(e.Action) {
		return fmt.Errorf("%w: действие %q — нужен <модуль>.<действие>", ErrInvalidEntry, e.Action)
	}
	if e.ObjectType == "" {
		return fmt.Errorf("%w: нет типа объекта", ErrInvalidEntry)
	}
	if err := checkActor(e); err != nil {
		return err
	}
	before, err := Mask(e.Before)
	if err != nil {
		return fmt.Errorf("%w: before: %v", ErrInvalidEntry, err) //nolint:errorlint // причина текстом
	}
	after, err := Mask(e.After)
	if err != nil {
		return fmt.Errorf("%w: after: %v", ErrInvalidEntry, err) //nolint:errorlint // причина текстом
	}
	rowID := e.ID
	if rowID == uuid.Nil {
		rowID = id.New()
	}
	return q.InsertAudit(ctx, auditdb.InsertAuditParams{
		ID:           rowID,
		ActorUserID:  optUUID(e.ActorUserID),
		ActorStaffID: optUUID(e.ActorStaffID),
		ActorRole:    optString(e.ActorRole),
		ActorLabel:   optString(e.ActorLabel),
		Action:       e.Action,
		ObjectType:   e.ObjectType,
		ObjectID:     optUUID(e.ObjectID),
		ScopeCityID:  optUUID(e.ScopeCityID),
		Before:       before,
		After:        after,
		Reason:       optString(e.Reason),
		Ip:           optAddr(e.IP),
		UserAgent:    optString(e.UserAgent),
	})
}

// checkActor повторяет CHECK audit_log (0020): автор — не больше одного; роль обязательна и из
// списка; служебная роль ⇔ нет автора; у сотрудника — роль сотрудника и снимок имени; снимок
// имени — 1–120 символов без крайних пробелов (btrim убирает только пробелы).
func checkActor(e Entry) error {
	user, staff := e.ActorUserID != uuid.Nil, e.ActorStaffID != uuid.Nil
	role := e.ActorRole
	switch {
	case user && staff:
		return fmt.Errorf("%w: два автора — игрок и сотрудник", ErrInvalidEntry)
	case role == "":
		return fmt.Errorf("%w: нет роли автора", ErrInvalidEntry)
	case !playerRoles[role] && !staffRoles[role] && !serviceRoles[role]:
		return fmt.Errorf("%w: роль %q не из списка", ErrInvalidEntry, role)
	case !user && !staff && !serviceRoles[role]:
		return fmt.Errorf("%w: запись без автора — роль system, operator или anonymous, не %q", ErrInvalidEntry, role)
	case (user || staff) && serviceRoles[role]:
		return fmt.Errorf("%w: у автора служебная роль %q", ErrInvalidEntry, role)
	case staff && !staffRoles[role]:
		return fmt.Errorf("%w: у сотрудника роль %q — не роль сотрудника", ErrInvalidEntry, role)
	case staff && e.ActorLabel == "":
		return fmt.Errorf("%w: у сотрудника нет снимка имени", ErrInvalidEntry)
	}
	if e.ActorLabel != "" {
		if n := utf8.RuneCountInString(strings.Trim(e.ActorLabel, " ")); n < 1 || n > maxLabel {
			return fmt.Errorf("%w: снимок имени — 1–%d символов без крайних пробелов", ErrInvalidEntry, maxLabel)
		}
	}
	return nil
}

func optUUID(v uuid.UUID) *uuid.UUID {
	if v == uuid.Nil {
		return nil
	}
	return &v
}

func optString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func optAddr(v netip.Addr) *netip.Addr {
	if !v.IsValid() {
		return nil
	}
	return &v
}
