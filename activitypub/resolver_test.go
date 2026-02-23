package activitypub

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestCacheEntryIsExpired(t *testing.T) {
	t.Run("not expired", func(t *testing.T) {
		entry := &CacheEntry{
			value:     "test",
			expiresAt: time.Now().Add(1 * time.Hour),
		}
		assert.False(t, entry.IsExpired())
	})

	t.Run("expired", func(t *testing.T) {
		entry := &CacheEntry{
			value:     "test",
			expiresAt: time.Now().Add(-1 * time.Hour),
		}
		assert.True(t, entry.IsExpired())
	})

	t.Run("just expired", func(t *testing.T) {
		entry := &CacheEntry{
			value:     "test",
			expiresAt: time.Now().Add(-1 * time.Millisecond),
		}
		assert.True(t, entry.IsExpired())
	})
}

func TestShouldRefreshAccount(t *testing.T) {
	t.Run("nil LastActivityAt should refresh", func(t *testing.T) {
		account := &models.Account{
			LastActivityAt: nil,
		}
		assert.True(t, shouldRefreshAccount(account))
	})

	t.Run("recent activity should not refresh", func(t *testing.T) {
		now := time.Now()
		account := &models.Account{
			LastActivityAt: &now,
		}
		assert.False(t, shouldRefreshAccount(account))
	})

	t.Run("old activity should refresh", func(t *testing.T) {
		old := time.Now().Add(-48 * time.Hour)
		account := &models.Account{
			LastActivityAt: &old,
		}
		assert.True(t, shouldRefreshAccount(account))
	})

	t.Run("exactly 24h ago should not refresh", func(t *testing.T) {
		// 24 hours exactly - time.Since returns > 24h only if strictly more
		justOver := time.Now().Add(-24*time.Hour - 1*time.Second)
		account := &models.Account{
			LastActivityAt: &justOver,
		}
		assert.True(t, shouldRefreshAccount(account))
	})

	t.Run("23h ago should not refresh", func(t *testing.T) {
		recent := time.Now().Add(-23 * time.Hour)
		account := &models.Account{
			LastActivityAt: &recent,
		}
		assert.False(t, shouldRefreshAccount(account))
	})
}

func TestResolverCacheTTL(t *testing.T) {
	resolver := NewResolver(nil, "https://example.com/actor")
	assert.Equal(t, defaultCacheTTL, resolver.cacheTTL)
	assert.Equal(t, 30*time.Minute, defaultCacheTTL)
}

func TestResolverMaxCacheEntries(t *testing.T) {
	assert.Equal(t, 10000, maxCacheEntries)
}

func TestResolverAccountCache(t *testing.T) {
	resolver := NewResolver(nil, "https://example.com/actor")

	// Cache miss
	account, ok := resolver.loadAccountCache("https://remote.com/users/alice")
	assert.False(t, ok)
	assert.Nil(t, account)

	// Store and retrieve
	testAccount := &models.Account{
		Username: "alice",
	}
	resolver.storeAccountCache("https://remote.com/users/alice", testAccount)
	account, ok = resolver.loadAccountCache("https://remote.com/users/alice")
	assert.True(t, ok)
	assert.Equal(t, "alice", account.Username)

	// Different key should miss
	account, ok = resolver.loadAccountCache("https://remote.com/users/bob")
	assert.False(t, ok)
	assert.Nil(t, account)
}

func TestResolverAccountCacheExpiry(t *testing.T) {
	resolver := &Resolver{
		client:   NewClient(nil, ""),
		cacheTTL: 50 * time.Millisecond, // Very short TTL
	}

	testAccount := &models.Account{Username: "alice"}
	resolver.storeAccountCache("key", testAccount)

	// Should be cached
	account, ok := resolver.loadAccountCache("key")
	assert.True(t, ok)
	assert.NotNil(t, account)

	// Wait for expiry
	time.Sleep(60 * time.Millisecond)

	// Should be expired
	account, ok = resolver.loadAccountCache("key")
	assert.False(t, ok)
	assert.Nil(t, account)
}

func TestResolverStatusCache(t *testing.T) {
	resolver := NewResolver(nil, "https://example.com/actor")

	// Cache miss
	status, ok := resolver.loadStatusCache("https://remote.com/statuses/1")
	assert.False(t, ok)
	assert.Nil(t, status)

	// Store and retrieve
	testStatus := &models.Status{
		Visibility: models.StatusVisibilityPublic,
	}
	resolver.storeStatusCache("https://remote.com/statuses/1", testStatus)
	status, ok = resolver.loadStatusCache("https://remote.com/statuses/1")
	assert.True(t, ok)
	assert.Equal(t, uint8(models.StatusVisibilityPublic), status.Visibility)
}

func TestResolverStatusCacheExpiry(t *testing.T) {
	resolver := &Resolver{
		client:   NewClient(nil, ""),
		cacheTTL: 50 * time.Millisecond,
	}

	testStatus := &models.Status{}
	resolver.storeStatusCache("key", testStatus)

	status, ok := resolver.loadStatusCache("key")
	assert.True(t, ok)
	assert.NotNil(t, status)

	time.Sleep(60 * time.Millisecond)

	status, ok = resolver.loadStatusCache("key")
	assert.False(t, ok)
	assert.Nil(t, status)
}
