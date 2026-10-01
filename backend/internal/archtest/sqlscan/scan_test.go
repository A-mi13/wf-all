package sqlscan_test

import (
	"reflect"
	"testing"

	"wf/backend/internal/archtest/sqlscan"
)

func TestScan(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want sqlscan.Usage
	}{
		{
			name: "insert из CTE с join представления и вызовом функции",
			sql: `WITH x AS (SELECT id FROM teams)
				INSERT INTO matches (id) SELECT id FROM x JOIN city_settings cs ON true
				WHERE nearby_pitches(1, 2, 3) IS NOT NULL`,
			want: sqlscan.Usage{Writes: []string{"matches"}, Reads: []string{"city_settings", "teams"}, Functions: []string{"nearby_pitches"}},
		},
		{
			name: "update с from и delete в одном файле",
			sql:  `UPDATE users u SET status = 'active' FROM teams t WHERE t.id = u.id; DELETE FROM sessions WHERE id = $1`,
			want: sqlscan.Usage{Writes: []string{"sessions", "users"}, Reads: []string{"teams"}},
		},
		{
			name: "sqlc.arg не функция схемы, public снимается",
			sql:  `SELECT sqlc.arg(id)::uuid, p.* FROM public.pitches p`,
			want: sqlscan.Usage{Reads: []string{"pitches"}},
		},
		{
			name: "именованный параметр sqlc",
			sql: `-- name: GetFlag :one
SELECT key FROM feature_flags WHERE key = @key`,
			want: sqlscan.Usage{Reads: []string{"feature_flags"}},
		},
		{
			name: "@ в строке и в комментарии не ломает разбор",
			sql: `-- пишет a@b.ru
SELECT 1 FROM users WHERE email = 'a@b.ru'`,
			want: sqlscan.Usage{Reads: []string{"users"}},
		},
		{
			name: "truncate — запись",
			sql:  `TRUNCATE rate_limits`,
			want: sqlscan.Usage{Writes: []string{"rate_limits"}},
		},
		{
			name: "чужая схема квалифицируется",
			sql:  `SELECT relname FROM pg_catalog.pg_class`,
			want: sqlscan.Usage{Reads: []string{"pg_catalog.pg_class"}},
		},
		{
			name: "подзапрос в where",
			sql:  `SELECT 1 FROM teams WHERE city_id IN (SELECT id FROM cities)`,
			want: sqlscan.Usage{Reads: []string{"cities", "teams"}},
		},
		{
			name: "insert on conflict",
			sql:  `INSERT INTO outbox (aggregate_id) VALUES ($1) ON CONFLICT DO NOTHING`,
			want: sqlscan.Usage{Writes: []string{"outbox"}},
		},
		{
			name: "CTE с именем таблицы не прячет цель записи",
			sql:  `WITH sessions AS (DELETE FROM sessions WHERE id = $1 RETURNING *) SELECT count(*) FROM sessions`,
			want: sqlscan.Usage{Writes: []string{"sessions"}, Functions: []string{"count"}},
		},
		{
			name: "цель записи — всегда таблица, даже при видимом CTE с тем же именем",
			sql:  `WITH users AS (SELECT 1 AS id) DELETE FROM users WHERE id IN (SELECT id FROM users)`,
			want: sqlscan.Usage{Writes: []string{"users"}},
		},
		{
			name: "имя CTE видно только внутри своего WITH",
			sql: `WITH teams AS (SELECT 1 AS id) SELECT * FROM teams;
				UPDATE teams SET name = 'x';
				SELECT * FROM teams JOIN users ON true`,
			want: sqlscan.Usage{Writes: []string{"teams"}, Reads: []string{"teams", "users"}},
		},
		{
			name: "нерекурсивный CTE внутри себя видит таблицу, а не себя",
			sql:  `WITH teams AS (SELECT * FROM teams) SELECT * FROM teams`,
			want: sqlscan.Usage{Reads: []string{"teams"}},
		},
		{
			name: "рекурсивный CTE внутри себя видит себя",
			sql: `WITH RECURSIVE tree AS (SELECT id FROM cities UNION ALL SELECT c.id FROM cities c JOIN tree t ON t.id = c.id)
				SELECT id FROM tree`,
			want: sqlscan.Usage{Reads: []string{"cities"}},
		},
		{
			name: "FOR UPDATE OF — псевдоним, не таблица",
			sql:  `SELECT * FROM teams t WHERE t.id = $1 FOR UPDATE OF t`,
			want: sqlscan.Usage{Reads: []string{"teams"}},
		},
		{
			name: "оператор @@ перед функцией не путается с параметром sqlc",
			sql:  `SELECT 1 FROM users WHERE tsv @@to_tsquery(@q)`,
			want: sqlscan.Usage{Reads: []string{"users"}, Functions: []string{"to_tsquery"}},
		},
		{
			name: "merge — запись в цель, чтение источника",
			sql:  `MERGE INTO users u USING teams t ON t.id = u.id WHEN MATCHED THEN UPDATE SET status = 'active'`,
			want: sqlscan.Usage{Writes: []string{"users"}, Reads: []string{"teams"}},
		},
		{
			name: "пишущий CTE с отдельным именем",
			sql:  `WITH x AS (DELETE FROM users WHERE id = $1 RETURNING id) SELECT id FROM x`,
			want: sqlscan.Usage{Writes: []string{"users"}},
		},
		{
			name: "delete using",
			sql:  `DELETE FROM sessions s USING users u WHERE u.id = s.user_id`,
			want: sqlscan.Usage{Writes: []string{"sessions"}, Reads: []string{"users"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sqlscan.Scan(c.sql)
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestScanRejectsInvalidSQL(t *testing.T) {
	if _, err := sqlscan.Scan("SELEC 1"); err == nil {
		t.Fatal("ошибка синтаксиса не обнаружена")
	}
}

// Страж закрыт по умолчанию: оператор, который разбор не понимает, — ошибка, а не пустой Usage.
func TestScanRejectsNonDML(t *testing.T) {
	for _, sql := range []string{
		`CALL matches_set_slot($1)`,
		`REFRESH MATERIALIZED VIEW users`,
		`SELECT * INTO newt FROM users`,
		`CREATE TABLE x (id int)`,
	} {
		t.Run(sql, func(t *testing.T) {
			if got, err := sqlscan.Scan(sql); err == nil {
				t.Fatalf("ошибки нет, got %+v", got)
			}
		})
	}
}
