package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Файлы /.well-known до веба (спека identity §9.4, К4; план I-2, Task 12, R14): ассоциация приложений с доменом api
// для webcredentials (iOS) и Credential Manager (Android). Вне контракта: public.NewHandler монтирует обработчик до
// маршрутизатора контракта (Options.WellKnown), как пробы и /docs, — без валидатора и лимитов класса auth.

const (
	// WellKnownPrefix — каталог, который монтирует public.NewHandler.
	WellKnownPrefix = "/.well-known/"
	pathAASA        = WellKnownPrefix + "apple-app-site-association"
	pathAssetLinks  = WellKnownPrefix + "assetlinks.json"
)

type aasa struct {
	WebCredentials struct {
		Apps []string `json:"apps"`
	} `json:"webcredentials"`
}

type assetLink struct {
	Relation []string        `json:"relation"`
	Target   assetLinkTarget `json:"target"`
}

type assetLinkTarget struct {
	Namespace    string   `json:"namespace"`
	PackageName  string   `json:"package_name"`
	Fingerprints []string `json:"sha256_cert_fingerprints"`
}

// WellKnown — GET /.well-known/apple-app-site-association ({"webcredentials": {"apps": [...]}}) по appleAppIDs
// (API_WELLKNOWN_APPLE_APP_IDS) и GET /.well-known/assetlinks.json (отношение
// delegate_permission/common.get_login_creds) по android (API_WELLKNOWN_ANDROID: <package>=<SHA-256>[;…]).
// Content-Type: application/json, без редиректов; файл с пустым конфигом, иной путь или метод — 404. Конфиг
// разбирается при сборке: битая строка android (нет пакета, отпечаток не 32 байта hex через «:») — ошибка с именем
// ключа; cmd/api зовёт до подключения к базе — отказ старта.
func WellKnown(appleAppIDs []string, android string) (http.Handler, error) {
	files := map[string][]byte{}
	var apps []string
	for _, id := range appleAppIDs {
		if id = strings.TrimSpace(id); id != "" {
			apps = append(apps, id)
		}
	}
	if len(apps) > 0 {
		var doc aasa
		doc.WebCredentials.Apps = apps
		b, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		files[pathAASA] = b
	}
	links, err := parseAndroid(android)
	if err != nil {
		return nil, err
	}
	if len(links) > 0 {
		b, err := json.Marshal(links)
		if err != nil {
			return nil, err
		}
		files[pathAssetLinks] = b
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}), nil
}

// parseAndroid — «<package>=<SHA-256 сертификата>» через «;»; пустое — без файла.
func parseAndroid(s string) ([]assetLink, error) {
	var out []assetLink
	for part := range strings.SplitSeq(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pkg, fp, ok := strings.Cut(part, "=")
		pkg, fp = strings.TrimSpace(pkg), strings.TrimSpace(fp)
		if !ok || pkg == "" || !isSHA256Fingerprint(fp) {
			return nil, fmt.Errorf("API_WELLKNOWN_ANDROID: %q — нужно <package>=<SHA-256 сертификата: 32 байта hex через «:»>", part)
		}
		out = append(out, assetLink{Relation: []string{"delegate_permission/common.get_login_creds"},
			Target: assetLinkTarget{Namespace: "android_app", PackageName: pkg, Fingerprints: []string{fp}}})
	}
	return out, nil
}

// isSHA256Fingerprint — отпечаток в форме assetlinks: 32 байта, каждый — две hex-цифры, между байтами «:»
// («AA:BB:…», 95 символов). Регистр не меняется: файл отдаёт значение как задано.
func isSHA256Fingerprint(fp string) bool {
	const n = 32
	if len(fp) != n*3-1 {
		return false
	}
	for i := range len(fp) {
		if i%3 == 2 {
			if fp[i] != ':' {
				return false
			}
			continue
		}
		if !isHex(fp[i]) {
			return false
		}
	}
	return true
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
