package domain

import (
	"strconv"
	"strings"
)

// Пределы строк устройства (devices, спека §5.3): длиннее — отказ валидации запроса.
const (
	MaxModelLen     = 64
	MaxOSVersionLen = 32

	maxVersionPart = 9 // цифр в части версии: int32 и без переполнений
)

// NormalizeAppVersion — версия приложения в виде MAJOR.MINOR.PATCH: «1.4» → «1.4.0», «1.4.0» — как есть; части —
// десятичные без ведущих нулей (кроме «0»), не длиннее 9 цифр; иное (префикс v, суффиксы, четыре части) — ok=false
// (хвост плана I-1: сравнение с минимальной версией платформы — по числам).
func NormalizeAppVersion(s string) (string, bool) {
	parts := strings.Split(s, ".")
	if len(parts) == 2 {
		parts = append(parts, "0")
	}
	if len(parts) != 3 {
		return "", false
	}
	for _, p := range parts {
		if p == "" || len(p) > maxVersionPart || (len(p) > 1 && p[0] == '0') {
			return "", false
		}
		if _, err := strconv.ParseUint(p, 10, 32); err != nil {
			return "", false
		}
	}
	return strings.Join(parts, "."), true
}
