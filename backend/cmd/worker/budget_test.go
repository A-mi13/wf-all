package main

import (
	"testing"

	"wf/backend/internal/platform/config"
)

// План C, R4: остановка воркера — relay со сроком + мягкая River + жёсткая — в бюджете 25 с (если воркер встанет на
// Render); срок relay держит TestWorkerRelayStopBounded (events).
func TestWorkerShutdownBudget(t *testing.T) {
	t.Parallel()
	if got := config.WorkerStopTime(); got > config.StopBudget {
		t.Fatalf("остановка воркера %s (relay %s + River %s + %s) — больше бюджета %s",
			got, config.WorkerRelayStop, config.WorkerRiverSoftStop, config.HardStop, config.StopBudget)
	}
}
