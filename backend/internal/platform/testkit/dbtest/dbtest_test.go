// backend/internal/platform/testkit/dbtest/dbtest_test.go
package dbtest_test

import (
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "")
	c := dbtest.Config()
	if c.Host != "localhost" || c.Port != "15432" || c.User != "postgres" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestConfigFromEnvOverride(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "25432")
	if got := dbtest.Config().Port; got != "25432" {
		t.Fatalf("port = %q", got)
	}
}
