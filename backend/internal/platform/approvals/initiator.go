package approvals

import (
	"context"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
)

// Initiator — инициатор в payload запроса модуля не из staff (R5): id учётки и её версия на момент запроса.
// Имена JSON — те же, что у payload staff (init_id, init_ver): проверка инициатора (staff.checkInitiator) и хук
// замка акторов читают их из payload любого действия. Модуль встраивает Initiator в свой payload рядом с v.
type Initiator struct {
	ID  uuid.UUID `json:"init_id"`
	Ver int64     `json:"init_ver"`
}

// InitiatorOf — инициатор из принципала запроса: версия — из загрузки сессии (auth.Staff.Version). Отставшая
// версия ведёт к отказу при подтверждении, а не к пропуску.
func InitiatorOf(s *auth.Staff) Initiator {
	return Initiator{ID: s.StaffID, Ver: s.Version}
}

// approvalKey — ключ id исполняемого запроса в ctx исполнителя.
type approvalKey struct{}

// ApprovalIDFrom — id запроса, который сейчас исполняется (Approve кладёт его в ctx исполнителя): модуль пишет его в
// свой аудит (approval_id). Вне исполнителя — ok = false.
func ApprovalIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(approvalKey{}).(uuid.UUID)
	return id, ok
}
