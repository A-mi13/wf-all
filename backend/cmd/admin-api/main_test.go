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

// Заголовок CDN без прокси хостинга — отказ старта до подключения к базе: ADMIN_CLIENT_IP_HEADER
// доходит до peer.Config.
func TestRunRejectsClientIPHeaderWithoutProxies(t *testing.T) {
	env := []string{"ADMIN_HTTP_ADDR=127.0.0.1:0", "ADMIN_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"ADMIN_CLIENT_IP_HEADER=CF-Connecting-IP"}
	err := run(context.Background(), env, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "CF-Connecting-IP") {
		t.Fatalf("err = %v", err)
	}
}
