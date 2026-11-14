package app

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// «Где вы вошли» и способы входа (спека §3.7, §4.4) — владелец Task 12. Сигнатуры — Task 8 (I9), тело —
// errNotImplemented; владелец может вынести AuthMethods в methods.go.

// SessionView — сессия в списке (IP не отдаётся).
type SessionView struct {
	ID                                     uuid.UUID
	Platform, Model, OSVersion, AppVersion string
	Browser, OS, AuthMethod, City          string
	CreatedAt, LastSeenAt                  time.Time
	Current                                bool
}

// ListSessions — живые сессии аккаунта.
func (a *App) ListSessions(_ context.Context, _, _ uuid.UUID) ([]SessionView, error) {
	return nil, errNotImplemented
}

// RevokeSession — «выйти здесь»: чужая или нет — 404 identity.session_not_found.
func (a *App) RevokeSession(_ context.Context, _, _ uuid.UUID) error {
	return errNotImplemented
}

// RevokeOthers — «выйти везде, кроме этого».
func (a *App) RevokeOthers(_ context.Context, _, _ uuid.UUID) (int, error) {
	return 0, errNotImplemented
}

// AuthMethodsView — способы входа (getAuthMethods).
type AuthMethodsView struct {
	Password                                           bool
	AppleSignup, AppleLogin, GoogleSignup, GoogleLogin bool
	PasskeysRegister, PasskeysLogin                    bool
}

// AuthMethods — флаг и готовность провайдера (R4).
func (a *App) AuthMethods(_ context.Context) (AuthMethodsView, error) {
	return AuthMethodsView{}, errNotImplemented
}
