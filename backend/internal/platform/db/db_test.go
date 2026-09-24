// backend/internal/platform/db/db_test.go
package db_test

import (
	"context"
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
	_, err := db.Open(context.Background(), config.DB{
		URL: "postgres://x:x@127.0.0.1:1/wf?sslmode=disable&connect_timeout=1", MaxConns: 1,
	})
	if err == nil {
		t.Fatal("ждали ошибку подключения")
	}
}
