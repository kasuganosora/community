package homeserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const matrixMediaGrace = 7 * 24 * time.Hour

var errMatrixMediaReferenced = errors.New("matrix media is still referenced")

// MediaGCOptions deliberately has no age-based deletion of referenced media.
// Unknown legacy/native uploads are excluded: encrypted references are opaque.
type MediaGCOptions struct {
	Apply  bool
	Origin string // "", "local", "remote"; legacy origin is never guessed
	Limit  int
	Grace  time.Duration
}
type MediaGCResult struct{ Scanned, Retained, Waiting, Deleted int }

// CollectMedia is bounded and safe to run on several nodes. A durable deleting
// state fences new references; storage failures leave metadata for the next run.
func CollectMedia(ctx context.Context, conn *gorm.DB, now time.Time, opts MediaGCOptions) (MediaGCResult, error) {
	var result MediaGCResult
	if opts.Origin != "" && opts.Origin != "local" && opts.Origin != "remote" {
		return result, errors.New("invalid media origin")
	}
	if opts.Limit == 0 {
		opts.Limit = 32
	}
	if opts.Limit < 0 || opts.Limit > 1000 {
		return result, errors.New("media GC limit must be between 1 and 1000")
	}
	if opts.Grace == 0 {
		opts.Grace = matrixMediaGrace
	}
	if opts.Grace < 24*time.Hour {
		return result, errors.New("media GC grace must be at least 24 hours")
	}
	conn = conn.WithContext(ctx)
	var candidates []MatrixMedia
	q := conn.Where("(gc_eligible = ? AND gc_state = '' AND created_at <= ?) OR gc_state = ?", true, now.Add(-24*time.Hour), "deleting").Where("gc_next_check_at IS NULL OR gc_next_check_at <= ? OR (gc_state = '' AND gc_unreferenced_at <= ?)", now, now.Add(-opts.Grace))
	if opts.Origin != "" {
		q = q.Where("origin = ?", opts.Origin)
	}
	if err := q.Order("COALESCE(gc_next_check_at, created_at) ASC, id ASC").Limit(opts.Limit).Find(&candidates).Error; err != nil {
		return result, err
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Scanned++
		ready := false
		err := conn.Transaction(func(tx *gorm.DB) error {
			var m MatrixMedia
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&m, candidate.ID).Error; err != nil {
				return err
			}
			graceDue := m.GCState == "" && m.GCUnreferencedAt != nil && !m.GCUnreferencedAt.Add(opts.Grace).After(now)
			if m.GCState == "deleted" || (m.GCNextCheckAt != nil && m.GCNextCheckAt.After(now) && !graceDue) {
				return nil
			}
			if m.GCState == "deleting" {
				ready = true
				return nil
			}
			referenced, err := matrixMediaReferenced(tx, &m)
			if err != nil {
				return err
			}
			if referenced {
				result.Retained++
				if opts.Apply {
					return tx.Model(&m).Updates(map[string]any{"gc_unreferenced_at": nil, "gc_next_check_at": now.Add(time.Hour)}).Error
				}
				return nil
			}
			if m.GCUnreferencedAt == nil || m.GCUnreferencedAt.Add(opts.Grace).After(now) {
				result.Waiting++
				if opts.Apply {
					observed := now
					if m.GCUnreferencedAt != nil {
						observed = *m.GCUnreferencedAt
					}
					return tx.Model(&m).Updates(map[string]any{"gc_unreferenced_at": observed, "gc_next_check_at": observed.Add(opts.Grace)}).Error
				}
				return nil
			}
			ready = true
			if opts.Apply {
				return tx.Model(&m).Updates(map[string]any{"gc_state": "deleting", "gc_next_check_at": now.Add(time.Hour)}).Error
			}
			return nil
		})
		if err != nil {
			return result, err
		}
		if ready {
			if opts.Apply {
				if err := finishMatrixMediaDeletion(ctx, conn, candidate.ServerName, candidate.MediaID); err != nil {
					return result, err
				}
			}
			result.Deleted++
		}
	}
	return result, nil
}

// Content is the current, redacted representation. FederationJSON is an
// immutable signature snapshot and must not keep redacted attachments alive.
// LIKE can yield conservative false positives; it must never yield a deletion
// merely because another room/profile (or encrypted usage) was overlooked.
func matrixMediaReferenced(tx *gorm.DB, m *MatrixMedia) (bool, error) {
	uri := "mxc://" + m.ServerName + "/" + m.MediaID
	checks := []*gorm.DB{
		tx.Model(&MatrixEvent{}).Where("content LIKE ?", "%"+uri+"%"),
		tx.Model(&MatrixRoomState{}).Where("content LIKE ?", "%"+uri+"%"),
		tx.Model(&MatrixRoomMember{}).Where("avatar_url = ?", uri),
		tx.Model(&MatrixRoom{}).Where("avatar_url = ?", uri),
		tx.Model(&WebGroupUpload{}).Where("media_id = ? AND server_name = ? AND used_event_id <> ? AND expires_at > ?", m.MediaID, m.ServerName, "deleting", time.Now()),
		tx.Model(&AsyncMediaInfo{}).Where("media_id = ? AND status IN ?", m.MediaID, []string{"pending", "processing"}),
	}
	if m.UploaderID != "" {
		checks = append(checks, tx.Model(&MatrixEvent{}).Where("sender = ? AND event_type = ?", m.UploaderID, "m.room.encrypted"),
			tx.Table("matrix_room_members AS members").Joins("JOIN matrix_room_state AS encryption ON encryption.room_id = members.room_id AND encryption.event_type = ?", "m.room.encryption").Where("members.user_id = ? AND members.membership IN ?", m.UploaderID, []string{"join", "invite"}))
	}
	// These tables are optional only in standalone homeserver installations.
	if tx.Migrator().HasTable(&models.Account{}) {
		checks = append(checks, tx.Model(&models.Account{}).Unscoped().Where("avatar LIKE ? OR header LIKE ? OR avatar_static LIKE ? OR header_static LIKE ?", "%"+m.MediaID+"%", "%"+m.MediaID+"%", "%"+m.MediaID+"%", "%"+m.MediaID+"%"))
	}
	if m.FilePath != "" {
		// Shared Community-owned objects have their own lifecycle. Never delete them
		// from Matrix, including legacy records with unknown ownership.
		if !strings.HasPrefix(m.FilePath, "matrix/") {
			return true, nil
		}
		refs, err := matrixMediaFileReferences(tx, m)
		if err != nil {
			return false, err
		}
		aliases := append([]string{}, refs...)
		for _, ref := range refs {
			if !strings.HasPrefix(ref, "matrix/") {
				return true, nil
			}
			aliases = append(aliases, filepath.Base(ref))
		}
		checks = append(checks, tx.Model(&MatrixMedia{}).Where("id <> ? AND file_path IN ? AND gc_state <> ?", m.ID, refs, "deleted"),
			tx.Model(&MatrixThumbnail{}).Where("media_id <> ? AND file_path IN ?", m.MediaID, aliases))
		if tx.Migrator().HasTable(&models.MediaAttachment{}) {
			checks = append(checks, tx.Model(&models.MediaAttachment{}).Unscoped().Where("file_path IN ? OR thumbnail_path IN ?", aliases, aliases))
		}
	}
	for _, q := range checks {
		var found []int
		if err := q.Select("1").Limit(1).Scan(&found).Error; err != nil {
			return false, err
		}
		if len(found) != 0 {
			return true, nil
		}
	}
	return false, nil
}

func deleteMatrixMedia(ctx context.Context, conn *gorm.DB, server, id string) error {
	conn = conn.WithContext(ctx)
	err := conn.Transaction(func(tx *gorm.DB) error {
		var m MatrixMedia
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("server_name = ? AND media_id = ?", server, id).First(&m).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if m.GCState != "" {
			return nil
		}
		referenced, err := matrixMediaReferenced(tx, &m)
		if err != nil {
			return err
		}
		if referenced {
			return errMatrixMediaReferenced
		}
		return tx.Model(&m).Updates(map[string]any{"gc_state": "deleting", "gc_next_check_at": time.Now().Add(time.Hour)}).Error
	})
	if err != nil {
		return err
	}
	return finishMatrixMediaDeletion(ctx, conn, server, id)
}

func finishMatrixMediaDeletion(ctx context.Context, conn *gorm.DB, server, id string) error {
	// Lock through storage operations: concurrent collectors must not delete an
	// object recreated after a completed deletion. The caller's deadline bounds IO.
	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m MatrixMedia
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("server_name = ? AND media_id = ?", server, id).First(&m).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if m.GCState == "deleted" {
			return nil
		}
		if m.GCState != "deleting" {
			return errors.New("media deletion was not claimed")
		}
		refs, err := matrixMediaFileReferences(tx, &m)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref == "" {
				continue
			}
			if filepath.IsAbs(ref) {
				err = os.Remove(ref)
			} else {
				key, _ := storage.NormalizeKey(ref)
				if key == "" {
					return storage.ErrInvalidKey
				}
				err = storage.Default().Delete(ctx, key)
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
		}
		if err := tx.Where("media_id = ?", id).Delete(&MatrixThumbnail{}).Error; err != nil {
			return err
		}
		// Keep a tiny tombstone so a racing send cannot resurrect a deleted MXC.
		return tx.Model(&m).Updates(map[string]any{"gc_state": "deleted", "file_path": "", "file_size": 0, "remote_url": "", "gc_next_check_at": nil, "gc_unreferenced_at": nil}).Error
	})
}

var mediaMXCPattern = regexp.MustCompile(`mxc://[^\s"'<>\\]+`)

// pinMatrixMedia serializes publication with collection. Used in the event
// transaction and by explicit profile updates. Foreign MXCs have no local row.
func pinMatrixMedia(tx *gorm.DB, content any) error {
	return pinMatrixMediaReferences(tx, content, true)
}
func pinMatrixMediaReferences(tx *gorm.DB, content any, strict bool) error {
	set := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			for _, m := range mediaMXCPattern.FindAllString(v, -1) {
				set[m] = true
			}
		case map[string]any:
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(content)
	uris := make([]string, 0, len(set))
	for uri := range set {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	for _, uri := range uris {
		server, id, ok := strings.Cut(strings.TrimPrefix(uri, "mxc://"), "/")
		if !ok {
			continue
		}
		q := tx.Session(&gorm.Session{NewDB: true})
		var m MatrixMedia
		err := q.Clauses(clause.Locking{Strength: "UPDATE"}).Where("server_name = ? AND media_id = ?", server, id).First(&m).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if m.GCState != "" {
			if strict {
				return errors.New("media has expired; upload it again")
			}
			continue
		}
		if m.GCEligible {
			if err := q.Model(&m).Updates(map[string]any{"gc_unreferenced_at": nil, "gc_next_check_at": nil}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// Reap expired reservation metadata, including crashed processing claims. The
// conditional DELETE shares the same claim predicate as the upload transaction.
func cleanupAsyncMedia(ctx context.Context, conn *gorm.DB, now time.Time) error {
	expired := func(q *gorm.DB) *gorm.DB {
		return q.Where("expires_at <= ? AND (status <> ? OR claim_expires_at IS NULL OR claim_expires_at <= ?)", now, "processing", now)
	}
	var ids []uint64
	if err := expired(conn.WithContext(ctx).Model(&AsyncMediaInfo{})).Order("expires_at,id").Limit(128).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return expired(conn.WithContext(ctx).Where("id IN ?", ids)).Delete(&AsyncMediaInfo{}).Error
}

func runMatrixMediaMaintenance(ctx context.Context, hs *HomeServer) error {
	now := time.Now()
	var failures []error
	if err := cleanupWebGroupUploads(ctx, hs, now); err != nil {
		failures = append(failures, fmt.Errorf("upload leases: %w", err))
	}
	if err := cleanupAsyncMedia(ctx, db.GetDB(), now); err != nil {
		failures = append(failures, fmt.Errorf("async leases: %w", err))
	}
	result, err := CollectMedia(ctx, db.GetDB(), now, MediaGCOptions{Apply: true})
	if result.Deleted > 0 {
		log.Info("Matrix media collection: deleted=%d scanned=%d", result.Deleted, result.Scanned)
	}
	return errors.Join(append(failures, err)...)
}

var matrixMediaCleanupOnce sync.Once

func startMatrixMediaMaintenance() {
	matrixMediaCleanupOnce.Do(func() {
		go func() {
			// No per-user timers or workers. SQL claims make concurrent nodes safe.
			for range time.NewTicker(5 * time.Minute).C {
				hs := GetHomeServer()
				if hs == nil || db.GetDB() == nil {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				if err := runMatrixMediaMaintenance(ctx, hs); err != nil {
					log.Warn("Matrix media maintenance failed: %v", err)
				}
				cancel()
			}
		}()
	})
}

func matrixMediaFileReferences(tx *gorm.DB, m *MatrixMedia) ([]string, error) {
	var thumbs []MatrixThumbnail
	if err := tx.Where("media_id = ?", m.MediaID).Find(&thumbs).Error; err != nil {
		return nil, err
	}
	refs := []string{m.FilePath}
	for _, thumb := range thumbs {
		ref := thumb.FilePath
		if ref != "" && !strings.ContainsAny(ref, "/\\") {
			ref = filepath.Join(filepath.Dir(m.FilePath), ref)
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}
