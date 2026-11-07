package activitypub

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestNoteUsesFinalMediaMetadata(t *testing.T) {
	width, height := 12, 8
	duration := 1.5
	status := &models.Status{Id: 3, Attachments: models.Attachments{{
		Id: 9, FileName: "clip.webm", ContentType: "video/webm", Type: models.MediaTypeVideo,
		Width: &width, Height: &height, Duration: &duration, ProcessState: models.ProcessReady,
	}}}
	note := buildNoteFromStatus(status, &models.Account{Id: 1, Username: "ada"}, "https://example.test")
	attachments, ok := note["attachment"].([]map[string]any)
	require.True(t, ok)
	require.Equal(t, "video/webm", attachments[0]["mediaType"])
	require.Equal(t, 12, attachments[0]["width"])
	require.Equal(t, 8, attachments[0]["height"])
	require.Equal(t, 1.5, attachments[0]["duration"])
	require.Contains(t, attachments[0]["url"], "/media/9/clip.webm")
}
