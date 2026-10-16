package oapi

// Ручной файл рядом со сгенерированным api.gen.go (oapi-codegen пишет только его): перевод запроса на
// подтверждение платформы в схему контракта. Им пользуются и httpapi/admin (операции подтверждений), и
// staff/admin (202 {approval}): staff/admin не может импортировать сборку сервера (archtest), а
// платформа не знает типов контракта.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
)

// ApprovalOf — запрос на подтверждение как схема Approval. Пустые значения платформы (uuid.Nil, "",
// nil) — null контракта; решившая система (DecidedBy == uuid.Nil) — decided_by null при времени
// решения. payload — JSON-объект; null или пусто — пустой объект; иное — ошибка (payload пишет модуль,
// это сбой, а не ввод клиента).
func ApprovalOf(a approvals.Approval) (Approval, error) {
	payload := map[string]any{}
	if raw := bytes.TrimSpace(a.Payload); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		// не объект (массив, строка, число) в карту не разбирается — ошибка
		if err := json.Unmarshal(raw, &payload); err != nil {
			return Approval{}, fmt.Errorf("oapi: payload запроса %s: %w", a.ID, err)
		}
	}
	out := Approval{
		Id: a.ID, Action: ApprovalAction(a.Action), Permission: a.Permission, Status: ApprovalStatus(a.Status),
		Payload:     payload,
		TargetId:    optID(a.TargetID),
		ScopeCityId: optID(a.ScopeCityID),
		RequestedBy: ApprovalActor{Id: a.RequestedBy, Label: a.RequestedByLabel},
		RequestedAt: a.RequestedAt.UTC(), ExpiresAt: a.ExpiresAt.UTC(),
		DecidedAt: utc(a.DecidedAt), Comment: optText(a.Comment), Error: optText(a.Error),
	}
	if a.DecidedBy != uuid.Nil {
		out.DecidedBy = &ApprovalActor{Id: a.DecidedBy, Label: a.DecidedByLabel}
	}
	return out, nil
}

// ApprovalsOf — страница запросов: items — всегда массив (не null).
func ApprovalsOf(list []approvals.Approval) ([]Approval, error) {
	out := make([]Approval, 0, len(list))
	for _, a := range list {
		v, err := ApprovalOf(a)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func optID(u uuid.UUID) *uuid.UUID {
	if u == uuid.Nil {
		return nil
	}
	return &u
}

func optText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// utc — указатель на время в UTC (nil — nil).
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
