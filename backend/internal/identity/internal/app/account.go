package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity/internal/domain"
)

// Мой аккаунт, ник, документы, мои способы входа (спека §3.16, §5.8, §6.2.4) — владелец Task 14. Сигнатуры —
// Task 8 (I9), тело — errNotImplemented.

// MeView — аккаунт игрока (Me).
type MeView struct {
	ID                          uuid.UUID
	Email, Nickname, Locale     string
	Status                      string
	CityID                      uuid.UUID
	CreatedAt                   time.Time
	NicknameChangeableAt        *time.Time
	HasPassword, MfaEnabled     bool
	DocumentsAcceptanceRequired bool
}

// MyAuthMethodsView — мои способы входа.
type MyAuthMethodsView struct {
	HasPassword bool
	MfaEnabled  bool
}

// Me — мой аккаунт.
func (a *App) Me(_ context.Context, _ uuid.UUID) (MeView, error) {
	return MeView{}, errNotImplemented
}

// UpdateMe — язык аккаунта.
func (a *App) UpdateMe(_ context.Context, _ uuid.UUID, _ string) (MeView, error) {
	return MeView{}, errNotImplemented
}

// ChangeNickname — смена ника не чаще 30 дней.
func (a *App) ChangeNickname(_ context.Context, _ uuid.UUID, _ string) (MeView, error) {
	return MeView{}, errNotImplemented
}

// AcceptDocuments — согласие с текущими документами.
func (a *App) AcceptDocuments(_ context.Context, _ uuid.UUID, _ []domain.Accepted, _ domain.ClientKind) (MeView, error) {
	return MeView{}, errNotImplemented
}

// MyAuthMethods — пароль, привязки, ключи, 2FA.
func (a *App) MyAuthMethods(_ context.Context, _ uuid.UUID) (MyAuthMethodsView, error) {
	return MyAuthMethodsView{}, errNotImplemented
}
