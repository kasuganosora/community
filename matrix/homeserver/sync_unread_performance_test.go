package homeserver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type syncReadMetrics struct {
	logger.Interface
	queries int
	rows    int64
}

func (m *syncReadMetrics) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	_, rows := fc()
	m.queries++
	if rows > 0 {
		m.rows += rows
	}
}

func TestSyncUnreadReadAmplification(t *testing.T) {
	metrics := &syncReadMetrics{Interface: logger.Discard}
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: metrics})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&MatrixEvent{}, &MatrixReceipt{}))
	const room = "!unread:test.local"
	const viewer = "@viewer:test.local"
	events := make([]MatrixEvent, 0, 2000)
	for i := 1; i <= 1000; i++ {
		body := "plain"
		if i%100 == 0 {
			body = viewer
		}
		content := map[string]any{"body": body}
		if i%10 == 0 {
			content["m.relates_to"] = map[string]any{"rel_type": "m.thread", "event_id": "$thread"}
		}
		events = append(events, MatrixEvent{EventID: fmt.Sprintf("$message%d", i), RoomID: room, Sender: "@other:test.local", EventType: EventTypeRoomMessage, OriginServerTS: int64(i), Content: content})
		events = append(events, MatrixEvent{EventID: fmt.Sprintf("$state%d", i), RoomID: room, Sender: "@other:test.local", EventType: EventTypeRoomTopic, OriginServerTS: int64(i + 1000), Content: map[string]any{"topic": "topic"}})
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).CreateInBatches(&events, 50).Error)
	receipts := []MatrixReceipt{{RoomID: room, UserID: viewer, EventID: "$message900", ReceiptType: "m.read", TS: 900}}
	for i := 0; i < 16; i++ {
		receipts = append(receipts, MatrixReceipt{RoomID: room, UserID: viewer, EventID: "$message800", ReceiptType: "m.read", ThreadID: fmt.Sprintf("$receipt-scope%d", i), TS: 800})
	}
	require.NoError(t, conn.Create(&receipts).Error)
	service := &SyncService{}
	metrics.queries, metrics.rows = 0, 0
	all := service.getUnreadNotifications(room, viewer)
	require.Equal(t, int64(100), all.NotificationCount)
	require.Equal(t, int64(1), all.HighlightCount)
	t.Logf("all unread queries=%d returned_rows=%d", metrics.queries, metrics.rows)
	require.LessOrEqual(t, metrics.queries, 2)
	require.LessOrEqual(t, metrics.rows, int64(117))
	metrics.queries, metrics.rows = 0, 0
	main := service.getUnreadNotificationsMainTimeline(room, viewer)
	threads := service.getUnreadThreadNotifications(room, viewer)
	require.Equal(t, int64(90), main.NotificationCount)
	require.Equal(t, int64(0), main.HighlightCount)
	require.Equal(t, int64(10), threads["$thread"].NotificationCount)
	require.Equal(t, int64(1), threads["$thread"].HighlightCount)
	t.Logf("threaded unread queries=%d returned_rows=%d", metrics.queries, metrics.rows)
	require.LessOrEqual(t, metrics.queries, 4)
	metrics.queries, metrics.rows = 0, 0
	snapshot, err := readUnreadSnapshot(conn, room, viewer, true)
	require.NoError(t, err)
	require.Equal(t, main, snapshot.main)
	require.Equal(t, threads, snapshot.threads)
	require.LessOrEqual(t, metrics.queries, 2)
	require.LessOrEqual(t, metrics.rows, int64(117))

	// Main and thread receipts affect only their own scopes. Private receipts
	// do not silently become public read markers; reactions/self/state do not notify.
	require.NoError(t, conn.Create(&[]MatrixReceipt{
		{RoomID: room, UserID: viewer, EventID: "$message950", ReceiptType: "m.read", ThreadID: "main", TS: 950},
		{RoomID: room, UserID: viewer, EventID: "$message980", ReceiptType: "m.read", ThreadID: "$thread", TS: 980},
	}).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&[]MatrixEvent{
		{EventID: "$encrypted", RoomID: room, Sender: "@other:test.local", EventType: "m.room.encrypted", OriginServerTS: 1001, Content: map[string]any{"ciphertext": "opaque"}},
		{EventID: "$annotation", RoomID: room, Sender: "@other:test.local", EventType: EventTypeRoomMessage, OriginServerTS: 1002, Content: map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation"}}},
		{EventID: "$self", RoomID: room, Sender: viewer, EventType: EventTypeRoomMessage, OriginServerTS: 1003, Content: map[string]any{"body": "self"}},
	}).Error)
	all = service.getUnreadNotifications(room, viewer)
	require.Equal(t, int64(48), all.NotificationCount)
	require.Equal(t, int64(1), all.HighlightCount)
	snapshot, err = readUnreadSnapshot(conn, room, viewer, true)
	require.NoError(t, err)
	require.Equal(t, int64(46), snapshot.main.NotificationCount)
	require.Equal(t, int64(2), snapshot.threads["$thread"].NotificationCount)
	require.NoError(t, conn.Create(&MatrixReceipt{RoomID: room, UserID: viewer, EventID: "$message1000", ReceiptType: "m.read.private", TS: 1000}).Error)
	require.Equal(t, int64(1), service.getUnreadNotifications(room, viewer).NotificationCount)
	snapshot, err = readUnreadSnapshot(conn, room, viewer, true)
	require.NoError(t, err)
	require.Equal(t, int64(1), snapshot.main.NotificationCount)
	require.Empty(t, snapshot.threads)
	require.Contains(t, service.getReceiptsForUser(room, viewer, 0)["$message1000"], "m.read.private")
	otherReceipts := service.getReceiptsForUser(room, "@other:test.local", 0)
	require.NotContains(t, otherReceipts, "$message1000")
	require.Contains(t, otherReceipts, "$message950", "public receipts must still be shared")
	// Even a direct caller cannot accidentally federate a private receipt. A nil
	// HomeServer would panic if the function proceeded beyond the private guard.
	(&SyncService{}).federateReceipt(room, "$message1000", viewer, "m.read.private", "", 1000)

}

func TestSyncEmptyRoomReadAmplification(t *testing.T) {
	hs := createTestHomeServer(t)
	roomID := createRoomWithUser(t, hs, "@empty-sync:test.local")
	conn := database.GetDB()
	metrics := &syncReadMetrics{Interface: logger.Discard}
	previous := conn.Logger
	conn.Logger = metrics
	t.Cleanup(func() { conn.Logger = previous })
	room := hs.Sync.buildJoinedRoomForDevice(roomID, "@empty-sync:test.local", "DEVICE", time.Now().Add(time.Second).UnixMilli(), false, nil)
	require.NotNil(t, room)
	require.Empty(t, room.Timeline.Events)
	require.Empty(t, room.State.Events)
	t.Logf("empty room queries=%d returned_rows=%d", metrics.queries, metrics.rows)
	require.LessOrEqual(t, metrics.queries, 7)
	require.Nil(t, room.UnreadNotifications, "unused statistics must not be calculated")
	require.Nil(t, room.Summary)
}

func TestSyncRequestCancellationAndAccountDataOnlyChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, matrixErr := (&SyncService{}).SyncContext(ctx, "@cancel:test.local", "D", &SyncRequest{})
	require.Nil(t, response)
	require.NotNil(t, matrixErr)
	require.Contains(t, matrixErr.Error, context.Canceled.Error())
	hs := createTestHomeServer(t)
	const user = "@account-data-sync:test.local"
	roomID := createRoomWithUser(t, hs, user)
	conn := database.GetDB()
	since := time.Now().Add(time.Second).UnixMilli()
	changed := time.UnixMilli(since + 10)
	require.NoError(t, conn.Create(&MatrixAccountData{UserID: user, RoomID: &roomID, DataType: "m.tag", Content: map[string]any{"tags": map[string]any{"favourite": map[string]any{}}}, CreatedAt: &changed, UpdatedAt: &changed}).Error)
	room := hs.Sync.buildJoinedRoomForDevice(roomID, user, "D", since, false, nil)
	require.True(t, joinedRoomHasChanges(roomID, room, since))
	require.Len(t, room.AccountData.Events, 1)
	require.NotNil(t, room.UnreadNotifications)
	unchanged := hs.Sync.buildJoinedRoomForDevice(roomID, user, "D", since+20, false, nil)
	require.False(t, joinedRoomHasChanges(roomID, unchanged, since+20))
	metrics := &syncReadMetrics{Interface: logger.Discard}
	previous := conn.Logger
	conn.Logger = metrics
	t.Cleanup(func() { conn.Logger = previous })
	requestService := *hs.Sync
	requestService.joinTransitions = make(map[syncJoinKey]bool)
	require.False(t, requestService.userJoinedAfter(roomID, user, since))
	queries := metrics.queries
	require.Equal(t, 2, queries)
	require.False(t, requestService.userJoinedAfter(roomID, user, since))
	require.Equal(t, queries, metrics.queries, "device lists must reuse the same request's join transition check")
	_, err := readUnreadSnapshot(conn.WithContext(ctx), roomID, user, false)
	require.ErrorIs(t, err, context.Canceled)
}

func TestPrivateReceiptNotifiesOnlyOwnDevices(t *testing.T) {
	hs := createTestHomeServer(t)
	const user = "@private-read:test.local"
	const other = "@other-private-read:test.local"
	roomID := createRoomWithUser(t, hs, user)
	conn := database.GetDB()
	require.NoError(t, conn.AutoMigrate(&MatrixUserSession{}))
	require.NoError(t, conn.Create(&[]MatrixUserSession{
		{UserID: user, DeviceID: "FIRST", AccessToken: "private-receipt-fixture-first"},
		{UserID: user, DeviceID: "SECOND", AccessToken: "private-receipt-fixture-second"},
		{UserID: other, DeviceID: "OTHER", AccessToken: "private-receipt-fixture-other"},
	}).Error)
	hs.SyncNotifier = NewSyncNotifier()
	first, cleanFirst := hs.SyncNotifier.Subscribe(user, "FIRST")
	defer cleanFirst()
	second, cleanSecond := hs.SyncNotifier.Subscribe(user, "SECOND")
	defer cleanSecond()
	others, cleanOther := hs.SyncNotifier.Subscribe(other, "OTHER")
	defer cleanOther()
	require.Nil(t, hs.Sync.SetReceipt(roomID, "$private-position", user, "m.read.private"))
	for _, ch := range []<-chan struct{}{first, second} {
		select {
		case <-ch:
		default:
			t.Fatal("private receipt must notify each own device")
		}
	}
	select {
	case <-others:
		t.Fatal("private receipt notified another user")
	default:
	}
	require.Contains(t, hs.Sync.getReceiptsForUser(roomID, user, 0), "$private-position")
	require.NotContains(t, hs.Sync.getReceiptsForUser(roomID, other, 0), "$private-position")
}
