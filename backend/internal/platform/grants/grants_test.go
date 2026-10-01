package grants_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/grants"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Страж спеки §12.7: под api нельзя прочитать staff_* и изменить audit_log, под worker —
// прочитать credentials (кроме user_id для удаления). Таблиц staff_* и credentials в схеме
// ещё нет (спека identity) — тест создаёт их сам, до применения прав: правило должно
// сработать по имени. Перед Apply
// тест раздаёт всем ролям всё на проверяемые таблицы — так отказы доказывают, что Apply
// права отзывает, а не просто не выдаёт (свежий кластер широких умолчаний не имеет).
func TestRolePrivileges(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS staff_probe (id int)",
		"CREATE TABLE IF NOT EXISTS credentials (user_id uuid, password_hash text)",
		"GRANT ALL ON staff_probe, credentials, audit_log, event_inbox, outbox, goose_db_version, river_job, river_leader TO api, admin, worker",
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 { // идемпотентность: второй прогон не падает и не меняет итог
		if err := grants.Apply(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		role, sql string
		allowed   bool
	}{
		{"api", "SELECT 1 FROM staff_probe", false},
		{"api", "UPDATE audit_log SET reason = reason", false},
		{"api", "DELETE FROM audit_log", false},
		{"api", "SELECT 1 FROM audit_log", false},
		{"api", "INSERT INTO audit_log (action, object_type) VALUES ('test.probe', 'probe')", true},
		{"api", "SELECT 1 FROM event_inbox", false},
		{"api", "SELECT 1 FROM outbox", false},
		{"api", "SELECT 1 FROM goose_db_version", false},
		{"api", "SELECT 1 FROM feature_flags", true},
		{"admin", "SELECT 1 FROM audit_log", true},
		{"admin", "UPDATE audit_log SET reason = reason", false},
		{"admin", "SELECT 1 FROM staff_probe", true},
		// SELECT 1 под колоночным правом (user_id) PostgreSQL пропускает — проверяем полную строку
		{"worker", "SELECT * FROM credentials", false},
		{"worker", "SELECT password_hash FROM credentials", false},
		// WHERE читает user_id — без SELECT (user_id) удаление аккаунта упало бы с 42501
		{"worker", "DELETE FROM credentials WHERE user_id = gen_random_uuid()", true},
		{"worker", "SELECT 1 FROM staff_probe", false},
		{"worker", "SELECT 1 FROM event_inbox", true},
		// рантайм воркера: relay забирает пачку outbox, River держит лидерство и очереди
		{"worker", "SELECT 1 FROM outbox FOR UPDATE SKIP LOCKED", true},
		{"worker", "SELECT 1 FROM river_leader", true},
		{"worker", "SELECT 1 FROM river_queue", true},
		// River на лидере ежедневно переиндексирует river_job (REINDEX … CONCURRENTLY) — нужен
		// MAINTAIN; обычный REINDEX проверяет то же право и допустим в транзакции
		{"worker", "REINDEX INDEX river_job_kind", true},
		{"api", "REINDEX INDEX river_job_kind", false},
		// рантайм API: публикация события в транзакции запроса
		{"api", "INSERT INTO outbox (id, event_type, schema_version, aggregate_type, aggregate_id, aggregate_version, payload) " +
			"VALUES (gen_random_uuid(), 'test.probe', 1, 'probe', gen_random_uuid(), 1, '{}')", true},
		{"api", "SELECT 1 FROM river_leader", false},
	}
	for _, c := range cases {
		t.Run(c.role+": "+c.sql, func(t *testing.T) {
			err := asRole(ctx, t, db, c.role, c.sql)
			var pg *pgconn.PgError
			denied := errors.As(err, &pg) && pg.Code == "42501" // insufficient_privilege
			switch {
			case c.allowed && err != nil:
				t.Fatalf("ждали доступ, получили: %v", err)
			case !c.allowed && !denied:
				t.Fatalf("ждали отказ в правах (42501), получили: %v", err)
			}
		})
	}
}

// asRole исполняет запрос под ролью в транзакции и откатывает её: проверки не меняют данные.
func asRole(ctx context.Context, t *testing.T, db *sql.DB, role, query string) error {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+role); err != nil {
		t.Fatalf("SET ROLE %s: %v — роли создаёт deploy/dev/initdb/20_wf.sql", role, err)
	}
	_, err = tx.ExecContext(ctx, query)
	return err
}
