package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
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
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)

	kc.mu.RLock()
	entry, exists := kc.cache[cacheKey]
	kc.mu.RUnlock()

	if !exists {
		// 尝试从数据库加载
		entry = kc.loadFromDB(serverName, keyID)
		if entry == nil {
			return nil, false
		}
	}

	// 检查是否过期
	if time.Now().After(entry.ValidUntil) {
		kc.mu.Lock()
		delete(kc.cache, cacheKey)
		kc.mu.Unlock()

		return nil, false
	}

	return entry.PublicKey, true
}

// Set 设置缓存密钥.
func (kc *KeyCache) Set(serverName, keyID string, publicKey ed25519.PublicKey, validUntil time.Time) {
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)

	entry := &KeyCacheEntry{
		PublicKey:   publicKey,
		ValidUntil:  validUntil,
		LastUpdated: time.Now(),
	}

	kc.mu.Lock()
	kc.cache[cacheKey] = entry
	kc.mu.Unlock()

	// 同时保存到数据库
	kc.saveToDB(serverName, keyID, entry)
}

// loadFromDB 从数据库加载密钥.
func (kc *KeyCache) loadFromDB(serverName, keyID string) *KeyCacheEntry {
	var record MatrixServerKey
	if err := db.GetDB().Where("server_name = ? AND key_id = ?", serverName, keyID).First(&record).Error; err != nil {
		return nil
	}

	// 解码公钥
	publicKeyBytes, err := base64.RawStdEncoding.DecodeString(record.PublicKey)
	if err != nil {
		log.Error("Failed to decode cached public key for %s:%s: %v", serverName, keyID, err)

		return nil
	}

	entry := &KeyCacheEntry{
		PublicKey:   ed25519.PublicKey(publicKeyBytes),
		ValidUntil:  record.ValidUntil,
		LastUpdated: record.UpdatedAt,
	}

	// 添加到内存缓存
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)

	kc.mu.Lock()
	kc.cache[cacheKey] = entry
	kc.mu.Unlock()

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
	kc.mu.Unlock()

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
