// Package logx — единая настройка slog для всех бинарников.
package logx

import (
	"io"
	"log/slog"

	"wf/backend/internal/platform/config"
)

func New(w io.Writer, c config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.Level}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
