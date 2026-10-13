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
// доходит до peer.Config. Остальные обязательные переменные заданы: иначе отказ был бы про них.
func TestRunRejectsClientIPHeaderWithoutProxies(t *testing.T) {
	env := []string{"ADMIN_HTTP_ADDR=127.0.0.1:0", "ADMIN_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"ADMIN_ORIGIN=http://127.0.0.1:5173",
		// 32 нулевых байта: годный ключ без энтропии (gitleaks не примет за секрет)
		"ADMIN_STAFF_SECRET_KEYS=" + strings.Repeat("A", 43) + "=",
		"ADMIN_MAIL_SMTP_ADDR=127.0.0.1:1", "ADMIN_MAIL_FROM=WF <noreply@wf.local>",
		"ADMIN_CLIENT_IP_HEADER=CF-Connecting-IP"}
	err := run(context.Background(), env, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "CF-Connecting-IP") {
		t.Fatalf("err = %v", err)
	}
}

// Неверный ADMIN_ORIGIN и аллоулист «пускает всех» — отказ старта до подключения к базе.
func TestRunRejectsBadOriginAndAllowlist(t *testing.T) {
	base := []string{"ADMIN_HTTP_ADDR=127.0.0.1:0", "ADMIN_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"ADMIN_STAFF_SECRET_KEYS=" + strings.Repeat("A", 43) + "=",
		"ADMIN_MAIL_SMTP_ADDR=127.0.0.1:1", "ADMIN_MAIL_FROM=WF <noreply@wf.local>"}
	for _, extra := range [][]string{
		{"ADMIN_ORIGIN=http://127.0.0.1:5173/"},
		{"ADMIN_ORIGIN=http://127.0.0.1:5173", "ADMIN_IP_ALLOWLIST=10.0.0.0/8,0.0.0.0/0"},
	} {
		err := run(context.Background(), append(append([]string{}, base...), extra...), io.Discard)
		if err == nil || !strings.Contains(err.Error(), "ADMIN_") || strings.Contains(err.Error(), "connect") {
			t.Errorf("%v: err = %v", extra, err)
		}
	}
}
