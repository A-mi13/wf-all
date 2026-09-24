package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// errUnparsableURL — ошибка разбора MIGRATOR_DATABASE_URL. Исходную ошибку pgx
// не заворачиваем: она цитирует строку, а пароль маскирует лишь «best effort»
// (хвост после пробела в пароле, '@' в пароле, незакрытая кавычка — утекают).
var errUnparsableURL = errors.New("MIGRATOR_DATABASE_URL: строка подключения не разбирается (значение не выводится)")

// parseURL разбирает строку подключения один раз — для проверки localOnly и
// для самого подключения. Тот же парсер, что реально подключается к БД: он
// уже умеет multi-host (host1,host2,...) и IPv6-литералы в скобках.
func parseURL(databaseURL string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, errUnparsableURL
	}
	return cfg, nil
}

// localOnly отказывает, если конфиг подключения ведёт не на локальный хост.
// reset (DownTo(0) + Up) стирает все данные — без этой проверки его можно
// случайно направить на прод. Unix-сокет тоже отвергается (консервативно).
func localOnly(cfg *pgx.ConnConfig) error {
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
