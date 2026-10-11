package homeserver

import (
	"context"
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestBridgeRoomNameRealParticipantAndExistingRoomUpgrade(t *testing.T) {
	hs, conn := directBridgeDB(t)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 1).Update("display_name", "露娜").Error)
	seedDirectBridge(t, conn, 21, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 21))
	var mapping models.MatrixDMDelivery
	require.NoError(t, conn.First(&mapping, "status_id = ?", 21).Error)
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", mapping.RoomID).Error)
	require.Equal(t, "露娜（@remote@remote.example）", *room.Name)
	// Upgrade existing default names even when their topic is already current.
	key := ""
	_, me := hs.Event.SendRoomEvent(room.RoomID, room.Creator, "m.room.name", nil, map[string]any{"name": "Fediverse private messages"}, &key)
	require.Nil(t, me)
	hs.refreshDirectBridgeTopics(context.Background())
	require.NoError(t, conn.First(&room, "room_id = ?", mapping.RoomID).Error)
	require.Equal(t, "露娜（@remote@remote.example）", *room.Name)
	var before, after int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", room.RoomID, "m.room.name").Count(&before).Error)
	require.NoError(t, hs.syncBridgeRoomName(context.Background(), room.RoomID))
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", room.RoomID, "m.room.name").Count(&after).Error)
	require.Equal(t, before, after)
	// A custom title stays authoritative, including if a client retained our marker.
	_, me = hs.Event.SendRoomEvent(room.RoomID, room.Creator, "m.room.name", nil, map[string]any{"name": "旅行计划", bridgeGeneratedNameKey: "露娜（@remote@remote.example）"}, &key)
	require.Nil(t, me)
	require.NoError(t, hs.syncBridgeRoomName(context.Background(), room.RoomID))
	require.NoError(t, conn.First(&room, "room_id = ?", mapping.RoomID).Error)
	require.Equal(t, "旅行计划", *room.Name)
}

func TestBridgeParticipantTitleStableAndBounded(t *testing.T) {
	require.Equal(t, "远端（@luna@example.org）", bridgeParticipantTitle([]bridgeNameMember{{id: "local", label: "luna", local: true}, {id: "remote", label: "远端（@luna@example.org）"}}))
	group := []bridgeNameMember{{id: "b", label: "Bob", local: true}, {id: "a", label: "Alice", local: true}, {id: "c", label: "Carol", local: true}, {id: "d", label: "Dana", local: true}}
	require.Equal(t, "群聊 · Alice、Bob、Carol（4 人）", bridgeParticipantTitle(group))
	require.Equal(t, "Alice · Bob", bridgeParticipantTitle(group[:2]))
	require.Equal(t, "luna", bridgeNameText("\u202eluna\n", 20))
	require.Len(t, []rune(bridgeNameText(strings.Repeat("名", 500), 255)), 255)
}

func TestExplicitBridgeRoomNameUsesAccountsAtCreation(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	conv, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", BridgeConversationRequest{TransactionID: "named-conversation", PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "1"}}})
	require.NoError(t, err)
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", conv.RoomID).Error)
	require.Equal(t, "@remote@remote.example", *room.Name)
}
