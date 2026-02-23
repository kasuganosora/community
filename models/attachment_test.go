package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAttachment_TableName(t *testing.T) {
	a := Attachment{}
	assert.Equal(t, "attachments", a.TableName())
}

func TestAttachment_GetTypeString(t *testing.T) {
	tests := []struct {
		name     string
		aType    uint
		expected string
	}{
		{"image", AttachmentTypeImage, "image"},
		{"video", AttachmentTypeVideo, "video"},
		{"audio", AttachmentTypeAudio, "audio"},
		{"file", AttachmentTypeFile, "file"},
		{"gifv", AttachmentTypeGifv, "gifv"},
		{"unknown defaults to file", AttachmentTypeUnknown, "file"},
		{"invalid defaults to file", 99, "file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Attachment{Type: tt.aType}
			assert.Equal(t, tt.expected, a.GetTypeString())
		})
	}
}

func TestAttachment_BeforeSave(t *testing.T) {
	a := &Attachment{
		FileSize:        1024,
		PreviewFileSize: 256,
	}
	err := a.BeforeSave(nil)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1280), a.TotalSize)
}

func TestAttachment_BeforeSave_ZeroSizes(t *testing.T) {
	a := &Attachment{}
	err := a.BeforeSave(nil)
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), a.TotalSize)
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
	assert.Len(t, ids, 0)
}

func TestAttachmentTypeConstants(t *testing.T) {
	assert.Equal(t, uint(0), uint(AttachmentTypeUnknown))
	assert.Equal(t, uint(1), uint(AttachmentTypeImage))
	assert.Equal(t, uint(2), uint(AttachmentTypeVideo))
	assert.Equal(t, uint(3), uint(AttachmentTypeAudio))
	assert.Equal(t, uint(4), uint(AttachmentTypeFile))
	assert.Equal(t, uint(5), uint(AttachmentTypeGifv))
}
