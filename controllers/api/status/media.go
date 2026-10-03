package status

import (
	"gorm.io/gorm"
	"strconv"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// loadAttachableMedia loads the media attachments referenced by mediaIds for
// a new or edited status. Every id must parse, belong to accountId and be
// either unattached or already attached to statusId (0 for a new status).
// The result keeps the order of mediaIds. ok is false if any id is invalid.
func loadAttachableMedia(mediaIds []string, accountId uint64, statusId uint64) (attachments models.Attachments, ok bool) {
	return loadAttachableMediaDB(db.GetDB(), mediaIds, accountId, statusId)
}

func loadAttachableMediaDB(conn *gorm.DB, mediaIds []string, accountId uint64, statusId uint64) (attachments models.Attachments, ok bool) {
	ids := make([]uint64, 0, len(mediaIds))
	seen := make(map[uint64]bool, len(mediaIds))

	for _, raw := range mediaIds {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 || seen[id] {
			return nil, false
		}

		seen[id] = true
		ids = append(ids, id)
	}

	if len(ids) == 0 {
		return models.Attachments{}, true
	}

	found := make(models.Attachments, 0, len(ids))

	err := conn.Where("id IN ?", ids).Where("account_id = ?", accountId).Find(&found).Error
	if err != nil {
		return nil, false
	}

	byId := make(map[uint64]*models.MediaAttachment, len(found))
	for _, m := range found {
		if m.StatusId != nil && *m.StatusId != 0 && *m.StatusId != statusId {
			return nil, false // already attached to another status
		}

		byId[m.Id] = m
	}

	attachments = make(models.Attachments, 0, len(ids))

	for _, id := range ids {
		m, exists := byId[id]
		if !exists {
			return nil, false
		}

		attachments = append(attachments, m)
	}

	return attachments, true
}
