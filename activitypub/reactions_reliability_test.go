package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func reactionReliabilityDB(t *testing.T) (*gorm.DB, *models.Account, *models.Account, *models.Status) {
	t.Helper()
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}, &models.RemoteReactionActivity{}, &models.Notification{}, &ProcessedActivity{}))
	owner := outboundAccount(t, conn, "owner", false)
	actor := outboundAccount(t, conn, "reactor", true)
	status := &models.Status{AccountId: owner.Id, Content: models.StatusContent{Content: "reaction ordering"}}
	require.NoError(t, conn.Create(status).Error)
	return conn, owner, actor, status
}
func reactionCounts(t *testing.T, conn *gorm.DB, rows, notifications int64) {
	t.Helper()
	var count int64
	require.NoError(t, conn.Model(&models.StatusReaction{}).Count(&count).Error)
	require.Equal(t, rows, count)
	require.NoError(t, conn.Model(&models.Notification{}).Where("type = ?", models.NotificationTypeEmojiReaction).Count(&count).Error)
	require.Equal(t, notifications, count)
}
func TestReactionReliabilityUndoBeforeLike(t *testing.T) {
	conn, owner, actor, status := reactionReliabilityDB(t)
	like := map[string]any{"id": "https://remote.example/likes/early-undo", "type": "Like", "actor": actor.OriginId, "object": statusObjectURI(status, owner, systemsettings.GetBaseURL()), "content": "😀"}
	undo := map[string]any{"id": "https://remote.example/undo/early", "type": "Undo", "actor": actor.OriginId, "object": like}
	ctx := context.WithValue(context.Background(), actorKey, actor.OriginId)
	call := func(activity map[string]any, isUndo bool) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		processInboxTransaction(ctx, c, activity["id"].(string), func(ctx context.Context) { handleEmojiReaction(ctx, c, activity, isUndo) })
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	call(undo, true)
	call(like, false)
	call(undo, true)
	call(like, false)
	reactionCounts(t, conn, 0, 0)
	var saved models.RemoteReactionActivity
	require.NoError(t, conn.First(&saved).Error)
	require.True(t, saved.Undone)
	var count int64
	require.NoError(t, conn.Model(&ProcessedActivity{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
}
func TestReactionReliabilityReplacementAndOldReplay(t *testing.T) {
	conn, _, actor, status := reactionReliabilityDB(t)
	apply := func(name, id string, undo bool) {
		require.NoError(t, models.SetStatusReaction(conn, status.Id, actor, name, nil, "https://remote.example/likes/"+id, undo))
	}
	apply("😀", "old", false)
	apply("👍", "new", false)
	// Original Like replays must not revert a replacement, including after its Undo.
	apply("😀", "old", false)
	apply("😀", "old", true)
	apply("😀", "old", false)
	var current models.StatusReaction
	require.NoError(t, conn.First(&current).Error)
	require.Equal(t, "👍", current.Name)
	reactionCounts(t, conn, 1, 1)
	apply("👍", "new", true)
	apply("👍", "new", false)
	reactionCounts(t, conn, 0, 0)
	// A previously unknown old Undo must not delete a newer Like.
	apply("🔥", "third", false)
	apply("😀", "unknown-old", true)
	current = models.StatusReaction{}
	require.NoError(t, conn.First(&current).Error)
	require.Equal(t, "🔥", current.Name)
	apply("😀", "unknown-old", false)
	reactionCounts(t, conn, 1, 1)
}
func TestReactionReliabilitySameEmojiDifferentActivities(t *testing.T) {
	conn, _, actor, status := reactionReliabilityDB(t)
	oldURI, newURI := "https://remote.example/likes/same-old", "https://remote.example/likes/same-new"
	for _, uri := range []string{oldURI, newURI, oldURI} {
		require.NoError(t, models.SetStatusReaction(conn, status.Id, actor, "😀", nil, uri, false))
	}
	require.NoError(t, models.SetStatusReaction(conn, status.Id, actor, "😀", nil, oldURI, true))
	reactionCounts(t, conn, 1, 1)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, actor, "😀", nil, newURI, true))
	reactionCounts(t, conn, 0, 0)
}
func TestReactionReliabilityActorAndAuthorizationIsolation(t *testing.T) {
	conn, owner, actor, status := reactionReliabilityDB(t)
	other := outboundAccount(t, conn, "other", true)
	uri := "https://remote.example/likes/shared-id"
	require.NoError(t, models.SetStatusReaction(conn, status.Id, other, "😀", nil, uri, true))
	require.NoError(t, models.SetStatusReaction(conn, status.Id, actor, "😀", nil, uri, false))
	reactionCounts(t, conn, 1, 1)
	require.NoError(t, conn.Create(&models.Block{AccountId: owner.Id, TargetAccountId: actor.Id}).Error)
	require.ErrorIs(t, models.SetStatusReaction(conn, status.Id, actor, "🔥", nil, "https://remote.example/likes/blocked", true), models.ErrReactionForbidden)
	var n int64
	require.NoError(t, conn.Model(&models.RemoteReactionActivity{}).Where("activity_uri = ?", "https://remote.example/likes/blocked").Count(&n).Error)
	require.Zero(t, n)
}
func TestReactionReliabilityOuterRollbackAndRetry(t *testing.T) {
	conn, _, actor, status := reactionReliabilityDB(t)
	ctx, publish := models.BeginReactionNotifications(context.Background())
	calls := 0
	listener := event.Register("notification", "after_create", &event.Handler{Exec: func(e event.Event) bool { calls++; return true }})
	defer event.Remove("notification", "after_create", listener)
	aborted := errors.New("abort outer transaction")
	uri := "https://remote.example/likes/retry"
	err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		require.NoError(t, models.SetStatusReaction(tx, status.Id, actor, "😀", nil, uri, false))
		require.Zero(t, calls)
		return aborted
	})
	require.ErrorIs(t, err, aborted)
	_ = publish // The caller must not publish an aborted transaction.
	reactionCounts(t, conn, 0, 0)
	var n int64
	require.NoError(t, conn.Model(&models.RemoteReactionActivity{}).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, actor, "😀", nil, uri, false))
	require.Equal(t, 1, calls)
	reactionCounts(t, conn, 1, 1)
}
func TestReactionReliabilityNestedFailurePreservesNotificationBuffer(t *testing.T) {
	conn, _, actor, status := reactionReliabilityDB(t)
	ctx, publish := models.BeginReactionNotifications(context.Background())
	var notifications []uint64
	listener := event.Register("notification", "after_create", &event.Handler{Exec: func(e event.Event) bool {
		notifications = append(notifications, e.Data.(*models.Notification).Id)
		return true
	}})
	defer event.Remove("notification", "after_create", listener)
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("fail_reaction_replacement", func(tx *gorm.DB) {
		if r, ok := tx.Statement.Dest.(*models.StatusReaction); ok && r.Name == "🔥" {
			tx.AddError(errors.New("injected row failure"))
		}
	}))
	require.NoError(t, conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		require.NoError(t, models.SetStatusReaction(tx, status.Id, actor, "😀", nil, "https://remote.example/likes/kept", false))
		require.Error(t, models.SetStatusReaction(tx, status.Id, actor, "🔥", nil, "https://remote.example/likes/failed", false))
		require.Empty(t, notifications)
		return nil
	}))
	publish()
	require.Len(t, notifications, 1)
	var n models.Notification
	require.NoError(t, conn.First(&n, notifications[0]).Error)
	reactionCounts(t, conn, 1, 1)
	var count int64
	require.NoError(t, conn.Model(&models.RemoteReactionActivity{}).Where("activity_uri = ?", "https://remote.example/likes/failed").Count(&count).Error)
	require.Zero(t, count)
}
func TestReactionReliabilitySingleCommitOnlyPublishesFinalNotification(t *testing.T) {
	conn, _, actor, status := reactionReliabilityDB(t)
	ctx, publish := models.BeginReactionNotifications(context.Background())
	var notifications []uint64
	listener := event.Register("notification", "after_create", &event.Handler{Exec: func(e event.Event) bool {
		notifications = append(notifications, e.Data.(*models.Notification).Id)
		return true
	}})
	defer event.Remove("notification", "after_create", listener)
	require.NoError(t, conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		require.NoError(t, models.SetStatusReaction(tx, status.Id, actor, "😀", nil, "https://remote.example/likes/first", false))
		require.NoError(t, models.SetStatusReaction(tx, status.Id, actor, "👍", nil, "https://remote.example/likes/final", false))
		return nil
	}))
	publish()
	require.Len(t, notifications, 1)
	var n models.Notification
	require.NoError(t, conn.First(&n, notifications[0]).Error)
	reactionCounts(t, conn, 1, 1)
}

func TestReactionReliabilityQueuedLikesRecheckCurrentState(t *testing.T) {
	conn, owner, actor, status := reactionReliabilityDB(t)
	// Local actor reacts to a remote object; construct an already-expanded queue
	// entry so this exercises send-time authorization, not only outbox expansion.
	uri := "https://remote.example/notes/queued"
	remoteStatus := &models.Status{AccountId: actor.Id, OriginUri: &uri, Content: models.StatusContent{Content: "queued"}}
	require.NoError(t, conn.Create(remoteStatus).Error)
	require.NoError(t, models.SetStatusReaction(conn, remoteStatus.Id, owner, "😀", nil, "", false))
	var e models.FederationOutbox
	require.NoError(t, conn.Where("kind = ?", "Reaction").Last(&e).Error)
	activity, _, err := reactionPayload(&e, accountActorURI(owner, systemsettings.GetBaseURL()), "queued-like")
	require.NoError(t, err)
	require.NotNil(t, activity)
	auth := DeliveryAuthorization{AccountID: owner.Id, StatusID: remoteStatus.Id, RecipientIDs: []uint64{actor.Id}, Kind: "Reaction", Visibility: remoteStatus.Visibility}
	data, err := json.Marshal(auth)
	require.NoError(t, err)
	delivery := &Delivery{Authorization: string(data)}
	_, allowed, err := authorizeQueuedDelivery(delivery, activity)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, models.SetStatusReaction(conn, remoteStatus.Id, owner, "👍", nil, "", false))
	_, allowed, err = authorizeQueuedDelivery(delivery, activity)
	require.NoError(t, err)
	require.False(t, allowed, "replaced queued Like must not be sent")
	e = models.FederationOutbox{}
	require.NoError(t, conn.Where("kind = ?", "Reaction").Last(&e).Error)
	next, _, err := reactionPayload(&e, accountActorURI(owner, systemsettings.GetBaseURL()), "next-like")
	require.NoError(t, err)
	require.NotNil(t, next)
	_, allowed, err = authorizeQueuedDelivery(delivery, next)
	require.NoError(t, err)
	require.True(t, allowed, "current replacement must remain deliverable")
	require.NoError(t, models.SetStatusReaction(conn, remoteStatus.Id, owner, "👍", nil, "", true))
	_, allowed, err = authorizeQueuedDelivery(delivery, next)
	require.NoError(t, err)
	require.False(t, allowed, "revoked queued Like must not resurrect remotely")
	auth.Kind = "ReactionUndo"
	data, err = json.Marshal(auth)
	require.NoError(t, err)
	delivery.Authorization = string(data)
	_, allowed, err = authorizeQueuedDelivery(delivery, map[string]any{"type": "Undo"})
	require.NoError(t, err)
	require.True(t, allowed)
	_ = status
}

func TestReactionReliabilityUndoSurvivesLocalOnlyPolicyChange(t *testing.T) {
	conn, owner, remote, _ := reactionReliabilityDB(t)
	uri := "https://remote.example/notes/policy"
	status := &models.Status{AccountId: remote.Id, OriginUri: &uri, Content: models.StatusContent{Content: "policy"}}
	require.NoError(t, conn.Create(status).Error)
	emoji := &models.Emoji{Shortcode: "policy", Url: "https://local.example/policy.png", StaticUrl: "https://local.example/policy.png"}
	require.NoError(t, conn.Create(emoji).Error)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, owner, ":policy:", emoji, "", false))
	require.NoError(t, conn.Model(emoji).UpdateColumn("local_only", true).Error)
	require.NoError(t, models.SetStatusReaction(conn, status.Id, owner, ":policy:", nil, "", true))
	var undo models.FederationOutbox
	require.NoError(t, conn.Where("kind = ?", "ReactionUndo").Last(&undo).Error)
	require.Equal(t, []uint64{remote.Id}, undo.RecipientIDs)
	// By contrast, local-only from creation never generates any remote event.
	localStatus := &models.Status{AccountId: owner.Id, Content: models.StatusContent{Content: "local only"}}
	require.NoError(t, conn.Create(localStatus).Error)
	require.NoError(t, models.SetStatusReaction(conn, localStatus.Id, owner, ":policy:", emoji, "", false))
	require.NoError(t, models.SetStatusReaction(conn, localStatus.Id, owner, ":policy:", nil, "", true))
	var n int64
	require.NoError(t, conn.Model(&models.FederationOutbox{}).Where("object_id = ? AND kind IN ?", localStatus.Id, []string{"Reaction", "ReactionUndo"}).Count(&n).Error)
	require.Zero(t, n)
}

func TestReactionReliabilityInboxCommitFailureRetainsRetryability(t *testing.T) {
	conn, owner, actor, status := reactionReliabilityDB(t)
	like := map[string]any{"id": "https://remote.example/likes/inbox-rollback", "type": "Like", "actor": actor.OriginId, "object": statusObjectURI(status, owner, systemsettings.GetBaseURL()), "content": "😀"}
	ctx := context.WithValue(context.Background(), actorKey, actor.OriginId)
	broadcasts := 0
	listener := event.Register("notification", "after_create", &event.Handler{Exec: func(e event.Event) bool { broadcasts++; return true }})
	defer event.Remove("notification", "after_create", listener)
	call := func(fail bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		processInboxTransaction(ctx, c, like["id"].(string), func(ctx context.Context) {
			handleEmojiReaction(ctx, c, like, false)
			if fail {
				c.JSON(503, gin.H{"error": "injected handler failure"})
			}
		})
		return w
	}
	require.Equal(t, 503, call(true).Code)
	require.Zero(t, broadcasts)
	reactionCounts(t, conn, 0, 0)
	var n int64
	require.NoError(t, conn.Model(&models.RemoteReactionActivity{}).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, conn.Model(&ProcessedActivity{}).Count(&n).Error)
	require.Zero(t, n)
	w := call(false)
	require.Equal(t, 200, w.Code, w.Body.String())
	reactionCounts(t, conn, 1, 1)
	require.Equal(t, 1, broadcasts)
	w = call(false)
	require.Equal(t, 200, w.Code, w.Body.String())
	reactionCounts(t, conn, 1, 1)
	require.Equal(t, 1, broadcasts, "HTTP replay must not duplicate notification broadcast")
}
