package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/cmd/internal/mediapurposes"
	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/media"
	"wf/backend/internal/media/httpdev"
	"wf/backend/internal/media/intx"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/blob/blobopen"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
)

// openBlob — хранилище api до базы (спека media §6.4, Task 4): API_BLOB_* через config.LoadBlob (fs в прод-образе —
// отказ старта), blobopen.Open (в сеть не ходит) и реестр целей бинарника (R12: только mediapurposes).
func openBlob(ctx context.Context, environ []string, clk clock.Clock) (blob.Opened, *media.Registry, error) {
	cfg, err := config.LoadBlob("API_", environ)
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

// mediaKit — media процесса api: открытое хранилище, реестр, сервис (билеты, чтение, ссылки) и операции в транзакции
// владельца. Владельцев в коде пока нет (спека §12a): их ручки получат сервис отсюда; сейчас его берут тестовые ручки
// сборки wf_e2e (mediaE2E).
type mediaKit struct {
	blob blob.Opened
	reg  *media.Registry
	svc  media.Service
	tx   *intx.Tx
}

// newMedia — сборка на пуле api: клиент River только для вставки (media.process в транзакции «завершить»), лимитер
// игроков (суточный счёт билетов, R19). intx.Deps.PublicBaseURL — то же значение из blobopen.Open, что у сервиса.
func newMedia(log *slog.Logger, pool *pgxpool.Pool, b blob.Opened, reg *media.Registry, clk clock.Clock,
	limiter ratelimit.Limiter) (mediaKit, error) {
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		return mediaKit{}, err
	}
	svc, err := media.New(media.Deps{Pool: pool, Clock: clk, Registry: reg, Blob: b, Limiter: limiter, River: ins, Log: log})
	if err != nil {
		return mediaKit{}, err
	}
	tx, err := intx.New(intx.Deps{Clock: clk, Registry: reg, River: ins, PublicBaseURL: b.PublicBaseURL, Log: log})
	if err != nil {
		return mediaKit{}, err
	}
	return mediaKit{blob: b, reg: reg, svc: svc, tx: tx}, nil
}

// devMedia — dev-пути хранилища (только fs; иначе nil — public их не монтирует, спека §6.3).
func (k mediaKit) devMedia(log *slog.Logger) public.DevMedia {
	if h := httpdev.New(k.blob, k.reg, log); h != nil {
		return h
	}
	return nil
}
