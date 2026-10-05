// Package appversion — минимальная и рекомендуемая версии нативного приложения (спека geo
// §3.5): чтение из app_versions, сравнение semver, правила записи. Ручки — в
// internal/httpapi/{public,admin}: платформа httpapi не импортирует.
package appversion

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Platform — платформа нативного приложения.
type Platform string

const (
	IOS     Platform = "ios"
	Android Platform = "android"
)

// storeHosts — единственный допустимый хост store_url платформы.
var storeHosts = map[Platform]string{IOS: "apps.apple.com", Android: "play.google.com"}

// Versions — строка app_versions. Version растёт при каждой правке (событие
// platform.app_versions_changed), UpdatedAt — время правки.
type Versions struct {
	Platform    Platform
	Min         string
	Recommended string
	StoreURL    string
	Version     int64
	UpdatedAt   time.Time
}

// ErrFormat — версия не MAJOR.MINOR.PATCH из десятичных чисел без ведущих нулей (semver).
// Сборка (+45) и пре-релиз (-beta) — тоже ошибка: клиент отбрасывает их сам, сервер их не хранит.
var ErrFormat = errors.New("appversion: версия не MAJOR.MINOR.PATCH")

func parse(s string) ([3]uint64, error) {
	var v [3]uint64
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("%w: %q", ErrFormat, s)
	}
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') || strings.TrimLeft(p, "0123456789") != "" {
			return v, fmt.Errorf("%w: %q", ErrFormat, s)
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return v, fmt.Errorf("%w: %q", ErrFormat, s)
		}
		v[i] = n
	}
	return v, nil
}

// Compare — -1, 0, 1 по semver MAJOR.MINOR.PATCH (числа, не строки: 1.10.0 > 1.9.9).
// Негодная версия — ошибка с ErrFormat.
func Compare(a, b string) (int, error) {
	va, err := parse(a)
	if err != nil {
		return 0, err
	}
	vb, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i := range va {
		if c := cmp.Compare(va[i], vb[i]); c != 0 {
			return c, nil
		}
	}
	return 0, nil
}

// Коды нарушений FieldError — по смыслу правил схемы контракта.
const (
	CodeEnum    = "enum"    // неизвестная платформа
	CodeFormat  = "format"  // не MAJOR.MINOR.PATCH; store_url не https-URL с хостом
	CodeMinimum = "minimum" // recommended_version ниже min_version
	CodeHost    = "host"    // хост store_url не стор этой платформы
)

// FieldError — нарушение правила в поле; Field — имя поля контракта (platform, min_version,
// recommended_version, store_url): ручка добавляет префикс места (body., path.).
type FieldError struct {
	Field string
	Code  string
}

// ValidationError — все нарушения Validate сразу: админка подсвечивает каждое поле.
type ValidationError []FieldError

func (e ValidationError) Error() string {
	parts := make([]string, len(e))
	for i, f := range e {
		parts[i] = f.Field + ": " + f.Code
	}
	return "appversion: " + strings.Join(parts, ", ")
}

// Validate — правила записи (спека geo §3.5): платформа ios|android, обе версии semver,
// min ≤ recommended, store_url — https на хост стора платформы (без userinfo и порта).
// Нарушения — ValidationError, nil — можно писать.
func Validate(v Versions) error {
	var errs ValidationError
	host, known := storeHosts[v.Platform]
	if !known {
		errs = append(errs, FieldError{Field: "platform", Code: CodeEnum})
	}
	_, minErr := parse(v.Min)
	if minErr != nil {
		errs = append(errs, FieldError{Field: "min_version", Code: CodeFormat})
	}
	_, recErr := parse(v.Recommended)
	if recErr != nil {
		errs = append(errs, FieldError{Field: "recommended_version", Code: CodeFormat})
	}
	if minErr == nil && recErr == nil {
		if c, _ := Compare(v.Min, v.Recommended); c > 0 {
			errs = append(errs, FieldError{Field: "recommended_version", Code: CodeMinimum})
		}
	}
	if code := storeURLViolation(v.StoreURL, host, known); code != "" {
		errs = append(errs, FieldError{Field: "store_url", Code: code})
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// storeURLViolation — код нарушения store_url или "". Хост сравнивается точно: суффикс
// (apps.apple.com.evil.example), порт и userinfo (apps.apple.com@evil.example) не проходят.
// Неизвестная платформа — хост не проверяется (её нарушение уже в platform).
func storeURLViolation(raw, host string, known bool) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Host == "" {
		return CodeFormat
	}
	if known && u.Host != host {
		return CodeHost
	}
	return ""
}
