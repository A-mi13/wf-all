package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Причины «нельзя стать капитаном» (открытый enum CaptainIneligibility, спека §3.12 п. 3).
const (
	ReasonTooYoung               = "too_young"
	ReasonRestricted             = "restricted"
	ReasonProfileIncomplete      = "profile_incomplete"        // порт players.profile_complete
	ReasonCaptainElsewhereInCity = "captain_elsewhere_in_city" // порт teams.city_captaincy
	ReasonAlreadyPermitted       = "already_permitted"
	ReasonChecksUnavailable      = "checks_unavailable" // порт не подключён или город без настроек
)

// ReasonOrder — детерминированный порядок reasons в ответах (R5 плана I-4); причины вне списка — после, по алфавиту.
var ReasonOrder = []string{ReasonTooYoung, ReasonRestricted, ReasonProfileIncomplete, ReasonCaptainElsewhereInCity,
	ReasonAlreadyPermitted, ReasonChecksUnavailable}

// RejectionCodes — коды причины отказа модератора (открытый enum CaptainRejectionCode, спека §7.3; CHECK
// captain_applications_rejection_code_check); other — с обязательным комментарием.
var RejectionCodes = []string{"profile_incomplete", "insufficient_info", "contact_unreachable", "suspicious_activity",
	"duplicate_account", "other"}

// ApplicationCooldown — повторная подача после отказа не раньше (спека §3.12 п. 7, В3).
const ApplicationCooldown = 7 * 24 * time.Hour

// CooldownUntil — когда можно подать снова после отказа в decidedAt (граница — «уже можно»).
func CooldownUntil(decidedAt time.Time) time.Time { return decidedAt.Add(ApplicationCooldown) }

// maxContactLink — предел contact_link в символах (CHECK captain_applications_contact_link_check).
const maxContactLink = 300

const httpsScheme = "https://"

// NormalizeContactLink — ссылка для связи из заявки: схема https в любом регистре приводится к «https://» (R26 плана
// I-4: CHECK принимает любой регистр), остальное как есть; не https или длиннее 300 символов — false.
func NormalizeContactLink(s string) (string, bool) {
	if len(s) < len(httpsScheme) || !strings.EqualFold(s[:len(httpsScheme)], httpsScheme) ||
		utf8.RuneCountInString(s) > maxContactLink {
		return "", false
	}
	return httpsScheme + s[len(httpsScheme):], true
}
