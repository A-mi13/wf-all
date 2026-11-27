package reconcile_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/media"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/media/reconcile"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

var at = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func put(t *testing.T, s blob.Store, key string) {
	t.Helper()
	if err := s.Put(context.Background(), key, bytes.NewReader([]byte("x")), blob.PutOptions{ContentType: "image/webp"}); err != nil {
		t.Fatal(err)
	}
}

func has(t *testing.T, s blob.Store, key string) bool {
	t.Helper()
	_, err := s.Head(context.Background(), key)
	return err == nil
}

// §6.5 «Бэкапы»: после восстановления — сверка. Строка ready без файла варианта и файлы в pub/priv без строки
// объекта — числа и ключи в отчёте (без ПД); без --fix ничего не меняется; с --fix удаляются только файлы без строки
// (строку ready в rejected перевести нельзя — переход запрещён, она остаётся оператору).
func TestReconcileReport(t *testing.T) {
	t.Parallel()
	p := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	clk := clocktest.New(at)
	opened, err := mediatest.MemoryBlob(clk)
	if err != nil {
		t.Fatal(err)
	}
	b := opened.Buckets
	by := media.Uploader{UserID: uuid.Must(uuid.NewV7())}
	seedReady := func(purpose string, v media.Visibility) uuid.UUID {
		oid, err := mediatest.SeedObject(ctx, p.Owner, mediatest.Seed{Purpose: purpose, Visibility: v, State: media.StateReady,
			Owner: media.OwnerRef{Type: mediatest.OwnerType, ID: uuid.Must(uuid.NewV7())}, By: by, At: at,
			Variants: []string{"thumb", "full"}})
		if err != nil {
			t.Fatal(err)
		}
		return oid
	}
	whole := seedReady(mediatest.PurposeImage, media.VisibilityPublic)
	put(t, b.Pub, mediatest.VariantKey(whole, "thumb"))
	put(t, b.Pub, mediatest.VariantKey(whole, "full"))
	broken := seedReady(mediatest.PurposeImagePrivate, media.VisibilityPrivate)
	put(t, b.Priv, mediatest.VariantKey(broken, "thumb")) // full потерян
	detached, err := mediatest.SeedObject(ctx, p.Owner, mediatest.Seed{Purpose: mediatest.PurposeImage,
		Visibility: media.VisibilityPublic, State: media.StateDetached,
		Owner: media.OwnerRef{Type: mediatest.OwnerType, ID: uuid.Must(uuid.NewV7())}, By: by, At: at})
	if err != nil {
		t.Fatal(err)
	}
	put(t, b.Pub, mediatest.VariantKey(detached, "thumb")) // строка есть — не сирота (его удаляет media.purge)
	orphanPub := mediatest.VariantKey(uuid.Must(uuid.NewV7()), "full")
	orphanPriv := mediatest.VariantKey(uuid.Must(uuid.NewV7()), "thumb")
	put(t, b.Pub, orphanPub)
	put(t, b.Priv, orphanPriv)

	var out bytes.Buffer
	rep, err := reconcile.Run(ctx, p.As, b, clk, false, &out)
	if err != nil {
		t.Fatal(err)
	}
	if rep != (reconcile.Report{RowsWithoutFile: 1, FilesWithoutRow: 2}) {
		t.Fatalf("отчёт: %+v\n%s", rep, out.String())
	}
	for _, want := range []string{broken.String(), mediatest.VariantKey(broken, "full"), orphanPub, orphanPriv} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("в выводе нет %s:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), by.UserID.String()) {
		t.Fatalf("в выводе загрузивший:\n%s", out.String())
	}
	if !has(t, b.Pub, orphanPub) || !has(t, b.Priv, orphanPriv) {
		t.Fatal("без --fix удалён файл")
	}

	out.Reset()
	rep, err = reconcile.Run(ctx, p.As, b, clk, true, &out)
	if err != nil {
		t.Fatal(err)
	}
	if rep != (reconcile.Report{RowsWithoutFile: 1, FilesWithoutRow: 2, Fixed: 2}) {
		t.Fatalf("отчёт --fix: %+v\n%s", rep, out.String())
	}
	if has(t, b.Pub, orphanPub) || has(t, b.Priv, orphanPriv) {
		t.Fatal("--fix не удалил файл без строки")
	}
	for _, k := range []string{mediatest.VariantKey(whole, "thumb"), mediatest.VariantKey(whole, "full"),
		mediatest.VariantKey(detached, "thumb")} {
		if !has(t, b.Pub, k) {
			t.Fatalf("--fix удалил файл со строкой: %s", k)
		}
	}
	if !has(t, b.Priv, mediatest.VariantKey(broken, "thumb")) {
		t.Fatal("--fix удалил файл объекта со строкой")
	}
	var state string
	if err := p.Owner.QueryRow(ctx, `SELECT state FROM media_objects WHERE id = $1`, broken).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("строка без файла изменена: %s %v", state, err)
	}
}
