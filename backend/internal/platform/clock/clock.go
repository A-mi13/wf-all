// Package clock — текущее время для платформы и модулей (спека бэкенда §6.9): в коде —
// clock.System, в тестах — clocktest.Fake. Хранится и передаётся только UTC; местное время
// считается по таймзоне места (поле → город) функцией In. База таймзон вшита в бинарник
// (time/tzdata): контейнер без tzdata не ломает расчёт местного времени.
package clock

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata" // база таймзон в бинарнике
)

// Clock — источник текущего времени.
type Clock interface {
	Now() time.Time
}

type system struct{}

func (system) Now() time.Time { return time.Now().UTC() }

// System — настоящие часы, время в UTC.
var System Clock = system{}

// ErrNoZone — ни одной непустой таймзоны не передано.
var ErrNoZone = errors.New("clock: не задано ни одной таймзоны")

// In — t в местном времени первой непустой таймзоны из zones (таймзона поля, затем города;
// пустая users.timezone пропускается). Неизвестная таймзона — ошибка, а не тихий UTC.
func In(t time.Time, zones ...string) (time.Time, error) {
	for _, z := range zones {
		if z == "" {
			continue
		}
		// Local — таймзона машины, а не места
		if z == "Local" {
			return time.Time{}, fmt.Errorf("clock: таймзона %q не принимается", z)
		}
		loc, err := time.LoadLocation(z)
		if err != nil {
			return time.Time{}, fmt.Errorf("clock: таймзона %q: %w", z, err)
		}
		return t.In(loc), nil
	}
	return time.Time{}, ErrNoZone
}
