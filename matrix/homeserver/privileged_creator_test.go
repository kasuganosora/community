package homeserver

import (
	"encoding/json"
	"testing"

	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/stretchr/testify/require"
)

func TestVersionRedactionTargetFormat(t *testing.T) {
	for _, version := range []string{"1", "9", "10", "11", "12"} {
		t.Run(version, func(t *testing.T) {
			implementation, err := gmsl.GetRoomVersion(gmsl.RoomVersion(version))
			require.NoError(t, err)
			require.Equal(t, version == "11" || version == "12", redactionTargetInContent(implementation))
		})
	}
}

func TestV12CreatorPermissionsAreDerivedAndNotSerialized(t *testing.T) {
	hs := createTestHomeServer(t)
	room, matrixErr := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "12", CreationContent: map[string]any{"additional_creators": []string{"@coowner:test.local"}}})
	require.Nil(t, matrixErr)
	for _, user := range []string{"@coowner:test.local", "@bob:test.local"} {
		_, matrixErr = hs.Room.JoinRoom(user, room.RoomID, &JoinRoomRequest{})
		require.Nil(t, matrixErr)
	}
	levels, err := hs.Room.getRoomPowerLevels(room.RoomID)
	require.NoError(t, err)
	for _, creator := range []string{"@alice:test.local", "@coowner:test.local"} {
		require.Equal(t, gmsl.CreatorPowerLevel, hs.Room.getUserPowerLevel(levels, creator))
		require.True(t, hs.Room.canUserKick(room.RoomID, creator))
		require.True(t, hs.Room.canUserBan(room.RoomID, creator))
		require.True(t, hs.Event.canUserSendStateEvent(room.RoomID, creator, EventTypeRoomName))
		_, matrixErr = hs.Event.SendRoomEvent(room.RoomID, creator, "m.room.name", nil, map[string]any{"name": "Creator update " + creator}, new(""))
		require.Nil(t, matrixErr, "creator must pass both client precheck and gmsl authorization")
	}
	require.Zero(t, hs.Room.getUserPowerLevel(levels, "@bob:test.local"))
	require.False(t, hs.Event.canUserRedact(room.RoomID, "@bob:test.local"))
	raw, err := json.Marshal(levels)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "privileged")
	require.NotContains(t, string(raw), "@coowner:test.local")
	for _, creator := range []string{"@alice:test.local", "@coowner:test.local"} {
		_, matrixErr = hs.Event.SendRoomEvent(room.RoomID, "@alice:test.local", "m.room.power_levels", nil, map[string]any{"users": map[string]any{creator: 100}}, new(""))
		require.NotNil(t, matrixErr, "v12 must reject explicit creator entries instead of overwriting implicit unlimited power")
	}

	message, matrixErr := hs.Event.SendRoomEvent(room.RoomID, "@bob:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "Local moderation"}, nil)
	require.Nil(t, matrixErr)
	_, matrixErr = hs.Event.RedactEvent(room.RoomID, message.EventID, "@coowner:test.local", "moderation", nil)
	require.Nil(t, matrixErr)
	var redacted MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", message.EventID).First(&redacted).Error)
	require.Empty(t, redacted.Content)

}

func TestV12FederationRedactionHonorsAdditionalCreator(t *testing.T) {
	hs := createTestHomeServer(t)
	room, matrixErr := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "12", CreationContent: map[string]any{"additional_creators": []string{"@coowner:test.local"}}})
	require.Nil(t, matrixErr)
	for _, user := range []string{"@coowner:test.local", "@bob:test.local", "@charlie:test.local"} {
		_, matrixErr = hs.Room.JoinRoom(user, room.RoomID, &JoinRoomRequest{})
		require.Nil(t, matrixErr)
	}
	message, matrixErr := hs.Event.SendRoomEvent(room.RoomID, "@bob:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "Only privileged creator may remove this"}, nil)
	require.Nil(t, matrixErr)
	for _, sender := range []string{"@charlie:test.local", "@coowner:test.local"} {
		event := &MatrixEvent{RoomID: room.RoomID, Sender: sender, EventType: EventTypeRoomRedaction, Redacts: &message.EventID, Content: map[string]any{}}
		require.NoError(t, buildLocalPDU(db.GetDB(), hs, event, "12"))
		wire, err := matrixEventToPDU(event)
		require.NoError(t, err)
		require.NotContains(t, wire, "redacts")
		require.Equal(t, message.EventID, wire["content"].(map[string]any)["redacts"])
		parsed, err := parseAndValidatePDU("12", []byte(event.FederationJSON))
		require.NoError(t, err)
		require.Equal(t, message.EventID, pduRedactionTarget(parsed))
		_, err = storeFederationPDU(hs, wire)
		require.NoError(t, err)
		var stored MatrixEvent
		require.NoError(t, db.GetDB().Where("event_id = ?", message.EventID).First(&stored).Error)
		if sender == "@charlie:test.local" {
			require.NotEmpty(t, stored.Content["body"])
		} else {
			require.Empty(t, stored.Content)
		}
	}
}

func TestAdditionalCreatorDoesNotGrantLegacyRoomPrivileges(t *testing.T) {
	hs := createTestHomeServer(t)
	room, matrixErr := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "11", CreationContent: map[string]any{"additional_creators": []string{"@coowner:test.local"}}})
	require.Nil(t, matrixErr)
	_, matrixErr = hs.Room.JoinRoom("@coowner:test.local", room.RoomID, &JoinRoomRequest{})
	require.Nil(t, matrixErr)
	levels, err := hs.Room.getRoomPowerLevels(room.RoomID)
	require.NoError(t, err)
	require.Zero(t, hs.Room.getUserPowerLevel(levels, "@coowner:test.local"))
	require.False(t, hs.Event.canUserRedact(room.RoomID, "@coowner:test.local"))
}
