package workerhost

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/queue"
)

// validateGeoNames — до подключения к базе: источник только https, лимиты положительны.
func validateGeoNames(g config.GeoNames) error {
	var errs []error
	if u, err := url.Parse(g.BaseURL); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fmt.Errorf("WORKER_GEONAMES_BASE_URL = %q: нужен https://<хост>", g.BaseURL))
	}
	if g.MaxCompressed < 1 {
		errs = append(errs, fmt.Errorf("WORKER_GEONAMES_MAX_COMPRESSED = %d: нужно больше нуля", g.MaxCompressed))
	}
	if g.MaxUncompressed < 1 {
		errs = append(errs, fmt.Errorf("WORKER_GEONAMES_MAX_UNCOMPRESSED = %d: нужно больше нуля", g.MaxUncompressed))
	}
	return errors.Join(errs...)
}

// geoNamesTimeout — предел одного запроса к источнику, как у клиента по умолчанию пакета source (15 мин): копию клиента
// с нулевым Timeout source не ограничивает.
const geoNamesTimeout = 15 * time.Minute

// geoNamesClient — HTTP-клиент загрузки GeoNames: подставленный хостом (тесты) — как есть; без WORKER_GEONAMES_CA_FILE —
// nil (клиент по умолчанию source, системные корни); с ним — системные корни плюс корень из файла (HTTPS-фикстура
// живого e2e, зеркало с собственным CA; Task 23).
func geoNamesClient(given *http.Client, g config.GeoNames) (*http.Client, error) {
	if given != nil {
		return given, nil
	}
	pool, err := g.RootCAs()
	if err != nil || pool == nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // DefaultTransport — *http.Transport
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: tr, Timeout: geoNamesTimeout}, nil
}

// validateRelay — до подключения к базе: пачка меньше 1 зациклила бы Drain, неположительный
// опрос уронил бы time.NewTicker паникой.
func validateRelay(r config.Relay) error {
	var errs []error
	if r.Batch < 1 {
		errs = append(errs, fmt.Errorf("WORKER_RELAY_BATCH = %d: нужно не меньше 1", r.Batch))
	}
	if r.Poll <= 0 {
		errs = append(errs, fmt.Errorf("WORKER_RELAY_POLL = %s: нужно больше нуля", r.Poll))
	}
	return errors.Join(errs...)
}

// validateQueues — до подключения к базе (R4): конкурентность каждой обслуживаемой очереди ≥ 1 — River отверг бы
// MaxWorkers < 1 в NewClient уже после подключения. Очередь import проверяется, только если импорт включён.
func validateQueues(c config.Worker) error {
	q := c.Queues
	checks := []struct {
		key string
		n   int
	}{
		{"WORKER_QUEUE_EVENTS", q.Events},
		{"WORKER_QUEUE_LIFECYCLE", q.Lifecycle},
		{"WORKER_QUEUE_NOTIFY", q.Notify},
		{"WORKER_QUEUE_MAIL", q.Mail},
		{"WORKER_QUEUE_STATS", q.Stats},
		{"WORKER_QUEUE_MEDIA", q.Media},
		{"WORKER_QUEUE_MAINTENANCE", q.Maintenance},
	}
	if c.GeoImportEnabled {
		checks = append(checks, struct {
			key string
			n   int
		}{"WORKER_QUEUE_IMPORT", q.Import})
	}
	var errs []error
	for _, ch := range checks {
		if ch.n < 1 {
			errs = append(errs, fmt.Errorf("%s = %d: нужно не меньше 1", ch.key, ch.n))
		}
	}
	return errors.Join(errs...)
}

// queues — очереди, которые обслуживает воркер: все восемь queue.Config; импорт выключен
// (WORKER_GEO_IMPORT_ENABLED=false) — без очереди import (задачи geo.import ждут), а не MaxWorkers 0.
func queues(c config.Worker) map[string]river.QueueConfig {
	m := queue.Config(c.Queues)
	if !c.GeoImportEnabled {
		delete(m, queue.Import)
	}
	return m
}
