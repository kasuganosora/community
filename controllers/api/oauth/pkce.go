package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

const (
	pkceMethodS256 = "S256"
	pkceMinLen     = 43
	pkceMaxLen     = 128
)

// validatePKCERequest checks an authorization request. PKCE is optional for
// pre-registered Mastodon apps and required for IndieAuth / Misskey OAuth clients.
func validatePKCERequest(challenge, method string, required bool) string {
	challenge = strings.TrimSpace(challenge)
	method = strings.TrimSpace(method)

	if challenge == "" && method == "" {
		if required {
			return "code_challenge is required"
		}

		return ""
	}

	if challenge == "" || method == "" {
		return "code_challenge and code_challenge_method are both required"
	}

	if method != pkceMethodS256 {
		return "unsupported code_challenge_method"
	}

	if len(challenge) < pkceMinLen || len(challenge) > pkceMaxLen {
		return "invalid code_challenge"
	}

	if _, err := base64.RawURLEncoding.DecodeString(challenge); err != nil {
		return "invalid code_challenge"
	}

	return ""
}

func verifyPKCE(verifier, challenge, method string) bool {
	if method != pkceMethodS256 || challenge == "" {
		return false
	}

	if !validVerifier(verifier) {
		return false
	}

	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])

	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

func validVerifier(verifier string) bool {
	if len(verifier) < pkceMinLen || len(verifier) > pkceMaxLen {
		return false
	}

	for _, r := range verifier {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '.' || r == '_' || r == '~':
		default:
			return false
		}
	}

	return true
}
