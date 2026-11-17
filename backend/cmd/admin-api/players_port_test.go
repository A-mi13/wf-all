package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestPlayersPortAdapter — порт staff.PlayerDirectory сборки (план I-4, R15) под ролью admin: активный и в удалении —
// ник; удалённый и несуществующий — ok = false; сборка admin-api отдаёт порт сценариям staff.
func TestPlayersPortAdapter(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	a := e.assemble()
	ctx := context.Background()
	seed := func(nick, extra string) uuid.UUID {
		var id uuid.UUID
		if err := e.owner.QueryRow(ctx, `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
			VALUES ($1, now(), $2, '2000-01-01', 'ru') RETURNING id`, nick+"@example.com", nick).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if extra != "" {
			if _, err := e.owner.Exec(ctx, extra, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	live := seed("port_live", "")
	pending := seed("port_pending", `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'active',
		deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`)
	gone := seed("port_gone", `UPDATE users SET status = 'deleted', deleted_at = now() WHERE id = $1`)

	p := a.deps.Players
	if p == nil {
		t.Fatal("сборка без порта Players")
	}
	for _, c := range []struct {
		id   uuid.UUID
		nick string
		ok   bool
	}{{live, "port_live", true}, {pending, "port_pending", true}, {gone, "", false}, {uuid.New(), "", false}} {
		nick, ok, err := p.Player(ctx, c.id)
		if err != nil || ok != c.ok || nick != c.nick {
			t.Fatalf("%s: %q %v %v — ждали %q %v", c.id, nick, ok, err, c.nick, c.ok)
		}
	}
}
