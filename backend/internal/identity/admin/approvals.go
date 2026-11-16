package admin

import "wf/backend/internal/platform/approvals"

// RegisterApprovals — действие identity «вдвоём» (identity.mfa_reset, спека §3.15, §7.1) в реестре подтверждений и
// сервис для запросов сброса. Сборка admin-api зовёт до reg.Validate() (как geo); действие с LockActors — без хука
// замка акторов staff Validate откажет. Нулевой Handler ничего не регистрирует — его операции отвечают 501.
func (h *Handler) RegisterApprovals(r *approvals.Registry, svc *approvals.Service) {
	if h == nil || h.app == nil {
		return
	}
	h.approvals = svc
	r.Register(h.app.MfaResetAction())
}
