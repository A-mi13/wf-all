package jobs_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/media"
	"wf/backend/internal/media/jobs"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/blob"
)

func purgeJob(a jobs.PurgeArgs) *river.Job[jobs.PurgeArgs] {
	return &river.Job[jobs.PurgeArgs]{JobRow: &rivertype.JobRow{ID: 3, Attempt: 1, MaxAttempts: 25, Kind: "media.purge"}, Args: a}
}

// §3.8: media.purge удаляет каждый ключ (нет файла — не ошибка), затем сбрасывает кэш по публичным адресам; повтор —
// тот же итог. Неизвестный бакет и негодный ключ — отмена (повтор не поможет).
func TestPurgeIdempotent(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	ctx := context.Background()
	oid := uuid.Must(uuid.NewV7())
	k1, k2 := mediatest.VariantKey(oid, "thumb"), mediatest.VariantKey(oid, "full")
	for _, k := range []string{k1, k2} {
		if err := e.mem.Pub.Put(ctx, k, bytes.NewReader([]byte("x")), blob.PutOptions{ContentType: "image/webp"}); err != nil {
			t.Fatal(err)
		}
	}
	missing := mediatest.VariantKey(oid, "card")
	urls := []string{mediatest.PublicBaseURL + k1, mediatest.PublicBaseURL + k2}
	a := jobs.PurgeArgs{V: 1, Bucket: jobs.BucketPub, Keys: []string{k1, missing, k2}, PublicURLs: urls}
	for i := range 2 {
		if err := e.gw.Work(ctx, purgeJob(a)); err != nil {
			t.Fatalf("проход %d: %v", i+1, err)
		}
		if got := keys(t, e.mem.Pub, oid); len(got) != 0 {
			t.Fatalf("проход %d: остались %v", i+1, got)
		}
	}
	if got := e.purger.got(); !slices.Equal(got, append(slices.Clone(urls), urls...)) {
		t.Fatalf("Purger: %v", got)
	}
	if n := e.pub.count("delete"); n != 6 {
		t.Fatalf("Delete: %d", n)
	}
	// закрытое и входящее — свои бакеты
	if err := e.mem.Priv.Put(ctx, k1, bytes.NewReader([]byte("x")), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.mem.In.Put(ctx, oid.String(), bytes.NewReader([]byte("x")), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []jobs.PurgeArgs{{V: 1, Bucket: jobs.BucketPriv, Keys: []string{k1}}, {V: 1, Bucket: jobs.BucketIn, Keys: []string{oid.String()}}} {
		if err := e.gw.Work(ctx, purgeJob(a)); err != nil {
			t.Fatal(err)
		}
	}
	if len(keys(t, e.mem.Priv, oid)) != 0 || inExists(t, e.mem.In, oid) {
		t.Fatal("priv или in не удалены")
	}
	for name, bad := range map[string]jobs.PurgeArgs{
		"бакет": {V: 1, Bucket: "etc", Keys: []string{k1}},
		"ключ":  {V: 1, Bucket: jobs.BucketPub, Keys: []string{"../etc"}},
	} {
		if err := e.gw.Work(ctx, purgeJob(bad)); !errors.Is(err, &river.JobCancelError{}) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Сбой хранилища — обычная ошибка (повтор River, до 25 попыток), Purger не зовётся до удаления файлов.
func TestPurgeStoreErrorRetries(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	d := e.deps
	d.Blob.Buckets.Pub = failingDelete{e.mem.Pub}
	w, err := jobs.NewPurgeWorker(d)
	if err != nil {
		t.Fatal(err)
	}
	k := mediatest.VariantKey(uuid.Must(uuid.NewV7()), "full")
	err = w.Work(context.Background(), purgeJob(jobs.PurgeArgs{V: 1, Bucket: jobs.BucketPub, Keys: []string{k},
		PublicURLs: []string{mediatest.PublicBaseURL + k}}))
	if !errors.Is(err, errBlink) || errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("сбой: %v", err)
	}
	if got := e.purger.got(); len(got) != 0 {
		t.Fatalf("Purger до удаления файлов: %v", got)
	}
}

type failingDelete struct{ blob.Store }

func (failingDelete) Delete(context.Context, string) error { return errBlink }

// Режим none (m3-3): media.purge отменяется.
func TestPurgeNoneCancels(t *testing.T) {
	t.Parallel()
	w, err := jobs.NewPurgeWorker(jobs.WorkerDeps{Blob: blob.Opened{}})
	if err != nil {
		t.Fatal(err)
	}
	err = w.Work(context.Background(), purgeJob(jobs.PurgeArgs{V: 1, Bucket: jobs.BucketIn, Keys: []string{"x"}}))
	if !errors.Is(err, &river.JobCancelError{}) || !errors.Is(err, media.ErrUnavailable) {
		t.Fatalf("none: %v", err)
	}
}
