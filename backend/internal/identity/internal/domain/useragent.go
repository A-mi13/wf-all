package domain

import "strings"

// maxUserAgent — длиннее UA не разбирается (sessions.user_agent ≤ 512).
const maxUserAgent = 512

// Порядок важен: Edge, Opera и Samsung Internet пишут в UA и «Chrome/», Chrome на iOS — «Safari/».
var browserMarks = []struct{ name, mark string }{
	{"Edge", "Edg/"}, {"Edge", "EdgA/"}, {"Edge", "EdgiOS/"},
	{"Opera", "OPR/"}, {"Opera", "OPiOS/"},
	{"Samsung Internet", "SamsungBrowser/"},
	{"Firefox", "Firefox/"}, {"Firefox", "FxiOS/"},
	{"Chrome", "Chrome/"}, {"Chrome", "CriOS/"},
	{"Safari", "Safari/"},
}

// Порядок важен: iPad и iPhone пишут «like Mac OS X», Android — «Linux».
var osMarks = []struct{ name, mark string }{
	{"iOS", "iPhone"}, {"iOS", "iPad"}, {"iOS", "iPod"},
	{"Android", "Android"},
	{"Windows", "Windows NT"},
	{"macOS", "Macintosh"},
	{"Linux", "Linux"},
}

// ParseUserAgent — браузер и ОС для «Где вы вошли» (R25, без зависимостей): Chrome, Edge, Firefox, Safari, Opera,
// Samsung Internet; Windows, macOS, iOS, Android, Linux. Незнакомое — пустая строка.
func ParseUserAgent(ua string) (browser, os string) {
	if len(ua) > maxUserAgent {
		ua = ua[:maxUserAgent]
	}
	for _, m := range browserMarks {
		if strings.Contains(ua, m.mark) {
			browser = m.name
			break
		}
	}
	// Safari без «Version/» — это встроенный WebView или иной движок WebKit, не браузер Safari
	if browser == "Safari" && !strings.Contains(ua, "Version/") {
		browser = ""
	}
	for _, m := range osMarks {
		if strings.Contains(ua, m.mark) {
			os = m.name
			break
		}
	}
	return browser, os
}
