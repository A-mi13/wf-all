package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/testkit/dbtest"
)

// devKey — ключ подписи fs для тестов: 32 нулевых байта в base64.
const devKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// Спека media §6.5, deploy/server/README.md: worker media reconcile [--fix] — флаги разобраны (с --fix файл без строки
// удалён, без него — только в отчёте); неизвестный флаг и лишний аргумент — ошибка использования, код 2; хранилище
// none — «хранилище выключено», код 0; fs в прод-образе (WF_BUILD_KIND=prod) — отказ с именем ключа (config.LoadBlob,
// blobopen.Open признак образа не проверяет). Почта и кольцо identity разовой команде не нужны.
func TestCommandMediaReconcile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbEnv := "WORKER_DATABASE_URL=" + dbtest.NewURL(t)
	root := t.TempDir()
	fsEnv := []string{dbEnv, "WORKER_BLOB_BACKEND=fs", "WORKER_BLOB_FS_ROOT=" + root,
		"WORKER_BLOB_FS_BASE_URL=http://127.0.0.1:8080", "WORKER_BLOB_DEV_SIGN_KEY=" + devKey}
	b, err := blob.NewFSBuckets(root)
	if err != nil {
		t.Fatal(err)
	}
	orphan := uuid.Must(uuid.NewV7()).String() + "/full.webp"
	if err := b.Pub.Put(ctx, orphan, bytes.NewReader([]byte("x")), blob.PutOptions{ContentType: "image/webp"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run(ctx, []string{"media", "reconcile"}, fsEnv, &out, io.Discard); err != nil {
		t.Fatalf("без --fix: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "pub/"+orphan) || !strings.Contains(out.String(), "файлов без строки: 1, исправлено: 0") {
		t.Fatalf("отчёт без --fix:\n%s", out.String())
	}
	if _, err := b.Pub.Head(ctx, orphan); err != nil {
		t.Fatalf("без --fix файл удалён: %v", err)
	}
	out.Reset()
	if err := run(ctx, []string{"media", "reconcile", "--fix"}, fsEnv, &out, io.Discard); err != nil {
		t.Fatalf("--fix: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "исправлено: 1") {
		t.Fatalf("отчёт --fix:\n%s", out.String())
	}
	if _, err := b.Pub.Head(ctx, orphan); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("--fix не удалил файл без строки: %v", err)
	}

	for _, args := range [][]string{{"media", "reconcile", "--fixx"}, {"media", "reconcile", "--fix", "лишнее"}} {
		err := run(ctx, args, fsEnv, io.Discard, io.Discard)
		if !errors.Is(err, errUsage) || exitCode(err) != 2 || !strings.Contains(err.Error(), "media reconcile [--fix]") {
			t.Errorf("%q: %v (код %d)", args, err, exitCode(err))
		}
	}

	out.Reset()
	if err := run(ctx, []string{"media", "reconcile", "--fix"}, []string{dbEnv, blobNone}, &out, io.Discard); err != nil ||
		!strings.Contains(out.String(), "хранилище выключено") {
		t.Fatalf("none: %v\n%s", err, out.String())
	}
	err = run(ctx, []string{"media", "reconcile"}, append([]string{"WF_BUILD_KIND=prod"}, fsEnv...), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "WORKER_BLOB_BACKEND") || exitCode(err) != 1 {
		t.Fatalf("fs в прод-образе: %v", err)
	}
	// разовые команды, кроме media reconcile, ключей хранилища не читают: без WORKER_BLOB_BACKEND — до своей ошибки
	err = run(ctx, []string{"geo", "import"}, []string{dbEnv}, io.Discard, io.Discard)
	if !errors.Is(err, errUsage) {
		t.Fatalf("geo import без хранилища: %v", err)
	}
}

// Сервису воркера хранилище обязательно: без WORKER_BLOB_BACKEND — отказ старта до базы с именем ключа; fs в
// прод-образе — тоже.
func TestRunRequiresBlob(t *testing.T) {
	t.Parallel()
	base := []string{"WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1"}
	for _, kv := range mailEnv {
		if kv != blobNone {
			base = append(base, kv)
		}
	}
	if err := run(context.Background(), nil, base, io.Discard, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "WORKER_BLOB_BACKEND") {
		t.Fatalf("без хранилища: %v", err)
	}
	prod := append(base, "WF_BUILD_KIND=prod", "WORKER_BLOB_BACKEND=fs", "WORKER_BLOB_FS_ROOT="+t.TempDir(),
		"WORKER_BLOB_FS_BASE_URL=http://127.0.0.1:8080", "WORKER_BLOB_DEV_SIGN_KEY="+devKey)
	if err := run(context.Background(), nil, prod, io.Discard, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "WORKER_BLOB_BACKEND=fs") {
		t.Fatalf("fs в прод-образе: %v", err)
	}
}
