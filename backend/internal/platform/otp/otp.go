// Package otp — одноразовые коды второго фактора, общие для игроков и сотрудников: TOTP по RFC 6238,
// резервные коды и код приглашения одного формата (спека identity §3.3, §5.7; спека сотрудников §4.1).
// Перенесено из staff/internal/domain без изменения поведения. Только формат и сверка кода: MAC резервных
// кодов (своя метка у каждого модуля) и хранение — у вызывающего. Без БД, HTTP и часов: текущее время
// передаёт вызывающий (clock.Clock). Зависимости — стандартная библиотека и golang.org/x/text (NFKC).
package otp

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 с SHA-1 — то, что понимают все приложения-аутентификаторы (спека сотрудников §4.1)
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// Параметры TOTP (спека сотрудников §4.1): RFC 6238, SHA-1, 6 цифр, шаг 30 с, допуск ±1 шаг, секрет 160 бит.
const (
	Digits      = 6
	Period      = 30
	Skew        = 1
	SecretBytes = 20
)

// modulo — 10^Digits: код — остаток усечённого HMAC.
const modulo = 1_000_000

// b32 — base32 RFC 4648 без '=': так секрет показывается рядом с QR и вводится руками.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret — новый секрет TOTP из rnd (crypto/rand.Reader; в тестах — детерминированный).
func NewSecret(rnd io.Reader) ([]byte, error) {
	s := make([]byte, SecretBytes)
	if _, err := io.ReadFull(rnd, s); err != nil {
		return nil, fmt.Errorf("otp: секрет TOTP: %w", err)
	}
	return s, nil
}

// Step — номер 30-секундного шага Unix-времени (RFC 6238, T0 = 0). Время до 1970 не бывает.
func Step(t time.Time) int64 { return t.Unix() / Period }

// Code — код шага step: HOTP (RFC 4226) с динамическим усечением, 6 цифр с ведущими нулями.
func Code(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // шаг неотрицателен: время после 1970
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", Digits, bin%modulo)
}

// Verify — принят ли код в окне now±Skew шагов. Возвращает шаг принятого кода: его вызывающий пишет
// в last_used_step. Шаг не больше lastUsed не принимается — так тот же код второй раз и код старше
// уже принятого отвергаются (спека сотрудников §4.1, спека identity §3.3). Ввод нормализуется
// (NormalizeCode); все шаги окна сравниваются за постоянное время.
func Verify(secret []byte, code string, now time.Time, lastUsed int64) (step int64, ok bool) {
	code, valid := NormalizeCode(code)
	if !valid {
		return 0, false
	}
	cur := Step(now)
	for d := -Skew; d <= Skew; d++ {
		s := cur + int64(d)
		match := subtle.ConstantTimeCompare([]byte(Code(secret, s)), []byte(code)) == 1
		if match && s > lastUsed && !ok {
			step, ok = s, true
		}
	}
	return step, ok
}

// Base32 — секрет для показа и ручного ввода: base32 RFC 4648 без '='.
func Base32(secret []byte) string { return b32.EncodeToString(secret) }

// URI — ссылка для QR (Key URI Format): otpauth://totp/<issuer>:<account>?secret=…&issuer=…
// &algorithm=SHA1&digits=6&period=30. Пробел кодируется %20, а не '+': '+' часть приложений
// показывает буквально. issuer — бренд из конфигурации или локалей, не из кода (CLAUDE.md).
func URI(issuer, account string, secret []byte) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		escape(issuer), escape(account), Base32(secret), escape(issuer), Digits, Period)
}

// escape — RFC 3986 для частей ссылки: QueryEscape кодирует всё зарезервированное, пробел — '+',
// а буквальный '+' — %2B; поэтому '+' после него безопасно заменить на %20.
func escape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
