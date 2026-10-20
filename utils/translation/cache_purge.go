package translation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
)

// contentHash identifies a status's translatable content independent of the
// target language and backend.
func contentHash(p llmsvc.TranslationPayload) string {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func rememberStatusHash(statusID uint64, hash string) {
	conn := db.GetDB()
	if conn == nil {
		return
	}
	var n int64
	if conn.Model(&llmsvc.TranslationCacheRef{}).Where("status_id = ? AND content_hash = ?", statusID, hash).Count(&n).Error != nil || n > 0 {
		return
	}
	// A concurrent duplicate hits the unique index; that is harmless.
	_ = conn.Create(&llmsvc.TranslationCacheRef{StatusID: statusID, ContentHash: hash, CreatedAt: time.Now()}).Error
}

// PurgeStatus removes cached translations of a status's content. keepHash is
// the status's current content hash (kept on edits that did not change the
// translatable text); pass "" when the status is deleted. Entries are shared,
// so another status with identical content simply re-translates.
func PurgeStatus(statusID uint64, keepHash string) {
	conn := db.GetDB()
	if conn == nil || statusID == 0 {
		return
	}
	var hashes []string
	if err := conn.Model(&llmsvc.TranslationCacheRef{}).Where("status_id = ?", statusID).Pluck("content_hash", &hashes).Error; err != nil {
		return
	}
	for _, h := range hashes {
		if h == keepHash {
			continue
		}
		if err := conn.Where("content_hash = ?", h).Delete(&llmsvc.TranslationCache{}).Error; err != nil {
			log.Warn("translation cache: purge failed")
			continue
		}
		conn.Where("content_hash = ?", h).Delete(&llmsvc.TranslationCacheRef{})
	}
}

// PruneExpiredCache deletes cache entries and refs older than the cache TTL.
func PruneExpiredCache() {
	conn := db.GetDB()
	if conn == nil {
		return
	}
	cutoff := time.Now().Add(-cacheTTL)
	if err := conn.Where("created_at < ?", cutoff).Delete(&llmsvc.TranslationCache{}).Error; err != nil {
		log.Warn("translation cache: expiry cleanup failed")
	}
	conn.Where("created_at < ?", cutoff).Delete(&llmsvc.TranslationCacheRef{})
	conn.Where("created_at < ?", cutoff).Delete(&llmsvc.GroupTranslationCacheRef{})
}

// StartCacheJanitor prunes expired translation cache rows every hour.
func StartCacheJanitor(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		PruneExpiredCache()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				PruneExpiredCache()
			}
		}
	}()
}

// purgeAsync runs purges off the hook path; tests set it to run inline.
var purgeAsync = func(f func()) { go f() }

// SetSyncPurgeForTest makes status-event purges run synchronously.
func SetSyncPurgeForTest() { purgeAsync = func(f func()) { f() } }

func init() {
	event.Register("status", "*", &event.Handler{Exec: func(e event.Event) bool {
		s, ok := e.Data.(*models.Status)
		if !ok || s == nil || s.Id == 0 {
			return true
		}
		switch e.Name {
		case "after_delete":
			id := s.Id
			purgeAsync(func() { PurgeStatus(id, "") })
		case "after_update":
			p, _, _ := payloadOf(s)
			id, keep := s.Id, contentHash(p)
			purgeAsync(func() { PurgeStatus(id, keep) })
		}
		return true
	}})
}
