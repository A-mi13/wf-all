package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	geojobs "wf/backend/internal/geo/jobs"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

// mailEnv — почта воркера обязательна; в тестах — адрес, на котором никто не слушает: письма не
// отправляются, пока модули не поставят задачу.
var mailEnv = []string{"WORKER_MAIL_SMTP_ADDR=127.0.0.1:1", "WORKER_MAIL_FROM=WF <noreply@wf.local>", "WORKER_MAIL_SMTP_TLS=none"}

func TestRunFailsWithoutConfig(t *testing.T) {
	err := run(context.Background(), nil, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "WORKER_DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
}

// Пачка 0 зациклила бы Drain, нулевой опрос уронил бы NewTicker паникой: воркер отказывается
// стартовать до подключения к базе и называет ключ окружения.
func TestRunRejectsInvalidRelayConfig(t *testing.T) {
	// Адрес недостижим: если проверка не сработает, ошибка будет о подключении, не о ключе.
	base := "WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1"
	for _, tc := range []struct{ kv, key string }{
		{"WORKER_RELAY_BATCH=0", "WORKER_RELAY_BATCH"},
		{"WORKER_RELAY_BATCH=-5", "WORKER_RELAY_BATCH"},
		{"WORKER_RELAY_POLL=0s", "WORKER_RELAY_POLL"},
		{"WORKER_RELAY_POLL=-1s", "WORKER_RELAY_POLL"},
	} {
		t.Run(tc.kv, func(t *testing.T) {
			err := run(context.Background(), nil, append(mailEnv, base, tc.kv), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("err = %v, ждали ошибку с %s", err, tc.key)
			}
		})
	}
}

// Воркер поднимает relay: событие, лежавшее в outbox, становится опубликованным; по отмене
// ctx воркер останавливается без ошибки.
func TestRunPublishesPendingEventsAndStops(t *testing.T) {
	url := dbtest.NewURL(t)
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		_, err := events.Publish(ctx, events.Event{
			Type: "teams.member_joined", SchemaVersion: 1, AggregateType: "team",
			AggregateID: id.New(), AggregateVersion: 1, Payload: map[string]string{},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // run остановится и при падении теста до cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, append(mailEnv, "WORKER_DATABASE_URL="+url), io.Discard, io.Discard) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var left int
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay воркера не опубликовал событие")
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка: %v", err)
	}
}

func TestReplayCommandValidatesArguments(t *testing.T) {
	env := append(mailEnv, "WORKER_DATABASE_URL="+dbtest.NewURL(t))
	cases := map[string][]string{
		"неизвестная команда":   {"events", "rewind"},
		"нет --since":           {"events", "replay", "--type", "teams.member_joined", "--subscriber", "notify.roster"},
		"битое --since":         {"events", "replay", "--type", "teams.member_joined", "--subscriber", "notify.roster", "--since", "вчера"},
		"неизвестный подписчик": {"events", "replay", "--type", "teams.member_joined", "--subscriber", "notify.roster", "--since", "2026-10-01T00:00:00Z"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if err := run(context.Background(), args, env, io.Discard, io.Discard); err == nil {
				t.Fatal("ждали ошибку")
			}
		})
	}
}

// Переигровка писем не шлёт: WORKER_MAIL_* ей не нужны — команда доходит до events.Replay.
func TestReplayDoesNotRequireMail(t *testing.T) {
	env := []string{"WORKER_DATABASE_URL=" + dbtest.NewURL(t)}
	args := []string{"events", "replay", "--type", "teams.member_joined", "--subscriber", "notify.roster", "--since", "2026-10-01T00:00:00Z"}
	if err := run(context.Background(), args, env, io.Discard, io.Discard); !errors.Is(err, events.ErrUnknownSubscription) {
		t.Fatalf("err = %v — ждали отказ Replay (подписчика нет), а не требование почты", err)
	}
}

// Воркеру почта обязательна: без WORKER_MAIL_* — отказ старта до подключения к базе.
func TestRunRequiresMail(t *testing.T) {
	env := []string{"WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1"}
	if err := run(context.Background(), nil, env, io.Discard, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "WORKER_MAIL_SMTP_ADDR") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRejectsInvalidMailConfig(t *testing.T) {
	env := []string{"WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1",
		"WORKER_MAIL_SMTP_ADDR=nohost", "WORKER_MAIL_FROM=WF <noreply@wf.local>"}
	err := run(context.Background(), nil, env, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "mail") {
		t.Fatalf("err = %v — битый адрес SMTP должен остановить старт до подключения к базе", err)
	}
}

// Источник GeoNames проверяется на старте, до подключения к базе: ошибка называет ключ.
func TestRunRejectsInvalidGeoNamesConfig(t *testing.T) {
	base := "WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1"
	for _, tc := range []struct{ kv, key string }{
		{"WORKER_GEONAMES_BASE_URL=http://download.geonames.org", "WORKER_GEONAMES_BASE_URL"},
		{"WORKER_GEONAMES_BASE_URL=download.geonames.org", "WORKER_GEONAMES_BASE_URL"},
		{"WORKER_GEONAMES_MAX_COMPRESSED=0", "WORKER_GEONAMES_MAX_COMPRESSED"},
		{"WORKER_GEONAMES_MAX_UNCOMPRESSED=-1", "WORKER_GEONAMES_MAX_UNCOMPRESSED"},
	} {
		t.Run(tc.kv, func(t *testing.T) {
			err := run(context.Background(), []string{"geo", "import", "--country", "RU"}, []string{base, tc.kv}, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("err = %v, ждали ошибку с %s", err, tc.key)
			}
		})
	}
}

func TestGeoImportCommandValidatesArguments(t *testing.T) {
	env := []string{"WORKER_DATABASE_URL=" + dbtest.NewURL(t)}
	if err := run(context.Background(), []string{"geo", "import"}, env, io.Discard, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "--country") {
		t.Fatalf("без --country: %v", err)
	}
	if err := run(context.Background(), []string{"geo", "import", "--cuntry", "RU"}, env, io.Discard, io.Discard); err == nil {
		t.Fatal("неизвестный флаг принят")
	}
	if err := run(context.Background(), []string{"geo", "export"}, env, io.Discard, io.Discard); err == nil {
		t.Fatal("неизвестная команда принята")
	}
	// страны нет в справочнике — отказ до загрузки (адрес источника недостижим: дошли бы до сети —
	// ошибка была бы другой)
	env = append(env, "WORKER_GEONAMES_BASE_URL=https://127.0.0.1:1")
	if err := run(context.Background(), []string{"geo", "import", "--country", "ZZ"}, env, io.Discard, io.Discard); !errors.Is(err, geojobs.ErrCountryNotFound) {
		t.Fatalf("ZZ: %v", err)
	}
}

// geonamesTLS — выгрузка RU из выдержек testdata пакета source на TLS-сервере.
func geonamesTLS(t *testing.T) *httptest.Server {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "..", "internal", "geo", "internal", "source", "testdata", name)) //nolint:gosec // выдержки GeoNames репо
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	zipOf := func(body []byte) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("RU.txt")
		if err == nil {
			_, err = w.Write(body)
		}
		if err == nil {
			err = zw.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	files := map[string][]byte{
		"/export/dump/admin1CodesASCII.txt":  read("admin1CodesASCII.txt"),
		"/export/dump/RU.zip":                zipOf(read("RU.txt")),
		"/export/dump/alternatenames/RU.zip": zipOf(read("alternateNamesV2-RU.txt")),
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Итог команды: счётчики и перечень slug по каждой категории сверки; значения разные и ненулевые —
// перепутанное или потерянное поле не пройдёт.
func TestPrintGeoSummary(t *testing.T) {
	var out bytes.Buffer
	printGeoSummary(&out, "RU", geojobs.Summary{
		ImportID:       uuid.MustParse("c41bb0df-449d-46fe-bacd-e9dd35dd3395"),
		PlacesUpserted: 3, PlacesRemoved: 5, PlacesMissing: 7, PlacesSkipped: 2, NamesUpserted: 12,
		Linked: []string{"a", "a2"}, Ambiguous: []string{"b"}, NotFound: []string{"c"}, Conflict: []string{"e"},
	})
	got := out.String()
	for _, want := range []string{
		"импорт RU завершён, журнал geonames_imports c41bb0df-449d-46fe-bacd-e9dd35dd3395\n",
		"мест: 3, удалено: 5, пропало из источника (держит город): 7, названий: 12\n",
		"пропущено (таймзона): 2\n",
		"сверка городов: привязано 2, неоднозначно 1, не найдено 1, конфликт 1\n",
		"  неоднозначно: b\n", "  не найдено: c\n", "  конфликт: e\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет строки %q в выводе:\n%s", want, got)
		}
	}
}

// Команда оператора импортирует синхронно в своём процессе: задачу River не ставит, почта не
// нужна, журнал — без сотрудника и без задачи, аудит — от operator.
func TestGeoImportCommandRunsSynchronously(t *testing.T) {
	url := dbtest.NewURL(t)
	srv := geonamesTLS(t)
	geoHTTPClient = srv.Client()
	t.Cleanup(func() { geoHTTPClient = nil })
	var out bytes.Buffer
	env := []string{"WORKER_DATABASE_URL=" + url, "WORKER_GEONAMES_BASE_URL=" + srv.URL}
	if err := run(context.Background(), []string{"geo", "import", "--country", "ru"}, env, &out, io.Discard); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "мест: 3") || !strings.Contains(out.String(), "пропущено (таймзона): 0") ||
		!strings.Contains(out.String(), "конфликт 0") {
		t.Fatalf("вывод:\n%s", out.String())
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var places, jobs int
	var status, role string
	var startedBy *uuid.UUID
	var riverJob *int64
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM geonames_places),
		(SELECT count(*) FROM river_job WHERE kind = 'geo.import'),
		(SELECT status FROM geonames_imports),
		(SELECT started_by FROM geonames_imports),
		(SELECT river_job_id FROM geonames_imports),
		(SELECT actor_role FROM audit_log WHERE action = 'geo.import')`).
		Scan(&places, &jobs, &status, &startedBy, &riverJob, &role); err != nil {
		t.Fatal(err)
	}
	if places != 3 || jobs != 0 || status != "succeeded" || startedBy != nil || riverJob != nil || role != "operator" {
		t.Fatalf("мест %d, задач %d, журнал %s %v %v, аудит %q", places, jobs, status, startedBy, riverJob, role)
	}
}
