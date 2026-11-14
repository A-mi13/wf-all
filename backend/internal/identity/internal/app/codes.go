package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity/internal/store/identitydb"
)

// Коды из письма (спека §3.14) — владелец Task 10 (общие для Task 13). Сигнатуры объявлены Task 8 (I9) с телом
// errNotImplemented: красный шаг TDD владельца идёт через них; новая сигнатура — через контроллера плана.

// CodeMode — что checkCode делает с верным кодом (N7).
type CodeMode int

const (
	// CodeConsume — верный код гасится сразу (consumed_at, code_enc = NULL).
	CodeConsume CodeMode = iota
	// CodeVerifyOnly — только сверка без записей; гасит вызывающий consumeCode в своей tx (Task 13).
	CodeVerifyOnly
)

// StartCode — новый код: назначение, почта, аккаунт (если есть), приманка, данные для payload_enc.
type StartCode struct {
	Purpose identitydb.AuthCodePurpose
	Email   string
	UserID  uuid.UUID // uuid.Nil — без аккаунта
	Decoy   bool
	Payload []byte // данные регистрации (signup) или новая почта (email_change)
	Client  Client
}

// CodeSent — ответ о коде (202 CodeSent).
type CodeSent struct {
	CodeID          uuid.UUID
	ExpiresAt       time.Time
	ResendAvailable time.Time
}

// codeRow — строка auth_codes под замком.
type codeRow = identitydb.AuthCode

// startCode — один живой код на (purpose, email): прежний отзывается; приманка — без секретов.
func (a *App) startCode(_ context.Context, _ pgx.Tx, _ StartCode) (CodeSent, error) {
	return CodeSent{}, errNotImplemented
}

// checkCode — сверка кода приёмом R30 (lockByRow); mode — гасить ли верный.
func (a *App) checkCode(_ context.Context, _ pgx.Tx, _ identitydb.AuthCodePurpose, _ uuid.UUID, _ string, _ CodeMode) (codeRow, error) {
	return codeRow{}, errNotImplemented
}

// consumeCode — погашение кода, сверенного в CodeVerifyOnly.
func (a *App) consumeCode(_ context.Context, _ pgx.Tx, _ uuid.UUID) error {
	return errNotImplemented
}

// ResendCode — повторная отправка кода (паузы domain.ResendAvailableAt).
func (a *App) ResendCode(_ context.Context, _ uuid.UUID) (CodeSent, error) {
	return CodeSent{}, errNotImplemented
}

// заглушки ещё не вызываются — ссылка для линтера unused; убирает Task 10
var _ = []any{(*App).startCode, (*App).checkCode, (*App).consumeCode}
