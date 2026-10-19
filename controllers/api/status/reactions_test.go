package status

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/require"
)

func TestReactionAPIVisibilityIdempotenceAndList(t *testing.T) {
	conn, fix, alice := replacementDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}))
	bob := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "react_bob" }))
	outsider := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "react_outsider" }))
	s := fix.CreateStatus(alice, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityDirect
		s.Content.Content = "private reactions"
	})
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: s.Id, AccountId: bob.Id}).Error)
	call := func(viewer *models.Account, name string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(s.Id, 10)}, {Key: "name", Value: name}}
		if viewer != nil {
			c.Set("account", viewer)
		}
		safeHandler(c, handler)
		return w
	}
	require.Equal(t, 404, call(outsider, "😀", AddReaction).Code)
	require.Equal(t, 404, call(nil, "", Reactions).Code)
	for i := 0; i < 2; i++ {
		w := call(bob, "😀", AddReaction)
		require.Equal(t, 200, w.Code, w.Body.String())
		var status map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
		rs := status["emoji_reactions"].([]any)
		require.Len(t, rs, 1)
		require.Equal(t, true, rs[0].(map[string]any)["me"])
		require.EqualValues(t, 1, rs[0].(map[string]any)["count"])
	}
	w := call(alice, "", Reactions)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), bob.Username)
	require.Equal(t, 422, call(bob, "<script>", AddReaction).Code)
	require.Equal(t, 422, call(bob, ":not_installed:", AddReaction).Code)
	for i := 0; i < 2; i++ {
		w = call(bob, "😀", RemoveReaction)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", s.Id).UpdateColumn("visibility", models.StatusVisibilityPublic).Error)
	require.Equal(t, 200, call(nil, "", Reactions).Code)
}

func TestReactionNotificationsReplaceRemoveAndSelf(t *testing.T) {
	conn, fix, alice := replacementDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}))
	bob := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "notify_reactor" }))
	s := fix.CreateStatus(alice)
	require.NoError(t, models.SetStatusReaction(conn, s.Id, bob, "😀", nil, "", false))
	var ns []models.Notification
	require.NoError(t, conn.Where("type = ?", models.NotificationTypeEmojiReaction).Find(&ns).Error)
	require.Len(t, ns, 1)
	require.Equal(t, alice.Id, ns[0].AccountId)
	serialized, err := serializer.ToNotification(&ns[0], alice)
	require.NoError(t, err)
	require.Equal(t, "emoji_reaction", serialized.Type)
	require.Equal(t, "😀", serialized.EmojiReaction.Name)
	first := ns[0].Id
	require.NoError(t, models.SetStatusReaction(conn, s.Id, bob, "😀", nil, "", false))
	require.NoError(t, models.SetStatusReaction(conn, s.Id, bob, "👍", nil, "", false))
	ns = nil
	require.NoError(t, conn.Where("type = ?", models.NotificationTypeEmojiReaction).Find(&ns).Error)
	require.Len(t, ns, 1)
	require.Greater(t, ns[0].Id, first)
	require.NoError(t, models.SetStatusReaction(conn, s.Id, bob, "👍", nil, "", true))
	require.NoError(t, models.SetStatusReaction(conn, s.Id, alice, "😀", nil, "", false))
	var count int64
	require.NoError(t, conn.Model(&models.Notification{}).Where("type = ?", models.NotificationTypeEmojiReaction).Count(&count).Error)
	require.Zero(t, count)
}

func TestReactionReplacementCleansRowsAndRollsBackTogether(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}))
	actor := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "replacement_reactor" }))
	original := fix.CreateStatus(owner)
	require.NoError(t, models.SetStatusReaction(conn, original.Id, actor, "😀", nil, "", false))
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("reaction_test_fail_outbox", func(tx *gorm.DB) {
		if e, ok := tx.Statement.Dest.(*models.FederationOutbox); ok && e.Kind == "Create" {
			tx.AddError(errors.New("injected replacement failure"))
		}
	}))
	w := callReplacement(t, owner, original.Id, map[string]any{"status": "must roll back"})
	require.Equal(t, 500, w.Code, w.Body.String())
	var count int64
	require.NoError(t, conn.Model(&models.StatusReaction{}).Where("status_id = ?", original.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Model(&models.Notification{}).Where("type = ? AND status_id = ?", models.NotificationTypeEmojiReaction, original.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Callback().Create().Remove("reaction_test_fail_outbox"))
	w = callReplacement(t, owner, original.Id, map[string]any{"status": "replacement"})
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, conn.Model(&models.StatusReaction{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, conn.Model(&models.Notification{}).Where("type = ?", models.NotificationTypeEmojiReaction).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, conn.Model(&models.FederationOutbox{}).Where("kind = ?", "ReactionUndo").Count(&count).Error)
	require.Zero(t, count, "the parent Delete already revokes the remote reactions")
}

func TestAuthorReactionPreferencesAreEnforcedAndUndoRemainsAvailable(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}, &models.RemoteReactionActivity{}))
	local := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "policy_local" }))
	remote := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "policy_remote" }))
	domain := "remote.example"
	remote.Domain = &domain
	remote.OriginId = "https://remote.example/users/policy_remote"
	require.NoError(t, conn.Save(remote).Error)
	post := fix.CreateStatus(owner)
	sensitive := &models.Emoji{Shortcode: "sensitive", Url: "https://local.example/sensitive.png", StaticUrl: "https://local.example/sensitive.png", IsSensitive: true}
	require.NoError(t, conn.Create(sensitive).Error)
	require.NoError(t, models.SetStatusReaction(conn, post.Id, local, ":sensitive:", sensitive, "", false))
	require.NoError(t, conn.Model(owner).UpdateColumn("reaction_acceptance", "nonSensitiveOnly").Error)
	require.ErrorIs(t, models.SetStatusReaction(conn, post.Id, local, ":sensitive:", sensitive, "", false), models.ErrReactionForbidden)
	require.NoError(t, models.SetStatusReaction(conn, post.Id, local, ":sensitive:", nil, "", true))
	require.NoError(t, models.SetStatusReaction(conn, post.Id, local, "😀", nil, "", false))
	require.NoError(t, conn.Model(owner).UpdateColumn("reaction_acceptance", "likeOnlyForRemote").Error)
	require.ErrorIs(t, models.SetStatusReaction(conn, post.Id, remote, "😀", nil, "", false), models.ErrReactionForbidden)
	require.NoError(t, models.SetStatusReaction(conn, post.Id, remote, "❤️", nil, "https://remote.example/likes/1", false))
	require.NoError(t, models.SetStatusReaction(conn, post.Id, local, ":sensitive:", sensitive, "", false))
	require.NoError(t, conn.Model(owner).UpdateColumn("reaction_acceptance", "likeOnly").Error)
	require.ErrorIs(t, models.SetStatusReaction(conn, post.Id, local, "😀", nil, "", false), models.ErrReactionForbidden)
	require.NoError(t, models.SetStatusReaction(conn, post.Id, local, ":sensitive:", nil, "", true))
	var count int64
	require.NoError(t, conn.Model(&models.StatusReaction{}).Where("account_id = ? AND status_id = ?", local.Id, post.Id).Count(&count).Error)
	require.Zero(t, count)
}
