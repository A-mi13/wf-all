package admin_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Финальное ревью ветки, I-1: ключи и ответы мутаций admin-api (в ответах ПД — почта, имя, города) лежат
// только в staff_idempotency_keys, права на которую — только у роли admin. В idempotency_keys (у роли api
// там DML) их нет; роль api их не читает (42501). Повтор тем же ключом отдаёт сохранённый ответ — из новой
// таблицы.
func TestStaffIdempotencyOwnTable(t *testing.T) {
	e := newStaffEnv(t)
	a := e.owners(1)[0]
	c1, c2 := e.city("vremensk"), e.city("pokrovsk")
	mod := e.moderator("mod@wf.example", c1, c2)
	key := uuid.NewString()
	path := "/v1/staff/accounts/" + mod.String() + "/role"

	first := e.send(http.MethodPut, path, a.token, key, roleBody("moderator", c1))
	wantStatus(t, first, http.StatusOK)
	again := e.send(http.MethodPut, path, a.token, key, roleBody("moderator", c1))
	wantStatus(t, again, http.StatusOK)
	if !replayed(again) || !sameJSON(t, first, again) {
		t.Fatalf("повтор не из сохранённого ответа: %v %s", again.Header(), again.Body.String())
	}

	if n := e.count(`SELECT count(*) FROM staff_idempotency_keys
		WHERE key = $1 AND user_id = $2 AND response_body ->> 'email' = 'mod@wf.example'`, key, a.id); n != 1 {
		t.Fatalf("ответ с почтой в staff_idempotency_keys: %d строк", n)
	}
	if n := e.count(`SELECT count(*) FROM idempotency_keys`); n != 0 {
		t.Fatalf("ключи admin-api в idempotency_keys: %d строк", n)
	}

	// роль api (публичный API) ответы сотрудников не читает
	ctx := context.Background()
	tx, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE api"); err != nil {
		t.Fatal(err)
	}
	var body string
	err = tx.QueryRow(ctx, `SELECT response_body::text FROM staff_idempotency_keys LIMIT 1`).Scan(&body)
	if pg, ok := errors.AsType[*pgconn.PgError](err); !ok || pg.Code != "42501" {
		t.Fatalf("роль api прочитала ключи admin-api: %q, %v", body, err)
	}
}
