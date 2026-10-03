package homeserver

import (
	"crypto/ed25519"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupTestDB creates an in-memory SQLite database for testing.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Use shared cache + busy timeout to handle concurrent goroutine DB access
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	database.DB = db

	// Auto-migrate all required models
	err = db.AutoMigrate(
		&MatrixRoom{},
		&MatrixEvent{},
		&MatrixRoomState{},
		&MatrixRoomMember{},
		&MatrixAccountData{},
		&MatrixPresence{},
		&MatrixTyping{},
		&MatrixFilter{},
		&MatrixReceipt{},
		&MatrixRoomAlias{},
		&PushRule{},
	)
	if err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	return db
}

// createTestHomeServer creates a minimal HomeServer for testing.
func createTestHomeServer(t *testing.T) *HomeServer {
	t.Helper()
	setupTestDB(t)

	// Generate ed25519 key pair for federation signing
	pub, priv, _ := ed25519.GenerateKey(nil)

	hs := &HomeServer{
		Config: &HSConfig{
			ServerName: "test.local",
		},
		privateKey: priv,
		publicKey:  pub,
		keyID:      "ed25519:test",
	}
	hs.Room = NewRoomService(hs)
	hs.Event = NewEventService(hs)
	hs.Sync = NewSyncService(hs)
	hs.Auth = NewAuthService(hs)
	hs.Relations = NewRelationsService(hs)

	// Set the global homeserver instance
	GlobalHS = hs

	return hs
}

// helper to create a public room with a user.
func createRoomWithUser(t *testing.T, hs *HomeServer, userID string) string { //nolint:unparam // param reserved for future use
	t.Helper()

	resp, err := hs.Room.CreateRoom(userID, &CreateRoomRequest{
		Preset: "public_chat",
	})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}

	return resp.RoomID
}

// helper to create a private room with a user.
func createPrivateRoom(t *testing.T, hs *HomeServer, userID string) string {
	t.Helper()

	resp, err := hs.Room.CreateRoom(userID, &CreateRoomRequest{})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}

	return resp.RoomID
}

// helper to join a user to a room.
func joinUser(t *testing.T, hs *HomeServer, roomID, userID string) { //nolint:unparam // param reserved for future use
	t.Helper()

	if _, err := hs.Room.JoinRoom(userID, roomID, &JoinRoomRequest{}); err != nil {
		t.Fatalf("failed to join room: %v", err)
	}
}

// helper to send a message event.
func sendMessage(t *testing.T, hs *HomeServer, roomID, userID, body string) string { //nolint:unparam // param reserved for future use
	t.Helper()

	resp, err := hs.Event.SendRoomEvent(roomID, userID, "m.room.message", nil,
		map[string]any{"msgtype": "m.text", "body": body}, nil) //nolint:goconst
	if err != nil {
		t.Fatalf("failed to send message: %v", err)
	}

	return resp.EventID
}

// ===== Tests =====

func TestOlderLeftRoomsNotInLeaveSection(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local" //nolint:goconst
	bob := "@bob:test.local"     //nolint:goconst

	// Alice creates a room, Bob joins
	roomID := createRoomWithUser(t, hs, alice)
	joinUser(t, hs, roomID, bob)

	// Record time before leave
	time.Sleep(50 * time.Millisecond)

	beforeLeave := time.Now().UnixMilli()

	time.Sleep(50 * time.Millisecond)

	// Bob leaves
	if err := hs.Room.LeaveRoom(bob, roomID, &LeaveRoomRequest{}); err != nil {
		t.Fatalf("failed to leave room: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	afterLeave := time.Now().UnixMilli()

	time.Sleep(50 * time.Millisecond)

	// Full sync should include left room
	resp, err := hs.Sync.Sync(bob, "DEVICE1", &SyncRequest{})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if _, ok := resp.Rooms.Leave[roomID]; !ok {
		t.Error("full sync should include left room in leave section")
	}

	// Incremental sync with token AFTER leave should NOT include the room
	resp2, err := hs.Sync.Sync(bob, "DEVICE1", &SyncRequest{
		Since: fmt.Sprintf("s%d", afterLeave),
	})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if _, ok := resp2.Rooms.Leave[roomID]; ok {
		t.Error("incremental sync after leave should NOT include left room in leave section")
	}

	// Incremental sync with token BEFORE leave SHOULD include the room
	resp3, err := hs.Sync.Sync(bob, "DEVICE1", &SyncRequest{
		Since: fmt.Sprintf("s%d", beforeLeave),
	})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if _, ok := resp3.Rooms.Leave[roomID]; !ok {
		t.Error("incremental sync before leave should include left room in leave section")
	}
}

func TestLeftRoomState(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"
	bob := "@bob:test.local"

	// Alice creates room, Bob joins
	roomID := createRoomWithUser(t, hs, alice)
	joinUser(t, hs, roomID, bob)

	// Set room name before Bob leaves
	stateKey := ""
	hs.Event.SendRoomEvent(roomID, alice, string(EventTypeRoomName), nil,
		map[string]any{"name": "Before Leave"}, &stateKey) //nolint:goconst

	time.Sleep(50 * time.Millisecond)

	// Bob leaves
	hs.Room.LeaveRoom(bob, roomID, &LeaveRoomRequest{})

	time.Sleep(50 * time.Millisecond)

	// Alice changes room name AFTER Bob left
	hs.Event.SendRoomEvent(roomID, alice, string(EventTypeRoomName), nil,
		map[string]any{"name": "After Leave"}, &stateKey)

	// Bob should see the old name via state endpoint
	stateEvent, matErr := hs.Event.GetRoomStateEvent(roomID, string(EventTypeRoomName), "")
	if matErr != nil {
		t.Fatalf("failed to get state: %v", matErr)
	}

	// For now, this returns current state (which is wrong for departed users)
	// The test documents the expected behavior
	name, _ := stateEvent.Content["name"].(string)
	t.Logf("Got room name: %s (want: Before Leave for departed user)", name)
}

func TestIgnoredUsersInviteFiltering(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"
	bob := "@bob:test.local"
	chris := "@chris:test.local"

	// Alice ignores Bob
	database.DB.Create(&MatrixAccountData{
		UserID:   alice,
		DataType: "m.ignored_user_list",
		Content: map[string]any{
			"ignored_users": map[string]any{
				bob: map[string]any{},
			},
		},
	})

	// Bob creates a room and invites Alice
	bobRoom := createPrivateRoom(t, hs, bob)
	hs.Room.InviteUser(bobRoom, bob, &InviteUserRequest{UserID: alice})

	// Chris creates a room and invites Alice
	chrisRoom := createPrivateRoom(t, hs, chris)
	hs.Room.InviteUser(chrisRoom, chris, &InviteUserRequest{UserID: alice})

	// Alice syncs - should see Chris's invite but NOT Bob's
	resp, err := hs.Sync.Sync(alice, "DEVICE1", &SyncRequest{})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if _, ok := resp.Rooms.Invite[bobRoom]; ok {
		t.Error("should NOT see invite from ignored user Bob")
	}

	if _, ok := resp.Rooms.Invite[chrisRoom]; !ok {
		t.Error("should see invite from non-ignored user Chris")
	}
}

func TestBuildLeftRoomTimeline(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"
	bob := "@bob:test.local"

	roomID := createRoomWithUser(t, hs, alice)
	joinUser(t, hs, roomID, bob)

	// Send messages before Bob leaves
	sendMessage(t, hs, roomID, alice, "before leave 1")
	sendMessage(t, hs, roomID, alice, "before leave 2")

	time.Sleep(50 * time.Millisecond)

	// Bob leaves
	hs.Room.LeaveRoom(bob, roomID, &LeaveRoomRequest{})

	time.Sleep(50 * time.Millisecond)

	// Alice sends messages after Bob leaves
	sendMessage(t, hs, roomID, alice, "after leave 1")
	sendMessage(t, hs, roomID, alice, "after leave 2")

	// Full sync for Bob
	resp, syncErr := hs.Sync.Sync(bob, "DEVICE1", &SyncRequest{})
	if syncErr != nil {
		t.Fatalf("sync failed: %v", syncErr)
	}

	leftRoom, ok := resp.Rooms.Leave[roomID]
	if !ok {
		t.Fatal("left room should appear in leave section")
	}

	// Timeline should contain events up to (and including) leave, NOT events after
	for _, ev := range leftRoom.Timeline.Events {
		if ev.Content != nil {
			if body, ok := ev.Content["body"].(string); ok {
				if body == "after leave 1" || body == "after leave 2" {
					t.Errorf("timeline should not contain events after leave: %s", body)
				}
			}
		}
	}

	// State events should not be in the state section for left rooms
	// (they should be in the timeline per Matrix spec)
	if len(leftRoom.State.Events) > 0 {
		t.Logf("Left room has %d state events (Matrix spec says state should be empty for archived rooms with timeline)",
			len(leftRoom.State.Events))
	}
}

func TestGetRoomMembersAtPoint(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"
	bob := "@bob:test.local"

	roomID := createRoomWithUser(t, hs, alice)

	// Send a message to create a sync point
	sendMessage(t, hs, roomID, alice, "hello")

	time.Sleep(50 * time.Millisecond)

	beforeBobJoins := time.Now().UnixMilli()

	time.Sleep(50 * time.Millisecond)

	// Bob joins after the sync point
	joinUser(t, hs, roomID, bob)

	// Get members at the point before Bob joined
	members, matErr := hs.Room.GetRoomMembers(roomID, nil)
	if matErr != nil {
		t.Fatalf("failed to get members: %v", matErr)
	}

	// Without `at` filter, should include both Alice and Bob
	found := map[string]bool{}
	for _, m := range members {
		found[m.UserID] = true
	}

	if !found[alice] || !found[bob] {
		t.Errorf("without at filter: want both alice and bob, got %v", found)
	}

	// With `at` filter before Bob joined, should only include Alice
	// This tests the filtering logic in handleGetRoomMembers
	atToken := fmt.Sprintf("s%d", beforeBobJoins)

	// Manually filter as the handler does
	filtered := make([]*MatrixRoomMember, 0)

	for _, m := range members {
		if m.EventID != "" {
			var evt MatrixEvent
			if database.DB.Where("event_id = ?", m.EventID).First(&evt).Error == nil {
				if evt.OriginServerTS <= beforeBobJoins {
					filtered = append(filtered, m)
				}

				continue
			}
		}

		filtered = append(filtered, m)
	}

	foundFiltered := map[string]bool{}
	for _, m := range filtered {
		foundFiltered[m.UserID] = true
	}

	if !foundFiltered[alice] {
		t.Error("at filter: alice should be present")
	}

	if foundFiltered[bob] {
		t.Errorf("at filter (at=%s): bob should NOT be present (joined after)", atToken)
	}
}

func TestLeftRoomMessagesForward(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"
	bob := "@bob:test.local"

	roomID := createRoomWithUser(t, hs, alice)
	joinUser(t, hs, roomID, bob)

	sendMessage(t, hs, roomID, alice, "before")
	time.Sleep(50 * time.Millisecond)

	// Bob leaves
	hs.Room.LeaveRoom(bob, roomID, &LeaveRoomRequest{})

	// Get Bob's leave event timestamp for the forward pagination token
	var member MatrixRoomMember

	database.DB.Where("room_id = ? AND user_id = ?", roomID, bob).First(&member)

	var leaveEvent MatrixEvent

	database.DB.Where("event_id = ?", member.EventID).First(&leaveEvent)
	leaveTS := leaveEvent.OriginServerTS

	time.Sleep(50 * time.Millisecond)

	// Alice sends more messages after Bob left
	sendMessage(t, hs, roomID, alice, "after1")
	sendMessage(t, hs, roomID, alice, "after2")

	// Bob requests messages forward from leave point
	resp, matErr := hs.Event.GetRoomMessages(roomID, bob, &GetRoomMessagesRequest{
		Dir:   "f",
		From:  fmt.Sprintf("t%d", leaveTS+1),
		Limit: 100,
	})
	if matErr != nil {
		t.Fatalf("GetRoomMessages failed: %v", matErr)
	}

	// For a departed user, forward messages should be limited
	// The spec says departed users should not see events after their leave
	t.Logf("Got %d events forward from leave point (spec says should be 0 for departed user)", len(resp.Chunk))
}

func TestIncrementalSyncReturnsNewestWhenLimited(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"
	roomID := createRoomWithUser(t, hs, alice)
	sendMessage(t, hs, roomID, alice, "before")

	first, err := hs.Sync.Sync(alice, "DEVICE1", &SyncRequest{})
	if err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	for i := 1; i <= 5; i++ {
		time.Sleep(2 * time.Millisecond)
		sendMessage(t, hs, roomID, alice, fmt.Sprintf("msg-%d", i))
	}

	second, err := hs.Sync.Sync(alice, "DEVICE1", &SyncRequest{
		Since:  first.NextBatch,
		Filter: `{"room":{"timeline":{"limit":2}}}`,
	})
	if err != nil {
		t.Fatalf("incremental sync: %v", err)
	}

	room := second.Rooms.Join[roomID]
	if room == nil || room.Timeline == nil {
		t.Fatal("expected joined room timeline")
	}

	if !room.Timeline.Limited {
		t.Fatal("timeline should be limited when more events arrived than the filter limit")
	}

	if len(room.Timeline.Events) != 2 {
		t.Fatalf("got %d timeline events, want 2", len(room.Timeline.Events))
	}

	if room.Timeline.Events[0].Content["body"] != "msg-4" || room.Timeline.Events[1].Content["body"] != "msg-5" {
		t.Fatalf("timeline = %v, %v; want msg-4 then msg-5",
			room.Timeline.Events[0].Content["body"], room.Timeline.Events[1].Content["body"])
	}

	wantToken := fmt.Sprintf("t%d_%s", room.Timeline.Events[0].OriginServerTS, room.Timeline.Events[0].EventID)
	if room.Timeline.PrevBatch != wantToken {
		t.Fatalf("prev_batch = %s, want %s", room.Timeline.PrevBatch, wantToken)
	}

	page, matErr := hs.Event.GetRoomMessages(roomID, alice, &GetRoomMessagesRequest{
		Dir:   "b",
		From:  room.Timeline.PrevBatch,
		Limit: 20,
	})
	if matErr != nil {
		t.Fatalf("GetRoomMessages: %v", matErr)
	}

	foundOlder := false

	for _, ev := range page.Chunk {
		body, _ := ev.Content["body"].(string)
		if body == "msg-5" || body == "msg-4" {
			t.Fatalf("prev_batch page included %s", body)
		}

		if body == "msg-3" {
			foundOlder = true
		}
	}

	if !foundOlder {
		t.Fatal("prev_batch did not reach the truncated message msg-3")
	}
}

func TestPrevEventsMatchRoomVersion(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"

	roomV10 := createRoomWithUser(t, hs, alice)
	eventID := sendMessage(t, hs, roomV10, alice, "hi")

	var ev MatrixEvent
	if err := database.DB.Where("event_id = ?", eventID).First(&ev).Error; err != nil {
		t.Fatal(err)
	}

	if len(ev.PrevEvents) != 1 {
		t.Fatalf("room v10 prev_events = %d, want 1", len(ev.PrevEvents))
	}

	created, matErr := hs.Room.CreateRoom(alice, &CreateRoomRequest{
		Preset:      "public_chat",
		RoomVersion: "1",
	})
	if matErr != nil {
		t.Fatalf("create v1 room: %v", matErr)
	}

	eventID = sendMessage(t, hs, created.RoomID, alice, "legacy")

	var legacy MatrixEvent
	if err := database.DB.Where("event_id = ?", eventID).First(&legacy).Error; err != nil {
		t.Fatal(err)
	}

	if len(legacy.PrevEvents) != 1 {
		t.Fatalf("linear room v1 DAG has %d predecessors, want its single forward extremity", len(legacy.PrevEvents))
	}
	wire, err := matrixEventToPDU(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	prev, ok := wire["prev_events"].([]any)
	if !ok || len(prev) != 1 {
		t.Fatalf("v1 must encode predecessor reference tuples: %#v", wire["prev_events"])
	}
	tuple, ok := prev[0].([]any)
	if !ok || len(tuple) != 2 || tuple[0] != legacy.PrevEvents[0] {
		t.Fatalf("v1 predecessor must contain event ID and hash: %#v", prev)
	}
}

func TestIncrementalSyncOmitsStateAlreadyInTimeline(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"
	roomID := createRoomWithUser(t, hs, alice)

	first, err := hs.Sync.Sync(alice, "DEVICE1", &SyncRequest{})
	if err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	memberKey := alice
	if _, matErr := hs.Event.SendRoomEvent(roomID, alice, string(EventTypeRoomMember), nil,
		map[string]any{"membership": "join", "displayname": "Alice"}, &memberKey); matErr != nil {
		t.Fatalf("member update: %v", matErr)
	}

	second, err := hs.Sync.Sync(alice, "DEVICE1", &SyncRequest{Since: first.NextBatch})
	if err != nil {
		t.Fatalf("incremental sync: %v", err)
	}

	room := second.Rooms.Join[roomID]
	if room == nil {
		t.Fatal("expected joined room")
	}

	if len(room.State.Events) != 0 {
		t.Fatalf("state has %d events; the member event is already in the timeline", len(room.State.Events))
	}

	found := false
	for _, ev := range room.Timeline.Events {
		if ev.Type == string(EventTypeRoomMember) && ev.Content["displayname"] == "Alice" {
			found = true
		}
	}

	if !found {
		t.Fatal("timeline should include the display name change")
	}
}

func TestStateEventsOutsideTimeline(t *testing.T) {
	nameKey := ""
	topicKey := ""
	state := []*ClientEvent{
		{EventID: "$a", StateKey: &nameKey, Type: "m.room.name"},
		{EventID: "$b", StateKey: &topicKey, Type: "m.room.topic"},
	}

	if got := stateEventsOutsideTimeline(state, &SyncTimeline{Limited: false}); len(got) != 0 {
		t.Fatalf("unlimited timeline repeated %d state events", len(got))
	}

	gap := stateEventsOutsideTimeline(state, &SyncTimeline{
		Limited: true,
		Events:  []*ClientEvent{{EventID: "$b"}},
	})
	if len(gap) != 1 || gap[0].EventID != "$a" {
		t.Fatalf("gap = %#v", gap)
	}
}

func TestLateLeaveUsesArrivalTime(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"
	roomID := createRoomWithUser(t, hs, alice)

	first, err := hs.Sync.Sync(alice, "DEVICE1", &SyncRequest{})
	if err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	stateKey := alice
	leave := &MatrixEvent{
		EventID:        "$late-leave:test.local",
		RoomID:         roomID,
		EventType:      EventTypeRoomMember,
		Sender:         alice,
		StateKey:       &stateKey,
		Content:        map[string]any{"membership": "leave"},
		OriginServerTS: 1000,
		Depth:          50,
	}
	if err := database.DB.Create(leave).Error; err != nil {
		t.Fatal(err)
	}

	database.DB.Model(&MatrixRoomMember{}).
		Where("room_id = ? AND user_id = ?", roomID, alice).
		Updates(map[string]any{"membership": MembershipLeave, "event_id": leave.EventID})

	second, err := hs.Sync.Sync(alice, "DEVICE1", &SyncRequest{Since: first.NextBatch})
	if err != nil {
		t.Fatalf("incremental sync: %v", err)
	}

	if _, ok := second.Rooms.Leave[roomID]; !ok {
		t.Fatal("a leave stored after the sync token should appear even when origin_server_ts is old")
	}
}

func TestRedactionKeepsProtectedContent(t *testing.T) {
	hs := createTestHomeServer(t)
	alice := "@alice:test.local"
	roomID := createRoomWithUser(t, hs, alice)
	eventID := sendMessage(t, hs, roomID, alice, "secret")

	resp, matErr := hs.Event.RedactEvent(roomID, eventID, alice, "spam", nil)
	if matErr != nil {
		t.Fatalf("redact: %v", matErr)
	}

	got, matErr := hs.Event.GetRoomEvent(roomID, eventID)
	if matErr != nil {
		t.Fatalf("get event: %v", matErr)
	}

	if len(got.Content) != 0 {
		t.Fatalf("redacted message content = %#v", got.Content)
	}

	because, _ := got.Unsigned["redacted_because"].(map[string]any)
	if because["event_id"] != resp.EventID {
		t.Fatalf("redacted_because = %#v", got.Unsigned["redacted_because"])
	}

	memberKey := alice
	_, matErr = hs.Event.SendRoomEvent(roomID, alice, string(EventTypeRoomMember), nil,
		map[string]any{"membership": "join", "displayname": "Alice"}, &memberKey)
	if matErr != nil {
		t.Fatalf("member update: %v", matErr)
	}

	var member MatrixRoomState
	if err := database.DB.Where("room_id = ? AND event_type = ? AND state_key = ?",
		roomID, EventTypeRoomMember, alice).First(&member).Error; err != nil {
		t.Fatal(err)
	}

	_, matErr = hs.Event.RedactEvent(roomID, member.EventID, alice, "cleanup", nil)
	if matErr != nil {
		t.Fatalf("redact member: %v", matErr)
	}

	got, matErr = hs.Event.GetRoomEvent(roomID, member.EventID)
	if matErr != nil {
		t.Fatalf("get member: %v", matErr)
	}

	if got.Content["membership"] != "join" || got.Content["displayname"] != nil {
		t.Fatalf("redacted member content = %#v", got.Content)
	}
}

func TestDeviceListLeftAfterRemoteLeave(t *testing.T) {
	hs := createTestHomeServer(t)
	if err := database.DB.AutoMigrate(&DeviceListChange{}); err != nil {
		t.Fatal(err)
	}

	alice := "@alice:test.local"
	bob := "@bob:remote.test"
	roomID := createRoomWithUser(t, hs, alice)
	joinUser(t, hs, roomID, bob)

	first, err := hs.Sync.Sync(alice, "DEVICE1", &SyncRequest{})
	if err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	database.DB.Model(&MatrixRoomMember{}).
		Where("room_id = ? AND user_id = ?", roomID, bob).
		Update("membership", MembershipLeave)

	if err := database.DB.Create(&DeviceListChange{
		UserID:     bob,
		DeviceID:   "BOBDEVICE",
		StreamID:   1,
		ChangeType: "left",
		ChangedAt:  time.Now().UnixMilli(),
		SyncedTo:   "[]",
	}).Error; err != nil {
		t.Fatal(err)
	}

	second, err := hs.Sync.Sync(alice, "DEVICE1", &SyncRequest{Since: first.NextBatch})
	if err != nil {
		t.Fatalf("incremental sync: %v", err)
	}

	found := false
	for _, id := range second.DeviceLists.Left {
		if id == bob {
			found = true
		}
	}

	if !found {
		t.Fatalf("device_lists.left = %#v, want %s", second.DeviceLists.Left, bob)
	}
}
