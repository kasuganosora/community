package homeserver

import (
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestLegacyHistoricalJoinedEvidenceAndNewSnapshots(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	write := conn.Session(&gorm.Session{SkipHooks: true})
	roomID, alice, bob := "!legacy-history:test.local", "@alice:test.local", "@bob:test.local"
	empty := ""
	require.NoError(t, conn.Create(&MatrixRoom{RoomID: roomID, Creator: alice, RoomVersion: "10", HistoryVisibility: "joined"}).Error)
	rows := []MatrixEvent{
		{EventID: "$old-create", EventType: EventTypeRoomCreate, StateKey: &empty, Sender: alice, Content: map[string]any{"creator": alice}},
		{EventID: "$old-alice", EventType: EventTypeRoomMember, StateKey: &alice, Sender: alice, PrevEvents: []string{"$old-create"}, Content: map[string]any{"membership": "join"}},
		{EventID: "$old-history", EventType: EventTypeRoomHistoryVisibility, StateKey: &empty, Sender: alice, PrevEvents: []string{"$old-alice"}, Content: map[string]any{"history_visibility": "joined"}},
		{EventID: "$old-secret", EventType: EventTypeRoomMessage, Sender: alice, PrevEvents: []string{"$old-history"}, Content: map[string]any{"body": "before Bob"}},
		{EventID: "$old-bob", EventType: EventTypeRoomMember, StateKey: &bob, Sender: bob, PrevEvents: []string{"$old-secret"}, Content: map[string]any{"membership": "join"}},
	}
	for i := range rows {
		rows[i].RoomID = roomID
		rows[i].Depth = int64(i + 1)
		rows[i].OriginServerTS = int64(i + 1)
		require.NoError(t, write.Create(&rows[i]).Error)
		if rows[i].StateKey != nil {
			require.NoError(t, conn.Create(&MatrixRoomState{RoomID: roomID, EventType: rows[i].EventType, StateKey: *rows[i].StateKey, EventID: rows[i].EventID, Content: rows[i].Content}).Error)
		}
	}
	for _, user := range []string{alice, bob} {
		require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: roomID, UserID: user, Membership: MembershipJoin}).Error)
	}
	fresh, matrixErr := hs.Event.SendRoomEvent(roomID, alice, string(EventTypeRoomMessage), nil, map[string]any{"msgtype": "m.text", "body": "after Bob"}, nil)
	require.Nil(t, matrixErr)
	var stored MatrixEvent
	require.NoError(t, conn.Where("event_id = ?", fresh.EventID).First(&stored).Error)
	require.True(t, stored.StateSnapshotKnown)
	require.Empty(t, stored.FederationJSON)
	require.Len(t, stored.StateBefore, 4)
	require.False(t, canReadClientEvent(roomID, bob, "$old-secret"))
	require.True(t, canReadClientEvent(roomID, alice, "$old-secret"))
	require.True(t, canReadClientEvent(roomID, bob, fresh.EventID))
	page, pageErr := hs.Event.GetRoomMessages(roomID, bob, &GetRoomMessagesRequest{Dir: "b", Limit: 20, Filter: `{"lazy_load_members":false}`})
	require.Nil(t, pageErr)
	found := false
	for _, event := range page.Chunk {
		require.NotEqual(t, "$old-secret", event.EventID)
		if event.EventID == fresh.EventID {
			found = true
		}
	}
	require.True(t, found)
	syncResponse, syncErr := hs.Sync.Sync(bob, "OLDDEVICE", &SyncRequest{})
	require.Nil(t, syncErr)
	joined := syncResponse.Rooms.Join[roomID]
	require.NotNil(t, joined)
	found = false
	for _, event := range joined.Timeline.Events {
		require.NotEqual(t, "$old-secret", event.EventID)
		if event.EventID == fresh.EventID {
			found = true
		}
	}
	require.True(t, found)
	// Traversal warmed ancestors in its cache: reading those events must not
	// bypass the conservative missing visibility-evidence check.
	reader, err := newClientHistoryReader(conn, roomID, bob)
	require.NoError(t, err)
	visible, err := reader.filter([]MatrixEvent{rows[3], rows[0], rows[1]})
	require.NoError(t, err)
	require.Empty(t, visible)
	orphan := MatrixEvent{EventID: "$legacy-orphan", RoomID: roomID, Sender: alice, EventType: EventTypeRoomMessage}
	require.NoError(t, write.Create(&orphan).Error)
	require.False(t, canReadClientEvent(roomID, bob, orphan.EventID))
}
