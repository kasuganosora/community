package homeserver

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/db/database"
	"github.com/stretchr/testify/require"
)

func TestNewJoinSyncIncludesEncryptionAfterMembershipPublication(t *testing.T) {
	hs := createTestHomeServer(t)
	alice, bob := "@join-publisher:test.local", "@join-reader:test.local"
	roomID := createRoomWithUser(t, hs, alice)
	emptyKey := ""
	_, matErr := hs.Event.SendRoomEvent(roomID, alice, "m.room.encryption", nil, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}, &emptyKey)
	require.Nil(t, matErr)
	joinUser(t, hs, roomID, bob)
	var member MatrixRoomMember
	require.NoError(t, database.DB.Where("room_id = ? AND user_id = ?", roomID, bob).First(&member).Error)
	// Simulate the real interleaving: event stored, sync starts, then the
	// membership row becomes visible. Encryption predates the sync token.
	since := time.Now().Add(time.Second).UnixMilli()
	before := time.UnixMilli(since - 20)
	published := time.UnixMilli(since + 20)
	require.NoError(t, database.DB.Model(&MatrixEvent{}).Where("event_id = ?", member.EventID).Update("created_at", before).Error)
	require.NoError(t, database.DB.Model(&MatrixRoomMember{}).Where("id = ?", member.ID).UpdateColumn("updated_at", published).Error)
	require.True(t, hs.Sync.userJoinedAfter(roomID, bob, since))
	room := hs.Sync.buildJoinedRoomForDevice(roomID, bob, "DEVICE", since, false, nil)
	require.NotNil(t, room)
	encrypted := false
	for _, event := range room.State.Events {
		if event.Type == "m.room.encryption" {
			encrypted = true
			require.Equal(t, "m.megolm.v1.aes-sha2", event.Content["algorithm"])
		}
	}
	require.True(t, encrypted, "first joined-room sync must configure the encryption algorithm")
	// Once the publication is behind the token, the idle room has no new data.
	require.False(t, hs.Sync.userJoinedAfter(roomID, bob, since+40))
	later := hs.Sync.buildJoinedRoomForDevice(roomID, bob, "DEVICE", since+40, false, nil)
	require.Empty(t, later.State.Events)
	require.Empty(t, later.Timeline.Events)
}
