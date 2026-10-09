package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMediaStorageKey(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"2026/10/09/a.jpg":         "media/2026/10/09/a.jpg",
		"imports/uuid":             "media/imports/uuid",
		"uploads/media/2026/a.jpg": "media/2026/a.jpg",
		"refreshed/1_a.jpg":        "refreshed/1_a.jpg",
		"https://acct.r2.cloudflarestorage.com/community-file/media/2026/10/09/a.jpg": "media/2026/10/09/a.jpg",
		"https://community-file.acct.r2.cloudflarestorage.com/media/2026/10/09/a.jpg": "media/2026/10/09/a.jpg",
	}
	for in, want := range cases {
		assert.Equal(t, want, MediaStorageKey(in), in)
	}

	thumb := "thumb_a.jpg"
	m := &MediaAttachment{FilePath: "https://acct.r2.cloudflarestorage.com/community-file/media/2026/10/09/a.jpg", ThumbnailPath: &thumb}
	assert.Equal(t, "media/2026/10/09/thumb_a.jpg", m.ThumbnailStorageKey())
	assert.Equal(t, "2026/10/09/a.jpg", NormalizeMediaFilePath(m.FilePath))
	assert.Empty(t, (&MediaAttachment{}).ThumbnailStorageKey())
}
