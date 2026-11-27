package jobs_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/media"
	"wf/backend/internal/media/jobs"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/id"
)

func cleanupJob() *river.Job[jobs.CleanupArgs] {
	return &river.Job[jobs.CleanupArgs]{JobRow: &rivertype.JobRow{ID: 3, Attempt: 1, MaxAttempts: 5, Kind: "media.cleanup"},
		Args: jobs.CleanupArgs{V: 1}}
}

// clean — один проход чистки на часах e.clk.
func (e *env) clean(t *testing.T) {
	t.Helper()
	if err := e.cw.Work(context.Background(), cleanupJob()); err != nil {
		t.Fatalf("чистка: %v", err)
	}
}

func (e *env) exists(t *testing.T, oid uuid.UUID) bool {
	t.Helper()
	return count(t, e.pools.Owner, `SELECT count(*) FROM media_objects WHERE id = $1`, oid) == 1
}

// seedAt — объект цели e2e_image в состоянии st: created_at/uploaded_at/ended_at = at; у pending_upload билет до
// expires.
func (e *env) seedAt(t *testing.T, purpose string, st media.State, at, expires time.Time) uuid.UUID {
	t.Helper()
	p, _ := e.reg.Get(purpose)
	s := mediatest.Seed{Purpose: purpose, Visibility: p.Visibility, State: st, By: media.Uploader{UserID: uuid.Must(uuid.NewV7())},
		At: at, ExpiresAt: expires}
	if st == media.StatePendingUpload {
		s.Intended = newOwner()
	} else {
		s.Owner = newOwner()
	}
	oid, err := mediatest.SeedObject(context.Background(), e.pools.Owner, s)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func put(t *testing.T, s blob.Store, key string) {
	t.Helper()
	if err := s.Put(context.Background(), key, bytes.NewReader([]byte("x")), blob.PutOptions{ContentType: "image/webp"}); err != nil {
		t.Fatal(err)
	}
}

// §3.9: окно «завершить» (expires_at + 1 ч) — граница строгая: ровно на границе входящее не трогается, через 1 мкс —
// удаляется (timestamptz хранит микросекунды: граница 1 нс в SQL неразличима, чистка отстаёт меньше чем на 1 мкс —
// в безопасную сторону: прикрепление отказывает с now ≥ expires_at + 1 ч); строка (и билет каскадом) — только когда created_at старше суток (суточная квота байт считается по
// строкам), тоже строго.
func TestCleanupExpiredPending(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	created := at
	expires := at.Add(15 * time.Minute)
	oid := e.seedAt(t, mediatest.PurposeImage, media.StatePendingUpload, created, expires)
	put(t, e.mem.In, oid.String())

	e.clk.Set(expires.Add(time.Hour))
	e.clean(t)
	if !inExists(t, e.mem.In, oid) || !e.exists(t, oid) {
		t.Fatal("ровно на границе окна входящее или строка удалены")
	}
	e.clk.Set(expires.Add(time.Hour + time.Microsecond))
	e.clean(t)
	if inExists(t, e.mem.In, oid) {
		t.Fatal("окно прошло, входящее осталось")
	}
	if !e.exists(t, oid) {
		t.Fatal("строка моложе суток удалена — квота байт вернулась бы раньше срока")
	}
	e.clk.Set(created.Add(24 * time.Hour))
	e.clean(t)
	if !e.exists(t, oid) {
		t.Fatal("строка ровно суточной давности удалена")
	}
	e.clk.Set(created.Add(24*time.Hour + time.Nanosecond))
	e.clean(t)
	if e.exists(t, oid) {
		t.Fatal("строка старше суток осталась")
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_uploads WHERE object_id = $1`, oid); n != 0 {
		t.Fatalf("билетов осталось: %d", n)
	}
}

// Пачки по 100 и курсор: строки моложе суток не занимают пачку — 101-я и следующие просроченные доходят за один проход.
func TestCleanupExpiredPendingPages(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	expires := at.Add(15 * time.Minute)
	ids := make([]uuid.UUID, 0, 105)
	for range 105 {
		oid := e.seedAt(t, mediatest.PurposeImage, media.StatePendingUpload, at, expires)
		put(t, e.mem.In, oid.String())
		ids = append(ids, oid)
	}
	e.clk.Set(expires.Add(2 * time.Hour))
	e.clean(t)
	for _, oid := range ids {
		if inExists(t, e.mem.In, oid) {
			t.Fatalf("входящее %s осталось после прохода", oid)
		}
	}
}

func processJobs(t *testing.T, e *env, oid uuid.UUID) int {
	t.Helper()
	return count(t, e.pools.Owner, `SELECT count(*) FROM river_job WHERE kind = 'media.process' AND args->>'object_id' = $1
		AND queue = 'media'`, oid.String())
}

// Зависшие uploaded/processing (uploaded_at старше 1 ч, строго) — media.process заново; повтор чистки дубля не ставит.
func TestCleanupRestartsStale(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	e.clk.Set(at.Add(61 * time.Minute))
	stale := e.seedAt(t, mediatest.PurposeImage, media.StateUploaded, at, time.Time{})
	crashed := e.seedAt(t, mediatest.PurposeImage, media.StateProcessing, at, time.Time{})
	edge := e.seedAt(t, mediatest.PurposeImage, media.StateUploaded, at.Add(time.Minute), time.Time{})
	ready := e.seedAt(t, mediatest.PurposeImage, media.StateReady, at, time.Time{})
	for range 2 {
		e.clean(t)
		if a, b := processJobs(t, e, stale), processJobs(t, e, crashed); a != 1 || b != 1 {
			t.Fatalf("задач по зависшим: uploaded %d, processing %d — ждали по одной", a, b)
		}
		if a, b := processJobs(t, e, edge), processJobs(t, e, ready); a != 0 || b != 0 {
			t.Fatalf("задачи на границе (%d) или по ready (%d)", a, b)
		}
	}
}

// §3.9: чистка уступает сценарию — строка под FOR UPDATE параллельной транзакции пропускается (SKIP LOCKED), чистка
// не ждёт её и доделывает остальное; срок ctx — чтобы ожидание замка было падением, а не зависанием.
func TestCleanupYieldsToScenario(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	expires := at.Add(15 * time.Minute)
	lockedPending := e.seedAt(t, mediatest.PurposeImage, media.StatePendingUpload, at, expires)
	freePending := e.seedAt(t, mediatest.PurposeImage, media.StatePendingUpload, at, expires)
	lockedEnded := e.seedAt(t, mediatest.PurposeImage, media.StateDetached, at, time.Time{})
	freeEnded := e.seedAt(t, mediatest.PurposeImage, media.StateDetached, at, time.Time{})
	e.clk.Set(at.Add(48 * time.Hour))
	release1 := lockedTx(t, e.pools.Owner, lockedPending)
	release2 := lockedTx(t, e.pools.Owner, lockedEnded)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.cw.Work(ctx, cleanupJob()); err != nil {
		t.Fatalf("чистка ждала замок сценария: %v", err)
	}
	release1()
	release2()
	if !e.exists(t, lockedPending) || !e.exists(t, lockedEnded) {
		t.Fatal("строка под замком сценария удалена")
	}
	if e.exists(t, freePending) || e.exists(t, freeEnded) {
		t.Fatal("свободные строки не удалены")
	}
}

// §3.9: detached/taken_down — 24 ч после ended_at, rejected — 30 сут (строго); удаляются строки и файлы вариантов
// в бакете видимости; моложе срока — не трогаются.
func TestCleanupFinalStates(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	day, month := 24*time.Hour, 30*24*time.Hour
	type row struct {
		purpose string
		state   media.State
		age     time.Duration
		gone    bool
	}
	rows := []row{
		{mediatest.PurposeImage, media.StateDetached, day + time.Second, true},
		{mediatest.PurposeImage, media.StateDetached, day, false},
		{mediatest.PurposeImage, media.StateDetached, 23 * time.Hour, false},
		{mediatest.PurposeImagePrivate, media.StateTakenDown, day + time.Second, true},
		{mediatest.PurposeImage, media.StateTakenDown, 23 * time.Hour, false},
		{mediatest.PurposeImage, media.StateRejected, month + time.Second, true},
		{mediatest.PurposeImagePrivate, media.StateRejected, month - time.Second, false},
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = e.seedAt(t, r.purpose, r.state, at.Add(-r.age), time.Time{})
		s := e.mem.Pub
		if r.purpose == mediatest.PurposeImagePrivate {
			s = e.mem.Priv
		}
		put(t, s, mediatest.VariantKey(ids[i], "thumb"))
		put(t, s, mediatest.VariantKey(ids[i], "full"))
	}
	e.clean(t)
	for i, r := range rows {
		files := append(keys(t, e.mem.Pub, ids[i]), keys(t, e.mem.Priv, ids[i])...)
		if r.gone && (e.exists(t, ids[i]) || len(files) != 0) {
			t.Errorf("%s %s возраст %s: строка %v, файлы %v — ждали удаления", r.purpose, r.state, r.age, e.exists(t, ids[i]), files)
		}
		if !r.gone && (!e.exists(t, ids[i]) || len(files) != 2) {
			t.Errorf("%s %s возраст %s: строка %v, файлы %v — моложе срока, не трогать", r.purpose, r.state, r.age, e.exists(t, ids[i]), files)
		}
	}
}

// uuidAt — UUIDv7 с моментом создания tm (первые 48 бит — миллисекунды Unix).
func uuidAt(tm time.Time) uuid.UUID {
	u := id.New()
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(tm.UnixMilli()))
	copy(u[:6], ms[2:])
	return u
}

// Страховка жизненного цикла (§3.9): входящее без строки старше суток (по времени UUIDv7 ключа, строго) удаляется;
// моложе суток или со строкой — нет; чужой ключ (не UUIDv7) чистка не трогает.
func TestCleanupInboxOrphans(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	// ключи посева (id.New по настоящим часам) старше суток; миллисекунды — точность времени UUIDv7
	e.clk.Set(time.Now().Add(72 * time.Hour).Truncate(time.Millisecond))
	now := e.clk.Now()
	old := uuidAt(now.Add(-24*time.Hour - time.Millisecond))
	edge := uuidAt(now.Add(-24 * time.Hour))
	young := uuidAt(now.Add(-23 * time.Hour))
	withRow := e.seedAt(t, mediatest.PurposeImage, media.StateReady, now, time.Time{})
	for _, k := range []string{old.String(), edge.String(), young.String(), withRow.String(), "not-a-uuid"} {
		put(t, e.mem.In, k)
	}
	e.clean(t)
	if inExists(t, e.mem.In, old) {
		t.Fatal("входящее без строки старше суток осталось")
	}
	for _, k := range []uuid.UUID{edge, young, withRow} {
		if !inExists(t, e.mem.In, k) {
			t.Fatalf("удалено входящее %s (граница, моложе суток или со строкой)", k)
		}
	}
	if _, err := e.mem.In.Head(context.Background(), "not-a-uuid"); err != nil {
		t.Fatalf("чужой ключ: %v", err)
	}
}

// Режим none: строки чистятся, хранилище и очередь media не трогаются (зависшие не перезапускаются — задача
// отменилась бы).
func TestCleanupNone(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	d := e.deps
	d.Blob = blob.Opened{}
	w, err := jobs.NewCleanupWorker(d)
	if err != nil {
		t.Fatal(err)
	}
	ended := e.seedAt(t, mediatest.PurposeImage, media.StateDetached, at.Add(-48*time.Hour), time.Time{})
	stale := e.seedAt(t, mediatest.PurposeImage, media.StateUploaded, at.Add(-2*time.Hour), time.Time{})
	if err := w.Work(context.Background(), cleanupJob()); err != nil {
		t.Fatal(err)
	}
	if e.exists(t, ended) {
		t.Fatal("none: конечная строка не удалена")
	}
	if n := processJobs(t, e, stale); n != 0 {
		t.Fatalf("none: поставлено media.process: %d", n)
	}
	if n := e.in.total() + e.pub.total() + e.priv.total(); n != 0 {
		t.Fatalf("none: вызовов хранилища %d", n)
	}
}

// Аргументы и сроки: очередь maintenance, 5 попыток, одна задача на 10 минут; v ≠ 1 — ошибка без отмены.
func TestCleanupArgs(t *testing.T) {
	t.Parallel()
	o := jobs.CleanupArgs{}.InsertOpts()
	if o.Queue != "maintenance" || o.MaxAttempts != 5 || o.UniqueOpts.ByPeriod != 10*time.Minute {
		t.Fatalf("опции: %+v", o)
	}
	if (jobs.CleanupArgs{}).Kind() != "media.cleanup" || (&jobs.CleanupWorker{}).Timeout(nil) != 10*time.Minute {
		t.Fatal("вид или срок")
	}
	e := newEnv(t, envOpt{})
	j := cleanupJob()
	j.Args.V = 2
	if err := e.cw.Work(context.Background(), j); err == nil || errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("v=2: %v", err)
	}
	p := jobs.Periodic()
	if len(p) != 1 || len(jobs.Periodic()) != 1 || p[0] == jobs.Periodic()[0] {
		t.Fatal("Periodic — одна задача, новая на вызов")
	}
}
