package geo_test

import (
	"slices"
	"testing"

	"wf/backend/internal/geo/internal/domain"
	"wf/backend/internal/platform/i18n"
)

// Языки названий справочника = языки backend/locales: HTTP-слой согласует Accept-Language по
// i18n.Supported (domain недоступен ему из-за internal), и списки не должны разойтись.
func TestSupportedLocalesMatchCatalog(t *testing.T) {
	if !slices.Equal(domain.SupportedLocales, i18n.Supported) {
		t.Fatalf("domain.SupportedLocales = %q, i18n.Supported = %q — должны совпадать", domain.SupportedLocales, i18n.Supported)
	}
}
