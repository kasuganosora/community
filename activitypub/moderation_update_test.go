package activitypub

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/stretchr/testify/require"
)

func TestRemoteNoteUpdateAppliesWordPolicy(t *testing.T) {
	conn := emojiDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusHistory{}))
	restore := moderation.SetForTesting(moderation.Snapshot{SensitiveWords: []string{"spoiler"}, ProhibitedWords: []string{"/casino/i"}})
	defer restore()

	actorURI := "https://remote.example/users/editor2"
	author, err := createRemoteAccount(conn, &Actor{ID: actorURI, PreferredUsername: "editor2"})
	require.NoError(t, err)
	domain := "remote.example"
	uri := "https://remote.example/notes/policy"
	created := time.Now().Add(-time.Hour)
	status := models.Status{AccountId: author.Id, Domain: &domain, OriginUri: &uri, Visibility: models.StatusVisibilityPublic,
		Content: models.StatusContent{Content: "<p>Hello</p>"}, CreatedAt: &created}
	require.NoError(t, persistIncomingStatusWithEmoji(context.Background(), conn, &status, nil))

	update := func(content string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ctx := context.WithValue(context.WithValue(context.Background(), actorKey, actorURI), inboxTransactionKey{}, conn)
		handleUpdateActivity(ctx, c, map[string]any{"object": map[string]any{"id": uri, "type": "Note", "content": content}}, nil)
		c.Writer.WriteHeaderNow()
		return w.Code
	}
	reload := func() models.Status {
		var s models.Status
		require.NoError(t, conn.Preload("Content").First(&s, status.Id).Error)
		return s
	}

	require.Equal(t, 202, update("<p>Visit CASINO now</p>"))
	got := reload()
	require.Equal(t, "<p>Hello</p>", got.Content.Content, "edit with prohibited words is dropped")
	require.EqualValues(t, models.StatusVisibilityPublic, got.Visibility)

	require.Equal(t, 200, update("<p>big spoiler here</p>"))
	got = reload()
	require.Equal(t, "<p>big spoiler here</p>", got.Content.Content)
	require.NotEqualValues(t, models.StatusVisibilityPublic, got.Visibility, "sensitive edit downgrades public")
}
