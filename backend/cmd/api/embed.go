package main

import (
	"errors"
	"log/slog"
	"slices"

	"wf/backend/cmd/internal/workerhost"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/keys"
)

// Встроенный режим (API_EMBED_WORKER=true, спека встроенного воркера §3): тот же воркер (workerhost), что у отдельного
// процесса, в процессе api на втором пуле под ролью worker (WORKER_DATABASE_URL). Только dev-стенд.

// errRingMismatch — кольца identity api и воркера разошлись: письма с кодами собирались бы другим ключом.
var errRingMismatch = errors.New("WORKER_IDENTITY_SECRET_KEYS и API_IDENTITY_SECRET_KEYS — разные кольца " +
	"(нужны те же ключи в том же порядке)")

// embeddedStop — сроки фона встроенного режима: короче, чем у отдельного воркера, — перед ними дренаж HTTP (R5).
func embeddedStop() workerhost.Stop {
	return workerhost.Stop{Relay: config.APIEmbeddedRelayStop, Soft: config.APIEmbeddedRiverSoftStop}
}

// prepareEmbedded — проверки встроенного воркера до базы: окружение WORKER_ (как у отдельного процесса; журнал —
// api с component=worker), сверка кольца identity с кольцом api secrets по именам ключей по порядку (R6: сами
// секреты не сравниваются и в ошибку не попадают), бюджет остановки — предупреждение.
func prepareEmbedded(cfg config.API, secrets *keys.Ring, environ []string, startLog *slog.Logger) (workerhost.Prepared,
	error) {
	b, err := workerhost.Load(environ, nil)
	if err != nil {
		return workerhost.Prepared{}, err
	}
	p, err := workerhost.Prepare(b, environ, startLog.With("component", "worker"))
	if err != nil {
		return workerhost.Prepared{}, err
	}
	if !slices.Equal(secrets.IDs(), p.RingIDs()) {
		return workerhost.Prepared{}, errRingMismatch
	}
	// бюджет остановки — предупреждение, не отказ: локально дренаж бывает и длиннее
	if st := config.APIEmbeddedStopTime(cfg.HTTP.ShutdownTimeout); st > config.StopBudget {
		startLog.Warn("остановка api со встроенным воркером может не уложиться в окно хостинга до SIGKILL",
			"stop", st, "budget", config.StopBudget, "drain", cfg.HTTP.ShutdownTimeout)
	}
	return p, nil
}
