package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/staff"
)

// Связь сотрудника с учёткой игрока в tx вызывающего (staff.LinkedPlayerTx, R38 плана I-4): есть — id игрока, нет —
// uuid.Nil, сотрудника нет — staff.ErrNotFound; строка учётки держится FOR SHARE до конца tx — FOR UPDATE той же
// строки (PutPlayerLink/DeletePlayerLink) ждёт коммита. Под ролью admin. Тест — здесь, а не в корне staff: корень без
// базы, его тесты последовательные (archtest.TestDBTestsAreParallel).
func TestStaffLinkedPlayerTx(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	linked := e.addMember("linked@wf.example", memberOpts{})
	free := e.addMember("free@wf.example", memberOpts{})
	var player uuid.UUID
	if err := e.owner.QueryRow(ctx, `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ('linked.player@example.com', now(), 'linked_player', '2000-01-01', 'ru') RETURNING id`).Scan(&player); err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at) VALUES ($1, $2, $1, now())`,
		linked.ID, player)

	read := func(id uuid.UUID) (uuid.UUID, error) {
		var got uuid.UUID
		err := db.InTx(ctx, e.deps.Pool, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			got, err = staff.LinkedPlayerTx(ctx, tx, id)
			return err
		})
		return got, err
	}
	if got, err := read(linked.ID); err != nil || got != player {
		t.Fatalf("связь: %v %v, ждали %v", got, err, player)
	}
	if got, err := read(free.ID); err != nil || got != uuid.Nil {
		t.Fatalf("без связи: %v %v", got, err)
	}
	if _, err := read(uuid.New()); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет сотрудника: %v", err)
	}

	// замок: держим FOR SHARE в tx1; FOR UPDATE той же строки в tx2 упирается в lock_timeout, после коммита tx1 — проходит
	forUpdate := func() error {
		return db.InTx(ctx, e.deps.Pool, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '300ms'`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`, linked.ID)
			return err
		})
	}
	tx1, err := e.deps.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if got, err := staff.LinkedPlayerTx(ctx, tx1, linked.ID); err != nil || got != player {
		t.Fatalf("в tx1: %v %v", got, err)
	}
	var pgErr *pgconn.PgError
	if err := forUpdate(); !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("FOR UPDATE под FOR SHARE: %v, ждали 55P03 lock_not_available", err)
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := forUpdate(); err != nil {
		t.Fatalf("FOR UPDATE после коммита: %v", err)
	}
}

// Связь, заведённая, пока LinkedPlayerTx ждал замок учётки (R38: решение по заявке ∥ PutPlayerLink): держатель берёт
// учётку FOR UPDATE (как lockAccounts у PutPlayerLink), вставляет связь и коммитит после того, как LinkedPlayerTx встал
// на замок, — LinkedPlayerTx обязан вернуть новую связь. Один оператор «FOR SHARE OF a + LEFT JOIN связи» её не видел:
// READ COMMITTED после ожидания перечитывает только заблокированную строку, соединённая берётся из старого снимка.
func TestStaffLinkedPlayerTxSeesLinkCommittedWhileWaiting(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	m := e.addMember("waiting@wf.example", memberOpts{})
	var player uuid.UUID
	if err := e.owner.QueryRow(ctx, `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ('waiting.player@example.com', now(), 'waiting_player', '2000-01-01', 'ru') RETURNING id`).Scan(&player); err != nil {
		t.Fatal(err)
	}
	hold, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at)
		VALUES ($1, $2, $1, now())`, m.ID, player); err != nil {
		t.Fatal(err)
	}
	var (
		got     uuid.UUID
		readErr error
		done    = make(chan struct{})
	)
	go func() {
		defer close(done)
		readErr = db.InTx(ctx, e.deps.Pool, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			got, err = staff.LinkedPlayerTx(ctx, tx, m.ID)
			return err
		})
	}()
	e.waitLockWaiters(1)
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
	if readErr != nil || got != player {
		t.Fatalf("связь после ожидания замка: %v %v, ждали %v", got, readErr, player)
	}
}
