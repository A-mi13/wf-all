// Package idempotency — повтор мутирующего запроса не исполняет действие дважды (спека бэкенда
// §6.4). Ключ (user_id, key) вставляется хуком в бизнес-транзакцию ручки: дубль не закоммитится
// и при обрыве связи. user_id — id субъекта запроса (auth.SubjectFrom): игрока public API или
// сотрудника admin-api (решение D19). Таблица — Config.Table: у публичного API idempotency_keys, у
// admin-api своя staff_idempotency_keys с правами только роли admin (ответы сотрудников — с ПД).
// Ответ сохраняется после коммита; повтор с тем же запросом получает его. Операция с
// x-idempotency: no-store (ответ с секретами) хранит только хэш запроса и статус.
// Одна закоммиченная транзакция на идемпотентный запрос: db.InTx после коммита или внутри
// открытой — ошибка; после отката (повтор при 40001) ключ вставляется заново.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/idempotency/idempotencydb"
)

const (
	Header = "Idempotency-Key"
	// Extension — расширение операции контракта: NoStore — ответ с секретами (секрет TOTP,
	// резервные коды) не сохраняется: в строке ключа только хэш запроса и статус, повтор того же
	// запроса — 409 idempotency.replay_unavailable (решение D14).
	Extension          = "x-idempotency"
	NoStore            = "no-store"
	headerReplayed     = "Idempotency-Replayed"
	defaultLockTimeout = 3 * time.Second
	codeLockTimeout    = "55P03" // lock_not_available
)

// сохраняемые заголовки ответа: повтор отдаёт тот же тип, адрес созданного и версию
var savedHeaders = []string{"Content-Type", "Location", "ETag"}

var errSecondTx = errors.New("idempotency: вторая транзакция в идемпотентном запросе — нужна одна (спека §6.4)")

type Config struct {
	// LockTimeout — сколько дубль ждёт первый запрос на уникальном индексе, прежде чем
	// получить 409 idempotency.in_progress.
	LockTimeout time.Duration
	// Table — таблица ключей: нулевое значение Players (idempotency_keys) — публичный API; admin-api —
	// Staff (staff_idempotency_keys, права только роли admin). Неизвестная — паника при сборке.
	Table Table
}

// RequestHash — SHA-256 от метода, фактического пути с query и тела.
func RequestHash(method, requestURI string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method + "\n" + requestURI + "\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Middleware — идемпотентность мутирующего запроса с Idempotency-Key от вошедшего субъекта
// (игрок — auth.With, сотрудник — auth.WithStaff). Без ключа, без субъекта и для GET/HEAD/OPTIONS
// запрос проходит как есть.
func Middleware(dbtx idempotencydb.DBTX, cfg Config, log *slog.Logger) func(http.Handler) http.Handler {
	if cfg.LockTimeout <= 0 {
		cfg.LockTimeout = defaultLockTimeout
	}
	// lock_timeout в миллисекундах, а "0ms" значит «ждать без конца»: меньше 1 мс — это 1 мс
	cfg.LockTimeout = max(cfg.LockTimeout, time.Millisecond)
	q := storeOf(cfg.Table, dbtx)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(Header)
			s, ok := auth.SubjectFrom(r.Context())
			if key == "" || !ok || !mutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeRequestInvalid, "")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			c := &claim{subjectID: s.ID, key: key, endpoint: r.URL.Path,
				hash: RequestHash(r.Method, r.URL.RequestURI(), body), lockTimeout: cfg.LockTimeout,
				noStore: noStore(r), table: cfg.Table}

			row, err := q.get(r.Context(), idempotencydb.GetParams{UserID: c.subjectID, Key: key})
			switch {
			case err == nil:
				replay(w, r, row, c.hash, c.noStore)
				return
			case !errors.Is(err, pgx.ErrNoRows):
				httpx.WriteError(log, w, r, err)
				return
			}

			buf := newBuffer()
			next.ServeHTTP(buf, r.WithContext(db.WithTxHook(r.Context(), c.hook)))

			committed, conflict := c.state()
			switch {
			case conflict != nil:
				httpx.WriteError(log, w, r, conflict)
			case !committed && buf.code() < http.StatusBadRequest:
				// успех без закоммиченного ключа: транзакции не было или ручка проглотила её откат —
				// повтор исполнил бы действие ещё раз
				log.ErrorContext(r.Context(), "idempotency: успешный ответ без транзакции — ручка обязана выполняться в db.InTx и вернуть успех только после коммита",
					"method", r.Method, "path", r.URL.Path)
				httpx.WriteProblem(w, r, http.StatusInternalServerError, httpx.CodeInternal, "")
			default:
				// ответ сохраняется только в ключ, закоммиченный этим запросом: откаченный ключ
				// мог уже занять другой запрос с тем же хешем
				if committed && buf.code() < http.StatusInternalServerError {
					c.save(context.WithoutCancel(r.Context()), r, q, buf, log)
				}
				buf.flushTo(w)
			}
		})
	}
}

// noStore — операция маршрута помечена x-idempotency: no-store. Маршрута нет (запрос не из
// контракта, тесты без Routes) — ответ сохраняется как обычно.
func noStore(r *http.Request) bool {
	rt, ok := httpx.RouteFrom(r.Context())
	return ok && rt.Extension(Extension) == NoStore
}

func mutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func replay(w http.ResponseWriter, r *http.Request, row idempotencydb.GetRow, hash string, noStore bool) {
	switch {
	case row.RequestHash != hash:
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity, httpx.CodeIdempotencyKeyReused, "")
	case row.ResponseStatus == nil || noStore:
		httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeIdempotencyReplayUnavailable, "")
	default:
		var h map[string]string
		_ = json.Unmarshal(row.ResponseHeaders, &h)
		for k, v := range h {
			w.Header().Set(k, v)
		}
		w.Header().Set(headerReplayed, "true")
		w.WriteHeader(int(*row.ResponseStatus))
		_, _ = w.Write(row.ResponseBody)
	}
}

// claim — ключ этого запроса: вставляется хуком транзакции. Состояние следует за исходом
// транзакции (after хука): откат снимает ключ — следующая db.InTx того же запроса (повтор после
// 40001) вставляет его заново; коммит закрепляет — ответ сохраняется только тогда.
type claim struct {
	subjectID   uuid.UUID // user_id строки: игрок или сотрудник
	key         string
	endpoint    string
	hash        string
	lockTimeout time.Duration
	noStore     bool  // ответ не сохраняется: только статус (x-idempotency: no-store)
	table       Table // таблица ключей (Config.Table)

	mu        sync.Mutex
	pending   bool // ключ вставлен в открытую транзакцию, исход ещё неизвестен
	committed bool // ключ закоммичен вместе с действием
	conflict  *httpx.Error
}

func (c *claim) state() (bool, *httpx.Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.committed, c.conflict
}

// hook — вставка ключа первой операцией бизнес-транзакции. При конфликте транзакция дубля
// откатывается сразу: Claim с 0 строк (ON CONFLICT … WHERE) всё равно держит блокировку
// конфликтной строки до конца транзакции, поэтому дальше в ней ничего не делается.
// Транзакция при открытой (pending) или закоммиченной — вторая в запросе: errSecondTx.
func (c *claim) hook(ctx context.Context, tx pgx.Tx) (func(committed bool), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending || c.committed || c.conflict != nil {
		return nil, errSecondTx
	}
	q := storeOf(c.table, tx)
	prev, err := q.currentLockTimeout(ctx)
	if err != nil {
		return nil, err
	}
	if err := q.setLockTimeout(ctx, fmt.Sprintf("%dms", c.lockTimeout.Milliseconds())); err != nil {
		return nil, err
	}
	err = q.claim(ctx, idempotencydb.ClaimParams{UserID: c.subjectID, Key: c.key, Endpoint: c.endpoint, RequestHash: c.hash})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeLockTimeout {
		c.conflict = httpx.NewError(http.StatusConflict, httpx.CodeIdempotencyInProgress)
		return nil, c.conflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// ключ занял запрос, закоммиченный между проверкой и вставкой
		c.conflict = httpx.NewError(http.StatusConflict, httpx.CodeIdempotencyInProgress)
		if row, err := q.get(ctx, idempotencydb.GetParams{UserID: c.subjectID, Key: c.key}); err == nil && row.RequestHash != c.hash {
			c.conflict = httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeIdempotencyKeyReused)
		}
		return nil, c.conflict
	}
	if err != nil {
		return nil, err
	}
	c.pending = true
	// lock_timeout — только для вставки ключа, не для бизнес-запросов транзакции; after
	// возвращается и при ошибке: InTx откатит транзакцию и снимет pending
	return c.settle, q.setLockTimeout(ctx, prev)
}

// settle — исход транзакции, в которую вставлен ключ.
func (c *claim) settle(committed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = false
	c.committed = committed
}

func (c *claim) save(ctx context.Context, r *http.Request, q store, buf *buffer, log *slog.Logger) {
	var body, headers []byte
	if !c.noStore {
		body = buf.body.Bytes()
		if len(body) == 0 {
			body = nil
		} else if !json.Valid(body) {
			log.WarnContext(ctx, "idempotency: ответ не JSON — повтор получит replay_unavailable",
				"method", r.Method, "path", r.URL.Path)
			return
		}
		h := map[string]string{}
		for _, k := range savedHeaders {
			if v := buf.header.Get(k); v != "" {
				h[k] = v
			}
		}
		headers, _ = json.Marshal(h)
	}
	status := int32(buf.code()) //nolint:gosec // HTTP-статус
	if _, err := q.saveResponse(ctx, idempotencydb.SaveResponseParams{Status: &status, Headers: headers, Body: body,
		UserID: c.subjectID, Key: c.key, RequestHash: c.hash}); err != nil {
		log.ErrorContext(ctx, "idempotency: ответ не сохранён — повтор получит replay_unavailable",
			"method", r.Method, "path", r.URL.Path, "err", err)
	}
}
