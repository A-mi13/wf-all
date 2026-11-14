package domain

import (
	"errors"
	"time"
)

var (
	// ErrBirthDateInvalid — дата рождения в будущем или старше MaxAgeYears.
	ErrBirthDateInvalid = errors.New("identity: дата рождения в будущем или старше 120 лет")
	// ErrNoTimezone — таймзона города не известна: возраст не считается (не UTC молча и не паника).
	ErrNoTimezone = errors.New("identity: нет таймзоны города для возраста")
)

// MaxAgeYears — предел возраста при регистрации.
const MaxAgeYears = 120

// maxZoneAhead — самая восточная таймзона (UTC+14): «сегодня» где-то на Земле наступает не позже now+14 ч.
const maxZoneAhead = 14 * time.Hour

// AgeYears — полных лет на «сегодня» в таймзоне tz (город игрока). birth — дата рождения как её отдаёт pgx для
// date: полночь UTC; берутся её год, месяц и день. Родившийся 29 февраля в невисокосный год становится старше 1 марта.
// age_years() базы считает по current_date сервера — сценарии берут возраст отсюда. tz nil — ErrNoTimezone.
func AgeYears(birth, now time.Time, tz *time.Location) (int, error) {
	if tz == nil {
		return 0, ErrNoTimezone
	}
	return ageIn(birth, now, tz), nil
}

func ageIn(birth, now time.Time, tz *time.Location) int {
	by, bm, bd := birth.UTC().Date()
	ty, tm, td := now.In(tz).Date()
	age := ty - by
	if tm < bm || (tm == bm && td < bd) {
		age--
	}
	return age
}

// CheckBirthDate — дата рождения не в будущем (ни в одной таймзоне) и не старше MaxAgeYears по UTC.
func CheckBirthDate(birth, now time.Time) error {
	b := birth.UTC()
	if b.After(now.Add(maxZoneAhead)) || ageIn(b, now, time.UTC) > MaxAgeYears {
		return ErrBirthDateInvalid
	}
	return nil
}
