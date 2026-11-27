package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/cmd/internal/mediapurposes"
	"wf/backend/internal/media"
	"wf/backend/internal/media/intx"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/blob/blobopen"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/ratelimit"
)

// openBlob — хранилище admin-api до базы (спека media §6.4, Task 4): ADMIN_BLOB_* через config.LoadBlob (fs в
// прод-образе — отказ старта), blobopen.Open (в сеть не ходит) и реестр целей бинарника (R12: только mediapurposes).
// Только серверу: команды оператора ключей хранилища не читают.
func openBlob(ctx context.Context, environ []string, clk clock.Clock) (blob.Opened, *media.Registry, error) {
	cfg, err := config.LoadBlob("ADMIN_", environ)
	if err != nil {
		return blob.Opened{}, nil, err
	}
	reg, err := mediapurposes.Registry()
	if err != nil {
		return blob.Opened{}, nil, err
	}
	b, err := blobopen.Open(ctx, cfg, clk)
	if err != nil {
		return blob.Opened{}, nil, err
	}
	return b, reg, nil
}

// mediaKit — media процесса admin-api: хранилище, сервис (прямая загрузка Stage, чтение, ссылки на закрытое) и операции
// в транзакции владельца (CreateDirectInTx, TakeDownInTx). Владельцев в коде пока нет (спека §12a): сейчас сборку берут
// тестовые ручки wf_e2e (mediaE2E), операции ассетов добавит план economy.
type mediaKit struct {
	blob blob.Opened
	svc  media.Service
	tx   *intx.Tx
}

// newMedia — сборка на пуле admin-api: клиент River только для вставки (тот же, что у staff и geo: media.process и
// media.purge в транзакции владельца), лимитер сотрудников (суточные лимиты media — только у игроков, R19, поэтому
// media.Deps.Rules не задаётся — умолчания: ADMIN_RATE_LIMITS на них не влияет, сотрудник их не считает).
// intx.Deps.PublicBaseURL — то же значение из blobopen.Open, что у сервиса.
func newMedia(log *slog.Logger, pool *pgxpool.Pool, b blob.Opened, reg *media.Registry, clk clock.Clock,
	limiter ratelimit.Limiter, ins *river.Client[pgx.Tx]) (mediaKit, error) {
	svc, err := media.New(media.Deps{Pool: pool, Clock: clk, Registry: reg, Blob: b, Limiter: limiter, River: ins, Log: log})
	if err != nil {
		return mediaKit{}, err
	}
	tx, err := intx.New(intx.Deps{Clock: clk, Registry: reg, River: ins, PublicBaseURL: b.PublicBaseURL, Log: log})
	if err != nil {
		return mediaKit{}, err
	}
	return mediaKit{blob: b, svc: svc, tx: tx}, nil
}
