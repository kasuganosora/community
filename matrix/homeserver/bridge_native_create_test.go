package homeserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNativeMatrixCreateRoomDeliversProactiveAPMessageAndReply(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	var remote models.Account
	require.NoError(t, conn.First(&remote, 1).Error)
	created, me := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{IsDirect: true, Preset: "trusted_private_chat", Invite: []string{hs.bridgeAccountID(remote)}})
	require.Nil(t, me)
	require.True(t, strings.HasPrefix(created.RoomID, "!apchat_"))
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", created.RoomID).Error)
	require.True(t, room.IsDirect)
	require.Equal(t, "@remote@remote.example", *room.Name)
	var member MatrixRoomMember
	require.NoError(t, conn.First(&member, "room_id = ? AND user_id = ?", room.RoomID, hs.bridgeAccountID(remote)).Error)
	require.Equal(t, MembershipJoin, member.Membership)
	require.NotNil(t, member.DisplayName)
	require.Contains(t, *member.DisplayName, "remote@remote.example")
	sent := sendOutboundTest(t, hs, room.RoomID, map[string]any{"msgtype": "m.text", "body": "first native client DM"})
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	require.Equal(t, "delivered", job.State)
	require.Zero(t, job.SourceStatusID)
	var status models.Status
	require.NoError(t, conn.Preload("ToAccounts").First(&status, job.StatusID).Error)
	require.Equal(t, uint8(models.StatusVisibilityDirect), status.Visibility)
	require.Len(t, status.ToAccounts, 1)
	require.Equal(t, remote.Id, status.ToAccounts[0].Id)
	var outbox models.FederationOutbox
	require.NoError(t, conn.First(&outbox, "object_id = ? AND kind = ?", job.StatusID, "Create").Error)
	require.Equal(t, []uint64{remote.Id}, outbox.RecipientIDs)
	seedDirectBridge(t, conn, 9001, 2)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 9001).Update("in_reply_to_id", job.StatusID).Error)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 9001))
	var reply models.MatrixDMDelivery
	require.NoError(t, conn.First(&reply, "status_id = ?", 9001).Error)
	require.Equal(t, room.RoomID, reply.RoomID)
	require.Equal(t, "delivered", reply.State)
}

func TestNativeMatrixAPCreateRejectsEncryptionPublicAndBlocked(t *testing.T) {
	for _, variant := range []string{"encrypted", "public", "blocked", "space", "no_federation"} {
		t.Run(variant, func(t *testing.T) {
			hs, conn, _ := outboundBridgeFixture(t)
			var remote models.Account
			require.NoError(t, conn.First(&remote, 1).Error)
			req := &CreateRoomRequest{IsDirect: true, Invite: []string{hs.bridgeAccountID(remote)}}
			switch variant {
			case "space":
				req.CreationContent = map[string]any{"type": "m.space"}
			case "no_federation":
				req.CreationContent = map[string]any{"m.federate": false}
			case "encrypted":
				req.InitialState = []CreateRoomStateEvent{{Type: "m.room.encryption", Content: map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}}}
			case "public":
				req.Visibility = "public"
			case "blocked":
				require.NoError(t, conn.Create(&models.Block{AccountId: 2, TargetAccountId: 1}).Error)
			}
			var before, after int64
			require.NoError(t, conn.Model(&MatrixRoom{}).Count(&before).Error)
			result, me := hs.Room.CreateRoom("@alice:test.local", req)
			require.Nil(t, result)
			require.NotNil(t, me)
			require.NoError(t, conn.Model(&MatrixRoom{}).Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestNativeMatrixOrdinaryRoomRemainsUnbridged(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	created, me := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{IsDirect: true, Invite: []string{"@bob:test.local"}})
	require.Nil(t, me)
	require.False(t, strings.HasPrefix(created.RoomID, "!apchat_"))
	var count int64
	require.NoError(t, conn.Model(&BridgeConversation{}).Where("room_id = ?", created.RoomID).Count(&count).Error)
	require.Zero(t, count)
}

func TestNativeMatrixProfileFailureDoesNotFailCreatedRoom(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	var remote models.Account
	require.NoError(t, conn.First(&remote, 1).Error)
	injected := false
	callback := "test:fail-cosmetic-profile"
	require.NoError(t, conn.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "matrix_room_members" {
			return
		}
		if values, ok := tx.Statement.Dest.(map[string]any); ok {
			if _, updating := values["display_name"]; updating {
				injected = true
				tx.AddError(errors.New("profile temporarily unavailable"))
			}
		}
	}))
	defer conn.Callback().Update().Remove(callback)
	created, me := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{IsDirect: true, Invite: []string{hs.bridgeAccountID(remote)}})
	require.True(t, injected)
	require.Nil(t, me)
	require.NotNil(t, created)
	var conv BridgeConversation
	require.NoError(t, conn.First(&conv, "room_id = ?", created.RoomID).Error)
	require.Equal(t, "active", conv.State)
}
