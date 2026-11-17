package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity/internal/domain"
)

// Порты условий капитанства (спека §10.3, решение контроллера 13.11, К1): identity поставляет механизм, проверки
// пишут модули выше (players — профиль, teams — капитанство в городе). До их появления сборка cmd/api и cmd/admin-api
// подключает для обоих имён UnavailableCheck — заявку подать и одобрить нельзя, правило не обходится; тестовые
// реализации — identity/identitytest (Go-тесты и бинарники с тегом wf_e2e).

// CaptainCheck — одно условие капитанства. Check зовётся в транзакции вызывающего (q): ok = false — условие не
// выполнено, reason — значение CaptainIneligibility (profile_incomplete, captain_elsewhere_in_city,
// checks_unavailable); err — сбой проверки (500), а не «не выполнено».
type CaptainCheck interface {
	Name() string
	Check(ctx context.Context, q pgx.Tx, userID, cityID uuid.UUID) (reason string, ok bool, err error)
}

// Имена портов (RequiredCaptainChecks).
const (
	CheckProfileComplete = "players.profile_complete"
	CheckCityCaptaincy   = "teams.city_captaincy"
)

// RequiredCaptainChecks — ровно эти порты обязана подключить сборка api и admin-api (ValidateCaptainChecks).
var RequiredCaptainChecks = []string{CheckProfileComplete, CheckCityCaptaincy}

// StaffLinks — текущая связь «сотрудник → учётка игрока» в транзакции вызывающего (R38 плана I-4): строка
// staff_accounts сотрудника FOR SHARE, затем staff_player_links; uuid.Nil — связи нет. Замок сериализует решение по
// заявке с PutPlayerLink/DeletePlayerLink (FOR UPDATE той же строки). Реализация — staff.LinkedPlayerTx, адаптер —
// cmd/admin-api (identity staff не импортирует: staff — слой 0, но сборка межмодульных портов — в cmd).
type StaffLinks interface {
	LinkedUser(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) (uuid.UUID, error)
}

// UnavailableCheck — порт «модуль ещё не построен»: всегда checks_unavailable, к базе не обращается.
func UnavailableCheck(name string) CaptainCheck { return unavailableCheck(name) }

type unavailableCheck string

func (c unavailableCheck) Name() string { return string(c) }

func (unavailableCheck) Check(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (string, bool, error) {
	return domain.ReasonChecksUnavailable, false, nil
}

// ErrCaptainChecks — набор портов не равен RequiredCaptainChecks: отказ старта.
var ErrCaptainChecks = errors.New("identity: порты условий капитанства")

// ValidateCaptainChecks — имена портов ровно RequiredCaptainChecks (порядок не важен), без повторов и nil; иначе
// ошибка с перечнем расхождений (опечатка видна как «лишний» и «нет»).
func ValidateCaptainChecks(cs []CaptainCheck) error {
	var bad []string
	seen := map[string]bool{}
	for i, c := range cs {
		if c == nil {
			bad = append(bad, fmt.Sprintf("nil на месте %d", i))
			continue
		}
		name := c.Name()
		switch {
		case seen[name]:
			bad = append(bad, "повтор "+name)
		case !slices.Contains(RequiredCaptainChecks, name):
			bad = append(bad, "лишний "+name)
		}
		seen[name] = true
	}
	for _, name := range RequiredCaptainChecks {
		if !seen[name] {
			bad = append(bad, "нет "+name)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%w: %s (нужны ровно %v)", ErrCaptainChecks, strings.Join(bad, ", "), RequiredCaptainChecks)
	}
	return nil
}
