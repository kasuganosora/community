package homeserver

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestExplicitConversationReplyUsesTrustedParentAndCurrentAudience(t *testing.T) {
	hs, conn := directBridgeDB(t)
	ctx := context.Background()
	seedDirectBridge(t, conn, 11, 2)
	require.NoError(t, hs.ProcessDirectMessage(ctx, 11))
	var delivery models.MatrixDMDelivery
	require.NoError(t, conn.First(&delivery, "status_id = ?", 11).Error)
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", delivery.RoomID).Error)
	conv := BridgeConversation{ID: "routing-test", RoomID: room.RoomID, Controller: room.Creator, CreatorAccountID: 2, State: "active", Revision: 1, RequestKey: "routing-test"}
	require.NoError(t, conn.Create(&conv).Error)
	var accounts []models.Account
	require.NoError(t, conn.Where("id IN ?", []uint64{1, 2}).Find(&accounts).Error)
	participants := map[string]models.Account{}
	for _, account := range accounts {
		mxid := hs.bridgeAccountID(account)
		participants[mxid] = account
		require.NoError(t, conn.Create(&BridgeConversationMember{ConversationID: conv.ID, MatrixID: mxid, AccountID: account.Id, JoinedRevision: 1}).Error)
	}
	parent := uint64(11)
	status := models.Status{AccountId: 1, InReplyToId: &parent, Visibility: models.StatusVisibilityDirect}
	id, creator, mapped, explicit, err := hs.explicitBridgeRoomForStatus(ctx, &status, participants)
	require.NoError(t, err)
	require.True(t, explicit)
	require.Equal(t, room.RoomID, id)
	require.Equal(t, room.Creator, creator)
	require.Len(t, mapped, 2)
	require.NoError(t, conn.Model(&conv).Update("state", "provisioning").Error)
	_, _, _, explicit, err = hs.explicitBridgeRoomForStatus(ctx, &status, participants)
	require.True(t, explicit)
	require.Error(t, err) // Do not silently route transitional rooms elsewhere.
	require.NoError(t, conn.Model(&conv).Update("state", "active").Error)
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: room.RoomID, UserID: "@outsider:test.local", Membership: MembershipJoin}).Error)
	_, _, _, _, err = hs.explicitBridgeRoomForStatus(ctx, &status, participants)
	require.ErrorIs(t, err, errDirectMessageSkipped)
	// No new member may receive an older, narrower AP reply merely by joining.
	require.NoError(t, conn.Create(&BridgeConversationMember{ConversationID: conv.ID, MatrixID: "@outsider:test.local", AccountID: 4, JoinedRevision: 2}).Error)
	_, _, _, _, err = hs.explicitBridgeRoomForStatus(ctx, &status, participants)
	require.ErrorIs(t, err, errDirectMessageSkipped)
}
