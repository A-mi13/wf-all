package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"wf/backend/internal/platform/httpx"
)

// Побудка api (спека встроенного воркера §8.1): на dev-стенде фон работает в процессе wf-api, который засыпает без
// трафика; письма и события, поставленные admin-api, ждали бы первого запроса к api. После успешной мутации admin-api
// шлёт в фоне GET <ADMIN_WAKE_API_URL>/healthz. Побудка — подсказка, не обязательство: ошибки только в журнал.

const (
	// wakeTimeout — срок запроса побудки (R16): спящий инстанс Render free просыпается до минуты, оборванный раньше
	// запрос может его не разбудить; запрос в фоне, ответ мутации не держит.
	wakeTimeout = 60 * time.Second
	// wakeMinGap — не чаще одного запроса за промежуток.
	wakeMinGap = 5 * time.Second
)

// ValidateWakeURL — ADMIN_WAKE_API_URL до подключения к базе: пусто — побудка выключена; иначе https, хост, без пути
// (кроме «/»), запроса, фрагмента и userinfo.
func ValidateWakeURL(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("admin: ADMIN_WAKE_API_URL %q: %w", s, err)
	}
	var errs []error
	if u.Scheme != "https" {
		errs = append(errs, errors.New("схема — https"))
	}
	if u.Host == "" || u.Hostname() == "" {
		errs = append(errs, errors.New("нет хоста"))
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.Opaque != "" {
		errs = append(errs, errors.New("только схема, хост и порт — без пути, запроса, фрагмента и логина"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("admin: ADMIN_WAKE_API_URL %q: %w", s, errors.Join(errs...))
	}
	return nil
}

// Waker — фоновые запросы побудки api: не чаще gap и не больше одного в полёте.
type Waker struct {
	url     string // base + "/healthz"
	host    string // для журнала — только хост
	client  *http.Client
	log     *slog.Logger
	gap     time.Duration
	timeout time.Duration
	origin  time.Time    // начало отсчёта монотонных отметок
	last    atomic.Int64 // отметка начала последнего запроса, нс от origin; -1 — не было
	busy    atomic.Bool  // запрос в полёте
	pokes   atomic.Int64 // вызовы Poke (тесты: побудка строго после ответа хендлера)
}

// NewWaker — побудка на base (ADMIN_WAKE_API_URL, проверен ValidateWakeURL); пусто — nil (выключено). client nil —
// свой со сроком wakeTimeout.
func NewWaker(base string, client *http.Client, log *slog.Logger) *Waker {
	if base == "" {
		return nil
	}
	base = strings.TrimSuffix(base, "/")
	if client == nil {
		client = &http.Client{Timeout: wakeTimeout}
	}
	host := base
	if u, err := url.Parse(base); err == nil {
		host = u.Host
	}
	w := &Waker{url: base + "/healthz", host: host, client: client, log: log, gap: wakeMinGap, timeout: wakeTimeout,
		origin: time.Now()}
	w.last.Store(-1)
	return w
}

// Poke — разбудить api в фоне; не блокирует, безопасен на nil.
func (w *Waker) Poke() {
	if w == nil {
		return
	}
	w.pokes.Add(1)
	// один в полёте: занят — пропуск (запрос в полёте и так будит)
	if !w.busy.CompareAndSwap(false, true) {
		return
	}
	now := int64(time.Since(w.origin))
	if last := w.last.Load(); last >= 0 && now-last < int64(w.gap) {
		w.busy.Store(false)
		return
	}
	w.last.Store(now)
	go func() {
		defer w.busy.Store(false)
		w.do()
	}()
}

func (w *Waker) do() {
	ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.url, nil)
	if err != nil {
		w.log.Warn("побудка api: запрос", "host", w.host, "err", err)
		return
	}
	resp, err := w.client.Do(req)
	if err != nil {
		// url.Error несёт полный адрес — в журнал только хост и причина
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		w.log.Warn("побудка api не удалась", "host", w.host, "err", err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		w.log.Warn("побудка api: ответ не 2xx", "host", w.host, "status", resp.StatusCode)
	}
}

// wake — звено конвейера снаружи идемпотентности (не db.WithTxHook: хук на ctx один): после ответа хендлера мутация
// маршрута контракта (не GET/HEAD/OPTIONS) со статусом 2xx будит api. Повтор по Idempotency-Key отдаёт сохранённый
// 2xx и будит ещё раз — безвредно. nil — звено пропускает как есть.
func wake(w *Waker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if w == nil {
			return next
		}
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(rw, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				return
			}
			if _, ok := httpx.RouteFrom(r.Context()); !ok {
				return
			}
			// хендлер ничего не записал — net/http ответит 200
			if st := ww.Status(); st == 0 || (st >= 200 && st <= 299) {
				w.Poke()
			}
		})
	}
}
