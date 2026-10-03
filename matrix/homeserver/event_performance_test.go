package homeserver

import (
	"context"
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRoomStateAllQueryBudget(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	roomID := "!statebudget:test.local"
	for i := 0; i < 50; i++ {
		key := fmt.Sprint(i)
		eventID := "$state" + key
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&MatrixEvent{EventID: eventID, RoomID: roomID, EventType: "test.state", StateKey: &key, Sender: "@alice:test.local", OriginServerTS: int64(i + 1), Content: map[string]any{"value": i}}).Error)
		require.NoError(t, conn.Create(&MatrixRoomState{RoomID: roomID, EventType: "test.state", StateKey: key, EventID: eventID, Content: map[string]any{"value": i}}).Error)
	}
	queries := 0
	require.NoError(t, conn.Callback().Query().After("gorm:query").Register("test:state_count", func(*gorm.DB) { queries++ }))
	events, matrixErr := hs.Room.GetRoomStateAll(roomID)
	require.Nil(t, matrixErr)
	require.Len(t, events, 50)
	for _, event := range events {
		require.Equal(t, "@alice:test.local", event.Sender)
		require.NotZero(t, event.OriginServerTS)
	}
	t.Logf("state-all 50 entries SQL queries=%d", queries)
	require.Equal(t, 2, queries)
}

func TestJoinedHistoryMessagesRegression(t *testing.T) {
	hs := createTestHomeServer(t)
	alice, bob := "@historyalice:test.local", "@historybob:test.local"
	roomID := createRoomWithUser(t, hs, alice)
	empty := ""
	_, matrixErr := hs.Event.SendRoomEvent(roomID, alice, "m.room.history_visibility", nil, map[string]any{"history_visibility": "joined"}, &empty)
	require.Nil(t, matrixErr)
	message, matrixErr := hs.Event.SendRoomEvent(roomID, alice, "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "before bob joined"}, nil)
	require.Nil(t, matrixErr)
	_, matrixErr = hs.Room.JoinRoom(bob, roomID, &JoinRoomRequest{})
	require.Nil(t, matrixErr)
	response, matrixErr := hs.Event.GetRoomMessages(roomID, bob, &GetRoomMessagesRequest{Dir: "b", Limit: 100, Filter: `{"lazy_load_members":true}`})
	require.Nil(t, matrixErr)
	leaked := false
	for _, event := range response.Chunk {
		if event.EventID == message.EventID {
			leaked = true
		}
	}
	t.Logf("joined-history prejoin message exposed=%v", leaked)
	require.False(t, leaked)
	require.False(t, canReadClientEvent(roomID, bob, message.EventID))
	_, contextErr := hs.Event.GetRoomContext(roomID, message.EventID, bob, 10)
	require.Equal(t, ErrNotFound, contextErr)
}

func TestMessagesBatchLazyMembersAndToCursor(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := db.GetDB()
	roomID, userID := "!pagebudget:test.local", "@reader:test.local"
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: roomID, UserID: userID, Membership: MembershipJoin}).Error)
	require.NoError(t, conn.Create(&MatrixRoom{RoomID: roomID, RoomVersion: "10"}).Error)
	write := conn.Session(&gorm.Session{SkipHooks: true})
	for i := 0; i < 50; i++ {
		sender := fmt.Sprintf("@sender%d:test.local", i)
		require.NoError(t, write.Create(&MatrixEvent{EventID: fmt.Sprintf("$member%02d", i), RoomID: roomID, EventType: EventTypeRoomMember, StateKey: &sender, Sender: sender, OriginServerTS: 1, StateSnapshotKnown: true, Content: map[string]any{"membership": "join"}}).Error)
		require.NoError(t, write.Create(&MatrixEvent{EventID: fmt.Sprintf("$message%02d", i), RoomID: roomID, EventType: EventTypeRoomMessage, Sender: sender, OriginServerTS: 100, StateSnapshotKnown: true, Content: map[string]any{"body": i}}).Error)
	}
	queries := 0
	require.NoError(t, conn.Callback().Query().After("gorm:query").Register("test:message_count", func(*gorm.DB) { queries++ }))
	require.NoError(t, conn.Callback().Row().After("gorm:row").Register("test:message_row_count", func(*gorm.DB) { queries++ }))
	response, matrixErr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 50, Filter: `{"lazy_load_members":true}`})
	require.Nil(t, matrixErr)
	require.Len(t, response.Chunk, 50)
	require.Len(t, response.State, 50)
	require.LessOrEqual(t, queries, 6, "lazy loading must not issue one query per sender")
	t.Logf("50 senders lazy membership SQL queries=%d", queries)
	from := hs.Event.generatePaginationTokenForEvent(100, "$message40")
	to := hs.Event.generatePaginationTokenForEvent(100, "$message35")
	page, matrixErr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", From: from, To: to, Limit: 10, Filter: `{"lazy_load_members":false}`})
	require.Nil(t, matrixErr)
	require.Len(t, page.Chunk, 4)
	require.Equal(t, "$message39", page.Chunk[0].EventID)
	require.Equal(t, "$message36", page.Chunk[3].EventID)
	forward, matrixErr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "f", From: to, To: from, Limit: 10})
	require.Nil(t, matrixErr)
	require.Len(t, forward.Chunk, 4)
	require.Equal(t, "$message36", forward.Chunk[0].EventID)
	for _, filter := range []string{`{"types":["m.room.message"]}`, `{"types":["*"],"not_types":["m.room.member"]}`} {
		from := ""
		seen := map[string]bool{}
		for pageNumber := 0; pageNumber < 3; pageNumber++ {
			page, err := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 20, From: from, Filter: filter})
			require.Nil(t, err)
			if pageNumber < 2 {
				require.Len(t, page.Chunk, 20)
			} else {
				require.Len(t, page.Chunk, 10)
			}
			for _, event := range page.Chunk {
				require.Equal(t, string(EventTypeRoomMessage), event.Type)
				require.False(t, seen[event.EventID])
				seen[event.EventID] = true
			}
			from = page.End
		}
		require.Len(t, seen, 50)
	}
	emptyPage, emptyErr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 20, Filter: `{"not_types":["*"]}`})
	require.Nil(t, emptyErr)
	require.Empty(t, emptyPage.Chunk)

}

func TestClientHistoricalVisibilityPolicies(t *testing.T) {
	hs := createTestHomeServer(t)
	_ = hs
	conn := db.GetDB()
	roomID, userID := "!policies:test.local", "@viewer:test.local"
	write := conn.Session(&gorm.Session{SkipHooks: true})
	empty := ""
	for _, visibility := range []string{"shared", "joined", "invited", "world_readable"} {
		id := "$visibility-" + visibility
		require.NoError(t, write.Create(&MatrixEvent{EventID: id, RoomID: roomID, EventType: EventTypeRoomHistoryVisibility, StateKey: &empty, Content: map[string]any{"history_visibility": visibility}}).Error)
	}
	for _, membership := range []string{"join", "invite", "leave"} {
		require.NoError(t, write.Create(&MatrixEvent{EventID: "$membership-" + membership, RoomID: roomID, EventType: EventTypeRoomMember, StateKey: &userID, Content: map[string]any{"membership": membership}}).Error)
	}
	cases := []struct {
		visibility, membership string
		current                MembershipState
		want                   bool
	}{
		{"joined", "leave", MembershipJoin, false}, {"joined", "join", MembershipLeave, true},
		{"invited", "invite", MembershipJoin, true}, {"invited", "leave", MembershipJoin, false},
		{"shared", "leave", MembershipInvite, false}, {"shared", "leave", MembershipJoin, true},
		{"shared", "leave", MembershipLeave, false}, {"world_readable", "leave", MembershipInvite, true},
	}
	for _, tc := range cases {
		r := &clientHistoryReader{conn: conn, roomID: roomID, userID: userID, member: &MatrixRoomMember{Membership: tc.current, EventID: "$membership-leave"}, state: map[string]MatrixEvent{}}
		events, err := r.filter([]MatrixEvent{{EventID: "$target", RoomID: roomID, StateSnapshotKnown: true, StateBefore: []string{"$visibility-" + tc.visibility, "$membership-" + tc.membership}}})
		require.NoError(t, err)
		require.Equal(t, tc.want, len(events) == 1, "%+v", tc)
	}
	r := &clientHistoryReader{conn: conn, roomID: roomID, userID: userID, member: &MatrixRoomMember{Membership: MembershipJoin, Forgotten: true}, state: map[string]MatrixEvent{}}
	events, err := r.filter([]MatrixEvent{{EventID: "$target", RoomID: roomID, StateSnapshotKnown: true, StateBefore: []string{"$visibility-world_readable"}}})
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestClientHistoryMissingAndCrossRoomStateFailClosed(t *testing.T) {
	hs := createTestHomeServer(t)
	_ = hs
	conn := db.GetDB()
	key := ""
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&MatrixEvent{EventID: "$foreign-history", RoomID: "!foreign:test.local", EventType: EventTypeRoomHistoryVisibility, StateKey: &key, Content: map[string]any{"history_visibility": "joined"}}).Error)
	for _, reference := range []string{"$missing-history", "$foreign-history"} {
		reader := &clientHistoryReader{conn: conn, roomID: "!local:test.local", userID: "@viewer:test.local", member: &MatrixRoomMember{Membership: MembershipJoin}, state: map[string]MatrixEvent{}}
		events, err := reader.filter([]MatrixEvent{{EventID: "$target", RoomID: "!local:test.local", StateSnapshotKnown: true, StateBefore: []string{reference}}})
		require.NoError(t, err)
		require.Empty(t, events)
	}
}

func TestBatchClientEventsPreservesRedactionAndDeviceMetadata(t *testing.T) {
	hs := createTestHomeServer(t)
	_ = hs
	conn := db.GetDB()
	write := conn.Session(&gorm.Session{SkipHooks: true})
	events := []MatrixEvent{}
	txn := "txn-123"
	for i := 0; i < 20; i++ {
		event := MatrixEvent{EventID: fmt.Sprintf("$batch%d", i), RoomID: "!batch:test.local", EventType: EventTypeRoomMessage, Sender: "@alice:test.local", OriginServerTS: 100, TxnID: &txn, SenderDeviceID: "A", Content: map[string]any{"body": "text"}}
		require.NoError(t, write.Create(&event).Error)
		events = append(events, event)
	}
	target := events[0].EventID
	for _, redaction := range []MatrixEvent{{EventID: "$older", RoomID: events[0].RoomID, EventType: EventTypeRoomRedaction, Redacts: &target, OriginServerTS: 101, Content: map[string]any{"reason": "old"}}, {EventID: "$newer", RoomID: events[0].RoomID, EventType: EventTypeRoomRedaction, Redacts: &target, OriginServerTS: 102, Content: map[string]any{"reason": "new"}}, {EventID: "$wrongroom", RoomID: "!other:test.local", EventType: EventTypeRoomRedaction, Redacts: &target, OriginServerTS: 999}} {
		require.NoError(t, write.Create(&redaction).Error)
	}
	for _, device := range []string{"A", "B"} {
		want := []*ClientEvent{}
		for _, event := range events {
			want = append(want, event.ToClientEventForDevice(device))
		}
		queries := 0
		name := "test:batch_count" + device
		require.NoError(t, conn.Callback().Row().After("gorm:row").Register(name, func(*gorm.DB) { queries++ }))
		got, err := batchClientEvents(conn, events, device)
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.Equal(t, 1, queries)
		require.NoError(t, conn.Callback().Row().Remove(name))
	}
}

func TestDeviceTransactionRetryAvoidsTimelineQueries(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@retry:test.local"
	roomID := createRoomWithUser(t, hs, alice)
	txn := "same-transaction"
	content := map[string]any{"msgtype": "m.text", "body": "first"}
	first, matrixErr := hs.Event.SendRoomEventFromDevice(roomID, alice, "DEVICE_A", string(EventTypeRoomMessage), &txn, content, nil)
	require.Nil(t, matrixErr)
	var previousQueries atomic.Int32
	conn := db.GetDB()
	require.NoError(t, conn.Callback().Query().After("gorm:query").Register("test:retry_prev", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "depth DESC, origin_server_ts DESC") {
			previousQueries.Add(1)
		}
	}))
	retry, matrixErr := hs.Event.SendRoomEventFromDevice(roomID, alice, "DEVICE_A", string(EventTypeRoomMessage), &txn, map[string]any{"msgtype": "m.text", "body": "retry changed"}, nil)
	require.Nil(t, matrixErr)
	require.Equal(t, first.EventID, retry.EventID)
	require.Zero(t, previousQueries.Load())
	other, matrixErr := hs.Event.SendRoomEventFromDevice(roomID, alice, "DEVICE_B", string(EventTypeRoomMessage), &txn, content, nil)
	require.Nil(t, matrixErr)
	require.NotEqual(t, first.EventID, other.EventID)
	var stored MatrixEvent
	require.NoError(t, conn.Where("event_id = ?", first.EventID).First(&stored).Error)
	require.Equal(t, "first", stored.Content["body"])
	require.Equal(t, txn, *stored.TxnID)
	require.Equal(t, "DEVICE_A", stored.SenderDeviceID)
}

func TestHistoryQueriesRespectCancellation(t *testing.T) {
	hs := createTestHomeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, matrixErr := hs.Event.GetRoomMessages("!cancel:test.local", "@reader:test.local", &GetRoomMessagesRequest{Context: ctx, Dir: "b", Limit: 20})
	require.Equal(t, ErrInternal, matrixErr)
	_, matrixErr = hs.Room.GetRoomStateAllContext(ctx, "!cancel:test.local")
	require.Equal(t, ErrInternal, matrixErr)
	_, matrixErr = hs.Event.GetRoomContextContext(ctx, "!cancel:test.local", "$none", "@reader:test.local", 20)
	require.NotNil(t, matrixErr)
}

func TestHistoricalSnapshotBoundCheckedBeforeSQLAndUnionChunked(t *testing.T) {
	hs := createTestHomeServer(t)
	_ = hs
	conn := db.GetDB()
	roomID := "!snapshot-budget:test.local"
	reader := &clientHistoryReader{conn: conn, roomID: roomID, userID: "@viewer:test.local", member: &MatrixRoomMember{Membership: MembershipJoin}, state: map[string]MatrixEvent{}}
	queries, maxVars := 0, 0
	require.NoError(t, conn.Callback().Query().After("gorm:query").Register("test:snapshot_count", func(tx *gorm.DB) {
		queries++
		if len(tx.Statement.Vars) > maxVars {
			maxVars = len(tx.Statement.Vars)
		}
	}))
	excessive := make([]string, historicalStateMaxEvents+1)
	for i := range excessive {
		excessive[i] = fmt.Sprintf("$excess%d", i)
	}
	visible, err := reader.filter([]MatrixEvent{{EventID: "$huge", RoomID: roomID, StateSnapshotKnown: true, StateBefore: excessive}})
	require.NoError(t, err)
	require.Empty(t, visible)
	require.Zero(t, queries)
	rows := make([]MatrixEvent, 1001)
	ids := make([]string, len(rows))
	for i := range rows {
		key := fmt.Sprint(i)
		ids[i] = "$budget" + key
		rows[i] = MatrixEvent{EventID: ids[i], RoomID: roomID, EventType: "test.state", StateKey: &key}
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).CreateInBatches(&rows, 100).Error)
	queries = 0
	maxVars = 0
	visible, err = reader.filter([]MatrixEvent{{EventID: "$bounded", RoomID: roomID, StateSnapshotKnown: true, StateBefore: ids}})
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.Equal(t, 3, queries)
	require.LessOrEqual(t, maxVars, 501)
}

func TestOutsiderMessagesDenyPrivateWithoutHiddenCursor(t *testing.T) {
	hs := createTestHomeServer(t)
	roomID := createRoomWithUser(t, hs, "@private-owner:test.local")
	_, matrixErr := hs.Event.SendRoomEvent(roomID, "@private-owner:test.local", string(EventTypeRoomMessage), nil, map[string]any{"msgtype": "m.text", "body": "secret"}, nil)
	require.Nil(t, matrixErr)
	response, matrixErr := hs.Event.GetRoomMessages(roomID, "@outsider:test.local", &GetRoomMessagesRequest{Dir: "b", Limit: 20})
	require.Equal(t, ErrForbidden, matrixErr)
	require.Nil(t, response)
	empty := ""
	_, matrixErr = hs.Event.SendRoomEvent(roomID, "@private-owner:test.local", string(EventTypeRoomHistoryVisibility), nil, map[string]any{"history_visibility": "world_readable"}, &empty)
	require.Nil(t, matrixErr)
	public, matrixErr := hs.Event.SendRoomEvent(roomID, "@private-owner:test.local", string(EventTypeRoomMessage), nil, map[string]any{"msgtype": "m.text", "body": "public"}, nil)
	require.Nil(t, matrixErr)
	response, matrixErr = hs.Event.GetRoomMessages(roomID, "@outsider:test.local", &GetRoomMessagesRequest{Dir: "b", Limit: 20})
	require.Nil(t, matrixErr)
	require.NotNil(t, response)
	found := false
	for _, event := range response.Chunk {
		if event.EventID == public.EventID {
			found = true
		}
		require.NotEqual(t, "secret", event.Content["body"])
	}
	require.True(t, found)
}
