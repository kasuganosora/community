package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"gorm.io/gorm"
)

// orphanNamespaces are the storage key namespaces remove-orphans may delete
// from. Every object written below them is owned by a media_attachments row
// (media/, legacy refreshed/) or a matrix_media / matrix_media_thumbnails row
// (matrix/). Emoji and brand images (emojis/) and legacy avatars (avatars/)
// are referenced from many places and are never deleted by the scan.
var orphanNamespaces = []string{"media/", "matrix/", "refreshed/"}

// minOrphanAge is the smallest grace period remove-orphans accepts: an upload
// writes its object before its database row, so younger objects may simply
// be in flight.
const minOrphanAge = time.Hour

const refBatchSize = 1000

// referenceSet holds every storage key the database points at.
type referenceSet struct {
	keys map[string]struct{}
	// owned counts references from the tables that own the deletable
	// namespaces (media attachments and Matrix media).
	owned int
}

func newReferenceSet() *referenceSet { return &referenceSet{keys: map[string]struct{}{}} }

func (r *referenceSet) add(keys ...string) {
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if c, err := storage.CleanKey(k); err == nil {
			r.keys[c] = struct{}{}
		}
	}
}

func (r *referenceSet) has(key string) bool {
	_, ok := r.keys[key]

	return ok
}

// Len returns the number of distinct referenced keys.
func (r *referenceSet) Len() int { return len(r.keys) }

// genericRefKeys maps a stored reference of unknown shape (key, host path,
// bucket URL, app URL) to every storage key it might denote.
func genericRefKeys(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	out := []string{emojiasset.ReferencedKey(raw)}

	if !filepath.IsAbs(raw) || strings.HasPrefix(raw, "/uploads/") {
		if key, _ := storage.NormalizeKey(raw); key != "" {
			out = append(out, key)
		}
	}

	// Absolute host paths written by old releases ("/srv/app/uploads/x").
	if i := strings.LastIndex(filepath.ToSlash(raw), "/uploads/"); i >= 0 {
		out = append(out, filepath.ToSlash(raw)[i+len("/uploads/"):])
	}

	return out
}

// mediaRefKeys returns the keys a media_attachments row may point at: the
// canonical key and, for legacy rows, the un-prefixed form, plus the
// thumbnails next to either.
func mediaRefKeys(filePath string, thumb *string) []string {
	m := models.MediaAttachment{FilePath: filePath, ThumbnailPath: thumb}
	keys := []string{m.StorageKey(), m.ThumbnailStorageKey()}
	keys = append(keys, genericRefKeys(filePath)...)

	rel := models.NormalizeMediaFilePath(filePath)
	keys = append(keys, rel, models.MediaKeyPrefix+rel)

	if t := m.ThumbPath(); t != "" {
		base := path.Base(filepath.ToSlash(t))
		for _, k := range []string{rel, models.MediaKeyPrefix + rel} {
			if k != "" {
				keys = append(keys, path.Join(path.Dir(k), base))
			}
		}
	}

	return keys
}

// collectReferencedKeys loads every storage reference from the database.
// Any query error aborts: a partial set would make live files look orphaned.
func collectReferencedKeys(ctx context.Context, gdb *gorm.DB) (*referenceSet, error) {
	refs := newReferenceSet()
	gdb = gdb.WithContext(ctx)

	type mediaRow struct {
		Id            uint64
		FilePath      string
		ThumbnailPath *string
	}

	var media []mediaRow
	err := gdb.Model(&models.MediaAttachment{}).Select("id, file_path, thumbnail_path").
		FindInBatches(&media, refBatchSize, func(*gorm.DB, int) error {
			for _, m := range media {
				if m.FilePath == "" {
					continue
				}
				refs.add(mediaRefKeys(m.FilePath, m.ThumbnailPath)...)
				refs.owned++
			}

			return nil
		}).Error
	if err != nil {
		return nil, fmt.Errorf("load media attachments: %w", err)
	}

	type accountRow struct {
		Id                                         uint64
		Avatar, AvatarStatic, Header, HeaderStatic string
	}

	var accounts []accountRow
	err = gdb.Model(&models.Account{}).Select("id, avatar, avatar_static, header, header_static").
		FindInBatches(&accounts, refBatchSize, func(*gorm.DB, int) error {
			for _, a := range accounts {
				for _, v := range []string{a.Avatar, a.AvatarStatic, a.Header, a.HeaderStatic} {
					refs.add(genericRefKeys(v)...)
				}
			}

			return nil
		}).Error
	if err != nil {
		return nil, fmt.Errorf("load account images: %w", err)
	}

	// Emoji (soft-deleted rows included: they can be restored) and other
	// rows that hold /emoji-assets URLs.
	urlColumns := []struct {
		model   any
		table   string
		columns []string
	}{
		{&models.Emoji{}, "emojis", []string{"url", "static_url"}},
		{&models.EmojiUpload{}, "emoji_uploads", []string{"url", "static_url"}},
		{&models.AvatarDecoration{}, "avatar_decorations", []string{"image_url"}},
		{&models.Advertisement{}, "advertisements", []string{"image_url"}},
	}
	for _, uc := range urlColumns {
		if !gdb.Migrator().HasTable(uc.model) {
			continue
		}
		for _, col := range uc.columns {
			var values []string
			if err := gdb.Unscoped().Model(uc.model).Where(col+" <> ''").Distinct().Pluck(col, &values).Error; err != nil {
				return nil, fmt.Errorf("load %s.%s: %w", uc.table, col, err)
			}
			for _, v := range values {
				refs.add(genericRefKeys(v)...)
			}
		}
	}

	if err := collectMatrixRefs(gdb, refs); err != nil {
		return nil, err
	}

	return refs, nil
}

func collectMatrixRefs(gdb *gorm.DB, refs *referenceSet) error {
	if !gdb.Migrator().HasTable("matrix_media") {
		return nil
	}

	type matrixRow struct {
		Id       uint64
		MediaId  string
		FilePath string
	}

	originals := map[string]string{} // media_id -> key

	var rows []matrixRow
	err := gdb.Table("matrix_media").Select("id, media_id, file_path").
		FindInBatches(&rows, refBatchSize, func(*gorm.DB, int) error {
			for _, r := range rows {
				if r.FilePath == "" {
					continue
				}
				keys := genericRefKeys(r.FilePath)
				refs.add(keys...)
				refs.owned++
				if k, _ := storage.NormalizeKey(r.FilePath); k != "" && !filepath.IsAbs(r.FilePath) {
					originals[r.MediaId] = k
				} else if len(keys) > 0 {
					originals[r.MediaId] = keys[len(keys)-1]
				}
			}

			return nil
		}).Error
	if err != nil {
		return fmt.Errorf("load matrix media: %w", err)
	}

	if !gdb.Migrator().HasTable("matrix_media_thumbnails") {
		return nil
	}

	rows = nil
	err = gdb.Table("matrix_media_thumbnails").Select("id, media_id, file_path").
		FindInBatches(&rows, refBatchSize, func(*gorm.DB, int) error {
			for _, r := range rows {
				if r.FilePath == "" {
					continue
				}
				refs.add(genericRefKeys(r.FilePath)...)
				refs.owned++
				// Bare file names live next to the original.
				if !strings.Contains(filepath.ToSlash(r.FilePath), "/") {
					if orig := originals[r.MediaId]; orig != "" {
						refs.add(path.Join(path.Dir(orig), r.FilePath))
					}
				}
			}

			return nil
		}).Error
	if err != nil {
		return fmt.Errorf("load matrix thumbnails: %w", err)
	}

	return nil
}

// stillReferenced re-checks a candidate against the database right before it
// is deleted, catching rows created after the reference set was loaded.
func stillReferenced(ctx context.Context, gdb *gorm.DB, key string) (bool, error) {
	gdb = gdb.WithContext(ctx)
	base := path.Base(key)

	var n int64

	switch {
	case strings.HasPrefix(key, "matrix/"):
		for _, table := range []string{"matrix_media", "matrix_media_thumbnails"} {
			if !gdb.Migrator().HasTable(table) {
				continue
			}
			if err := gdb.Table(table).Where("file_path IN ?", []string{key, base}).Count(&n).Error; err != nil {
				return true, err
			}
			if n > 0 {
				return true, nil
			}
		}

		return false, nil
	default:
		rel := strings.TrimPrefix(key, models.MediaKeyPrefix)
		err := gdb.Model(&models.MediaAttachment{}).
			Where("file_path IN ? OR thumbnail_path = ? OR file_path LIKE ?", []string{key, rel}, base, "%/"+base).
			Count(&n).Error

		return n > 0 || err != nil, err
	}
}

// orphanScanOptions configures scanOrphans.
type orphanScanOptions struct {
	Prefixes   []string
	StartAfter string
	MinAge     time.Duration
	Delete     bool
	Now        time.Time
	Out        io.Writer
}

// orphanScanResult summarises a scan.
type orphanScanResult struct {
	Scanned, Referenced, TooYoung, Orphans, Deleted, Failed int
	OrphanBytes                                             int64
}

// resolveOrphanPrefixes validates --prefix. Only keys inside orphanNamespaces
// may be scanned.
func resolveOrphanPrefixes(prefix string) ([]string, error) {
	prefix = strings.TrimLeft(strings.TrimSpace(prefix), "/")
	prefix = strings.TrimPrefix(prefix, "uploads/")
	if prefix == "" {
		return orphanNamespaces, nil
	}

	if strings.Contains(prefix, "..") || strings.ContainsAny(prefix, "\\\x00") {
		return nil, fmt.Errorf("invalid --prefix %q", prefix)
	}

	for _, ns := range orphanNamespaces {
		if strings.HasPrefix(prefix, ns) || prefix+"/" == ns {
			return []string{prefix}, nil
		}
	}

	return nil, fmt.Errorf("--prefix %q is outside the scannable namespaces %s (emoji, avatars and other assets are never scanned)", prefix, strings.Join(orphanNamespaces, ", "))
}

// scanOrphans lists objects below opts.Prefixes and reports (or, with
// opts.Delete, deletes) those that no database row references and that are
// older than opts.MinAge.
func scanOrphans(ctx context.Context, backend storage.Backend, gdb *gorm.DB, refs *referenceSet, opts orphanScanOptions) (orphanScanResult, error) {
	var res orphanScanResult

	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	if opts.Out == nil {
		opts.Out = io.Discard
	}

	cutoff := opts.Now.Add(-opts.MinAge)

	for _, prefix := range opts.Prefixes {
		err := backend.List(ctx, prefix, func(obj storage.ObjectInfo) error {
			if opts.StartAfter != "" && obj.Key <= opts.StartAfter {
				return nil
			}

			res.Scanned++

			if refs.has(obj.Key) {
				res.Referenced++

				return nil
			}

			if obj.ModTime.IsZero() || obj.ModTime.After(cutoff) {
				res.TooYoung++

				return nil
			}

			if live, err := stillReferenced(ctx, gdb, obj.Key); err != nil {
				return fmt.Errorf("re-check %s: %w", obj.Key, err)
			} else if live {
				res.Referenced++

				return nil
			}

			res.Orphans++
			res.OrphanBytes += obj.Size

			if !opts.Delete {
				_, _ = fmt.Fprintf(opts.Out, "[ORPHAN] %s (%d bytes, modified %s)\n", obj.Key, obj.Size, obj.ModTime.Format(time.RFC3339))

				return nil
			}

			if err := backend.Delete(ctx, obj.Key); err != nil {
				res.Failed++
				_, _ = fmt.Fprintf(opts.Out, "[FAILED] %s: %v\n", obj.Key, err)

				return nil
			}

			res.Deleted++
			_, _ = fmt.Fprintf(opts.Out, "[DELETED] %s (%d bytes)\n", obj.Key, obj.Size)

			return nil
		})
		if err != nil {
			return res, fmt.Errorf("list %s: %w", prefix, err)
		}
	}

	return res, nil
}

// namespaceUsage is the object count and byte total of one key namespace.
type namespaceUsage struct {
	Namespace string
	Objects   int64
	Bytes     int64
}

// storageUsage sums object sizes per top-level namespace.
func storageUsage(ctx context.Context, backend storage.Backend) ([]namespaceUsage, error) {
	byNS := map[string]*namespaceUsage{}

	err := backend.List(ctx, "", func(obj storage.ObjectInfo) error {
		ns, _, found := strings.Cut(obj.Key, "/")
		if !found {
			ns = "(root)"
		}

		u := byNS[ns]
		if u == nil {
			u = &namespaceUsage{Namespace: ns}
			byNS[ns] = u
		}

		u.Objects++
		u.Bytes += obj.Size

		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]namespaceUsage, 0, len(byNS))
	for _, u := range byNS {
		out = append(out, *u)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Namespace < out[j].Namespace })

	return out, nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

var errNoReferences = errors.New("the database has no media references but storage holds objects; refusing to treat everything as orphaned (wrong database?)")
