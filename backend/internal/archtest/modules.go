package archtest

import (
	"fmt"
	"strings"
)

// Layers — модули бэкенда и их слой (спека бэкенда §4.3). Модуль импортирует только
// модули с меньшим слоем; верхний слой (9) друг друга не импортирует. Новый модуль —
// новая строка здесь, иначе TestInternalDirsAreKnown упадёт.
var Layers = map[string]int{
	"geo":          0,
	"identity":     1,
	"media":        2,
	"economy":      3,
	"players":      4,
	"pitches":      5,
	"teams":        6,
	"matches":      7,
	"results":      8,
	"stats":        9,
	"reputation":   9,
	"competitions": 9,
	"predictions":  9,
	"chat":         9,
	"moderation":   9,
	"notify":       9,
	"ads":          9,
}

// IntxPairs — кто может импортировать подпакет intx другого модуля (спека §4.4):
// ключ — {импортирующий модуль, владелец intx}. Расширяется только правкой спеки.
var IntxPairs = map[[2]string]bool{
	{"teams", "identity"}:      true, // капитанский допуск, смена города
	{"moderation", "identity"}: true, // санкции
	{"matches", "economy"}:     true, // платная бронь позиции
	{"teams", "economy"}:       true, // командные предметы
	{"predictions", "economy"}: true, // ставка прогноза
	{"results", "matches"}:     true, // явка и исход матча из протокола
}

// infra — каталоги internal/, которые не модули.
var infra = map[string]bool{"archtest": true, "httpapi": true, "platform": true}

const internalPrefix = "wf/backend/internal/"

// isKnownOwner — имя модуля из Layers или platform (владелец общих таблиц).
func isKnownOwner(name string) bool {
	_, ok := Layers[name]
	return ok || name == "platform"
}

// splitInternal — первый сегмент после internal/ и остаток пути.
func splitInternal(pkg string) (top, rest string, ok bool) {
	r, ok := strings.CutPrefix(pkg, internalPrefix)
	if !ok {
		return "", "", false
	}
	top, rest, _ = strings.Cut(r, "/")
	return top, rest, true
}

// under — rest равен dir или лежит внутри него.
func under(rest, dir string) bool {
	return rest == dir || strings.HasPrefix(rest, dir+"/")
}

// importViolation — почему импорт from → to нарушает границы модулей; "" — не нарушает.
// cmd/* и внешние пакеты — точки сборки и зависимости, их не ограничиваем. Немодульные
// каталоги internal/ (httpapi, platform, archtest) видят у модуля только корневой пакет,
// httpapi и admin; платформа не зависит ни от модулей, ни от httpapi и archtest.
func importViolation(from, to string) string {
	fTop, fRest, ok := splitInternal(from)
	if !ok {
		return ""
	}
	tTop, tRest, ok := splitInternal(to)
	if !ok || fTop == tTop {
		return ""
	}
	_, fromModule := Layers[fTop]
	_, toModule := Layers[tTop]
	switch {
	case fTop == "platform" && toModule:
		return "платформа не зависит от модулей"
	case fTop == "platform" && (tTop == "httpapi" || tTop == "archtest"):
		return "платформа не зависит от сборки HTTP-сервера и стражей"
	case infra[fTop] && toModule:
		// сборка сервера (httpapi) и стражи видят модуль снаружи: корневой пакет и хендлеры
		// httpapi/ и admin/; intx — только из пар §4.4, внутренности модуля — никому
		if tRest != "" && tRest != "httpapi" && tRest != "admin" {
			return fmt.Sprintf("из internal/%s у модуля %s можно импортировать только корневой пакет, httpapi и admin", fTop, tTop)
		}
	case fromModule && tTop == "httpapi":
		if under(fRest, "httpapi") && to == internalPrefix+"httpapi/public/oapi" ||
			under(fRest, "admin") && to == internalPrefix+"httpapi/admin/oapi" {
			return ""
		}
		return "модуль не зависит от сборки HTTP-сервера; сгенерированные типы oapi — только из своих httpapi/ и admin/"
	case fromModule && tTop == "archtest":
		return "модуль не зависит от archtest"
	case fromModule && toModule:
		if tRest != "" && tRest != "intx" {
			return fmt.Sprintf("у модуля %s можно импортировать только корневой пакет и intx", tTop)
		}
		if Layers[tTop] >= Layers[fTop] {
			return fmt.Sprintf("модуль %s (слой %d) не может зависеть от %s (слой %d) — спека §4.3",
				fTop, Layers[fTop], tTop, Layers[tTop])
		}
		if tRest == "intx" && !IntxPairs[[2]string{fTop, tTop}] {
			return fmt.Sprintf("%s → %s/intx нет в списке транзакций между модулями (спека §4.4)", fTop, tTop)
		}
	}
	return ""
}
