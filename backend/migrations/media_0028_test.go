package migrations_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"wf/backend/internal/platform/testkit/dbtest"
)

// mediaVersion — 0028_media.sql (план media, Task 5): media_objects, media_uploads, media_variants и представление
// media_read_objects (спека media §5).
const mediaVersion = 28

// mediaRow — строка media_objects для проверок CHECK; nil — NULL.
type mediaRow struct {
	visibility, state                   string
	ownerType, ownerID, byUser, byStaff any
	declaredBytes                       int64
	width, height, rejectCode           any
	uploadedAt, readyAt, endedAt        any
}

var (
	mediaOwner = "01930000-0000-7000-8000-0000000000a1"
	mediaUser  = "01930000-0000-7000-8000-0000000000a2"
	mediaStaff = "01930000-0000-7000-8000-0000000000a3"
	mediaAt    = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
)

// validMedia — допустимая строка состояния state (спека media §4): владелец у всех, кроме pending_upload; ended_at —
// у конечных; reject_code — у rejected; ready_at — у ready.
func validMedia(state string) mediaRow {
	r := mediaRow{visibility: "public", state: state, ownerType: "e2e_owner", ownerID: mediaOwner, byUser: mediaUser,
		declaredBytes: 1024, uploadedAt: mediaAt}
	switch state {
	case "pending_upload":
		r.ownerType, r.ownerID, r.uploadedAt = nil, nil, nil
	case "ready":
		r.width, r.height, r.readyAt = 640, 480, mediaAt
	case "rejected":
		r.rejectCode, r.endedAt = "corrupt", mediaAt
	case "detached", "taken_down":
		r.endedAt = mediaAt
	}
	return r
}

func (r mediaRow) with(edit func(r *mediaRow)) mediaRow {
	edit(&r)
	return r
}

const insertMedia = `INSERT INTO media_objects (id, purpose, visibility, state, owner_type, owner_id, uploaded_by_user,
	uploaded_by_staff, declared_type, declared_bytes, width, height, reject_code, created_at, uploaded_at, ready_at, ended_at)
	VALUES (gen_random_uuid(), 'e2e_image', $1, $2, $3, $4::uuid, $5::uuid, $6::uuid, 'image/jpeg', $7, $8, $9, $10, now(),
	$11, $12, $13)`

func (r mediaRow) args() []any {
	return []any{r.visibility, r.state, r.ownerType, r.ownerID, r.byUser, r.byStaff, r.declaredBytes, r.width, r.height,
		r.rejectCode, r.uploadedAt, r.readyAt, r.endedAt}
}

var mediaStates = []string{"pending_upload", "uploaded", "processing", "ready", "rejected", "detached", "taken_down"}

// Спека media §4, §5.2 (I-6): по строке на каждое правило CHECK — нарушение даёт 23514 с именем ограничения, а
// допустимые пары «состояние — поля» вставляются. Каждое нарушение задевает ровно одно правило: PostgreSQL проверяет
// CHECK по алфавиту имён, лишнее нарушение подменило бы ожидаемое имя.
func TestMediaSchemaChecks(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	violations := []struct {
		name, constraint string
		row              mediaRow
	}{
		{"visibility", "media_objects_visibility_check", validMedia("uploaded").with(func(r *mediaRow) { r.visibility = "secret" })},
		{"state", "media_objects_state_check", validMedia("uploaded").with(func(r *mediaRow) { r.state = "deleted" })},
		{"declared_bytes zero", "media_objects_declared_bytes_check", validMedia("uploaded").with(func(r *mediaRow) { r.declaredBytes = 0 })},
		{"one_uploader", "media_objects_one_uploader", validMedia("uploaded").with(func(r *mediaRow) { r.byStaff = mediaStaff })},
		{"one_uploader pending", "media_objects_one_uploader", validMedia("pending_upload").with(func(r *mediaRow) { r.byStaff = mediaStaff })},
		{"owner on pending", "media_objects_owner", validMedia("pending_upload").with(func(r *mediaRow) {
			r.ownerType, r.ownerID = "e2e_owner", mediaOwner
		})},
		{"owner_type only on pending", "media_objects_owner", validMedia("pending_upload").with(func(r *mediaRow) { r.ownerType = "e2e_owner" })},
		{"owner_id only on pending", "media_objects_owner", validMedia("pending_upload").with(func(r *mediaRow) { r.ownerID = mediaOwner })},
		{"owner missing uploaded", "media_objects_owner", validMedia("uploaded").with(func(r *mediaRow) { r.ownerType, r.ownerID = nil, nil })},
		{"owner_id missing ready", "media_objects_owner", validMedia("ready").with(func(r *mediaRow) { r.ownerID = nil })},
		{"owner_type missing detached", "media_objects_owner", validMedia("detached").with(func(r *mediaRow) { r.ownerType = nil })},
		{"ended missing detached", "media_objects_ended", validMedia("detached").with(func(r *mediaRow) { r.endedAt = nil })},
		{"ended missing taken_down", "media_objects_ended", validMedia("taken_down").with(func(r *mediaRow) { r.endedAt = nil })},
		{"ended missing rejected", "media_objects_ended", validMedia("rejected").with(func(r *mediaRow) { r.endedAt = nil })},
		{"ended on ready", "media_objects_ended", validMedia("ready").with(func(r *mediaRow) { r.endedAt = mediaAt })},
		{"ended on pending", "media_objects_ended", validMedia("pending_upload").with(func(r *mediaRow) { r.endedAt = mediaAt })},
		{"reject_code missing", "media_objects_reject_code", validMedia("rejected").with(func(r *mediaRow) { r.rejectCode = nil })},
		{"reject_code empty", "media_objects_reject_code", validMedia("rejected").with(func(r *mediaRow) { r.rejectCode = "" })},
		{"reject_code on processing", "media_objects_reject_code", validMedia("processing").with(func(r *mediaRow) { r.rejectCode = "corrupt" })},
		{"reject_code on detached", "media_objects_reject_code", validMedia("detached").with(func(r *mediaRow) { r.rejectCode = "corrupt" })},
		{"reject_code empty on uploaded", "media_objects_reject_code", validMedia("uploaded").with(func(r *mediaRow) { r.rejectCode = "" })},
		{"ready_at missing", "media_objects_ready_at", validMedia("ready").with(func(r *mediaRow) { r.readyAt = nil })},
		{"ready_at on uploaded", "media_objects_ready_at", validMedia("uploaded").with(func(r *mediaRow) { r.readyAt = mediaAt })},
		{"ready_at on processing", "media_objects_ready_at", validMedia("processing").with(func(r *mediaRow) { r.readyAt = mediaAt })},
		{"ready_at on rejected", "media_objects_ready_at", validMedia("rejected").with(func(r *mediaRow) { r.readyAt = mediaAt })},
		{"ready_at on pending", "media_objects_ready_at", validMedia("pending_upload").with(func(r *mediaRow) { r.readyAt = mediaAt })},
		{"width zero", "media_objects_size", validMedia("ready").with(func(r *mediaRow) { r.width = 0 })},
		{"height negative", "media_objects_size", validMedia("ready").with(func(r *mediaRow) { r.height = -1 })},
	}
	for _, c := range violations {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wantArgsError(t, db, "23514", c.constraint, insertMedia, c.row.args()...)
		})
	}

	allowed := map[string]mediaRow{}
	for _, s := range mediaStates {
		allowed[s] = validMedia(s)
	}
	allowed["detached keeps ready_at"] = validMedia("detached").with(func(r *mediaRow) { r.readyAt, r.width, r.height = mediaAt, 10, 10 })
	allowed["taken_down keeps ready_at"] = validMedia("taken_down").with(func(r *mediaRow) { r.readyAt = mediaAt })
	allowed["staff uploader"] = validMedia("uploaded").with(func(r *mediaRow) { r.byUser, r.byStaff = nil, mediaStaff })
	allowed["system, no uploader"] = validMedia("ready").with(func(r *mediaRow) { r.byUser = nil })
	allowed["private"] = validMedia("processing").with(func(r *mediaRow) { r.visibility = "private" })
	for name, row := range allowed {
		t.Run("allowed/"+name, func(t *testing.T) {
			t.Parallel()
			execArgs(t, db, insertMedia, row.args()...)
		})
	}
}

// Билет и варианты: метод только PUT | POST; строки билета и вариантов удаляются вместе с объектом (каскад).
func TestMediaUploadsAndVariants(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	id := queryString(t, db, `INSERT INTO media_objects (id, purpose, visibility, state, uploaded_by_user, declared_type,
		declared_bytes, created_at) VALUES (gen_random_uuid(), 'e2e_image', 'public', 'pending_upload', $1, 'image/png', 10,
		now()) RETURNING id::text`, mediaUser)
	const upload = `INSERT INTO media_uploads (object_id, intended_owner_type, intended_owner_id, method, max_bytes,
		expires_at) VALUES ($1, 'e2e_owner', $2, $3, 10, now())`
	wantArgsError(t, db, "23514", "media_uploads_method_check", upload, id, mediaOwner, "GET")
	execArgs(t, db, upload, id, mediaOwner, "POST")
	wantArgsError(t, db, "23505", "media_uploads_pkey", upload, id, mediaOwner, "PUT")
	const variant = `INSERT INTO media_variants (object_id, name, blob_key, content_type, width, height, bytes)
		VALUES ($1::uuid, $2, $1::text || '/' || $2 || '.webp', 'image/webp', 320, 240, 100)`
	execArgs(t, db, variant, id, "thumb")
	execArgs(t, db, variant, id, "full")
	wantArgsError(t, db, "23505", "media_variants_pkey", variant, id, "thumb")
	wantArgsError(t, db, "23503", "media_variants_object_id_fkey", variant, mediaOwner, "thumb")
	execArgs(t, db, `DELETE FROM media_objects WHERE id = $1`, id)
	if got := queryString(t, db, `SELECT (SELECT count(*) FROM media_uploads) || ' ' || (SELECT count(*) FROM media_variants)`); got != "0 0" {
		t.Fatalf("после удаления объекта осталось билетов и вариантов: %s", got)
	}
}

// Спека media §5.4: колонки представления — ровно перечисленные, без «кто загрузил» и заявленного.
func TestReadViewHidesUploader(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	got := queryString(t, db, `SELECT string_agg(column_name || ' ' || data_type, ',' ORDER BY ordinal_position)
		FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'media_read_objects'`)
	const want = "id uuid,purpose text,visibility text,state text,owner_type text,owner_id uuid,reject_code text," +
		"width integer,height integer,variants jsonb"
	if got != want {
		t.Fatalf("колонки media_read_objects:\n%s\nждали\n%s", got, want)
	}
}

// Варианты в представлении — массив по имени; у объекта без вариантов — пустой массив, не NULL.
func TestReadViewVariants(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	row := validMedia("ready")
	id := queryString(t, db, insertMedia+" RETURNING id::text", row.args()...)
	if got := queryString(t, db, `SELECT variants::text FROM media_read_objects WHERE id = $1`, id); got != "[]" {
		t.Fatalf("без вариантов: %s", got)
	}
	for _, v := range []string{"thumb", "full"} {
		execArgs(t, db, `INSERT INTO media_variants (object_id, name, blob_key, content_type, width, height, bytes)
			VALUES ($1::uuid, $2, $1::text || '/' || $2 || '.webp', 'image/webp', 320, 240, 100)`, id, v)
	}
	got := queryString(t, db, `SELECT variants::text FROM media_read_objects WHERE id = $1`, id)
	want := fmt.Sprintf(`[{"name": "full", "bytes": 100, "width": 320, "height": 240, "blob_key": "%[1]s/full.webp", `+
		`"content_type": "image/webp"}, {"name": "thumb", "bytes": 100, "width": 320, "height": 240, `+
		`"blob_key": "%[1]s/thumb.webp", "content_type": "image/webp"}]`, id)
	if got != want {
		t.Fatalf("варианты:\n%s\nждали\n%s", got, want)
	}
}

// Накат 0028 на схеме 0027 и откат: откат возвращает схему 0027 в точности, повторный накат — ту же схему.
func TestMedia0028UpDown(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	if err := migrateTo(t, db, identityCaptaincyVersion); err != nil {
		t.Fatalf("down до 0027: %v", err)
	}
	before := snapshotOf(t, db, identitySnapshotSQL)
	if err := migrateTo(t, db, mediaVersion); err != nil {
		t.Fatalf("up 0028: %v", err)
	}
	up := snapshotOf(t, db, identitySnapshotSQL)
	for _, want := range []string{
		"rel r media_objects", "rel r media_uploads", "rel r media_variants", "rel v media_read_objects",
		"idx CREATE INDEX media_objects_cleanup_idx ON public.media_objects USING btree (state, ended_at)",
		"idx CREATE INDEX media_objects_owner_idx ON public.media_objects USING btree (owner_type, owner_id)",
		"idx CREATE INDEX media_objects_uploaded_by_user_idx ON public.media_objects USING btree (uploaded_by_user) WHERE (uploaded_by_user IS NOT NULL)",
		"idx CREATE INDEX media_objects_stale_idx ON public.media_objects USING btree (state, uploaded_at) WHERE (state = ANY (ARRAY['uploaded'::text, 'processing'::text]))",
		"idx CREATE INDEX media_uploads_expires_idx ON public.media_uploads USING btree (expires_at)",
	} {
		if !slices.Contains(up, want) {
			t.Errorf("после 0028 нет %q", want)
		}
	}
	if err := migrateTo(t, db, identityCaptaincyVersion); err != nil {
		t.Fatalf("down 0028: %v", err)
	}
	after := snapshotOf(t, db, identitySnapshotSQL)
	if extra, lost := missingFrom(after, before), missingFrom(before, after); len(extra) != 0 || len(lost) != 0 {
		t.Fatalf("откат 0028 не вернул схему 0027:\nлишнее: %q\nпропало: %q", extra, lost)
	}
	if err := migrateTo(t, db, mediaVersion); err != nil {
		t.Fatalf("повторный up 0028: %v", err)
	}
	if again := snapshotOf(t, db, identitySnapshotSQL); !slices.Equal(again, up) {
		t.Fatalf("повторный накат дал другую схему:\nлишнее: %q\nпропало: %q", missingFrom(again, up), missingFrom(up, again))
	}
}
