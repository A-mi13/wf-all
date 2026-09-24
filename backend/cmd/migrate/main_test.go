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
	if err := run(context.Background(), []string{"status"}, env, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0001_platform.sql") {
		t.Fatalf("status без 0001: %s", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	env := []string{"MIGRATOR_DATABASE_URL=postgres://x@127.0.0.1:1/wf"}
	err := run(context.Background(), []string{"drop-everything"}, env, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "up | down | status | reset") {
		t.Fatalf("err = %v", err)
	}
}

// reset не должен даже пытаться подключиться к нелокальной БД: хост
// db.prod.example.internal недостижим — если бы проверка происходила после
// sql.Open/подключения, тест бы завис или упал по таймауту, а не сразу.
func TestResetRefusesNonLocalHost(t *testing.T) {
	env := []string{"MIGRATOR_DATABASE_URL=postgres://x@db.prod.example.internal:5432/wf"}
	err := run(context.Background(), []string{"reset"}, env, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "reset запрещён") || !strings.Contains(err.Error(), "db.prod.example.internal") {
		t.Fatalf("err = %v", err)
	}
}

// Битая строка подключения с паролем не должна утекать в ошибку ни одной
// команды: для up/down/status строку раньше лениво разбирали sql.Open/goose,
// и pgx цитировал её с маскировкой пароля лишь «best effort».
func TestMalformedURLDoesNotLeakPassword(t *testing.T) {
	urls := []string{
		"host=127.0.0.1 password=S3cr3t PW dbname=wf",
		"postgres://u:S3cr3t@PW@host:bad/db",
		"host=127.0.0.1 password='S3cr3t PW dbname=wf",
	}
	for _, cmd := range []string{"up", "down", "status", "reset"} {
		for _, url := range urls {
			err := run(context.Background(), []string{cmd}, []string{"MIGRATOR_DATABASE_URL=" + url}, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil {
				t.Fatalf("%s с %q: ждали ошибку разбора", cmd, url)
			}
			for _, secret := range []string{"S3cr3t", "PW"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("%s с %q: ошибка содержит %q: %v", cmd, url, secret, err)
				}
			}
		}
	}
}

// Конфиг логов мигратора (MIGRATOR_LOG_*) реально применяется: goose пишет
// о каждой миграции через общий slog-логгер с нужным форматом.
func TestResetLogsMigrationsWithConfiguredLogger(t *testing.T) {
	var logs bytes.Buffer
	env := []string{"MIGRATOR_DATABASE_URL=" + dbtest.NewURL(t), "MIGRATOR_LOG_FORMAT=text"}
	if err := run(context.Background(), []string{"reset"}, env, &bytes.Buffer{}, &logs); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"service=migrate", "logger=goose", "0001_platform.sql"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("в логе нет %q:\n%s", want, logs.String())
		}
	}
}
