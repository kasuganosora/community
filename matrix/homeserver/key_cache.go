package homeserver

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"gorm.io/gorm"
)

const currentKeyVerificationPolicy = "trusted-notaries-v1"

// KeyCacheEntry 缓存的密钥条目.
type KeyCacheEntry struct {
	PublicKey   ed25519.PublicKey
	ValidUntil  time.Time
	LastUpdated time.Time
	ExpiredTS   int64
}

// KeyCache 服务器密钥缓存.
type KeyCache struct {
	mu     sync.RWMutex
	cache  map[string]*KeyCacheEntry // key: "serverName:keyID"
	ttl    time.Duration
	dbGate chan struct{}

	// Persistence is best effort and asynchronous: the in-memory entry is what
	// verification uses, the database copy only survives restarts. Writes are
	// deduplicated per key, batched in one transaction and run under their own
	// timeout, so a slow database never fails (or blocks) a signature check.
	saveMu      sync.Mutex
	pending     map[string]pendingKeySave
	saveWake    chan struct{}
	saveStarted bool
	saveBusy    bool
	warn        keySaveWarner
}

type pendingKeySave struct {
	serverName, keyID string
	entry             *KeyCacheEntry
}

// keySaveTimeout bounds one batched write; keySaveBatch is keys per transaction;
// keySaveWarnInterval limits persistence warnings to one per interval.
var (
	keySaveTimeout      = 20 * time.Second
	keySaveBatch        = 200
	keySaveWarnInterval = time.Minute
)

// keySaveWarner collapses persistence failures into one warning per interval.
type keySaveWarner struct {
	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func (w *keySaveWarner) report(failed int, err error) {
	w.mu.Lock()
	w.suppressed += failed
	if time.Since(w.last) < keySaveWarnInterval {
		w.mu.Unlock()
		return
	}
	n := w.suppressed
	w.suppressed, w.last = 0, time.Now()
	w.mu.Unlock()
	log.Warn("Failed to cache %d server signing key(s) to the database (best effort, kept in memory; further failures within %s are not logged individually): %v", n, keySaveWarnInterval, err)
}

// NewKeyCache 创建新的密钥缓存.
func NewKeyCache() *KeyCache {
	return &KeyCache{
		cache:    make(map[string]*KeyCacheEntry),
		dbGate:   make(chan struct{}, 1),
		ttl:      24 * time.Hour, // 默认缓存24小时
		pending:  make(map[string]pendingKeySave),
		saveWake: make(chan struct{}, 1),
	}
}

func (kc *KeyCache) lockDB(ctx context.Context) (func(), error) {
	select {
	case kc.dbGate <- struct{}{}:
		return func() { <-kc.dbGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Get 从缓存获取密钥.
func (kc *KeyCache) Get(serverName, keyID string) (ed25519.PublicKey, bool) {
	return kc.GetAt(serverName, keyID, time.Now())
}

// Set 设置缓存密钥.
func (kc *KeyCache) Set(serverName, keyID string, publicKey ed25519.PublicKey, validUntil time.Time) {
	kc.set(serverName, keyID, publicKey, validUntil, 0)
}

func (kc *KeyCache) set(serverName, keyID string, publicKey ed25519.PublicKey, validUntil time.Time, expiredTS int64) {
	kc.setContext(context.Background(), serverName, keyID, publicKey, validUntil, expiredTS)
}

func (kc *KeyCache) setContext(_ context.Context, serverName, keyID string, publicKey ed25519.PublicKey, validUntil time.Time, expiredTS int64) {
	// The request/job context deliberately does not reach the database write:
	// a short or cancelled context must not lose the key or fail the write.
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)

	entry := &KeyCacheEntry{
		PublicKey:   append(ed25519.PublicKey(nil), publicKey...),
		ValidUntil:  validUntil,
		LastUpdated: time.Now(),
		ExpiredTS:   expiredTS,
	}

	kc.mu.Lock()
	kc.cache[cacheKey] = entry
	kc.mu.Unlock()

	kc.enqueueSave(cacheKey, serverName, keyID, entry)
}

// enqueueSave records the latest entry for a key; an older unsaved entry for the
// same key is replaced (dedupe), and the writer goroutine is woken.
func (kc *KeyCache) enqueueSave(cacheKey, serverName, keyID string, entry *KeyCacheEntry) {
	kc.saveMu.Lock()
	if kc.pending == nil {
		kc.pending = make(map[string]pendingKeySave)
	}
	if kc.saveWake == nil {
		kc.saveWake = make(chan struct{}, 1)
	}
	kc.pending[cacheKey] = pendingKeySave{serverName: serverName, keyID: keyID, entry: entry}
	if !kc.saveStarted {
		kc.saveStarted = true
		go kc.saveLoop()
	}
	kc.saveMu.Unlock()
	select {
	case kc.saveWake <- struct{}{}:
	default:
	}
}

func (kc *KeyCache) saveLoop() {
	for range kc.saveWake {
		// Let a burst (one key document carries several keys, a bulk join
		// carries thousands of servers) accumulate into one batch.
		time.Sleep(50 * time.Millisecond)
		kc.flushPending()
	}
}

// flushPending writes everything queued so far, in batches, under a fresh
// context. Failures are counted and warned about once per interval; the keys
// stay valid in memory and are simply re-fetched after a restart.
func (kc *KeyCache) flushPending() {
	for {
		kc.saveMu.Lock()
		if len(kc.pending) == 0 {
			kc.saveBusy = false
			kc.saveMu.Unlock()
			return
		}
		kc.saveBusy = true
		batch := make([]pendingKeySave, 0, min(len(kc.pending), keySaveBatch))
		for k, item := range kc.pending {
			batch = append(batch, item)
			delete(kc.pending, k)
			if len(batch) >= keySaveBatch {
				break
			}
		}
		kc.saveMu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), keySaveTimeout)
		unlock, err := kc.lockDB(ctx)
		if err == nil {
			// Skip entries replaced or invalidated while this batch waited.
			live := batch[:0]
			kc.mu.RLock()
			for _, item := range batch {
				if kc.cache[fmt.Sprintf("%s:%s", item.serverName, item.keyID)] == item.entry {
					live = append(live, item)
				}
			}
			kc.mu.RUnlock()
			err = kc.saveBatch(ctx, live)
			unlock()
		}
		cancel()
		if err != nil {
			kc.warn.report(len(batch), err)
		}
	}
}

func (kc *KeyCache) saveBatch(ctx context.Context, batch []pendingKeySave) error {
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range batch {
			record := keyRecord(item.serverName, item.keyID, item.entry)
			if err := tx.Where("server_name = ? AND key_id = ?", item.serverName, item.keyID).Assign(record).FirstOrCreate(&record).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// WaitSaved blocks until queued key writes finished or ctx ends (tests, shutdown).
func (kc *KeyCache) WaitSaved(ctx context.Context) error {
	for {
		kc.saveMu.Lock()
		idle := len(kc.pending) == 0 && !kc.saveBusy
		kc.saveMu.Unlock()
		if idle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func keyRecord(serverName, keyID string, entry *KeyCacheEntry) MatrixServerKey {
	return MatrixServerKey{
		VerificationPolicy: currentKeyVerificationPolicy,
		ExpiredTS:          entry.ExpiredTS,
		ServerName:         serverName,
		KeyID:              keyID,
		PublicKey:          base64.RawStdEncoding.EncodeToString(entry.PublicKey),
		ValidUntil:         entry.ValidUntil,
	}
}

// loadFromDB 从数据库加载密钥.
func (kc *KeyCache) loadFromDB(serverName, keyID string) *KeyCacheEntry {
	return kc.loadFromDBContext(context.Background(), serverName, keyID)
}

func (kc *KeyCache) loadFromDBContext(ctx context.Context, serverName, keyID string) *KeyCacheEntry {
	// Reads are not serialised behind batched writes: a key being saved is
	// already in memory, which is checked first.
	if ctx.Err() != nil {
		return nil
	}
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)
	kc.mu.RLock()
	entry := kc.cache[cacheKey]
	kc.mu.RUnlock()
	if entry != nil {
		return entry
	}
	var record MatrixServerKey
	if err := db.GetDB().WithContext(ctx).Where("server_name = ? AND key_id = ?", serverName, keyID).First(&record).Error; err != nil {
		return nil
	}

	// Older rows may originate from arbitrary join-server notaries. Refetch
	// rather than treating an unknown trust source as an authenticated key.
	if record.VerificationPolicy != currentKeyVerificationPolicy {
		return nil
	}
	// 解码公钥
	publicKeyBytes, err := base64.RawStdEncoding.DecodeString(record.PublicKey)
	if err != nil || len(publicKeyBytes) != ed25519.PublicKeySize {
		log.Error("Failed to decode cached public key for %s:%s: %v", serverName, keyID, err)

		return nil
	}

	entry = &KeyCacheEntry{
		PublicKey:   ed25519.PublicKey(publicKeyBytes),
		ValidUntil:  record.ValidUntil,
		LastUpdated: record.UpdatedAt,
		ExpiredTS:   record.ExpiredTS,
	}

	// 添加到内存缓存
	kc.mu.Lock()
	if fresh := kc.cache[cacheKey]; fresh != nil {
		entry = fresh
	} else {
		kc.cache[cacheKey] = entry
	}
	kc.mu.Unlock()
	return entry
}

// saveToDB 保存密钥到数据库.
func (kc *KeyCache) saveToDB(serverName, keyID string, entry *KeyCacheEntry) {
	kc.saveToDBContext(context.Background(), serverName, keyID, entry)
}

func (kc *KeyCache) saveToDBContext(ctx context.Context, serverName, keyID string, entry *KeyCacheEntry) {
	record := keyRecord(serverName, keyID, entry)

	// 使用 Upsert 操作
	err := db.GetDB().WithContext(ctx).Where("server_name = ? AND key_id = ?", serverName, keyID).
		Assign(record).
		FirstOrCreate(&record).Error
	if err != nil {
		kc.warn.report(1, err)
	}
}

// Invalidate 使缓存失效.
func (kc *KeyCache) Invalidate(serverName, keyID string) {
	unlock, _ := kc.lockDB(context.Background())
	defer unlock()
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)

	// A queued (not yet written) save must not revive the key afterwards.
	kc.saveMu.Lock()
	delete(kc.pending, cacheKey)
	kc.saveMu.Unlock()

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
	ExpiredTS          int64     `gorm:"not null;default:0"`
	VerificationPolicy string    `gorm:"size:64;not null;default:''"`
	ID                 uint64    `gorm:"primary_key;autoIncrement"`
	ServerName         string    `gorm:"not null;index;size:255"`
	KeyID              string    `gorm:"not null;index;size:64"`
	PublicKey          string    `gorm:"not null;type:text"`
	ValidUntil         time.Time `gorm:"not null;index"`
	CreatedAt          time.Time `gorm:"autoCreateTime"`
	UpdatedAt          time.Time `gorm:"autoUpdateTime"`
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
	return kc.GetForContext(context.Background(), serverName, keyID, at, check)
}

func (kc *KeyCache) GetForContext(ctx context.Context, serverName, keyID string, at spec.Timestamp, check gmsl.SignatureValidityCheckFunc) (ed25519.PublicKey, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	cacheKey := fmt.Sprintf("%s:%s", serverName, keyID)
	kc.mu.RLock()
	entry := kc.cache[cacheKey]
	kc.mu.RUnlock()
	if entry == nil {
		entry = kc.loadFromDBContext(ctx, serverName, keyID)
	}
	if entry == nil {
		return nil, false
	}
	if check == nil {
		check = gmsl.StrictValiditySignatureCheck
	}
	if entry.ExpiredTS > 0 {
		if int64(at) >= entry.ExpiredTS {
			return nil, false
		}
	} else if !check(at, spec.AsTimestamp(entry.ValidUntil)) {
		return nil, false
	}
	return append(ed25519.PublicKey(nil), entry.PublicKey...), true
}
