package migrations_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

// staffRateLimitsVersion — версия миграции 0022_staff_rate_limits.sql.
const staffRateLimitsVersion = 22

// tableShape — колонки, ограничения и индексы таблицы с подставленным вместо её имени «T»: так схемы
// rate_limits и staff_rate_limits сравниваются напрямую. Последний элемент — relpersistence (u — UNLOGGED).
func tableShape(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT 'col ' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod) || CASE WHEN a.attnotnull THEN ' not null' ELSE '' END
		  FROM pg_attribute a WHERE a.attrelid = $1::text::regclass AND a.attnum > 0 AND NOT a.attisdropped
		UNION ALL
		SELECT 'con ' || replace(conname::text, $1::text, 'T') || ' ' || pg_get_constraintdef(oid)
		  FROM pg_constraint WHERE conrelid = $1::text::regclass AND contype <> 'n'
		UNION ALL
		SELECT 'idx ' || replace(indexdef, $1::text, 'T') FROM pg_indexes WHERE schemaname = 'public' AND tablename = $1::text
		UNION ALL
		SELECT 'persistence ' || relpersistence::text FROM pg_class WHERE oid = $1::text::regclass
		ORDER BY 1`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// 0022 (план B, Task 3, R2): staff_rate_limits — копия rate_limits (колонки, PK, индекс по tat, UNLOGGED) со
// своими именами ограничений; откат удаляет таблицу, повторный накат проходит.
func TestStaffRateLimits0022(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()
	players, staff := tableShape(t, db, "rate_limits"), tableShape(t, db, "staff_rate_limits")
	if len(players) == 0 || !slices.Equal(players, staff) {
		t.Fatalf("staff_rate_limits не копия rate_limits:\nrate_limits:       %q\nstaff_rate_limits: %q", players, staff)
	}
	for _, want := range []string{"con T_pkey PRIMARY KEY (key)", "idx CREATE INDEX T_tat_idx ON public.T USING btree (tat)", "persistence u"} {
		if !slices.Contains(staff, want) {
			t.Errorf("у staff_rate_limits нет %q", want)
		}
	}

	p := newProvider(t, db)
	if _, err := p.DownTo(ctx, staffRateLimitsVersion-1); err != nil {
		t.Fatalf("down до %d: %v", staffRateLimitsVersion-1, err)
	}
	var reg sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.staff_rate_limits')::text`).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg.Valid {
		t.Fatal("после отката 0022 staff_rate_limits осталась")
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("повторный up после отката 0022: %v", err)
	}
}
