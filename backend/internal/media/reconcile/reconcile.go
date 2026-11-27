// Package reconcile — сверка хранилища с базой после восстановления из бэкапа (спека media §6.5): команда
// worker media reconcile [--fix] под ролью worker.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/media"
	"wf/backend/internal/media/internal/store/mediadb"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/clock"
)

// page — строк вариантов за запрос.
const page = 500

// Report — итог сверки: объекты ready, у которых нет хотя бы одного файла варианта; файлы в pub/priv без строки
// объекта; удалено с --fix.
type Report struct {
	RowsWithoutFile, FilesWithoutRow int
	Fixed                            int
}

// Run — сверка: (1) строки ready без файлов вариантов — в отчёт (перевести их некуда: из ready в rejected перехода
// нет — решает оператор, правки нет и с fix); (2) файлы в pub и priv, чей id (ключ <id>/…) не принадлежит ни одной
// строке media_objects, — в отчёт, с fix — Delete. Файл объекта со строкой (в том числе ещё не опубликованный или
// снятый — его удаляет media.purge) сиротой не считается: сверка не гоняется с обработкой. Вывод — только id и
// ключи, без ПД.
func Run(ctx context.Context, pool *pgxpool.Pool, b blob.Buckets, clk clock.Clock, fix bool, out io.Writer) (Report, error) {
	var rep Report
	q := mediadb.New(pool)
	fmt.Fprintf(out, "сверка хранилища %s, исправление: %v\n", clk.Now().UTC().Format(time.RFC3339), fix)
	if err := rowsWithoutFiles(ctx, q, b, out, &rep); err != nil {
		return rep, err
	}
	for _, bk := range []struct {
		name string
		s    blob.Store
	}{{"pub", b.Pub}, {"priv", b.Priv}} {
		if err := filesWithoutRows(ctx, q, bk.name, bk.s, fix, out, &rep); err != nil {
			return rep, err
		}
	}
	fmt.Fprintf(out, "строк без файла: %d, файлов без строки: %d, исправлено: %d\n", rep.RowsWithoutFile,
		rep.FilesWithoutRow, rep.Fixed)
	return rep, nil
}

// rowsWithoutFiles — ключи вариантов строк ready по порядку ключа (варианты объекта идут подряд): нет файла в бакете
// видимости — объект в отчёт (один раз).
func rowsWithoutFiles(ctx context.Context, q *mediadb.Queries, b blob.Buckets, out io.Writer, rep *Report) error {
	after := ""
	var last uuid.UUID
	for {
		rows, err := q.ReadyVariantKeys(ctx, mediadb.ReadyVariantKeysParams{After: after, Lim: page})
		if err != nil {
			return fmt.Errorf("media/reconcile: варианты: %w", err)
		}
		for _, r := range rows {
			s, name := b.Pub, "pub"
			if media.Visibility(r.Visibility) == media.VisibilityPrivate {
				s, name = b.Priv, "priv"
			}
			_, err := s.Head(ctx, r.BlobKey)
			switch {
			case errors.Is(err, blob.ErrNotFound):
				fmt.Fprintf(out, "строка без файла: объект %s, нет %s/%s\n", r.ObjectID, name, r.BlobKey)
				if r.ObjectID != last {
					rep.RowsWithoutFile++
					last = r.ObjectID
				}
			case err != nil:
				return fmt.Errorf("media/reconcile: %s/%s: %w", name, r.BlobKey, err)
			}
		}
		if len(rows) < page {
			return nil
		}
		after = rows[len(rows)-1].BlobKey
	}
}

// filesWithoutRows — файлы бакета, чей id (сегмент до «/») не принадлежит строке объекта; чужой ключ (не id) — тоже
// сирота: бакеты отданы media целиком. Удаление — после обхода (List не обязан терпеть удаление на ходу).
func filesWithoutRows(ctx context.Context, q *mediadb.Queries, name string, s blob.Store, fix bool, out io.Writer, rep *Report) error {
	var orphans []string
	known := map[uuid.UUID]bool{}
	err := s.List(ctx, "", func(key string) error {
		head, _, _ := strings.Cut(key, "/")
		oid, err := uuid.Parse(head)
		if err == nil && oid.String() == head {
			exists, ok := known[oid]
			if !ok {
				if exists, err = q.ObjectExists(ctx, oid); err != nil {
					return fmt.Errorf("media/reconcile: строка %s: %w", oid, err)
				}
				known[oid] = exists
			}
			if exists {
				return nil
			}
		}
		orphans = append(orphans, key)
		return nil
	})
	if err != nil {
		return fmt.Errorf("media/reconcile: обход %s: %w", name, err)
	}
	for _, k := range orphans {
		rep.FilesWithoutRow++
		if !fix {
			fmt.Fprintf(out, "файл без строки: %s/%s\n", name, k)
			continue
		}
		if err := s.Delete(ctx, k); err != nil {
			return fmt.Errorf("media/reconcile: удаление %s/%s: %w", name, k, err)
		}
		rep.Fixed++
		fmt.Fprintf(out, "файл без строки: %s/%s — удалён\n", name, k)
	}
	return nil
}
