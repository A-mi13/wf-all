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

// Страж спеки бэкенда §12.7 и спеки «Сотрудники и админка geo» §3.3 (D3): под api и worker нельзя
// прочитать staff_* и approvals, под api — изменить audit_log, под worker — прочитать credentials (кроме
// user_id для удаления). admin-api исполняет задачи River очереди staff — права на служебные таблицы
// River у admin как у воркера. Таблицы credentials в схеме ещё нет (спека identity) — тест создаёт её
// сам, как и пробные последовательности: правило должно сработать по имени. Перед Apply тест раздаёт
// всем ролям всё на проверяемые объекты — так отказы доказывают, что Apply права отзывает, а не просто
// не выдаёт (свежий кластер широких умолчаний не имеет).
func TestRolePrivileges(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS credentials (user_id uuid, password_hash text)",
		"CREATE SEQUENCE IF NOT EXISTS staff_probe_seq",
		"CREATE SEQUENCE IF NOT EXISTS approvals_probe_seq",
		"GRANT ALL ON credentials, audit_log, event_inbox, outbox, goose_db_version, river_job, river_leader, " +
			"river_queue, river_notification TO api, admin, worker",
		"GRANT ALL ON countries, regions, cities, districts, country_names, region_names, city_names, city_slug_history, " +
			"geonames_imports, geonames_places, geonames_place_names, geonames_admin1, geonames_admin1_names, app_versions " +
			"TO api, admin, worker",
		"GRANT ALL ON staff_accounts, staff_roles, staff_role_cities, staff_mfa, staff_recovery_codes, staff_invites, " +
			"staff_sessions, approvals TO api, admin, worker",
		"GRANT ALL ON SEQUENCE staff_probe_seq, approvals_probe_seq TO api, admin, worker",
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
		{"api", "UPDATE audit_log SET reason = reason", false},
		{"api", "DELETE FROM audit_log", false},
		{"api", "SELECT 1 FROM audit_log", false},
		{"api", "INSERT INTO audit_log (action, object_type, actor_role) VALUES ('test.probe', 'probe', 'system')", true},
		{"api", "SELECT 1 FROM event_inbox", false},
		{"api", "SELECT 1 FROM outbox", false},
		{"api", "SELECT 1 FROM goose_db_version", false},
		{"api", "SELECT 1 FROM feature_flags", true},
		{"admin", "SELECT 1 FROM audit_log", true},
		{"admin", "UPDATE audit_log SET reason = reason", false},
		// SELECT 1 под колоночным правом (user_id) PostgreSQL пропускает — проверяем полную строку
		{"worker", "SELECT * FROM credentials", false},
		{"worker", "SELECT password_hash FROM credentials", false},
		// WHERE читает user_id — без SELECT (user_id) удаление аккаунта упало бы с 42501
		{"worker", "DELETE FROM credentials WHERE user_id = gen_random_uuid()", true},
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
		// край платформы (план 3/3): API считает лимиты, тратит решения PoW, ведёт ключи
		// идемпотентности; воркер чистит просроченное
		{"api", "INSERT INTO rate_limits (key, tat) VALUES ('probe', now()) ON CONFLICT (key) DO UPDATE SET tat = excluded.tat", true},
		{"api", "SELECT tat FROM rate_limits WHERE key = 'probe'", true},
		{"api", "INSERT INTO humancheck_spent (signature, expires_at) VALUES ('probe', now()) ON CONFLICT DO NOTHING", true},
		{"api", "INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash) VALUES (gen_random_uuid(), 'probe', '/p', 'h')", true},
		{"api", "UPDATE idempotency_keys SET response_status = 200, response_headers = '{}' WHERE false", true},
		{"worker", "DELETE FROM rate_limits WHERE tat < now()", true},
		{"worker", "DELETE FROM humancheck_spent WHERE expires_at < now()", true},
		{"worker", "DELETE FROM idempotency_keys WHERE expires_at < now()", true},
		// admin-api — тоже край: лимиты входа сотрудников и идемпотентность мутаций админки
		{"admin", "INSERT INTO rate_limits (key, tat) VALUES ('probe', now()) ON CONFLICT (key) DO UPDATE SET tat = excluded.tat", true},
		{"admin", "INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash) VALUES (gen_random_uuid(), 'probe', '/p', 'h')", true},
		// справочник geo (спека geo §3.8): API только читает справочник, источник без журнала и
		// версии приложения
		{"api", "SELECT 1 FROM countries", true},
		{"api", "SELECT 1 FROM cities", true},
		{"api", "SELECT 1 FROM districts", true},
		{"api", "SELECT 1 FROM country_names", true},
		{"api", "SELECT 1 FROM city_names", true},
		{"api", "SELECT 1 FROM city_slug_history", true},
		{"api", "SELECT 1 FROM geonames_places", true},
		{"api", "SELECT 1 FROM geonames_place_names", true},
		{"api", "SELECT 1 FROM geonames_admin1", true},
		{"api", "SELECT 1 FROM geonames_admin1_names", true},
		{"api", "SELECT 1 FROM app_versions", true},
		{"api", "SELECT 1 FROM geo_read_city_settings", true},
		{"api", "SELECT 1 FROM geonames_imports", false},
		{"api", "UPDATE cities SET name = name WHERE false", false},
		{"api", "DELETE FROM countries WHERE false", false},
		{"api", "INSERT INTO city_names (city_id, locale, name) SELECT id, 'de', 'Probe' FROM cities LIMIT 1", false},
		{"api", "DELETE FROM geonames_places WHERE false", false},
		{"api", "UPDATE app_versions SET version = version WHERE false", false},
		// админка: читает всё, включая журнал импорта; правит справочник и версии; удаляет только
		// переводы (районы архивируются, города и страны не удаляются); источник пишет только воркер
		{"admin", "SELECT 1 FROM geonames_imports", true},
		{"admin", "SELECT 1 FROM geonames_place_names", true},
		{"admin", "INSERT INTO geonames_imports (country_code, status, started_at) VALUES ('RU', 'running', now())", false},
		{"admin", "UPDATE geonames_places SET population = population WHERE false", false},
		{"admin", "INSERT INTO cities (country_id, name, slug, timezone, centroid) " +
			"SELECT id, 'Проба', 'probe', 'Europe/Moscow', 'SRID=4326;POINT(41.9 45.0)'::geography FROM countries WHERE code = 'RU'", true},
		{"admin", "UPDATE districts SET archived_at = now() WHERE false", true},
		{"admin", "INSERT INTO city_slug_history (slug, city_id, replaced_at) SELECT 'stavropol-old', id, now() FROM cities WHERE slug = 'stavropol'", true},
		{"admin", "DELETE FROM city_names WHERE false", true},
		{"admin", "DELETE FROM region_names WHERE false", true},
		{"admin", "DELETE FROM country_names WHERE false", true},
		{"admin", "DELETE FROM cities WHERE false", false},
		{"admin", "DELETE FROM districts WHERE false", false},
		{"admin", "DELETE FROM countries WHERE false", false},
		{"admin", "DELETE FROM city_slug_history WHERE false", false},
		{"admin", "INSERT INTO app_versions (platform, min_version, recommended_version, store_url) " +
			"VALUES ('ios', '1.0.0', '1.1.0', 'https://apps.apple.com/app/id1') " +
			"ON CONFLICT (platform) DO UPDATE SET min_version = excluded.min_version, version = app_versions.version + 1", true},
		{"admin", "DELETE FROM app_versions WHERE false", false},
		// воркер: источник целиком; сверка импорта (§5.4) — UPDATE городов и регионов, только
		// недостающие переводы городов и регионов; страны — только чтение; остальное geo закрыто
		{"worker", "INSERT INTO geonames_imports (country_code, status, started_at) VALUES ('RU', 'running', now())", true},
		{"worker", "UPDATE geonames_imports SET status = 'failed', finished_at = now() WHERE false", true},
		{"worker", "DELETE FROM geonames_places WHERE false", true},
		{"worker", "UPDATE geonames_admin1 SET ascii_name = ascii_name WHERE false", true},
		{"worker", "DELETE FROM geonames_admin1_names WHERE false", true},
		{"worker", "UPDATE cities SET geoname_id = geoname_id, version = version + 1 WHERE false", true},
		{"worker", "UPDATE regions SET geoname_admin1_code = geoname_admin1_code WHERE false", true},
		{"worker", "INSERT INTO city_names (city_id, locale, name) SELECT id, 'de', 'Stawropol' FROM cities WHERE slug = 'stavropol' ON CONFLICT DO NOTHING", true},
		{"worker", "INSERT INTO region_names (region_id, locale, name) SELECT id, 'de', 'Region Stawropol' FROM regions LIMIT 1 ON CONFLICT DO NOTHING", true},
		{"worker", "SELECT 1 FROM countries", true},
		{"worker", "SELECT 1 FROM country_names", true},
		{"worker", "INSERT INTO cities (country_id, name, slug, timezone, centroid) SELECT country_id, name, 'probe', timezone, centroid FROM cities LIMIT 1", false},
		{"worker", "DELETE FROM regions WHERE false", false},
		{"worker", "UPDATE city_names SET name = name WHERE false", false},
		{"worker", "DELETE FROM city_names WHERE false", false},
		{"worker", "UPDATE countries SET name = name WHERE false", false},
		{"worker", "INSERT INTO country_names (country_id, locale, name) SELECT id, 'de', 'Russland' FROM countries LIMIT 1", false},
		{"worker", "SELECT 1 FROM districts", false},
		{"worker", "SELECT 1 FROM city_slug_history", false},
		{"worker", "SELECT 1 FROM app_versions", false},
		// сотрудники (спека «Сотрудники и админка geo» §3.3, D3): только admin; воркер и api — ничего
		{"api", "SELECT 1 FROM staff_accounts", false},
		{"api", "SELECT 1 FROM staff_sessions", false},
		{"api", "SELECT 1 FROM staff_invites", false},
		{"api", "INSERT INTO staff_accounts (id, email, display_name, created_at) VALUES (gen_random_uuid(), 'probe@example.com', 'Проба', now())", false},
		{"worker", "SELECT 1 FROM staff_accounts", false},
		{"worker", "SELECT 1 FROM staff_invites", false},
		{"worker", "DELETE FROM staff_sessions WHERE false", false},
		{"worker", "UPDATE staff_mfa SET key_id = key_id WHERE false", false},
		{"worker", "SELECT 1 FROM staff_recovery_codes", false},
		{"admin", "SELECT 1 FROM staff_role_cities", true},
		{"admin", "INSERT INTO staff_accounts (id, email, display_name, created_at) VALUES (gen_random_uuid(), 'probe@example.com', 'Проба', now())", true},
		{"admin", "UPDATE staff_mfa SET key_id = key_id WHERE false", true},
		{"admin", "DELETE FROM staff_sessions WHERE false", true},
		{"admin", "DELETE FROM staff_invites WHERE false", true},
		// подтверждения вдвоём (D2): admin создаёт, решает и чистит решённые старше 180 дней; api и worker — ничего
		{"admin", "INSERT INTO approvals (id, action, permission, payload, requested_by, requested_by_label, requested_at, expires_at) " +
			"VALUES (gen_random_uuid(), 'staff.invite', 'staff.roles.grant', '{\"v\": 1}', gen_random_uuid(), 'Проба', now(), now() + interval '1 day')", true},
		{"admin", "UPDATE approvals SET status = status WHERE false", true},
		{"admin", "DELETE FROM approvals WHERE false", true},
		{"api", "SELECT 1 FROM approvals", false},
		{"worker", "SELECT 1 FROM approvals", false},
		// последовательности staff_* и approvals_* — только admin (M12)
		{"admin", "SELECT nextval('staff_probe_seq')", true},
		{"admin", "SELECT nextval('approvals_probe_seq')", true},
		{"api", "SELECT nextval('staff_probe_seq')", false},
		{"api", "SELECT nextval('approvals_probe_seq')", false},
		{"worker", "SELECT nextval('staff_probe_seq')", false},
		// River в admin-api (D3): исполнение задач очереди staff и обязанности лидера — как у воркера
		{"admin", "DELETE FROM river_job WHERE false", true},
		{"admin", "REINDEX INDEX river_job_kind", true},
		{"admin", "SELECT 1 FROM river_leader", true},
		{"admin", "SELECT 1 FROM river_queue", true},
		// river_client и river_client_queue River v7 удалил (0016_river_v7.sql); остались job, leader, queue, notification
		{"admin", "DELETE FROM river_notification WHERE false", true},
		{"admin", "UPDATE river_queue SET paused_at = paused_at WHERE false", true},
		{"api", "DELETE FROM river_job WHERE false", false},
		{"api", "SELECT 1 FROM river_queue", false},
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
