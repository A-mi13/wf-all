package domain

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Document — legal_document (спека §3.16): открытый enum контракта, в I-2 — два документа.
type Document string

const (
	DocTerms   Document = "terms"   // правила сервиса
	DocPrivacy Document = "privacy" // политика конфиденциальности
)

// documents — все документы в порядке выдачи; каждый обязателен.
var documents = [...]Document{DocTerms, DocPrivacy}

// MaxDocVersionLen — предел версии (consents.version 1–32).
const MaxDocVersionLen = 32

var (
	// ErrLegalConfig — API_LEGAL_DOCUMENTS не разбирается: отказ старта api до базы.
	ErrLegalConfig = errors.New("identity: API_LEGAL_DOCUMENTS")
	// ErrDocumentsOutdated — принятые документы не совпадают с текущими (identity.documents_outdated).
	ErrDocumentsOutdated = errors.New("identity: документы не приняты или устарели")
)

// semver 2.0.0 (semver.org, рекомендованное выражение) — без префикса «v».
var semverRe = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
	`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
	`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// LegalDoc — текущая версия документа и ссылка на текст.
type LegalDoc struct {
	Document Document
	Version  string
	URL      string
}

// LegalDocs — текущие документы из конфига; нулевое значение не годно (только ParseLegalDocuments).
type LegalDocs struct {
	items [len(documents)]LegalDoc
}

// Accepted — документ и версия, которые принял игрок.
type Accepted struct {
	Document Document
	Version  string
}

// ParseLegalDocuments — API_LEGAL_DOCUMENTS: «terms=<версия>|<url>,privacy=<версия>|<url>» в любом порядке. Каждый
// документ — ровно один раз, незнакомых нет; версия — дата YYYY-MM-DD или semver, 1–32 символа (спека §3.16); ссылка —
// абсолютная https с хостом. Ошибка — ErrLegalConfig с причиной.
func ParseLegalDocuments(s string) (LegalDocs, error) {
	var d LegalDocs
	var seen [len(documents)]bool
	if strings.TrimSpace(s) == "" {
		return d, fmt.Errorf("%w: пусто", ErrLegalConfig)
	}
	for part := range strings.SplitSeq(s, ",") {
		name, rest, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return d, fmt.Errorf("%w: %q — нужно <документ>=<версия>|<ссылка>", ErrLegalConfig, part)
		}
		version, link, ok := strings.Cut(rest, "|")
		if !ok {
			return d, fmt.Errorf("%w: %s — нет ссылки", ErrLegalConfig, name)
		}
		i := docIndex(Document(name))
		switch {
		case i < 0:
			return d, fmt.Errorf("%w: незнакомый документ %q", ErrLegalConfig, name)
		case seen[i]:
			return d, fmt.Errorf("%w: %s — дважды", ErrLegalConfig, name)
		case !validDocVersion(version):
			return d, fmt.Errorf("%w: %s — версия %q не YYYY-MM-DD и не semver (1–%d символов)", ErrLegalConfig, name,
				version, MaxDocVersionLen)
		}
		u, err := url.Parse(link)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return d, fmt.Errorf("%w: %s — ссылка %q не https", ErrLegalConfig, name, link)
		}
		seen[i] = true
		d.items[i] = LegalDoc{Document: Document(name), Version: version, URL: link}
	}
	for i, ok := range seen {
		if !ok {
			return d, fmt.Errorf("%w: нет документа %s", ErrLegalConfig, documents[i])
		}
	}
	return d, nil
}

// Items — текущие документы в порядке terms, privacy (getLegalDocuments).
func (d LegalDocs) Items() []LegalDoc { return append([]LegalDoc(nil), d.items[:]...) }

// Check — игрок принял ровно текущие версии всех документов: нет документа, лишний или повтор, версия ≠ текущей —
// ErrDocumentsOutdated.
func (d LegalDocs) Check(accepted []Accepted) error {
	if len(accepted) != len(documents) {
		return ErrDocumentsOutdated
	}
	var seen [len(documents)]bool
	for _, a := range accepted {
		i := docIndex(a.Document)
		if i < 0 || seen[i] || d.items[i].Version == "" || a.Version != d.items[i].Version {
			return ErrDocumentsOutdated
		}
		seen[i] = true
	}
	return nil
}

func docIndex(doc Document) int {
	for i, d := range documents {
		if d == doc {
			return i
		}
	}
	return -1
}

func validDocVersion(v string) bool {
	if v == "" || len(v) > MaxDocVersionLen {
		return false
	}
	if semverRe.MatchString(v) {
		return true
	}
	t, err := time.Parse(time.DateOnly, v)
	return err == nil && t.Format(time.DateOnly) == v
}
