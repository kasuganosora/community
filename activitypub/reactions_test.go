package activitypub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestReactionAtomicReplacementAndUndo(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}, &models.RemoteReactionActivity{}))
	local := outboundAccount(t, conn, "local", false)
	remote := outboundAccount(t, conn, "remote", true)
	uri := "https://remote.example/notes/reaction"
	status := &models.Status{AccountId: remote.Id, OriginUri: &uri, Content: models.StatusContent{Content: "hello"}}
	require.NoError(t, conn.Create(status).Error)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, "😀", nil, "", false))
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, "😀", nil, "", false))
	require.Len(t, outboxEvents(t, conn), 1)
	initial := outboxEvents(t, conn)[0]
	like, _, err := reactionPayload(&initial, "https://local.example/users/local", "event1")
	require.NoError(t, err)
	require.Equal(t, "😀", like["_misskey_reaction"])
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, "👍", nil, "", false))
	events := outboxEvents(t, conn)
	require.Len(t, events, 3)
	require.Equal(t, "ReactionUndo", events[1].Kind)
	var rows []models.StatusReaction
	require.NoError(t, conn.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "👍", rows[0].Name)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, "😀", nil, "", true))
	require.Len(t, outboxEvents(t, conn), 3)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, "👍", nil, "", true))
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, "👍", nil, "", true))
	require.Len(t, outboxEvents(t, conn), 4)
	var count int64
	require.NoError(t, conn.Model(&models.StatusReaction{}).Count(&count).Error)
	require.Zero(t, count)
	var refreshed models.Status
	require.NoError(t, conn.First(&refreshed, status.Id).Error)
	require.Zero(t, refreshed.FavouritesCount)
	// Wire Like/Undo use the same stable original ID and Misskey metadata.
	stale, _, err := reactionPayload(&events[0], "https://local.example/users/local", "event1")
	require.NoError(t, err)
	require.Nil(t, stale)
	undo, _, err := reactionPayload(&events[1], "https://local.example/users/local", "event2")
	require.NoError(t, err)
	require.Equal(t, like["id"], undo["object"].(map[string]any)["id"])
}

func TestReactionDirectAndBlockAuthorization(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}, &models.RemoteReactionActivity{}))
	owner := outboundAccount(t, conn, "owner", false)
	allowed := outboundAccount(t, conn, "allowed", true)
	outsider := outboundAccount(t, conn, "outsider", true)
	status := &models.Status{AccountId: owner.Id, Visibility: models.StatusVisibilityDirect, Content: models.StatusContent{Content: "private"}}
	require.NoError(t, conn.Create(status).Error)
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: allowed.Id}).Error)
	require.ErrorIs(t, models.SetStatusReaction(conn, status.Id, outsider, "😀", nil, "", false), models.ErrReactionForbidden)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, allowed, "😀", nil, "https://remote.example/likes/one", false))
	// An old or forged undo cannot delete the current reaction.
	require.NoError(t, models.SetStatusReaction(conn, status.Id, allowed, "😀", nil, "https://remote.example/likes/forged", true))
	var count int64
	require.NoError(t, conn.Model(&models.StatusReaction{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Create(&models.Block{AccountId: owner.Id, TargetAccountId: allowed.Id}).Error)
	require.ErrorIs(t, models.SetStatusReaction(conn, status.Id, allowed, "👍", nil, "", false), models.ErrReactionForbidden)
}

func TestReactionInboundUndoRejectsActorMismatch(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx := context.WithValue(context.Background(), actorKey, "https://attacker.example/a")
	handleEmojiReaction(ctx, c, map[string]any{"object": map[string]any{"type": "Like", "actor": "https://victim.example/a", "content": "😀"}}, true)
	require.Equal(t, 403, w.Code)
}
func TestReactionUnicodeAndCustomPolicy(t *testing.T) {
	for _, s := range []string{"😀", "❤️", "👍🏽", "👨‍👩‍👧‍👦", "1️⃣"} {
		require.True(t, models.ValidUnicodeReaction(s), s)
	}
	for _, s := range []string{"", "hello", "<script>", "\u200d", "😀 text", ":emoji:", "a"} {
		require.False(t, models.ValidUnicodeReaction(s), s)
	}
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.RoleUser{}, &models.StatusReaction{}, &models.RemoteReactionActivity{}))
	local := outboundAccount(t, conn, "local", false)
	remote := outboundAccount(t, conn, "remote", true)
	emoji := models.Emoji{Shortcode: "restricted", Url: "https://local.example/e.png", StaticUrl: "https://local.example/e.png", ReactionRoleIDs: []uint64{9}}
	require.NoError(t, conn.Create(&emoji).Error)
	_, err := models.ResolveReaction(conn, ":restricted:", local)
	require.ErrorIs(t, err, models.ErrInvalidReaction)
	require.NoError(t, conn.Create(&models.RoleUser{UserID: local.UserId, RoleID: 9}).Error)
	got, err := models.ResolveReaction(conn, ":restricted:", local)
	require.NoError(t, err)
	require.Equal(t, emoji.Id, got.Id)
	_, err = models.ResolveReaction(conn, ":restricted:", remote)
	require.ErrorIs(t, err, models.ErrInvalidReaction)
	_, err = models.ResolveReaction(conn, ":restricted@remote.example:", local)
	require.ErrorIs(t, err, models.ErrInvalidReaction)
	raw, err := json.Marshal(models.ReactionSnapshot{Emoji: &emoji})
	require.NoError(t, err)
	require.Contains(t, string(raw), "restricted")
}

func TestReactionPlainLikeKeepsFavouriteCounterAccurate(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.FavoriteStatus{}, &models.StatusReaction{}, &models.RemoteReactionActivity{}))
	author := outboundAccount(t, conn, "author", false)
	actor := outboundAccount(t, conn, "reactor", true)
	status := &models.Status{AccountId: author.Id, Content: models.StatusContent{Content: "favourite"}}
	require.NoError(t, conn.Create(status).Error)
	// Use the exact canonical local object URI, never an arbitrary numeric ID.
	uri := statusObjectURI(status, author, systemsettings.GetBaseURL())
	like := map[string]any{"id": "https://remote.example/likes/fav", "type": "Like", "actor": actor.OriginId, "object": uri}
	ctx := context.WithValue(context.Background(), actorKey, actor.OriginId)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		handleLikeActivity(ctx, c, like, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	var loaded models.Status
	require.NoError(t, conn.First(&loaded, status.Id).Error)
	require.EqualValues(t, 1, loaded.FavouritesCount)
	undo := map[string]any{"type": "Undo", "actor": actor.OriginId, "object": like}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		handleUndoActivity(ctx, c, undo, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	require.NoError(t, conn.First(&loaded, status.Id).Error)
	require.Zero(t, loaded.FavouritesCount)
}

func TestMisskeyLikeReactionsInbound(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.FavoriteStatus{}, &models.StatusReaction{}, &models.RemoteReactionActivity{}))
	author := outboundAccount(t, conn, "author", false)
	actor := outboundAccount(t, conn, "reactor", true)
	status := &models.Status{AccountId: author.Id, Content: models.StatusContent{Content: "react"}}
	require.NoError(t, conn.Create(status).Error)
	uri := statusObjectURI(status, author, systemsettings.GetBaseURL())
	ctx := context.WithValue(context.Background(), actorKey, actor.OriginId)
	domain := *actor.Domain
	send := func(fn func(context.Context, *gin.Context, map[string]any, *models.Account), a map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		fn(ctx, c, a, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	reactions := func() []models.StatusReaction {
		var rows []models.StatusReaction
		require.NoError(t, conn.Where("status_id = ?", status.Id).Find(&rows).Error)
		return rows
	}
	favs := func() int64 {
		var n int64
		require.NoError(t, conn.Model(&models.FavoriteStatus{}).Where("status_id = ?", status.Id).Count(&n).Error)
		return n
	}
	likeID := func(s string) string { return "https://" + domain + "/likes/" + s }

	// Sender-local custom emoji with Emoji tag: stored as a reaction, not a favourite.
	cool := map[string]any{"id": likeID("1"), "type": "Like", "actor": actor.OriginId, "object": uri, "content": ":cool:", "_misskey_reaction": ":cool:",
		"tag": []any{map[string]any{"type": "Emoji", "name": ":cool:", "icon": map[string]any{"type": "Image", "url": "https://" + domain + "/files/cool.png", "mediaType": "image/png"}}}}
	send(handleLikeActivity, cool)
	rows := reactions()
	require.Len(t, rows, 1)
	require.Equal(t, ":cool@"+domain+":", rows[0].Name)
	require.Zero(t, favs())
	var e models.Emoji
	require.NoError(t, conn.Where("shortcode = ? AND domain = ?", "cool", domain).First(&e).Error)
	require.Equal(t, "https://"+domain+"/files/cool.png", e.Url)
	send(handleUndoActivity, map[string]any{"type": "Undo", "actor": actor.OriginId, "object": cool})
	require.Empty(t, reactions())

	// Emoji cached by the sender from a third host, without tag: Misskey proxy fallback.
	foreign := map[string]any{"id": likeID("2"), "type": "Like", "actor": actor.OriginId, "object": uri, "_misskey_reaction": ":blob@other.example:"}
	send(handleLikeActivity, foreign)
	rows = reactions()
	require.Len(t, rows, 1)
	require.Equal(t, ":blob@other.example:", rows[0].Name)
	e = models.Emoji{}
	require.NoError(t, conn.Where("shortcode = ? AND domain = ?", "blob", "other.example").First(&e).Error)
	require.Equal(t, "https://"+domain+"/emoji/blob@other.example.webp", e.Url)
	send(handleUndoActivity, map[string]any{"type": "Undo", "actor": actor.OriginId, "object": foreign})
	require.Empty(t, reactions())

	// An existing foreign emoji row is never overwritten by another actor's tag.
	evil := map[string]any{"id": likeID("3"), "type": "Like", "actor": actor.OriginId, "object": uri, "content": ":blob@other.example:",
		"tag": []any{map[string]any{"type": "Emoji", "name": ":blob:", "icon": map[string]any{"url": "https://evil.example/x.png"}}}}
	send(handleLikeActivity, evil)
	e = models.Emoji{}
	require.NoError(t, conn.Where("shortcode = ? AND domain = ?", "blob", "other.example").First(&e).Error)
	require.Equal(t, "https://"+domain+"/emoji/blob@other.example.webp", e.Url)
	send(handleUndoActivity, map[string]any{"type": "Undo", "actor": actor.OriginId, "object": evil})

	// Unicode reaction.
	thumbs := map[string]any{"id": likeID("4"), "type": "Like", "actor": actor.OriginId, "object": uri, "content": "👍"}
	send(handleLikeActivity, thumbs)
	rows = reactions()
	require.Len(t, rows, 1)
	require.Equal(t, "👍", rows[0].Name)
	send(handleUndoActivity, map[string]any{"type": "Undo", "actor": actor.OriginId, "object": thumbs})
	require.Empty(t, reactions())

	// Default heart stays a plain favourite.
	heart := map[string]any{"id": likeID("5"), "type": "Like", "actor": actor.OriginId, "object": uri, "_misskey_reaction": "❤"}
	send(handleLikeActivity, heart)
	require.Empty(t, reactions())
	require.EqualValues(t, 1, favs())
	send(handleUndoActivity, map[string]any{"type": "Undo", "actor": actor.OriginId, "object": heart})
	require.Zero(t, favs())
}
