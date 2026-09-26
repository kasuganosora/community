package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Attachment is an alias of MediaAttachment, stored in media_attachments.
func TestAttachment_IsMediaAttachment(t *testing.T) {
	var a *Attachment = &MediaAttachment{Id: 7}
	assert.Equal(t, uint64(7), a.Id)
}

func TestMediaAttachment_ThumbPath(t *testing.T) {
	assert.Empty(t, (&MediaAttachment{}).ThumbPath())

	p := "thumb.jpg"
	assert.Equal(t, "thumb.jpg", (&MediaAttachment{ThumbnailPath: &p}).ThumbPath())
}

func TestAttachments_GetIds(t *testing.T) {
	attachments := Attachments{
		{Id: 1},
		{Id: 2},
		{Id: 3},
	}
	ids := attachments.GetIds()
	assert.Equal(t, []uint64{1, 2, 3}, ids)
}

func TestAttachments_GetIds_Empty(t *testing.T) {
	attachments := Attachments{}
	ids := attachments.GetIds()
	assert.Empty(t, ids)
}
