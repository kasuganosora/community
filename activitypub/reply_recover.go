package activitypub

import (
	"context"
	"errors"
	"fmt"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// ReplyParentRepair walks unlinked remote replies from newest to older ids.
// Limit is the page size. AfterID is the oldest id already examined; only
// smaller ids are read, and zero starts at the newest. MaxPages stops early;
// zero keeps reading until no older unlinked remote reply remains.
// OnPage, when set, runs after each page with that page's oldest id.
// The parent object is never fetched.
type ReplyParentRepair struct {
	Limit    int
	AfterID  uint64
	MaxPages int
	OnPage   func(page, examined, linked int, oldestID uint64)
}

// ReplyParentRepairResult reports one cursor walk.
// Examined is the status ids read, newest first.
// Cursor is the oldest id examined, or zero when no remote reply was read.
// Pass Cursor as AfterID to continue with older ids.
// Done is true when no older unlinked remote reply remains.
type ReplyParentRepairResult struct {
	Linked   int
	Examined []uint64
	Cursor   uint64
	Done     bool
}

// RecoverUnlinkedReplyParents links replies whose parent is already stored.
// When fetch is set and limit is positive, it reads remote replies from their
// own origin, one page of limit at a time, continuing with older status ids
// until none remain. It uses inReplyTo only when that parent is already
// stored. It does not fetch the parent.
func RecoverUnlinkedReplyParents(ctx context.Context, limit int, fetch func(context.Context, string) (map[string]any, error)) (int, error) {
	result, err := RepairReplyParents(ctx, ReplyParentRepair{Limit: limit}, fetch)
	return result.Linked, err
}

// RepairReplyParents links stored reply parents, then reads unlinked remote
// replies page by page. Rows that stay unlinked still advance the cursor.
func RepairReplyParents(ctx context.Context, repair ReplyParentRepair, fetch func(context.Context, string) (map[string]any, error)) (ReplyParentRepairResult, error) {
	var result ReplyParentRepairResult
	if repair.Limit < 0 || repair.MaxPages < 0 {
		return result, errors.New("limit and max-pages must be zero or positive")
	}
	conn := db.GetDB()
	base := systemsettings.GetBaseURL()
	linked, err := models.RepairUnlinkedReplyParents(conn, base)
	if err != nil {
		return result, err
	}
	result.Linked = linked
	if fetch == nil || repair.Limit == 0 {
		result.Done = true
		return result, nil
	}

	after := repair.AfterID
	for page := 1; repair.MaxPages == 0 || page <= repair.MaxPages; page++ {
		rows, err := listUnlinkedRemoteReplies(ctx, conn, after, repair.Limit)
		if err != nil {
			return result, err
		}
		if len(rows) == 0 {
			result.Done = true
			return result, nil
		}
		pageLinked, oldest, err := repairReplyParentPage(ctx, conn, base, after, rows, fetch, &result)
		if err != nil {
			return result, err
		}
		if oldest == 0 || (after > 0 && oldest >= after) {
			return result, fmt.Errorf("reply parent repair did not advance past status %d", after)
		}
		after = oldest
		result.Cursor = oldest
		if repair.OnPage != nil {
			repair.OnPage(page, len(rows), pageLinked, oldest)
		}
		if len(rows) < repair.Limit {
			result.Done = true
			return result, nil
		}
	}
	return result, nil
}

func repairReplyParentPage(ctx context.Context, conn *gorm.DB, base string, after uint64, rows []models.Status, fetch func(context.Context, string) (map[string]any, error), result *ReplyParentRepairResult) (int, uint64, error) {
	pageLinked := 0
	var oldest uint64
	for i := range rows {
		status := &rows[i]
		if after > 0 && status.Id >= after {
			return pageLinked, oldest, fmt.Errorf("reply parent repair did not advance past status %d", after)
		}
		if oldest != 0 && status.Id >= oldest {
			return pageLinked, oldest, fmt.Errorf("reply parent repair saw status %d out of descending id order", status.Id)
		}
		if err := ctx.Err(); err != nil {
			return pageLinked, oldest, err
		}
		linked, err := linkRemoteReplyParent(ctx, conn, base, status, fetch)
		if err != nil {
			return pageLinked, oldest, err
		}
		result.Examined = append(result.Examined, status.Id)
		result.Linked += linked
		pageLinked += linked
		oldest = status.Id
		result.Cursor = oldest
	}
	return pageLinked, oldest, nil
}

func linkRemoteReplyParent(ctx context.Context, conn *gorm.DB, base string, status *models.Status, fetch func(context.Context, string) (map[string]any, error)) (int, error) {
	if status.OriginUri == nil || *status.OriginUri == "" {
		return 0, nil
	}
	note, fetchErr := fetch(ctx, *status.OriginUri)
	if fetchErr != nil || note == nil {
		return 0, nil
	}
	target := noteInReplyTo(note["inReplyTo"])
	if target == "" || target == *status.OriginUri {
		return 0, nil
	}
	models.SetReplyToURI(status, target)
	if err := models.LinkStoredReplyParents(conn, []*models.Status{status}, base); err != nil {
		return 0, err
	}
	if status.InReplyToId != nil && *status.InReplyToId != 0 {
		return 1, nil
	}
	if err := conn.WithContext(ctx).Model(status).Select("Properties").Updates(status).Error; err != nil {
		return 0, err
	}
	return 0, nil
}

func listUnlinkedRemoteReplies(ctx context.Context, conn *gorm.DB, afterID uint64, limit int) ([]models.Status, error) {
	query := conn.WithContext(ctx).Preload("Content").
		Where("(in_reply_to_id IS NULL OR in_reply_to_id = 0) AND deleted_at IS NULL AND origin_uri IS NOT NULL AND origin_uri <> '' AND (reblog_id IS NULL OR reblog_id = 0)")
	if afterID > 0 {
		query = query.Where("id < ?", afterID)
	}
	var rows []models.Status
	err := query.Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
