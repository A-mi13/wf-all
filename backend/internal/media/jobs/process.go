package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"wf/backend/internal/media"
	"wf/backend/internal/media/internal/domain"
	"wf/backend/internal/media/internal/imaging"
	"wf/backend/internal/media/internal/lottie"
	"wf/backend/internal/media/internal/store/mediadb"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
)

const (
	// maxCrashes — на каком по счёту захвате, заставшем processing (падение исполнителя), объект отклоняется (m2-3).
	maxCrashes = 3
	// releaseTimeout — срок транзакции отката или отклонения после ошибки (на неотменяемом ctx, m3-2).
	releaseTimeout = 5 * time.Second

	cacheControlPublic  = "public, max-age=86400"
	cacheControlPrivate = "private, no-store"
	contentTypeLottie   = "application/json"
	// embeddedQuality — WebP вшитой в Lottie картинки (без масштаба, прозрачность сохраняется).
	embeddedQuality = 85
)

var (
	errVersion = errors.New("media/jobs: незнакомая версия аргументов задачи")
	// errInputMissing — входящего нет, а объект ещё в обработке (жизненный цикл бакета убрал файл): обычная ошибка,
	// после последней попытки — processing_failed.
	errInputMissing = errors.New("media/jobs: входящего файла нет, объект в обработке")
	// errNotProcessing — при публикации объект uploaded (откатил другой исполнитель): повтор с захвата.
	errNotProcessing  = errors.New("media/jobs: при публикации объект не в обработке")
	errUnknownPurpose = errors.New("media/jobs: цели объекта нет в реестре")
	errTooLarge       = errors.New("media/jobs: входящее больше предела цели")
)

// ProcessWorker — исполнитель media.process (спека §3.3): захват, чтение входящего, конвейер, запись вариантов вне
// транзакций, публикация под замком строки; отказ — rejected с кодом.
type ProcessWorker struct {
	river.WorkerDefaults[ProcessArgs]
	d   WorkerDeps
	ins insertClient
}

// Timeout — срок задачи (строка media.process в queue.WorkerKinds).
func (*ProcessWorker) Timeout(*river.Job[ProcessArgs]) time.Duration { return 2 * time.Minute }

// output — вариант к записи и публикации (строка media_variants).
type output struct {
	name, key, contentType string
	width, height          int
	data                   []byte
}

// Work — шаги 1–8 §3.3. Ошибка шагов 2–7 — откат processing → uploaded (неотменяемый ctx, 5 с) и ошибка River;
// на последней попытке — rejected(processing_failed).
func (w *ProcessWorker) Work(ctx context.Context, job *river.Job[ProcessArgs]) error {
	if job.Args.V != 1 {
		return fmt.Errorf("%w: media.process v=%d", errVersion, job.Args.V)
	}
	if !w.d.Blob.Available {
		return river.JobCancel(media.ErrUnavailable)
	}
	o, ok, err := w.claim(ctx, job.Args.ObjectID)
	if err != nil || !ok {
		return err
	}
	p, known := w.d.Registry.Get(o.Purpose)
	if !known {
		err = fmt.Errorf("%w: %q", errUnknownPurpose, o.Purpose)
	} else {
		err = w.process(ctx, o, p)
	}
	if err == nil {
		return nil
	}
	return w.fail(ctx, job, o, err)
}

// claim — шаг 1: короткая транзакция под замком строки (ждать). Не uploaded/processing (и нет строки) — ok=false без
// действий; застали processing — падение (process_attempts + 1), третье — rejected(processing_failed).
func (w *ProcessWorker) claim(ctx context.Context, objectID uuid.UUID) (mediadb.MediaObject, bool, error) {
	var out mediadb.MediaObject
	claimed := false
	err := db.InTx(ctx, w.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := mediadb.New(tx)
		o, ok, err := lock(ctx, q, objectID)
		if err != nil || !ok {
			return err
		}
		from := media.State(o.State)
		if from != media.StateUploaded && from != media.StateProcessing {
			return nil
		}
		if err := domain.Transition(from, media.StateProcessing); err != nil {
			return err
		}
		row, err := q.SetProcessing(ctx, objectID)
		if err != nil {
			return fmt.Errorf("media/jobs: захват: %w", err)
		}
		if row.ProcessAttempts >= maxCrashes {
			return w.rejectTx(ctx, tx, q, row, media.RejectProcessingFailed)
		}
		out, claimed = row, true
		return nil
	})
	if err != nil {
		return mediadb.MediaObject{}, false, err
	}
	return out, claimed, nil
}

// lock — строка объекта FOR UPDATE (ждать); нет строки — ok=false.
func lock(ctx context.Context, q *mediadb.Queries, objectID uuid.UUID) (mediadb.MediaObject, bool, error) {
	o, err := q.LockObject(ctx, objectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, false, nil
	}
	if err != nil {
		return o, false, fmt.Errorf("media/jobs: замок объекта: %w", err)
	}
	return o, true, nil
}

// process — шаги 2–7. nil — объект опубликован, отклонён или уже не в обработке; ошибка — обычная (повтор).
func (w *ProcessWorker) process(ctx context.Context, o mediadb.MediaObject, p media.Purpose) error {
	data, err := w.read(ctx, o.ID, p.MaxBytes)
	switch {
	case errors.Is(err, errTooLarge):
		return w.reject(ctx, o.ID, media.RejectTooLarge)
	case errors.Is(err, blob.ErrNotFound):
		return w.inputGone(ctx, o.ID)
	case err != nil:
		return err
	}
	outs, width, height, err := w.render(ctx, o, p, data)
	if err != nil {
		if code, ok := rejectCode(err); ok {
			return w.reject(ctx, o.ID, code)
		}
		return err
	}
	// сеть — вне транзакций: замок строки на время записи не держится (снятие и открепление не ждут хранилища)
	if err := w.write(ctx, o, outs); err != nil {
		return err
	}
	return w.publish(ctx, o, outs, width, height)
}

// read — шаг 2: входящее целиком, но не больше MaxBytes + 1 (перезаливка по живому билету не обходит предел).
func (w *ProcessWorker) read(ctx context.Context, objectID uuid.UUID, maxBytes int64) ([]byte, error) {
	rc, _, err := w.d.Blob.Buckets.In.Get(ctx, objectID.String())
	if err != nil {
		return nil, fmt.Errorf("media/jobs: входящее: %w", err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("media/jobs: чтение входящего: %w", err)
	}
	if int64(len(b)) > maxBytes {
		return nil, errTooLarge
	}
	return b, nil
}

// inputGone — входящего нет: объект сняли или открепили (media.purge удалил файл) либо другой исполнитель уже
// опубликовал — перечитать строку и завершить без изменений; ещё в обработке — обычная ошибка.
func (w *ProcessWorker) inputGone(ctx context.Context, objectID uuid.UUID) error {
	var state media.State
	err := db.InTx(ctx, w.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		o, ok, err := lock(ctx, mediadb.New(tx), objectID)
		if ok {
			state = media.State(o.State)
		}
		return err
	})
	if err != nil {
		return err
	}
	if state == media.StateUploaded || state == media.StateProcessing {
		return errInputMissing
	}
	return nil
}

// render — шаги 3–6: растр — imaging.Process по вариантам цели, Lottie — lottie.Sanitize с вшитыми картинками через
// imaging. Возвращает варианты и размеры исходника (после EXIF-ориентации; у Lottie — w, h анимации).
func (w *ProcessWorker) render(ctx context.Context, o mediadb.MediaObject, p media.Purpose, data []byte) ([]output, int, int, error) {
	if isLottie(p) {
		out, err := lottie.Sanitize(ctx, data, lottie.DefaultLimits, w.embedded(p))
		if err != nil {
			return nil, 0, 0, err
		}
		var size struct{ W, H float64 }
		if err := json.Unmarshal(out, &size); err != nil {
			return nil, 0, 0, fmt.Errorf("media/jobs: размеры анимации: %w", err)
		}
		width, height := int(size.W), int(size.H)
		return []output{{name: p.Variants[0].Name, key: o.ID.String() + "/" + lottieFile, contentType: contentTypeLottie,
			width: width, height: height, data: out}}, width, height, nil
	}
	specs := make([]imaging.Spec, len(p.Variants))
	for i, v := range p.Variants {
		specs[i] = imaging.Spec{Name: v.Name, MaxSide: v.MaxSide, Lossy: v.Lossy, Quality: v.Quality}
	}
	inputs := make([]imaging.Kind, len(p.Inputs))
	for i, in := range p.Inputs {
		inputs[i] = imaging.Kind(in)
	}
	outs, h, err := imaging.Process(ctx, data, imaging.Limits{MaxPixels: p.MaxPixels, Inputs: inputs}, specs, w.check(ctx, p))
	if err != nil {
		return nil, 0, 0, err
	}
	res := make([]output, len(outs))
	for i, x := range outs {
		res[i] = output{name: x.Name, key: o.ID.String() + "/" + x.Name + ".webp", contentType: x.ContentType,
			width: x.Width, height: x.Height, data: x.Data}
	}
	return res, h.Width, h.Height, nil
}

// embedded — перекодирование вшитой в Lottie картинки: тот же конвейер, один вариант full без масштаба, не больше
// lottie.DefaultLimits.MaxImagePixels; отказы imaging дают свои коды.
func (w *ProcessWorker) embedded(p media.Purpose) lottie.ImageFunc {
	lim := imaging.Limits{MaxPixels: lottie.DefaultLimits.MaxImagePixels,
		Inputs: []imaging.Kind{imaging.KindJPEG, imaging.KindPNG, imaging.KindWebP}}
	spec := []imaging.Spec{{Name: "full", Lossy: true, Quality: embeddedQuality}}
	return func(ctx context.Context, b []byte) ([]byte, error) {
		outs, _, err := imaging.Process(ctx, b, lim, spec, w.check(ctx, p))
		if err != nil {
			return nil, err
		}
		return outs[0].Data, nil
	}
}

// check — порт проверки содержимого у целей с ContentCheck (§8), иначе nil.
func (w *ProcessWorker) check(ctx context.Context, p media.Purpose) func(image.Image) (bool, string, error) {
	if !p.ContentCheck {
		return nil
	}
	return func(img image.Image) (bool, string, error) {
		v, err := w.d.Check.Check(ctx, img)
		return v.Allow, v.Category, err
	}
}

// rejectCode — код отклонения по ошибке конвейера; ok=false — ошибка не отказ (повтор).
func rejectCode(err error) (media.RejectCode, bool) {
	switch {
	case errors.Is(err, lottie.ErrUnsafe):
		return media.RejectUnsafeAnimation, true
	case errors.Is(err, imaging.ErrContentRejected):
		return media.RejectContentRejected, true
	case errors.Is(err, imaging.ErrUnsupported):
		return media.RejectUnsupportedType, true
	case errors.Is(err, imaging.ErrTooManyPixels):
		return media.RejectTooManyPixels, true
	case errors.Is(err, imaging.ErrCorrupt), errors.Is(err, lottie.ErrCorrupt):
		return media.RejectCorrupt, true
	}
	return "", false
}

// bucketOf — бакет видимости объекта (по строке, не по цели) и имя для PurgeArgs.
func (w *ProcessWorker) bucketOf(o mediadb.MediaObject) (blob.Store, string) {
	if media.Visibility(o.Visibility) == media.VisibilityPrivate {
		return w.d.Blob.Buckets.Priv, BucketPriv
	}
	return w.d.Blob.Buckets.Pub, BucketPub
}

// write — шаг 6: варианты в бакет видимости с Cache-Control (повтор перезаписывает те же ключи теми же байтами).
func (w *ProcessWorker) write(ctx context.Context, o mediadb.MediaObject, outs []output) error {
	s, _ := w.bucketOf(o)
	cc := cacheControlPublic
	if media.Visibility(o.Visibility) == media.VisibilityPrivate {
		cc = cacheControlPrivate
	}
	for _, x := range outs {
		if err := s.Put(ctx, x.key, bytes.NewReader(x.data), blob.PutOptions{ContentType: x.contentType, CacheControl: cc}); err != nil {
			return fmt.Errorf("media/jobs: запись варианта %s: %w", x.name, err)
		}
	}
	return nil
}

// publish — шаг 7 под замком строки: processing — строки вариантов, ready, событие; ready — ничего (опубликовал
// другой исполнитель); detached | taken_down | rejected или строки нет — media.purge записанных ключей (с публичными
// адресами). После коммита — удаление входящего.
func (w *ProcessWorker) publish(ctx context.Context, o mediadb.MediaObject, outs []output, width, height int) error {
	err := db.InTx(ctx, w.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := mediadb.New(tx)
		cur, ok, err := lock(ctx, q, o.ID)
		if err != nil {
			return err
		}
		state := media.StateDetached // строки нет — как откреплённый: файлы не нужны
		if ok {
			state = media.State(cur.State)
		}
		switch state {
		case media.StateProcessing:
			return w.ready(ctx, q, cur, outs, width, height)
		case media.StateReady:
			return nil
		case media.StateDetached, media.StateTakenDown, media.StateRejected:
			_, bucket := w.bucketOf(o)
			a := PurgeArgs{V: 1, Bucket: bucket}
			for _, x := range outs {
				a.Keys = append(a.Keys, x.key)
				if bucket == BucketPub {
					a.PublicURLs = append(a.PublicURLs, w.d.Blob.PublicBaseURL+x.key)
				}
			}
			return w.insertPurges(ctx, tx, a)
		default:
			return fmt.Errorf("%w: %s", errNotProcessing, state)
		}
	})
	if err != nil {
		return err
	}
	// входящее больше не нужно; не удалилось — уберёт жизненный цикл бакета (сутки), повтор задачи ничего не даст
	if err := w.d.Blob.Buckets.In.Delete(ctx, o.ID.String()); err != nil {
		w.d.Log.WarnContext(ctx, "media: входящее не удалено после публикации", "object_id", o.ID, "err", err)
	}
	return nil
}

// ready — processing → ready: строки вариантов (только здесь, вместе со сменой состояния), размеры исходника, событие.
func (w *ProcessWorker) ready(ctx context.Context, q *mediadb.Queries, o mediadb.MediaObject, outs []output, width, height int) error {
	if err := domain.Transition(media.StateProcessing, media.StateReady); err != nil {
		return err
	}
	for _, x := range outs {
		n := int64(len(x.data))
		if err := q.InsertVariant(ctx, mediadb.InsertVariantParams{ObjectID: o.ID, Name: x.name, BlobKey: x.key,
			ContentType: x.contentType, Width: dim(x.width), Height: dim(x.height), Bytes: &n}); err != nil {
			return fmt.Errorf("media/jobs: вариант %s: %w", x.name, err)
		}
	}
	now := w.d.Clock.Now()
	row, err := q.SetReady(ctx, mediadb.SetReadyParams{Width: dim(width), Height: dim(height), Now: &now, ID: o.ID})
	if err != nil {
		return fmt.Errorf("media/jobs: публикация: %w", err)
	}
	if _, err := events.Publish(ctx, media.ObjectReady(media.ObjectRef{ID: row.ID, Version: row.Version}, row.Purpose,
		ownerOf(row))); err != nil {
		return fmt.Errorf("media/jobs: событие готовности: %w", err)
	}
	return nil
}

// dim — размер для колонки (0 и меньше — NULL); стороны ограничены imaging (≤ 65535) и анимацией.
func dim(v int) *int32 {
	if v <= 0 || v > 1<<30 {
		return nil
	}
	x := int32(v)
	return &x
}

// reject — шаг 8 своей транзакцией.
func (w *ProcessWorker) reject(ctx context.Context, objectID uuid.UUID, code media.RejectCode) error {
	return db.InTx(ctx, w.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := mediadb.New(tx)
		o, ok, err := lock(ctx, q, objectID)
		if err != nil || !ok {
			return err
		}
		return w.rejectTx(ctx, tx, q, o, code)
	})
}

// rejectTx — отклонение под замком строки o: только из uploaded/processing (иначе ничего) — rejected с кодом и
// ended_at, событие media.object_rejected, media.purge входящего и всех ключей вариантов цели (с публичными адресами).
func (w *ProcessWorker) rejectTx(ctx context.Context, tx pgx.Tx, q *mediadb.Queries, o mediadb.MediaObject, code media.RejectCode) error {
	from := media.State(o.State)
	if from != media.StateUploaded && from != media.StateProcessing {
		return nil
	}
	if err := domain.Transition(from, media.StateRejected); err != nil {
		return err
	}
	now := w.d.Clock.Now()
	c := string(code)
	row, err := q.SetState(ctx, mediadb.SetStateParams{ID: o.ID, State: string(media.StateRejected), EndedAt: &now, RejectCode: &c})
	if err != nil {
		return fmt.Errorf("media/jobs: отклонение: %w", err)
	}
	if _, err := events.Publish(ctx, media.ObjectRejected(media.ObjectRef{ID: row.ID, Version: row.Version}, row.Purpose,
		ownerOf(row), code)); err != nil {
		return fmt.Errorf("media/jobs: событие отклонения: %w", err)
	}
	// все ключи цели, а не только записанные этой попыткой: прошлая неудачная (или упавшая) могла оставить варианты
	p, _ := w.d.Registry.Get(o.Purpose)
	_, bucket := w.bucketOf(o)
	vs := PurgeArgs{V: 1, Bucket: bucket, Keys: VariantKeys(o.ID, p)}
	if bucket == BucketPub {
		vs.PublicURLs = PublicURLs(w.d.Blob.PublicBaseURL, vs.Keys)
	}
	return w.insertPurges(ctx, tx, PurgeArgs{V: 1, Bucket: BucketIn, Keys: []string{o.ID.String()}}, vs)
}

// insertPurges — задачи media.purge в транзакции tx; без ключей — не ставятся.
func (w *ProcessWorker) insertPurges(ctx context.Context, tx pgx.Tx, as ...PurgeArgs) error {
	opts := PurgeInsertOpts()
	for _, a := range as {
		if len(a.Keys) == 0 {
			continue
		}
		if _, err := w.ins.InsertTx(ctx, tx, a, &opts); err != nil {
			return fmt.Errorf("media/jobs: задача удаления: %w", err)
		}
	}
	return nil
}

// fail — обычная ошибка шагов 2–7 (m3-2): на неотменяемом ctx со своим сроком — последняя попытка River —
// rejected(processing_failed) с удалением всех ключей цели, иначе processing → uploaded. Ошибка возвращается River.
func (w *ProcessWorker) fail(ctx context.Context, job *river.Job[ProcessArgs], o mediadb.MediaObject, cause error) error {
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if job.Attempt >= job.MaxAttempts {
		rejected, err := w.rejectFailed(bg, o.ID)
		if err != nil {
			return errors.Join(cause, fmt.Errorf("media/jobs: отклонение после последней попытки: %w", err))
		}
		msg := "media: обработка не удалась, объект отклонён"
		if !rejected { // конечный (удаление вариантов поставлено), ready или строки нет — ревью Task 10 m3, R1 m1
			msg = "media: обработка не удалась на последней попытке, объект уже не в обработке — не отклонялся"
		}
		w.d.Log.WarnContext(ctx, msg, "object_id", o.ID, "err", cause)
		return cause
	}
	if err := w.release(bg, o.ID); err != nil {
		return errors.Join(cause, fmt.Errorf("media/jobs: откат в uploaded: %w", err))
	}
	return cause
}

// rejectFailed — последняя попытка River: rejected(processing_failed) под замком строки (rejected = true после
// коммита, только из uploaded/processing); конечное состояние — media.purge всех ключей цели, как у release (m-r1-1
// на последней попытке); ready (опубликовал другой) и нет строки — ничего.
func (w *ProcessWorker) rejectFailed(ctx context.Context, objectID uuid.UUID) (rejected bool, err error) {
	err = db.InTx(ctx, w.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := mediadb.New(tx)
		o, ok, err := lock(ctx, q, objectID)
		if err != nil || !ok {
			return err
		}
		if domain.IsFinal(media.State(o.State)) {
			return w.purgeEnded(ctx, tx, o)
		}
		// rejectTx отклоняет только из uploaded/processing; ready (опубликовал другой исполнитель) — без изменений
		st := media.State(o.State)
		rejected = st == media.StateUploaded || st == media.StateProcessing
		return w.rejectTx(ctx, tx, q, o, media.RejectProcessingFailed)
	})
	return rejected && err == nil, err
}

// purgeEnded — объект стал конечным во время обработки (сняли, открепили, отклонили): media.purge всех ключей цели в
// бакете видимости (+ PublicURLs у pub). purge того сценария мог пройти раньше записи варианта этой попыткой, а до
// публикации (которая удалила бы записанное) она не дошла (ревью Task 9, m-r1-1).
func (w *ProcessWorker) purgeEnded(ctx context.Context, tx pgx.Tx, o mediadb.MediaObject) error {
	p, _ := w.d.Registry.Get(o.Purpose)
	_, bucket := w.bucketOf(o)
	a := PurgeArgs{V: 1, Bucket: bucket, Keys: VariantKeys(o.ID, p)}
	if bucket == BucketPub {
		a.PublicURLs = PublicURLs(w.d.Blob.PublicBaseURL, a.Keys)
	}
	return w.insertPurges(ctx, tx, a)
}

// release — processing → uploaded под замком строки; конечное состояние — purgeEnded; иначе ничего.
func (w *ProcessWorker) release(ctx context.Context, objectID uuid.UUID) error {
	return db.InTx(ctx, w.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := mediadb.New(tx)
		o, ok, err := lock(ctx, q, objectID)
		if err != nil || !ok {
			return err
		}
		if domain.IsFinal(media.State(o.State)) {
			return w.purgeEnded(ctx, tx, o)
		}
		if media.State(o.State) != media.StateProcessing {
			return nil
		}
		if err := domain.Transition(media.StateProcessing, media.StateUploaded); err != nil {
			return err
		}
		if _, err := q.SetState(ctx, mediadb.SetStateParams{ID: objectID, State: string(media.StateUploaded)}); err != nil {
			return fmt.Errorf("media/jobs: откат: %w", err)
		}
		return nil
	})
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func ownerOf(o mediadb.MediaObject) media.OwnerRef {
	return media.OwnerRef{Type: deref(o.OwnerType), ID: deref(o.OwnerID)}
}
