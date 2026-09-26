package models

// Media attachments live in a single table, media_attachments, written by the
// /api/v1/media and /api/v2/media upload endpoints and linked to a status via
// media_attachments.status_id (like Mastodon).
//
// The former Attachment model (table "attachments", joined to statuses via
// status_media_attachments) was never migrated and did not match what the
// upload path writes, so posting with media_ids failed with "Table
// 'attachments' doesn't exist". Attachment/Attachments are kept as aliases so
// callers keep compiling; migration UnifyMediaAttachmentsScript moves any old
// join-table links into media_attachments.status_id and drops the old tables.

// Attachment is an alias of MediaAttachment.
type Attachment = MediaAttachment

// Attachments is an alias of MediaAttachments.
type Attachments = MediaAttachments

// GetIds returns the ids of the attachments in order.
func (a MediaAttachments) GetIds() []uint64 {
	ids := make([]uint64, 0, len(a))
	for _, attachment := range a {
		ids = append(ids, attachment.Id)
	}

	return ids
}
