package ratelimit_test

import (
	"os"
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

// Пакет с базой: умолчание -parallel (dbtest.DefaultParallel) и уборка общих клонов — dbtest.Main.
func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }
