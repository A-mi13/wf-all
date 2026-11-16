package domain

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"wf/backend/internal/platform/otp"
)

// Код из письма (спека §3.1, §3.4, §3.9): регистрация, сброс пароля, смена почты.
const (
	CodeTTL      = 15 * time.Minute
	CodeAttempts = 5 // неверных вводов до погашения
	MaxSends     = 4 // отправок одного кода (первая + три повтора)

	codeModulo = 1_000_000
	// codeLimit — наибольшее кратное codeModulo, не превышающее 2^32: значения от него и выше отбрасываются, иначе
	// младшие коды выпадали бы чаще.
	codeLimit = (1 << 32) / codeModulo * codeModulo
)

// resendPauses — пауза перед следующей отправкой после N-й (индекс N-1).
var resendPauses = [MaxSends - 1]time.Duration{60 * time.Second, 120 * time.Second, 300 * time.Second}

// NewEmailCode — 6 цифр из rnd равномерно (отбраковка хвоста uint32). Ошибка источника — как есть.
func NewEmailCode(rnd io.Reader) (string, error) {
	var b [4]byte
	for {
		if _, err := io.ReadFull(rnd, b[:]); err != nil {
			return "", fmt.Errorf("identity: код из письма: %w", err)
		}
		if v := binary.BigEndian.Uint32(b[:]); v < codeLimit {
			return fmt.Sprintf("%06d", v%codeModulo), nil
		}
	}
}

// NormalizeEmailCode — ввод кода: 6 цифр после NFKC без пробелов и невидимых (как код TOTP, otp.NormalizeCode).
func NormalizeEmailCode(s string) (string, bool) { return otp.NormalizeCode(s) }

// ResendAvailableAt — когда можно отправить код ещё раз после sends отправок (последняя — lastSent): паузы 60, 120,
// 300 с после 1-й, 2-й, 3-й; ok=false — отправок больше нет (sends ≥ MaxSends) или sends < 1.
func ResendAvailableAt(lastSent time.Time, sends int) (time.Time, bool) {
	if sends < 1 || sends >= MaxSends {
		return time.Time{}, false
	}
	return lastSent.Add(resendPauses[sends-1]), true
}
