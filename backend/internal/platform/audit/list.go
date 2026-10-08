package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit/auditdb"
	"wf/backend/internal/platform/page"
)

// listSet — набор полей курсора журнала (page.Keyset, R25 плана geo): поменял порядок — поменяй.
const listSet = "audit.log(created_at,id)"

// ListParams — фильтры журнала действий (GET /v1/audit, спека §5.3). Нулевое значение поля —
// фильтра нет. From — включительно, To — исключительно.
type ListParams struct {
	ActorStaffID uuid.UUID
	ActionPrefix string // "staff." — все действия модуля; совпадение с начала строки, без шаблонов
	ObjectType   string
	ObjectID     uuid.UUID
	From, To     time.Time
	// CityIDs != nil — журнал модератора: записи этих городов (scope_city_id) и его собственные
	// (actor_staff_id = OwnStaffID). Пустой, но не nil срез — только собственные.
	CityIDs    []uuid.UUID
	OwnStaffID uuid.UUID
	Cursor     string // из next прошлой страницы; негодный — page.ErrBadCursor
	Limit      int    // 0 — 20, больше 100 — 100 (page.Limit)
}

// Record — запись журнала для чтения. Before/After уже маскированы при записи (Mask); IP
// маскируется здесь: подсеть /24 (IPv4) или /48 (IPv6), полный адрес наружу не уходит.
// Нулевые значения — NULL в базе.
type Record struct {
	ID                        uuid.UUID
	ActorUserID, ActorStaffID uuid.UUID
	ActorLabel                string
	ActorRole                 string
	Action, ObjectType        string
	ObjectID, ScopeCityID     uuid.UUID
	Before, After             json.RawMessage
	Reason                    string
	IP                        string // "203.0.113.0/24" | "2001:db8:abcd::/48" | ""
	UserAgent                 string
	CreatedAt                 time.Time
}

// List — страница журнала: новые сверху (created_at DESC, id DESC), next — курсор следующей
// страницы ("" — страниц больше нет). Чтение журнала не аудируется (спека §4.12).
func List(ctx context.Context, q auditdb.DBTX, p ListParams) (items []Record, next string, err error) {
	limit := page.Limit(&p.Limit)
	arg := auditdb.ListAuditParams{
		ActorStaffID: optUUID(p.ActorStaffID),
		ActionPrefix: optString(p.ActionPrefix),
		ObjectType:   optString(p.ObjectType),
		ObjectID:     optUUID(p.ObjectID),
		FromAt:       optTime(p.From),
		ToAt:         optTime(p.To),
		Scoped:       p.CityIDs != nil,
		CityIds:      p.CityIDs,
		OwnStaffID:   p.OwnStaffID,
		Lim:          int32(limit + 1), //nolint:gosec // page.Limit ≤ 100
	}
	if p.Cursor != "" {
		k, err := page.DecodeKeyset(p.Cursor, listSet, page.KindTime, page.KindUUID)
		if err != nil {
			return nil, "", err
		}
		arg.HasCursor, arg.CursorAt, arg.CursorID = true, k.Values[0].(time.Time), k.Values[1].(uuid.UUID)
	}
	rows, err := auditdb.New(q).ListAudit(ctx, arg)
	if err != nil {
		return nil, "", err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = page.EncodeKeyset(page.Keyset{Set: listSet, Values: []any{last.CreatedAt, last.ID}},
			page.KindTime, page.KindUUID)
	}
	items = make([]Record, 0, len(rows))
	for _, r := range rows {
		items = append(items, record(r))
	}
	return items, next, nil
}

// Get — одна запись журнала по id (письмо о действии, Task 8b/10c): поля и маска IP — как в List;
// нет записи — ok = false без ошибки.
func Get(ctx context.Context, q auditdb.DBTX, id uuid.UUID) (Record, bool, error) {
	r, err := auditdb.New(q).GetAudit(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	return record(auditdb.ListAuditRow(r)), true, nil
}

// record — строка журнала для чтения; Get приводит свою строку к ListAuditRow (те же колонки).
func record(r auditdb.ListAuditRow) Record {
	return Record{
		ID: r.ID, ActorUserID: val(r.ActorUserID), ActorStaffID: val(r.ActorStaffID),
		ActorLabel: val(r.ActorLabel), ActorRole: val(r.ActorRole),
		Action: r.Action, ObjectType: r.ObjectType,
		ObjectID: val(r.ObjectID), ScopeCityID: val(r.ScopeCityID),
		Before: r.Before, After: r.After, Reason: val(r.Reason),
		IP: maskIP(r.Ip), UserAgent: val(r.UserAgent), CreatedAt: r.CreatedAt,
	}
}

// MaskIP — подсеть адреса для показа: /24 у IPv4 (и IPv4 внутри IPv6), /48 у IPv6. Тем же
// правилом маскируют IP сессий и писем (спека §4.7, §4.12).
func MaskIP(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	a = a.Unmap()
	bits := 48
	if a.Is4() {
		bits = 24
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.String()
}

func maskIP(a *netip.Addr) string {
	if a == nil {
		return ""
	}
	return MaskIP(*a)
}

func val[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func optTime(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}
