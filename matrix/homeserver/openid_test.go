package homeserver

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ===== generateOpenIDToken tests =====

func TestGenerateOpenIDToken_Format(t *testing.T) {
	token := generateOpenIDToken()
	assert.True(t, strings.HasPrefix(token, "openid_"), "token should start with openid_ prefix")
	// Default (crypto.rand) path: openid_ + 64 hex chars
	assert.Greater(t, len(token), len("openid_")+10)
}

func TestGenerateOpenIDToken_Uniqueness(t *testing.T) {
	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok := generateOpenIDToken()
		assert.False(t, tokens[tok], "duplicate token generated: %s", tok)
		tokens[tok] = true
	}
}

// ===== OpenIDToken model tests =====

func TestOpenIDToken_TableName(t *testing.T) {
	assert.Equal(t, "matrix_openid_tokens", OpenIDToken{}.TableName())
}

// ===== OpenIDTokenRequest / OpenIDTokenResponse struct tests =====

func TestOpenIDTokenRequest_Default(t *testing.T) {
	req := &OpenIDTokenRequest{}
	assert.Equal(t, int64(0), req.ExpiresIn)
}

func TestOpenIDTokenResponse_Fields(t *testing.T) {
	resp := &OpenIDTokenResponse{
		AccessToken:      "openid_abc123",
		TokenType:        "Bearer",
		MatrixServerName: "test.local",
		ExpiresIn:        3600,
	}
	assert.Equal(t, "openid_abc123", resp.AccessToken)
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.Equal(t, "test.local", resp.MatrixServerName)
	assert.Equal(t, int64(3600), resp.ExpiresIn)
}

// ===== OpenIDUserInfo struct test =====

func TestOpenIDUserInfo(t *testing.T) {
	info := &OpenIDUserInfo{Sub: "@alice:test.local"}
	assert.Equal(t, "@alice:test.local", info.Sub)
}
