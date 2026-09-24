package main

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// localOnly отказывает, если connection string ведёт не на локальный хост.
// reset (DownTo(0) + Up) стирает все данные — без этой проверки его можно
// случайно направить на прод. Разбор строки — через pgx.ParseConfig, а не
// вручную: он уже умеет multi-host (host1,host2,...) и IPv6-литералы в
// скобках, и это тот же парсер, что реально подключается к БД.
func localOnly(databaseURL string) error {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("разбор MIGRATOR_DATABASE_URL: %w", err)
	}
	hosts := []string{cfg.Host}
	for _, fb := range cfg.Fallbacks {
		hosts = append(hosts, fb.Host)
	}
	for _, host := range hosts {
		if !isLocalHost(host) {
			return fmt.Errorf("reset запрещён: хост %q не локальный (разрешены только localhost, 127.0.0.1, ::1)", host)
		}
	}
	return nil
}

func isLocalHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
