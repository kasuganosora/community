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
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}))
	local := outboundAccount(t, conn, "local", false)
	remote := outboundAccount(t, conn, "remote", true)
	uri := "https://remote.example/notes/reaction"
	status := &models.Status{AccountId: remote.Id, OriginUri: &uri, Content: models.StatusContent{Content: "hello"}}
	require.NoError(t, conn.Create(status).Error)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, "😀", nil, "", false))
	require.NoError(t, models.SetStatusReaction(conn, status.Id, local, "😀", nil, "", false))
	require.Len(t, outboxEvents(t, conn), 1)
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
	like, _, err := reactionPayload(&events[0], "https://local.example/users/local", "event1")
	require.NoError(t, err)
	require.Equal(t, "😀", like["_misskey_reaction"])
	undo, _, err := reactionPayload(&events[1], "https://local.example/users/local", "event2")
	require.NoError(t, err)
	require.Equal(t, like["id"], undo["object"].(map[string]any)["id"])
}

func TestReactionDirectAndBlockAuthorization(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}))
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
	require.NoError(t, conn.AutoMigrate(&models.RoleUser{}, &models.StatusReaction{}))
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
	require.NoError(t, conn.AutoMigrate(&models.FavoriteStatus{}, &models.StatusReaction{}))
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
