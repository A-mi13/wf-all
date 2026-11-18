package domain

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// contact_link: схема https в любом регистре приводится к «https://» (R26), остальное как есть; не https и длиннее
// 300 символов — отказ (CHECK captain_applications_contact_link_check).
func TestNormalizeContactLink(t *testing.T) {
	t.Parallel()
	ok := map[string]string{
		"HTTPS://t.me/x":    "https://t.me/x",
		"https://t.me/X":    "https://t.me/X",
		"HtTpS://vk.com/Ab": "https://vk.com/Ab",
	}
	for in, want := range ok {
		if got, good := NormalizeContactLink(in); !good || got != want {
			t.Errorf("%q → %q %v, ждали %q", in, got, good, want)
		}
	}
	long := "https://t.me/" + strings.Repeat("я", 300-len("https://t.me/")) // ровно 300 символов
	if got, good := NormalizeContactLink(long); !good || got != long {
		t.Errorf("300 символов: отказ")
	}
	for _, bad := range []string{"http://t.me/x", "", "t.me/x", "https:/t.me", " https://t.me/x", long + "я"} {
		if got, good := NormalizeContactLink(bad); good {
			t.Errorf("%q принят как %q", bad, got)
		}
	}
}

// Пауза — 7 дней от решения; порядок причин и коды отказа — §3.12, §7.3.
func TestCaptaincyConstants(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	if got := CooldownUntil(at); !got.Equal(at.Add(7 * 24 * time.Hour)) {
		t.Fatalf("CooldownUntil = %v", got)
	}
	want := []string{"too_young", "restricted", "profile_incomplete", "captain_elsewhere_in_city", "already_permitted",
		"checks_unavailable"}
	if !slices.Equal(ReasonOrder, want) {
		t.Fatalf("ReasonOrder = %v", ReasonOrder)
	}
	codes := []string{"profile_incomplete", "insufficient_info", "contact_unreachable", "suspicious_activity",
		"duplicate_account", "other"}
	if !slices.Equal(RejectionCodes, codes) {
		t.Fatalf("RejectionCodes = %v", RejectionCodes)
	}
}
