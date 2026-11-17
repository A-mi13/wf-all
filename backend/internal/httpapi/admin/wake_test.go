package admin_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
)

// healthStub — подставной api: считает запросы, помнит пути, отвечает status; hold (не nil) держит ответ до закрытия.
type healthStub struct {
	srv    *httptest.Server
	n      atomic.Int64
	status atomic.Int32
	mu     sync.Mutex
	paths  []string
	hold   chan struct{}
}

func newHealthStub(t *testing.T, hold chan struct{}) *healthStub {
	t.Helper()
	s := &healthStub{hold: hold}
	s.status.Store(http.StatusOK)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.mu.Unlock()
		s.n.Add(1)
		if s.hold != nil {
			<-s.hold
		}
		w.WriteHeader(int(s.status.Load()))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// waker — Waker на заглушку с промежутком gap; уборка теста ждёт, пока запрос в полёте завершится (горутина Poke не
// переживает тест), — до закрытия сервера.
func waker(t *testing.T, s *healthStub, base string, gap time.Duration, log *slog.Logger) *admin.Waker {
	t.Helper()
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	w := admin.NewWaker(base, s.srv.Client(), log)
	admin.SetWakeGap(w, gap)
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for !admin.WakeIdle(w) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	})
	return w
}

// waitRequests — счётчик запросов дорос до want за 3 с.
func waitRequests(t *testing.T, s *healthStub, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for s.n.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("запросов побудки %d, ждали %d", s.n.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// noRequests — за паузу ≥ 1 с запросов не прибавилось.
func noRequests(t *testing.T, s *healthStub, before int64) {
	t.Helper()
	time.Sleep(time.Second)
	if n := s.n.Load(); n != before {
		t.Fatalf("запросов побудки %d, ждали %d", n, before)
	}
}

// wakeChain — маршруты контракта admin → звено побудки → хендлер с заданным статусом; pokesAtCall — счётчик Poke в
// момент вызова хендлера (побудка строго после next).
func wakeChain(t *testing.T, w *admin.Waker, status int, pokesAtCall *atomic.Int64) http.Handler {
	t.Helper()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(spec)
	if err != nil {
		t.Fatal(err)
	}
	return routes(admin.Wake(w)(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		if pokesAtCall != nil {
			pokesAtCall.Store(admin.WakePokes(w))
		}
		rw.WriteHeader(status)
	})))
}

func serveWake(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, nil))
	return rec
}

// мутации маршрутов контракта admin по методам
var wakeMutations = []struct{ method, path string }{
	{http.MethodPost, "/v1/staff/auth/login"},
	{http.MethodPut, "/v1/staff/accounts/" + uuid.NewString() + "/role"},
	{http.MethodPatch, "/v1/staff/me/locale"},
	{http.MethodDelete, "/v1/staff/me/sessions/" + uuid.NewString()},
}

// Успешная мутация маршрута контракта (200, 204) будит api — по запросу на каждую, строго после хендлера.
func TestWakeOnSuccessfulMutation(t *testing.T) {
	t.Parallel()
	s := newHealthStub(t, nil)
	w := waker(t, s, s.srv.URL, 0, nil)
	var want int64
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		var atCall atomic.Int64
		h := wakeChain(t, w, status, &atCall)
		for _, m := range wakeMutations {
			before := admin.WakePokes(w)
			if rec := serveWake(h, m.method, m.path); rec.Code != status {
				t.Fatalf("%s %s: ответ %d, ждали %d", m.method, m.path, rec.Code, status)
			}
			if atCall.Load() != before {
				t.Fatalf("%s %s: побудка до хендлера (Poke %d → %d в момент вызова)", m.method, m.path, before, atCall.Load())
			}
			want++
			waitRequests(t, s, want)
		}
	}
}

// Неуспешная мутация (4xx, 5xx) не будит.
func TestWakeNotOnFailure(t *testing.T) {
	t.Parallel()
	s := newHealthStub(t, nil)
	w := waker(t, s, s.srv.URL, 0, nil)
	for _, status := range []int{400, 404, 409, 422, 500} {
		h := wakeChain(t, w, status, nil)
		for _, m := range wakeMutations {
			serveWake(h, m.method, m.path)
		}
	}
	noRequests(t, s, 0)
	if p := admin.WakePokes(w); p != 0 {
		t.Fatalf("Poke вызван %d раз", p)
	}
}

// Чтения не будят.
func TestWakeNotOnRead(t *testing.T) {
	t.Parallel()
	s := newHealthStub(t, nil)
	w := waker(t, s, s.srv.URL, 0, nil)
	h := wakeChain(t, w, http.StatusOK, nil)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		serveWake(h, method, "/v1/staff/me")
	}
	noRequests(t, s, 0)
}

// Вне контракта (статика, /docs) — без побудки, даже на 200.
func TestWakeNotOutsideContract(t *testing.T) {
	t.Parallel()
	s := newHealthStub(t, nil)
	w := waker(t, s, s.srv.URL, 0, nil)
	h := wakeChain(t, w, http.StatusOK, nil)
	for _, path := range []string{"/docs", "/index.html", "/v1/nope"} {
		serveWake(h, http.MethodPost, path)
	}
	noRequests(t, s, 0)
}

// Пустой адрес — побудки нет: NewWaker = nil, звено пропускает как есть, Poke на nil безопасен.
func TestWakeNilWaker(t *testing.T) {
	t.Parallel()
	if w := admin.NewWaker("", nil, slog.New(slog.DiscardHandler)); w != nil {
		t.Fatal("NewWaker(\"\") не nil")
	}
	var w *admin.Waker
	w.Poke()
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { rw.WriteHeader(http.StatusTeapot) })
	rec := httptest.NewRecorder()
	admin.Wake(nil)(next).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/staff/auth/login", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("ответ %d", rec.Code)
	}
}

// Троттлинг: не чаще gap и не больше одного запроса в полёте.
func TestWakeThrottled(t *testing.T) {
	t.Parallel()
	// 3 с — запас на ожидание первого запроса под нагрузкой полного прогона (проверка ниже — до истечения gap)
	const gap = 3 * time.Second
	s := newHealthStub(t, nil)
	w := waker(t, s, s.srv.URL, gap, nil)
	start := time.Now()
	for range 10 {
		w.Poke()
	}
	waitRequests(t, s, 1)
	// промежуток: первый запрос завершён (в полёте нет — CAS busy не отсекает), Poke внутри gap запроса не шлёт
	for !admin.WakeIdle(w) {
		time.Sleep(5 * time.Millisecond)
	}
	w.Poke()
	if !admin.WakeIdle(w) {
		t.Fatal("Poke внутри промежутка начал запрос")
	}
	time.Sleep(100 * time.Millisecond)
	if n := s.n.Load(); n != 1 {
		t.Fatalf("Poke внутри промежутка %s — %d запросов, ждали 1", gap, n)
	}
	if el := time.Since(start); el >= gap {
		t.Fatalf("проверка промежутка заняла %s ≥ %s — машина слишком медленная, проверка не доказательна", el, gap)
	}
	// промежуток вышел — следующий Poke будит
	time.Sleep(time.Until(start.Add(gap + 50*time.Millisecond)))
	w.Poke()
	waitRequests(t, s, 2)

	// запрос в полёте — новые Poke второй не начинают (и при gap 0)
	hold := make(chan struct{})
	held := newHealthStub(t, hold)
	// отпустить ответы до закрытия сервера и при падении теста: иначе Close ждал бы их вечно
	release := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	hw := waker(t, held, held.srv.URL, 0, nil)
	hw.Poke()
	waitRequests(t, held, 1)
	for range 5 {
		hw.Poke()
	}
	time.Sleep(200 * time.Millisecond)
	if n := held.n.Load(); n != 1 {
		t.Fatalf("при запросе в полёте — %d запросов, ждали 1", n)
	}
	release()
}

// Завершающий «/» адреса обрезается: путь запроса — ровно /healthz.
func TestWakeTrimsSlash(t *testing.T) {
	t.Parallel()
	s := newHealthStub(t, nil)
	w := waker(t, s, s.srv.URL+"/", 0, nil)
	w.Poke()
	waitRequests(t, s, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.paths) != 1 || s.paths[0] != "/healthz" {
		t.Fatalf("пути запросов %q", s.paths)
	}
}

// syncLog — журнал Waker, который пишет горутина побудки и читает тест.
type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// waitLog — в журнале появилась строка с want за 3 с.
func waitLog(t *testing.T, l *syncLog, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(l.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("в журнале нет %q: %q", want, l.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Ошибка побудки (500, сервер закрыт) — предупреждение в журнал, наружу не выходит, ответ звена прежний.
func TestWakeErrorsLogged(t *testing.T) {
	t.Parallel()
	s := newHealthStub(t, nil)
	s.status.Store(http.StatusInternalServerError)
	var buf syncLog
	w := waker(t, s, s.srv.URL, 0, slog.New(slog.NewTextHandler(&buf, nil)))
	h := wakeChain(t, w, http.StatusOK, nil)
	if rec := serveWake(h, http.MethodPost, "/v1/staff/auth/login"); rec.Code != http.StatusOK {
		t.Fatalf("ответ %d", rec.Code)
	}
	waitLog(t, &buf, "status=500")
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Fatalf("не предупреждение: %q", buf.String())
	}
	for !admin.WakeIdle(w) {
		time.Sleep(10 * time.Millisecond)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	base := closed.URL
	closed.Close()
	var buf2 syncLog
	cw := admin.NewWaker(base+"/", nil, slog.New(slog.NewTextHandler(&buf2, nil)))
	admin.SetWakeGap(cw, 0)
	cw.Poke()
	waitLog(t, &buf2, "level=WARN")
	if strings.Contains(buf2.String(), "/healthz") {
		t.Fatalf("в журнале путь запроса (только хост): %q", buf2.String())
	}
	for !admin.WakeIdle(cw) {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestValidateWakeURL(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"", "https://wf-api.onrender.com", "https://wf-api.onrender.com/"} {
		if err := admin.ValidateWakeURL(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://wf-api.onrender.com", "https://", "https://h/path", "https://h?x=1",
		"https://u:p@h", "ftp://h", "https://h#f", "wf-api.onrender.com"} {
		if err := admin.ValidateWakeURL(bad); err == nil || !strings.Contains(err.Error(), "ADMIN_WAKE_API_URL") {
			t.Errorf("%q: err = %v, ждали отказ с ADMIN_WAKE_API_URL", bad, err)
		}
	}
	// значение в ошибке — без userinfo, пути, запроса и фрагмента: текст уходит в stderr (журнал деплоя Render)
	for _, bad := range []string{"https://u:secret@h", "https://secret@h", "http://u:secret@h", "https://h/secret",
		"https://h?k=secret", "https://h#secret", "https://u:secret@h/%zz", "https://u:sec ret@h"} {
		err := admin.ValidateWakeURL(bad)
		if err == nil || !strings.Contains(err.Error(), "ADMIN_WAKE_API_URL") {
			t.Errorf("%q: err = %v, ждали отказ с ADMIN_WAKE_API_URL", bad, err)
			continue
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "sec ret") {
			t.Errorf("%q: секрет в тексте ошибки: %v", bad, err)
		}
	}
}

// stableRequests — счётчик запросов, не менявшийся ≥ 1 с: подготовка окружения сама шлёт 2xx-мутации — в полном
// конвейере мерить только разность после стабилизации.
func stableRequests(t *testing.T, s *healthStub) int64 {
	t.Helper()
	for {
		before := s.n.Load()
		time.Sleep(time.Second)
		if s.n.Load() == before {
			return before
		}
	}
}

// Звено побудки — в конвейере admin-api: успешная мутация будит, чтение — нет.
func TestWakeInPipeline(t *testing.T) {
	t.Parallel()
	s := newHealthStub(t, nil)
	w := waker(t, s, s.srv.URL, 0, nil)
	e := newStaffEnv(t, func(o *admin.Options) { o.Wake = w })
	a := e.owners(1)[0]
	c1, c2 := e.city("vremensk"), e.city("pokrovsk")
	mod := e.moderator("mod@wf.example", c1, c2)
	base := stableRequests(t, s)
	rec := e.send(http.MethodPut, "/v1/staff/accounts/"+mod.String()+"/role", a.token, uuid.NewString(), roleBody("moderator", c1))
	wantStatus(t, rec, http.StatusOK)
	waitRequests(t, s, base+1)
	wantStatus(t, e.send(http.MethodGet, "/v1/staff/me", a.token, "", ""), http.StatusOK)
	noRequests(t, s, base+1)
}

// Побудка снаружи идемпотентности: повтор с тем же ключом — сохранённый ответ, сценарий исполнен один раз, побудка —
// на каждый 2xx (повтор будит ещё раз — безвредно).
func TestWakeKeepsIdempotency(t *testing.T) {
	t.Parallel()
	s := newHealthStub(t, nil)
	w := waker(t, s, s.srv.URL, 0, nil)
	e := newStaffEnv(t, func(o *admin.Options) { o.Wake = w })
	a := e.owners(1)[0]
	c1, c2 := e.city("vremensk"), e.city("pokrovsk")
	mod := e.moderator("mod@wf.example", c1, c2)
	base := stableRequests(t, s)
	key, path := uuid.NewString(), "/v1/staff/accounts/"+mod.String()+"/role"
	first := e.send(http.MethodPut, path, a.token, key, roleBody("moderator", c1))
	wantStatus(t, first, http.StatusOK)
	waitRequests(t, s, base+1)
	for !admin.WakeIdle(w) {
		time.Sleep(10 * time.Millisecond)
	}
	again := e.send(http.MethodPut, path, a.token, key, roleBody("moderator", c1))
	wantStatus(t, again, http.StatusOK)
	if !replayed(again) || !sameJSON(t, first, again) {
		t.Fatalf("повтор не из сохранённого ответа: %v %s", again.Header(), again.Body.String())
	}
	waitRequests(t, s, base+2)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.role_changed' AND object_id = $1`, mod); n != 1 {
		t.Fatalf("сценарий исполнен %d раз", n)
	}
}
