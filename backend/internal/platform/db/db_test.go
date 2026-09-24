package db_test

import (
	"context"
	"strings"
	"testing"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestOpenPingsDatabase(t *testing.T) {
	pool, err := db.Open(context.Background(), config.DB{URL: dbtest.NewURL(t), MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if got := pool.Config().MaxConns; got != 2 {
		t.Fatalf("MaxConns = %d", got)
	}
}

func TestOpenFailsFastOnUnreachableDB(t *testing.T) {
	_, err := db.Open(context.Background(), config.DB{ //nolint:gosec // фиктивные тестовые креды, не боевой секрет
		URL: "postgres://x:x@127.0.0.1:1/wf?sslmode=disable&connect_timeout=1", MaxConns: 1,
	})
	if err == nil {
		t.Fatal("ждали ошибку подключения")
	}
}

// Битая строка подключения не должна утекать в ошибку (а значит, в логи):
// pgx маскирует пароль «best effort», и хвосты вида "PW" после пробела,
// '@' в пароле или незакрытая кавычка проходят в текст ошибки как есть.
func TestOpenDoesNotLeakPasswordOnParseError(t *testing.T) {
	for _, url := range malformedURLsWithPassword {
		_, err := db.Open(context.Background(), config.DB{URL: url, MaxConns: 1})
		if err == nil {
			t.Fatalf("Open(%q): ждали ошибку разбора", url)
		}
		for _, secret := range []string{"S3cr3t", "PW"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("Open(%q): ошибка содержит %q: %v", url, secret, err)
			}
		}
	}
}

// Строки, на которых pgx.ParseConfig падает и частично выводит пароль.
var malformedURLsWithPassword = []string{
	"host=127.0.0.1 password=S3cr3t PW dbname=wf",
	"postgres://u:S3cr3t@PW@host:bad/db",
	"host=127.0.0.1 password='S3cr3t PW dbname=wf",
}
