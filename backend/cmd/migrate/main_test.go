package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

func TestStatusListsMigrations(t *testing.T) {
	var out bytes.Buffer
	env := []string{"MIGRATOR_DATABASE_URL=" + dbtest.NewURL(t)}
	if err := run(context.Background(), []string{"status"}, env, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0001_platform.sql") {
		t.Fatalf("status без 0001: %s", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	env := []string{"MIGRATOR_DATABASE_URL=postgres://x@127.0.0.1:1/wf"}
	err := run(context.Background(), []string{"drop-everything"}, env, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "up | down | status | reset") {
		t.Fatalf("err = %v", err)
	}
}
