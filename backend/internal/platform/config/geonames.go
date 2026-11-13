package config

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// RootCAs — корни TLS источника GeoNames: CAFile пуст — nil (системные корни клиента по умолчанию); задан — системные
// корни плюс сертификаты из файла (на Windows и macOS пул с добавленными корнями проверяет и платформой, и ими).
// Файла нет, в нём нет ни одного сертификата или сертификат испорчен — ошибка с именем ключа: воркер отказывается
// стартовать до подключения к базе, а не падает x509 на первом импорте.
func (g GeoNames) RootCAs() (*x509.CertPool, error) {
	if g.CAFile == "" {
		return nil, nil
	}
	bad := func(err error) error {
		return fmt.Errorf("WORKER_GEONAMES_CA_FILE = %q: %w", g.CAFile, err)
	}
	data, err := os.ReadFile(g.CAFile)
	if err != nil {
		return nil, bad(err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool() // системного пула нет (редкие платформы) — только корень из файла
	}
	n := 0
	for rest := data; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, bad(err)
		}
		pool.AddCert(cert)
		n++
	}
	if n == 0 {
		return nil, bad(errors.New("в файле нет сертификата PEM"))
	}
	return pool, nil
}
