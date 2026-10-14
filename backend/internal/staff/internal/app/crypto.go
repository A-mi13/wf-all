package app

import (
	"crypto/hmac"

	"github.com/google/uuid"

	"wf/backend/internal/platform/ratelimit"
)

// Секрет TOTP шифруется кольцом ключей, aad — id сотрудника: шифротекст другой учётки не
// расшифруется (спека §4.1, поправка «Ключи»). Одна пара функций для настройки (Task 7) и входа.
func totpAAD(staffID uuid.UUID) []byte { return staffID[:] }

// sealTOTP — секрет TOTP для staff_mfa: шифротекст и key_id текущего ключа.
func (a *App) sealTOTP(staffID uuid.UUID, secret []byte) (ct []byte, keyID string, err error) {
	return a.keys.Seal(secret, totpAAD(staffID))
}

// openTOTP — секрет TOTP из staff_mfa.
func (a *App) openTOTP(staffID uuid.UUID, ct []byte, keyID string) ([]byte, error) {
	return a.keys.Open(ct, keyID, totpAAD(staffID))
}

// recoveryMAC — HMAC резервного кода: ключ key_id строки, метка staff-recovery, сообщение — id
// сотрудника ‖ нормализованный код (domain.NormalizeRecoveryCode). Одна функция для выдачи (Task 7)
// и входа: иначе выданные коды не сработали бы.
func (a *App) recoveryMAC(keyID string, staffID uuid.UUID, norm string) ([]byte, error) {
	msg := make([]byte, 0, len(staffID)+len(norm))
	msg = append(msg, staffID[:]...)
	msg = append(msg, norm...)
	return a.keys.MAC(keyID, labelRecovery, msg)
}

// loginEmailKey — ключ лимита входа по почте (D7).
func (a *App) loginEmailKey(email string) (string, error) { return a.emailKey(classStaffLogin, email) }

// emailKey — ключ лимита класса class по почте: HMAC почты текущим ключом кольца, сама почта в
// rate_limits не попадает.
func (a *App) emailKey(class, email string) (string, error) {
	mac, err := a.keys.MAC(a.keys.CurrentID(), labelLoginEmail, []byte(email))
	if err != nil {
		return "", err
	}
	return ratelimit.EmailKey(class, mac), nil
}

// macEqual — сравнение HMAC за постоянное время.
func macEqual(a, b []byte) bool { return hmac.Equal(a, b) }
