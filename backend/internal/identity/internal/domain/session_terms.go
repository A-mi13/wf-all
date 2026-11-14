package domain

import "time"

// ClientKind — вид клиента сессии; ставит сервер по peer.ViaBFF (спека §3.7), не клиент.
type ClientKind string

const (
	ClientMobile ClientKind = "mobile"
	ClientWeb    ClientKind = "web"
)

// Сроки сессий и шагов (спека §3.7, §3.11, §3.13, §3.15).
const (
	mobileIdle     = 90 * 24 * time.Hour
	mobileAbsolute = 365 * 24 * time.Hour
	webIdle        = 30 * 24 * time.Hour
	webAbsolute    = 180 * 24 * time.Hour

	// RefreshRaceWindow — окно гонки: повтор того же refresh с того же устройства отдаёт того же преемника.
	RefreshRaceWindow = 15 * time.Second
	// MaxLiveSessions — живых сессий у аккаунта; сверх — старшие по last_seen_at отзываются (R17).
	MaxLiveSessions = 100
	// ReauthFresh — свежесть входа для операций «С» (§6.3) и вторым фактором (R27).
	ReauthFresh = 10 * time.Minute
	// ReauthFailures неудач свежего входа за ReauthFailuresWindow от первой — 429 (R16).
	ReauthFailures       = 5
	ReauthFailuresWindow = 15 * time.Minute
	// ChallengeAttempts — попыток по токену шага (mfa_token).
	ChallengeAttempts = 5
	// DeletionGrace — период отмены удаления до анонимизации.
	DeletionGrace = 30 * 24 * time.Hour
	// MfaResetDelay — сброс 2FA поддержкой исполняется не раньше.
	MfaResetDelay = 7 * 24 * time.Hour
)

// SessionTerms — срок бездействия (скользящий срок refresh) и абсолютный максимум семьи. Незнакомый вид — сроки веба
// (короче).
func SessionTerms(k ClientKind) (idle, absolute time.Duration) {
	if k == ClientMobile {
		return mobileIdle, mobileAbsolute
	}
	return webIdle, webAbsolute
}

// ChallengePurpose — назначение auth_challenges (тип auth_challenge_purpose).
type ChallengePurpose string

const (
	ChallengeMfa             ChallengePurpose = "mfa"
	ChallengeDeletionRestore ChallengePurpose = "deletion_restore"
)

// ChallengeTTL — срок токена шага I-2: mfa — 5 минут, deletion_restore — 10 минут. Назначения I-3 (соцвход, WebAuthn,
// аттестация) — их план; ok=false.
func ChallengeTTL(p ChallengePurpose) (time.Duration, bool) {
	switch p {
	case ChallengeMfa:
		return 5 * time.Minute, true
	case ChallengeDeletionRestore:
		return 10 * time.Minute, true
	}
	return 0, false
}

// RevokedReason — sessions.revoked_reason (CHECK, спека §5.3).
type RevokedReason string

const (
	RevokedLogout          RevokedReason = "logout"
	RevokedByUser          RevokedReason = "revoked" // «выйти здесь» и вытеснение сверх MaxLiveSessions (R17)
	RevokedOthers          RevokedReason = "revoked_others"
	RevokedPasswordChanged RevokedReason = "password_changed"
	RevokedPasswordReset   RevokedReason = "password_reset"
	RevokedRefreshReuse    RevokedReason = "refresh_reuse"
	RevokedAccountDeleted  RevokedReason = "account_deleted"
	RevokedBanned          RevokedReason = "banned"
	RevokedExpired         RevokedReason = "expired"
)
