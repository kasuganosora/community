package homeserver

import (
	"bytes"
	"crypto/ed25519"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKeyCacheOwnsImmutableKeyBytes(t *testing.T) {
	setupKeyCacheTestDB(t)
	cache := NewKeyCache()
	original := bytes.Repeat([]byte{1}, ed25519.PublicKeySize)
	cache.Set("immutable.test", "ed25519:key", original, time.Now().Add(time.Hour))
	original[0] = 2
	key, ok := cache.Get("immutable.test", "ed25519:key")
	require.True(t, ok)
	require.Equal(t, byte(1), key[0])
	key[0] = 3
	key, ok = cache.Get("immutable.test", "ed25519:key")
	require.True(t, ok)
	require.Equal(t, byte(1), key[0])
}

func TestKeyCacheGetAtUsesOneEntryDuringConcurrentRefresh(t *testing.T) {
	cache := NewKeyCache()
	now := time.Now()
	at := now.Add(2 * time.Hour)
	old := &KeyCacheEntry{PublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize), ValidUntil: now.Add(time.Hour)}
	fresh := &KeyCacheEntry{PublicKey: bytes.Repeat([]byte{2}, ed25519.PublicKeySize), ValidUntil: now.Add(3 * time.Hour)}
	const slot = "concurrent.test:ed25519:key"
	cache.cache[slot] = old
	var invalid atomic.Bool
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 20000 {
			cache.mu.Lock()
			cache.cache[slot] = fresh
			cache.mu.Unlock()
			cache.mu.Lock()
			cache.cache[slot] = old
			cache.mu.Unlock()
		}
	})
	for range 4 {
		workers.Go(func() {
			for range 20000 {
				key, ok := cache.GetAt("concurrent.test", "ed25519:key", at)
				if ok && !bytes.Equal(key, fresh.PublicKey) {
					invalid.Store(true)
				}
			}
		})
	}
	workers.Wait()
	require.False(t, invalid.Load(), "an old key must not borrow a concurrently refreshed entry's validity")
}

func TestKeyCacheMissDoesNotOverwriteVerifiedRefresh(t *testing.T) {
	db := setupKeyCacheTestDB(t)
	cache := NewKeyCache()
	old := bytes.Repeat([]byte{1}, ed25519.PublicKeySize)
	require.NoError(t, db.Create(&MatrixServerKey{VerificationPolicy: currentKeyVerificationPolicy, ServerName: "refresh.test", KeyID: "ed25519:key", PublicKey: encodePubKey(old), ValidUntil: time.Now().Add(time.Hour)}).Error)
	fresh := &KeyCacheEntry{PublicKey: bytes.Repeat([]byte{2}, ed25519.PublicKeySize), ValidUntil: time.Now().Add(2 * time.Hour)}
	cache.cache["refresh.test:ed25519:key"] = fresh
	require.Same(t, fresh, cache.loadFromDB("refresh.test", "ed25519:key"))
}
