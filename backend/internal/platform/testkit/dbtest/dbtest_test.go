package dbtest_test

import (
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "")
	t.Setenv("WF_PG_PORT", "")
	c := dbtest.Config()
	if c.Host != "localhost" || c.Port != "15432" || c.User != "postgres" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestConfigFromEnvOverride(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "25432")
	t.Setenv("WF_PG_PORT", "25433")
	if got := dbtest.Config().Port; got != "25432" {
		t.Fatalf("port = %q, want WF_TEST_PG_PORT", got)
	}
}

// Порт dev-базы из deploy/dev/.env (WF_PG_PORT) — порт тестов по умолчанию:
// меняешь порт в одном месте, тесты идут туда же.
func TestConfigFallsBackToDevPort(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "")
	t.Setenv("WF_PG_PORT", "25433")
	if got := dbtest.Config().Port; got != "25433" {
		t.Fatalf("port = %q, want WF_PG_PORT", got)
	}
}
