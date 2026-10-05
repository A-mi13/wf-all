// Package source — загрузка и разбор выгрузки GeoNames (https://www.geonames.org, CC BY 4.0):
// основной файл страны (<CC>.zip), альтернативные названия V2 (alternatenames/<CC>.zip) и
// admin1CodesASCII.txt. Только HTTPS, редиректы — на тот же хост, лимиты сжатого и
// распакованного размера на файл, из zip — ровно одна запись <CC>.txt, разбор потоком
// без распаковки на диск (спека geo §5.4 шаги 2–3).
package source

import "net/http"

// FetchConfig — откуда и в каких пределах качать. HTTPClient nil — клиент по умолчанию
// (таймаут 15 минут); тесты подставляют клиент TLS-сервера httptest.
type FetchConfig struct {
	BaseURL         string // https://download.geonames.org
	MaxCompressed   int64  // байт на скачиваемый файл
	MaxUncompressed int64  // байт на распакованную запись zip
	HTTPClient      *http.Client
}

// File — скачанный файл для журнала импорта (geonames_imports.source_files).
type File struct {
	Name   string // путь от export/dump/: RU.zip, alternatenames/RU.zip, admin1CodesASCII.txt
	URL    string // итоговый адрес после редиректов
	Bytes  int64  // размер скачанного (сжатого для zip)
	SHA256 string // hex скачанного
}

// Raw — разобранная выгрузка страны.
type Raw struct {
	Places   []Place
	AltNames []AltName
	Admin1   []Admin1
	Files    []File
}

// Place — строка основного файла (19 колонок; нужные импорту).
type Place struct {
	GeonameID    int64
	Name         string
	ASCIIName    string
	FeatureClass string
	FeatureCode  string
	CountryCode  string
	Admin1Code   string
	Timezone     string // пустая бывает (PPLQ и др.): импорт такие места пропускает
	Population   int64
	Lat, Lon     float64
}

// AltName — строка alternateNamesV2 (10 колонок). To — «до какого времени название
// употреблялось» как в источнике: год (1946) или дата (28.07.1997, 1997-07-28, 19970728).
type AltName struct {
	ID, GeonameID int64
	Locale        string // isolanguage: ru, en, "" (без языка), link, wkdt, post…
	Name          string
	Preferred     bool
	Short         bool
	Colloquial    bool
	Historic      bool
	To            string
}

// Admin1 — строка admin1CodesASCII.txt: code «RU.70» → CountryCode RU, Code 70.
// Названия в файле английские; переводы региона — альтернативные названия по GeonameID.
type Admin1 struct {
	CountryCode string
	Code        string
	Name        string
	ASCIIName   string
	GeonameID   int64
}
