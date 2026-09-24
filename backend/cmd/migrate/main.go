// Command migrate — миграции goose из вшитой FS: up | down | status | reset.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/migrate"
)

const usage = "up | down | status | reset"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Environ(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args, environ []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("нужна одна команда: %s", usage)
	}
	switch args[0] {
	case "up", "down", "status", "reset":
	default:
		return fmt.Errorf("неизвестная команда %q: %s", args[0], usage)
	}
	cfg, err := config.Load[config.Migrator]("MIGRATOR_", environ)
	if err != nil {
		return err
	}
	if args[0] == "reset" {
		// reset стирает все данные — проверяем локальность хоста ДО подключения к БД.
		if err := localOnly(cfg.DB.URL); err != nil {
			return err
		}
	}
	db, err := sql.Open("pgx", cfg.DB.URL)
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := migrate.NewProvider(db)
	if err != nil {
		return err
	}
	switch args[0] {
	case "up":
		res, err := p.Up(ctx)
		for _, r := range res {
			fmt.Fprintln(out, r)
		}
		return err
	case "down":
		r, err := p.Down(ctx)
		if r != nil {
			fmt.Fprintln(out, r)
		}
		return err
	case "reset":
		if _, err := p.DownTo(ctx, 0); err != nil {
			return err
		}
		_, err := p.Up(ctx)
		return err
	default: // status
		st, err := p.Status(ctx)
		for _, s := range st {
			fmt.Fprintf(out, "%-10s %s\n", s.State, s.Source.Path)
		}
		return err
	}
}
