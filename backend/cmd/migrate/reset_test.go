package main

import (
	"errors"
	"testing"
)

// Табличный тест parseURL + localOnly — без подключения к БД.
func TestLocalOnly(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"localhost", "postgres://u:p@localhost:5432/db", false},
		{"ipv4 loopback", "postgres://u:p@127.0.0.1:5432/db", false},
		{"ipv6 loopback в скобках", "postgres://u:p@[::1]:5432/db", false},
		{"прод-хост", "postgres://u:p@db.prod.example.com:5432/db", true},
		{"несколько хостов, все локальные", "postgres://u:p@localhost:5432,127.0.0.1:5432/db", false},
		{"несколько хостов, один нелокальный", "postgres://u:p@localhost:5432,db.prod.example.com:5432/db", true},
		{"unix-сокет", "host=/var/run/postgresql user=u dbname=db", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseURL(c.url)
			if err != nil {
				t.Fatalf("parseURL(%q) = %v", c.url, err)
			}
			err = localOnly(cfg)
			if (err != nil) != c.wantErr {
				t.Fatalf("localOnly(%q) = %v, wantErr=%v", c.url, err, c.wantErr)
			}
		})
	}
}

func TestParseURLRejectsMalformed(t *testing.T) {
	if _, err := parseURL("not a valid connection string %%%"); !errors.Is(err, errUnparsableURL) {
		t.Fatalf("err = %v, ждали errUnparsableURL", err)
	}
}
