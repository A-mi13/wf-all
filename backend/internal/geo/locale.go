package geo

import (
	"wf/backend/internal/geo/internal/domain"
)

// nameLocale — локаль названий в запросе: поддерживаемая (domain.LocaleSupported) — она же,
// иначе "" — названия на языке страны каждой записи (спека §4.2). Защищает от локали, которую
// вызывающий не согласовал через i18n.Negotiate.
func nameLocale(locale string) string {
	if domain.LocaleSupported(locale) {
		return locale
	}
	return ""
}
