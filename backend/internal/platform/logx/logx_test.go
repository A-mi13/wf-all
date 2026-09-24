// backend/internal/platform/logx/logx_test.go
package logx_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/logx"
)

func TestJSONAndLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	log := logx.New(&buf, config.Log{Level: slog.LevelInfo, Format: "json"})
	log.Debug("скрыто")
	log.Info("видно", "k", 1)
	out := buf.String()
	if strings.Contains(out, "скрыто") || !strings.Contains(out, `"msg":"видно"`) {
		t.Fatalf("out = %s", out)
	}
}
