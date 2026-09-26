package middleware

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kasuganosora/community/models"
)

func TestTokenDataExpired(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)

	assert.False(t, tokenDataExpired(&models.AuthTokenData{ExpiredAt: 0}, now), "0 = never expires (OAuth tokens)")
	assert.False(t, tokenDataExpired(&models.AuthTokenData{ExpiredAt: now.Unix() + 60}, now))
	assert.True(t, tokenDataExpired(&models.AuthTokenData{ExpiredAt: now.Unix() - 1}, now))
}
