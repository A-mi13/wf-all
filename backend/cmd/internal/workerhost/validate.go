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

// queues — очереди, которые обслуживает воркер: все восемь queue.Config; импорт выключен
// (WORKER_GEO_IMPORT_ENABLED=false) — без очереди import (задачи geo.import ждут), а не MaxWorkers 0.
func queues(c config.Worker) map[string]river.QueueConfig {
	m := queue.Config(c.Queues)
	if !c.GeoImportEnabled {
		delete(m, queue.Import)
	}
	return m
}
