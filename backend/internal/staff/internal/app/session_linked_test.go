package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestLoadStaffLinkedUser — связь «сотрудник → учётка игрока» (staff_player_links, спека identity §7.6, R8)
// загружается тем же запросом, что сессия: есть — auth.Staff.LinkedUserID, нет — uuid.Nil. Чужая связь (другого
// сотрудника) в поле не попадает.
func TestLoadStaffLinkedUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	linked := e.addMember("olga.v@wf.example", memberOpts{Role: "admin"})
	other := e.addMember("boris.k@wf.example", memberOpts{Role: "admin"})
	var player uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO users (email, email_verified_at, nickname,
		birth_date, locale) VALUES ('player@example.com', now(), 'linked_player', '2000-01-01', 'ru') RETURNING id`).
		Scan(&player); err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at) VALUES ($1, $2, $1, now())`,
		linked.ID, player)
	if got := e.load(e.signIn(linked)).LinkedUserID; got != player {
		t.Fatalf("связь есть: LinkedUserID = %s, ждали %s", got, player)
	}
	if got := e.load(e.signIn(other)).LinkedUserID; got != uuid.Nil {
		t.Fatalf("связи нет: LinkedUserID = %s, ждали uuid.Nil", got)
	}
}
