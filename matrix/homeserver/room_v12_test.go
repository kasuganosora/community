package homeserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

func TestCreateVersion12RoomUsesCreationHash(t *testing.T) {
	hs := createTestHomeServer(t)
	room, me := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{RoomVersion: "12", Preset: "public_chat"})
	require.Nil(t, me)
	require.NotContains(t, room.RoomID, ":")
	var create MatrixEvent
	require.NoError(t, db.GetDB().First(&create, "room_id = ? AND event_type = ?", room.RoomID, EventTypeRoomCreate).Error)
	require.Equal(t, "!"+create.EventID[1:], room.RoomID)
	var wire map[string]any
	require.NoError(t, json.Unmarshal([]byte(create.FederationJSON), &wire))
	require.NotContains(t, wire, "room_id")
	require.NotContains(t, create.Content, "creator")
	var events []MatrixEvent
	require.NoError(t, db.GetDB().Where("room_id = ?", room.RoomID).Find(&events).Error)
	require.GreaterOrEqual(t, len(events), 6)
	for _, row := range events {
		p, err := parseAndValidatePDU("12", []byte(row.FederationJSON))
		require.NoError(t, err)
		require.Equal(t, room.RoomID, p.RoomID().String())
		require.NoError(t, validateIncomingPDU(context.Background(), hs, p, true))
	}
	room2, me := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{RoomVersion: "12", Preset: "public_chat"})
	require.Nil(t, me)
	require.NotEqual(t, room.RoomID, room2.RoomID)
}
