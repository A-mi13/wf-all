package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/staff"
	staffadmin "wf/backend/internal/staff/admin"
)

// Server собирает API админки: встраивает хендлеры модулей (internal/<модуль>/admin),
// операции платформы (тег platform) реализует сам. Компилятор ловит и нереализованную
// операцию, и реализованную двумя модулями (неоднозначный метод) — спека §8.2.
// Хендлеры модулей встроены указателями: модуль строит *Handler конструктором, методы — с
// указателем-получателем (собирает NewHandler из Options.Staff и Options.Geo).
type Server struct {
	*staffHandler // тег staff: вход, сессии, «Мой доступ», роли и доступ сотрудников
	*geoHandler   // тег geo: города для выбора городов модератора

	// операции платформы; нулевые (Server{} в тестах конвейера) — 501 http.not_implemented
	approvals ApprovalService                                    // подтверждения вдвоём
	label     func(context.Context, *auth.Staff) (string, error) // подпись решившего (Staff.Label)
	audit     *pgxpool.Pool                                      // журнал действий (роль admin)
}

// Псевдонимы — имена встроенных полей: хендлеры модулей называются одинаково (Handler), а имя
// встроенного поля — имя типа. Модуль-реализатора apitest.Owners берёт из пакета типа.
type (
	staffHandler = staffadmin.Handler
	geoHandler   = geoadmin.Handler
)

var _ oapi.StrictServerInterface = Server{}

var (
	errNotImplemented  = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)
	errUnauthenticated = httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthenticated)
	errForbidden       = httpx.NewError(http.StatusForbidden, httpx.CodeForbidden)
)

// metaKey — approvals.Meta запроса в ctx операций (requestMeta).
type metaKey struct{}

// requestMeta — strict-middleware операций платформы (сборка ставит его внешним, после Middleware staff):
//   - откуда запрос (IP — peer, User-Agent — httpx.UserAgent: ≤ 512 байт) в ctx: strict-сервер не передаёт операции
//     *http.Request, а meta модуля staff заполняет только его Middleware и только для операций тега staff
//     (ревью 10a, отказ 3);
//   - auth.ErrSessionInvalid операции (подпись решившего: сессию погасили или учётку выключили после
//     конвейера) — 401 и стирание cookie clearCookie, как у Middleware staff (ревью 10b m3). Ошибки операций
//     staff сюда приходят уже переведёнными (Problem) — стирания второй раз нет.
func requestMeta(clearCookie *http.Cookie) oapi.StrictMiddlewareFunc {
	return func(f oapi.StrictHandlerFunc, _ string) oapi.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
			ctx = context.WithValue(ctx, metaKey{}, approvals.Meta{IP: peer.From(ctx).IP, UserAgent: httpx.UserAgent(r)})
			resp, err := f(ctx, w, r, req)
			if err != nil && errors.Is(err, auth.ErrSessionInvalid) {
				http.SetCookie(w, clearCookie)
				return nil, staffadmin.Problem(err)
			}
			return resp, err
		}
	}
}

// metaFrom — IP и User-Agent запроса для аудита решений.
func metaFrom(ctx context.Context) approvals.Meta {
	m, _ := ctx.Value(metaKey{}).(approvals.Meta)
	return m
}

// Операции платформы (тег platform): подтверждения вдвоём (спека §4.8) и журнал действий (§5.3). Тонкие:
// запрос → approvals.Service / audit.List → ответ контракта. Ошибки — через staffadmin.Problem (единственный
// перевод ошибок staff и платформы: ProblemError как есть, коды модуля — их статусы, сессия — 401).

const permAuditRead = "audit.log.read"

// approveStaffCodes — коды модуля staff, которыми может ответить approve (x-error-codes approveApproval):
// бизнес-отказы исполнителей, каждый — закоммиченный failed. Иной код staff.* (staff.not_found: исполнитель
// его не порождает, отсутствующая цель — ErrStale) клиенту не уходит — 500 internal (страж
// TestApproveCodesMatchContract сверяет список с контрактом).
var approveStaffCodes = []string{staff.CodeLastOwner, staff.CodeEmailTaken, staff.CodeCityNotFound}

// staffFrom — сотрудник запроса (конвейер). Нет — 401: без сессии до операций платформы не доходят
// (валидатор), это защита в глубину.
func staffFrom(ctx context.Context) (*auth.Staff, error) {
	st, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	return st, nil
}

// decider — решающий и его подпись (снимок имени для approvals и журнала). Подпись перечитывает учётку и
// сессию (Staff.Label): выключен или сессия погашена после загрузки конвейером — 401 со стиранием cookie
// (auth.ErrSessionInvalid уходит как есть — переводит requestMeta), роль сменилась — 403, до любых записей.
func (s Server) decider(ctx context.Context) (*auth.Staff, string, error) {
	st, err := staffFrom(ctx)
	if err != nil {
		return nil, "", err
	}
	label, err := s.label(ctx, st)
	if errors.Is(err, auth.ErrSessionInvalid) {
		return nil, "", fmt.Errorf("admin: подпись решившего: %w", err)
	}
	if err != nil {
		return nil, "", staffadmin.Problem(err)
	}
	return st, label, nil
}

// ListApprovals — GET /v1/approvals: входящие (box=incoming — которые можно решить) или свои (mine), новые
// сверху; ожидающий с вышедшим сроком — expired.
func (s Server) ListApprovals(ctx context.Context, req oapi.ListApprovalsRequestObject) (oapi.ListApprovalsResponseObject, error) {
	if s.approvals == nil {
		return nil, errNotImplemented
	}
	st, err := staffFrom(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	var status, cursor string
	var limit int
	if p.Status != nil {
		status = string(*p.Status)
	}
	if p.Cursor != nil {
		cursor = *p.Cursor
	}
	if p.Limit != nil {
		limit = *p.Limit
	}
	list, next, err := s.approvals.List(ctx, st, string(p.Box), status, cursor, limit)
	if err != nil {
		return nil, staffadmin.Problem(err)
	}
	items, err := oapi.ApprovalsOf(list)
	if err != nil {
		return nil, err
	}
	out := oapi.ListApprovals200JSONResponse{Items: items}
	if next != "" {
		out.NextCursor = &next
	}
	return out, nil
}

// ApproveApproval — POST /v1/approvals/{approvalId}/approve: подтверждение и исполнение в одной транзакции.
// 200 — только executed (контракт d347777). Неудача исполнения — ошибка с кодом, запрос уже failed в базе:
// ErrStale (и ErrInitiatorChanged) — 409 platform.approval_stale; бизнес-отказ исполнителя — его код
// (approveStaffCodes); отказ без кода — 500. Срок вышел — expired закоммичен, 409 closed. Под
// Idempotency-Key — одна закоммиченная транзакция (Approve): 409 сохраняется в ключ.
func (s Server) ApproveApproval(ctx context.Context, req oapi.ApproveApprovalRequestObject) (oapi.ApproveApprovalResponseObject, error) {
	if s.approvals == nil {
		return nil, errNotImplemented
	}
	st, label, err := s.decider(ctx)
	if err != nil {
		return nil, err
	}
	var comment string
	if req.Body != nil && req.Body.Comment != nil {
		comment = *req.Body.Comment
	}
	ap, err := s.approvals.Approve(ctx, st, label, metaFrom(ctx), req.ApprovalId, comment)
	if err != nil {
		return nil, approveProblem(err)
	}
	if ap.Status != approvals.StatusExecuted {
		return nil, fmt.Errorf("admin: approve %s без ошибки, но в состоянии %s", ap.ID, ap.Status)
	}
	out, err := oapi.ApprovalOf(ap)
	if err != nil {
		return nil, err
	}
	return oapi.ApproveApproval200JSONResponse(out), nil
}

// approveProblem — ошибка approve клиенту: код staff.* — только из approveStaffCodes, иной — 500 (без
// обёртки %w: Problem не должен узнать в нём код модуля); остальное — Problem.
func approveProblem(err error) error {
	if code, ok := staff.CodeOf(err); ok && !slices.Contains(approveStaffCodes, code) {
		return fmt.Errorf("admin: approve: исполнитель вернул код %s вне x-error-codes approveApproval", code)
	}
	return staffadmin.Problem(err)
}

// RejectApproval — POST /v1/approvals/{approvalId}/reject: отклонение с обязательным комментарием.
func (s Server) RejectApproval(ctx context.Context, req oapi.RejectApprovalRequestObject) (oapi.RejectApprovalResponseObject, error) {
	if s.approvals == nil {
		return nil, errNotImplemented
	}
	st, label, err := s.decider(ctx)
	if err != nil {
		return nil, err
	}
	var comment string
	if req.Body != nil {
		comment = req.Body.Comment
	}
	ap, err := s.approvals.Reject(ctx, st, label, metaFrom(ctx), req.ApprovalId, comment)
	if err != nil {
		return nil, staffadmin.Problem(err)
	}
	out, err := oapi.ApprovalOf(ap)
	if err != nil {
		return nil, err
	}
	return oapi.RejectApproval200JSONResponse(out), nil
}

// CancelApproval — POST /v1/approvals/{approvalId}/cancel: инициатор отзывает свой ожидающий запрос; чужой —
// 404 (существование не раскрывается).
func (s Server) CancelApproval(ctx context.Context, req oapi.CancelApprovalRequestObject) (oapi.CancelApprovalResponseObject, error) {
	if s.approvals == nil {
		return nil, errNotImplemented
	}
	st, label, err := s.decider(ctx)
	if err != nil {
		return nil, err
	}
	ap, err := s.approvals.Cancel(ctx, st, label, metaFrom(ctx), req.ApprovalId)
	if err != nil {
		return nil, staffadmin.Problem(err)
	}
	out, err := oapi.ApprovalOf(ap)
	if err != nil {
		return nil, err
	}
	return oapi.CancelApproval200JSONResponse(out), nil
}

// ListAudit — GET /v1/audit: журнал, новые сверху. Модератору (право scoped) — записи его городов и свои:
// скоуп по GrantScoped, а не по len(CityIDs) — модератору всегда не-nil срез (без городов — только свои
// записи, ⚠️ 3b). from не раньше to — 400 query.from range. Чтение журнала не аудируется.
func (s Server) ListAudit(ctx context.Context, req oapi.ListAuditRequestObject) (oapi.ListAuditResponseObject, error) {
	if s.audit == nil {
		return nil, errNotImplemented
	}
	st, err := staffFrom(ctx)
	if err != nil {
		return nil, err
	}
	q := req.Params
	p := audit.ListParams{}
	switch st.Grant(permAuditRead) {
	case auth.GrantDeny:
		return nil, errForbidden
	case auth.GrantScoped:
		p.CityIDs, p.OwnStaffID = append(make([]uuid.UUID, 0, len(st.CityIDs)), st.CityIDs...), st.StaffID
	}
	if q.ActorStaffId != nil {
		p.ActorStaffID = *q.ActorStaffId
	}
	if q.ActionPrefix != nil {
		p.ActionPrefix = *q.ActionPrefix
	}
	if q.ObjectType != nil {
		p.ObjectType = *q.ObjectType
	}
	if q.ObjectId != nil {
		p.ObjectID = *q.ObjectId
	}
	if q.From != nil {
		p.From = q.From.UTC()
	}
	if q.To != nil {
		p.To = q.To.UTC()
	}
	if q.From != nil && q.To != nil && !p.From.Before(p.To) {
		return nil, httpx.NewFieldError("query.from", "range")
	}
	if q.Cursor != nil {
		p.Cursor = *q.Cursor
	}
	if q.Limit != nil {
		p.Limit = *q.Limit
	}
	items, next, err := audit.List(ctx, s.audit, p)
	if err != nil {
		return nil, err // в том числе page.ErrBadCursor — 400 по query.cursor
	}
	out := oapi.ListAudit200JSONResponse{Items: make([]oapi.AuditRecord, 0, len(items))}
	for _, r := range items {
		rec, err := auditOut(r)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, rec)
	}
	if next != "" {
		out.NextCursor = &next
	}
	return out, nil
}

// auditOut — AuditRecord: пустые значения — null; before/after — объекты (уже маскированы при записи).
func auditOut(r audit.Record) (oapi.AuditRecord, error) {
	before, err := jsonObject(r.Before)
	if err != nil {
		return oapi.AuditRecord{}, fmt.Errorf("admin: журнал %s, before: %w", r.ID, err)
	}
	after, err := jsonObject(r.After)
	if err != nil {
		return oapi.AuditRecord{}, fmt.Errorf("admin: журнал %s, after: %w", r.ID, err)
	}
	return oapi.AuditRecord{
		Id: r.ID, Action: r.Action, ObjectType: r.ObjectType, CreatedAt: r.CreatedAt.UTC(),
		ActorStaffId: optID(r.ActorStaffID), ActorUserId: optID(r.ActorUserID),
		ActorLabel: optText(r.ActorLabel), ActorRole: optText(r.ActorRole),
		ObjectId: optID(r.ObjectID), ScopeCityId: optID(r.ScopeCityID),
		Before: before, After: after,
		Reason: optText(r.Reason), Ip: optText(r.IP), UserAgent: optText(r.UserAgent),
	}, nil
}

// jsonObject — JSON-объект как карта; NULL или null — nil (null в ответе).
func jsonObject(raw json.RawMessage) (*map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
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
