package app

import "github.com/google/uuid"

// Для тестов: секрет TOTP и резервные коды тем же путём, что у выдачи (Task 7).

func (a *App) SealTOTP(staffID uuid.UUID, secret []byte) ([]byte, string, error) {
	return a.sealTOTP(staffID, secret)
}

func (a *App) RecoveryMAC(keyID string, staffID uuid.UUID, norm string) ([]byte, error) {
	return a.recoveryMAC(keyID, staffID, norm)
}

func (a *App) LoginEmailKey(email string) (string, error) { return a.loginEmailKey(email) }

// Сроки — тестам блокировки и сессии.
const (
	MaxFactorFailures = maxFactorFailures
	ReplacedGrace     = replacedGrace
)
