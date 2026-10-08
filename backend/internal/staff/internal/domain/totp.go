// Package domain — чистые правила модуля staff (спека §4.1): TOTP по RFC 6238, код приглашения,
// резервные коды и нормализация ввода (почта, коды). Без БД, HTTP и часов: текущее время передаёт
// вызывающий (clock.Clock). Зависимости — стандартная библиотека и golang.org/x/text (NFKC).
package domain

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 с SHA-1 — то, что понимают все приложения-аутентификаторы (спека §4.1)
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// Параметры TOTP (спека §4.1): RFC 6238, SHA-1, 6 цифр, шаг 30 с, допуск ±1 шаг, секрет 160 бит.
const (
	TOTPDigits      = 6
	TOTPPeriod      = 30
	TOTPSkew        = 1
	TOTPSecretBytes = 20
)

// totpModulo — 10^TOTPDigits: код — остаток усечённого HMAC.
const totpModulo = 1_000_000

// b32 — base32 RFC 4648 без '=': так секрет показывается рядом с QR и вводится руками.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret — новый секрет TOTP из rnd (crypto/rand.Reader; в тестах — детерминированный).
func NewTOTPSecret(rnd io.Reader) ([]byte, error) {
	s := make([]byte, TOTPSecretBytes)
	if _, err := io.ReadFull(rnd, s); err != nil {
		return nil, fmt.Errorf("domain: секрет TOTP: %w", err)
	}
	return s, nil
}

// TOTPStep — номер 30-секундного шага Unix-времени (RFC 6238, T0 = 0). Время до 1970 не бывает.
func TOTPStep(t time.Time) int64 { return t.Unix() / TOTPPeriod }

// TOTPCode — код шага step: HOTP (RFC 4226) с динамическим усечением, 6 цифр с ведущими нулями.
func TOTPCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // шаг неотрицателен: время после 1970
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", TOTPDigits, bin%totpModulo)
}

// VerifyTOTP — принят ли код в окне now±TOTPSkew шагов. Возвращает шаг принятого кода: его
// вызывающий пишет в staff_mfa.last_used_step. Шаг не больше lastUsed не принимается — так тот же
// код второй раз и код старше уже принятого отвергаются (спека §4.1). Ввод нормализуется
// (NormalizeCode); все шаги окна сравниваются за постоянное время.
func VerifyTOTP(secret []byte, code string, now time.Time, lastUsed int64) (step int64, ok bool) {
	code, valid := NormalizeCode(code)
	if !valid {
		return 0, false
	}
	cur := TOTPStep(now)
	for d := -TOTPSkew; d <= TOTPSkew; d++ {
		s := cur + int64(d)
		match := subtle.ConstantTimeCompare([]byte(TOTPCode(secret, s)), []byte(code)) == 1
		if match && s > lastUsed && !ok {
			step, ok = s, true
		}
	}
	return step, ok
}

// Base32Secret — секрет для показа и ручного ввода: base32 RFC 4648 без '='.
func Base32Secret(secret []byte) string { return b32.EncodeToString(secret) }

// OTPAuthURI — ссылка для QR (Key URI Format): otpauth://totp/<issuer>:<account>?secret=…&issuer=…
// &algorithm=SHA1&digits=6&period=30. Пробел кодируется %20, а не '+': '+' часть приложений
// показывает буквально. issuer — бренд из конфигурации или локалей, не из кода (CLAUDE.md).
func OTPAuthURI(issuer, account string, secret []byte) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		escape(issuer), escape(account), Base32Secret(secret), escape(issuer), TOTPDigits, TOTPPeriod)
}

// escape — RFC 3986 для частей ссылки: QueryEscape кодирует всё зарезервированное, пробел — '+',
// а буквальный '+' — %2B; поэтому '+' после него безопасно заменить на %20.
func escape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
