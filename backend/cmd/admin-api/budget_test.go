package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/config"
)

// План C, R4: остановка admin-api — дренаж HTTP (ADMIN_SHUTDOWN_TIMEOUT) + мягкая остановка River + жёсткая — не дольше
// бюджета 25 с (окно Render между SIGTERM и SIGKILL — 30 с; 5 с — закрытие пула и выход). Константы — из config, тест
// их не копирует; значение стенда из render.yaml сверяет тест конфигурации стенда (Task 5). Полный run с письмом,
// зависшим в полёте, укладывается в посчитанный срок: мягкая остановка не дождалась — ctx письма отменён, задача
// вернётся в повтор.
//
// serial: holdMail подменяет переменную пакета wrapSender; порог реального времени.
func TestAdminShutdownBudget(t *testing.T) {
	if config.StopBudget+5*time.Second > config.StopWindow {
		t.Fatalf("бюджет %s без 5 с запаса до окна %s", config.StopBudget, config.StopWindow)
	}
	cfg, err := config.Load[config.Admin]("ADMIN_", offlineEnv())
	if err != nil {
		t.Fatal(err)
	}
	if got := config.AdminStopTime(cfg.HTTP.ShutdownTimeout); got > config.StopBudget {
		t.Fatalf("остановка admin-api по умолчанию %s (дренаж %s + River %s + %s) — больше бюджета %s",
			got, cfg.HTTP.ShutdownTimeout, config.AdminRiverSoftStop, config.HardStop, config.StopBudget)
	}

	e := newTestEnv(t)
	hs := holdMail(t)
	hs.setHold(make(chan struct{})) // письмо не отпускается никогда
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, e.environ("ADMIN_SHUTDOWN_TIMEOUT=1s"), io.Discard, io.Discard) }()
	for {
		if code, _ := e.tryGet("/healthz"); code == 200 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("admin-api не стартовал: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if _, _, err := e.operator("bootstrap", "--email", "anna@wf.example", "--name", "Анна"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-hs.inSend:
	case <-time.After(30 * time.Second):
		t.Fatal("письмо не взято в работу")
	}
	start := time.Now()
	cancel() // SIGTERM
	limit := config.AdminStopTime(time.Second)
	select {
	case err := <-done:
		took := time.Since(start)
		if err == nil || !strings.Contains(err.Error(), "мягкая остановка") {
			t.Fatalf("run = %v, ждали ошибку мягкой остановки", err)
		}
		if took < config.AdminRiverSoftStop || took > limit {
			t.Fatalf("остановка заняла %s, ждали от %s (мягкая River) до %s (бюджет)", took, config.AdminRiverSoftStop, limit)
		}
	case <-time.After(limit + 10*time.Second):
		t.Fatalf("admin-api не остановился за %s", limit)
	}
	if !hs.cancelled.Load() {
		t.Fatal("ctx письма не отменён — жёсткой остановки не было")
	}
}
