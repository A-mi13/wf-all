package media_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/media/internal/store/mediadb"
	"wf/backend/internal/platform/testkit/dbtest"
)

var storeAt = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

// seedObject — объект в состоянии state (поля по CHECK §4) от игрока user; у pending_upload — билет со сроком expires.
func seedObject(t *testing.T, pool *pgxpool.Pool, state string, user uuid.UUID, expires time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	var owner any = uuid.Must(uuid.NewV7())
	ownerType, uploaded, ended, reject, ready := any("e2e_owner"), any(storeAt), any(nil), any(nil), any(nil)
	switch state {
	case "pending_upload":
		owner, ownerType, uploaded = nil, nil, nil
	case "ready":
		ready = storeAt
	case "rejected":
		ended, reject = storeAt, "corrupt"
	case "detached", "taken_down":
		ended = storeAt
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_objects (id, purpose, visibility, state, owner_type, owner_id,
		uploaded_by_user, declared_type, declared_bytes, created_at, uploaded_at, ready_at, ended_at, reject_code)
		VALUES ($1, 'e2e_image', 'public', $2, $3, $4, $5, 'image/png', 10, $6, $7, $8, $9, $10)`,
		id, state, ownerType, owner, user, storeAt, uploaded, ready, ended, reject); err != nil {
		t.Fatal(err)
	}
	if state == "pending_upload" {
		if _, err := pool.Exec(ctx, `INSERT INTO media_uploads (object_id, intended_owner_type, intended_owner_id, method,
			max_bytes, expires_at) VALUES ($1, 'e2e_owner', gen_random_uuid(), 'POST', 10, $2)`, id, expires); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// I-6 (спека §3.10): удаление аккаунта под ролью worker обнуляет uploaded_by_user у объектов игрока в любом
// состоянии — CHECK «не больше одного загрузившего» и права роли пропускают; чужие объекты не трогаются.
func TestWorkerClearsUploaderPassesCheck(t *testing.T) {
	t.Parallel()
	p := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	user, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, s := range []string{"pending_upload", "uploaded", "processing", "ready", "rejected", "detached", "taken_down"} {
		seedObject(t, p.Owner, s, user, storeAt)
	}
	keep := seedObject(t, p.Owner, "ready", other, storeAt)
	n, err := mediadb.New(p.As).ClearUploadedByUser(ctx, user)
	if err != nil || n != 7 {
		t.Fatalf("ClearUploadedByUser: %d, %v", n, err)
	}
	var left int
	if err := p.Owner.QueryRow(ctx, `SELECT count(*) FROM media_objects WHERE uploaded_by_user = $1`, user).Scan(&left); err != nil || left != 0 {
		t.Fatalf("осталось с загрузившим: %d, %v", left, err)
	}
	var by uuid.UUID
	if err := p.Owner.QueryRow(ctx, `SELECT uploaded_by_user FROM media_objects WHERE id = $1`, keep).Scan(&by); err != nil || by != other {
		t.Fatalf("чужой объект: %v, %v", by, err)
	}
}

// Граница окна строгая (§3.9): билет со сроком ровно before не берётся, раньше — берётся; живые и чужие состояния —
// нет; лимит соблюдается.
func TestExpiredPendingStrictBoundary(t *testing.T) {
	t.Parallel()
	p := dbtest.NewPoolsAs(t, "worker")
	user := uuid.Must(uuid.NewV7())
	before := storeAt.Add(-time.Hour)
	early := seedObject(t, p.Owner, "pending_upload", user, before.Add(-time.Second))
	earlier := seedObject(t, p.Owner, "pending_upload", user, before.Add(-time.Minute))
	seedObject(t, p.Owner, "pending_upload", user, before)
	seedObject(t, p.Owner, "pending_upload", user, before.Add(time.Second))
	seedObject(t, p.Owner, "uploaded", user, time.Time{})
	tx, err := p.As.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	got, err := mediadb.New(tx).ExpiredPending(context.Background(), mediadb.ExpiredPendingParams{Before: before, Lim: 10})
	if err != nil {
		t.Fatal(err)
	}
	if want := []uuid.UUID{earlier, early}; !slices.Equal(got, want) {
		t.Fatalf("ExpiredPending = %v, ждали %v", got, want)
	}
	if got, err := mediadb.New(tx).ExpiredPending(context.Background(), mediadb.ExpiredPendingParams{Before: before, Lim: 1}); err != nil || len(got) != 1 {
		t.Fatalf("лимит 1: %v, %v", got, err)
	}
}

// DeleteObjects удаляет только pending_upload и конечные (билет — каскадом); живые не трогает даже по своему id.
func TestDeleteObjectsKeepsLive(t *testing.T) {
	t.Parallel()
	p := dbtest.NewPoolsAs(t, "worker")
	user := uuid.Must(uuid.NewV7())
	ids := map[string]uuid.UUID{}
	for _, s := range []string{"pending_upload", "uploaded", "processing", "ready", "rejected", "detached", "taken_down"} {
		ids[s] = seedObject(t, p.Owner, s, user, storeAt)
	}
	all := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		all = append(all, id)
	}
	got, err := mediadb.New(p.As).DeleteObjects(context.Background(), all)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(got, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	want := []uuid.UUID{ids["pending_upload"], ids["rejected"], ids["detached"], ids["taken_down"]}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if !slices.Equal(got, want) {
		t.Fatalf("удалены %v, ждали %v", got, want)
	}
	var left, uploads int
	if err := p.Owner.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM media_objects),
		(SELECT count(*) FROM media_uploads)`).Scan(&left, &uploads); err != nil || left != 3 || uploads != 0 {
		t.Fatalf("осталось объектов %d, билетов %d (%v)", left, uploads, err)
	}
}

// ReadObject под api: варианты — JSON-массив по имени (пустой — []), без полей загрузившего.
func TestReadObjectVariants(t *testing.T) {
	t.Parallel()
	p := dbtest.NewPoolsAs(t, "api")
	id := seedObject(t, p.Owner, "ready", uuid.Must(uuid.NewV7()), storeAt)
	q := mediadb.New(p.As)
	r, err := q.ReadObject(context.Background(), id)
	if err != nil || string(r.Variants) != "[]" || r.State != "ready" {
		t.Fatalf("без вариантов: %+v, %v", r, err)
	}
	if _, err := p.Owner.Exec(context.Background(), `INSERT INTO media_variants (object_id, name, blob_key, content_type,
		width, height, bytes) VALUES ($1::uuid, 'full', $1::text || '/full.webp', 'image/webp', 1920, 1080, 500)`, id); err != nil {
		t.Fatal(err)
	}
	r, err = q.ReadObject(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var vs []struct {
		Name    string `json:"name"`
		BlobKey string `json:"blob_key"`
		Width   int    `json:"width"`
	}
	if err := json.Unmarshal(r.Variants, &vs); err != nil || len(vs) != 1 || vs[0].BlobKey != id.String()+"/full.webp" || vs[0].Width != 1920 {
		t.Fatalf("варианты: %s, %v", r.Variants, err)
	}
}
