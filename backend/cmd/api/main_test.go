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

// Заголовок CDN без прокси хостинга — отказ старта до подключения к базе: API_CLIENT_IP_HEADER
// доходит до peer.Config.
func TestRunRejectsClientIPHeaderWithoutProxies(t *testing.T) {
	env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"API_JWT_SEEDS=" + testSeed, "API_HUMANCHECK_KEYS=" + testSeed, "API_CLIENT_IP_HEADER=CF-Connecting-IP"}
	err := run(context.Background(), env, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "CF-Connecting-IP") {
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

// Битые ключи PoW (не base64 или короче 32 байт) — отказ старта до подключения к базе.
func TestRunRejectsBadHumancheckKeys(t *testing.T) {
	for _, k := range []string{"не-base64", "c2hvcnQ="} {
		env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
			"API_JWT_SEEDS=" + testSeed, "API_HUMANCHECK_KEYS=" + k}
		if err := run(context.Background(), env, io.Discard); err == nil || !strings.Contains(err.Error(), "humancheck") {
			t.Errorf("%q: err = %v", k, err)
		}
	}
}
