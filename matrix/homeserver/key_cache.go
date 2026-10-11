package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// KeyCacheEntry 缓存的密钥条目.
type KeyCacheEntry struct {
	PublicKey   ed25519.PublicKey
	ValidUntil  time.Time
	LastUpdated time.Time
}

// KeyCache 服务器密钥缓存.
type KeyCache struct {
	mu    sync.RWMutex
	cache map[string]*KeyCacheEntry // key: "serverName:keyID"
	ttl   time.Duration
}

// NewKeyCache 创建新的密钥缓存.
func NewKeyCache() *KeyCache {
	return &KeyCache{
		cache: make(map[string]*KeyCacheEntry),
		ttl:   24 * time.Hour, // 默认缓存24小时
	}
}

// Get 从缓存获取密钥.
func (kc *KeyCache) Get(serverName, keyID string) (ed25519.PublicKey, bool) {
	return kc.GetAt(serverName, keyID, time.Now())
}

// Set 设置缓存密钥.
func (kc *KeyCache) Set(serverName, keyID string, publicKey ed25519.PublicKey, validUntil time.Time) {
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)

	entry := &KeyCacheEntry{
		PublicKey:   append(ed25519.PublicKey(nil), publicKey...),
		ValidUntil:  validUntil,
		LastUpdated: time.Now(),
	}

	kc.mu.Lock()
	kc.cache[cacheKey] = entry
	defer kc.mu.Unlock()

	// Serialize persistence with replacement/invalidation so a delayed write cannot revive an old key.
	kc.saveToDB(serverName, keyID, entry)
}

// loadFromDB 从数据库加载密钥.
func (kc *KeyCache) loadFromDB(serverName, keyID string) *KeyCacheEntry {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)
	// A concurrent verified refresh wins over a stale cache miss.
	if entry := kc.cache[cacheKey]; entry != nil {
		return entry
	}
	var record MatrixServerKey
	if err := db.GetDB().Where("server_name = ? AND key_id = ?", serverName, keyID).First(&record).Error; err != nil {
		return nil
	}

	// 解码公钥
	publicKeyBytes, err := base64.RawStdEncoding.DecodeString(record.PublicKey)
	if err != nil || len(publicKeyBytes) != ed25519.PublicKeySize {
		log.Error("Failed to decode cached public key for %s:%s: %v", serverName, keyID, err)

		return nil
	}

	entry := &KeyCacheEntry{
		PublicKey:   ed25519.PublicKey(publicKeyBytes),
		ValidUntil:  record.ValidUntil,
		LastUpdated: record.UpdatedAt,
	}

	// 添加到内存缓存
	kc.cache[cacheKey] = entry
	return entry
}

// saveToDB 保存密钥到数据库.
func (kc *KeyCache) saveToDB(serverName, keyID string, entry *KeyCacheEntry) {
	record := MatrixServerKey{
		ServerName: serverName,
		KeyID:      keyID,
		PublicKey:  base64.RawStdEncoding.EncodeToString(entry.PublicKey),
		ValidUntil: entry.ValidUntil,
	}

	// 使用 Upsert 操作
	err := db.GetDB().Where("server_name = ? AND key_id = ?", serverName, keyID).
		Assign(record).
		FirstOrCreate(&record).Error
	if err != nil {
		log.Error("Failed to cache server key to DB for %s:%s: %v", serverName, keyID, err)
	}
}

// Invalidate 使缓存失效.
func (kc *KeyCache) Invalidate(serverName, keyID string) {
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)

	kc.mu.Lock()
	delete(kc.cache, cacheKey)
	defer kc.mu.Unlock()

	// 从数据库删除
	db.GetDB().Where("server_name = ? AND key_id = ?", serverName, keyID).Delete(&MatrixServerKey{})
}

// Cleanup 清理过期缓存.
func (kc *KeyCache) Cleanup() {
	kc.mu.Lock()
	defer kc.mu.Unlock()

	now := time.Now()
	for key, entry := range kc.cache {
		if now.After(entry.ValidUntil) {
			delete(kc.cache, key)
		}
	}
}

// MatrixServerKey 服务器密钥数据库模型.
type MatrixServerKey struct {
	ID         uint64    `gorm:"primary_key;autoIncrement"`
	ServerName string    `gorm:"not null;index;size:255"`
	KeyID      string    `gorm:"not null;index;size:64"`
	PublicKey  string    `gorm:"not null;type:text"`
	ValidUntil time.Time `gorm:"not null;index"`
	CreatedAt  time.Time `gorm:"autoCreateTime"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime"`
}

func (MatrixServerKey) TableName() string {
	return "matrix_server_keys"
}

// 全局密钥缓存实例.
var globalKeyCache *KeyCache
var keyCacheOnce sync.Once

// GetKeyCache 获取全局密钥缓存实例.
func GetKeyCache() *KeyCache {
	keyCacheOnce.Do(func() {
		globalKeyCache = NewKeyCache()
	})

	return globalKeyCache
}

// StartKeyCacheCleanup 启动定期清理任务.
func StartKeyCacheCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		for range ticker.C {
			GetKeyCache().Cleanup()
		}
	}()
}

// GetAt additionally checks the event timestamp against the advertised validity.
func (kc *KeyCache) GetAt(serverName, keyID string, at time.Time) (ed25519.PublicKey, bool) {
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)
	kc.mu.RLock()
	entry := kc.cache[cacheKey]
	kc.mu.RUnlock()
	if entry == nil {
		entry = kc.loadFromDB(serverName, keyID)
	}
	if entry == nil {
		return nil, false
	}
	// Key bytes and validity belong to the same immutable entry, even when another
	// goroutine replaces the cache slot while verification is in progress.
	if time.Now().After(entry.ValidUntil) {
		kc.mu.Lock()
		if kc.cache[cacheKey] == entry {
			delete(kc.cache, cacheKey)
		}
		kc.mu.Unlock()
		return nil, false
	}
	if at.After(entry.ValidUntil) {
		return nil, false
	}
	return append(ed25519.PublicKey(nil), entry.PublicKey...), true
}

// GetFor returns a cached key if the room version's validity rule accepts it
// for a signature made at `at`. Unlike GetAt it does not discard keys merely
// because their document has since expired: for rooms >= v5 a key whose
// valid_until_ts (or old-key expired_ts) is not before the event timestamp is
// still good for that event; for v1-v4 check is NoStrictValidityCheck and any
// known key is accepted. Requests made "now" (check = strict, at = now) still
// miss once the key has expired, which triggers a refetch.
func (kc *KeyCache) GetFor(serverName, keyID string, at spec.Timestamp, check gmsl.SignatureValidityCheckFunc) (ed25519.PublicKey, bool) {
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)
	kc.mu.RLock()
	entry := kc.cache[cacheKey]
	kc.mu.RUnlock()
	if entry == nil {
		entry = kc.loadFromDB(serverName, keyID)
	}
	if entry == nil {
		return nil, false
	}
	if check == nil {
		check = gmsl.StrictValiditySignatureCheck
	}
	if !check(at, spec.AsTimestamp(entry.ValidUntil)) {
		return nil, false
	}
	return append(ed25519.PublicKey(nil), entry.PublicKey...), true
}
