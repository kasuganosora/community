package activitypub

import (
	"context"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// RecoverUnlinkedReplyParents links replies whose parent is already stored.
// When fetch is set, it also reads up to limit remote replies from their own
// origin and uses inReplyTo only when that parent is already local or stored.
// It does not fetch the parent.
func RecoverUnlinkedReplyParents(ctx context.Context, limit int, fetch func(context.Context, string) (map[string]any, error)) (int, error) {
	conn := db.GetDB()
	base := systemsettings.GetBaseURL()
	linked, err := models.RepairUnlinkedReplyParents(conn, base)
	if err != nil || fetch == nil || limit <= 0 {
		return linked, err
	}
	var rows []models.Status
	err = conn.Preload("Content").
		Where("(in_reply_to_id IS NULL OR in_reply_to_id = 0) AND deleted_at IS NULL AND origin_uri IS NOT NULL AND origin_uri <> '' AND (reblog_id IS NULL OR reblog_id = 0)").
		Order("id DESC").Limit(limit).Find(&rows).Error
	if err != nil {
		return linked, err
	}
	for i := range rows {
		status := &rows[i]
		if status.OriginUri == nil || *status.OriginUri == "" {
			continue
		}
		if ctx.Err() != nil {
			return linked, ctx.Err()
		}
		note, fetchErr := fetch(ctx, *status.OriginUri)
		if fetchErr != nil || note == nil {
			continue
		}
		target := noteInReplyTo(note["inReplyTo"])
		if target == "" || target == *status.OriginUri {
			continue
		}
		models.SetReplyToURI(status, target)
		if err = models.LinkStoredReplyParents(conn, []*models.Status{status}, base); err != nil {
			return linked, err
		}
		if status.InReplyToId != nil && *status.InReplyToId != 0 {
			linked++
			continue
		}
		if err = conn.Model(status).Select("Properties").Updates(status).Error; err != nil {
			return linked, err
		}
	}
	return linked, nil
}
