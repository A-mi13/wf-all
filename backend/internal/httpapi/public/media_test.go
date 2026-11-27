package public_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/token"
)

// fakeDevMedia — dev-пути хранилища: читает тело целиком и отвечает 204 (чтение сверх лимита — 413).
type fakeDevMedia struct{ limit int64 }

func (f fakeDevMedia) UploadLimit() int64 { return f.limit }

func (fakeDevMedia) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Спека media §6.3, R16: dev-пути (Options.Media) монтируются только заданными — /_blob/in со своим лимитом тела
// (больше общего 1 МиБ), /_media/*; без них — 404. Тестовые ручки (Options.E2E) — так же под /_e2e/.
func TestDevMediaMounted(t *testing.T) {
	t.Parallel()
	o := testOptions(t)
	serve := func(o public.Options, method, path string, size int) int {
		t.Helper()
		h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(make([]byte, size))))
		return rec.Code
	}
	const mib = 1 << 20
	for _, c := range []struct {
		method, path string
		size         int
	}{{http.MethodPost, "/_blob/in", 0}, {http.MethodGet, "/_media/pub/x/thumb.webp", 0}, {http.MethodPost, "/_e2e/media/uploads", 0}} {
		if got := serve(o, c.method, c.path, c.size); got != http.StatusNotFound {
			t.Errorf("без Media/E2E %s %s: %d, ждали 404", c.method, c.path, got)
		}
	}
	o.Media = fakeDevMedia{limit: 16 * mib}
	o.E2E = fakeDevMedia{}
	for _, c := range []struct {
		method, path string
		size, want   int
	}{
		{http.MethodPost, "/_blob/in", 6 * mib, http.StatusNoContent},
		{http.MethodPost, "/_blob/in", 16*mib + 1, http.StatusRequestEntityTooLarge},
		{http.MethodGet, "/_media/pub/x/thumb.webp", 0, http.StatusNoContent},
		{http.MethodGet, "/_media/priv/x/full.webp", 0, http.StatusNoContent},
		{http.MethodPost, "/_e2e/media/uploads", mib + 1, http.StatusRequestEntityTooLarge}, // общий лимит
		{http.MethodPost, "/_e2e/media/uploads", 10, http.StatusNoContent},
		{http.MethodPost, "/_blob/inx", 10, http.StatusNotFound},
	} {
		if got := serve(o, c.method, c.path, c.size); got != c.want {
			t.Errorf("%s %s %d байт: %d, ждали %d", c.method, c.path, c.size, got, c.want)
		}
	}
}

// Ревью Task 11 m1: dev-пути fs — вне звеньев сессии и идемпотентности, как S3 (он Authorization и Idempotency-Key не
// читает): клиент, вешающий их на все запросы к адресу api, получает ответ httpdev — и с живым Bearer и ключом (раньше
// 500 «успех без транзакции»), и с просроченным или битым токеном (раньше 401). Ответ — от fakeDevMedia (204).
func TestDevMediaOutsidePipeline(t *testing.T) {
	t.Parallel()
	o := testOptions(t)
	seed, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	k, err := token.ParseKeys([]string{seed})
	if err != nil {
		t.Fatal(err)
	}
	iss := token.NewIssuer(k, clock.System)
	uid, sid := uuid.New(), uuid.New()
	o.Tokens = iss
	o.Sessions = auth.SessionLoaderFunc(func(_ context.Context, s uuid.UUID) (*auth.Principal, error) {
		if s != sid {
			return nil, auth.ErrSessionInvalid
		}
		return &auth.Principal{UserID: uid, SessionID: sid, Status: "active"}, nil
	})
	o.Media = fakeDevMedia{limit: 1 << 20}
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	live, _, err := iss.Access(uid, sid)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, method, path, authz, key string
	}{
		{"живой Bearer и ключ", http.MethodPost, "/_blob/in", "Bearer " + live, uuid.NewString()},
		{"битый Bearer", http.MethodPost, "/_blob/in", "Bearer garbage", ""},
		{"битый Bearer и ключ", http.MethodPost, "/_blob/in", "Bearer garbage", uuid.NewString()},
		{"чтение, битый Bearer", http.MethodGet, "/_media/pub/x/thumb.webp", "Bearer garbage", ""},
	} {
		r := httptest.NewRequestWithContext(context.Background(), c.method, c.path, bytes.NewReader([]byte("x")))
		r.Header.Set("Authorization", c.authz)
		if c.key != "" {
			r.Header.Set("Idempotency-Key", c.key)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusNoContent {
			t.Errorf("%s: %d %s — ждали 204 от dev-пути", c.name, rec.Code, rec.Body.String())
		}
	}
	// прочие пути — по-прежнему за конвейером: битый Bearer на /_e2e/ (без маршрута контракта) — 401
	o.E2E = fakeDevMedia{}
	h, err = public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/_e2e/media/uploads", nil)
	r.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/_e2e/ с битым Bearer: %d — конвейер должен остаться", rec.Code)
	}
}
