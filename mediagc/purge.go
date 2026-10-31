// Package mediagc removes storage objects for media that has been unattached
// longer than the retention window.
//
// Deleting a status does not delete its files. The attachment row stays, with
// status_id NULL, so the author can redraft with the same media id. This
// package is the later pass: rows with status_id NULL whose updated_at is
// older than the TTL (7 days by default) have their objects deleted and the
// row removed. There is no separate draft record. A scheduled post keeps
// status_id, so it is not eligible. An upload that has not been posted yet
// is simply unattached, and the TTL is its grace period too.
package mediagc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// DefaultMinAge is how long unattached media is kept. Status deletion
	// refreshes updated_at, so this is also the redraft window. Nothing in
	// system settings stores a media retention TTL; remove-orphans' one-hour
	// floor is a different grace period for objects that have no row yet.
	DefaultMinAge = 7 * 24 * time.Hour
	// MinimumMinAge stays above the async upload timeout so an in-flight
	// object is not treated as abandoned.
	MinimumMinAge = time.Hour
	// DefaultSweepLimit bounds one automatic pass so a backlog cannot hold
	// a database lock for the whole table.
	DefaultSweepLimit = 200

	sweepBatch = 50
)

// Options selects which detached attachments to purge.
type Options struct {
	// MinAge is the redraft window. Values below MinimumMinAge are rejected.
	MinAge time.Duration
	// Now is the clock. Zero means time.Now.
	Now time.Time
	// Limit is the maximum number of rows examined. Zero uses DefaultSweepLimit.
	Limit int
	// DryRun reports what would be deleted and does not change anything.
	DryRun bool
	// Out receives one line per candidate. Nil discards the detail.
	Out io.Writer
}

// Stats is the outcome of one purge pass.
type Stats struct {
	Candidates   int
	Removed      int
	WouldRemove  int
	Objects      int
	SharedKept   int
	ProfileKept  int
	Reattached   int
	Failed       int
	StoppedEarly bool
}

// Enabled reports whether the background purge should run. Empty is on.
// "off", "false", and "0" disable it.
func Enabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "false", "0":
		return false
	default:
		return true
	}
}

// ParseMinAge parses a retention window. Empty means DefaultMinAge.
func ParseMinAge(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultMinAge, nil
	}

	age, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}

	if age < MinimumMinAge {
		return 0, fmt.Errorf("retention %s is below the minimum %s", age, MinimumMinAge)
	}

	return age, nil
}

// Purge deletes expired detached attachments and their storage objects.
// A storage error leaves that row in place so the next pass can retry.
// Objects still referenced by another attachment, or by a profile image
// URL, are not deleted. Shared rows are removed; profile rows are kept
// because /media/{id} is what the profile URL serves.
func Purge(ctx context.Context, conn *gorm.DB, opts Options) (Stats, error) {
	if opts.MinAge < MinimumMinAge {
		return Stats{}, fmt.Errorf("min age %s is below the minimum %s", opts.MinAge, MinimumMinAge)
	}

	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	if opts.Limit <= 0 {
		opts.Limit = DefaultSweepLimit
	}

	cutoff := opts.Now.Add(-opts.MinAge)
	var stats Stats
	var failed error
	var lastID uint64

	for stats.Candidates < opts.Limit {
		if err := ctx.Err(); err != nil {
			return stats, err
		}

		n := sweepBatch
		if remain := opts.Limit - stats.Candidates; remain < n {
			n = remain
		}

		// Advance by id so a dry run, a profile row, or a storage failure
		// cannot select the same page forever.
		ids, err := candidateIDs(ctx, conn, cutoff, lastID, n)
		if err != nil {
			return stats, err
		}

		if len(ids) == 0 {
			break
		}

		lastID = ids[len(ids)-1]
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return stats, err
			}

			stats.Candidates++
			outcome, err := purgeOne(ctx, conn, id, cutoff, opts)
			if err != nil {
				stats.Failed++
				failed = errors.Join(failed, err)
				log.Warn("unattached media %d: storage delete failed: %v", id, err)
				printf(opts.Out, "media %d kept: storage delete failed\n", id)

				continue
			}

			switch outcome {
			case outcomeRemoved:
				if opts.DryRun {
					stats.WouldRemove++
				} else {
					stats.Removed++
					stats.Objects++
				}
			case outcomeShared:
				stats.SharedKept++
				if opts.DryRun {
					stats.WouldRemove++
				} else {
					stats.Removed++
				}
			case outcomeProfile:
				stats.ProfileKept++
			case outcomeReattached:
				stats.Reattached++
			}
		}

		if len(ids) < n {
			break
		}

		if stats.Candidates >= opts.Limit {
			stats.StoppedEarly = true
		}
	}

	if stats.Failed > 0 {
		return stats, fmt.Errorf("detached media purge kept %d attachment(s) after storage deletion failed: %w", stats.Failed, failed)
	}

	return stats, nil
}

type outcome int

const (
	outcomeReattached outcome = iota
	outcomeProfile
	outcomeShared
	outcomeRemoved
)

func candidateIDs(ctx context.Context, conn *gorm.DB, cutoff time.Time, afterID uint64, limit int) ([]uint64, error) {
	// No draft table exists. Scheduled posts and live posts keep status_id.
	var ids []uint64
	err := conn.WithContext(ctx).Model(&models.MediaAttachment{}).
		Where("id > ? AND status_id IS NULL AND updated_at < ?", afterID, cutoff).
		Order("id").Limit(limit).Pluck("id", &ids).Error

	return ids, err
}

func purgeOne(ctx context.Context, conn *gorm.DB, id uint64, cutoff time.Time, opts Options) (outcome, error) {
	var result outcome
	err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row models.MediaAttachment
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result = outcomeReattached
			return nil
		}

		if err != nil {
			return err
		}

		if !eligible(&row, cutoff) {
			result = outcomeReattached
			return nil
		}

		profile, err := profileUses(tx, &row)
		if err != nil {
			return err
		}

		if profile {
			result = outcomeProfile
			printf(opts.Out, "media %d kept: still used as a profile image\n", row.Id)

			return nil
		}

		shared, err := storageShared(tx, &row)
		if err != nil {
			return err
		}

		keys := objectKeys(&row)
		if opts.DryRun {
			result = outcomeRemoved
			if shared {
				result = outcomeShared
			}
			writeCandidate(opts.Out, &row, keys, shared)

			return nil
		}

		if shared {
			if err := tx.Delete(&row).Error; err != nil {
				return err
			}
			result = outcomeShared
			writeCandidate(opts.Out, &row, keys, true)

			return nil
		}

		// Delete objects before the row, while this transaction holds the
		// row lock, so a redraft cannot attach the id underneath us. A
		// storage failure returns an error and the row stays for retry.
		// Deleting a missing object is success, so a retry after a partial
		// delete finishes the rest.
		if err := row.DeleteStoredFiles(ctx); err != nil {
			return fmt.Errorf("media %d: %w", row.Id, err)
		}

		if err := tx.Delete(&row).Error; err != nil {
			return err
		}

		result = outcomeRemoved
		writeCandidate(opts.Out, &row, keys, false)

		return nil
	})

	return result, err
}

func eligible(row *models.MediaAttachment, cutoff time.Time) bool {
	if row.StatusId != nil && *row.StatusId != 0 {
		return false
	}

	return row.UpdatedAt.Before(cutoff)
}

func printf(out io.Writer, format string, args ...any) {
	if out == nil {
		return
	}

	_, _ = fmt.Fprintf(out, format, args...)
}

func objectKeys(row *models.MediaAttachment) []string {
	var keys []string
	if key := row.StorageKey(); key != "" {
		keys = append(keys, key)
	}

	if key := row.ThumbnailStorageKey(); key != "" && (len(keys) == 0 || key != keys[0]) {
		keys = append(keys, key)
	}

	return keys
}

func writeCandidate(out io.Writer, row *models.MediaAttachment, keys []string, shared bool) {
	if out == nil {
		return
	}

	note := "delete objects"
	if shared {
		note = "shared storage kept, row only"
	}

	if len(keys) == 0 {
		note = "no stored object, row only"
	}

	_, _ = fmt.Fprintf(out, "media %d %s: %s\n", row.Id, note, strings.Join(keys, ", "))
}

func profileUses(tx *gorm.DB, row *models.MediaAttachment) (bool, error) {
	if !tx.Migrator().HasTable("accounts") {
		return false, nil
	}

	like := "%/media/" + strconv.FormatUint(row.Id, 10) + "/%"
	q := tx.Table("accounts").Where(
		"avatar LIKE ? OR avatar_static LIKE ? OR header LIKE ? OR header_static LIKE ?",
		like, like, like, like,
	)

	if key := row.StorageKey(); key != "" {
		exact := []string{key, "/" + key, "uploads/" + key}
		q = q.Or(
			"avatar IN ? OR avatar_static IN ? OR header IN ? OR header_static IN ?",
			exact, exact, exact, exact,
		)
	}

	var n int64
	if err := q.Count(&n).Error; err != nil {
		return false, err
	}

	return n > 0, nil
}

func storageShared(tx *gorm.DB, row *models.MediaAttachment) (bool, error) {
	if strings.TrimSpace(row.FilePath) == "" {
		return false, nil
	}

	var n int64
	if err := tx.Model(&models.MediaAttachment{}).Where("id <> ? AND file_path = ?", row.Id, row.FilePath).Count(&n).Error; err != nil {
		return false, err
	}

	if n > 0 {
		return true, nil
	}

	keys := objectKeys(row)
	if len(keys) == 0 {
		return false, nil
	}

	for _, table := range []string{"matrix_media", "matrix_media_thumbnails"} {
		if !tx.Migrator().HasTable(table) {
			continue
		}

		n = 0
		if err := tx.Table(table).Where("file_path IN ?", keys).Count(&n).Error; err != nil {
			return false, err
		}

		if n > 0 {
			return true, nil
		}
	}

	return false, nil
}
