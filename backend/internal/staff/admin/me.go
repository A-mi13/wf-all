package admin

import (
	"context"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/staff"
)

// «Мой доступ» (спека §4.7, R15): кто вошёл, язык, второй фактор, пароль, свои сессии. Каждый запрос —
// от сотрудника конвейера (current): чужой id сессии — staff.session_not_found, как несуществующий.

// GetStaffMe — GET /v1/staff/me (любая стадия и Reauth).
func (h *Handler) GetStaffMe(ctx context.Context, _ oapi.GetStaffMeRequestObject) (oapi.GetStaffMeResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.app.Me(ctx, s)
	if err != nil {
		return nil, err
	}
	return oapi.GetStaffMe200JSONResponse(meOut(v)), nil
}

// meOut — StaffMe (R15): city_ids и permissions — всегда массив и объект (не null); в mfa_pending —
// без полей учётки, но с сессией (экран входа сравнивает id брошенной сессии, Task 13 N2); в setup —
// учётка без прав.
func meOut(v staff.MeView) oapi.StaffMe {
	out := oapi.StaffMe{
		CityIds:        append(make([]uuid.UUID, 0, len(v.CityIDs)), v.CityIDs...),
		Stage:          oapi.StaffStage(v.Stage),
		ReauthRequired: v.ReauthRequired,
		Permissions:    make(map[string]oapi.PermissionGrant, len(v.Permissions)),
	}
	for k, g := range v.Permissions {
		out.Permissions[k] = oapi.PermissionGrant(g.String())
	}
	if v.Session != nil {
		out.Session = &oapi.StaffMeSession{Id: v.Session.ID, ExpiresAt: v.Session.ExpiresAt.UTC()}
	}
	if a := v.Account; a != nil {
		locale, role := oapi.StaffLocale(a.Locale), oapi.StaffRole(a.Role)
		out.Id, out.Email, out.DisplayName = &a.ID, &a.Email, &a.DisplayName
		out.Locale, out.Role = &locale, &role
	}
	return out
}

// UpdateStaffMeLocale — PATCH /v1/staff/me/locale: язык админки и писем, 204.
func (h *Handler) UpdateStaffMeLocale(ctx context.Context, req oapi.UpdateStaffMeLocaleRequestObject) (oapi.UpdateStaffMeLocaleResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.app.SetLocale(ctx, s, string(req.Body.Locale), meta(ctx)); err != nil {
		return nil, err
	}
	return oapi.UpdateStaffMeLocale204Response{}, nil
}

// GetStaffMfa — GET /v1/staff/me/mfa: когда настроен второй фактор и сколько резервных кодов осталось.
func (h *Handler) GetStaffMfa(ctx context.Context, _ oapi.GetStaffMfaRequestObject) (oapi.GetStaffMfaResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.app.MFAStatus(ctx, s)
	if err != nil {
		return nil, err
	}
	out := oapi.GetStaffMfa200JSONResponse{RecoveryCodesLeft: v.RecoveryCodesLeft}
	if v.ConfirmedAt != nil {
		at := v.ConfirmedAt.UTC()
		out.ConfirmedAt = &at
	}
	return out, nil
}

// RegenerateStaffRecoveryCodes — POST /v1/staff/me/mfa/recovery-codes: код из приложения → 10 новых
// резервных кодов; no-store, в idempotency_keys не сохраняется.
func (h *Handler) RegenerateStaffRecoveryCodes(ctx context.Context, req oapi.RegenerateStaffRecoveryCodesRequestObject) (oapi.RegenerateStaffRecoveryCodesResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	codes, err := h.app.RegenerateRecovery(ctx, s, req.Body.Code, meta(ctx))
	if err != nil {
		return nil, err
	}
	return oapi.RegenerateStaffRecoveryCodes200JSONResponse{
		Body:    oapi.StaffRecoveryCodes{RecoveryCodes: codes},
		Headers: oapi.RegenerateStaffRecoveryCodes200ResponseHeaders{CacheControl: noStore},
	}, nil
}

// ChangeStaffPassword — POST /v1/staff/me/password: текущий пароль, второй фактор и новый пароль, 204;
// другие свои сессии гасятся.
func (h *Handler) ChangeStaffPassword(ctx context.Context, req oapi.ChangeStaffPasswordRequestObject) (oapi.ChangeStaffPasswordResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if err := h.app.ChangePassword(ctx, s, b.CurrentPassword, deref(b.Code), deref(b.RecoveryCode), b.NewPassword, meta(ctx)); err != nil {
		return nil, err
	}
	return oapi.ChangeStaffPassword204Response{}, nil
}

// ListStaffMySessions — GET /v1/staff/me/sessions: свои живые сессии, текущая отмечена.
func (h *Handler) ListStaffMySessions(ctx context.Context, _ oapi.ListStaffMySessionsRequestObject) (oapi.ListStaffMySessionsResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	views, err := h.app.MySessions(ctx, s)
	if err != nil {
		return nil, err
	}
	return oapi.ListStaffMySessions200JSONResponse{Items: sessionsOut(views)}, nil
}

// sessionsOut — StaffSession (общая с карточкой сотрудника 10b): items — всегда массив; пустой
// User-Agent в базе сценарий отдаёт nil — в ответе null (⚠️ 9a).
func sessionsOut(views []staff.SessionView) []oapi.StaffSession {
	out := make([]oapi.StaffSession, 0, len(views))
	for _, v := range views {
		out = append(out, oapi.StaffSession{
			Id: v.ID, Stage: oapi.StaffStage(v.Stage), Current: v.Current, Ip: v.IP, UserAgent: v.UserAgent,
			CreatedAt: v.CreatedAt.UTC(), LastSeenAt: v.LastSeenAt.UTC(), ExpiresAt: v.ExpiresAt.UTC(),
		})
	}
	return out
}

// RevokeStaffMySession — DELETE /v1/staff/me/sessions/{sessionId}: завершить свою сессию, 204. Текущую
// тоже можно — cookie не стирается, следующий запрос встретит конвейер (401 и стирание).
func (h *Handler) RevokeStaffMySession(ctx context.Context, req oapi.RevokeStaffMySessionRequestObject) (oapi.RevokeStaffMySessionResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.app.RevokeMySession(ctx, s, req.SessionId, meta(ctx)); err != nil {
		return nil, err
	}
	return oapi.RevokeStaffMySession204Response{}, nil
}

// RevokeStaffOtherSessions — POST /v1/staff/me/sessions/revoke-others: все свои сессии, кроме текущей;
// ответ — сколько завершено.
func (h *Handler) RevokeStaffOtherSessions(ctx context.Context, _ oapi.RevokeStaffOtherSessionsRequestObject) (oapi.RevokeStaffOtherSessionsResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.app.RevokeOtherSessions(ctx, s, meta(ctx))
	if err != nil {
		return nil, err
	}
	return oapi.RevokeStaffOtherSessions200JSONResponse{Revoked: n}, nil
}
