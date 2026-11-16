package admin

import (
	"context"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
)

// Сброс 2FA поддержкой (спека §3.15 п. 2–4, §7.1; план I-2, Task 17): тонкие ручки — запрос → сценарий app → ответ
// контракта. Ошибки сценария — httpx.ProblemError (коды x-error-codes операций), прочее — 500.

// RequestMfaReset — POST /v1/identity/users/{userId}/mfa-reset: запрос сброса 2FA игрока — всегда 202 с запросом
// подтверждения identity.mfa_reset (право dual проверил конвейер, сценарий — ещё раз).
func (h *Handler) RequestMfaReset(ctx context.Context, req oapi.RequestMfaResetRequestObject) (oapi.RequestMfaResetResponseObject, error) {
	if h == nil || h.app == nil || h.approvals == nil {
		return nil, errNotImplemented
	}
	st, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	if req.Body == nil {
		return nil, errNoBody
	}
	ap, err := h.app.RequestMfaReset(ctx, h.approvals, st, approvals.MetaFrom(ctx),
		app.MfaResetInput{UserID: req.UserId, Reason: req.Body.Reason, Ticket: req.Body.Ticket})
	if err != nil {
		return nil, err
	}
	out, err := oapi.ApprovalOf(ap)
	if err != nil {
		return nil, err
	}
	return oapi.RequestMfaReset202JSONResponse{Approval: out}, nil
}

// CancelMfaReset — POST /v1/identity/mfa-resets/{resetId}/cancel: отмена назначенного сброса сотрудником — сразу, без
// подтверждения (отмена привилегий не даёт, §7.2).
func (h *Handler) CancelMfaReset(ctx context.Context, req oapi.CancelMfaResetRequestObject) (oapi.CancelMfaResetResponseObject, error) {
	if h == nil || h.app == nil {
		return nil, errNotImplemented
	}
	st, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	out, err := h.app.CancelMfaResetByStaff(ctx, st, approvals.MetaFrom(ctx), req.ResetId)
	if err != nil {
		return nil, err
	}
	return oapi.CancelMfaReset200JSONResponse{Id: out.ID, CancelledAt: out.CancelledAt.UTC()}, nil
}
