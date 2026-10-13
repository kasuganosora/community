package homeserver

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Query counts must grow with page batches, not with the number of joined
// rooms. This isolates SQL amplification from HTTP/database background traffic.
func TestWebGroupReadsBatchAcrossHundredRooms(t *testing.T) {
	hs, db, _ := setupWebGroupCore(t)
	user := "@alice:test.local"
	rooms := make([]MatrixRoom, 0, 100)
	seed := func(start, end int) {
		for i := start; i < end; i++ {
			room := MatrixRoom{RoomID: fmt.Sprintf("!group-read-%03d:test.local", i), RoomVersion: "12", Creator: user, Visibility: "private", JoinRule: "invite", HistoryVisibility: "joined"}
			require.NoError(t, db.Create(&room).Error)
			require.NoError(t, db.Create(&MatrixRoomMember{RoomID: room.RoomID, UserID: user, Membership: MembershipJoin, EventID: fmt.Sprintf("$member-%d", i)}).Error)
			require.NoError(t, db.Create(&MatrixRoomState{RoomID: room.RoomID, EventType: EventTypeRoomCreate, EventID: fmt.Sprintf("$create-%d", i), Content: map[string]any{"room_version": "12"}}).Error)
			rooms = append(rooms, room)
		}
	}
	metrics := &syncReadMetrics{Interface: logger.Discard}
	conn := db.Session(&gorm.Session{Logger: metrics})
	seed(0, 1)
	metrics.queries = 0
	one, err := webGroupDTOs(hs, conn, rooms, user)
	require.NoError(t, err)
	require.Len(t, one, 1)
	singleQueries := metrics.queries
	metrics.queries = 0
	initialOne, err := webGroupSyncSnapshot(hs, conn, user, webGroupSyncCursor{User: user}, true)
	require.NoError(t, err)
	require.Len(t, initialOne.Groups, 1)
	singleSyncQueries := metrics.queries
	seed(1, 100)
	metrics.queries = 0
	many, err := webGroupDTOs(hs, conn, rooms, user)
	require.NoError(t, err)
	require.Len(t, many, 100)
	require.Equal(t, singleQueries, metrics.queries, "group details must use batch queries")
	t.Logf("DTO 1 vs 100 rooms: %d SQL statements", metrics.queries)
	metrics.queries = 0
	initial, err := webGroupSyncSnapshot(hs, conn, user, webGroupSyncCursor{User: user}, true)
	require.NoError(t, err)
	require.Len(t, initial.Groups, 100)
	require.Equal(t, singleSyncQueries, metrics.queries, "initial sync must not query per room")
	t.Logf("initial sync 1 vs 100 rooms: %d SQL statements", metrics.queries)
	var cursor webGroupSyncCursor
	require.NoError(t, webGroupDecode(initial.NextCursor, &cursor))
	metrics.queries = 0
	unchanged, err := webGroupSyncSnapshot(hs, conn, user, cursor, false)
	require.NoError(t, err)
	require.Empty(t, unchanged.Changed)
	require.Empty(t, unchanged.Messages)
	require.LessOrEqual(t, metrics.queries, 7, "idle sync only checks batch revisions and membership")
	t.Logf("unchanged sync 100 rooms: %d SQL statements", metrics.queries)
}
