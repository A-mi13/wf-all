package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunFailsWithoutConfig(t *testing.T) {
	err := run(context.Background(), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "API_") {
		t.Fatalf("err = %v", err)
	}
}

// Висячая запятая в списке сетей даёт пустой элемент — отказ старта до подключения к базе
// (адрес базы недостижим: иначе ошибка была бы о подключении).
func TestRunRejectsEmptyNetElement(t *testing.T) {
	env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"API_JWT_SEEDS=" + testSeed, "API_HUMANCHECK_KEYS=" + testSeed, "API_TRUSTED_PROXIES=10.0.0.0/8,"}
	err := run(context.Background(), env, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "TrustedProxies") {
		t.Fatalf("err = %v", err)
	}
}

// Битые сиды JWT — отказ старта до подключения к базе.
func TestRunRejectsBadJWTSeeds(t *testing.T) {
	env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"API_JWT_SEEDS=short", "API_HUMANCHECK_KEYS=" + testSeed}
	if err := run(context.Background(), env, io.Discard); err == nil || !strings.Contains(err.Error(), "token:") {
		t.Fatalf("err = %v", err)
	}
}

// testSeed — валидный сид: 32 нулевых байта в base64 (только для тестов).
const testSeed = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
