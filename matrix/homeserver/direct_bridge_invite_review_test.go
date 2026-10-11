package homeserver

import (
	"context"
	"errors"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestBridgePendingInviteCanBeRevokedWithoutGrantingKickPower(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 501, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 501))
	var delivery models.MatrixDMDelivery
	require.NoError(t, conn.First(&delivery, "status_id = ?", 501).Error)
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", delivery.RoomID).Error)
	require.Nil(t, hs.Room.InviteUser(room.RoomID, room.Creator, &InviteUserRequest{UserID: "@bob:test.local"}))
	require.NotNil(t, hs.Room.KickUser(room.RoomID, "@alice:test.local", &KickUserRequest{UserID: "@bob:test.local"}))
	require.Nil(t, hs.Room.KickUser(room.RoomID, room.Creator, &KickUserRequest{UserID: "@bob:test.local"}))
	var member MatrixRoomMember
	require.NoError(t, conn.Where("room_id = ? AND user_id = ?", room.RoomID, "@bob:test.local").First(&member).Error)
	require.Equal(t, MembershipLeave, member.Membership)
	require.NotNil(t, hs.Room.KickUser(room.RoomID, room.Creator, &KickUserRequest{UserID: "@bob:test.local"}))
}

func TestMembershipDepartureRollsBackEventAndStateOnWriteFailure(t *testing.T) {
	for _, action := range []string{"leave", "kick", "ban"} {
		t.Run(action, func(t *testing.T) {
			hs, conn := directBridgeDB(t)
			seedDirectBridge(t, conn, 502, 2)
			require.NoError(t, hs.ProcessDirectMessage(context.Background(), 502))
			var delivery models.MatrixDMDelivery
			require.NoError(t, conn.First(&delivery, "status_id = ?", 502).Error)
			var room MatrixRoom
			require.NoError(t, conn.First(&room, "room_id = ?", delivery.RoomID).Error)
			var before MatrixRoomMember
			require.NoError(t, conn.First(&before, "room_id = ? AND user_id = ?", room.RoomID, "@alice:test.local").Error)
			var countBefore int64
			require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ?", room.RoomID).Count(&countBefore).Error)
			name := "test:membership-write-failure"
			require.NoError(t, conn.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
				if tx.Statement.Table == "matrix_room_members" {
					tx.AddError(errors.New("injected membership failure"))
				}
			}))
			defer conn.Callback().Update().Remove(name)
			var me *MatrixError
			switch action {
			case "leave":
				me = hs.Room.LeaveRoom("@alice:test.local", room.RoomID, &LeaveRoomRequest{})
			case "kick":
				me = hs.Room.KickUser(room.RoomID, room.Creator, &KickUserRequest{UserID: "@alice:test.local"})
			case "ban":
				me = hs.Room.BanUser(room.RoomID, room.Creator, &BanUserRequest{UserID: "@alice:test.local"})
			}
			require.NotNil(t, me)
			var after MatrixRoomMember
			require.NoError(t, conn.First(&after, "room_id = ? AND user_id = ?", room.RoomID, before.UserID).Error)
			require.Equal(t, before.EventID, after.EventID)
			require.Equal(t, MembershipJoin, after.Membership)
			var state MatrixRoomState
			require.NoError(t, conn.First(&state, "room_id = ? AND event_type = ? AND state_key = ?", room.RoomID, EventTypeRoomMember, before.UserID).Error)
			require.Equal(t, before.EventID, state.EventID)
			var countAfter int64
			require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ?", room.RoomID).Count(&countAfter).Error)
			require.Equal(t, countBefore, countAfter)
		})
	}
}
