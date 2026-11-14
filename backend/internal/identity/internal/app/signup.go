package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity/internal/domain"
)

// Регистрация по почте (спека §3.1, §3.16) — владелец Task 10. Сигнатуры — Task 8 (I9), тело — errNotImplemented.

// SignupInput — startSignup.
type SignupInput struct {
	Email, Password, Nickname, Locale string
	BirthDate                         time.Time
	CityID                            uuid.UUID
	Accepted                          []domain.Accepted
}

// ConfirmInput — confirmSignup: код и, если ник заняли, новый ник.
type ConfirmInput struct {
	CodeID         uuid.UUID
	Code, Nickname string
}

// LegalDocument — текущий документ (getLegalDocuments).
type LegalDocument = domain.LegalDoc

// StartSignup — начало регистрации: код или приманка, ответ одинаков.
func (a *App) StartSignup(_ context.Context, _ SignupInput, _ Client) (CodeSent, error) {
	return CodeSent{}, errNotImplemented
}

// ConfirmSignup — аккаунт и сессия по коду.
func (a *App) ConfirmSignup(_ context.Context, _ ConfirmInput, _ Client) (LoginOutcome, error) {
	return LoginOutcome{}, errNotImplemented
}

// LegalDocuments — текущие версии документов (заглушка — пусто).
func (a *App) LegalDocuments() []LegalDocument { return nil }

// CheckNickname — доступность ника (§5.8): reason — invalid | taken | reserved.
func (a *App) CheckNickname(_ context.Context, _ string) (available bool, reason string, err error) {
	return false, "", errNotImplemented
}
