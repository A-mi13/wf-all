package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunFailsWithoutConfig(t *testing.T) {
	err := run(context.Background(), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "ADMIN_") {
		t.Fatalf("err = %v", err)
	}
}
