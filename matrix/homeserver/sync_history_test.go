package homeserver

import (
	"fmt"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSyncTimelineHonorsJoinedHistoryAcrossInitialNewJoinAndLeave(t *testing.T) {
	hs := createTestHomeServer(t)
	alice, bob := "@sync-history-alice:test.local", "@sync-history-bob:test.local"
	roomID := createRoomWithUser(t, hs, alice)
	empty := ""
	_, matrixErr := hs.Event.SendRoomEvent(roomID, alice, "m.room.history_visibility", nil, map[string]any{"history_visibility": "joined"}, &empty)
	require.Nil(t, matrixErr)
	hidden, matrixErr := hs.Event.SendRoomEvent(roomID, alice, "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "hidden before join"}, nil)
	require.Nil(t, matrixErr)
	since := time.Now().UnixMilli() - 1
	_, matrixErr = hs.Room.JoinRoom(bob, roomID, &JoinRoomRequest{})
	require.Nil(t, matrixErr)
	visible, matrixErr := hs.Event.SendRoomEvent(roomID, alice, "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "visible after join"}, nil)
	require.Nil(t, matrixErr)
	limit := 10
	filter := &parsedFilter{Room: &parsedRoomFilter{Timeline: &parsedEventFilter{Limit: &limit, Types: []string{"m.room.message"}}}}
	for _, token := range []int64{0, since} {
		room := hs.Sync.buildJoinedRoomForDevice(roomID, bob, "DEVICE", token, false, filter)
		require.Nil(t, hs.Sync.readError)
		require.Len(t, room.Timeline.Events, 1)
		require.Equal(t, visible.EventID, room.Timeline.Events[0].EventID)
		require.NotEqual(t, hidden.EventID, room.Timeline.Events[0].EventID)
	}
	require.Nil(t, hs.Room.LeaveRoom(bob, roomID, &LeaveRoomRequest{}))
	afterLeave, matrixErr := hs.Event.SendRoomEvent(roomID, alice, "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "hidden after leave"}, nil)
	require.Nil(t, matrixErr)
	left := hs.Sync.buildLeftRoom(roomID, bob, 0, filter)
	require.Nil(t, hs.Sync.readError)
	require.Len(t, left.Timeline.Events, 1)
	require.Equal(t, visible.EventID, left.Timeline.Events[0].EventID)
	require.NotEqual(t, afterLeave.EventID, left.Timeline.Events[0].EventID)
}

func TestSyncTimelineRefillsPastInvisibleHistoryAndComputesLimited(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	roomID, user := "!sync-history-pages:test.local", "@sync-history-page:test.local"
	write := conn.Session(&gorm.Session{SkipHooks: true})
	empty := ""
	old := time.Now().Add(-time.Hour)
	visibility := MatrixEvent{EventID: "$sync-history-visibility", CreatedAt: &old, RoomID: roomID, EventType: EventTypeRoomHistoryVisibility, StateKey: &empty, Content: map[string]any{"history_visibility": "joined"}}
	joined := MatrixEvent{EventID: "$sync-history-join", CreatedAt: &old, RoomID: roomID, EventType: EventTypeRoomMember, StateKey: &user, Content: map[string]any{"membership": "join"}}
	left := MatrixEvent{EventID: "$sync-history-leave", CreatedAt: &old, RoomID: roomID, EventType: EventTypeRoomMember, StateKey: &user, Content: map[string]any{"membership": "leave"}}
	require.NoError(t, write.Create(&[]MatrixEvent{visibility, joined, left}).Error)
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: roomID, UserID: user, Membership: MembershipJoin, EventID: joined.EventID, CreatedAt: &old, UpdatedAt: &old}).Error)
	now := time.Now()
	for i := 1; i <= 45; i++ {
		state := []string{visibility.EventID, left.EventID}
		if i <= 3 {
			state = []string{visibility.EventID, joined.EventID}
		}
		require.NoError(t, write.Create(&MatrixEvent{EventID: fmt.Sprintf("$sync-page-%02d", i), RoomID: roomID, EventType: EventTypeRoomMessage, Sender: "@author:test.local", OriginServerTS: int64(i), CreatedAt: &now, Content: map[string]any{"body": i}, StateSnapshotKnown: true, StateBefore: state}).Error)
	}
	limit := 2
	filter := &parsedFilter{Room: &parsedRoomFilter{Timeline: &parsedEventFilter{Limit: &limit, Types: []string{"m.room.message"}}}}
	room := hs.Sync.buildJoinedRoomForDevice(roomID, user, "D", now.UnixMilli()-1, false, filter)
	require.Nil(t, hs.Sync.readError)
	require.True(t, room.Timeline.Limited)
	require.Len(t, room.Timeline.Events, 2)
	require.Equal(t, "$sync-page-02", room.Timeline.Events[0].EventID)
	require.Equal(t, "$sync-page-03", room.Timeline.Events[1].EventID)
	require.Equal(t, "t2_$sync-page-02", room.Timeline.PrevBatch)
	// Increasing the visible limit must refill across >32 rejected rows, and
	// exhaustion rather than raw candidate count determines limited=false.
	limit = 5
	room = hs.Sync.buildJoinedRoomForDevice(roomID, user, "D", now.UnixMilli()-1, false, filter)
	require.Nil(t, hs.Sync.readError)
	require.Len(t, room.Timeline.Events, 3)
	require.False(t, room.Timeline.Limited)
}

func TestSyncPrevBatchContinuesMessagesWithEqualTimestamps(t *testing.T) {
	hs := createTestHomeServer(t)
	user := "@sync-cursor:test.local"
	since := time.Now().Add(-time.Minute).UnixMilli()
	roomID := createRoomWithUser(t, hs, user)
	seed, matrixErr := hs.Event.SendRoomEvent(roomID, user, "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "seed"}, nil)
	require.Nil(t, matrixErr)
	var template MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", seed.EventID).First(&template).Error)
	// Use the real room's complete authorized snapshot, with deterministic
	// timestamp ties and local arrival times different from origin timestamps.
	originTS := template.OriginServerTS + 10000
	now := time.Now()
	for i := 1; i <= 5; i++ {
		event := MatrixEvent{EventID: fmt.Sprintf("$sync-cursor-%d", i), RoomID: roomID, Sender: user,
			EventType: EventTypeRoomMessage, OriginServerTS: originTS, CreatedAt: &now,
			Content:            map[string]any{"msgtype": "m.text", "body": fmt.Sprintf("tied-%d", i)},
			StateSnapshotKnown: true, StateBefore: template.StateBefore}
		require.NoError(t, db.GetDB().Session(&gorm.Session{SkipHooks: true}).Create(&event).Error)
	}
	for _, sinceToken := range []string{"", fmt.Sprintf("s%d", since)} {
		t.Run(fmt.Sprintf("since_%s", sinceToken), func(t *testing.T) {
			response, syncErr := hs.Sync.Sync(user, "CURSOR_DEVICE", &SyncRequest{Since: sinceToken,
				Filter: `{"room":{"timeline":{"limit":2,"types":["m.room.message"]}}}`})
			require.Nil(t, syncErr)
			timeline := response.Rooms.Join[roomID].Timeline
			require.True(t, timeline.Limited)
			require.Len(t, timeline.Events, 2)
			require.Equal(t, fmt.Sprintf("t%d_$sync-cursor-4", originTS), timeline.PrevBatch)
			seen := map[string]bool{}
			for _, event := range timeline.Events {
				seen[event.EventID] = true
			}
			cursor := timeline.PrevBatch
			for pageNumber := 0; pageNumber < 10; pageNumber++ {
				page, pageErr := hs.Event.GetRoomMessages(roomID, user, &GetRoomMessagesRequest{
					Dir: "b", From: cursor, Limit: 2, Filter: `{"types":["m.room.message"]}`})
				require.Nil(t, pageErr)
				for _, event := range page.Chunk {
					require.False(t, seen[event.EventID], "overlapping event %s", event.EventID)
					seen[event.EventID] = true
				}
				if len(page.Chunk) == 0 || page.End == "" {
					break
				}
				require.NotEqual(t, cursor, page.End)
				cursor = page.End
			}
			require.True(t, seen[seed.EventID])
			for i := 1; i <= 5; i++ {
				require.True(t, seen[fmt.Sprintf("$sync-cursor-%d", i)])
			}
		})
	}
}
