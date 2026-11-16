package httpapi

import (
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity/internal/app"
)

// authResult — единственная сборка AuthResult (§6.2.1) для всех сценариев входа: пароль, код из письма, отмена
// удаления, второй шаг. Ветка по status; documents_acceptance_required посчитан в транзакции сценария
// (app.AuthOutcome) — после коммита база не читается. newUser — только у подтверждения регистрации.
func authResult(out app.AuthOutcome, newUser bool) oapi.AuthResult {
	r := oapi.AuthResult{Status: oapi.AuthStatus(out.Status)}
	switch out.Status {
	case app.StatusAuthenticated:
		if out.Issued != nil {
			t := tokenPair(out.Issued.Tokens)
			r.Tokens = &t
		}
		r.User = &oapi.AuthUser{Id: out.User.ID, Nickname: out.User.Nickname, NewUser: newUser,
			DocumentsAcceptanceRequired: out.DocumentsRequired}
	case app.StatusMfaRequired:
		methods := make([]oapi.MfaMethod, 0, len(out.Methods))
		for _, m := range out.Methods {
			methods = append(methods, oapi.MfaMethod(m))
		}
		r.Mfa = &oapi.MfaChallenge{MfaToken: out.MfaToken, ExpiresAt: out.MfaExpires, Methods: methods, ResetAt: out.ResetAt}
	case app.StatusDeletionPending:
		r.Deletion = &oapi.DeletionPending{RestoreToken: out.RestoreToken, ExpiresAt: out.RestoreExpires, PurgeAt: out.PurgeAt}
	}
	return r
}

// tokenPair — пара токенов сессии в форме контракта.
func tokenPair(p app.TokenPair) oapi.TokenPair {
	return oapi.TokenPair{AccessToken: p.Access, AccessExpiresAt: p.AccessExpires, RefreshToken: p.Refresh,
		RefreshIdleExpiresAt: p.RefreshIdleExpires, SessionId: p.SessionID}
}
