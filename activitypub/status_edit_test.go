package activitypub

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestEditTime(t *testing.T) {
	now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	obj := map[string]any{"published": "2026-10-09T03:00:00Z", "updated": "2026-10-09T03:30:00Z"}
	require.Equal(t, time.Date(2026, 10, 9, 3, 30, 0, 0, time.UTC), editTime(obj, now).UTC())
	// Missing, not after published, or far in the future: use the receive time.
	require.Equal(t, now, editTime(map[string]any{}, now))
	require.Equal(t, now, editTime(map[string]any{"published": "2026-10-09T03:00:00Z", "updated": "2026-10-09T03:00:00Z"}, now))
	require.Equal(t, now, editTime(map[string]any{"updated": "2027-01-01T00:00:00Z"}, now))
}

func TestRemoteNoteUpdateRecordsHistoryOnlyForRealEdits(t *testing.T) {
	conn := emojiDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusHistory{}))
	actorURI := "https://remote.example/users/editor"
	author, err := createRemoteAccount(conn, &Actor{ID: actorURI, PreferredUsername: "editor"})
	require.NoError(t, err)
	domain := "remote.example"
	uri := "https://remote.example/notes/edit"
	created := time.Now().Add(-time.Hour)
	status := models.Status{AccountId: author.Id, Domain: &domain, OriginUri: &uri, Content: models.StatusContent{Content: "<p>Old</p>"}, CreatedAt: &created}
	require.NoError(t, persistIncomingStatusWithEmoji(context.Background(), conn, &status, nil))

	update := func(content string) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ctx := context.WithValue(context.WithValue(context.Background(), actorKey, actorURI), inboxTransactionKey{}, conn)
		handleUpdateActivity(ctx, c, map[string]any{"object": map[string]any{"id": uri, "type": "Note", "content": content}}, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	count := func() int64 {
		var n int64
		require.NoError(t, conn.Model(&models.StatusHistory{}).Where("status_id = ?", status.Id).Count(&n).Error)
		return n
	}
	update("<p>Old</p>") // e.g. a counter refresh: same text
	require.EqualValues(t, 0, count())
	update("<p>New</p>")
	require.EqualValues(t, 1, count())

	statuses := []*models.Status{{Id: status.Id}, {Id: status.Id + 1000}}
	models.AttachEditFlags(conn, statuses)
	require.True(t, statuses[0].Edited)
	require.False(t, statuses[1].Edited)
}
