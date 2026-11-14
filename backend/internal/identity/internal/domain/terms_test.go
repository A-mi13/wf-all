package domain_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/password"
)

const day = 24 * time.Hour

// kindRe — CHECK security_events.kind (спека §5.3).
var kindRe = regexp.MustCompile(`^[a-z][a-z_]*$`)

func TestSessionTerms(t *testing.T) {
	for k, want := range map[domain.ClientKind][2]time.Duration{
		domain.ClientMobile: {90 * day, 365 * day},
		domain.ClientWeb:    {30 * day, 180 * day},
	} {
		idle, abs := domain.SessionTerms(k)
		if idle != want[0] || abs != want[1] {
			t.Errorf("%s: %s %s", k, idle, abs)
		}
	}
	// незнакомый вид — строже веба не бывает: короткие сроки веба
	if idle, abs := domain.SessionTerms("tv"); idle != 30*day || abs != 180*day {
		t.Errorf("незнакомый: %s %s", idle, abs)
	}
	if domain.RefreshRaceWindow != 15*time.Second || domain.MaxLiveSessions != 100 || domain.ReauthFresh != 10*time.Minute ||
		domain.ReauthFailures != 5 || domain.ReauthFailuresWindow != 15*time.Minute || domain.DeletionGrace != 30*day ||
		domain.MfaResetDelay != 7*day || domain.ChallengeAttempts != 5 {
		t.Fatal("сроки §3.7")
	}
	for p, want := range map[domain.ChallengePurpose]time.Duration{
		domain.ChallengeMfa: 5 * time.Minute, domain.ChallengeDeletionRestore: 10 * time.Minute,
	} {
		if got, ok := domain.ChallengeTTL(p); !ok || got != want {
			t.Errorf("%s: %s %v", p, got, ok)
		}
	}
	if _, ok := domain.ChallengeTTL("social_signup"); ok {
		t.Error("срок задачи I-3 — в I-2")
	}
}

// Причины отзыва — ровно список CHECK sessions.revoked_reason (спека §5.3).
func TestRevokedReasons(t *testing.T) {
	want := []domain.RevokedReason{"logout", "revoked", "revoked_others", "password_changed", "password_reset",
		"refresh_reuse", "account_deleted", "banned", "expired"}
	got := []domain.RevokedReason{domain.RevokedLogout, domain.RevokedByUser, domain.RevokedOthers,
		domain.RevokedPasswordChanged, domain.RevokedPasswordReset, domain.RevokedRefreshReuse,
		domain.RevokedAccountDeleted, domain.RevokedBanned, domain.RevokedExpired}
	if len(got) != len(want) {
		t.Fatal(len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %q ≠ %q", i, got[i], want[i])
		}
	}
}

func TestPasswordWeakReason(t *testing.T) {
	for pw, want := range map[string]string{
		"short":                  "too_short",
		"password123":            "common",
		strings.Repeat("a", 129): "too_long",
	} {
		got, ok := domain.PasswordWeakReason(password.Check(pw))
		if !ok || got != want {
			t.Errorf("%q…: %q %v", pw[:min(len(pw), 8)], got, ok)
		}
	}
	if _, ok := domain.PasswordWeakReason(password.Check("длинный-и-редкий-пароль-42")); ok {
		t.Error("годный пароль — слабый")
	}
	if _, ok := domain.PasswordWeakReason(errors.New("другое")); ok {
		t.Error("чужая ошибка — причина")
	}
}

func TestSecurityLabels(t *testing.T) {
	if domain.LabelCode != "identity-code" || domain.LabelRecovery != "identity-recovery" ||
		domain.LabelDevice != "identity-device" || domain.LabelRateLimit != "identity-ratelimit" {
		t.Fatal("метки HMAC R15")
	}
	for _, k := range domain.SecurityKinds() {
		if !kindRe.MatchString(string(k)) {
			t.Errorf("вид %q не проходит CHECK security_events.kind", k)
		}
	}
	if len(domain.SecurityKinds()) != 15 {
		t.Fatalf("видов %d", len(domain.SecurityKinds()))
	}
}
