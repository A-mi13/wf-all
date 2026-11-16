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
	env = append(env, identityEnv...)
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
	env = append(env, identityEnv...)
	err := run(context.Background(), env, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "CF-Connecting-IP") {
		t.Fatalf("err = %v", err)
	}
}

// Битые сиды JWT — отказ старта до подключения к базе.
func TestRunRejectsBadJWTSeeds(t *testing.T) {
	env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"API_JWT_SEEDS=short", "API_HUMANCHECK_KEYS=" + testSeed}
	env = append(env, identityEnv...)
	if err := run(context.Background(), env, io.Discard); err == nil || !strings.Contains(err.Error(), "token:") {
		t.Fatalf("err = %v", err)
	}
}

// identityEnv — обязательные ключи identity (план I-2, Task 4): без них run падает на конфиге раньше проверяемого.
var identityEnv = []string{"API_IDENTITY_SECRET_KEYS=" + testSeed, "API_IDENTITY_DEVICE_KEYS=" + testSeed,
	"API_IDENTITY_TOTP_ISSUER=WF test", "API_LEGAL_DOCUMENTS=terms=0.1.0|https://example.com/terms,privacy=0.1.0|https://example.com/privacy"}

// testSeed — валидный сид: 32 нулевых байта в base64 (только для тестов).
const testSeed = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// Битые ключи PoW (не base64 или короче 32 байт) — отказ старта до подключения к базе.
func TestRunRejectsBadHumancheckKeys(t *testing.T) {
	for _, k := range []string{"не-base64", "c2hvcnQ="} {
		env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
			"API_JWT_SEEDS=" + testSeed, "API_HUMANCHECK_KEYS=" + k}
		env = append(env, identityEnv...)
		if err := run(context.Background(), env, io.Discard); err == nil || !strings.Contains(err.Error(), "humancheck") {
			t.Errorf("%q: err = %v", k, err)
		}
	}
}

// identityEnvWith — identityEnv с заменой одного ключа.
func identityEnvWith(key, value string) []string {
	out := make([]string, 0, len(identityEnv))
	for _, kv := range identityEnv {
		if strings.HasPrefix(kv, key+"=") {
			kv = key + "=" + value
		}
		out = append(out, kv)
	}
	return out
}

// Битое кольцо identity (не base64; пустые элементы списка) — отказ старта до подключения к базе (план I-2, Task 8).
func TestRunRejectsBadIdentityRings(t *testing.T) {
	for _, key := range []string{"API_IDENTITY_SECRET_KEYS", "API_IDENTITY_DEVICE_KEYS"} {
		for _, v := range []string{"abc", ","} {
			env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
				"API_JWT_SEEDS=" + testSeed, "API_HUMANCHECK_KEYS=" + testSeed}
			env = append(env, identityEnvWith(key, v)...)
			if err := run(context.Background(), env, io.Discard); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("%s=%q: err = %v", key, v, err)
			}
		}
	}
}

// Негодный API_LEGAL_DOCUMENTS — отказ старта до подключения к базе.
func TestRunRejectsBadLegalDocuments(t *testing.T) {
	env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"API_JWT_SEEDS=" + testSeed, "API_HUMANCHECK_KEYS=" + testSeed}
	env = append(env, identityEnvWith("API_LEGAL_DOCUMENTS", "terms=1.0.0|http://example.com/terms")...)
	if err := run(context.Background(), env, io.Discard); err == nil || !strings.Contains(err.Error(), "API_LEGAL_DOCUMENTS") {
		t.Fatalf("err = %v", err)
	}
}

// Файла базы города по IP нет — отказ старта до подключения к базе.
func TestRunRejectsMissingIPGeoDB(t *testing.T) {
	env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"API_JWT_SEEDS=" + testSeed, "API_HUMANCHECK_KEYS=" + testSeed, "API_IPGEO_DB=nope/missing.mmdb"}
	env = append(env, identityEnv...)
	if err := run(context.Background(), env, io.Discard); err == nil || !strings.Contains(err.Error(), "ipgeo") {
		t.Fatalf("err = %v", err)
	}
}

// Битый API_WELLKNOWN_ANDROID (отпечаток не SHA-256) — ошибка старта до подключения к базе, не паника.
func TestRunRejectsBadWellKnownAndroid(t *testing.T) {
	env := []string{"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"API_JWT_SEEDS=" + testSeed, "API_HUMANCHECK_KEYS=" + testSeed, "API_WELLKNOWN_ANDROID=app.wf=AA:BB"}
	env = append(env, identityEnv...)
	if err := run(context.Background(), env, io.Discard); err == nil || !strings.Contains(err.Error(), "API_WELLKNOWN_ANDROID") {
		t.Fatalf("err = %v", err)
	}
}
