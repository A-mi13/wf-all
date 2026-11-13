package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"wf/backend/internal/platform/migrate"
)

// syncLog — журнал процесса, который тест читает, пока процесс пишет.
type syncLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncLog) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncLog) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// behindSchema — база теста без последней миграции в goose_db_version (релиз admin-api новее схемы).
func (e *testEnv) behindSchema() int64 {
	e.t.Helper()
	want, err := migrate.Latest()
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`DELETE FROM goose_db_version WHERE version_id = $1`, want)
	return want
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(60 * time.Second):
		t.Fatal("admin-api не вернулся")
		return nil
	}
}

// План C, R3: admin-api нового релиза, стартовавший раньше наката миграций wf-api, не слушает порт (health check
// Render не проходит, трафик не переключается), пока версия схемы не дорастёт до вшитой; по таймауту — код выхода 1 и
// понятная ошибка.
func TestAdminAPIWaitsSchema(t *testing.T) {
	t.Parallel()
	t.Run("waits", func(t *testing.T) {
		t.Parallel()
		e := newTestEnv(t)
		want := e.behindSchema()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		logOut := &syncLog{}
		done := make(chan error, 1)
		go func() { done <- run(ctx, nil, e.environ(), &bytes.Buffer{}, logOut) }()
		deadline := time.Now().Add(30 * time.Second)
		for !strings.Contains(logOut.String(), "схема базы отстаёт") {
			select {
			case err := <-done:
				t.Fatalf("admin-api вернулся, не дождавшись схемы: %v\n%s", err, logOut)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("admin-api не сообщил об ожидании схемы:\n%s", logOut)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if code, err := e.tryGet("/healthz"); err == nil {
			t.Fatalf("/healthz ответил %d до наката схемы", code)
		}
		e.exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, want)
		for {
			if code, _ := e.tryGet("/healthz"); code == http.StatusOK {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("admin-api не стартовал после наката: %v\n%s", err, logOut)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("admin-api не ответил /healthz после наката схемы:\n%s", logOut)
			}
			time.Sleep(50 * time.Millisecond)
		}
		cancel()
		if err := waitDone(t, done); err != nil {
			t.Fatalf("остановка: %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		e := newTestEnv(t)
		e.behindSchema()
		done := make(chan error, 1)
		go func() {
			done <- run(context.Background(), nil, e.environ("ADMIN_DB_SCHEMA_WAIT=1s"), &bytes.Buffer{}, &syncLog{})
		}()
		err := waitDone(t, done)
		if !errors.Is(err, migrate.ErrSchemaBehind) || exitCode(err) != 1 || !strings.Contains(err.Error(), "wf-api") {
			t.Fatalf("err = %v (код %d), ждали ErrSchemaBehind с подсказкой и код 1", err, exitCode(err))
		}
		if code, err := e.tryGet("/healthz"); err == nil {
			t.Fatalf("/healthz ответил %d при отставшей схеме", code)
		}
	})
}

// Команду оператора запускают руками: при отставшей схеме — ошибка сразу, без ожидания ADMIN_DB_SCHEMA_WAIT.
func TestOperatorCommandNoWait(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	e.behindSchema()
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), []string{"staff", "rekey"}, e.environ("ADMIN_DB_SCHEMA_WAIT=10m"), &bytes.Buffer{}, &syncLog{})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, migrate.ErrSchemaBehind) || exitCode(err) != 1 {
			t.Fatalf("err = %v, ждали ErrSchemaBehind и код 1", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("команда оператора ждёт схему")
	}
}
