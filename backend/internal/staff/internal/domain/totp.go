// Package domain — чистые правила модуля staff (спека сотрудников §4.1): TOTP по RFC 6238, код приглашения,
// резервные коды и нормализация ввода (почта, коды), отображаемое имя. Без БД, HTTP и часов: текущее время
// передаёт вызывающий (clock.Clock). TOTP, коды и почта — тонкие делегаты платформы (platform/otp,
// platform/emailaddr; спека identity §5.7): общие с игроками, поведение — байт в байт прежнее.
package domain

import (
	"io"
	"time"

	"wf/backend/internal/platform/otp"
)

// Параметры TOTP (спека §4.1): RFC 6238, SHA-1, 6 цифр, шаг 30 с, допуск ±1 шаг, секрет 160 бит.
const (
	TOTPDigits      = otp.Digits
	TOTPPeriod      = otp.Period
	TOTPSkew        = otp.Skew
	TOTPSecretBytes = otp.SecretBytes
)

// NewTOTPSecret — новый секрет TOTP из rnd (otp.NewSecret).
func NewTOTPSecret(rnd io.Reader) ([]byte, error) { return otp.NewSecret(rnd) }

// TOTPStep — номер 30-секундного шага Unix-времени (otp.Step).
func TOTPStep(t time.Time) int64 { return otp.Step(t) }

// TOTPCode — код шага step (otp.Code).
func TOTPCode(secret []byte, step int64) string { return otp.Code(secret, step) }

// VerifyTOTP — принят ли код в окне now±TOTPSkew шагов; шаг не больше lastUsed (staff_mfa.last_used_step)
// не принимается (otp.Verify).
func VerifyTOTP(secret []byte, code string, now time.Time, lastUsed int64) (step int64, ok bool) {
	return otp.Verify(secret, code, now, lastUsed)
}

// Base32Secret — секрет для показа и ручного ввода (otp.Base32).
func Base32Secret(secret []byte) string { return otp.Base32(secret) }

// OTPAuthURI — ссылка для QR (otp.URI).
func OTPAuthURI(issuer, account string, secret []byte) string {
	return otp.URI(issuer, account, secret)
}
