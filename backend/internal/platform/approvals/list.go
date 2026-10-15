package approvals

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals/approvalsdb"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/page"
)

// listSet — набор полей курсора списка (page.Keyset): поменял порядок — поменяй.
const listSet = "approvals.list(requested_at,id)"

var listKinds = []page.Kind{page.KindTime, page.KindUUID}

// List — страница запросов (GET /v1/approvals): новые сверху (requested_at DESC, id DESC), next —
// курсор следующей страницы ("" — страниц больше нет). box: BoxMine — созданные смотрящим;
// BoxIncoming — которые он может решить: не его и не о нём, право запроса у него allow|dual, или
// scoped и город запроса в его скоупе (как Decide). status "" — все состояния. Фильтр прав — в SQL:
// страница не «худеет», лимит честный. limit — page.Limit (0 — 20, больше 100 — 100).
func (s *Service) List(ctx context.Context, st *auth.Staff, box, status, cursor string, limit int) ([]Approval, string, error) {
	if !active(st) {
		return nil, "", ErrForbidden
	}
	if box != BoxIncoming && box != BoxMine {
		return nil, "", httpx.NewFieldError("query.box", "enum")
	}
	if status != "" && !slices.Contains(statuses, status) {
		return nil, "", httpx.NewFieldError("query.status", "enum")
	}
	lim := page.Limit(&limit)
	arg := approvalsdb.ListApprovalsParams{
		Mine: box == BoxMine, Viewer: st.StaffID,
		Allowed: []string{}, Scoped: []string{}, CityIds: []uuid.UUID{},
		Lim: int32(lim + 1), //nolint:gosec // page.Limit ≤ 100
	}
	if status != "" {
		arg.Status = &status
	}
	if box == BoxIncoming {
		for key, g := range st.Grants {
			switch g {
			case auth.GrantAllow, auth.GrantDual:
				arg.Allowed = append(arg.Allowed, key)
			case auth.GrantScoped:
				arg.Scoped = append(arg.Scoped, key)
			}
		}
		// как auth.Staff.InScope: глобальная роль — любой город, модератор — только свои
		arg.ScopedAnyCity = st.Role != auth.RoleModerator
		arg.CityIds = append(arg.CityIds, st.CityIDs...)
	}
	if cursor != "" {
		k, err := page.DecodeKeyset(cursor, listSet, listKinds...)
		if err != nil {
			return nil, "", err
		}
		// типы гарантирует DecodeKeyset по listKinds
		arg.HasCursor, arg.CursorAt, arg.CursorID = true, k.Values[0].(time.Time), k.Values[1].(uuid.UUID)
	}
	rows, err := approvalsdb.New(s.pool).ListApprovals(ctx, arg)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > lim {
		rows = rows[:lim]
		last := rows[lim-1]
		next = page.EncodeKeyset(page.Keyset{Set: listSet, Values: []any{last.RequestedAt.UTC(), last.ID}}, listKinds...)
	}
	items := make([]Approval, 0, len(rows))
	for _, r := range rows {
		items = append(items, toApproval(r))
	}
	return items, next, nil
}
