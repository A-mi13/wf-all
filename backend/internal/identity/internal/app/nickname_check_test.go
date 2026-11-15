package app

import (
	"context"
	"testing"
	"time"

	"wf/backend/internal/identity/internal/domain"
)

// strokeDeleted — «deleted-0a1b» с перечёркнутыми и крючковыми буквами без разложения NFD (đ, ł, ŧ, ɇ и т. п.):
// unaccent базы сводит их к d, l, t, e — двойник заглушки удалённого.
var strokeDeleted = []string{
	"đeleted-0a1b", "Đeleted-0a1b", "ɖeleted-0a1b", "ɗeleted-0a1b", "ȡeleted-0a1b", "ᵭeleted-0a1b", "ᶑeleted-0a1b",
	"dɇleted-0a1b", "dɆleted-0a1b",
	"dełeted-0a1b", "deŁeted-0a1b", "deƚeted-0a1b", "deȴeted-0a1b", "deɫeted-0a1b", "deɬeted-0a1b", "deɭeted-0a1b",
	"deleŧed-0a1b", "deleŦed-0a1b", "deleȶed-0a1b", "deleƫed-0a1b", "deleʈed-0a1b",
}

// TestNicknameStrokeMatchesNormalizeText — уборка W5 m1: каждое написание, которое normalize_text базы сводит к
// заглушке удалённого, отвергает и domain.CheckNickname (иначе двойник проходит формат, а база его не ловит: индекс
// ника частичный, заглушки удалённых в нём нет).
func TestNicknameStrokeMatchesNormalizeText(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var stub string
	if err := e.owner.QueryRow(context.Background(), `SELECT normalize_text('deleted-0a1b')`).Scan(&stub); err != nil {
		t.Fatal(err)
	}
	for _, n := range strokeDeleted {
		var got string
		if err := e.owner.QueryRow(context.Background(), `SELECT normalize_text($1)`, n).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != stub {
			t.Errorf("%q: normalize_text %q, заглушка %q — образец не двойник", n, got, stub)
		}
		if _, p := domain.CheckNickname(n); p != domain.NicknameInvalid {
			t.Errorf("%q: формат пропустил двойник заглушки", n)
		}
	}
}

// TestCheckNicknameOrphanQuarantine — уборка W5 m3 (отклонено): карантин удалённого владельца (previous_user_id NULL)
// занят и для регистрации (owner = NULL). «n.previous_user_id IS NULL OR» в NicknameState не лишнее: NULL IS DISTINCT
// FROM NULL — false, без него карантин удалённого при регистрации и проверке ника не действовал бы.
func TestCheckNicknameOrphanQuarantine(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	acctExec(t, e, `INSERT INTO nickname_reservations (nickname_normalized, previous_user_id, released_at, expires_at)
		VALUES (normalize_text('orphan_reg'), NULL, $1, $2)`, e.clock.Now(), e.clock.Now().Add(time.Hour))
	ok, reason, err := e.a.CheckNickname(context.Background(), "orphan_reg")
	if err != nil || ok || reason != NickTaken {
		t.Fatalf("карантин удалённого при проверке ника: %v %v %q", ok, err, reason)
	}
}
