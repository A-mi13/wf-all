package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// stepTokenBytes — 256 бит токена шага (mfa_token, restore_token).
const stepTokenBytes = 32

// NewStepToken — токен шага: 256 бит из crypto/rand в base64url без паддинга; в базе — только SHA-256 (hash).
func NewStepToken() (raw string, hash []byte, err error) {
	b := make([]byte, stepTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("identity: токен шага: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashStepToken(raw), nil
}

// HashStepToken — SHA-256 предъявленного токена для поиска по auth_challenges.token_hash.
func HashStepToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
