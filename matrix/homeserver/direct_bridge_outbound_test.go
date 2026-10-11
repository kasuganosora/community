package homeserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func outboundBridgeFixture(t *testing.T) (*HomeServer, *gorm.DB, models.MatrixDMDelivery) {
	t.Helper()
	hs, conn := directBridgeDB(t)
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.AccountExtra{}, &models.Emoji{}, &models.StatusEmoji{}, &models.Tag{}, &models.StatusTag{}, &models.Poll{}, &models.PollOption{}, &models.PollOptionVote{}, &models.Application{}, &models.Conversation{}, &models.AccountConversation{}, &models.FederationOutbox{}))
	require.NoError(t, conn.Create(&models.User{Id: 2, UserName: "alice", EmailHash: "alice-test"}).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 2).UpdateColumn("user_id", 2).Error)
	seedDirectBridge(t, conn, 11, 2, 3)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 11))
	var job models.MatrixDMDelivery
	require.NoError(t, conn.First(&job, "status_id = ?", 11).Error)
	return hs, conn, job
}
func sendOutboundTest(t *testing.T, hs *HomeServer, room string, content map[string]any) *SendMessageResponse {
	t.Helper()
	txn := "new-message"
	sent, me := hs.Event.SendRoomEventFromDevice(room, "@alice:test.local", "DEVICE", "m.room.message", &txn, content, nil)
	require.Nil(t, me)
	require.NotNil(t, sent)
	return sent
}
func TestOutboundDirectExactRecipientsReplyOutboxAndRetry(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	content := map[string]any{"msgtype": "m.text", "body": "> <@remote:test.local> quoted\n\nReply @outsider <b>literal</b>", "m.relates_to": map[string]any{"m.in_reply_to": map[string]any{"event_id": source.EventID}}}
	sent := sendOutboundTest(t, hs, source.RoomID, content)
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	require.Equal(t, sent.EventID, sendOutboundTest(t, hs, source.RoomID, content).EventID)
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	require.Equal(t, "delivered", job.State)
	var status models.Status
	require.NoError(t, conn.Preload("ToAccounts").Preload("Content").First(&status, job.StatusID).Error)
	require.Equal(t, uint8(models.StatusVisibilityDirect), status.Visibility)
	require.Equal(t, uint64(11), *status.InReplyToId)
	require.Equal(t, "Reply @outsider <b>literal</b>", status.Content.Content)
	ids := []uint64{}
	for _, a := range status.ToAccounts {
		ids = append(ids, a.Id)
	}
	require.ElementsMatch(t, []uint64{1, 3}, ids)
	var outbox []models.FederationOutbox
	require.NoError(t, conn.Where("object_id = ?", job.StatusID).Find(&outbox).Error)
	require.Len(t, outbox, 1)
	require.Equal(t, "Create", outbox[0].Kind)
	require.Equal(t, []uint64{1}, outbox[0].RecipientIDs)
	var mapping models.MatrixDMDelivery
	require.NoError(t, conn.First(&mapping, "status_id = ?", job.StatusID).Error)
	require.Equal(t, "delivered", mapping.State)
	require.Equal(t, sent.EventID, mapping.EventID)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), job.StatusID))
	var count int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", source.RoomID, EventTypeRoomMessage).Count(&count).Error)
	require.EqualValues(t, 2, count)
}
func TestOutboundDirectRollbackAndResumption(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "retry durable"})
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("fail-outbox", func(tx *gorm.DB) {
		if tx.Statement.Table == "activitypub_outbox_events" {
			tx.AddError(errors.New("injected outbox failure"))
		}
	}))
	require.Error(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	var count int64
	require.NoError(t, conn.Model(&models.Status{}).Where("account_id = ?", 2).Count(&count).Error)
	require.Zero(t, count)
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	require.Equal(t, "pending", job.State)
	require.NoError(t, conn.Callback().Create().Remove("fail-outbox"))
	require.NoError(t, conn.Model(&job).Update("next_attempt_at", time.Now().Add(-time.Minute)).Error)
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	require.NoError(t, conn.Model(&models.Status{}).Where("account_id = ?", 2).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
func TestOutboundDirectCurrentAudienceRevocation(t *testing.T) {
	for _, kind := range []string{"leave", "block", "narrow", "encrypted", "extra_member", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			hs, conn, source := outboundBridgeFixture(t)
			sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "must not escape"})
			switch kind {
			case "leave":
				require.NoError(t, conn.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", source.RoomID, "@bob:test.local").Update("membership", MembershipLeave).Error)
			case "block":
				require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Block{AccountId: 3, TargetAccountId: 2}).Error)
			case "narrow":
				require.NoError(t, conn.Where("status_id = ? AND account_id = ?", 11, 3).Delete(&models.StatusToAccount{}).Error)
			case "encrypted":
				require.NoError(t, conn.Create(&MatrixRoomState{RoomID: source.RoomID, EventType: EventTypeRoomEncryption, StateKey: "", EventID: "$fake-encryption"}).Error)
			case "extra_member":
				require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: source.RoomID, UserID: "@outsider:test.local", Membership: MembershipJoin}).Error)
			case "disabled":
				require.NoError(t, conn.Model(&models.User{}).Where("id = ?", 2).Update("status", models.UserStatusRejected).Error)
			}
			require.Error(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
			var count int64
			require.NoError(t, conn.Model(&models.Status{}).Where("account_id = ?", 2).Count(&count).Error)
			require.Zero(t, count)
			var job MatrixDMOutbound
			require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
			require.Equal(t, "skipped", job.State)
		})
	}
}
func TestOutboundDirectLocalAttachmentIndependentBytes(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	data := []byte("plain attachment")
	require.NoError(t, storage.Default().Put(context.Background(), "matrix-media/original.txt", bytes.NewReader(data), int64(len(data)), "text/plain"))
	require.NoError(t, conn.Create(&MatrixMedia{MediaID: "owned", ServerName: "test.local", UploaderID: "@alice:test.local", FilePath: "matrix-media/original.txt", FileName: "note.txt", FileSize: int64(len(data)), ContentType: "text/plain"}).Error)
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.file", "body": "note.txt", "url": "mxc://test.local/owned"})
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	var a models.MediaAttachment
	require.NoError(t, conn.Where("status_id = ?", job.StatusID).First(&a).Error)
	require.NotEqual(t, "matrix-media/original.txt", a.FilePath)
	require.NotContains(t, a.StorageKey(), "media/media/", "public Serve uses StorageKey, not FilePath")
	require.NotEqual(t, "note.txt", a.FileName)
	require.Len(t, strings.TrimSuffix(a.FileName, ".txt"), 64)
	require.Equal(t, "note.txt", a.Description)
	require.NoError(t, storage.Delete("matrix-media/original.txt"))
	r, _, err := storage.Open(context.Background(), a.StorageKey())
	require.NoError(t, err)
	defer r.Close()
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestOutboundDirectAdmissionBoundaries(t *testing.T) {
	for _, kind := range []string{"ordinary", "internal", "encrypted_event", "replace", "bad_reply", "unowned_media", "remote_media", "ghost"} {
		t.Run(kind, func(t *testing.T) {
			hs, conn, source := outboundBridgeFixture(t)
			eventType := "m.room.message"
			sender := "@alice:test.local"
			room := source.RoomID
			content := map[string]any{"msgtype": "m.text", "body": "> Not a fallback\n\nKeep literal", "org.community.activitypub": map[string]any{"transport": "activitypub"}}
			denied := false
			switch kind {
			case "ordinary":
				created, me := hs.Room.CreateRoom(sender, &CreateRoomRequest{Visibility: "private"})
				require.Nil(t, me)
				room = created.RoomID
			case "encrypted_event":
				eventType = "m.room.encrypted"
				content = map[string]any{"algorithm": "m.megolm.v1.aes-sha2", "ciphertext": "opaque"}
			case "replace":
				denied = true // Editing another sender's event must fail, not silently discard.
				content["m.relates_to"] = map[string]any{"rel_type": "m.replace", "event_id": source.EventID}
			case "bad_reply":
				content["m.relates_to"] = map[string]any{"m.in_reply_to": map[string]any{"event_id": "$not-this-room"}}
				denied = true
			case "unowned_media":
				require.NoError(t, conn.Create(&MatrixMedia{MediaID: "someone-else", ServerName: "test.local", UploaderID: "@bob:test.local", FilePath: "private.txt", FileSize: 1}).Error)
				content["msgtype"] = "m.file"
				content["url"] = "mxc://test.local/someone-else"
				denied = true
			case "remote_media":
				content["msgtype"] = "m.image"
				content["url"] = "mxc://remote.example/secret"
				denied = true
			case "ghost":
				var a models.Account
				require.NoError(t, conn.First(&a, 1).Error)
				sender = hs.bridgeAccountID(a)
				denied = true
			}
			txn := "ap-status-11" // Client-chosen reserved-looking txn cannot impersonate the internal sender.
			var me *MatrixError
			if kind == "internal" {
				_, me = hs.Event.SendRoomEvent(room, sender, eventType, &txn, content, nil)
			} else {
				_, me = hs.Event.SendRoomEventFromDevice(room, sender, "DEVICE", eventType, &txn, content, nil)
			}
			if denied {
				require.NotNil(t, me)
			} else {
				require.Nil(t, me)
			}
			var jobs int64
			require.NoError(t, conn.Model(&MatrixDMOutbound{}).Count(&jobs).Error)
			require.Zero(t, jobs)
		})
	}
}
func TestOutboundDirectEnqueueFailureRollsBackMatrixEvent(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	var before int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ?", source.RoomID).Count(&before).Error)
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("fail-dm-job", func(tx *gorm.DB) {
		if tx.Statement.Table == "matrix_dm_outbound" {
			tx.AddError(errors.New("queue write failed"))
		}
	}))
	defer conn.Callback().Create().Remove("fail-dm-job")
	txn := "atomic-send"
	_, me := hs.Event.SendRoomEventFromDevice(source.RoomID, "@alice:test.local", "DEVICE", "m.room.message", &txn, map[string]any{"msgtype": "m.text", "body": "not committed"}, nil)
	require.NotNil(t, me)
	var after int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ?", source.RoomID).Count(&after).Error)
	require.Equal(t, before, after)
}
func TestOutboundDirectNoticeDoesNotBackfillOldMatrixMessages(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	_, me := hs.Event.SendRoomEvent(source.RoomID, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "old Matrix only message"}, nil)
	require.Nil(t, me)
	hs.refreshDirectBridgeTopics(context.Background())
	var jobs int64
	require.NoError(t, conn.Model(&MatrixDMOutbound{}).Count(&jobs).Error)
	require.Zero(t, jobs)
	var state MatrixRoomState
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", source.RoomID, "m.room.topic").First(&state).Error)
	require.Equal(t, directBridgeTopic, state.Content["topic"])
}

func TestOutboundDirectRapidReplyWaitsForDurableParent(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	parent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "first"})
	txn := "second-message"
	child, me := hs.Event.SendRoomEventFromDevice(source.RoomID, "@alice:test.local", "DEVICE", "m.room.message", &txn, map[string]any{"msgtype": "m.text", "body": "> <@alice:test.local> first\n\nsecond", "m.relates_to": map[string]any{"m.in_reply_to": map[string]any{"event_id": parent.EventID}}}, nil)
	require.Nil(t, me)
	require.ErrorIs(t, hs.ProcessOutboundDirect(context.Background(), child.EventID), errOutboundParentPending)
	var childJob MatrixDMOutbound
	require.NoError(t, conn.First(&childJob, "event_id = ?", child.EventID).Error)
	require.Equal(t, "pending", childJob.State)
	require.Zero(t, childJob.StatusID)
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), parent.EventID))
	require.NoError(t, conn.Model(&childJob).Update("next_attempt_at", time.Now().Add(-time.Minute)).Error)
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), child.EventID))
	var parentJob MatrixDMOutbound
	require.NoError(t, conn.First(&parentJob, "event_id = ?", parent.EventID).Error)
	require.NoError(t, conn.First(&childJob, "event_id = ?", child.EventID).Error)
	var status models.Status
	require.NoError(t, conn.Preload("Content").First(&status, childJob.StatusID).Error)
	require.Equal(t, parentJob.StatusID, *status.InReplyToId)
	require.Equal(t, "second", status.Content.Content)
}

func TestOutboundDirectDeletedLatestDoesNotDisableLiveRoom(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	seedDirectBridge(t, conn, 12, 2, 3)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 12))
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Status{}).Where("id = ?", 12).UpdateColumn("deleted_at", time.Now()).Error)
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "reply to still-valid audience"})
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	require.EqualValues(t, 11, job.SourceStatusID)
	require.Equal(t, "delivered", job.State)
	var before, after int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Count(&before).Error)
	hs.refreshDirectBridgeTopics(context.Background())
	hs.refreshDirectBridgeTopics(context.Background())
	require.NoError(t, conn.Model(&MatrixEvent{}).Count(&after).Error)
	require.Equal(t, before, after, "startup must not add unchanged topic events")
}

func TestOutboundDirectReplyToInboundAttachmentBeforeDeliveryACK(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	var remote models.Account
	require.NoError(t, conn.First(&remote, 1).Error)
	txn := "ap-status-11-media-91"
	require.NoError(t, hs.sendDirectAttachment(source.RoomID, hs.bridgeAccountID(remote), txn, 11, models.MediaAttachment{Id: 91, Type: models.MediaTypeImage, FileName: "picture.png"}, MatrixMedia{MediaID: "inbound-image", ContentType: "image/png", FileSize: 100}))
	var ref MatrixDMEventReference
	require.NoError(t, conn.Where("status_id = ? AND event_id <> ?", 11, source.EventID).First(&ref).Error)
	// An attachment later in the same inbound delivery may still be retrying.
	require.NoError(t, conn.Model(&source).Update("state", "processing").Error)
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "> image fallback\n\nAbout this image", "m.relates_to": map[string]any{"m.in_reply_to": map[string]any{"event_id": ref.EventID}}})
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	var status models.Status
	require.NoError(t, conn.Preload("Content").First(&status, job.StatusID).Error)
	require.EqualValues(t, 11, *status.InReplyToId)
	require.Equal(t, "About this image", status.Content.Content)
}
func TestOutboundDirectInternalReferenceIsAtomicWithMatrixEvent(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	var before int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Count(&before).Error)
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("fail-dm-ref", func(tx *gorm.DB) {
		if tx.Statement.Table == "matrix_dm_event_references" {
			tx.AddError(errors.New("reference unavailable"))
		}
	}))
	defer conn.Callback().Create().Remove("fail-dm-ref")
	txn := "inbound-mapping-atomic"
	_, me := hs.Event.sendBridgeRoomEvent(source.RoomID, "@alice:test.local", "m.room.message", &txn, map[string]any{"msgtype": "m.text", "body": "rollback with reference"}, 11)
	require.NotNil(t, me)
	var after int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Count(&after).Error)
	require.Equal(t, before, after)
}
