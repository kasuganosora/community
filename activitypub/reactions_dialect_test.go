package activitypub

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func init() {
	// Package tests never probe real hosts for their reaction dialect.
	reactionTargetSoftware = func(string) string { return "" }
}

func TestOutboundReactionDialect(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}, &models.RemoteReactionActivity{}))
	local := outboundAccount(t, conn, "local", false)
	remote := outboundAccount(t, conn, "remote", true)
	uri := "https://remote.example/notes/dialect"
	status := &models.Status{AccountId: remote.Id, OriginUri: &uri, Content: models.StatusContent{Content: "hi"}}
	require.NoError(t, conn.Create(status).Error)
	emoji := &models.Emoji{Shortcode: "blobhaj", Url: "https://local.example/emoji/blobhaj.png", StaticUrl: "https://local.example/emoji/blobhaj.png", ContentType: "image/png"}
	require.NoError(t, conn.Create(emoji).Error)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, ":blobhaj:", emoji, "", false))
	ev := outboxEvents(t, conn)[0]
	old := reactionTargetSoftware
	defer func() { reactionTargetSoftware = old }()

	for software, typ := range map[string]string{softwareMisskey: "Like", "": "Like", softwarePleroma: "EmojiReact"} {
		reactionTargetSoftware = func(string) string { return software }
		act, _, err := reactionPayload(&ev, "https://local.example/users/local", "e1")
		require.NoError(t, err)
		require.NotNil(t, act)
		require.Equal(t, typ, act["type"])
		require.Equal(t, ":blobhaj:", act["content"])
		require.Equal(t, ":blobhaj:", act["_misskey_reaction"])
		tag := act["tag"].([]any)[0].(map[string]any)
		require.Equal(t, "Emoji", tag["type"])
		require.Equal(t, ":blobhaj:", tag["name"])
		require.Equal(t, emoji.Url, tag["icon"].(map[string]any)["url"])
		ok, err := reactionDeliveryCurrent(DeliveryAuthorization{Kind: "Reaction", AccountID: local.Id, StatusID: status.Id}, act)
		require.NoError(t, err)
		require.True(t, ok)
	}
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, ":blobhaj:", emoji, "", true))
	evs := outboxEvents(t, conn)
	reactionTargetSoftware = func(string) string { return softwarePleroma }
	undo, _, err := reactionPayload(&evs[len(evs)-1], "https://local.example/users/local", "e2")
	require.NoError(t, err)
	require.Equal(t, "Undo", undo["type"])
	require.Equal(t, "EmojiReact", undo["object"].(map[string]any)["type"])
}

func TestEmojiReactInbound(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.FavoriteStatus{}, &models.StatusReaction{}, &models.RemoteReactionActivity{}))
	author := outboundAccount(t, conn, "author", false)
	actor := outboundAccount(t, conn, "pleroma", true)
	status := &models.Status{AccountId: author.Id, Content: models.StatusContent{Content: "react"}}
	require.NoError(t, conn.Create(status).Error)
	uri := statusObjectURI(status, author, systemsettings.GetBaseURL())
	ctx := context.WithValue(context.Background(), actorKey, actor.OriginId)
	domain := *actor.Domain
	send := func(a map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		switch a["type"] {
		case "EmojiReact":
			handleEmojiReaction(ctx, c, a, false)
		default:
			handleUndoActivity(ctx, c, a, nil)
		}
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	count := func() int64 {
		var n int64
		require.NoError(t, conn.Model(&models.StatusReaction{}).Where("status_id = ?", status.Id).Count(&n).Error)
		return n
	}
	react := map[string]any{"id": "https://" + domain + "/activities/1", "type": "EmojiReact", "actor": actor.OriginId, "object": uri, "content": ":blobcat:",
		"tag": []any{map[string]any{"type": "Emoji", "name": ":blobcat:", "icon": map[string]any{"type": "Image", "url": "https://" + domain + "/emoji/blobcat.png"}}}}
	send(react)
	var row models.StatusReaction
	require.NoError(t, conn.Where("status_id = ?", status.Id).First(&row).Error)
	require.Equal(t, ":blobcat@"+domain+":", row.Name)
	send(map[string]any{"type": "Undo", "actor": actor.OriginId, "object": react})
	require.Zero(t, count())
	uni := map[string]any{"id": "https://" + domain + "/activities/2", "type": "EmojiReact", "actor": actor.OriginId, "object": uri, "content": "🔥"}
	send(uni)
	require.EqualValues(t, 1, count())
	send(map[string]any{"type": "Undo", "actor": actor.OriginId, "object": uni})
	require.Zero(t, count())
	require.True(t, strings.HasPrefix(row.Name, ":"))
}
