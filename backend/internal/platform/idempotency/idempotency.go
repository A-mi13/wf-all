// Package idempotency — повтор мутирующего запроса не исполняет действие дважды (спека бэкенда
// §6.4). Ключ (user_id, key) вставляется хуком в бизнес-транзакцию ручки: дубль не закоммитится
// и при обрыве связи. Ответ сохраняется после коммита; повтор с тем же запросом получает его.
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
	Header             = "Idempotency-Key"
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
}

// RequestHash — SHA-256 от метода, фактического пути с query и тела.
func RequestHash(method, requestURI string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method + "\n" + requestURI + "\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Middleware — идемпотентность мутирующего запроса с Idempotency-Key от вошедшего пользователя.
// Без ключа, без пользователя и для GET/HEAD/OPTIONS запрос проходит как есть.
func Middleware(dbtx idempotencydb.DBTX, cfg Config, log *slog.Logger) func(http.Handler) http.Handler {
	if cfg.LockTimeout <= 0 {
		cfg.LockTimeout = defaultLockTimeout
	}
	// lock_timeout в миллисекундах, а "0ms" значит «ждать без конца»: меньше 1 мс — это 1 мс
	cfg.LockTimeout = max(cfg.LockTimeout, time.Millisecond)
	q := idempotencydb.New(dbtx)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(Header)
			p, ok := auth.From(r.Context())
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
			c := &claim{userID: p.UserID, key: key, endpoint: r.URL.Path,
				hash: RequestHash(r.Method, r.URL.RequestURI(), body), lockTimeout: cfg.LockTimeout}

			row, err := q.Get(r.Context(), idempotencydb.GetParams{UserID: c.userID, Key: key})
			switch {
			case err == nil:
				replay(w, r, row, c.hash)
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

func mutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func replay(w http.ResponseWriter, r *http.Request, row idempotencydb.GetRow, hash string) {
	switch {
	case row.RequestHash != hash:
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity, httpx.CodeIdempotencyKeyReused, "")
	case row.ResponseStatus == nil:
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
	userID      uuid.UUID
	key         string
	endpoint    string
	hash        string
	lockTimeout time.Duration

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
	q := idempotencydb.New(tx)
	prev, err := q.CurrentLockTimeout(ctx)
	if err != nil {
		return nil, err
	}
	if err := q.SetLockTimeout(ctx, fmt.Sprintf("%dms", c.lockTimeout.Milliseconds())); err != nil {
		return nil, err
	}
	_, err = q.Claim(ctx, idempotencydb.ClaimParams{UserID: c.userID, Key: c.key, Endpoint: c.endpoint, RequestHash: c.hash})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeLockTimeout {
		c.conflict = httpx.NewError(http.StatusConflict, httpx.CodeIdempotencyInProgress)
		return nil, c.conflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// ключ занял запрос, закоммиченный между проверкой и вставкой
		c.conflict = httpx.NewError(http.StatusConflict, httpx.CodeIdempotencyInProgress)
		if row, err := q.Get(ctx, idempotencydb.GetParams{UserID: c.userID, Key: c.key}); err == nil && row.RequestHash != c.hash {
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
	return c.settle, q.SetLockTimeout(ctx, prev)
}

// settle — исход транзакции, в которую вставлен ключ.
func (c *claim) settle(committed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = false
	c.committed = committed
}

func (c *claim) save(ctx context.Context, r *http.Request, q *idempotencydb.Queries, buf *buffer, log *slog.Logger) {
	body := buf.body.Bytes()
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
	headers, _ := json.Marshal(h)
	status := int32(buf.code()) //nolint:gosec // HTTP-статус
	if _, err := q.SaveResponse(ctx, idempotencydb.SaveResponseParams{Status: &status, Headers: headers, Body: body,
		UserID: c.userID, Key: c.key, RequestHash: c.hash}); err != nil {
		log.ErrorContext(ctx, "idempotency: ответ не сохранён — повтор получит replay_unavailable",
			"method", r.Method, "path", r.URL.Path, "err", err)
	}
}
