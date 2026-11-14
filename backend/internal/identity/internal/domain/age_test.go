package domain_test

import (
	"errors"
	"testing"
	"time"

	"wf/backend/internal/identity/internal/domain"
)

func age(t *testing.T, birth, now time.Time, tz *time.Location) int {
	t.Helper()
	n, err := domain.AgeYears(birth, now, tz)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Таймзона города не известна (nil) — отказ, а не паника и не UTC молча.
func TestAgeYearsNilTZ(t *testing.T) {
	if _, err := domain.AgeYears(date(2010, time.May, 1), time.Now(), nil); !errors.Is(err, domain.ErrNoTimezone) {
		t.Fatalf("err = %v", err)
	}
}

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// Возраст — по «сегодня» в таймзоне города, а не UTC: во Владивостоке (UTC+10) день рождения наступает на 10 часов
// раньше, чем по UTC.
func TestAgeYearsCityTZ(t *testing.T) {
	vvo, err := time.LoadLocation("Asia/Vladivostok")
	if err != nil {
		t.Fatal(err)
	}
	birth := date(2012, time.March, 15)
	now := time.Date(2026, time.March, 14, 20, 0, 0, 0, time.UTC) // во Владивостоке уже 15 марта, 06:00
	if got := age(t, birth, now, vvo); got != 14 {
		t.Errorf("Владивосток: %d, ждали 14", got)
	}
	if got := age(t, birth, now, time.UTC); got != 13 {
		t.Errorf("UTC: %d, ждали 13", got)
	}
	// Калининград (UTC+2): ещё 14 марта 22:00 — 13
	kgd, _ := time.LoadLocation("Europe/Kaliningrad")
	if got := age(t, birth, now, kgd); got != 13 {
		t.Errorf("Калининград: %d, ждали 13", got)
	}
	// 29 февраля: в невисокосный год день рождения наступает 1 марта
	leap := date(2008, time.February, 29)
	for _, c := range []struct {
		now  time.Time
		want int
	}{
		{date(2026, time.February, 28), 17},
		{date(2026, time.March, 1), 18},
		{date(2028, time.February, 28), 19},
		{date(2028, time.February, 29), 20},
		{date(2008, time.February, 29), 0},
	} {
		if got := age(t, leap, c.now.Add(12*time.Hour), time.UTC); got != c.want {
			t.Errorf("29.02 на %s: %d, ждали %d", c.now.Format(time.DateOnly), got, c.want)
		}
	}
}

func TestCheckBirthDate(t *testing.T) {
	now := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
	if err := domain.CheckBirthDate(date(2010, time.May, 1), now); err != nil {
		t.Fatal(err)
	}
	if err := domain.CheckBirthDate(date(2026, time.November, 14), now); err != nil {
		t.Fatalf("сегодня: %v", err)
	}
	// «завтра по UTC» — уже сегодня на востоке (UTC+14): не будущее
	if err := domain.CheckBirthDate(date(2026, time.November, 15), now); err != nil {
		t.Fatalf("сегодня на UTC+14: %v", err)
	}
	if err := domain.CheckBirthDate(date(2026, time.November, 16), now); !errors.Is(err, domain.ErrBirthDateInvalid) {
		t.Fatalf("будущее: %v", err)
	}
	// граница «будущего» — ровно now+14 ч (UTC+14): ровно — можно, на секунду позже — нельзя
	b := date(2026, time.December, 1)
	if err := domain.CheckBirthDate(b, b.Add(-14*time.Hour)); err != nil {
		t.Fatalf("ровно +14 ч: %v", err)
	}
	if err := domain.CheckBirthDate(b, b.Add(-14*time.Hour-time.Second)); !errors.Is(err, domain.ErrBirthDateInvalid) {
		t.Fatalf("+14 ч и секунда: %v", err)
	}
	// граница 120 лет по UTC: ровно 120 — можно, 121 (с полуночи дня рождения) — нельзя
	old := date(1905, time.November, 14)
	if err := domain.CheckBirthDate(old, time.Date(2026, time.November, 13, 23, 59, 59, 0, time.UTC)); err != nil {
		t.Fatalf("120 лет: %v", err)
	}
	if err := domain.CheckBirthDate(old, date(2026, time.November, 14)); !errors.Is(err, domain.ErrBirthDateInvalid) {
		t.Fatalf("121 год: %v", err)
	}
	if err := domain.CheckBirthDate(date(1906, time.November, 15), now); err != nil {
		t.Fatalf("119 лет: %v", err)
	}
	if err := domain.CheckBirthDate(date(1905, time.November, 13), now); !errors.Is(err, domain.ErrBirthDateInvalid) {
		t.Fatalf("старше 120: %v", err)
	}
}
