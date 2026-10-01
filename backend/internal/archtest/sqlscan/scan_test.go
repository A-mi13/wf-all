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
