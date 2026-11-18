package identity_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
)

// namedCheck — порт с именем, без логики (набор для ValidateCaptainChecks).
type namedCheck string

func (c namedCheck) Name() string { return string(c) }

func (namedCheck) Check(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (string, bool, error) {
	return "", true, nil
}

// Набор портов при сборке — ровно имена RequiredCaptainChecks (спека §10.3, К1): опечатка, пропуск, повтор, nil
// или лишний порт — отказ старта с именем расхождения в ошибке; порядок не важен.
func TestValidateCaptainChecks(t *testing.T) {
	t.Parallel()
	const p, c = "players.profile_complete", "teams.city_captaincy"
	cases := []struct {
		name string
		cs   []identity.CaptainCheck
		want string // "" — ok; иначе подстрока ошибки
	}{
		{"ровно два", []identity.CaptainCheck{namedCheck(p), namedCheck(c)}, ""},
		{"обратный порядок", []identity.CaptainCheck{namedCheck(c), namedCheck(p)}, ""},
		{"пропуск", []identity.CaptainCheck{namedCheck(p)}, c},
		{"пусто", nil, p},
		{"опечатка", []identity.CaptainCheck{namedCheck(p), namedCheck("teams.city_captainсy")}, "teams.city_captainсy"},
		{"повтор", []identity.CaptainCheck{namedCheck(p), namedCheck(c), namedCheck(c)}, "повтор " + c},
		{"повтор вместо второго", []identity.CaptainCheck{namedCheck(p), namedCheck(p)}, "повтор " + p},
		{"nil", []identity.CaptainCheck{namedCheck(p), namedCheck(c), nil}, "nil"},
		{"лишний", []identity.CaptainCheck{namedCheck(p), namedCheck(c), namedCheck("players.extra")}, "players.extra"},
	}
	for _, tc := range cases {
		err := identity.ValidateCaptainChecks(tc.cs)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: ошибка %v, ждали с %q", tc.name, err, tc.want)
		}
	}
	if len(identity.RequiredCaptainChecks) != 2 || identity.RequiredCaptainChecks[0] != p ||
		identity.RequiredCaptainChecks[1] != c {
		t.Fatalf("RequiredCaptainChecks = %v", identity.RequiredCaptainChecks)
	}
}

// «Недоступно» — всегда checks_unavailable без обращения к базе (nil-транзакция не паникует).
func TestUnavailableCheckNoDB(t *testing.T) {
	t.Parallel()
	for _, name := range identity.RequiredCaptainChecks {
		ch := identity.UnavailableCheck(name)
		if ch.Name() != name {
			t.Fatalf("имя %q, ждали %q", ch.Name(), name)
		}
		reason, ok, err := ch.Check(context.Background(), nil, uuid.New(), uuid.New())
		if err != nil || ok || reason != "checks_unavailable" {
			t.Fatalf("%s: %q %v %v", name, reason, ok, err)
		}
	}
}
