package httpx

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

// MaxUserAgentBytes — потолок User-Agent, который уходит в аудит и сессии (ревью ветки, HTTP Minor 4).
// Настоящие браузеры укладываются в 200–300 байт; журнал неизменяем, и мегабайт от анонима в нём навсегда.
const MaxUserAgentBytes = 512

// UserAgent — заголовок User-Agent запроса для записи в базу (аудит, staff_sessions): не длиннее
// MaxUserAgentBytes байт с обрезкой по границе руны, невалидный UTF-8 — U+FFFD, без NUL (Postgres не
// принимает ни то, ни другое в text — запись упала бы 500). Нет заголовка — "". Единственный источник UA
// для записи: и requestMeta admin-api, и meta модуля staff.
func UserAgent(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) <= MaxUserAgentBytes && utf8.ValidString(ua) && !strings.ContainsRune(ua, 0) {
		return ua
	}
	// лишнее отрезается до разбора: работа ограничена потолком, а не длиной заголовка (до 1 МиБ)
	ua = ua[:min(len(ua), MaxUserAgentBytes+utf8.UTFMax)]
	ua = strings.ReplaceAll(strings.ToValidUTF8(ua, "�"), "\x00", "")
	if len(ua) <= MaxUserAgentBytes {
		return ua
	}
	cut := MaxUserAgentBytes
	for cut > 0 && !utf8.RuneStart(ua[cut]) {
		cut--
	}
	return ua[:cut]
}
