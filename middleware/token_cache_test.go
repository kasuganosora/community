package middleware

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useTestRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	oldClient := globalRedisClient
	globalRedisClientOnce.Do(func() {}) // make sure GetRedisClient won't overwrite it
	globalRedisClient = client

	t.Cleanup(func() {
		globalRedisClient = oldClient
		_ = client.Close()
	})

	return mr
}

func TestInvalidateTokenCache_RevokesCachedValidToken(t *testing.T) {
	mr := useTestRedis(t)

	const tokenStr = "raw-bearer-token-value"
	ctx := context.Background()

	// A token that AuthMiddleware recently validated is cached as "valid".
	require.NoError(t, globalRedisClient.Set(ctx, tokenCacheKey(tokenStr), "valid", tokenCacheTTL).Err())

	tok, err := getTokenWithCache(tokenStr)
	require.NoError(t, err)
	require.NotNil(t, tok)

	InvalidateTokenCache(tokenStr)

	_, err = getTokenWithCache(tokenStr)
	require.Error(t, err, "revoked token must be rejected without waiting for the cache TTL")

	ttl := mr.TTL(tokenCacheKey(tokenStr))
	assert.Greater(t, ttl, tokenCacheTTL)
	assert.LessOrEqual(t, ttl, revokedTokenCacheTTL)

	// Raw bearer tokens are never used as Redis keys.
	for _, k := range mr.Keys() {
		assert.False(t, strings.Contains(k, tokenStr), "redis key %q leaks the token", k)
	}
}

func TestInvalidateTokenCache_NoRedisIsNoOp(t *testing.T) {
	old := globalRedisClient
	globalRedisClientOnce.Do(func() {})
	globalRedisClient = nil
	t.Cleanup(func() { globalRedisClient = old })

	assert.NotPanics(t, func() { InvalidateTokenCache("anything") })
	assert.NotPanics(t, func() { InvalidateTokenCache("") })
}
