package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupKeyCacheTestDB creates an in-memory SQLite DB and assigns it to database.DB
// so that KeyCache's DB-backed methods work.
func setupKeyCacheTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := pgtest.GormOpen(t, sqlite.Open("file::memory:?cache=shared&_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	database.DB = db
	if err := db.AutoMigrate(&MatrixServerKey{}); err != nil {
		t.Fatalf("failed to migrate MatrixServerKey: %v", err)
	}

	return db
}

func TestNewKeyCache(t *testing.T) {
	kc := NewKeyCache()
	assert.NotNil(t, kc)
	assert.NotNil(t, kc.cache)
	assert.Equal(t, 24*time.Hour, kc.ttl)
}

func TestKeyCache_SetAndGet(t *testing.T) {
	setupKeyCacheTestDB(t)
	kc := NewKeyCache()

	pub, _, _ := ed25519.GenerateKey(nil)
	validUntil := time.Now().Add(24 * time.Hour)

	kc.Set("server.test", "ed25519:1", pub, validUntil)

	got, found := kc.Get("server.test", "ed25519:1")
	assert.True(t, found)
	assert.Equal(t, pub, got)
}

func TestKeyCache_Get_NotFound(t *testing.T) {
	setupKeyCacheTestDB(t)
	kc := NewKeyCache()

	got, found := kc.Get("nonexistent.test", "ed25519:abc")
	assert.False(t, found)
	assert.Nil(t, got)
}

func TestKeyCache_Get_Expired(t *testing.T) {
	setupKeyCacheTestDB(t)
	kc := NewKeyCache()

	pub, _, _ := ed25519.GenerateKey(nil)
	// Set with a past expiry → should be considered expired
	expiredTime := time.Now().Add(-1 * time.Hour)
	kc.Set("expired.test", "ed25519:1", pub, expiredTime)

	got, found := kc.Get("expired.test", "ed25519:1")
	assert.False(t, found, "expired key should not be found")
	assert.Nil(t, got)
}

func TestKeyCache_Invalidate(t *testing.T) {
	setupKeyCacheTestDB(t)
	kc := NewKeyCache()

	pub, _, _ := ed25519.GenerateKey(nil)
	kc.Set("invalidate.test", "ed25519:1", pub, time.Now().Add(24*time.Hour))

	// Should exist
	_, found := kc.Get("invalidate.test", "ed25519:1")
	assert.True(t, found)

	// Invalidate
	kc.Invalidate("invalidate.test", "ed25519:1")

	// Should no longer exist
	_, found = kc.Get("invalidate.test", "ed25519:1")
	assert.False(t, found)
}

func TestKeyCache_Cleanup(t *testing.T) {
	setupKeyCacheTestDB(t)
	kc := NewKeyCache()

	pub, _, _ := ed25519.GenerateKey(nil)

	// Add a valid key
	kc.Set("valid.test", "ed25519:1", pub, time.Now().Add(24*time.Hour))
	// Add an expired key
	kc.Set("expired.test", "ed25519:1", pub, time.Now().Add(-1*time.Hour))

	kc.Cleanup()

	// Valid key should remain
	_, found := kc.Get("valid.test", "ed25519:1")
	assert.True(t, found, "valid key should survive cleanup")

	// Expired key should be gone
	_, found = kc.Get("expired.test", "ed25519:1")
	assert.False(t, found, "expired key should be removed by cleanup")
}

func TestKeyCache_LoadFromDB(t *testing.T) {
	db := setupKeyCacheTestDB(t)
	kc := NewKeyCache()

	pub, _, _ := ed25519.GenerateKey(nil)

	// Insert a key directly into DB
	rec := MatrixServerKey{
		VerificationPolicy: currentKeyVerificationPolicy,
		ServerName:         "dbloaded.test",
		KeyID:              "ed25519:1",
		PublicKey:          encodePubKey(pub),
		ValidUntil:         time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.Create(&rec).Error)

	// Clear in-memory cache to force DB load
	kc.cache = make(map[string]*KeyCacheEntry)

	got, found := kc.Get("dbloaded.test", "ed25519:1")
	assert.True(t, found)
	assert.Equal(t, pub, got)
}

func TestKeyCache_PersistenceRoundTrip(t *testing.T) {
	setupKeyCacheTestDB(t)
	kc := NewKeyCache()

	pub, _, _ := ed25519.GenerateKey(nil)
	kc.Set("persist.test", "ed25519:1", pub, time.Now().Add(24*time.Hour))

	// Create a fresh cache (simulates restart) - key should load from DB
	kc2 := NewKeyCache()
	got, found := kc2.Get("persist.test", "ed25519:1")
	assert.True(t, found, "key should be loaded from DB after fresh cache")
	assert.Equal(t, pub, got)
}

func TestMatrixServerKey_TableName(t *testing.T) {
	assert.Equal(t, "matrix_server_keys", MatrixServerKey{}.TableName())
}

func TestKeyCacheEntry_Struct(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	entry := &KeyCacheEntry{
		PublicKey:   pub,
		ValidUntil:  time.Now().Add(1 * time.Hour),
		LastUpdated: time.Now(),
	}
	assert.Equal(t, pub, entry.PublicKey)
	assert.False(t, entry.ValidUntil.IsZero())
}

func encodePubKey(pub ed25519.PublicKey) string {
	return base64.RawStdEncoding.EncodeToString(pub)
}
