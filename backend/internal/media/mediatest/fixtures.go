package mediatest

import (
	"log/slog"

	"wf/backend/internal/media"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/clock"
)

// Адреса хранилища в памяти: подпись — как у FS (R4), публичное — /_media/pub/.
const (
	BaseURL       = "http://media.test"
	PublicBaseURL = BaseURL + "/_media/pub/"
)

// devSignKey — ключ подписи тестов (не секрет: только Memory).
var devSignKey = []byte("mediatest-dev-sign-key-0123456789abcdef")

// MemoryBlob — открытое хранилище на памяти с подписью DevSigner на часах clk (проверка — Opened.Dev).
func MemoryBlob(clk clock.Clock) (blob.Opened, error) {
	signer, err := blob.NewDevSigner(devSignKey, BaseURL, clk)
	if err != nil {
		return blob.Opened{}, err
	}
	return blob.Opened{
		Buckets:       blob.NewMemoryBuckets(),
		Presigner:     signer,
		Purger:        blob.NoopPurger{},
		Dev:           signer,
		PublicBaseURL: PublicBaseURL,
		Available:     true,
	}, nil
}

// Registry — реестр из E2EPurposes.
func Registry() (*media.Registry, error) { return media.NewRegistry(E2EPurposes()...) }

// Discard — журнал тестов.
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }
