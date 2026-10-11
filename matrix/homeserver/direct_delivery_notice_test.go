package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"gorm.io/gorm"
	"testing"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestDirectDeliveryNoticePrivateOnceAndRecovery(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}, &activitypub.Delivery{}, &MatrixAccountData{}))
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "SECRET_MESSAGE"})
	require.NoError(t, conn.Model(&MatrixDMOutbound{}).Where("event_id = ?", sent.EventID).Update("state", "failed").Error)
	hs.publishDirectDeliveryFeedback(context.Background())
	var notices []MatrixEvent
	require.NoError(t, conn.Where("room_id <> ? AND event_type = ?", source.RoomID, EventTypeRoomMessage).Find(&notices).Error)
	require.Len(t, notices, 1)
	notice := notices[0]
	require.Equal(t, "m.notice", notice.Content["msgtype"])
	require.Contains(t, notice.Content["body"], "matrix.to")
	require.NotContains(t, notice.Content["body"], "SECRET_MESSAGE")
	var members []MatrixRoomMember
	require.NoError(t, conn.Where("room_id = ? AND membership = ?", notice.RoomID, MembershipJoin).Find(&members).Error)
	require.Len(t, members, 2)
	for _, m := range members {
		require.True(t, m.UserID == "@alice:test.local" || m.UserID == notice.Sender)
	}
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	require.Equal(t, "failed", job.DeliveryNoticeState)
	// Crash between event commit and checkpoint retry reuses the exact event.
	require.NoError(t, conn.Model(&job).Updates(map[string]any{"delivery_notice_state": "", "delivery_feedback_at": time.Now().Add(-2 * time.Minute)}).Error)
	hs.publishDirectDeliveryFeedback(context.Background())
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", notice.RoomID, EventTypeRoomMessage).Find(&notices).Error)
	require.Len(t, notices, 1)
	_, err := hs.RetryDirectDelivery(context.Background(), 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	var outbox models.FederationOutbox
	require.NoError(t, conn.First(&outbox, "object_id = ?", job.StatusID).Error)
	require.NoError(t, conn.Model(&outbox).Update("state", "processed").Error)
	var author models.Account
	require.NoError(t, conn.First(&author, 2).Error)
	activityID := fmt.Sprintf("%s/activities/%d", author.ActorURI(systemsettings.GetBaseURL()), outbox.ID)
	require.NoError(t, conn.Create(&activitypub.Delivery{ActivityID: activityID, TargetInbox: "https://remote.example/inbox", Payload: "{}", Status: activitypub.DeliveryStatusDelivered}).Error)
	hs.publishDirectDeliveryFeedback(context.Background())
	hs.publishDirectDeliveryFeedback(context.Background())
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", notice.RoomID, EventTypeRoomMessage).Find(&notices).Error)
	require.Len(t, notices, 2)
	var jobs int64
	require.NoError(t, conn.Model(&MatrixDMOutbound{}).Count(&jobs).Error)
	require.EqualValues(t, 1, jobs, "internal notices must never bridge back to AP")
	// Respect an explicit departure from the author notification room.
	require.NoError(t, conn.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", notice.RoomID, "@alice:test.local").Update("membership", MembershipLeave).Error)
	require.NoError(t, conn.Model(&job).Updates(map[string]any{"delivery_feedback_state": "failed", "delivery_notice_state": ""}).Error)
	job.DeliveryFeedbackState = "failed"
	job.DeliveryNoticeState = ""
	require.ErrorIs(t, hs.publishDirectDeliveryNotice(context.Background(), &job), errDirectMessageSkipped)
	membership, err := hs.Room.GetUserMembership(notice.RoomID, "@alice:test.local")
	require.NoError(t, err)
	require.Equal(t, MembershipLeave, membership)
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", notice.RoomID, EventTypeRoomMessage).Find(&notices).Error)
	require.Len(t, notices, 2)

}

func TestDirectDeliveryExternalNoticeDoesNotForceJoin(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&MatrixExternalActor{}))
	account := models.Account{Id: 88, Username: "_mx_notice_test", Bot: true}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)
	user := "@writer:remote.example"
	require.NoError(t, conn.Create(&MatrixExternalActor{MatrixID: user, AccountID: &account.Id}).Error)
	sum := sha256.Sum256([]byte(user))
	key := hex.EncodeToString(sum[:])
	room := "!apnotice_" + key + ":" + hs.Config.ServerName
	bot := "@_ap_bridge_notice_" + key + ":" + hs.Config.ServerName
	_, me := hs.Room.CreateRoom(bot, &CreateRoomRequest{bridgeRoomID: room, Visibility: "private", Preset: "private_chat", CreationContent: map[string]any{"m.federate": true}})
	require.Nil(t, me)
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: room, UserID: user, Membership: MembershipInvite}).Error)
	job := MatrixDMOutbound{EventID: "$remote-message", RoomID: "!original:remote.example", SenderAccountID: account.Id, DeliveryFeedbackState: "failed"}
	err := hs.publishDirectDeliveryNotice(context.Background(), &job)
	require.ErrorContains(t, err, "awaits acceptance")
	membership, err := hs.Room.GetUserMembership(room, user)
	require.NoError(t, err)
	require.Equal(t, MembershipInvite, membership)
	var count int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", room, EventTypeRoomMessage).Count(&count).Error)
	require.Zero(t, count, "do not post private notices before the remote user accepts")
}
