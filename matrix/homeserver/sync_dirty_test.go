package homeserver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestChangedJoinedRoomsSourcesAndConstantQueries(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	write := conn.Session(&gorm.Session{SkipHooks: true})
	viewer := "@dirty:test.local"
	old, now := time.Now().Add(-time.Hour), time.Now()
	since := now.Add(-time.Minute).UnixMilli()
	rooms := make([]string, 61)
	for i := range rooms {
		rooms[i] = fmt.Sprintf("!dirty-%d:test.local", i)
		require.NoError(t, write.Create(&MatrixRoomMember{RoomID: rooms[i], UserID: viewer, Membership: MembershipJoin, CreatedAt: &old, UpdatedAt: &old}).Error)
	}
	// New local arrival with an old origin timestamp; changed state has the same candidate path.
	empty := ""
	for i := 0; i < 2; i++ {
		event := MatrixEvent{EventID: fmt.Sprintf("$dirty%d", i), RoomID: rooms[i], EventType: EventTypeRoomMessage, CreatedAt: &now, OriginServerTS: 1}
		if i == 1 {
			event.StateKey = &empty
			event.EventType = EventTypeRoomName
		}
		require.NoError(t, write.Create(&event).Error)
	}
	require.NoError(t, write.Create(&MatrixAccountData{UserID: viewer, RoomID: &rooms[2], DataType: "m.tag", UpdatedAt: &now}).Error)
	for i, receipt := range []MatrixReceipt{
		{UserID: "@other:test.local", ReceiptType: "m.read"},
		{UserID: viewer, ReceiptType: "m.read.private", ThreadID: "$thread"},
		{UserID: "@other:test.local", ReceiptType: "m.read.private"},
	} {
		receipt.RoomID = rooms[i+3]
		receipt.TS = now.UnixMilli()
		receipt.EventID = "$read"
		require.NoError(t, write.Create(&receipt).Error)
	}
	markTypingChanged(rooms[6])
	require.NoError(t, write.Model(&MatrixRoomMember{}).Where("room_id = ?", rooms[7]).UpdateColumn("updated_at", now).Error)
	require.NoError(t, write.Create(&MatrixEvent{EventID: "$dirty-null", RoomID: rooms[8], OriginServerTS: now.UnixMilli(), CreatedAt: &old}).Error)
	require.NoError(t, write.Model(&MatrixEvent{}).Where("event_id = ?", "$dirty-null").UpdateColumn("created_at", nil).Error)
	// Another user's account data and private receipt must not select a room.
	require.NoError(t, write.Create(&MatrixAccountData{UserID: "@other:test.local", RoomID: &rooms[9], DataType: "m.tag", UpdatedAt: &now}).Error)
	metrics := &syncReadMetrics{Interface: logger.Discard}
	service := *hs.Sync
	service.connection = conn.Session(&gorm.Session{Logger: metrics})
	dirty, err := service.changedJoinedRooms(viewer, rooms, since)
	require.NoError(t, err)
	require.Equal(t, 4, metrics.queries)
	for i, room := range rooms {
		expected := i <= 8 && i != 5
		require.Equal(t, expected, dirty[room], room)
		if !expected {
			require.False(t, service.userJoinedAfter(room, viewer, since))
		}
	}
	require.Equal(t, 4, metrics.queries, "unchanged join transitions must not issue per-room reads")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service.connection = conn.WithContext(ctx)
	_, err = service.changedJoinedRooms(viewer, rooms, since)
	require.Error(t, err)
	service.connection = conn
	require.NoError(t, conn.Migrator().DropTable(&MatrixReceipt{}))
	_, err = service.changedJoinedRooms(viewer, rooms, since)
	require.Error(t, err, "failed candidates must not silently skip rooms")
}

func TestSyncDirtyRoomsKeepsPresenceDevicesAndFullState(t *testing.T) {
	hs := createTestHomeServer(t)
	viewer, other := "@dirty-viewer:test.local", "@dirty-other:test.local"
	room := createRoomWithUser(t, hs, viewer)
	_, joinErr := hs.Room.JoinRoom(other, room, &JoinRoomRequest{})
	require.Nil(t, joinErr)
	conn := db.GetDB()
	old := time.Now().Add(-time.Hour)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&MatrixEvent{}).Where("room_id = ?", room).UpdateColumn("created_at", old).Error)
	require.NoError(t, conn.Model(&MatrixRoomMember{}).Where("room_id = ?", room).UpdateColumn("updated_at", old).Error)
	since := time.Now().Add(-time.Minute).UnixMilli()
	require.NoError(t, conn.Create(&MatrixPresence{UserID: other, Presence: "online", LastActiveTS: time.Now().UnixMilli()}).Error)
	require.NoError(t, conn.Create(&MatrixUserSession{UserID: other, DeviceID: "DIRTY_DEVICE", AccessToken: "dirty-fixture-token"}).Error)
	response, matrixErr := hs.Sync.Sync(viewer, "D", &SyncRequest{Since: fmt.Sprintf("s%d", since)})
	require.Nil(t, matrixErr)
	require.Empty(t, response.Rooms.Join)
	require.Contains(t, response.DeviceLists.Changed, other)
	found := false
	for _, event := range response.Presence.Events {
		if event.Sender == other {
			found = true
		}
	}
	require.True(t, found, "presence must still use all shared rooms")
	response, matrixErr = hs.Sync.Sync(viewer, "D", &SyncRequest{Since: fmt.Sprintf("s%d", since), FullState: true})
	require.Nil(t, matrixErr)
	require.NotNil(t, response.Rooms.Join[room])
	require.NotEmpty(t, response.Rooms.Join[room].State.Events)
}

func TestSyncNewSessionUsersBatchAndIsolation(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	old := time.Now().Add(-time.Hour)
	since := time.Now().Add(-time.Minute).UnixMilli()
	for i, user := range []string{"@new:test.local", "@old:test.local", "@outsider:test.local", "@left:test.local"} {
		session := MatrixUserSession{UserID: user, DeviceID: fmt.Sprintf("D%d", i), AccessToken: fmt.Sprintf("fixture-%d", i)}
		if i == 1 {
			session.CreatedAt = &old
		}
		require.NoError(t, conn.Create(&session).Error)
	}
	metrics := &syncReadMetrics{Interface: logger.Discard}
	service := *hs.Sync
	service.connection = conn.Session(&gorm.Session{Logger: metrics})
	members := map[string]bool{"@new:test.local": true, "@old:test.local": true, "@left:test.local": true}
	changed, left := map[string]bool{}, map[string]bool{"@left:test.local": true}
	service.addNewSessionUsers(members, changed, left, since)
	require.Nil(t, service.readError)
	require.Equal(t, 1, metrics.queries)
	require.Equal(t, map[string]bool{"@new:test.local": true}, changed)
	require.NoError(t, conn.Migrator().DropTable(&MatrixUserSession{}))
	service.addNewSessionUsers(members, map[string]bool{}, left, since)
	require.Error(t, service.readError)
}

func TestSyncDirtyMembershipPublicationIncludesJoinedState(t *testing.T) {
	hs := createTestHomeServer(t)
	alice, bob := "@dirty-publisher:test.local", "@dirty-reader:test.local"
	room := createRoomWithUser(t, hs, alice)
	_, joinErr := hs.Room.JoinRoom(bob, room, &JoinRoomRequest{})
	require.Nil(t, joinErr)
	conn := db.GetDB()
	old := time.Now().Add(-time.Hour)
	since := time.Now().Add(-time.Minute).UnixMilli()
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&MatrixEvent{}).Where("room_id = ?", room).UpdateColumn("created_at", old).Error)
	// Only membership publication is new; no event can make the candidate dirty.
	require.NoError(t, conn.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", room, bob).UpdateColumn("updated_at", time.Now()).Error)
	response, matrixErr := hs.Sync.Sync(bob, "D", &SyncRequest{Since: fmt.Sprintf("s%d", since)})
	require.Nil(t, matrixErr)
	require.NotNil(t, response.Rooms.Join[room])
	require.NotEmpty(t, response.Rooms.Join[room].State.Events)
}

func TestSyncDeviceSessionsSharedRoomsOneQuery(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&DeviceListChange{}))
	viewer, peer := "@many-viewer:test.local", "@many-peer:test.local"
	old := time.Now().Add(-time.Hour)
	since := time.Now().Add(-time.Minute).UnixMilli()
	rooms := make([]string, 61)
	service := *hs.Sync
	service.joinTransitions = make(map[syncJoinKey]bool)
	for i := range rooms {
		rooms[i] = fmt.Sprintf("!many-%d:test.local", i)
		require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: rooms[i], UserID: peer, Membership: MembershipJoin, CreatedAt: &old, UpdatedAt: &old}).Error)
		service.joinTransitions[syncJoinKey{roomID: rooms[i], userID: viewer, since: since}] = false
	}
	require.NoError(t, conn.Create(&MatrixUserSession{UserID: peer, DeviceID: "MANY", AccessToken: "many-fixture"}).Error)
	queries := 0
	name := "test:count-session-query"
	require.NoError(t, conn.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "matrix_user_sessions" {
			queries++
		}
	}))
	defer conn.Callback().Query().Remove(name)
	changed, left := service.getDeviceListUpdates(viewer, rooms, since)
	require.Nil(t, service.readError)
	require.Equal(t, []string{peer}, changed)
	require.Empty(t, left)
	require.Equal(t, 1, queries)
}
